import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { getTransformerLabel, formatCountdown } from '../utils/formatters.js';
import { t } from '../utils/i18n.js';

class Endpoints {
    constructor() {
        this.container = document.getElementById('view-container');
        this.endpoints = [];
        this.tokenPools = {};
        this.states = {};
        this.currentEndpoint = null;
        this.draggedIndex = null;
        this.currentTokenPoolEndpoint = null;
        this.refreshTimer = null;
    }

    async render() {
        this.container.innerHTML = `
            <div class="endpoints">
                <div class="flex-between mb-3">
                    <h1>${t('endpoints.title')}</h1>
                    <button class="btn btn-primary" id="add-endpoint-btn">
                        <span>+ ${t('endpoints.addEndpoint')}</span>
                    </button>
                </div>

                <div class="card">
                    <div class="card-body">
                        <div id="endpoints-table"></div>
                    </div>
                </div>
            </div>
        `;

        document.getElementById('add-endpoint-btn').addEventListener('click', () => this.showAddModal());
        this.installModalDismissHandlers();

        await this.loadEndpoints();
        this.startAutoRefresh();
    }

    // Wire up global "click outside to close" and "Esc to close" handlers for
    // the modal container. Done once on view init — subsequent modals don't
    // need to re-bind. Inner overlays (e.g. nested model-selection modal) opt
    // out via .no-overlay-dismiss on their .modal-overlay element.
    installModalDismissHandlers() {
        const container = document.getElementById('modal-container');
        if (!container || container.dataset.dismissBound === '1') return;
        container.dataset.dismissBound = '1';

        container.addEventListener('click', (e) => {
            const overlay = e.target.closest('.modal-overlay');
            if (!overlay || overlay.classList.contains('no-overlay-dismiss')) return;
            // Close only when the click landed on the overlay itself, not on
            // the modal content inside it.
            if (e.target === overlay) {
                this.closeModal();
            }
        });

        document.addEventListener('keydown', (e) => {
            if (e.key !== 'Escape') return;
            if (container.children.length === 0) return;
            this.closeModal();
        });
    }

    // Refresh endpoint data every 5s so the cooldown countdown stays current.
    // Skips refresh while dragging or while a modal is open to avoid disruption.
    startAutoRefresh() {
        this.stopAutoRefresh();
        this.refreshTimer = setInterval(() => {
            if (state.get('currentView') !== 'endpoints') {
                this.stopAutoRefresh();
                return;
            }
            if (this.draggedIndex !== null) {
                return;
            }
            const modal = document.getElementById('modal-container');
            if (modal && modal.children.length > 0) {
                return;
            }
            this.loadEndpoints();
        }, 5000);
    }

    stopAutoRefresh() {
        if (this.refreshTimer) {
            clearInterval(this.refreshTimer);
            this.refreshTimer = null;
        }
    }

    async loadEndpoints() {
        try {
            const data = await api.getEndpoints();
            this.endpoints = data.endpoints || [];
            this.tokenPools = data.tokenPools || {};
            this.states = data.states || {};

            // Prefer the current endpoint reported alongside the list; fall back
            // to the dedicated endpoint for older backends.
            if (data.current !== undefined && data.current !== null) {
                this.currentEndpoint = data.current || null;
            } else {
                try {
                    const currentData = await api.getCurrentEndpoint();
                    this.currentEndpoint = currentData.name || null;
                } catch (error) {
                    console.error('Failed to get current endpoint:', error);
                    this.currentEndpoint = null;
                }
            }

            this.renderTable();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToLoad')}: ${error.message}`);
        }
    }

    renderTable() {
        const container = document.getElementById('endpoints-table');

        if (this.endpoints.length === 0) {
            container.innerHTML = `
                <div class="empty-state">
                    <div class="empty-state-icon">🔗</div>
                    <div class="empty-state-title">${t('endpoints.noEndpoints')}</div>
                    <div class="empty-state-message">${t('endpoints.noEndpointsMessage')}</div>
                </div>
            `;
            return;
        }

        const enabledEndpoints = this.endpoints.filter(ep => ep.enabled);
        const allLimited = enabledEndpoints.length > 0 &&
            enabledEndpoints.every(ep => (this.states[ep.name]?.status) === 'limited');
        const warningHtml = allLimited ? `
            <div class="alert alert-warning">
                <span class="alert-icon">⚠</span>
                <span>${t('endpoints.allLimitedWarning')}</span>
            </div>
        ` : '';

        container.innerHTML = `
            ${warningHtml}
            <div class="table-container">
                <table class="table">
                    <thead>
                        <tr>
                            <th style="width: 30px;"></th>
                            <th>${t('common.name')}</th>
                            <th>${t('endpoints.apiUrl')}</th>
                            <th>${t('endpoints.transformer')}</th>
                            <th>${t('endpoints.model')}</th>
                            <th>${t('endpoints.tokenPool')}</th>
                            <th>${t('common.status')}</th>
                            <th>${t('common.actions')}</th>
                        </tr>
                    </thead>
                    <tbody id="endpoints-tbody">
                        ${this.endpoints.map((ep, index) => this.renderEndpointRow(ep, index)).join('')}
                    </tbody>
                </table>
            </div>
        `;

        // Attach event listeners
        this.attachEventListeners();
        this.attachDragListeners();
    }

