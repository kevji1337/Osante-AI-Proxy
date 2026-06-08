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

// Initialize application
function init() {
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
