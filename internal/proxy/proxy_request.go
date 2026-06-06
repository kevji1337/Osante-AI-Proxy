package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

type proxyRequestContext struct {
	httpRequest                 *http.Request
	bodyBytes                   []byte
	clientFormat                ClientFormat
	streamRequested             bool
	requestModel                string
	requestStart                time.Time
	requestBytes                int
	endpoints                   []config.Endpoint
	specifiedEndpoint           *config.Endpoint
	modelOverride               string
	useSpecificEndpoint         bool
	refreshedCredentialAttempts map[int64]bool
	lastUpstreamStatus          int
	lastUpstreamBody            []byte
	lastUpstreamHeader          http.Header
}

type endpointAttempt struct {
	endpoint           config.Endpoint
	authMode           string
	apiKey             string
	credentialID       int64
	selectedCredential *storage.EndpointCredential
	transformer        transformer.Transformer
	transformerName    string
	transformedBody    []byte
	modelName          string
	thinkingEnabled    bool
	proxyRequest       *http.Request
	response           *http.Response
}

type attemptResult int

const (
	attemptResultDone attemptResult = iota
	attemptResultRetrySameEndpoint
	attemptResultRetryNextEndpoint
)

func (p *Proxy) handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	reqCtx, err := p.newProxyRequestContext(w, r)
	if err != nil {
		return
	}

	maxRetries := p.computeMaxRetries(reqCtx.endpoints)
	endpointAttempts := 0
	lastEndpointName := ""

	for retry := 0; retry < maxRetries; retry++ {
		endpoint := p.nextEndpointForRequest(reqCtx)
		if endpoint.Name == "" {
			if p.writeLastUpstreamError(w, reqCtx) {
				return
			}
			http.Error(w, "No enabled endpoints available", http.StatusServiceUnavailable)
			return
		}

		if lastEndpointName != "" && lastEndpointName != endpoint.Name {
			endpointAttempts = 0
		}
		lastEndpointName = endpoint.Name
		endpointAttempts++

		attempt := &endpointAttempt{endpoint: endpoint}
		result := p.runEndpointAttempt(w, reqCtx, attempt)
		if result == attemptResultDone {
			return
		}

		if result == attemptResultRetrySameEndpoint {
			endpointAttempts = 0
			continue
		}

		if endpointAttempts >= 2 && !reqCtx.useSpecificEndpoint {
			p.rotateEndpoint()
			endpointAttempts = 0
		}
	}

	if p.writeLastUpstreamError(w, reqCtx) {
		return
	}
	http.Error(w, "All endpoints failed", http.StatusServiceUnavailable)
}

// writeLastUpstreamError replays the most recent upstream error response to the
// client. Used when every endpoint is exhausted (e.g. all in usage-limit
// cooldown) so the client sees the real upstream error instead of a generic 503.
func (p *Proxy) writeLastUpstreamError(w http.ResponseWriter, reqCtx *proxyRequestContext) bool {
	if reqCtx.lastUpstreamStatus == 0 || reqCtx.lastUpstreamBody == nil {
		return false
	}
	for key, values := range reqCtx.lastUpstreamHeader {
		if key == "Content-Encoding" || key == "Content-Length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(reqCtx.lastUpstreamStatus)
	_, _ = w.Write(reqCtx.lastUpstreamBody)
	return true
}

func (p *Proxy) newProxyRequestContext(w http.ResponseWriter, r *http.Request) (*proxyRequestContext, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return nil, err
	}
	defer r.Body.Close()

	clientFormat := detectClientFormat(r.URL.Path)
	logger.DebugLog("=== Proxy Request ===")
	logger.DebugLog("Method: %s, Path: %s, ClientFormat: %s", r.Method, r.URL.Path, clientFormat)
	logger.DebugLog("Request Body: %s", string(bodyBytes))

	var streamReq struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &streamReq)

	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		logger.Error("No enabled endpoints available")
		http.Error(w, "No enabled endpoints configured", http.StatusServiceUnavailable)
		return nil, errNoEnabledEndpoints
	}

	specifiedEndpoint, modelOverride, resolveErr := p.resolver.ResolveEndpoint(r, bodyBytes)
	if resolveErr != nil {
		logger.Warn("Endpoint resolution failed: %v", resolveErr)
		writeInvalidRequestError(w, resolveErr.Error())
		return nil, resolveErr
	}

	useSpecificEndpoint := specifiedEndpoint != nil
	if useSpecificEndpoint {
		logger.Debug("[Resolver] using specified endpoint: %s", specifiedEndpoint.Name)
	}

	return &proxyRequestContext{
		httpRequest:                 r,
		bodyBytes:                   bodyBytes,
		clientFormat:                clientFormat,
		streamRequested:             streamReq.Stream,
		requestModel:                strings.TrimSpace(streamReq.Model),
		requestStart:                time.Now(),
		requestBytes:                len(bodyBytes),
		endpoints:                   endpoints,
		specifiedEndpoint:           specifiedEndpoint,
		modelOverride:               modelOverride,
		useSpecificEndpoint:         useSpecificEndpoint,
		refreshedCredentialAttempts: make(map[int64]bool),
	}, nil
}