    renderEndpointRow(ep, index) {
        const isCurrentEndpoint = ep.name === this.currentEndpoint;
        const testStatus = this.getTestStatus(ep.name);
        let testStatusIcon = '⚠️';
        let testStatusTitle = t('endpoints.notTested');

        if (testStatus === true) {
            testStatusIcon = '✅';
            testStatusTitle = t('endpoints.testPassed');
        } else if (testStatus === false) {
            testStatusIcon = '❌';
            testStatusTitle = t('endpoints.testFailed');
        }

        return `
            <tr data-endpoint="${this.escapeHtml(ep.name)}" data-index="${index}" draggable="true" style="cursor: move;">
                <td style="cursor: grab; text-align: center;">⋮⋮</td>
                <td>
                    <strong>${this.escapeHtml(ep.name)}</strong>
                    <span title="${testStatusTitle}" style="margin-left: 5px;">${testStatusIcon}</span>
                    ${isCurrentEndpoint ? `<span class="badge badge-primary" style="margin-left: 5px;">${t('endpoints.current')}</span>` : ''}
                </td>
                <td>
                    <code style="font-size: 12px;">${this.escapeHtml(ep.apiUrl)}</code>
                    <button class="btn-icon copy-btn" data-copy="${this.escapeHtml(ep.apiUrl)}" title="${t('endpoints.copyUrl')}">
                        📋
                    </button>
                </td>
                <td>${getTransformerLabel(ep.transformer)}</td>
                <td>${this.escapeHtml(ep.model || '-')}</td>
                <td>${this.renderTokenPoolSummary(this.tokenPools[ep.name])}</td>
                <td>${this.renderStatusCell(ep)}</td>
                <td>
                    <div class="flex gap-2">
                        ${ep.enabled && !isCurrentEndpoint ? `
                            <button class="btn btn-sm btn-secondary switch-btn" data-name="${this.escapeHtml(ep.name)}" title="${t('endpoints.switchToEndpoint')}">
                                ${t('common.switch')}
                            </button>
                        ` : ''}
                        <button class="btn btn-sm btn-secondary test-btn" data-name="${this.escapeHtml(ep.name)}">
                            ${t('common.test')}
                        </button>
                        <button class="btn btn-sm btn-secondary token-pool-btn" data-name="${this.escapeHtml(ep.name)}">
                            ${t('endpoints.tokenPoolManagement')}
                        </button>
                        <label class="toggle-switch">
                            <input type="checkbox" class="toggle-endpoint" data-name="${this.escapeHtml(ep.name)}" ${ep.enabled ? 'checked' : ''}>
                            <span class="toggle-slider"></span>
                        </label>
                        <button class="btn btn-sm btn-secondary edit-btn" data-name="${this.escapeHtml(ep.name)}">
                            ${t('common.edit')}
                        </button>
                        <button class="btn btn-sm btn-secondary clone-btn" data-name="${this.escapeHtml(ep.name)}">
                            ${t('common.clone')}
                        </button>
                        <button class="btn btn-sm btn-secondary copy-config-btn" data-name="${this.escapeHtml(ep.name)}" title="${t('endpoints.copyConfig')}">
                            ${t('endpoints.copyConfig')}
                        </button>
                        <button class="btn btn-sm btn-danger delete-btn" data-name="${this.escapeHtml(ep.name)}">
                            ${t('common.delete')}
                        </button>
                    </div>
                </td>
            </tr>
        `;
    }

    renderStatusCell(ep) {
        const st = this.states[ep.name] || {};
        const status = st.status || (ep.enabled ? 'active' : 'disabled');
        const badge = this.statusBadgeHtml(status);

        if (status === 'limited') {
            return `${badge}${this.limitedDetailsHtml(st)}`;
        }
        if (status === 'error' && st.last_error) {
            return `${badge}
                <div class="text-muted" style="font-size:11px;margin-top:4px;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="${this.escapeHtml(st.last_error)}">
                    ${this.escapeHtml(st.last_error)}
                </div>`;
        }
        return badge;
    }

    statusBadgeHtml(status) {
        const labelMap = {
            active: t('endpoints.statusActive'),
            current: t('endpoints.statusCurrent'),
            limited: 'LIMITED',
            error: t('endpoints.statusError'),
            disabled: t('endpoints.statusDisabled')
        };
        const label = labelMap[status] || status;

        // disabled has no theme badge class -> render a neutral badge.
        if (status === 'disabled') {
            return `<span class="badge badge-neutral">${label}</span>`;
        }
        const classMap = {
            active: 'badge-success',
            current: 'badge-info',
            limited: 'badge-warning',
            error: 'badge-danger'
        };
        const cls = classMap[status] || 'badge-info';
        return `<span class="badge ${cls}">${label}</span>`;
    }

    limitedDetailsHtml(st) {
        const reason = st.cooldown_reason || t('endpoints.limitedReason');
        const resetLocal = st.cooldown_until ? new Date(st.cooldown_until).toLocaleString() : '';
        const left = formatCountdown(st.remaining_cooldown_seconds || 0);

        return `
            <div style="font-size:11px;margin-top:4px;line-height:1.5;">
                <div><strong>${this.escapeHtml(reason)}</strong></div>
                ${resetLocal ? `<div>${t('endpoints.resetAt')}: ${this.escapeHtml(resetLocal)}</div>` : ''}
                <div>⏳ ${this.escapeHtml(left)} ${t('endpoints.cooldownLeft')}</div>
                <div class="text-muted">${t('endpoints.skippedHint')}</div>
            </div>`;
    }

    renderTokenPoolSummary(pool) {
        if (!pool || !pool.total) {
            return '<span class="text-muted">0</span>';
        }

        return `
            <div style="font-size: 12px; line-height: 1.4;">
                <div>${t('endpoints.total')}: <strong>${pool.total}</strong></div>
                <div>A:${pool.active || 0} E:${pool.expiring || 0} X:${pool.expired || 0} I:${pool.invalid || 0}</div>
                <div>C:${pool.cooldown || 0} R:${pool.needRefresh || 0} D:${pool.disabled || 0}</div>
            </div>
        `;
    }

    attachEventListeners() {
        // Test buttons
        document.querySelectorAll('.test-btn').forEach(btn => {
            btn.addEventListener('click', () => this.testEndpoint(btn.dataset.name));
        });

        // Toggle switches
        document.querySelectorAll('.toggle-endpoint').forEach(toggle => {
            toggle.addEventListener('change', () => this.toggleEndpoint(toggle.dataset.name, toggle.checked));
        });

        // Edit buttons
        document.querySelectorAll('.edit-btn').forEach(btn => {
            btn.addEventListener('click', () => this.showEditModal(btn.dataset.name));
        });

        // Clone buttons
        document.querySelectorAll('.clone-btn').forEach(btn => {
            btn.addEventListener('click', () => this.cloneEndpoint(btn.dataset.name));
        });

        // Delete buttons
        document.querySelectorAll('.delete-btn').forEach(btn => {
            btn.addEventListener('click', () => this.deleteEndpoint(btn.dataset.name));
        });

        // Switch buttons
        document.querySelectorAll('.switch-btn').forEach(btn => {
            btn.addEventListener('click', () => this.switchEndpoint(btn.dataset.name));
        });

        // Token pool buttons
        document.querySelectorAll('.token-pool-btn').forEach(btn => {
            btn.addEventListener('click', () => this.showTokenPoolModal(btn.dataset.name));
        });

        // Copy buttons
        document.querySelectorAll('.copy-btn').forEach(btn => {
            btn.addEventListener('click', () => this.copyToClipboard(btn.dataset.copy, btn));
        });

        // Copy-config buttons (per-row Claude/Codex client config snippet).
        document.querySelectorAll('.copy-config-btn').forEach(btn => {
            btn.addEventListener('click', () => this.showCopyConfigModal(btn.dataset.name));
        });
    }

