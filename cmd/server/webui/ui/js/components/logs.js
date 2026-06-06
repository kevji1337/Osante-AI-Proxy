import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { t } from '../utils/i18n.js';

class Logs {
    constructor() {
        this.container = document.getElementById('view-container');
        this.entries = [];
        this.level = 'INFO';
        this.search = '';
        this.autoRefresh = true;
        this.refreshTimer = null;

        window.addEventListener('languageChanged', () => {
            if (state.get('currentView') === 'logs') {
                this.render();
            }
        });
    }

    async render() {
        this.container.innerHTML = `
            <div class="view">
                <div class="view-header">
                    <h2>${t('logs.title')}</h2>
                </div>

                <div class="toolbar" style="display:flex; gap:10px; align-items:center; flex-wrap:wrap; margin-bottom:12px;">
                    <label>
                        ${t('logs.level')}:
                        <select class="form-select" id="logs-level" style="display:inline-block; width:auto; margin-left:6px;">
                            <option value="DEBUG" ${this.level === 'DEBUG' ? 'selected' : ''}>DEBUG</option>
                            <option value="INFO"  ${this.level === 'INFO'  ? 'selected' : ''}>INFO</option>
                            <option value="WARN"  ${this.level === 'WARN'  ? 'selected' : ''}>WARN</option>
                            <option value="ERROR" ${this.level === 'ERROR' ? 'selected' : ''}>ERROR</option>
                        </select>
                    </label>

                    <input type="text" class="form-input" id="logs-search"
                           placeholder="${t('logs.searchPlaceholder')}"
                           value="${this.escapeHtml(this.search)}"
                           style="flex:1; min-width:200px;">

                    <label style="display:inline-flex; gap:6px; align-items:center;">
                        <input type="checkbox" id="logs-autorefresh" ${this.autoRefresh ? 'checked' : ''}>
                        ${t('logs.autoRefresh')}
                    </label>

                    <button class="btn btn-secondary" id="logs-refresh">${t('common.refresh')}</button>
                </div>

                <div id="logs-output" style="
                    background:#0f172a; color:#e2e8f0;
                    font-family: ui-monospace, 'Cascadia Code', Consolas, monospace;
                    font-size:12px; line-height:1.5;
                    border-radius:8px; padding:12px;
                    max-height: calc(100vh - 220px);
                    overflow-y:auto;
                    white-space:pre-wrap; word-break:break-word;">
                </div>
            </div>
        `;

        document.getElementById('logs-level').addEventListener('change', e => {
            this.level = e.target.value;
            this.fetchAndRender();
        });
        document.getElementById('logs-search').addEventListener('input', e => {
            this.search = e.target.value;
            this.renderEntries();
        });
        document.getElementById('logs-autorefresh').addEventListener('change', e => {
            this.autoRefresh = e.target.checked;
            this.toggleAutoRefresh();
        });
        document.getElementById('logs-refresh').addEventListener('click', () => this.fetchAndRender());

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
            if (state.get('currentView') !== 'logs') {
                clearInterval(this.refreshTimer);
                this.refreshTimer = null;
                return;
            }
            this.fetchAndRender(true);
        }, 3000);
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
    // filter and color-coding by level. Auto-scrolls to the bottom if the user
    // was already at the bottom (typical "tail -f" feel).
    renderEntries() {
        const out = document.getElementById('logs-output');
        if (!out) return;

        const search = this.search.trim().toLowerCase();
        const filtered = search
            ? this.entries.filter(e => e.message.toLowerCase().includes(search))
            : this.entries;

        const colorByLevel = {
            DEBUG: '#94a3b8',
            INFO:  '#60a5fa',
            WARN:  '#fbbf24',
            ERROR: '#f87171',
        };

        const wasAtBottom = (out.scrollHeight - out.scrollTop - out.clientHeight) < 20;

        out.innerHTML = filtered.length === 0
            ? `<span style="color:#94a3b8;">${t('logs.empty')}</span>`
            : filtered.map(e => {
                const lvl = (e.levelStr || '').toUpperCase();
                const color = colorByLevel[lvl] || '#e2e8f0';
                const ts = this.formatTimestamp(e.timestamp);
                return `<div><span style="color:#64748b;">${this.escapeHtml(ts)}</span> `
                    + `<span style="color:${color}; font-weight:600;">${this.escapeHtml(lvl.padEnd(5))}</span> `
                    + `<span>${this.escapeHtml(e.message)}</span></div>`;
            }).join('');

        if (wasAtBottom) out.scrollTop = out.scrollHeight;
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