func (p *Proxy) nextEndpointForRequest(reqCtx *proxyRequestContext) config.Endpoint {
	if reqCtx.useSpecificEndpoint && reqCtx.specifiedEndpoint != nil {
		if p.endpointInCooldown(reqCtx.specifiedEndpoint.Name) {
			return config.Endpoint{}
		}
		return *reqCtx.specifiedEndpoint
	}
	return p.currentRequestEndpoint()
}

func (p *Proxy) runEndpointAttempt(w http.ResponseWriter, reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	p.markRequestActive(attempt.endpoint.Name)

	if result := p.prepareEndpointAttempt(reqCtx, attempt); result != attemptResultDone {
		p.markRequestInactive(attempt.endpoint.Name)
		return result
	}

	p.logUpstreamRequest(reqCtx, attempt)
	resp, err := sendRequest(p.getEndpointContext(attempt.endpoint.Name), attempt.proxyRequest, p.httpClient, p.config)
	if err != nil {
		return p.handleSendError(err, attempt)
	}
	attempt.response = resp

	return p.handleAttemptResponse(w, reqCtx, attempt)
}

func (p *Proxy) prepareEndpointAttempt(reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	if result := p.resolveAttemptAuth(reqCtx, attempt); result != attemptResultDone {
		return result
	}

	attempt.modelName = resolveAttemptModelName(reqCtx, attempt.endpoint)
	trans, err := prepareTransformerForClient(reqCtx.clientFormat, attempt.endpoint, attempt.modelName)
	if err != nil {
		logger.Error("[%s] %v", attempt.endpoint.Name, err)
		p.stats.RecordError(attempt.endpoint.Name)
		return attemptResultRetryNextEndpoint
	}
	attempt.transformer = trans
	attempt.transformerName = trans.Name()

	transformedBody, err := trans.TransformRequest(reqCtx.bodyBytes)
	if err != nil {
		logger.Error("[%s] Failed to transform request: %v", attempt.endpoint.Name, err)
		p.stats.RecordError(attempt.endpoint.Name)
		return attemptResultRetryNextEndpoint
	}

	logger.DebugLog("[%s] Transformer: %s", attempt.endpoint.Name, attempt.transformerName)
	logger.DebugLog("[%s] Transformed Request: %s", attempt.endpoint.Name, string(transformedBody))

	if reqCtx.modelOverride != "" {
		transformedBody = overrideModelInPayload(transformedBody, reqCtx.modelOverride)
		logger.DebugLog("[%s] request after model override: %s", attempt.endpoint.Name, string(transformedBody))
	}

	cleanedBody, err := cleanIncompleteToolCalls(transformedBody)
	if err != nil {
		// Non-messages payloads (count_tokens, /models, empty probes) fail this
		// routinely — keep it at DEBUG so real warnings stay visible.
		logger.Debug("[%s] Skipping tool-call cleanup: %v", attempt.endpoint.Name, err)
		cleanedBody = transformedBody
	}
	if shouldOverridePayloadModel(attempt.transformerName) && attempt.modelName != "" {
		cleanedBody = overrideModelInPayload(cleanedBody, attempt.modelName)
	}
	attempt.transformedBody = cleanedBody
	attempt.thinkingEnabled = detectThinkingEnabled(attempt.transformerName, attempt.transformedBody)

	proxyReq, err := buildProxyRequest(reqCtx.httpRequest, attempt.endpoint, attempt.apiKey, attempt.transformedBody, attempt.transformerName, attempt.modelName, attempt.selectedCredential)
	if err != nil {
		logger.Error("[%s] Failed to create request: %v", attempt.endpoint.Name, err)
		p.stats.RecordError(attempt.endpoint.Name)
		return attemptResultRetryNextEndpoint
	}
	attempt.proxyRequest = proxyReq

	return attemptResultDone
}