    attachDragListeners() {
        const rows = document.querySelectorAll('#endpoints-tbody tr[draggable="true"]');

        rows.forEach(row => {
            row.addEventListener('dragstart', (e) => {
                this.draggedIndex = parseInt(row.dataset.index);
                row.style.opacity = '0.5';
            });

            row.addEventListener('dragend', (e) => {
                row.style.opacity = '1';
            });

            row.addEventListener('dragover', (e) => {
                e.preventDefault();
                row.style.borderTop = '2px solid var(--acid)';
                row.style.boxShadow = 'inset 0 1px 0 var(--acid-glow)';
            });

            row.addEventListener('dragleave', (e) => {
                row.style.borderTop = '';
                row.style.boxShadow = '';
            });

            row.addEventListener('drop', async (e) => {
                e.preventDefault();
                row.style.borderTop = '';
                row.style.boxShadow = '';

                const dropIndex = parseInt(row.dataset.index);
                if (this.draggedIndex !== null && this.draggedIndex !== dropIndex) {
                    await this.reorderEndpoints(this.draggedIndex, dropIndex);
                }
                this.draggedIndex = null;
            });
        });
    }

    async reorderEndpoints(fromIndex, toIndex) {
        try {
            // Reorder the array
            const [movedItem] = this.endpoints.splice(fromIndex, 1);
            this.endpoints.splice(toIndex, 0, movedItem);

            // Send new order to backend
            const names = this.endpoints.map(ep => ep.name);
            await api.reorderEndpoints(names);

            notifications.success(t('notifications.endpointsReordered'));
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToReorder')}: ${error.message}`);
            await this.loadEndpoints(); // Reload to reset order
        }
    }

    async switchEndpoint(name) {
        try {
            await api.switchEndpoint(name);
            notifications.success(`${t('notifications.endpointSwitched')} ${name}`);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToSwitch')}: ${error.message}`);
        }
    }

    copyToClipboard(text, button) {
        navigator.clipboard.writeText(text).then(() => {
            const originalText = button.textContent;
            button.textContent = '✓';
            setTimeout(() => {
                button.textContent = originalText;
            }, 1000);
        }).catch(err => {
            notifications.error(t('endpoints.failedToCopy'));
        });
    }

    // showCopyConfigModal renders a small modal with ready-to-paste client
    // config blocks for Claude Code (env vars) and Codex CLI (config.toml +
    // auth.json). The proxy URL is derived from window.location so it matches
    // however the user opened the UI (host:port).
    showCopyConfigModal(endpointName) {
        const endpoint = this.endpoints.find(ep => ep.name === endpointName);
        if (!endpoint) return;

        const proto = window.location.protocol === 'https:' ? 'https' : 'http';
        const host = window.location.host; // includes port
        const proxyBase = `${proto}://${host}`;
        const model = (endpoint.model || '').trim() || 'gpt-5.4';

        const claudeEnv =
            `ANTHROPIC_BASE_URL=${proxyBase}\n` +
            `ANTHROPIC_AUTH_TOKEN=anything`;

        const codexToml =
            `model_provider = "osante"\n` +
            `model = "${model}"\n\n` +
            `[model_providers.osante]\n` +
            `name = "Osante Proxy"\n` +
            `base_url = "${proxyBase}/v1"\n` +
            `wire_api = "responses"\n` +
            `requires_openai_auth = true`;

        const codexAuth = `{ "OPENAI_API_KEY": "stub" }`;

        const modalContainer = document.getElementById('modal-container');
        modalContainer.innerHTML = `
            <div class="modal-overlay">
                <div class="modal" style="max-width: 760px; width: 95vw;">
                    <div class="modal-header">
                        <h3 class="modal-title">${t('endpoints.copyConfigTitle')}: ${this.escapeHtml(endpointName)}</h3>
                        <button class="modal-close" id="close-modal">×</button>
                    </div>
                    <div class="modal-body">
                        <p class="text-muted" style="margin-top:0;">${t('endpoints.copyConfigHint')}</p>

                        <div class="form-group">
                            <label class="form-label">Claude Code — environment</label>
                            <div style="position:relative;">
                                <pre id="copy-config-claude" class="code-block" style="white-space:pre; overflow-x:auto;"></pre>
                                <button class="btn btn-sm btn-secondary" id="copy-config-claude-btn" style="position:absolute; top:8px; right:8px;">${t('common.copy')}</button>
                            </div>
                        </div>

                        <div class="form-group">
                            <label class="form-label">Codex CLI — ~/.codex/config.toml</label>
                            <div style="position:relative;">
                                <pre id="copy-config-codex" class="code-block" style="white-space:pre; overflow-x:auto;"></pre>
                                <button class="btn btn-sm btn-secondary" id="copy-config-codex-btn" style="position:absolute; top:8px; right:8px;">${t('common.copy')}</button>
                            </div>
                        </div>

                        <div class="form-group">
                            <label class="form-label">Codex CLI — ~/.codex/auth.json</label>
                            <div style="position:relative;">
                                <pre id="copy-config-auth" class="code-block" style="white-space:pre; overflow-x:auto;"></pre>
                                <button class="btn btn-sm btn-secondary" id="copy-config-auth-btn" style="position:absolute; top:8px; right:8px;">${t('common.copy')}</button>
                            </div>
                        </div>
                    </div>
                    <div class="modal-footer">
                        <button class="btn btn-secondary" id="copy-config-close-btn">${t('common.close')}</button>
                    </div>
                </div>
            </div>
        `;

        // Set code contents via textContent — never inject snippets as innerHTML
        // even though they're synthesized server-side-of-UI; defence-in-depth.
        document.getElementById('copy-config-claude').textContent = claudeEnv;
        document.getElementById('copy-config-codex').textContent = codexToml;
        document.getElementById('copy-config-auth').textContent = codexAuth;

        const wireCopy = (btnId, text) => {
            const btn = document.getElementById(btnId);
            btn.addEventListener('click', () => {
                navigator.clipboard.writeText(text).then(() => {
                    const original = btn.textContent;
                    btn.textContent = '✓';
                    notifications.success(t('endpoints.configCopied'));
                    setTimeout(() => { btn.textContent = original; }, 1000);
                }).catch(() => notifications.error(t('endpoints.failedToCopy')));
            });
        };
        wireCopy('copy-config-claude-btn', claudeEnv);
        wireCopy('copy-config-codex-btn', codexToml);
        wireCopy('copy-config-auth-btn', codexAuth);

        document.getElementById('close-modal').addEventListener('click', () => this.closeModal());
        document.getElementById('copy-config-close-btn').addEventListener('click', () => this.closeModal());
    }

