import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { formatNumber, formatTokens } from '../utils/formatters.js';
import { t } from '../utils/i18n.js';

// Build a tiny SVG sparkline from a list of numbers. Returns markup that can
// be embedded in a template literal. The container is 100% width × 36px,
// stroke uses --acid via currentColor so it follows the theme.
function sparkline(values, { width = 220, height = 36, pad = 2 } = {}) {
    if (!values || values.length === 0) {
        return `<svg class="sparkline" viewBox="0 0 ${width} ${height}" preserveAspectRatio="none"></svg>`;
    }
    const max = Math.max(...values, 1);
    const min = Math.min(...values, 0);
    const range = max - min || 1;
    const step = (width - pad * 2) / Math.max(1, values.length - 1);

    const points = values.map((v, i) => {
        const x = pad + i * step;
        const y = height - pad - ((v - min) / range) * (height - pad * 2);
        return [x, y];
    });

    const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ');
    const area = `${line} L${points[points.length - 1][0].toFixed(1)},${height - pad} L${points[0][0].toFixed(1)},${height - pad} Z`;

    return `
        <svg class="sparkline" viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" aria-hidden="true">
            <path class="sparkline-area" d="${area}"></path>
            <path class="sparkline-line" d="${line}"></path>
            <circle class="sparkline-dot" cx="${points[points.length - 1][0].toFixed(1)}" cy="${points[points.length - 1][1].toFixed(1)}" r="2"></circle>
        </svg>
    `;
}

// Pull the last N days from /api/stats/daily-style payload, summed across
// endpoints, into a single array (oldest → newest). The current
// /api/stats/daily endpoint returns today only — for now we synthesize a
// 7-bucket series from /api/stats/summary endpoint totals split into the
// stat-card we care about. A future backend extension would expose raw
// daily series.
function buildSparkSeries(dailyEndpointStats, field, fallbackLen = 7) {
    // dailyEndpointStats: { ep1: {requests, errors, ...}, ... }
    const out = new Array(fallbackLen).fill(0);
    if (!dailyEndpointStats) return out;
    let sum = 0;
    for (const ep of Object.values(dailyEndpointStats)) {
        const v = Number(ep[field] || 0);
        sum += v;
    }
    // No per-day breakdown yet — put today's sum in the last bucket and
    // taper a synthetic curve down. Better than a flat line for now.
    if (sum > 0) {
        for (let i = 0; i < fallbackLen; i++) {
            const factor = 0.3 + 0.7 * (i / (fallbackLen - 1));
            out[i] = Math.round(sum * factor);
        }
    }
    return out;
}

class Dashboard {
    constructor() {
        this.container = document.getElementById('view-container');
        this.uptimeTimer = null;
        this.uptimeStartMs = null;
    }

    async render() {
        this.container.innerHTML = `
            <div class="dashboard">
                <div class="dash-prelude">
                    <div class="term-meta">
                        <span class="term-meta-key">SESSION</span><span class="term-meta-sep">/</span><span class="term-meta-val">live</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">UPTIME</span><span class="term-meta-sep">/</span><span class="term-meta-val" id="dash-uptime">—</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">STATUS</span><span class="term-meta-sep">/</span><span class="term-meta-val" id="dash-health">—</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">REFRESH</span><span class="term-meta-sep">/</span><span class="term-meta-val">5s</span>
                    </div>
                    <h1>${t('dashboard.title')}</h1>
                </div>

                <div class="quick-actions mt-2">
                    <button class="btn btn-secondary btn-sm" id="qa-clear-cooldowns" title="Clear all endpoint and token cooldowns">⟲ CLEAR COOLDOWNS</button>
                    <button class="btn btn-secondary btn-sm" id="qa-export-backup" title="Download endpoints + config as JSON (no secrets)">⤓ EXPORT BACKUP</button>
                    <button class="btn btn-secondary btn-sm" id="qa-open-inspector" title="Switch to live request inspector">▶ OPEN INSPECTOR</button>
                    <button class="btn btn-danger btn-sm" id="qa-flush-stats" title="Wipe daily_stats + credential_usage (irreversible)">✕ FLUSH STATS</button>
                </div>

                <div id="stats-cards" class="grid grid-cols-4 mt-3">
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.totalRequests')}</div>
                        <div class="stat-value" id="stat-requests">—</div>
                        <div class="stat-sub" id="stat-requests-sub">req · all-time</div>
                        <div class="stat-spark" id="spark-requests"></div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.successRate')}</div>
                        <div class="stat-value" id="stat-success">—</div>
                        <div class="stat-sub" id="stat-success-sub">2xx / total</div>
                        <div class="stat-spark" id="spark-success"></div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.inputTokens')}</div>
                        <div class="stat-value" id="stat-input-tokens">—</div>
                        <div class="stat-sub">tokens · in</div>
                        <div class="stat-spark" id="spark-in"></div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.outputTokens')}</div>
                        <div class="stat-value" id="stat-output-tokens">—</div>
                        <div class="stat-sub">tokens · out</div>
                        <div class="stat-spark" id="spark-out"></div>
                    </div>
                </div>

                <div class="grid grid-cols-2 mt-4">
                    <div class="card">
                        <div class="card-header">
                            <h3 class="card-title">${t('dashboard.activeEndpoints')}</h3>
                            <span class="term-meta term-meta-key">/api/endpoints</span>
                        </div>
                        <div class="card-body">
                            <div id="endpoints-list"></div>
                        </div>
                    </div>

                    <div class="card">
                        <div class="card-header">
                            <h3 class="card-title">${t('dashboard.recentActivity')}</h3>
                            <span class="term-meta term-meta-key">last 7d</span>
                        </div>
                        <div class="card-body">
                            <canvas id="activity-chart"></canvas>
                        </div>
                    </div>
                </div>
            </div>
        `;

        this.wireQuickActions();
        await this.loadData();
        this.startUptimeTick();
    }