func (p *Proxy) resolveAttemptAuth(reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	attempt.authMode = config.NormalizeAuthMode(attempt.endpoint.AuthMode)
	attempt.apiKey = strings.TrimSpace(attempt.endpoint.APIKey)

	if config.IsTokenPoolAuthMode(attempt.authMode) {
		credential, err := p.selectCredential(attempt.endpoint.Name)
		if err != nil {
			logger.Warn("[%s] Failed to select token pool credential: %v", attempt.endpoint.Name, err)
			p.stats.RecordError(attempt.endpoint.Name)
			return attemptResultRetryNextEndpoint
		}
		if credential == nil || strings.TrimSpace(credential.AccessToken) == "" {
			logger.Warn("[%s] No usable token in token pool", attempt.endpoint.Name)
			p.stats.RecordError(attempt.endpoint.Name)
			return attemptResultRetryNextEndpoint
		}

		attempt.selectedCredential = credential
		if shouldTryCredentialRefresh(credential, time.Now().UTC()) {
			refreshed, refreshErr := p.refreshCredential(attempt.endpoint, credential)
			if refreshErr != nil {
				logger.Warn("[%s] Preflight credential refresh failed (id=%d): %v", attempt.endpoint.Name, credential.ID, refreshErr)
			} else {
				attempt.selectedCredential = refreshed
				reqCtx.refreshedCredentialAttempts[refreshed.ID] = true
			}
		}

		attempt.apiKey = strings.TrimSpace(credential.AccessToken)
		if attempt.selectedCredential != nil {
			attempt.apiKey = strings.TrimSpace(attempt.selectedCredential.AccessToken)
			attempt.credentialID = attempt.selectedCredential.ID
		}
		return attemptResultDone
	}

	if attempt.apiKey == "" {
		logger.Warn("[%s] API key mode but apiKey is empty", attempt.endpoint.Name)
		p.stats.RecordError(attempt.endpoint.Name)
		return attemptResultRetryNextEndpoint
	}

	return attemptResultDone
}

func (p *Proxy) logUpstreamRequest(reqCtx *proxyRequestContext, attempt *endpointAttempt) {
	proxyLabel := strings.TrimSpace(resolveProxyURLForRequest(p.config, attempt.proxyRequest.URL))
	action := "Requesting"
	if reqCtx.streamRequested {
		action = "Streaming"
	}
	if proxyLabel == "" {
		logger.Debug("[%s] %s %s %d", attempt.endpoint.Name, action, attempt.modelName, reqCtx.requestBytes)
		return
	}
	logger.Debug("[%s] %s %s %d %s", attempt.endpoint.Name, action, attempt.modelName, reqCtx.requestBytes, proxyLabel)
}

func (p *Proxy) handleSendError(err error, attempt *endpointAttempt) attemptResult {
	logger.Error("[%s] Request failed: %v", attempt.endpoint.Name, err)
	p.markRequestInactive(attempt.endpoint.Name)
	if isTransientNetworkError(err) {
		logger.Warn("[%s] Transient network error, retrying same endpoint: %v", attempt.endpoint.Name, err)
		time.Sleep(300 * time.Millisecond)
		return attemptResultRetrySameEndpoint
	}
	p.markCredentialFailure(attempt.credentialID, 0, err.Error())
	p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
	p.recordEndpointError(attempt.endpoint.Name, truncateString(err.Error(), 200))
	p.stats.RecordError(attempt.endpoint.Name)
	return attemptResultRetryNextEndpoint
}

