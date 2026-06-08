import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { t } from '../utils/i18n.js';

const PREFS_KEY = 'logs.prefs';

// Stored filter prefs survive reloads and tab switches so the user doesn't
// have to re-pick DEBUG / type the same search every time.
function loadPrefs() {
    try {
        const raw = localStorage.getItem(PREFS_KEY);
        if (!raw) return null;
        const p = JSON.parse(raw);
        return (p && typeof p === 'object') ? p : null;
    } catch {
        return null;
    }
}

function savePrefs(prefs) {
    try {
        localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
    } catch {
        // Ignore quota / privacy-mode errors.
    }
}

class Logs {
    constructor() {
        this.container = document.getElementById('view-container');
        this.entries = [];

        const prefs = loadPrefs() || {};
        const validLevels = ['DEBUG', 'INFO', 'WARN', 'ERROR'];
        this.level = validLevels.includes(prefs.level) ? prefs.level : 'INFO';
        this.search = typeof prefs.search === 'string' ? prefs.search : '';
        this.autoRefresh = prefs.autoRefresh !== undefined ? !!prefs.autoRefresh : true;
        this.followTail = prefs.followTail !== undefined ? !!prefs.followTail : true;
        this.refreshTimer = null;
        // EventSource for the SSE tail. Opened on render(), closed when the
        // view unmounts or auto-refresh is disabled.
        this.sse = null;
        // Cap on rendered entries so a very long-running view doesn't keep
        // accumulating DOM nodes forever (the backend ring is 1000).
        this.maxEntries = 1000;

        window.addEventListener('languageChanged', () => {
            if (state.get('currentView') === 'logs') {
                this.render();
            }
        });
    }

    persistPrefs() {
        savePrefs({
            level: this.level,
            search: this.search,
            autoRefresh: this.autoRefresh,
            followTail: this.followTail,
        });
    }

    async render() {
        this.container.innerHTML = `
            <div class="view">
                <div class="view-header">
                    <div class="term-meta">
                        <span class="term-meta-key">STREAM</span><span class="term-meta-sep">/</span><span class="term-meta-val">/api/logs</span>
                        <span class="term-meta-sep">·</span>
                        <span class="term-meta-key">RING</span><span class="term-meta-sep">/</span><span class="term-meta-val">1000</span>
                    </div>
                    <h1>${t('logs.title')}</h1>
                </div>

                <div class="logs-toolbar">
                    <label class="logs-toolbar-field">
                        <span>${t('logs.level')}</span>
                        <select class="form-select" id="logs-level">
                            <option value="DEBUG" ${this.level === 'DEBUG' ? 'selected' : ''}>DEBUG</option>
                            <option value="INFO"  ${this.level === 'INFO'  ? 'selected' : ''}>INFO</option>
                            <option value="WARN"  ${this.level === 'WARN'  ? 'selected' : ''}>WARN</option>
                            <option value="ERROR" ${this.level === 'ERROR' ? 'selected' : ''}>ERROR</option>
                        </select>
                    </label>

                    <input type="text" class="form-input logs-search" id="logs-search"
                           placeholder="${t('logs.searchPlaceholder')}"
                           value="${this.escapeHtml(this.search)}">

                    <label class="logs-toolbar-check">
                        <input type="checkbox" id="logs-autorefresh" ${this.autoRefresh ? 'checked' : ''}>
                        ${t('logs.autoRefresh')}
                    </label>

                    <label class="logs-toolbar-check">
                        <input type="checkbox" id="logs-follow-tail" ${this.followTail ? 'checked' : ''}>
                        ${t('logs.followTail')}
                    </label>

                    <button class="btn btn-secondary" id="logs-refresh">${t('common.refresh')}</button>
                </div>

                <div id="logs-output" class="logs-output"></div>
            </div>
        `;

        document.getElementById('logs-level').addEventListener('change', e => {
            this.level = e.target.value;
            this.persistPrefs();
            // Re-pull the ring at the new level, then re-subscribe so the
            // SSE stream filters server-side.
            this.fetchAndRender();
            this.toggleAutoRefresh();
        });
        document.getElementById('logs-search').addEventListener('input', e => {
            this.search = e.target.value;
            this.persistPrefs();
            this.renderEntries();
        });
        document.getElementById('logs-autorefresh').addEventListener('change', e => {
            this.autoRefresh = e.target.checked;
            this.persistPrefs();
            this.toggleAutoRefresh();
        });
        document.getElementById('logs-follow-tail').addEventListener('change', e => {
            this.followTail = e.target.checked;
            this.persistPrefs();
            if (this.followTail) {
                const out = document.getElementById('logs-output');
                if (out) out.scrollTop = out.scrollHeight;
            }
        });
        document.getElementById('logs-refresh').addEventListener('click', () => this.fetchAndRender());

        await this.fetchAndRender();
        this.toggleAutoRefresh();
    }

