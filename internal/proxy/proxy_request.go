package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	trace                       *TraceHandle
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
	defer func() {
		// Trace finalize must run regardless of how the request loop exits.
		// nil-safe on purpose — if tracing was disabled, this is a no-op.
		reqCtx.trace.Finalize()
	}()

	maxRetries := p.computeMaxRetries(reqCtx.endpoints)
	endpointAttempts := 0
	lastEndpointName := ""

	for retry := 0; retry < maxRetries; retry++ {
		endpoint := p.nextEndpointForRequest(reqCtx)
		if endpoint.Name == "" {
			if p.writeLastUpstreamError(w, reqCtx) {
				return
			}
			writeProxyError(w, http.StatusServiceUnavailable, "overloaded_error", "No enabled endpoints available")
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

	p.writeExhaustedResponse(w, reqCtx)
}

// writeExhaustedResponse is the final response when no endpoint could serve
// the request after the full retry loop ran out. We use HTTP 503 with an
// Anthropic-shape JSON envelope; this gives the client (a) a clear
// non-200 status so retry logic + circuit-breakers actually trigger,
// and (b) a parseable body so the user sees a real message instead of
// AssertionError on Hermes-style clients.
//
// We deliberately do NOT return 200 OK with the error masquerading as an
// assistant turn: that would short-circuit client-side retries and leave
// the user staring at "Osante Proxy: <something>" as the answer to their
// prompt — exactly what the proxy exists to prevent.
func (p *Proxy) writeExhaustedResponse(w http.ResponseWriter, reqCtx *proxyRequestContext) {
	if p.writeLastUpstreamError(w, reqCtx) {
		return
	}
	writeProxyError(w, http.StatusServiceUnavailable, "overloaded_error", "All endpoints failed")
}

// writeLastUpstreamError replays the most recent upstream error response to the
// client. Used when every endpoint is exhausted (e.g. all in usage-limit
// cooldown) so the client sees the real upstream error instead of a generic 503.
//
// If the upstream body is NOT JSON (Cloudflare HTML 502, gateway plain-text
// "Bad Gateway", reverse-proxy "no upstream" pages, etc.) we synthesize a
// proper Anthropic-style JSON error instead. If the body IS JSON but does not
// match the Anthropic envelope (FreeModel returns `{"error":"<msg>"}`,
// OpenAI returns `{"error":{"message":"...","type":"..."}}`), we extract the
// human message and rewrap. Without this rewrap, Hermes-style clients parse
// the body, fail to find `error.message`, and crash with AssertionError on
// a blank `Error:` field.
func (p *Proxy) writeLastUpstreamError(w http.ResponseWriter, reqCtx *proxyRequestContext) bool {
	if reqCtx.lastUpstreamStatus == 0 || reqCtx.lastUpstreamBody == nil {
		return false
	}

	status := reqCtx.lastUpstreamStatus

	if !looksLikeJSONBody(reqCtx.lastUpstreamBody) {
		// Plain-text or HTML upstream error. Don't forward it as-is — wrap.
		msg := truncateString(strings.TrimSpace(string(reqCtx.lastUpstreamBody)), 500)
		if msg == "" {
			msg = fmt.Sprintf("Upstream returned HTTP %d with empty body", status)
		}
		writeProxyError(w, status, errTypeForStatus(status), msg)
		return true
	}

	// JSON body. Either it already speaks Anthropic ({"type":"error","error":{...}})
	// or it doesn't — extract a message and rewrap so the envelope is consistent.
	msg, looksAnthropic := extractUpstreamErrorMessage(reqCtx.lastUpstreamBody)
	if looksAnthropic {
		for key, values := range reqCtx.lastUpstreamHeader {
			if key == "Content-Encoding" || key == "Content-Length" {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(reqCtx.lastUpstreamBody)
		return true
	}

	if msg == "" {
		msg = fmt.Sprintf("Upstream returned HTTP %d", status)
	}
	writeProxyError(w, status, errTypeForStatus(status), msg)
	return true
}

// extractUpstreamErrorMessage tries to pull a human-readable error message
// out of an upstream JSON body. Returns (message, isAnthropicShape).
//
// Recognised shapes:
//   - Anthropic:  {"type":"error","error":{"type":"...","message":"..."}}
//   - FreeModel:  {"error":"Usage limit reached, will reset on today at ..."}
//   - OpenAI:     {"error":{"message":"...","type":"...","code":"..."}}
//   - Generic:    {"message":"..."}, {"detail":"..."}, {"title":"..."}
//
// Anything we can't parse falls through with an empty message.
func extractUpstreamErrorMessage(body []byte) (string, bool) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", false
	}

	// Anthropic shape: {"type":"error","error":{"message":"..."}}
	if t, ok := raw["type"].(string); ok && t == "error" {
		if errObj, ok := raw["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
				return msg, true
			}
		}
	}

	// Nested `error` object (OpenAI-style): {"error":{"message":"...","type":"..."}}
	if errObj, ok := raw["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return msg, false
		}
		// `error` object without `message` — try `detail` or `title` inside it.
		if detail, ok := errObj["detail"].(string); ok && strings.TrimSpace(detail) != "" {
			return detail, false
		}
	}

	// Flat `error` string (FreeModel-style): {"error":"Usage limit reached..."}
	if errStr, ok := raw["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return errStr, false
	}

	// Generic fallbacks for whatever upstreams cooked up next.
	for _, key := range []string{"message", "detail", "title", "description"} {
		if v, ok := raw[key].(string); ok && strings.TrimSpace(v) != "" {
			return v, false
		}
	}

	return "", false
}