    getTestStatus(endpointName) {
        try {
            const statusMap = JSON.parse(localStorage.getItem('Osante Proxy_endpointTestStatus') || '{}');
            return statusMap[endpointName];
        } catch {
            return undefined;
        }
    }

    saveTestStatus(endpointName, success) {
        try {
            const statusMap = JSON.parse(localStorage.getItem('Osante Proxy_endpointTestStatus') || '{}');
            statusMap[endpointName] = success;
            localStorage.setItem('Osante Proxy_endpointTestStatus', JSON.stringify(statusMap));
        } catch (error) {
            console.error('Failed to save test status:', error);
        }
    }

    showAddModal() {
        this.showEndpointModal(null);
    }

    showEditModal(name) {
        const endpoint = this.endpoints.find(ep => ep.name === name);
        if (endpoint) {
            this.showEndpointModal(endpoint);
        }
    }

    showEndpointModal(endpoint, isClone = false) {
        const isEdit = !!endpoint && !isClone;
        const modalContainer = document.getElementById('modal-container');

        // For clone mode: show masked value like edit mode
        const apiKeyValue = endpoint ? '****' : '';
        const apiKeyPlaceholder = 'sk-...';
        const apiKeyHint = isEdit || isClone ? `<small class="text-muted">${t('endpoints.keepExistingKey')}</small>` : '';
        const cloneHiddenInput = isClone ? '<input type="hidden" name="isClone" value="true">' : '';
        const cloneFromValue = endpoint?.cloneFrom || '';
        const cloneFromInput = isClone && cloneFromValue ? `<input type="hidden" name="cloneFrom" value="${cloneFromValue}">` : '';

        modalContainer.innerHTML = `
            <div class="modal-overlay">
                <div class="modal">
                    <div class="modal-header">
                        <h3 class="modal-title">${isClone ? t('endpoints.cloneEndpoint') : (isEdit ? t('common.edit') : t('common.add'))} ${t('endpoints.title')}</h3>
                        <button class="modal-close" id="close-modal">×</button>
                    </div>
                    <div class="modal-body">
                        <form id="endpoint-form">
                            ${cloneHiddenInput}
                            ${cloneFromInput}
                            <div class="form-group">
                                <label class="form-label">${t('common.name')} *</label>
                                <input type="text" class="form-input" name="name" value="${endpoint ? this.escapeHtml(endpoint.name) : ''}" required ${isEdit ? 'readonly' : ''}>
                            </div>
                            <div class="form-group">
                                <label class="form-label">${t('endpoints.apiUrl')} *</label>
                                <input type="text" class="form-input" name="apiUrl" value="${endpoint ? this.escapeHtml(endpoint.apiUrl) : ''}" placeholder="${t('endpoints.apiUrlPlaceholder')}" required>
                            </div>
                            <div class="form-group" id="api-key-group">
                                <label class="form-label">${t('endpoints.apiKey')}</label>
                                <input type="password" class="form-input" name="apiKey" id="api-key-input" value="${apiKeyValue}" placeholder="${apiKeyPlaceholder}">
                                <small class="text-muted">${t('endpoints.apiKeyTokenPoolHint')}</small>
                                ${apiKeyHint}
                            </div>
                            <div class="form-group">
                                <label class="form-label">${t('endpoints.transformer')} *</label>
                                <select class="form-select" name="transformer" required>
                                    <option value="claude" ${endpoint?.transformer === 'claude' ? 'selected' : ''}>${t('transformers.claude')}</option>
                                    <option value="openai" ${endpoint?.transformer === 'openai' ? 'selected' : ''}>${t('transformers.openai')}</option>
                                    <option value="openai2" ${endpoint?.transformer === 'openai2' ? 'selected' : ''}>${t('transformers.openai2')}</option>
                                    <option value="gemini" ${endpoint?.transformer === 'gemini' ? 'selected' : ''}>${t('transformers.gemini')}</option>
                                    <option value="deepseek" ${endpoint?.transformer === 'deepseek' ? 'selected' : ''}>${t('transformers.deepseek')}</option>
                                </select>
                            </div>
                            <div class="form-group">
                                <label class="form-label">${t('endpoints.model')}</label>
                                <div style="display: flex; gap: 8px;">
                                    <input type="text" class="form-input" name="model" id="model-input" value="${endpoint ? this.escapeHtml(endpoint.model || '') : ''}" placeholder="${t('endpoints.modelPlaceholder')}" style="flex: 1;">
                                    <button type="button" class="btn btn-secondary" id="fetch-models-btn" style="white-space: nowrap;">
                                        ${t('endpoints.fetchModels')}
                                    </button>
                                </div>
                                <small class="text-muted">${t('endpoints.fetchModelsHint')}</small>
                            </div>
                            <div class="form-group">
                                <label class="form-label">${t('endpoints.remark')}</label>
                                <textarea class="form-textarea" name="remark">${endpoint ? this.escapeHtml(endpoint.remark || '') : ''}</textarea>
                            </div>
                            <div class="form-group">
                                <label>
                                    <input type="checkbox" class="form-checkbox" name="enabled" ${endpoint?.enabled !== false ? 'checked' : ''}>
                                    ${t('common.enabled')}
                                </label>
                            </div>
                        </form>
                    </div>
                    <div class="modal-footer">
                        <button class="btn btn-secondary" id="cancel-btn">${t('common.cancel')}</button>
                        <button class="btn btn-primary" id="save-btn">${isEdit ? t('common.update') : t('common.create')}</button>
                    </div>
                </div>
            </div>
        `;

        document.getElementById('close-modal').addEventListener('click', () => this.closeModal());
        document.getElementById('cancel-btn').addEventListener('click', () => this.closeModal());
        document.getElementById('save-btn').addEventListener('click', () => {
            const isClone = !!document.querySelector('input[name="isClone"]');
            this.saveEndpoint(isEdit, endpoint?.name, isClone);
        });
        document.getElementById('fetch-models-btn').addEventListener('click', () => this.fetchModels());
    }