    toggleAutoRefresh() {
        // Tear down whatever stream we previously had open.
        this.closeStream();
        if (this.refreshTimer) {
            clearInterval(this.refreshTimer);
            this.refreshTimer = null;
        }
        if (!this.autoRefresh) return;

        // Open an SSE tail. The server filters by level so we don't ship
        // entries the user isn't watching, and the connection is held open
        // for the lifetime of the view.
        try {
            const url = `/api/logs/stream?level=${encodeURIComponent(this.level)}`;
            this.sse = new EventSource(url);
            this.sse.onmessage = (event) => {
                if (state.get('currentView') !== 'logs') {
                    this.closeStream();
                    return;
                }
                try {
                    const entry = JSON.parse(event.data);
                    this.entries.push(entry);
                    if (this.entries.length > this.maxEntries) {
                        this.entries = this.entries.slice(-this.maxEntries);
                    }
                    this.renderEntries();
                } catch (e) {
                    // Malformed entry — skip, don't take the stream down.
                }
            };
            this.sse.onerror = () => {
                // Browser auto-reconnects after a short delay; nothing to do.
            };
        } catch (e) {
            // Fall back to polling if EventSource isn't available — keep
            // the dashboard usable on exotic browsers.
            this.refreshTimer = setInterval(() => {
                if (state.get('currentView') !== 'logs') {
                    clearInterval(this.refreshTimer);
                    this.refreshTimer = null;
                    return;
                }
                this.fetchAndRender(true);
            }, 3000);
        }
    }

    closeStream() {
        if (this.sse) {
            try { this.sse.close(); } catch (_) {}
            this.sse = null;
        }
    }

    async fetchAndRender(silent = false) {
        try {
            const data = await api.getLogs(this.level, 1000);
            this.entries = data.entries || [];
            this.renderEntries();
        } catch (err) {
            if (!silent) notifications.error(`${t('logs.fetchFailed')}: ${err.message}`);
        }
    }

    // Renders the current entries into the output panel, applying the search
    // filter and color-coding by level via CSS classes (no inline styles —
    // colors come from the terminal palette in components.css).
    renderEntries() {
        const out = document.getElementById('logs-output');
        if (!out) return;

        const search = this.search.trim().toLowerCase();
        const filtered = search
            ? this.entries.filter(e => e.message.toLowerCase().includes(search))
            : this.entries;

        const wasAtBottom = (out.scrollHeight - out.scrollTop - out.clientHeight) < 20;

        out.innerHTML = filtered.length === 0
            ? `<span class="logs-empty">${this.escapeHtml(t('logs.empty'))}</span>`
            : filtered.map(e => {
                const lvl = (e.levelStr || '').toUpperCase();
                const lvlClass = ['DEBUG', 'INFO', 'WARN', 'ERROR'].includes(lvl) ? lvl.toLowerCase() : 'info';
                const ts = this.formatTimestamp(e.timestamp);
                return `<div class="log-line log-line-${lvlClass}">`
                    + `<span class="log-ts">${this.escapeHtml(ts)}</span> `
                    + `<span class="log-lvl">${this.escapeHtml(lvl.padEnd(5))}</span> `
                    + `<span class="log-msg">${this.escapeHtml(e.message)}</span>`
                    + `</div>`;
            }).join('');

        if (this.followTail || wasAtBottom) out.scrollTop = out.scrollHeight;
    }

    formatTimestamp(iso) {
        try {
            const d = new Date(iso);
            return d.toLocaleTimeString();
        } catch {
            return iso;
        }
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text == null ? '' : String(text);
        return div.innerHTML;
    }
}

export const logs = new Logs();
