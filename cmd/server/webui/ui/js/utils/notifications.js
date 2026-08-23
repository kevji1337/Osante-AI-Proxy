// Toast notification system
class NotificationManager {
    constructor() {
        this._container = null;
    }

    // Resolved lazily: the constructor used to capture #toast-container at
    // module-eval time and appendChild on null if the node was missing, killing
    // every later notification. Recreating it is cheap and keeps the UI working
    // even if the markup changes.
    get container() {
        if (this._container && this._container.isConnected) {
            return this._container;
        }
        let node = document.getElementById('toast-container');
        if (!node) {
            node = document.createElement('div');
            node.id = 'toast-container';
            document.body.appendChild(node);
        }
        this._container = node;
        return node;
    }

    show(message, type = 'info', duration = 3000) {
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;

        const icons = {
            success: '✓',
            error: '✕',
            warning: '⚠',
            info: 'ℹ'
        };

        const icon = document.createElement('span');
        icon.className = 'toast-icon';
        icon.textContent = icons[type] || icons.info;

        const content = document.createElement('div');
        content.className = 'toast-content';
        const text = document.createElement('div');
        text.className = 'toast-message';
        // textContent, not innerHTML: messages carry upstream error strings, so
        // markup in them would break the layout at best and inject at worst.
        text.textContent = message === undefined || message === null ? '' : String(message);
        content.appendChild(text);

        const closeBtn = document.createElement('button');
        closeBtn.className = 'toast-close';
        closeBtn.type = 'button';
        closeBtn.setAttribute('aria-label', 'Dismiss notification');
        closeBtn.textContent = '×';
        closeBtn.addEventListener('click', () => this.remove(toast));

        toast.append(icon, content, closeBtn);
        this.container.appendChild(toast);

        if (duration > 0) {
            setTimeout(() => this.remove(toast), duration);
        }

        return toast;
    }

    remove(toast) {
        // The auto-dismiss timer and the close button both land here; without
        // the guard a manual close replays the exit animation 3s later.
        if (!toast || toast.dataset.removing === '1') {
            return;
        }
        toast.dataset.removing = '1';
        toast.style.animation = 'slideInRight 0.3s reverse';
        setTimeout(() => {
            if (toast.parentNode) {
                toast.parentNode.removeChild(toast);
            }
        }, 300);
    }

    success(message, duration) {
        return this.show(message, 'success', duration);
    }

    error(message, duration) {
        return this.show(message, 'error', duration);
    }

    warning(message, duration) {
        return this.show(message, 'warning', duration);
    }

    info(message, duration) {
        return this.show(message, 'info', duration);
    }
}

export const notifications = new NotificationManager();