    async fetchModels() {
        const apiUrlInput = document.querySelector('input[name="apiUrl"]');
        const apiKeyInput = document.querySelector('input[name="apiKey"]');
        const transformerSelect = document.querySelector('select[name="transformer"]');
        const modelInput = document.getElementById('model-input');
        const fetchBtn = document.getElementById('fetch-models-btn');

        const apiUrl = apiUrlInput.value.trim();
        const apiKey = apiKeyInput.value.trim();
        const transformer = transformerSelect.value;

        if (!apiUrl || !apiKey || apiKey === '****') {
            notifications.error(t('endpoints.enterApiUrlAndKey'));
            return;
        }

        try {
            fetchBtn.disabled = true;
            fetchBtn.textContent = 'Fetching...';

            const result = await api.fetchModels(apiUrl, apiKey, transformer);

            if (result.models && result.models.length > 0) {
                // Show model selection modal
                this.showModelSelectionModal(result.models, modelInput);
            } else {
                notifications.info(t('endpoints.noModelsFound'));
            }
        } catch (error) {
            notifications.error(`${t('endpoints.failedToFetchModels')}: ${error.message}`);
        } finally {
            fetchBtn.disabled = false;
            fetchBtn.textContent = 'Fetch Models';
        }
    }

    showModelSelectionModal(models, modelInput) {
        const modalContainer = document.getElementById('modal-container');
        const currentModal = modalContainer.querySelector('.modal');

        // Create a second modal overlay
        const modelModal = document.createElement('div');
        modelModal.className = 'modal-overlay no-overlay-dismiss';
        modelModal.style.zIndex = '1001';
        modelModal.innerHTML = `
            <div class="modal" style="max-width: 500px;">
                <div class="modal-header">
                    <h3 class="modal-title">${t('endpoints.selectModel')}</h3>
                    <button class="modal-close" id="close-model-modal">×</button>
                </div>
                <div class="modal-body">
                    <div style="max-height: 400px; overflow-y: auto;">
                        ${models.map(model => `
                            <div class="model-item" data-model="${this.escapeHtml(model)}">
                                <strong>${this.escapeHtml(model)}</strong>
                            </div>
                        `).join('')}
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" id="cancel-model-btn">${t('common.cancel')}</button>
                </div>
            </div>
        `;

        modalContainer.appendChild(modelModal);

        // Attach event listeners
        document.getElementById('close-model-modal').addEventListener('click', () => {
            modelModal.remove();
        });

        document.getElementById('cancel-model-btn').addEventListener('click', () => {
            modelModal.remove();
        });

        document.querySelectorAll('.model-item').forEach(item => {
            item.addEventListener('click', () => {
                const selectedModel = item.dataset.model;
                modelInput.value = selectedModel;
                notifications.success(`${t('notifications.modelSelected')} ${selectedModel}`);
                modelModal.remove();
            });

            item.addEventListener('mouseenter', () => {
                item.classList.add('is-hover');
            });

            item.addEventListener('mouseleave', () => {
                item.classList.remove('is-hover');
            });
        });
    }

    async saveEndpoint(isEdit, originalName, isClone = false) {
        const form = document.getElementById('endpoint-form');
        const formData = new FormData(form);

        const data = {
            name: formData.get('name'),
            apiUrl: formData.get('apiUrl'),
            apiKey: formData.get('apiKey'),
            transformer: formData.get('transformer'),
            model: formData.get('model'),
            remark: formData.get('remark'),
            enabled: formData.get('enabled') === 'on'
        };

        // If editing and API key is ****, don't send it (keep existing)
        if ((isEdit || isClone) && data.apiKey === '****') {
            delete data.apiKey;
        }

        // For clone mode, add cloneFrom field if available
        const cloneFromInput = document.querySelector('input[name="cloneFrom"]');
        if (isClone && cloneFromInput && cloneFromInput.value) {
            data.cloneFrom = cloneFromInput.value;
        }

        try {
            if (isEdit) {
                await api.updateEndpoint(originalName, data);
                notifications.success(t('notifications.endpointUpdated'));
            } else {
                await api.createEndpoint(data);
                notifications.success(t('notifications.endpointCreated'));
            }

            this.closeModal();
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToSave')}: ${error.message}`);
        }
    }

    async toggleEndpoint(name, enabled) {
        try {
            await api.toggleEndpoint(name, enabled);
            notifications.success(enabled ? t('notifications.endpointEnabled') : t('notifications.endpointDisabled'));
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToToggle')}: ${error.message}`);
            await this.loadEndpoints(); // Reload to reset toggle state
        }
    }

    async testEndpoint(name) {
        try {
            notifications.info(t('endpoints.testing'));
            const result = await api.testEndpoint(name);

            if (result.success) {
                this.saveTestStatus(name, true);
                notifications.success(`${t('notifications.testSuccessful')} ${result.latency}ms`);
                this.showTestResultModal(name, result);
                await this.loadEndpoints(); // Refresh to show test status
            } else {
                this.saveTestStatus(name, false);
                notifications.error(`${t('notifications.testFailed')} ${result.error}`);
                await this.loadEndpoints(); // Refresh to show test status
            }
        } catch (error) {
            this.saveTestStatus(name, false);
            notifications.error(`${t('endpoints.failedToTest')}: ${error.message}`);
            await this.loadEndpoints(); // Refresh to show test status
        }
    }

    showTestResultModal(name, result) {
        const modalContainer = document.getElementById('modal-container');

        modalContainer.innerHTML = `
            <div class="modal-overlay">
                <div class="modal">
                    <div class="modal-header">
                        <h3 class="modal-title">${t('endpoints.testResult')}: ${this.escapeHtml(name)}</h3>
                        <button class="modal-close" id="close-modal">×</button>
                    </div>
                    <div class="modal-body">
                        <div class="mb-2">
                            <strong>${t('common.status')}:</strong> <span class="badge badge-success">${t('common.success')}</span>
                        </div>
                        <div class="mb-2">
                            <strong>${t('endpoints.latency')}:</strong> ${result.latency}ms
                        </div>
                        <div class="mb-2">
                            <strong>${t('endpoints.response')}:</strong>
                            <div class="code-block mt-1">${this.escapeHtml(result.response || t('endpoints.noResponse'))}</div>
                        </div>
                    </div>
                    <div class="modal-footer">
                        <button class="btn btn-primary" id="close-btn">${t('common.close')}</button>
                    </div>
                </div>
            </div>
        `;

        document.getElementById('close-modal').addEventListener('click', () => this.closeModal());
        document.getElementById('close-btn').addEventListener('click', () => this.closeModal());
    }