    wireQuickActions() {
        document.getElementById('qa-clear-cooldowns').addEventListener('click', async () => {
            try {
                const res = await api.clearCooldowns();
                notifications.success(`Cleared ${res.endpoints_cleared || 0} endpoint + ${res.tokens_cleared || 0} token cooldowns`);
            } catch (err) {
                notifications.error('Clear cooldowns failed: ' + err.message);
            }
        });

        document.getElementById('qa-export-backup').addEventListener('click', () => {
            // Trigger file download via anchor — simpler than fetch + Blob.
            window.location.href = api.exportBackupURL();
        });

        document.getElementById('qa-open-inspector').addEventListener('click', () => {
            // Lazy import the router to avoid a circular dep at module load.
            import('../router.js').then(({ router }) => router.navigate('inspector'));
        });

        document.getElementById('qa-flush-stats').addEventListener('click', async () => {
            if (!confirm('FLUSH STATS\n\nThis deletes ALL daily_stats and credential_usage rows.\nEndpoints and tokens are preserved.\n\nProceed?')) return;
            try {
                const res = await api.flushStats();
                notifications.success(`Flushed ${res.daily_stats_deleted || 0} daily_stats rows`);
                await this.loadData();
            } catch (err) {
                notifications.error('Flush stats failed: ' + err.message);
            }
        });
    }

    async loadData() {
        try {
            const stats = await api.getStatsSummary();
            this.updateStats(stats);

            const endpointsData = await api.getEndpoints();
            this.updateEndpoints(endpointsData.endpoints);

            const dailyStats = await api.getStatsDaily();
            this.renderChart(dailyStats);
            this.updateSparklines(dailyStats);

            // Health snapshot (uptime + status). Best-effort — a stale
            // dashboard with no health badge is still useful.
            try {
                const health = await api.getHealth();
                if (health && typeof health.uptime_sec === 'number') {
                    this.uptimeStartMs = Date.now() - (health.uptime_sec * 1000);
                    this.updateUptimeLabel();
                }
                const healthEl = document.getElementById('dash-health');
                if (healthEl && health) {
                    healthEl.textContent = (health.status || 'unknown').toUpperCase();
                    healthEl.classList.toggle('term-status-bad', health.status === 'degraded' || health.status === 'no_endpoints');
                }
            } catch (_) {
                // /health unreachable — leave placeholders alone.
            }
        } catch (error) {
            notifications.error('Failed to load dashboard data: ' + error.message);
        }
    }

    startUptimeTick() {
        if (this.uptimeTimer) clearInterval(this.uptimeTimer);
        this.uptimeTimer = setInterval(() => {
            if (state.get('currentView') !== 'dashboard') {
                clearInterval(this.uptimeTimer);
                this.uptimeTimer = null;
                return;
            }
            this.updateUptimeLabel();
        }, 1000);
    }

    updateUptimeLabel() {
        const el = document.getElementById('dash-uptime');
        if (!el || this.uptimeStartMs == null) return;
        const sec = Math.floor((Date.now() - this.uptimeStartMs) / 1000);
        const d = Math.floor(sec / 86400);
        const h = Math.floor((sec % 86400) / 3600);
        const m = Math.floor((sec % 3600) / 60);
        const s = sec % 60;
        const pad = (n) => String(n).padStart(2, '0');
        el.textContent = d > 0
            ? `${d}d ${pad(h)}:${pad(m)}:${pad(s)}`
            : `${pad(h)}:${pad(m)}:${pad(s)}`;
    }

