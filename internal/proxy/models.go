package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// modelsFetchBudget bounds the whole /v1/models fan-out. Long enough for a slow
// upstream, short enough that a hung endpoint cannot pin the request.
const modelsFetchBudget = 15 * time.Second

// ModelInfo represents a single model information
type ModelInfo struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	EndpointID string `json:"endpoint_id"` // Source endpoint identifier
}

// ModelsCache represents cached models data with TTL
type ModelsCache struct {
	data      []ModelInfo
	updatedAt time.Time
	ttl       time.Duration
	mu        sync.RWMutex
}

// NewModelsCache creates a new models cache
func NewModelsCache(ttlMinutes int) *ModelsCache {
	if ttlMinutes <= 0 {
		ttlMinutes = 30 // Default 30 minutes
	}
	return &ModelsCache{
		data:      []ModelInfo{},
		updatedAt: time.Time{},
		ttl:       time.Duration(ttlMinutes) * time.Minute,
	}
}

// Get returns cached data if valid
func (c *ModelsCache) Get() ([]ModelInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Since(c.updatedAt) > c.ttl {
		return nil, false
	}
	return c.data, true
}

// Set updates cached data
func (c *ModelsCache) Set(data []ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = data
	c.updatedAt = time.Now()
}

// Clear clears the cache
func (c *ModelsCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = []ModelInfo{}
	c.updatedAt = time.Time{}
}

// modelsAPIKey resolves the key to authenticate a /v1/models probe with.
//
// Checking only AuthModeAPIKey meant this never authenticated in practice: this
// build forces every endpoint to token_pool and ApplyEndpointAuthModeRules
// clears Endpoint.APIKey for pools, so the probe went out unauthenticated, the
// upstream answered 401, and the model list silently fell back to the built-in
// defaults for every endpoint.
func (p *Proxy) modelsAPIKey(ep config.Endpoint) string {
	if ep.AuthMode == config.AuthModeAPIKey {
		return strings.TrimSpace(ep.APIKey)
	}
	if !config.IsTokenPoolAuthMode(ep.AuthMode) {
		return strings.TrimSpace(ep.APIKey)
	}
	cred, err := p.selectCredential(ep.Name)
	if err != nil {
		logger.Debug("Models probe: no usable token for %s: %v", ep.Name, err)
		return ""
	}
	if cred == nil {
		return ""
	}
	return strings.TrimSpace(cred.AccessToken)
}

// fetchModelsFromEndpoint fetches models from a specific endpoint
func (p *Proxy) fetchModelsFromEndpoint(ctx context.Context, ep config.Endpoint) ([]ModelInfo, error) {
	var modelsURL string
	var req *http.Request
	var err error

	apiKey := p.modelsAPIKey(ep)

	switch strings.ToLower(ep.Transformer) {
	case "openai", "openai2":
		// OpenAI compatible endpoints
		baseURL := strings.TrimSuffix(ep.APIUrl, "/")
		if strings.Contains(baseURL, "/v1") {
			modelsURL = baseURL + "/models"
		} else {
			modelsURL = baseURL + "/v1/models"
		}
		req, err = http.NewRequest("GET", modelsURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		// Add authorization header
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

	case "gemini":
		// Google Gemini endpoints
		baseURL := strings.TrimSuffix(ep.APIUrl, "/")
		if strings.Contains(baseURL, "/v1") {
			modelsURL = baseURL + "/models"
		} else {
			modelsURL = baseURL + "/v1beta/models"
		}
		// Add API key as query parameter
		if apiKey != "" {
			modelsURL = modelsURL + "?key=" + url.QueryEscape(apiKey)
		}
		req, err = http.NewRequest("GET", modelsURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

	default:
		// For transformers without /v1/models support (claude, codex)
		return nil, fmt.Errorf("transformer %s does not support /v1/models", ep.Transformer)
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "Osante Proxy/1.0")

	// The shared client has no overall timeout (streaming), so the caller's
	// context is what bounds this probe.
	req = req.WithContext(ctx)

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse response
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to ModelInfo with endpoint_id
	models := make([]ModelInfo, len(result.Data))
	for i, m := range result.Data {
		models[i] = ModelInfo{
			ID:         m.ID,
			Object:     m.Object,
			Created:    m.Created,
			OwnedBy:    m.OwnedBy,
			EndpointID: ep.Name,
		}
	}

	return models, nil
}

// getDefaultModels returns default models for endpoints that don't support /v1/models
func (p *Proxy) getDefaultModels(ep config.Endpoint) []ModelInfo {
	var modelID string
	var ownedBy string

	switch strings.ToLower(ep.Transformer) {
	case "claude":
		// Claude endpoints
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = "claude-sonnet-4-20250514" // Default Claude model
		}
		ownedBy = "anthropic"

	case "openai2":
		// Codex endpoints
		if ep.Model != "" {
			modelID = ep.Model
		} else if ep.AuthMode == config.AuthModeCodexTokenPool {
			modelID = "gpt-5-codex" // Default Codex model
		} else {
			modelID = "gpt-4o" // Default OpenAI model
		}
		ownedBy = "openai"

	case "1minai":
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = "gpt-4o"
		}
		ownedBy = "1minai"

	default:
		// Fallback for any other transformer
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = "unknown-model"
		}
		ownedBy = strings.ToLower(ep.Transformer)
	}

	return []ModelInfo{
		{
			ID:         modelID,
			Object:     "model",
			Created:    time.Now().Unix(),
			OwnedBy:    ownedBy,
			EndpointID: ep.Name,
		},
	}
}