    async deleteEndpoint(name) {
        if (!confirm(t('endpoints.confirmDelete').replace('{name}', name))) {
            return;
        }

        try {
            await api.deleteEndpoint(name);
            notifications.success(t('notifications.endpointDeleted'));
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToDelete')}: ${error.message}`);
        }
    }

    async cloneEndpoint(name) {
        const endpoint = this.endpoints.find(ep => ep.name === name);
        if (!endpoint) {
            notifications.error(t('endpoints.failedToClone'));
            return;
        }

        // Extract base name and add (Copy) suffix
        const baseName = name.replace(/\(Copy\)(?:\s+\d+)?$/, '').trim();
        let newName = `${baseName} (Copy)`;
        let counter = 1;
        while (this.endpoints.some(ep => ep.name === newName)) {
            newName = `${baseName} (Copy) ${counter}`;
            counter++;
        }

        // Create cloned endpoint - don't include apiKey, use cloneFrom instead
        const clonedEndpoint = {
            name: newName,
            apiUrl: endpoint.apiUrl,
            transformer: endpoint.transformer,
            model: endpoint.model,
            remark: endpoint.remark,
            enabled: endpoint.enabled,
            cloneFrom: name  // Reference to source endpoint
        };

        try {
            this.showEndpointModal(clonedEndpoint, true);
            notifications.success(t('notifications.endpointCloned'));
        } catch (error) {
            notifications.error(`${t('endpoints.failedToClone')}: ${error.message}`);
        }
    }

    async showTokenPoolModal(endpointName) {
        this.currentTokenPoolEndpoint = endpointName;

        try {
            const result = await api.getEndpointCredentials(endpointName);
            const credentials = result.credentials || [];
            const stats = result.stats || {};
            const modalContainer = document.getElementById('modal-container');

            modalContainer.innerHTML = `
                <div class="modal-overlay">
                    <div class="modal" style="max-width: 1200px; width: 95vw;">
                        <div class="modal-header">
                            <h3 class="modal-title">${t('endpoints.tokenPoolTitle')} ${this.escapeHtml(endpointName)}</h3>
                            <button class="modal-close" id="close-modal">×</button>
                        </div>
                        <div class="modal-body">
                            <div class="mb-2" style="font-size: 13px;">
                                <strong>${t('endpoints.total')}:</strong> ${stats.total || 0}
                                <span style="margin-left: 12px;"><strong>${t('endpoints.active')}:</strong> ${stats.active || 0}</span>
                                <span style="margin-left: 12px;"><strong>${t('endpoints.expiring')}:</strong> ${stats.expiring || 0}</span>
                                <span style="margin-left: 12px;"><strong>${t('endpoints.needRefresh')}:</strong> ${stats.needRefresh || 0}</span>
                                <span style="margin-left: 12px;"><strong>${t('endpoints.expired')}:</strong> ${stats.expired || 0}</span>
                                <span style="margin-left: 12px;"><strong>${t('endpoints.invalid')}:</strong> ${stats.invalid || 0}</span>
                            </div>

                            <div class="form-group">
                                <div style="display:flex; gap:8px; margin-bottom:10px;">
                                    <button type="button" class="btn btn-sm btn-primary" id="token-tab-simple">${t('endpoints.tokenTabSimple')}</button>
                                    <button type="button" class="btn btn-sm btn-secondary" id="token-tab-json">${t('endpoints.tokenTabJson')}</button>
                                </div>

                                <div id="token-simple-panel">
                                    <div id="token-rows"></div>
                                    <div style="margin-top:6px;">
                                        <button type="button" class="btn btn-sm btn-secondary" id="token-add-row-btn">＋ ${t('endpoints.addToken')}</button>
                                    </div>
                                    <label style="display:inline-flex; gap:8px; align-items:center; margin-top:10px;">
                                        <input type="checkbox" id="token-simple-overwrite">
                                        ${t('endpoints.overwriteExisting')}
                                    </label>
                                    <div style="margin-top:8px;">
                                        <button class="btn btn-primary" id="token-simple-import-btn">${t('common.import')}</button>
                                    </div>
                                </div>

                                <div id="token-json-panel" style="display:none;">
                                    <label class="form-label">${t('endpoints.batchImportJson')}</label>
                                    <textarea class="form-textarea" id="token-import-json" style="min-height: 140px;" placeholder='${t('endpoints.jsonPasteHint')}'></textarea>
                                    <label style="display: inline-flex; gap: 8px; align-items: center; margin-top: 8px;">
                                        <input type="checkbox" id="token-import-overwrite">
                                        ${t('endpoints.overwriteExisting')}
                                    </label>
                                    <div style="margin-top: 8px;">
                                        <button class="btn btn-primary" id="token-import-btn">${t('common.import')}</button>
                                    </div>
                                </div>
                            </div>

                            <div class="table-container">
                                <table class="table">
                                    <thead>
                                        <tr>
                                            <th>${t('endpoints.id')}</th>
                                            <th>${t('endpoints.account')}</th>
                                            <th>${t('endpoints.email')}</th>
                                            <th>${t('common.status')}</th>
                                            <th>${t('endpoints.expiresAt')}</th>
                                            <th>${t('endpoints.lastError')}</th>
                                            <th>${t('common.actions')}</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        ${this.renderCredentialRows(credentials)}
                                    </tbody>
                                </table>
                            </div>
                        </div>
                        <div class="modal-footer">
                            <button class="btn btn-secondary" id="refresh-token-pool-btn">${t('common.refresh')}</button>
                            <button class="btn btn-secondary" id="close-token-pool-btn">${t('common.close')}</button>
                        </div>
                    </div>
                </div>
            `;

            document.getElementById('close-modal').addEventListener('click', () => this.closeModal());
            document.getElementById('close-token-pool-btn').addEventListener('click', () => this.closeModal());
            document.getElementById('refresh-token-pool-btn').addEventListener('click', () => this.showTokenPoolModal(endpointName));
            document.getElementById('token-import-btn').addEventListener('click', () => this.importEndpointCredentials(endpointName));

            // Simple (form) token entry: tab switching, add-row, import.
            const simplePanel = document.getElementById('token-simple-panel');
            const jsonPanel = document.getElementById('token-json-panel');
            const tabSimple = document.getElementById('token-tab-simple');
            const tabJson = document.getElementById('token-tab-json');
            const selectTab = (simple) => {
                simplePanel.style.display = simple ? '' : 'none';
                jsonPanel.style.display = simple ? 'none' : '';
                tabSimple.className = `btn btn-sm ${simple ? 'btn-primary' : 'btn-secondary'}`;
                tabJson.className = `btn btn-sm ${simple ? 'btn-secondary' : 'btn-primary'}`;
            };
            tabSimple.addEventListener('click', () => selectTab(true));
            tabJson.addEventListener('click', () => selectTab(false));
            document.getElementById('token-add-row-btn').addEventListener('click', () => this.addTokenRow());
            document.getElementById('token-simple-import-btn').addEventListener('click', () => this.importTokensFromForm(endpointName));
            this.addTokenRow(); // start with one empty row

            document.querySelectorAll('.token-enable-toggle').forEach(toggle => {
                toggle.addEventListener('change', () => this.updateCredentialEnabled(endpointName, toggle.dataset.id, toggle.checked));
            });
            document.querySelectorAll('.token-update-btn').forEach(btn => {
                btn.addEventListener('click', () => this.updateCredentialToken(endpointName, btn.dataset.id));
            });
            document.querySelectorAll('.token-test-btn').forEach(btn => {
                btn.addEventListener('click', () => this.testCredential(endpointName, btn.dataset.id, btn));
            });
            document.querySelectorAll('.token-activate-btn').forEach(btn => {
                btn.addEventListener('click', () => this.activateCredential(endpointName, btn.dataset.id));
            });
            document.querySelectorAll('.token-delete-btn').forEach(btn => {
                btn.addEventListener('click', () => this.deleteCredential(endpointName, btn.dataset.id));
            });

            this.startTokenPoolCountdown(endpointName);
        } catch (error) {
            notifications.error(`${t('endpoints.failedToLoadTokenPool')}: ${error.message}`);
        }
    }