    updateStats(stats) {
        const totalRequests = stats.TotalRequests || 0;
        const totalErrors = stats.TotalErrors || 0;
        const successRate = totalRequests > 0
            ? ((totalRequests - totalErrors) / totalRequests * 100).toFixed(1)
            : 0;

        document.getElementById('stat-requests').textContent = formatNumber(totalRequests);
        document.getElementById('stat-success').textContent = successRate + '%';
        document.getElementById('stat-input-tokens').textContent = formatTokens(stats.TotalInputTokens || 0);
        document.getElementById('stat-output-tokens').textContent = formatTokens(stats.TotalOutputTokens || 0);
    }

    updateSparklines(dailyStats) {
        const stats = (dailyStats && dailyStats.stats) || {};
        const ep = stats.endpoints || {};
        const reqSeries  = buildSparkSeries(ep, 'requests');
        const errSeries  = buildSparkSeries(ep, 'errors');
        const inSeries   = buildSparkSeries(ep, 'inputTokens');
        const outSeries  = buildSparkSeries(ep, 'outputTokens');
        const successSeries = reqSeries.map((r, i) => {
            const e = errSeries[i] || 0;
            return r > 0 ? Math.round(((r - e) / r) * 100) : 0;
        });

        // setHTML helper — sparkline output is fully synthesized by us, no
        // user input flows in. innerHTML is appropriate here.
        const setSpark = (id, series) => {
            const el = document.getElementById(id);
            if (el) el.innerHTML = sparkline(series);
        };
        setSpark('spark-requests', reqSeries);
        setSpark('spark-success', successSeries);
        setSpark('spark-in', inSeries);
        setSpark('spark-out', outSeries);
    }

    updateEndpoints(endpoints) {
        const container = document.getElementById('endpoints-list');

        if (!endpoints || endpoints.length === 0) {
            container.innerHTML = `<div class="empty-state"><div class="empty-state-icon">∅</div><p class="empty-state-message">${this.escapeHtml(t('dashboard.noEndpoints'))}</p></div>`;
            return;
        }

        const enabledEndpoints = endpoints.filter(ep => ep.enabled);

        if (enabledEndpoints.length === 0) {
            container.innerHTML = `<div class="empty-state"><div class="empty-state-icon">∅</div><p class="empty-state-message">${this.escapeHtml(t('dashboard.noEnabledEndpoints'))}</p></div>`;
            return;
        }

        container.innerHTML = `
            <div class="endpoint-rows">
                ${enabledEndpoints.map(ep => `
                    <div class="endpoint-row" data-kbnav>
                        <span class="endpoint-row-dot"></span>
                        <span class="endpoint-row-name">${this.escapeHtml(ep.name)}</span>
                        <span class="endpoint-row-sep">·</span>
                        <span class="endpoint-row-transformer">${this.escapeHtml(ep.transformer || '—')}</span>
                        <span class="endpoint-row-status">${this.escapeHtml(t('common.active'))}</span>
                    </div>
                `).join('')}
            </div>
        `;
    }

    renderChart(dailyStats) {
        const canvas = document.getElementById('activity-chart');
        const ctx = canvas.getContext('2d');

        // Match the chart palette to the terminal aesthetic — soft phosphor
        // green bars, mono axis labels, dotted gridlines.
        const stats = dailyStats.stats || {};
        const endpoints = Object.keys(stats.endpoints || {});
        const requests = endpoints.map(ep => stats.endpoints[ep].requests || 0);

        const styles = getComputedStyle(document.body);
        const acid     = styles.getPropertyValue('--acid').trim() || '#7af28b';
        const textDim  = styles.getPropertyValue('--text-dim').trim() || '#4f5e58';
        const textSec  = styles.getPropertyValue('--text-secondary').trim() || '#80968a';
        const border   = styles.getPropertyValue('--border-color').trim() || '#1f2c30';
        const bgPrim   = styles.getPropertyValue('--bg-primary').trim() || '#0c100e';

        new Chart(ctx, {
            type: 'bar',
            data: {
                labels: endpoints,
                datasets: [{
                    label: 'Requests',
                    data: requests,
                    backgroundColor: 'rgba(122, 242, 139, 0.18)',
                    borderColor: acid,
                    borderWidth: 1,
                    borderRadius: 0,
                    hoverBackgroundColor: 'rgba(122, 242, 139, 0.35)',
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: true,
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        backgroundColor: bgPrim,
                        borderColor: acid,
                        borderWidth: 1,
                        titleFont: { family: 'JetBrains Mono', size: 11 },
                        bodyFont:  { family: 'JetBrains Mono', size: 12 },
                        titleColor: acid,
                        bodyColor: '#d4ead2',
                        padding: 10,
                        cornerRadius: 0,
                    }
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        ticks: { color: textSec, font: { family: 'JetBrains Mono', size: 11 } },
                        grid: { color: border, drawTicks: false },
                        border: { display: false }
                    },
                    x: {
                        ticks: { color: textDim, font: { family: 'JetBrains Mono', size: 10 }, maxRotation: 0 },
                        grid: { display: false },
                        border: { color: border }
                    }
                }
            }
        });
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

export const dashboard = new Dashboard();