func (p *Proxy) handleAttemptResponse(w http.ResponseWriter, reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	resp := attempt.response
	if resp.StatusCode == http.StatusOK {
		p.captureCodexRateLimitsFromHeaders(attempt.endpoint, attempt.credentialID, resp.Header)
	}

	if resp.StatusCode == http.StatusOK && !reqCtx.streamRequested && shouldAggregateCodexStreaming(attempt.endpoint, attempt.transformerName) {
		return p.handleAggregatedStreamingSuccess(w, reqCtx, attempt)
	}

	isStreaming := shouldHandleAsStreamingResponse(resp.Header.Get("Content-Type"), reqCtx.streamRequested, attempt.endpoint, attempt.transformerName)
	if resp.StatusCode == http.StatusOK && isStreaming {
		inputTokens, outputTokens, outputText := p.handleStreamingResponse(w, resp, attempt.endpoint, attempt.transformer, attempt.transformerName, attempt.thinkingEnabled, attempt.modelName, reqCtx.bodyBytes, attempt.credentialID)
		p.finishSuccessfulAttempt(reqCtx, attempt, inputTokens, outputTokens, outputText)
		return attemptResultDone
	}

	if resp.StatusCode == http.StatusOK {
		inputTokens, outputTokens, err := p.handleNonStreamingResponse(w, resp, attempt.endpoint, attempt.transformer)
		if err == nil {
			p.finishSuccessfulAttempt(reqCtx, attempt, inputTokens, outputTokens, "")
			return attemptResultDone
		}
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		return p.handlePaymentRequired(reqCtx, attempt)
	}

	if shouldRetry(resp.StatusCode) {
		return p.handleRetryableStatus(resp, attempt)
	}

	return p.handleFinalStatus(w, reqCtx, attempt)
}

// handlePaymentRequired handles HTTP 402 responses. When the body indicates a
// usage limit (FreeModel):
//   - in token_pool mode the *individual token* is put into cooldown and the
//     request is retried on the same endpoint, so the pool rotates to the next
//     token instead of dropping the whole endpoint;
//   - otherwise the *endpoint* is put into cooldown and the request is retried
//     on the next endpoint.
//
// Other (non usage-limit) 402s keep the prior retry behaviour. In all cases the
// upstream error is remembered so it can be returned to the client if every
// endpoint/token ends up exhausted.
func (p *Proxy) handlePaymentRequired(reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	resp := attempt.response
	body := readResponseBody(resp)
	bodyStr := string(body)
	errMsg := truncateString(bodyStr, 200)

	reqCtx.lastUpstreamStatus = resp.StatusCode
	reqCtx.lastUpstreamBody = body
	reqCtx.lastUpstreamHeader = resp.Header.Clone()

	usageLimited := isUsageLimitError(resp.StatusCode, bodyStr)
	isTokenPool := config.IsTokenPoolAuthMode(attempt.authMode) && attempt.credentialID > 0

	if usageLimited && isTokenPool {
		// Cool down only the offending token; retry on the same endpoint so the
		// pool advances to the next usable token.
		until := parseUsageLimitCooldown(bodyStr, time.Now().UTC())
		if p.storage != nil {
			if err := p.storage.MarkCredentialUsageLimit(attempt.credentialID, errMsg, until, time.Now().UTC()); err != nil {
				logger.Warn("[%s] Failed to set token usage-limit cooldown (id=%d): %v", attempt.endpoint.Name, attempt.credentialID, err)
			}
		}
		logger.Info("Endpoint %s token (id=%d) hit usage limit, switching to next token", attempt.endpoint.Name, attempt.credentialID)
		logger.Debug("[%s] Token id=%d usage limit cooldown until %s: %s", attempt.endpoint.Name, attempt.credentialID, until.Format(time.RFC3339), errMsg)

		p.recordEndpointError(attempt.endpoint.Name, errMsg)
		p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
		p.stats.RecordError(attempt.endpoint.Name)
		p.markRequestInactive(attempt.endpoint.Name)
		return attemptResultRetrySameEndpoint
	}

	if usageLimited {
		until := parseUsageLimitCooldown(bodyStr, time.Now().UTC())
		p.setEndpointCooldown(attempt.endpoint.Name, until, usageLimitReason)
		logger.Info("Endpoint %s hit usage limit, switching to next endpoint", attempt.endpoint.Name)
		logger.Debug("[%s] Usage limit cooldown until %s: %s", attempt.endpoint.Name, until.Format(time.RFC3339), errMsg)
	} else {
		logger.Warn("[%s] Request failed %d: %s", attempt.endpoint.Name, resp.StatusCode, errMsg)
	}

	p.recordEndpointError(attempt.endpoint.Name, errMsg)
	p.markCredentialFailure(attempt.credentialID, resp.StatusCode, errMsg)
	p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
	p.stats.RecordError(attempt.endpoint.Name)
	p.markRequestInactive(attempt.endpoint.Name)
	return attemptResultRetryNextEndpoint
}