    renderCredentialRows(credentials) {
        if (!credentials || credentials.length === 0) {
            return `<tr><td colspan="7" class="text-center text-muted">${t('endpoints.noCredentials')}</td></tr>`;
        }

        return credentials.map(cred => `
            <tr>
                <td>${cred.id}</td>
                <td><code>${this.escapeHtml(cred.accountId || '-')}</code></td>
                <td>${this.escapeHtml(cred.email || '-')}</td>
                <td>${this.renderCredentialStatusBadge(cred.status)}${this.renderCredentialCooldown(cred)}</td>
                <td>${this.escapeHtml(this.formatDateTime(cred.expiresAt))}</td>
                <td style="max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="${this.escapeHtml(cred.lastError || '')}">
                    ${this.escapeHtml(cred.lastError || '-')}
                </td>
                <td>
                    <div class="flex gap-2">
                        <label style="display: inline-flex; align-items: center; gap: 6px; font-size: 12px;">
                            <input type="checkbox" class="token-enable-toggle" data-id="${cred.id}" ${cred.enabled ? 'checked' : ''}>
                            ${t('common.enabled')}
                        </label>
                        <button class="btn btn-sm btn-secondary token-update-btn" data-id="${cred.id}">${t('common.update')}</button>
                        <button class="btn btn-sm btn-secondary token-test-btn" data-id="${cred.id}">${t('endpoints.testEndpoint')}</button>
                        <button class="btn btn-sm btn-secondary token-activate-btn" data-id="${cred.id}">${t('endpoints.activate')}</button>
                        <button class="btn btn-sm btn-danger token-delete-btn" data-id="${cred.id}">${t('common.delete')}</button>
                    </div>
                </td>
            </tr>
        `).join('');
    }

    // Renders a per-token cooldown line ("⏳ Xh Ym left" + reset time) when the
    // credential is cooling down. The span carries the cooldown deadline so the
    // countdown timer can tick it down in place without re-fetching.
    renderCredentialCooldown(cred) {
        if (!cred.cooldownUntil) return '';
        const until = new Date(cred.cooldownUntil);
        const remaining = Math.floor((until.getTime() - Date.now()) / 1000);
        if (!(remaining > 0)) return '';

        const resetLocal = until.toLocaleString();
        const reason = cred.lastError ? this.escapeHtml(cred.lastError) : t('endpoints.limitedReason');
        return `
            <div style="font-size:11px;margin-top:4px;line-height:1.5;">
                <div>⏳ <span class="token-cooldown" data-until="${until.toISOString()}">${formatCountdown(remaining)}</span> ${t('endpoints.cooldownLeft')}</div>
                <div class="text-muted">${t('endpoints.resetAt')}: ${this.escapeHtml(resetLocal)}</div>
                <div class="text-muted" style="max-width:240px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="${reason}">${reason}</div>
            </div>`;
    }

    // Ticks every token cooldown countdown once per second. When a token's
    // cooldown expires, the whole pool view is refreshed so its status flips
    // back to active. Cleared on modal close / re-render.
    startTokenPoolCountdown(endpointName) {
        this.stopTokenPoolCountdown();
        this.tokenPoolTimer = setInterval(() => {
            const spans = document.querySelectorAll('.token-cooldown');
            if (spans.length === 0) return;
            let expired = false;
            spans.forEach(span => {
                const remaining = Math.floor((new Date(span.dataset.until).getTime() - Date.now()) / 1000);
                if (remaining > 0) {
                    span.textContent = formatCountdown(remaining);
                } else {
                    expired = true;
                }
            });
            if (expired) {
                this.showTokenPoolModal(endpointName);
            }
        }, 1000);
    }

    stopTokenPoolCountdown() {
        if (this.tokenPoolTimer) {
            clearInterval(this.tokenPoolTimer);
            this.tokenPoolTimer = null;
        }
    }

    renderCredentialStatusBadge(status) {
        const normalized = status || 'unknown';
        // Map credential states onto the terminal badge palette so the modal
        // matches the rest of the UI. All variants reuse the .badge baseline.
        const variantMap = {
            active:       'badge-success',
            expiring:     'badge-warning',
            need_refresh: 'badge-warning',
            expired:      'badge-danger',
            invalid:      'badge-danger',
            cooldown:     'badge-info',
            disabled:     'badge-neutral',
        };
        const variant = variantMap[normalized] || 'badge-neutral';
        return `<span class="badge ${variant}">${this.escapeHtml(normalized)}</span>`;
    }