// handleModels handles GET /v1/models requests
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeProxyError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	// Check for refresh parameter
	refresh := r.URL.Query().Get("refresh") == "true"
	refreshEnabled := p.cfg().GetModelsCacheRefreshEnabled()

	if refresh && !refreshEnabled {
		writeProxyError(w, http.StatusForbidden, "permission_error", "Refresh is disabled in configuration")
		return
	}

	// Try to get from cache if not refreshing
	if !refresh {
		if cached, ok := p.modelsCache.Get(); ok {
			p.writeModelsResponse(w, cached)
			return
		}
	}

	// Serialize cache misses. Without this, every concurrent request that
	// arrives on an expired cache probes every endpoint (cache stampede);
	// whoever gets the lock second finds the freshly-filled cache instead.
	p.modelsFetchMu.Lock()
	defer p.modelsFetchMu.Unlock()
	if !refresh {
		if cached, ok := p.modelsCache.Get(); ok {
			p.writeModelsResponse(w, cached)
			return
		}
	}

	// The shared HTTP client deliberately has no overall timeout (long SSE
	// streams) and ResponseHeaderTimeout is 90s, so probing N endpoints
	// sequentially could hold the client for 90*N seconds. Bound the whole fan-out
	// and run the probes concurrently.
	ctx, cancel := context.WithTimeout(r.Context(), modelsFetchBudget)
	defer cancel()

	var enabled []config.Endpoint
	for _, ep := range p.cfg().GetEndpoints() {
		if ep.Enabled {
			enabled = append(enabled, ep)
		}
	}

	perEndpoint := make([][]ModelInfo, len(enabled))
	fetched := make([]bool, len(enabled))
	var wg sync.WaitGroup
	for i := range enabled {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ep := enabled[i]
			models, err := p.fetchModelsFromEndpoint(ctx, ep)
			if err != nil {
				// If fetch fails, use default models for this endpoint
				logger.Debug("Failed to fetch models from %s: %v", ep.Name, err)
				perEndpoint[i] = p.getDefaultModels(ep)
				return
			}
			perEndpoint[i] = models
			fetched[i] = true
		}(i)
	}
	wg.Wait()

	// Aggregate in endpoint order so the list is stable across requests.
	allModels := []ModelInfo{}
	allFailed := true
	for i := range enabled {
		if fetched[i] {
			allFailed = false
		}
		allModels = append(allModels, perEndpoint[i]...)
	}

	// If all endpoints failed, still return the aggregated default models
	if allFailed && len(enabled) > 0 {
		logger.Debug("All endpoints failed to fetch models, returning default models")
	}

	// Cache the result
	p.modelsCache.Set(allModels)

	// Write response
	p.writeModelsResponse(w, allModels)
}

// writeModelsResponse writes the models list response
func (p *Proxy) writeModelsResponse(w http.ResponseWriter, models []ModelInfo) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := struct {
		Object string      `json:"object"`
		Data   []ModelInfo `json:"data"`
	}{
		Object: "list",
		Data:   models,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Debug("Failed to encode models response: %v", err)
	}
}
