import { router } from './router.js';
import { state } from './state.js';
import { initLanguage, loadTranslations, t } from './utils/i18n.js';
import { dashboard } from './components/dashboard.js';
import { endpoints } from './components/endpoints.js';
import { stats } from './components/stats.js';
import { testing } from './components/testing.js';
import { logs } from './components/logs.js';
import en from './i18n/en.js';

// English-only.
loadTranslations({ en });
initLanguage();

// Initialize theme
function initTheme() {
    const savedTheme = localStorage.getItem('theme') || 'light';
    document.body.classList.toggle('dark-theme', savedTheme === 'dark');

    const themeToggle = document.getElementById('theme-toggle');
    themeToggle.addEventListener('click', () => {
        const isDark = document.body.classList.toggle('dark-theme');
        localStorage.setItem('theme', isDark ? 'dark' : 'light');
        themeToggle.querySelector('.icon').textContent = isDark ? '☀️' : '🌙';
    });

    themeToggle.querySelector('.icon').textContent = savedTheme === 'dark' ? '☀️' : '🌙';
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

    initTheme();
    updateSidebarTranslations();
    router.init();
    initRealtime();

    console.log('Osante Proxy admin initialized');
}

// Start application when DOM is ready
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
