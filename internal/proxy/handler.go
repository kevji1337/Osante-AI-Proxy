package proxy

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
	"github.com/kevji1337/Osante-AI-Proxy/internal/tokencount"
)

// healthEndpointStatus is the per-endpoint slice of /health output.
type healthEndpointStatus struct {
	Name                   string `json:"name"`
	Enabled                bool   `json:"enabled"`
	Transformer            string `json:"transformer,omitempty"`
	InCooldown             bool   `json:"in_cooldown"`
	CooldownRemainingSec   int64  `json:"cooldown_remaining_sec,omitempty"`
	CooldownReason         string `json:"cooldown_reason,omitempty"`
	LastError              string `json:"last_error,omitempty"`
	LastErrorAtUnix        int64  `json:"last_error_at_unix,omitempty"`
	HasError               bool   `json:"has_error"`
	TokenPoolTotal         int    `json:"token_pool_total,omitempty"`
	TokenPoolActive        int    `json:"token_pool_active,omitempty"`
	TokenPoolCooldown      int    `json:"token_pool_cooldown,omitempty"`
	TokenPoolInvalid       int    `json:"token_pool_invalid,omitempty"`
	TokenPoolExpired       int    `json:"token_pool_expired,omitempty"`
	TokenPoolExpiring      int    `json:"token_pool_expiring,omitempty"`
	TokenPoolNeedRefresh   int    `json:"token_pool_need_refresh,omitempty"`
	TokenPoolDisabled      int    `json:"token_pool_disabled,omitempty"`
}