    // Appends one empty token-entry row (token + optional label) to the simple
    // import panel.
    addTokenRow(token = '', label = '') {
        const container = document.getElementById('token-rows');
        if (!container) return;
        const row = document.createElement('div');
        row.className = 'token-entry-row';
        row.style.cssText = 'display:flex; gap:8px; margin-bottom:6px; align-items:center;';
        row.innerHTML = `
            <input type="text" class="form-input token-entry-value" placeholder="${t('endpoints.tokenValuePlaceholder')}" value="${this.escapeHtml(token)}" style="flex:3;">
            <input type="text" class="form-input token-entry-label" placeholder="${t('endpoints.tokenLabelPlaceholder')}" value="${this.escapeHtml(label)}" style="flex:1;">
            <button type="button" class="btn btn-sm btn-danger token-entry-remove" title="${t('common.delete')}">×</button>
        `;
        row.querySelector('.token-entry-remove').addEventListener('click', () => row.remove());
        container.appendChild(row);
    }

    // Collects the simple-panel rows into an items array and imports them,
    // reusing the same backend endpoint as the JSON import.
    async importTokensFromForm(endpointName) {
        const overwrite = document.getElementById('token-simple-overwrite')?.checked === true;
        const rows = Array.from(document.querySelectorAll('#token-rows .token-entry-row'));

        const items = [];
        rows.forEach((row, i) => {
            const token = row.querySelector('.token-entry-value')?.value.trim() || '';
            if (!token) return;
            const label = row.querySelector('.token-entry-label')?.value.trim() || '';
            items.push({
                access_token: token,
                type: 'api_key',
                account_id: label || `token-${Date.now()}-${i}`,
            });
        });

        if (items.length === 0) {
            notifications.warning(t('endpoints.pleaseEnterToken'));
            return;
        }

        try {
            const result = await api.importEndpointCredentials(endpointName, { items, overwrite });
            notifications.success(t('notifications.importDone')
                .replace('{created}', result.created || 0)
                .replace('{updated}', result.updated || 0)
                .replace('{skipped}', result.skipped || 0)
                .replace('{failed}', result.failed || 0));
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToImport')}: ${error.message}`);
        }
    }

    async importEndpointCredentials(endpointName) {
        const jsonInput = document.getElementById('token-import-json');
        const overwriteInput = document.getElementById('token-import-overwrite');
        const raw = (jsonInput?.value || '').trim();

        if (!raw) {
            notifications.warning(t('endpoints.pleasePasteJson'));
            return;
        }

        let payload;
        try {
            // Normalize smart quotes that often sneak in via copy/paste, which
            // otherwise make JSON.parse fail with a confusing error.
            const normalized = raw
                .replace(/[“”„‟″]/g, '"')
                .replace(/[‘’‚‛′]/g, "'");
            payload = JSON.parse(normalized);
        } catch (err) {
            notifications.error(`${t('endpoints.invalidJson')}: ${err.message}`);
            return;
        }

        let requestBody;
        if (Array.isArray(payload)) {
            requestBody = { items: payload, overwrite: overwriteInput?.checked === true };
        } else if (payload.items && Array.isArray(payload.items)) {
            requestBody = { ...payload, overwrite: overwriteInput?.checked === true };
        } else {
            requestBody = { items: [payload], overwrite: overwriteInput?.checked === true };
        }

        try {
            const result = await api.importEndpointCredentials(endpointName, requestBody);
            notifications.success(t('notifications.importDone').replace('{created}', result.created || 0).replace('{updated}', result.updated || 0).replace('{skipped}', result.skipped || 0).replace('{failed}', result.failed || 0));
            jsonInput.value = '';
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToImport')}: ${error.message}`);
        }
    }

    async updateCredentialEnabled(endpointName, credentialId, enabled) {
        try {
            await api.updateEndpointCredential(endpointName, credentialId, { enabled });
            notifications.success(enabled ? t('notifications.credentialEnabled') : t('notifications.credentialDisabled'));
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToUpdateCredential')}: ${error.message}`);
            await this.showTokenPoolModal(endpointName);
        }
    }

    // Tests a single token from the pool with a real request and reports the
    // outcome inline. The button is disabled while the test is in flight so the
    // user can't fire duplicate probes.
    async testCredential(endpointName, credentialId, btn) {
        const originalText = btn?.textContent;
        try {
            if (btn) { btn.disabled = true; btn.textContent = t('endpoints.testing'); }
            const result = await api.testEndpointCredential(endpointName, credentialId);
            if (result.success) {
                notifications.success(`${t('notifications.testSuccessful')} ${result.latency}ms`);
            } else {
                notifications.error(`${t('notifications.testFailed')}: ${result.error || ''}`);
            }
        } catch (error) {
            notifications.error(`${t('notifications.testFailed')}: ${error.message}`);
        } finally {
            if (btn) { btn.disabled = false; btn.textContent = originalText; }
        }
    }

    async activateCredential(endpointName, credentialId) {
        try {
            await api.updateEndpointCredential(endpointName, credentialId, { status: 'active' });
            notifications.success(t('notifications.credentialActivated'));
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToActivateCredential')}: ${error.message}`);
        }
    }

    async updateCredentialToken(endpointName, credentialId) {
        const accessToken = prompt(t('endpoints.enterAccessToken'));
        if (!accessToken) {
            return;
        }

        const expiresAt = prompt(t('endpoints.enterExpiresAt'), '');
        const payload = {
            accessToken: accessToken.trim(),
            status: 'active'
        };
        if (expiresAt && expiresAt.trim()) {
            payload.expiresAt = expiresAt.trim();
        }

        try {
            await api.updateEndpointCredential(endpointName, credentialId, payload);
            notifications.success(t('notifications.tokenUpdated'));
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToUpdateToken')}: ${error.message}`);
        }
    }

    async deleteCredential(endpointName, credentialId) {
        if (!confirm(t('endpoints.confirmDeleteCredential').replace('{id}', credentialId))) {
            return;
        }

        try {
            await api.deleteEndpointCredential(endpointName, credentialId);
            notifications.success(t('notifications.credentialDeleted'));
            await this.showTokenPoolModal(endpointName);
            await this.loadEndpoints();
        } catch (error) {
            notifications.error(`${t('endpoints.failedToDeleteCredential')}: ${error.message}`);
        }
    }

    formatDateTime(value) {
        if (!value) {
            return '-';
        }
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) {
            return value;
        }
        return date.toLocaleString();
    }

    closeModal() {
        this.stopTokenPoolCountdown();
        document.getElementById('modal-container').innerHTML = '';
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

export const endpoints = new Endpoints();