// errTypeForStatus maps an HTTP status into a plausible Anthropic-style
// error type so the Final error: message in clients is at least sensible.
func errTypeForStatus(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status == http.StatusPaymentRequired:
		return "rate_limit_error"
	case status >= 500 && status < 600:
		// 502/503/504 from a CDN are technically "the proxy can't reach the
		// origin"; treat as overloaded so clients retry.
		return "overloaded_error"
	}
	return "api_error"
}

func (p *Proxy) newProxyRequestContext(w http.ResponseWriter, r *http.Request) (*proxyRequestContext, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body: %v", err)
		writeProxyError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return nil, err
	}
	defer r.Body.Close()

	clientFormat := detectClientFormat(r.URL.Path)
	logger.DebugLog("=== Proxy Request ===")
	logger.DebugLog("Method: %s, Path: %s, ClientFormat: %s", r.Method, r.URL.Path, clientFormat)
	logger.DebugLog("Request Body: %s", string(bodyBytes))

	// Reject anything that can't be a real LLM request before we burn an
	// attempt against every endpoint. The proxy "/" route only handles POST
	// /v1/messages-style traffic; HEAD/GET/OPTIONS probes (Cloudflare health
	// checks, browser preflights, broken clients), empty bodies, and
	// non-JSON content types all reach transformers as `unexpected end of
	// JSON input` and spam ERROR once per endpoint per token. Catch them at
	// the boundary instead.
	if r.Method != http.MethodPost {
		logger.Debug("Rejecting non-POST %s to %s", r.Method, r.URL.Path)
		writeInvalidRequestError(w, "method not allowed")
		return nil, errInvalidProxyRequest
	}
	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		logger.Debug("Rejecting empty-body POST to %s", r.URL.Path)
		writeInvalidRequestError(w, "request body is empty")
		return nil, errInvalidProxyRequest
	}
	if !looksLikeJSONBody(bodyBytes) {
		logger.Debug("Rejecting non-JSON POST to %s (content-type=%q)", r.URL.Path, r.Header.Get("Content-Type"))
		writeInvalidRequestError(w, "request body is not JSON")
		return nil, errInvalidProxyRequest
	}

	var streamReq struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &streamReq)

	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		logger.Error("No enabled endpoints available")
		writeProxyError(w, http.StatusServiceUnavailable, "overloaded_error", "No enabled endpoints configured")
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

	trace := p.beginTrace()
	trace.Mark(PhaseReceived)
	trace.SetMeta(r.Method, r.URL.Path, string(clientFormat))
	trace.SetBytes(len(bodyBytes), 0)

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
		trace:                       trace,
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

	// GitLab Duo uses WebSocket, not HTTP — hand off to the dedicated handler
	// and short-circuit the normal HTTP proxy pipeline.
	if attempt.transformerName == "cc_gitlabduo" {
		p.markRequestInactive(attempt.endpoint.Name)
		// Pass the full conversation history as goal so GitLab Duo has context.
		goal := extractGitLabDuoHistory(reqCtx.bodyBytes)
		namespaceID := extractGitLabDuoNamespaceID(reqCtx.bodyBytes)
		logger.DebugLog("[gitlabduo] goal len=%d", len(goal))
		p.handleGitLabDuoRequest(
			w, reqCtx.httpRequest,
			attempt.endpoint, attempt.apiKey, attempt.credentialID,
			goal, attempt.modelName, namespaceID,
		)
		return attemptResultDone
	}

	p.logUpstreamRequest(reqCtx, attempt)
	resp, err := sendRequest(p.getEndpointContext(attempt.endpoint.Name), attempt.proxyRequest, p.httpClient, p.config)
	if err != nil {
		reqCtx.trace.SetError(err.Error())
		return p.handleSendError(err, attempt)
	}
	reqCtx.trace.Mark(PhaseUpstreamSent)
	reqCtx.trace.SetStatus(resp.StatusCode)
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
	reqCtx.trace.Mark(PhaseTransformed)
	reqCtx.trace.SetEndpoint(attempt.endpoint.Name, attempt.transformerName, attempt.modelName, reqCtx.useSpecificEndpoint)

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
			// Token pool exhausted: every credential is either disabled,
			// invalid, expired, or in cooldown. Put the *endpoint* itself in
			// cooldown until the soonest token reactivates so the retry loop
			// stops thrashing it on every request. Dedup the log line so
			// concurrent in-flight requests don't each print one.
			now := time.Now().UTC()
			until, cdErr := p.earliestTokenCooldown(attempt.endpoint.Name, now)
			if cdErr == nil && !until.IsZero() {
				p.setEndpointCooldown(attempt.endpoint.Name, until, "Token pool exhausted")
			}
			if p.shouldLogDedup("no_usable_token|"+attempt.endpoint.Name, 30*time.Second) {
				if !until.IsZero() {
					logger.Warn("[%s] No usable token in token pool — endpoint cooled down until %s", attempt.endpoint.Name, until.Format(time.RFC3339))
				} else {
					logger.Warn("[%s] No usable token in token pool", attempt.endpoint.Name)
				}
			}
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
	p.markRequestInactive(attempt.endpoint.Name)
	if isTransientNetworkError(err) {
		// Transient: a single WARN explains what happened and that we're
		// retrying. Logging an ERROR on top of that for the same event
		// double-counts the failure in the log feed.
		logger.Warn("[%s] Transient network error, retrying same endpoint: %v", attempt.endpoint.Name, err)
		time.Sleep(300 * time.Millisecond)
		return attemptResultRetrySameEndpoint
	}
	logger.Error("[%s] Request failed: %v", attempt.endpoint.Name, err)
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
		if p.shouldLogUsageLimit(attempt.endpoint.Name, attempt.credentialID) {
			logger.Info("Endpoint %s token (id=%d) hit usage limit, switching to next token", attempt.endpoint.Name, attempt.credentialID)
		}
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

	// Trace: record outcome. PhaseClientSent is approximate — we marked
	// PhaseUpstreamSent earlier and the whole streaming body has been
	// proxied by the time we land here.
	reqCtx.trace.Mark(PhaseClientSent)
	reqCtx.trace.SetTokens(inputTokens, outputTokens)
	reqCtx.trace.SetBytes(len(reqCtx.bodyBytes), len(outputText))
	reqCtx.trace.SetStreaming(reqCtx.streamRequested)
	reqCtx.trace.SetStatus(http.StatusOK)
}