// handleHealth handles health check requests with a detailed JSON snapshot
// suitable for prometheus-style scrapers and watch scripts.
//
// The endpoint stays unauthenticated since the entire admin API is
// unauthenticated by design on loopback. API keys are NOT exposed.
func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	endpoints := p.config.GetEndpoints()
	enabledCount := 0
	runtimeMap := p.EndpointRuntimeSnapshot()
	now := time.Now().UTC()

	statuses := make([]healthEndpointStatus, 0, len(endpoints))
	overallHealthy := true
	for _, ep := range endpoints {
		st := healthEndpointStatus{
			Name:        ep.Name,
			Enabled:     ep.Enabled,
			Transformer: ep.Transformer,
		}
		if ep.Enabled {
			enabledCount++
		}

		if rt, ok := runtimeMap[ep.Name]; ok {
			st.HasError = rt.HasError
			st.LastError = rt.LastError
			if !rt.LastErrorAt.IsZero() {
				st.LastErrorAtUnix = rt.LastErrorAt.Unix()
			}
			if !rt.CooldownUntil.IsZero() && rt.CooldownUntil.After(now) {
				st.InCooldown = true
				st.CooldownRemainingSec = int64(rt.CooldownUntil.Sub(now).Seconds())
				st.CooldownReason = rt.CooldownReason
			}
		}

		// Token-pool stats only when the endpoint is in token-pool mode and we
		// have a storage backend. Best-effort: any error here is non-fatal.
		if p.storage != nil && config.IsTokenPoolAuthMode(ep.AuthMode) {
			if pool, err := p.storage.GetTokenPoolStats(ep.Name); err == nil {
				st.TokenPoolTotal = pool.Total
				st.TokenPoolActive = pool.Active
				st.TokenPoolCooldown = pool.Cooldown
				st.TokenPoolInvalid = pool.Invalid
				st.TokenPoolExpired = pool.Expired
				st.TokenPoolExpiring = pool.Expiring
				st.TokenPoolNeedRefresh = pool.NeedRefresh
				st.TokenPoolDisabled = pool.Disabled
			}
		}

		if ep.Enabled && (st.InCooldown || st.HasError) {
			overallHealthy = false
		}
		statuses = append(statuses, st)
	}

	overallStatus := "healthy"
	if !overallHealthy {
		overallStatus = "degraded"
	}
	if enabledCount == 0 {
		overallStatus = "no_endpoints"
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	response := map[string]interface{}{
		"status":            overallStatus,
		"version":           "0.1",
		"uptime_sec":        int64(time.Since(p.startedAt).Seconds()),
		"started_at_unix":   p.startedAt.Unix(),
		"enabled_endpoints": enabledCount,
		"total_endpoints":   len(endpoints),
		"endpoints":         statuses,
		"runtime": map[string]interface{}{
			"goroutines":      runtime.NumGoroutine(),
			"go_version":      runtime.Version(),
			"heap_alloc_mb":   float64(mem.HeapAlloc) / 1024.0 / 1024.0,
			"heap_sys_mb":     float64(mem.HeapSys) / 1024.0 / 1024.0,
			"num_gc":          mem.NumGC,
		},
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// maskAPIKey was used by the old verbose /health response that returned full
// endpoint configs. The new /health output only emits per-endpoint runtime
// stats — no API keys, no model strings — so the masking helper is no longer
// needed. Kept in version control history in case some external consumer
// still reads /api/endpoints (which DOES return configs and uses its own
// masking).

// handleStats handles statistics requests
func (p *Proxy) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := p.GetStats()
	json.NewEncoder(w).Encode(stats)
}

// GetStats returns current statistics
func (p *Proxy) GetStats() *Stats {
	return p.stats
}

// handleCountTokens handles token counting requests
func (p *Proxy) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model    string                   `json:"model"`
		System   interface{}              `json:"system,omitempty"`
		Messages []map[string]interface{} `json:"messages"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Error("Failed to decode count_tokens request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	systemText := ""
	if req.System != nil {
		switch sys := req.System.(type) {
		case string:
			systemText = sys
		case []interface{}:
			for _, block := range sys {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if text, ok := blockMap["text"].(string); ok {
						systemText += text + "\n"
					}
				}
			}
		}
	}

	totalTokens := 0
	if systemText != "" {
		totalTokens += tokencount.EstimateOutputTokens(systemText)
	}

	for _, msg := range req.Messages {
		content, ok := msg["content"]
		if !ok {
			continue
		}

		switch c := content.(type) {
		case string:
			totalTokens += tokencount.EstimateOutputTokens(c)
		case []interface{}:
			for _, block := range c {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if text, ok := blockMap["text"].(string); ok {
						totalTokens += tokencount.EstimateOutputTokens(text)
					}
				}
			}
		}
	}

	response := map[string]interface{}{
		"input_tokens": totalTokens,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UpdateConfig updates the proxy configuration
func (p *Proxy) UpdateConfig(cfg *config.Config) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Save current endpoint name
	var currentEndpointName string
	if p.config != nil {
		endpoints := p.getEnabledEndpoints()
		if len(endpoints) > 0 && p.currentIndex < len(endpoints) {
			currentEndpointName = endpoints[p.currentIndex].Name
		}
	}

	p.config = cfg

	// Try to find the previous current endpoint in new config
	newEndpoints := p.getEnabledEndpoints()
	if currentEndpointName != "" && len(newEndpoints) > 0 {
		found := false
		for i, ep := range newEndpoints {
			if ep.Name == currentEndpointName {
				p.currentIndex = i
				found = true
				logger.Debug("[CONFIG UPDATE] Preserved current endpoint: %s at index %d", currentEndpointName, i)
				break
			}
		}
		if !found {
			p.currentIndex = 0
			logger.Debug("[CONFIG UPDATE] Current endpoint '%s' not found, reset to index 0", currentEndpointName)
		}
	} else {
		p.currentIndex = 0
	}

	// Clear models cache to force refresh with new endpoints
	if p.modelsCache != nil {
		p.modelsCache.Clear()
		logger.Debug("[CONFIG UPDATE] Cleared models cache")
	}

	logger.Info("Configuration updated: %d endpoints configured", len(cfg.GetEndpoints()))
	return nil
}