func (p *Proxy) handleAggregatedStreamingSuccess(w http.ResponseWriter, reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	inputTokens, outputTokens, outputText, err := p.handleStreamingAsNonStreaming(w, attempt.response, attempt.endpoint, attempt.transformer, attempt.credentialID)
	if err == nil {
		p.finishSuccessfulAttempt(reqCtx, attempt, inputTokens, outputTokens, outputText)
		return attemptResultDone
	}

	logger.Warn("[%s] Failed to aggregate streaming response as non-stream: %v", attempt.endpoint.Name, err)
	p.markCredentialFailure(attempt.credentialID, 0, err.Error())
	p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
	p.stats.RecordError(attempt.endpoint.Name)
	p.markRequestInactive(attempt.endpoint.Name)
	return attemptResultRetryNextEndpoint
}

func (p *Proxy) finishSuccessfulAttempt(reqCtx *proxyRequestContext, attempt *endpointAttempt, inputTokens, outputTokens int, outputText string) {
	if inputTokens == 0 || outputTokens == 0 {
		inputTokens, outputTokens = p.estimateTokens(reqCtx.bodyBytes, outputText, inputTokens, outputTokens, attempt.endpoint.Name)
	}
	p.stats.RecordRequest(attempt.endpoint.Name)
	p.stats.RecordTokens(attempt.endpoint.Name, inputTokens, outputTokens)
	p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 1, 0, inputTokens, outputTokens)
	p.markCredentialSuccess(attempt.credentialID)
	p.clearEndpointError(attempt.endpoint.Name)
	p.markRequestInactive(attempt.endpoint.Name)
	if p.onEndpointSuccess != nil {
		p.onEndpointSuccess(attempt.endpoint.Name)
	}
	totalElapsed := time.Since(reqCtx.requestStart).Round(time.Millisecond)
	logger.Debug("[%s] Requested tokens=%d/%d latency=%s cred_id=%d", attempt.endpoint.Name, inputTokens, outputTokens, totalElapsed, attempt.credentialID)
}

func (p *Proxy) handleRetryableStatus(resp *http.Response, attempt *endpointAttempt) attemptResult {
	errBody := readResponseBody(resp)
	errMsg := truncateString(string(errBody), 200)
	// Plain-text "404 page not found" responses from gateways for unsupported
	// service paths (/v1/models, /health, etc.) are not real proxy errors —
	// keep them at DEBUG. Real API errors (JSON bodies) stay at WARN.
	if resp.StatusCode == http.StatusNotFound && isGatewayNotFoundNoise(errMsg) {
		logger.Debug("[%s] Upstream 404 (gateway probe): %s", attempt.endpoint.Name, errMsg)
	} else {
		logger.Warn("[%s] Request failed %d: %s", attempt.endpoint.Name, resp.StatusCode, errMsg)
	}
	logger.DebugLog("[%s] Request failed %d: %s", attempt.endpoint.Name, resp.StatusCode, errMsg)
	p.recordEndpointError(attempt.endpoint.Name, errMsg)
	p.markCredentialFailure(attempt.credentialID, resp.StatusCode, errMsg)
	p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
	p.stats.RecordError(attempt.endpoint.Name)
	p.markRequestInactive(attempt.endpoint.Name)
	return attemptResultRetryNextEndpoint
}

