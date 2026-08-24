package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer/cc"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer/cx/chat"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer/cx/responses"
)

const (
	codexClientVersion = "0.101.0"
	codexUserAgent     = "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
)

// prepareTransformerForClient creates transformer based on client format and endpoint.
// effectiveModel is the final model that should reach upstream after applying
// endpoint-level overrides or falling back to the original request model.
func prepareTransformerForClient(clientFormat ClientFormat, endpoint config.Endpoint, effectiveModel string) (transformer.Transformer, error) {
	endpointTransformer := endpoint.Transformer
	if endpointTransformer == "" {
		endpointTransformer = "claude"
	}

	switch clientFormat {
	case ClientFormatClaude:
		return prepareCCTransformer(endpoint, endpointTransformer, effectiveModel)
	case ClientFormatOpenAIChat:
		return prepareCxChatTransformer(endpoint, endpointTransformer, effectiveModel)
	case ClientFormatOpenAIResponses:
		return prepareCxRespTransformer(endpoint, endpointTransformer, effectiveModel)
	}

	return nil, fmt.Errorf("unsupported client format: %s", clientFormat)
}

// transformerFactory maps an endpoint-transformer name ("claude", "openai",
// "openai2", "gemini") to a constructor that produces a transformer.Transformer
// for one specific client format (cc / cx-chat / cx-resp). The structure lets
// the three prepare*Transformer wrappers share a single switch and error path.
type transformerFactory map[string]func(effectiveModel string) transformer.Transformer

// ccFactory builds Claude Code (`cc_*`) client-side transformers.
//
// cc_claude is special-cased: a missing effectiveModel means "pass through
// the original model" and is the documented happy path, so the wrapper
// distinguishes it from the model-override variant.
var ccFactory = transformerFactory{
	"claude": func(effectiveModel string) transformer.Transformer {
		if effectiveModel != "" {
			return cc.NewClaudeTransformerWithModel(effectiveModel)
		}
		return cc.NewClaudeTransformer()
	},
	"openai":    func(m string) transformer.Transformer { return cc.NewOpenAITransformer(m) },
	"openai2":   func(m string) transformer.Transformer { return cc.NewOpenAI2Transformer(m) },
	"gemini":    func(m string) transformer.Transformer { return cc.NewGeminiTransformer(m) },
	"gitlabduo": func(m string) transformer.Transformer { return cc.NewGitLabDuoTransformer(m) },
	"1minai":    func(m string) transformer.Transformer { return cc.NewOneMinAITransformer(m) },
}

var cxChatFactory = transformerFactory{
	"claude":  func(m string) transformer.Transformer { return chat.NewClaudeTransformer(m) },
	"openai":  func(m string) transformer.Transformer { return chat.NewOpenAITransformer(m) },
	"openai2": func(m string) transformer.Transformer { return chat.NewOpenAI2Transformer(m) },
	"gemini":  func(m string) transformer.Transformer { return chat.NewGeminiTransformer(m) },
	"1minai":  func(m string) transformer.Transformer { return chat.NewOneMinAITransformer(m) },
}

var cxRespFactory = transformerFactory{
	"claude":  func(m string) transformer.Transformer { return responses.NewClaudeTransformer(m) },
	"openai":  func(m string) transformer.Transformer { return responses.NewOpenAITransformer(m) },
	"openai2": func(m string) transformer.Transformer { return responses.NewOpenAI2Transformer(m) },
	"gemini":  func(m string) transformer.Transformer { return responses.NewGeminiTransformer(m) },
	"1minai":  func(m string) transformer.Transformer { return responses.NewOneMinAITransformer(m) },
}

func buildFromFactory(f transformerFactory, label string, endpoint config.Endpoint, endpointTransformer string, effectiveModel string) (transformer.Transformer, error) {
	build, ok := f[endpointTransformer]
	if !ok {
		return nil, fmt.Errorf("unsupported endpoint transformer for %s: %s", label, endpointTransformer)
	}
	if endpointTransformer == "claude" && label == "cc" && effectiveModel != "" {
		logger.Debug("[%s] Using cc_claude with model override: %s", endpoint.Name, effectiveModel)
	}
	return build(effectiveModel), nil
}

