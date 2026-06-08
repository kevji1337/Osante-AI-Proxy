// Global keyboard shortcuts. Terminal aesthetic deserves vim-ish nav —
// j/k for row movement, Enter to drill in, 1–6 for view jumps, / for search
// focus, ? for a cheat-sheet overlay, Esc to dismiss modals.
//
// Shortcuts are no-ops while typing in form fields, so the user's search
// query / endpoint editor doesn't lose keystrokes.
import { router } from './router.js';

const VIEW_KEYS = {
    '1': 'dashboard',
    '2': 'endpoints',
    '3': 'stats',
    '4': 'testing',
    '5': 'logs',
    '6': 'inspector',
};

let helpEl = null;

function isTypingTarget(el) {
    if (!el) return false;
    if (el.isContentEditable) return true;
    const tag = (el.tagName || '').toUpperCase();
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
    return false;
}

function modalOpen() {
    const c = document.getElementById('modal-container');
    return c && c.children.length > 0;
}

function focusSearch() {
    // Best-effort: find a likely "search" input on the current view.
    const candidates = [
        '#logs-search',
        '#stats-search',
        'input[type="search"]',
        'input[placeholder*="earch"]',
    ];
    for (const sel of candidates) {
        const el = document.querySelector(sel);
        if (el) {
            el.focus();
            el.select?.();
            return true;
        }
    }
    return false;
}

// Move focus among "navigable" rows of the current view. We use elements
// tagged with `data-kbnav` (e.g. .endpoint-row, .trace-row). The first call
// focuses the first row; subsequent j/k moves up/down.
function navigateRows(direction) {
    const nodes = Array.from(document.querySelectorAll('[data-kbnav]'));
    if (nodes.length === 0) return false;

    let idx = nodes.findIndex(n => n.classList.contains('is-kbnav-focused'));
    if (idx < 0) {
        idx = direction > 0 ? 0 : nodes.length - 1;
    } else {
        nodes[idx].classList.remove('is-kbnav-focused');
        idx = Math.max(0, Math.min(nodes.length - 1, idx + direction));
    }
    nodes[idx].classList.add('is-kbnav-focused');
    nodes[idx].scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    return true;
}

function activateFocusedRow() {
    const target = document.querySelector('[data-kbnav].is-kbnav-focused');
    if (!target) return false;
    // Prefer a primary action button inside the row, otherwise click the row
    // itself (which is what mouse users do).
    const action = target.querySelector('[data-kbnav-primary]') || target;
    action.click();
    return true;
}

function buildHelp() {
    const rows = [
        ['1–6',     'Jump to view (Dashboard / Endpoints / Stats / Testing / Logs / Inspector)'],
        ['j / ↓',   'Focus next row'],
        ['k / ↑',   'Focus previous row'],
        ['Enter',   'Activate focused row (open / edit / drill in)'],
        ['/',       'Focus search / filter input'],
        ['Esc',     'Close modal · clear row focus'],
        ['t',       'Toggle theme (dark ⇄ light)'],
        ['?',       'Show / hide this help'],
    ];
    const el = document.createElement('div');
    el.className = 'kbd-help-overlay';
    el.innerHTML = `
        <div class="kbd-help">
            <div class="kbd-help-header">▸ KEYBOARD SHORTCUTS<span class="kbd-help-close" title="Close">×</span></div>
            <table class="kbd-help-table">
                <tbody>
                    ${rows.map(([k, d]) => `<tr><td><kbd>${escapeHtml(k)}</kbd></td><td>${escapeHtml(d)}</td></tr>`).join('')}
                </tbody>
            </table>
            <div class="kbd-help-foot">Shortcuts ignored while typing in inputs.</div>
        </div>
    `;
    el.addEventListener('click', (e) => {
        if (e.target === el || e.target.classList.contains('kbd-help-close')) {
            toggleHelp(false);
        }
    });
    return el;
}

function toggleHelp(show) {
    const want = show ?? !helpEl?.isConnected;
    if (want) {
        if (!helpEl) helpEl = buildHelp();
        if (!helpEl.isConnected) document.body.appendChild(helpEl);
    } else if (helpEl && helpEl.isConnected) {
        helpEl.remove();
    }
}

function escapeHtml(s) {
    const d = document.createElement('div');
    d.textContent = String(s);
    return d.innerHTML;
}

export function installKeyboardShortcuts() {
    document.addEventListener('keydown', (e) => {
        // Modifier-combos belong to the browser/OS — don't hijack.
        if (e.ctrlKey || e.metaKey || e.altKey) return;
        if (isTypingTarget(e.target)) {
            // Esc still cancels typing focus.
            if (e.key === 'Escape') e.target.blur();
            return;
        }

        switch (e.key) {
            case 'j':
            case 'ArrowDown':
                if (navigateRows(1)) e.preventDefault();
                return;
            case 'k':
            case 'ArrowUp':
                if (navigateRows(-1)) e.preventDefault();
                return;
            case 'Enter':
                if (activateFocusedRow()) e.preventDefault();
                return;
            case '/':
                if (focusSearch()) e.preventDefault();
                return;
            case 'Escape':
                if (helpEl?.isConnected) { toggleHelp(false); e.preventDefault(); return; }
                document.querySelectorAll('[data-kbnav].is-kbnav-focused')
                    .forEach(n => n.classList.remove('is-kbnav-focused'));
                return;
            case '?':
                toggleHelp();
                e.preventDefault();
                return;
            case 't':
                document.getElementById('theme-toggle')?.click();
                e.preventDefault();
                return;
        }

        if (VIEW_KEYS[e.key]) {
            router.navigate(VIEW_KEYS[e.key]);
            e.preventDefault();
        }
    });
}