func (p *Proxy) handleFinalStatus(w http.ResponseWriter, reqCtx *proxyRequestContext, attempt *endpointAttempt) attemptResult {
	resp := attempt.response
	respBody := readResponseBody(resp)
	skipCredentialPenalty := false

	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt.credentialID > 0 {
		errMsg := truncateString(string(respBody), 500)
		if !shouldTreatCredentialAuthFailure(resp.StatusCode, errMsg) {
			skipCredentialPenalty = true
			logger.Warn("[%s] Upstream %d looks like route/gateway denial, skipping credential invalidation", attempt.endpoint.Name, resp.StatusCode)
		}
		if !skipCredentialPenalty {
			if p.tryRefreshAfterAuthFailure(reqCtx, attempt, resp.StatusCode) {
				p.markRequestInactive(attempt.endpoint.Name)
				return attemptResultRetrySameEndpoint
			}
			p.markCredentialFailure(attempt.credentialID, resp.StatusCode, errMsg)
			p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
			p.stats.RecordError(attempt.endpoint.Name)
			p.markRequestInactive(attempt.endpoint.Name)
			logger.Warn("[%s] Credential auth failed (%d), retrying with next token", attempt.endpoint.Name, resp.StatusCode)
			return attemptResultRetrySameEndpoint
		}
		p.stats.RecordError(attempt.endpoint.Name)
	}

	p.markRequestInactive(attempt.endpoint.Name)
	if resp.StatusCode != http.StatusOK {
		errMsg := truncateString(string(respBody), 500)
		if resp.StatusCode == http.StatusBadRequest &&
			strings.Contains(errMsg, "api.responses.write") &&
			strings.Contains(attempt.transformerName, "openai2") {
			logger.Warn("[%s] Upstream rejected /v1/responses scope (api.responses.write). Try transformer=openai (chat/completions) for this token.", attempt.endpoint.Name)
		}
		if skipCredentialPenalty {
			p.markCredentialFailure(attempt.credentialID, 0, errMsg)
		} else {
			p.markCredentialFailure(attempt.credentialID, resp.StatusCode, errMsg)
		}
		p.recordCredentialUsage(attempt.credentialID, attempt.endpoint.Name, 0, 1, 0, 0)
		logger.Warn("[%s] Response %d: %s", attempt.endpoint.Name, resp.StatusCode, errMsg)
		logger.DebugLog("[%s] Response %d: %s", attempt.endpoint.Name, resp.StatusCode, errMsg)
	}

	copyResponseHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
	return attemptResultDone
}

func (p *Proxy) tryRefreshAfterAuthFailure(reqCtx *proxyRequestContext, attempt *endpointAttempt, statusCode int) bool {
	if attempt.selectedCredential == nil ||
		!isCodexProviderType(attempt.selectedCredential.ProviderType) ||
		strings.TrimSpace(attempt.selectedCredential.RefreshToken) == "" ||
		reqCtx.refreshedCredentialAttempts[attempt.credentialID] {
		return false
	}

	reqCtx.refreshedCredentialAttempts[attempt.credentialID] = true
	refreshed, refreshErr := p.refreshCredential(attempt.endpoint, attempt.selectedCredential)
	if refreshErr == nil {
		logger.Info("[%s] Credential refreshed after %d, retrying with updated token (id=%d)", attempt.endpoint.Name, statusCode, attempt.credentialID)
		if refreshed != nil && refreshed.ID > 0 {
			reqCtx.refreshedCredentialAttempts[refreshed.ID] = true
		}
		return true
	}
	logger.Warn("[%s] Credential refresh failed after %d (id=%d): %v", attempt.endpoint.Name, statusCode, attempt.credentialID, refreshErr)
	return false
}

func resolveAttemptModelName(reqCtx *proxyRequestContext, endpoint config.Endpoint) string {
	if strings.TrimSpace(endpoint.Model) != "" {
		logger.Debug("[%s] using endpoint model: %s", endpoint.Name, endpoint.Model)
		return strings.TrimSpace(endpoint.Model)
	}
	if reqCtx.modelOverride != "" {
		logger.Debug("[%s] using model override: %s", endpoint.Name, reqCtx.modelOverride)
		return reqCtx.modelOverride
	}
	return reqCtx.requestModel
}

func shouldOverridePayloadModel(transformerName string) bool {
	return strings.Contains(transformerName, "claude") ||
		strings.Contains(transformerName, "openai")
}

func detectThinkingEnabled(transformerName string, transformedBody []byte) bool {
	if !strings.Contains(transformerName, "openai") {
		return false
	}
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(transformedBody, &openaiReq); err != nil {
		return false
	}
	enable, _ := openaiReq["enable_thinking"].(bool)
	return enable
}

func writeInvalidRequestError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	errorResp := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": message,
		},
	}
	jsonBytes, err := json.Marshal(errorResp)
	if err == nil {
		_, _ = w.Write(jsonBytes)
	}
}

func readResponseBody(resp *http.Response) []byte {
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") == "gzip" {
		body, _ := decompressGzip(resp.Body)
		return body
	}
	body, _ := io.ReadAll(resp.Body)
	return body
}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if key == "Content-Encoding" || key == "Content-Length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func truncateString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

var errNoEnabledEndpoints = io.EOF