// prepareCCTransformer creates transformer for Claude Code client
func prepareCCTransformer(endpoint config.Endpoint, endpointTransformer string, effectiveModel string) (transformer.Transformer, error) {
	return buildFromFactory(ccFactory, "cc", endpoint, endpointTransformer, effectiveModel)
}

// prepareCxChatTransformer creates transformer for Codex Chat API client
func prepareCxChatTransformer(endpoint config.Endpoint, endpointTransformer string, effectiveModel string) (transformer.Transformer, error) {
	return buildFromFactory(cxChatFactory, "Codex Chat", endpoint, endpointTransformer, effectiveModel)
}

// prepareCxRespTransformer creates transformer for Codex Responses API client
func prepareCxRespTransformer(endpoint config.Endpoint, endpointTransformer string, effectiveModel string) (transformer.Transformer, error) {
	return buildFromFactory(cxRespFactory, "Codex Responses", endpoint, endpointTransformer, effectiveModel)
}

// forwardableClientHeaders is the allowlist of client headers relayed upstream.
// Everything else — most importantly the client's own Authorization / x-api-key
// and any Cookie — is dropped, because the endpoint on the other side is often a
// third party (OpenAI, Google, GitLab, 1min.AI) that has no business seeing the
// caller's Anthropic key.
//
// Keys are in canonical form (http.CanonicalHeaderKey).
var forwardableClientHeaders = map[string]bool{
	"Content-Type":      true,
	"Accept":            true,
	"Accept-Language":   true,
	"User-Agent":        true,
	"Anthropic-Version": true,
	"Anthropic-Beta":    true,
}

// getTargetPath determines the target API path based on transformer name
func getTargetPath(originalPath string, endpoint config.Endpoint, transformedBody []byte, transformerName string, modelName string) string {
	switch transformerName {
	case "cc_claude", "cx_chat_claude", "cx_resp_claude":
		return "/v1/messages"
	case "cc_openai", "cx_chat_openai", "cx_resp_openai":
		return "/v1/chat/completions"
	case "cc_openai2", "cx_resp_openai2", "cx_chat_openai2":
		return "/v1/responses"
	case "cc_1minai", "cx_chat_1minai", "cx_resp_1minai":
		return "/api/features"
	case "cc_gitlabduo":
		// GitLab Duo Chat completions REST endpoint. The endpoint URL
		// configured for this transformer should be the GitLab instance
		// root (e.g. "https://gitlab.com"); we append the API path here.
		return "/api/v4/chat/completions"
	case "cc_gemini", "cx_chat_gemini", "cx_resp_gemini":
		var geminiReq struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(transformedBody, &geminiReq); err != nil {
			// Not fatal: a body we cannot parse just means we cannot tell whether
			// the caller asked for streaming, so fall through to the unary path.
			logger.Debug("[%s] Could not inspect gemini payload for stream flag: %v", endpoint.Name, err)
		}
		model := strings.TrimSpace(modelName)
		if model == "" {
			model = strings.TrimSpace(endpoint.Model)
		}
		if model == "" {
			// Without a model the path degrades to "/v1beta/models/:generateContent",
			// which the upstream rejects with an opaque error. Keep the original
			// path so the failure is at least attributable.
			logger.Warn("[%s] Gemini request has no model, leaving path untouched", endpoint.Name)
			return originalPath
		}
		// The model comes from the client's request body: escape it so a value
		// like "../../v1/secret" cannot rewrite the upstream path.
		model = url.PathEscape(model)
		if geminiReq.Stream {
			return fmt.Sprintf("/v1beta/models/%s:streamGenerateContent", model)
		}
		return fmt.Sprintf("/v1beta/models/%s:generateContent", model)
	}
	return originalPath
}