func (p *Proxy) handleRetryableStatus(resp *http.Response, attempt *endpointAttempt) attemptResult {
	errBody := readResponseBody(resp)
	errMsg := truncateString(string(errBody), 200)
	// Plain-text "404 page not found" responses from gateways for unsupported
	// service paths (/v1/models, /health, etc.) are not real proxy errors —
	// keep them at DEBUG. Real API errors (JSON bodies) stay at WARN.
	if resp.StatusCode == http.StatusNotFound && isGatewayNotFoundNoise(errMsg) {
		logger.Debug("[%s] Upstream 404 (gateway probe): %s", attempt.endpoint.Name, errMsg)
	} else if p.shouldLogDedup(fmt.Sprintf("retryable|%s|%d", attempt.endpoint.Name, resp.StatusCode), 10*time.Second) {
		// Dedup transient-but-non-cooldown failures (Cloudflare 502/503/504,
		// upstream 429) so a burst of parallel retries on the same dead
		// upstream doesn't spam the log feed.
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
	writeProxyError(w, http.StatusBadRequest, "invalid_request_error", message)
}

// writeProxyError emits a JSON error in the Anthropic-style shape that all
// our supported clients (Claude Code, Hermes, plain OpenAI wrappers) can
// parse without choking. http.Error() returns text/plain — clients that try
// to parse the body as JSON to surface a useful message see an empty
// Error: field instead (e.g. Hermes prints "Error:" with nothing after it
// and then raises AssertionError because its result schema isn't satisfied).
//
// Shape (compatible with /v1/messages-style upstreams):
//
//	{
//	  "type": "error",
//	  "error": { "type": "<errType>", "message": "<message>" }
//	}
//
// errType is one of the documented Anthropic error types: invalid_request_error,
// authentication_error, permission_error, not_found_error, request_too_large,
// rate_limit_error, api_error, overloaded_error. Anything else gets mapped to
// api_error by the helper at the call site if needed.
func writeProxyError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": message,
		},
	})
	if err != nil {
		// Marshalling a fixed map can't realistically fail; fall back to a
		// minimal hand-written JSON so the client still parses something.
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"internal error"}}`))
		return
	}
	_, _ = w.Write(body)
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

var errNoEnabledEndpoints = errors.New("no enabled endpoints configured")
var errInvalidProxyRequest = errors.New("invalid proxy request")

// looksLikeJSONBody is a cheap pre-check: a real LLM request body is always a
// JSON object — first non-whitespace byte must be `{` (or `[`, for the few
// providers that allow array roots). Anything else (HTML error pages,
// form-encoded, plain text probes) is rejected before transformers see it.
func looksLikeJSONBody(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{', '[':
		return true
	}
	return false
}

// extractGitLabDuoHistory builds a goal string for GitLab Duo from the full
// Anthropic request body. It includes:
//  1. A condensed system prompt (first 800 chars) so GitLab Duo knows the
//     assistant's role and any project-specific instructions.
//  2. The full conversation history (user/assistant turns) so context is
//     preserved across messages without a persistent WebSocket session.
//
// Format:
//
//	[System: ...]
//	User: ...
//	Assistant: ...
//	User: ...
func extractGitLabDuoHistory(body []byte) string {
	var req struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	var sb strings.Builder

	// Include a condensed system prompt when present. We cap it at 800 runes
	// to leave room for the conversation history within the 16 384-char limit.
	if len(req.System) > 0 {
		// req.System is json.RawMessage — decode to any first.
		var sysAny any
		if err := json.Unmarshal(req.System, &sysAny); err == nil {
			sysText := flattenAnySystem(sysAny)
			// Strip Claude Code billing/tool headers — keep only human-readable lines.
			sysText = condensedSystemPrompt(sysText, 800)
			if sysText != "" {
				sb.WriteString("[System: ")
				sb.WriteString(sysText)
				sb.WriteString("]\n")
			}
		}
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			text := extractUserQuestion(m.Content)
			if text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("User: ")
			sb.WriteString(text)
		case "assistant":
			text := decodeRawContent(m.Content)
			if text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("Assistant: ")
			sb.WriteString(text)
		}
	}
	return sb.String()
}

// condensedSystemPrompt extracts meaningful lines from the system prompt and
// truncates to maxRunes. It skips Claude Code internal headers (billing,
// tool descriptions, XML tags) and keeps human-readable instructions.
func condensedSystemPrompt(s string, maxRunes int) string {
	var sb strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip Claude Code internal lines.
		if strings.HasPrefix(trimmed, "x-anthropic-") ||
			strings.HasPrefix(trimmed, "<") ||
			strings.HasPrefix(trimmed, "SessionStart:") ||
			strings.HasPrefix(trimmed, "//") {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(trimmed)
		if len([]rune(sb.String())) >= maxRunes {
			break
		}
	}
	runes := []rune(sb.String())
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return sb.String()
}

// extractGitLabDuoUserText pulls the actual user question from an Anthropic
// /v1/messages body. Claude Code wraps the real question together with a large
// <system-reminder> block inside the last user message. We skip system-context
// blocks and return only the human-typed text.
func extractGitLabDuoUserText(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	// Walk messages in reverse; for each user message try to extract the
	// actual question (skipping system-context injections).
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		if text := extractUserQuestion(req.Messages[i].Content); text != "" {
			return text
		}
	}
	return ""
}

// extractUserQuestion extracts the human-typed question from an Anthropic
// content field. Claude Code injects a large <system-reminder> block as the
// first text block; the actual question is in a later block or is the only
// block when there is no system injection.
func extractUserQuestion(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Plain string content — return as-is unless it looks like a system injection.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if !isSystemInjection(s) {
			return s
		}
		// The whole string is a system injection — nothing useful here.
		return ""
	}
	// Array of content blocks.
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	// Collect non-system text blocks; prefer the last one.
	var userParts []string
	for _, b := range blocks {
		btype, _ := b["type"].(string)
		switch btype {
		case "text":
			t, _ := b["text"].(string)
			if t != "" && !isSystemInjection(t) {
				userParts = append(userParts, t)
			}
		case "tool_result":
			// Skip tool results — they are context, not the user question.
		}
	}
	if len(userParts) > 0 {
		return strings.Join(userParts, "\n")
	}
	// Fallback: if all blocks were system injections, return the shortest one
	// (least likely to be a giant system prompt).
	shortest := ""
	for _, b := range blocks {
		if btype, _ := b["type"].(string); btype == "text" {
			if t, _ := b["text"].(string); t != "" {
				if shortest == "" || len(t) < len(shortest) {
					shortest = t
				}
			}
		}
	}
	return shortest
}

// isSystemInjection reports whether a text block looks like a Claude Code
// system-context injection rather than a human-typed message.
func isSystemInjection(s string) bool {
	prefixes := []string{
		"<system-reminder>",
		"<context>",
		"SessionStart:",
		"x-anthropic-billing-header:",
	}
	trimmed := strings.TrimSpace(s)
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// decodeRawContent decodes an Anthropic content field (string or []block).
func decodeRawContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if t, ok := b["text"].(string); ok && t != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(t)
		}
	}
	return sb.String()
}

// flattenAnySystem converts Anthropic's polymorphic system field to a string.
func flattenAnySystem(system any) string {
	switch s := system.(type) {
	case string:
		return s
	case []any:
		var sb strings.Builder
		for _, block := range s {
			if bm, ok := block.(map[string]any); ok {
				if t, ok := bm["text"].(string); ok && t != "" {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(t)
				}
			}
		}
		return sb.String()
	}
	return ""
}

// extractGitLabDuoNamespaceID tries to find a GitLab namespace/group ID in
// the request body or returns empty string (the handler will omit the param).
func extractGitLabDuoNamespaceID(body []byte) string {
	var req struct {
		NamespaceID string `json:"namespace_id"`
		GroupID     string `json:"group_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	if req.NamespaceID != "" {
		return req.NamespaceID
	}
	return req.GroupID
}
