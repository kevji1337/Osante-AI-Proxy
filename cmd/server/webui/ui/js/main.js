import { router } from './router.js';
import { state } from './state.js';
import { initLanguage, loadTranslations, t } from './utils/i18n.js';
import { dashboard } from './components/dashboard.js';
import { endpoints } from './components/endpoints.js';
import { stats } from './components/stats.js';
import { testing } from './components/testing.js';
import { logs } from './components/logs.js';
import { inspector } from './components/inspector.js';
import { installKeyboardShortcuts } from './shortcuts.js';
import en from './i18n/en.js';

// English-only.
loadTranslations({ en });
initLanguage();

// Initialize theme.
//
// The terminal aesthetic is dark-by-default. `light-theme` is the opt-in
// override applied to <body>. We keep the legacy `dark-theme` class around
// only for compatibility with any external bookmarklets or styles — the CSS
// never reads it any more.
function initTheme() {
    const savedTheme = localStorage.getItem('theme') || 'dark';
    const isLight = savedTheme === 'light';
    document.body.classList.toggle('light-theme', isLight);
    document.body.classList.toggle('dark-theme', !isLight);

    const themeToggle = document.getElementById('theme-toggle');
    const iconEl = themeToggle.querySelector('.icon');

    const setIcon = (isLightNow) => {
        // ◐ for light, ● for dark — keep terminal glyphs instead of emoji so
        // they match the rest of the UI's mono palette.
        iconEl.textContent = isLightNow ? '◐' : '●';
    };

    setIcon(isLight);

    themeToggle.addEventListener('click', () => {
        const nowLight = document.body.classList.toggle('light-theme');
        document.body.classList.toggle('dark-theme', !nowLight);
        localStorage.setItem('theme', nowLight ? 'light' : 'dark');
        setIcon(nowLight);
    });
}

// Apply translations to the sidebar (subtitle + nav labels).
function updateSidebarTranslations() {
    const subtitle = document.getElementById('sidebar-subtitle');
    if (subtitle) {
        subtitle.textContent = t('dashboard.subtitle');
    }
    document.querySelectorAll('.nav-label').forEach(el => {
        const key = el.getAttribute('data-i18n');
        if (key) {
            el.textContent = t(key);
        }
    });
}

// Initialize real-time updates
function initRealtime() {
    const eventSource = new EventSource('/api/events');

    eventSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data);

            if (data.type === 'stats') {
                state.update('stats', data.stats);
                state.update('currentEndpoint', data.currentEndpoint);
            }
        } catch (error) {
            console.error('Failed to parse SSE event:', error);
        }
    };

    eventSource.onerror = (error) => {
        console.error('SSE connection error:', error);
        setTimeout(() => {
            if (eventSource.readyState === EventSource.CLOSED) {
                initRealtime();
            }
        }, 5000);
    };
}

// Single tab coordinator to prevent multiple duplicate tabs
function initSingleTabCoordinator() {
    if (!('BroadcastChannel' in window)) {
        return;
    }

    const channel = new BroadcastChannel('osante_single_tab_channel');
    const tabId = 'tab_' + Math.random().toString(36).substring(2, 9) + '_' + Date.now();
    let isPrimary = false;

    // Check if another tab exists
    channel.postMessage({ type: 'PING', senderTabId: tabId });

    channel.onmessage = (event) => {
        const msg = event.data;
        if (!msg) return;

        if (msg.type === 'PING' && msg.senderTabId !== tabId) {
            // We are an existing tab, announce ourselves
            channel.postMessage({ type: 'PONG', targetTabId: msg.senderTabId });
            
            // Wake up / re-focus cue: flash title
            const origTitle = document.title;
            document.title = '>>> OSANTE // proxy <<<';
            setTimeout(() => { document.title = origTitle; }, 1500);

            // Re-sync with proxy if needed
            if (router && router.currentView) {
                router.handleRoute();
            }
        } else if (msg.type === 'PONG' && msg.targetTabId === tabId) {
            // Another tab is already active!
            console.log('[Osante] Existing tab detected. Closing duplicate tab.');
            window.close();

            // If browser blocks window.close() for externally opened tabs, render notice
            setTimeout(() => {
                const appEl = document.getElementById('app');
                if (appEl && !isPrimary) {
                    appEl.innerHTML = `
                        <div style="display:flex; align-items:center; justify-content:center; min-height:100vh; width:100%; font-family:var(--font-mono); color:var(--text-primary); padding:2rem; box-sizing:border-box;">
                            <div style="border:1px dashed var(--border-color); padding:2.5rem; background:var(--bg-secondary); max-width:480px; text-align:center;">
                                <h2 style="color:var(--acid); margin-bottom:1rem; font-size:1.4rem;">[ SESSION ALREADY OPEN ]</h2>
                                <p style="margin-bottom:1.5rem; color:var(--text-secondary); line-height:1.6; font-size:13px;">
                                    Osante Web UI is already open in another tab in your browser.
                                </p>
                                <button id="takeover-tab-btn" class="btn btn-primary" style="margin-bottom:1rem; width:100%;">
                                    Use This Tab Instead
                                </button>
                                <div style="font-size:11px; color:var(--text-dim);">
                                    You can close this tab safely.
                                </div>
                            </div>
                        </div>
                    `;
                    document.getElementById('takeover-tab-btn')?.addEventListener('click', () => {
                        isPrimary = true;
                        location.reload();
                    });
                }
            }, 100);
        }
    };
}

// Initialize application
function init() {
    initSingleTabCoordinator();
    router.register('dashboard', dashboard);
    router.register('endpoints', endpoints);
    router.register('stats', stats);
    router.register('testing', testing);
    router.register('logs', logs);
    router.register('inspector', inspector);

    initTheme();
    updateSidebarTranslations();
    router.init();
    initRealtime();
    installKeyboardShortcuts();

    console.log('Osante Proxy admin initialized');
}

// Start application when DOM is ready
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