// buildProxyRequest creates an HTTP request for the target API
func buildProxyRequest(r *http.Request, endpoint config.Endpoint, apiKey string, transformedBody []byte, transformerName string, modelName string, credential *storage.EndpointCredential) (*http.Request, error) {
	targetPath := getTargetPath(r.URL.Path, endpoint, transformedBody, transformerName, modelName)
	if targetPath == "" {
		targetPath = r.URL.Path
	}

	normalizedAPIUrl := strings.TrimRight(normalizeAPIUrl(endpoint.APIUrl), "/")
	targetPath = normalizeTargetPathForBaseURL(normalizedAPIUrl, targetPath)
	if targetPath != "" && !strings.HasPrefix(targetPath, "/") {
		targetPath = "/" + targetPath
	}
	requestBody := transformedBody
	if isCodexBackendBaseURL(normalizedAPIUrl) && isResponsesPath(targetPath) {
		requestBody = ensureCodexResponsesPayload(requestBody)
	}
	targetURL := fmt.Sprintf("%s%s", normalizedAPIUrl, targetPath)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// The inbound request's context is a sane default; sendRequest replaces it
	// with the merged client+endpoint context before the call goes out.
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	// Forward only an allowlist of client headers. Copying everything (the
	// previous behavior) leaked the client's own credentials to third parties:
	// the gemini branch passes the key as a query param and the openai branches
	// set Authorization, but neither stripped the x-api-key / Authorization the
	// client had already sent — so an ANTHROPIC_API_KEY traveled to Google or
	// OpenAI. Cookies went along too.
	for key, values := range r.Header {
		if !forwardableClientHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	// Nothing below should ever see a credential the client supplied.
	proxyReq.Header.Del("Authorization")
	proxyReq.Header.Del("X-Api-Key")
	proxyReq.Header.Del("Cookie")

	// Force gzip or no compression to avoid unsupported encodings (e.g., brotli)
	proxyReq.Header.Set("Accept-Encoding", "gzip, identity")

	// Set authentication based on transformer type
	switch transformerName {
	case "cc_openai", "cc_openai2", "cx_chat_openai", "cx_chat_openai2", "cx_resp_openai", "cx_resp_openai2":
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	case "cc_1minai", "cx_chat_1minai", "cx_resp_1minai":
		proxyReq.Header.Set("API-KEY", apiKey)
		proxyReq.Header.Set("Content-Type", "application/json")
		proxyReq.Header.Del("Authorization")
		proxyReq.Header.Del("x-api-key")
		proxyReq.Header.Del("X-Api-Key")
	case "cc_gitlabduo":
		// GitLab accepts both PRIVATE-TOKEN (PAT) and Authorization: Bearer
		// (PAT or OAuth) — set both so any kind of GitLab token works.
		proxyReq.Header.Set("PRIVATE-TOKEN", apiKey)
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
		// Strip the Anthropic-style x-api-key header that Claude Code adds —
		// some upstreams reject unexpected auth headers.
		proxyReq.Header.Del("x-api-key")
		proxyReq.Header.Del("X-Api-Key")
	case "cc_gemini", "cx_chat_gemini", "cx_resp_gemini":
		q := proxyReq.URL.Query()
		q.Set("key", apiKey)
		q.Set("alt", "sse")
		proxyReq.URL.RawQuery = q.Encode()
	default:
		// Claude endpoints
		proxyReq.Header.Set("x-api-key", apiKey)
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// Set the Host used for the upstream request. net/http ignores a "Host" key
	// in the header map, so the previous Header.Set("Host", …) was a no-op;
	// req.Host is the field that actually controls the sent Host header.
	if parsedBase, err := url.Parse(normalizedAPIUrl); err == nil && strings.TrimSpace(parsedBase.Host) != "" {
		proxyReq.Host = parsedBase.Host
	}
	applyCodexCredentialHeaders(proxyReq, credential, requestBody)

	return proxyReq, nil
}

func applyCodexCredentialHeaders(req *http.Request, credential *storage.EndpointCredential, payload []byte) {
	if req == nil || credential == nil {
		return
	}
	if !isCodexProviderType(credential.ProviderType) {
		return
	}
	if !isResponsesPath(req.URL.Path) {
		return
	}

	// Match Codex client headers for oauth credentials.
	ensureHeader(req.Header, "Version", codexClientVersion)
	ensureHeader(req.Header, "Session_id", uuid.NewString())
	ensureHeader(req.Header, "User-Agent", codexUserAgent)

	if isStreamingRequest(payload) {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Connection", "Keep-Alive")
	req.Header.Set("Originator", "codex_cli_rs")
	if accountID := strings.TrimSpace(credential.AccountID); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
}

func ensureHeader(headers http.Header, key, value string) {
	if headers == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(headers.Get(key)) == "" {
		headers.Set(key, value)
	}
}

func isResponsesPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return strings.HasSuffix(trimmed, "/responses") || strings.HasSuffix(trimmed, "/responses/compact")
}

func isStreamingRequest(payload []byte) bool {
	var streamReq struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(payload, &streamReq); err != nil {
		return false
	}
	return streamReq.Stream
}

func isCodexProviderType(providerType string) bool {
	p := strings.ToLower(strings.TrimSpace(providerType))
	return p == "" || p == "codex"
}

// normalizeTargetPathForBaseURL adjusts OpenAI Responses paths for Codex backend base URLs.
// This is endpoint URL compatibility handling and is independent from auth mode.
func normalizeTargetPathForBaseURL(baseURL, targetPath string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil {
		return targetPath
	}

	cleanPath := path.Clean(strings.TrimSpace(parsed.Path))
	isCodexBackend := strings.HasSuffix(cleanPath, "/backend-api/codex")
	if isCodexBackend {
		switch strings.TrimSpace(targetPath) {
		case "/v1/responses":
			return "/responses"
		case "/v1/responses/compact":
			return "/responses/compact"
		default:
			return targetPath
		}
	}

	if targetPath == "/api/features" {
		if strings.HasSuffix(cleanPath, "/api/features") {
			return ""
		}
		if strings.HasSuffix(cleanPath, "/api") {
			return "/features"
		}
	}

	return targetPath
}

func isCodexBackendBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil {
		return false
	}
	cleanPath := path.Clean(strings.TrimSpace(parsed.Path))
	return strings.HasSuffix(cleanPath, "/backend-api/codex")
}

// ensureCodexResponsesPayload adds the fields the codex backend requires on
// /v1/responses. Thin wrapper over rewriteRequestPayload; see payload.go.
func ensureCodexResponsesPayload(payload []byte) []byte {
	updated, err := rewriteRequestPayload(payload, payloadRewrite{CodexResponses: true})
	if err != nil {
		logger.Debug("Skipping codex payload fixup: %v", err)
	}
	return updated
}

// overrideModelInPayload replaces the top-level model field. Thin wrapper over
// rewriteRequestPayload; see payload.go.
func overrideModelInPayload(payload []byte, model string) []byte {
	updated, err := rewriteRequestPayload(payload, payloadRewrite{Model: model})
	if err != nil {
		logger.Debug("Skipping model override: %v", err)
	}
	return updated
}

// sendRequest sends the HTTP request and returns the response
func sendRequest(ctx context.Context, proxyReq *http.Request, httpClient *http.Client, cfg *config.Config) (*http.Response, error) {
	proxyReq = proxyReq.WithContext(ctx)

	proxyURL := resolveProxyURLForRequest(cfg, proxyReq.URL)
	// Apply proxy if configured
	if strings.TrimSpace(proxyURL) != "" {
		client, err := proxyClientFor(proxyURL, httpClient.Timeout)
		if err != nil {
			logger.Warn("Failed to create proxy transport: %v, using direct connection", err)
			return httpClient.Do(proxyReq)
		}
		return client.Do(proxyReq)
	}

	return httpClient.Do(proxyReq)
}

// proxyClients caches one *http.Client (and its *http.Transport) per proxy URL.
//
// Building a fresh Transport per request gave every request its own idle
// connection pool, so nothing was ever reused: a full TCP+TLS handshake on each
// call, and the sockets plus their readLoop/writeLoop goroutines piled up until
// IdleConnTimeout (90s). One Transport per distinct proxy URL is both correct and
// what net/http expects.
// maxCachedProxyClients bounds the cache. In practice one or two proxy URLs are
// ever in play; the cap only matters because the key is user-editable config, so
// repeatedly changing the proxy in the UI would otherwise accumulate Transports —
// each holding its own idle connections — for the lifetime of the process.
const maxCachedProxyClients = 8

var (
	proxyClientsMu sync.Mutex
	proxyClients   = map[string]*http.Client{}
)

func proxyClientFor(proxyURL string, timeout time.Duration) (*http.Client, error) {
	proxyClientsMu.Lock()
	defer proxyClientsMu.Unlock()

	if c, ok := proxyClients[proxyURL]; ok {
		return c, nil
	}
	transport, err := CreateProxyTransport(proxyURL)
	if err != nil {
		return nil, err
	}

	if len(proxyClients) >= maxCachedProxyClients {
		// Drop everything rather than track an eviction order: entries are
		// interchangeable, and a full reset costs one handshake per proxy still
		// in use. CloseIdleConnections is the point — without it the sockets
		// linger until IdleConnTimeout with nothing referencing them.
		evictProxyClientsLocked()
	}

	c := &http.Client{Transport: transport, Timeout: timeout}
	proxyClients[proxyURL] = c
	return c, nil
}

// evictProxyClientsLocked empties the cache, closing idle connections first.
// Callers must hold proxyClientsMu. In-flight requests keep working: they hold
// their own reference to the client, and CloseIdleConnections only touches
// connections that are not in use.
func evictProxyClientsLocked() {
	for key, client := range proxyClients {
		if t, ok := client.Transport.(*http.Transport); ok {
			t.CloseIdleConnections()
		}
		delete(proxyClients, key)
	}
	logger.Debug("Proxy client cache reached %d entries, cleared it", maxCachedProxyClients)
}

func resolveProxyURLForRequest(cfg *config.Config, targetURL *url.URL) string {
	if cfg == nil {
		return ""
	}
	if isCodexRequestURL(targetURL) {
		if codexProxy := cfg.GetCodexProxy(); codexProxy != nil && strings.TrimSpace(codexProxy.URL) != "" {
			return codexProxy.URL
		}
	}
	if proxyCfg := cfg.GetProxy(); proxyCfg != nil && strings.TrimSpace(proxyCfg.URL) != "" {
		return proxyCfg.URL
	}
	return ""
}

func isCodexRequestURL(targetURL *url.URL) bool {
	if targetURL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(targetURL.Host))
	if host != "chatgpt.com" {
		return false
	}
	cleanPath := path.Clean(strings.TrimSpace(targetURL.Path))
	return strings.Contains(cleanPath, "/backend-api/codex")
}

// CreateProxyTransport creates an http.Transport with proxy support
func CreateProxyTransport(proxyURL string) (*http.Transport, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	transport := &http.Transport{
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		ResponseHeaderTimeout:  90 * time.Second,
		WriteBufferSize:        128 * 1024, // 128KB write buffer for large SSE streams
		ReadBufferSize:         128 * 1024, // 128KB read buffer for large SSE streams
		MaxResponseHeaderBytes: 64 * 1024,  // 64KB max response headers
	}

	switch parsed.Scheme {
	case "socks5", "socks5h":
		auth := &proxy.Auth{}
		if parsed.User != nil {
			auth.User = parsed.User.Username()
			auth.Password, _ = parsed.User.Password()
		} else {
			auth = nil
		}
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		// DialContext, not the deprecated Dial: with Dial the request context
		// cannot abort a hung SOCKS5 handshake.
		if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = ctxDialer.DialContext
		} else {
			// Fallback for a SOCKS5 dialer that does not implement
			// ContextDialer. golang.org/x/net/proxy always does today, so this is
			// defensive; Dial is the only option when it does not.
			transport.Dial = dialer.Dial //nolint:staticcheck // no context-aware alternative on this path
		}
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}

	return transport, nil
}
