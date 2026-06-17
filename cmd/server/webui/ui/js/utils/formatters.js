// Utility functions for formatting data

export function formatNumber(num) {
    if (num >= 1000000) {
        return (num / 1000000).toFixed(1) + 'M';
    }
    if (num >= 1000) {
        return (num / 1000).toFixed(1) + 'K';
    }
    return num.toString();
}

export function formatTokens(tokens) {
    return formatNumber(tokens);
}

export function formatPercentage(value) {
    const sign = value >= 0 ? '+' : '';
    return `${sign}${value.toFixed(1)}%`;
}

export function formatDate(dateString) {
    const date = new Date(dateString);
    return date.toLocaleDateString();
}

export function formatDateTime(dateString) {
    const date = new Date(dateString);
    return date.toLocaleString();
}

export function formatLatency(ms) {
    if (ms < 1000) {
        return `${ms}ms`;
    }
    return `${(ms / 1000).toFixed(2)}s`;
}

export function getTransformerLabel(transformer) {
    const labels = {
        'claude': 'Claude',
        'openai': 'OpenAI',
        'openai2': 'OpenAI Responses',
        'gemini': 'Gemini',
        'gitlabduo': 'GitLab Duo',
        'deepseek': 'DeepSeek'
    };
    return labels[transformer] || transformer;
}

export function getStatusBadge(enabled) {
    if (enabled) {
        return '<span class="badge badge-success">Enabled</span>';
    }
    return '<span class="badge badge-danger">Disabled</span>';
}

// Formats a remaining-seconds duration as a short human countdown,
// e.g. 12180 -> "3h 23m", 312 -> "5m 12s", 0 -> "0s".
export function formatCountdown(totalSeconds) {
    let s = Math.max(0, Math.floor(totalSeconds));
    const h = Math.floor(s / 3600);
    s -= h * 3600;
    const m = Math.floor(s / 60);
    s -= m * 60;

    if (h > 0) {
        return `${h}h ${m}m`;
    }
    if (m > 0) {
        return `${m}m ${s}s`;
    }
    return `${s}s`;
}

export function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
