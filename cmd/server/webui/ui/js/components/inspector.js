import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { t } from '../utils/i18n.js';

// Inspector — live request trace ring. Pulls /api/trace every 2 seconds
// while the view is mounted and renders each request as a horizontal
// timeline broken down by phase. Click a row to expand a detail panel with
// the raw mark offsets, bytes, tokens, etc.
//
// Backend: internal/proxy/trace.go (RingBuffer of TraceRecord)
class Inspector {
    constructor() {
        this.container = document.getElementById('view-container');
        this.records = [];
        this.refreshTimer = null;
        this.selectedId = null;
        this.autoRefresh = true;
        this.maxScale = 1; // ms; for normalising bar widths
    }

    async render() {
        this.container.innerHTML = `
            <div class="view">
                <div class="view-header">
                    <div class="term-meta">
                        <span class="term-meta-key">RING</span><span class="term-meta-sep">/</span><span class="term-meta-val">/api/trace</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">CAP</span><span class="term-meta-sep">/</span><span class="term-meta-val">64</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">REFRESH</span><span class="term-meta-sep">/</span><span class="term-meta-val">2s</span>
                    </div>
                    <h1>${t('inspector.title')}</h1>
                </div>

                <div class="trace-toolbar">
                    <label class="logs-toolbar-check">
                        <input type="checkbox" id="trace-autorefresh" ${this.autoRefresh ? 'checked' : ''}>
                        ${t('inspector.autoRefresh')}
                    </label>
                    <span class="trace-legend">
                        <span class="trace-legend-item"><span class="trace-seg trace-phase-received"></span>recv</span>
                        <span class="trace-legend-item"><span class="trace-seg trace-phase-transformed"></span>xform</span>
                        <span class="trace-legend-item"><span class="trace-seg trace-phase-upstream_sent"></span>upstream</span>
                        <span class="trace-legend-item"><span class="trace-seg trace-phase-client_sent"></span>client</span>
                    </span>
                    <button class="btn btn-secondary btn-sm" id="trace-refresh-btn">${t('common.refresh')}</button>
                </div>

                <div id="trace-list" class="trace-list"></div>
            </div>
        `;

        document.getElementById('trace-autorefresh').addEventListener('change', e => {
            this.autoRefresh = e.target.checked;
            this.toggleAutoRefresh();
        });
        document.getElementById('trace-refresh-btn').addEventListener('click', () => this.fetchAndRender());

        await this.fetchAndRender();
        this.toggleAutoRefresh();
    }

    toggleAutoRefresh() {
        if (this.refreshTimer) {
            clearInterval(this.refreshTimer);
            this.refreshTimer = null;
        }
        if (!this.autoRefresh) return;
        this.refreshTimer = setInterval(() => {
            if (state.get('currentView') !== 'inspector') {
                clearInterval(this.refreshTimer);
                this.refreshTimer = null;
                return;
            }
            this.fetchAndRender(true);
        }, 2000);
    }

    async fetchAndRender(silent = false) {
        try {
            const data = await api.getTrace(50);
            this.records = data.records || [];
            this.maxScale = Math.max(50, ...this.records.map(r => r.total_ms || 0));
            this.renderList();
        } catch (err) {
            if (!silent) notifications.error(`${t('inspector.fetchFailed')}: ${err.message}`);
        }
    }

    renderList() {
        const out = document.getElementById('trace-list');
        if (!out) return;

        if (this.records.length === 0) {
            out.innerHTML = `<div class="empty-state"><div class="empty-state-icon">∅</div><p class="empty-state-message">${this.escapeHtml(t('inspector.noTraces'))}</p></div>`;
            return;
        }

        out.innerHTML = this.records.map(rec => this.renderRow(rec)).join('');

        out.querySelectorAll('.trace-row').forEach(row => {
            row.addEventListener('click', () => {
                const id = Number(row.dataset.id);
                this.selectedId = (this.selectedId === id) ? null : id;
                this.renderList();
            });
        });
    }

