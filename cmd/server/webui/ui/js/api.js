// API Client for Osante Proxy
class APIClient {
    constructor(baseURL = '/api') {
        this.baseURL = baseURL;
    }

    async request(method, path, data = null) {
        const options = {
            method,
            headers: {
                'Content-Type': 'application/json'
            }
        };

        if (data) {
            options.body = JSON.stringify(data);
        }

        try {
            const response = await fetch(`${this.baseURL}${path}`, options);
            const result = await response.json();

            if (!response.ok) {
                throw new Error(result.error || 'Request failed');
            }

            return result.data || result;
        } catch (error) {
            console.error(`API Error [${method} ${path}]:`, error);
            throw error;
        }
    }

    // Endpoint management
    async getEndpoints() {
        return this.request('GET', '/endpoints');
    }

    async createEndpoint(data) {
        return this.request('POST', '/endpoints', data);
    }

    async updateEndpoint(name, data) {
        return this.request('PUT', `/endpoints/${encodeURIComponent(name)}`, data);
    }

    async deleteEndpoint(name) {
        return this.request('DELETE', `/endpoints/${encodeURIComponent(name)}`);
    }

    async toggleEndpoint(name, enabled) {
        return this.request('PATCH', `/endpoints/${encodeURIComponent(name)}/toggle`, { enabled });
    }

    async testEndpoint(name) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/test`);
    }

    async reorderEndpoints(names) {
        return this.request('POST', '/endpoints/reorder', { names });
    }

    async getCurrentEndpoint() {
        return this.request('GET', '/endpoints/current');
    }

    async switchEndpoint(name) {
        return this.request('POST', '/endpoints/switch', { name });
    }

    async fetchModels(apiUrl, apiKey, transformer) {
        return this.request('POST', '/endpoints/fetch-models', { apiUrl, apiKey, transformer });
    }

    async getEndpointCredentials(name) {
        return this.request('GET', `/endpoints/${encodeURIComponent(name)}/credentials`);
    }

    async importEndpointCredentials(name, data) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/credentials/import`, data);
    }

    async updateEndpointCredential(name, id, data) {
        return this.request('PATCH', `/endpoints/${encodeURIComponent(name)}/credentials/${id}`, data);
    }

    async deleteEndpointCredential(name, id) {
        return this.request('DELETE', `/endpoints/${encodeURIComponent(name)}/credentials/${id}`);
    }

    async testEndpointCredential(name, id) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/credentials/${id}/test`);
    }

    async getLogs(level = 'INFO', limit = 500) {
        const params = new URLSearchParams({ level, limit: String(limit) });
        return this.request('GET', `/logs?${params.toString()}`);
    }

    // Live request trace ring (the Inspector view)
    async getTrace(limit = 50) {
        const params = new URLSearchParams({ limit: String(limit) });
        return this.request('GET', `/trace?${params.toString()}`);
    }

    // Detailed health snapshot — JSON, served by the proxy, not /api
    async getHealth() {
        const resp = await fetch('/health');
        if (!resp.ok) throw new Error(`Health check failed: HTTP ${resp.status}`);
        return resp.json();
    }

    // Quick actions
    async clearCooldowns()  { return this.request('POST', '/actions/clear-cooldowns'); }
    async flushStats()      { return this.request('POST', '/actions/flush-stats'); }
    // Returns the download URL — the browser handles the file save itself.
    exportBackupURL() { return `${this.baseURL}/actions/export-backup`; }

    // Statistics
    async getStatsSummary() {
        return this.request('GET', '/stats/summary');
    }

    async getStatsDaily() {
        return this.request('GET', '/stats/daily');
    }

    async getStatsWeekly() {
        return this.request('GET', '/stats/weekly');
    }

    async getStatsMonthly() {
        return this.request('GET', '/stats/monthly');
    }

    async getStatsTrends() {
        return this.request('GET', '/stats/trends');
    }

    // Configuration
    async getConfig() {
        return this.request('GET', '/config');
    }

    async updateConfig(data) {
        return this.request('PUT', '/config', data);
    }

    async getPort() {
        return this.request('GET', '/config/port');
    }

    async updatePort(port) {
        return this.request('PUT', '/config/port', { port });
    }

    async getLogLevel() {
        return this.request('GET', '/config/log-level');
    }

    async updateLogLevel(logLevel) {
        return this.request('PUT', '/config/log-level', { logLevel });
    }
}

export const api = new APIClient();