    renderRow(rec) {
        const isSelected = rec.id === this.selectedId;
        const statusClass = this.statusClass(rec);
        const segments = this.buildSegments(rec);
        const startStamp = this.formatStamp(rec.start_unix_ms);

        const detail = isSelected ? `
            <div class="trace-detail">
                <div class="trace-detail-grid">
                    <div><span class="trace-kv-k">id</span><span class="trace-kv-v">#${rec.id}</span></div>
                    <div><span class="trace-kv-k">model</span><span class="trace-kv-v">${this.escapeHtml(rec.model || '—')}</span></div>
                    <div><span class="trace-kv-k">client</span><span class="trace-kv-v">${this.escapeHtml(rec.client_format || '—')}</span></div>
                    <div><span class="trace-kv-k">xform</span><span class="trace-kv-v">${this.escapeHtml(rec.transformer || '—')}</span></div>
                    <div><span class="trace-kv-k">bytes in</span><span class="trace-kv-v">${this.fmtBytes(rec.bytes_in)}</span></div>
                    <div><span class="trace-kv-k">bytes out</span><span class="trace-kv-v">${this.fmtBytes(rec.bytes_out)}</span></div>
                    <div><span class="trace-kv-k">tok in</span><span class="trace-kv-v">${rec.input_tokens || 0}</span></div>
                    <div><span class="trace-kv-k">tok out</span><span class="trace-kv-v">${rec.output_tokens || 0}</span></div>
                    <div><span class="trace-kv-k">stream</span><span class="trace-kv-v">${rec.streaming ? 'yes' : 'no'}</span></div>
                    <div><span class="trace-kv-k">pinned</span><span class="trace-kv-v">${rec.pinned_endpoint ? 'yes' : 'no'}</span></div>
                </div>
                ${rec.err ? `<div class="trace-detail-err">err: ${this.escapeHtml(rec.err)}</div>` : ''}
                <table class="trace-marks-table">
                    <thead><tr><th>phase</th><th>+ms</th></tr></thead>
                    <tbody>
                        ${(rec.marks || []).map(m => `<tr><td>${this.escapeHtml(m.phase)}</td><td>${m.offset_ms}</td></tr>`).join('')}
                    </tbody>
                </table>
            </div>
        ` : '';

        return `
            <div class="trace-row ${isSelected ? 'is-selected' : ''} ${statusClass}" data-id="${rec.id}" data-kbnav>
                <div class="trace-row-head">
                    <span class="trace-row-time">${startStamp}</span>
                    <span class="trace-row-endpoint">${this.escapeHtml(rec.endpoint || '—')}</span>
                    <span class="trace-row-method">${this.escapeHtml(rec.method || '')} ${this.escapeHtml(rec.path || '')}</span>
                    <span class="trace-row-status">${this.formatStatus(rec)}</span>
                    <span class="trace-row-total">${rec.total_ms}ms</span>
                </div>
                <div class="trace-bar">
                    ${segments}
                </div>
                ${detail}
            </div>
        `;
    }

    // Build a flex-row of colored segments proportional to each phase's
    // duration. We walk marks in pairs (prev → curr) — each gap is one
    // segment colored by the *destination* phase.
    buildSegments(rec) {
        const marks = rec.marks || [];
        if (marks.length < 2) return '';
        const total = Math.max(1, rec.total_ms || 0);
        let prevOffset = 0;
        const parts = [];
        for (const m of marks) {
            const dur = Math.max(0, m.offset_ms - prevOffset);
            const pct = (dur / total) * 100;
            if (pct > 0) {
                parts.push(`<span class="trace-seg trace-phase-${this.escapeHtml(m.phase)}" style="width:${pct.toFixed(2)}%" title="${this.escapeHtml(m.phase)}: ${dur}ms"></span>`);
            }
            prevOffset = m.offset_ms;
        }
        return parts.join('');
    }

    statusClass(rec) {
        if (rec.err) return 'is-error';
        if (rec.status_code >= 500) return 'is-error';
        if (rec.status_code >= 400) return 'is-warn';
        if (rec.status_code >= 200 && rec.status_code < 300) return 'is-ok';
        return '';
    }

    formatStatus(rec) {
        if (rec.err) return 'ERR';
        if (!rec.status_code) return '—';
        return String(rec.status_code);
    }

    fmtBytes(n) {
        if (!n) return '0';
        if (n < 1024) return `${n}B`;
        if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)}KB`;
        return `${(n / 1024 / 1024).toFixed(2)}MB`;
    }

    formatStamp(ms) {
        if (!ms) return '—';
        const d = new Date(ms);
        return d.toLocaleTimeString();
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text == null ? '' : String(text);
        return div.innerHTML;
    }
}

export const inspector = new Inspector();
