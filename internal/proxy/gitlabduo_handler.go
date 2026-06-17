package proxy

// GitLab Duo WebSocket handler.
//
// Real protocol (reverse-engineered from duo.exe source — the `coI` WebSocket
// stream wrapper and `DuoWorkflowService` definitions):
//
//  1. POST /api/v4/ai/duo_workflows/workflows
//     → { "id": <workflowID> }
//
//  2. POST /api/v4/ai/duo_workflows/direct_access
//     → { "gitlab_rails": { "token": "<PAT-equivalent>", ... },
//         "duo_workflow_service": { "base_url": "...", "token": "<JWT>" } }
//
//  3. WebSocket wss://<gitlab_host>/api/v4/ai/duo_workflows/ws
//       ?root_namespace_id=<nsID>
//       &user_selected_model_identifier=<model>
//       &workflow_definition=chat
//     Auth headers (from duo.exe #G method):
//       authorization: Bearer <gitlab_rails.token>
//       x-request-id: <uuid>
//       x-gitlab-root-namespace-id: <nsID>
//       x-gitlab-client-type: node-websocket
//
//     IMPORTANT: messages are TEXT frames with JSON (NOT binary protobuf).
//       Send:  WebSocket.send(JSON.stringify(ClientEvent))
//       Recv:  Action.fromJSON(JSON.parse(text))
//
//     Send ClientEvent:
//       {"startRequest":{"clientVersion":"1.0","workflowID":"...",
//         "workflowDefinition":"chat","goal":"...","workflowMetadata":"{...}",
//         "clientCapabilities":[...]}}
//     Heartbeat:
//       {"heartbeat":{"timestamp":<unix-ms>}}
//     Recv Action:
//       {"newCheckpoint":{"status":"INPUT_REQUIRED","checkpoint":"{...json...}", ...}}
//     Stop on: newCheckpoint.status == "INPUT_REQUIRED" | "FINISHED" | "FAILED"

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/google/uuid"
	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// gitlabHTTPClient is a dedicated HTTP/1.1-only client for GitLab REST calls.
var gitlabHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{},
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 4,
	},
}

// handleGitLabDuoRequest is the entry point called from the main proxy
// handler when it detects a gitlabduo endpoint.
func (p *Proxy) handleGitLabDuoRequest(
	w http.ResponseWriter,
	r *http.Request,
	endpoint config.Endpoint,
	apiKey string,
	credentialID int64,
	userText string,
	modelIdentifier string,
	namespaceID string,
) {
	baseURL := strings.TrimRight(strings.TrimSpace(endpoint.APIUrl), "/")
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}

	// Context is bound to BOTH the client's request and a 180 s upper bound.
	// When Claude Code aborts (Esc/Ctrl+C) r.Context() is cancelled and we tear
	// down the workflow instead of burning a Duo credit for nothing.
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	// ── Deduplicate identical in-flight requests ─────────────────────────────
	// Claude Code sometimes fires the same /v1/messages twice in parallel.
	// Without dedup each one spends a Duo credit.
	dedupKey := computeInflightKey(endpoint.Name, apiKey, userText)
	infl, isLeader := globalDuoInflight.acquire(dedupKey)
	if !isLeader {
		logger.Info("[%s] GitLab Duo: deduplicating in-flight request", endpoint.Name)
		select {
		case <-infl.done:
			if infl.result.err != nil {
				writeProxyError(w, http.StatusBadGateway, "api_error",
					fmt.Sprintf("GitLab Duo: %v", infl.result.err))
				return
			}
			p.writeDuoSSEResponse(w, modelIdentifier, infl.result.answer)
			return
		case <-ctx.Done():
			writeProxyError(w, http.StatusRequestTimeout, "client_cancelled",
				"client cancelled while waiting for in-flight request")
			return
		}
	}
	// We are the leader — make sure waiters are released even on panic.
	publishedResult := duoInflightResult{}
	defer func() {
		globalDuoInflight.publish(dedupKey, infl, publishedResult)
	}()

	// ── Run the workflow with one automatic retry on transient errors ────────
	answer, err := p.runDuoWorkflow(ctx, endpoint, apiKey, credentialID, baseURL,
		userText, modelIdentifier, namespaceID)
	if err != nil && shouldRetryDuoError(err) && ctx.Err() == nil {
		logger.Info("[%s] GitLab Duo: retrying after transient error: %v", endpoint.Name, err)
		answer, err = p.runDuoWorkflow(ctx, endpoint, apiKey, credentialID, baseURL,
			userText, modelIdentifier, namespaceID)
	}
	if err != nil {
		publishedResult = duoInflightResult{err: err}
		if isGitLabDuoCreditError(err) {
			// markCredentialFailure was already called inside runDuoWorkflow.
			writeProxyError(w, http.StatusForbidden, "insufficient_quota",
				"GitLab Duo: insufficient GitLab credits for this token — it has been deactivated. Add a token with available Duo credits.")
			return
		}
		if ctx.Err() != nil {
			// Client cancelled or timed out — don't log as ERROR.
			logger.Info("[%s] GitLab Duo: request cancelled: %v", endpoint.Name, ctx.Err())
			return
		}
		logger.Error("[%s] GitLab Duo: %v", endpoint.Name, err)
		writeProxyError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("GitLab Duo: %v", err))
		return
	}
	publishedResult = duoInflightResult{answer: answer}

	// ── Record usage so the UI shows token counts for gitlabduo too ──────────
	// Output tokens are estimated from rune count (≈4 chars per token for English,
	// closer to 2 for Cyrillic — we use 3 as a middle ground).
	estOutputTokens := len([]rune(answer)) / 3
	estInputTokens := len([]rune(userText)) / 3
	p.recordCredentialUsage(credentialID, endpoint.Name, 1, 0, estInputTokens, estOutputTokens)

	p.writeDuoSSEResponse(w, modelIdentifier, answer)
}

// writeDuoSSEResponse writes the Anthropic SSE stream for a Duo answer.
func (p *Proxy) writeDuoSSEResponse(w http.ResponseWriter, modelIdentifier, answer string) {
	echoModel := modelIdentifier
	if echoModel == "" {
		echoModel = "gitlab-duo"
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	streamAnthropicSSE(w, echoModel, answer)
}

// runDuoWorkflow performs one full Duo workflow cycle: create → direct_access →
// WebSocket chat. Returns the assistant answer or an error.
func (p *Proxy) runDuoWorkflow(
	ctx context.Context,
	endpoint config.Endpoint,
	apiKey string,
	credentialID int64,
	baseURL, userText, modelIdentifier, namespaceID string,
) (string, error) {
	workflowID, err := gitlabCreateWorkflow(ctx, baseURL, apiKey, namespaceID, userText)
	if err != nil {
		if isGitLabDuoCreditError(err) {
			logger.Warn("[%s] GitLab Duo: token (cred=%d) has insufficient credits — deactivating",
				endpoint.Name, credentialID)
			p.markCredentialFailure(credentialID, http.StatusForbidden,
				"GitLab Duo: insufficient credits")
		}
		return "", fmt.Errorf("create workflow: %w", err)
	}
	logger.Info("[%s] GitLab Duo: workflow created: %s", endpoint.Name, workflowID)

	da, err := gitlabDirectAccess(ctx, baseURL, apiKey, namespaceID, workflowID)
	if err != nil {
		return "", fmt.Errorf("direct_access: %w", err)
	}
	wsToken := da.GitLabRails.Token
	if wsToken == "" {
		wsToken = apiKey
	}

	answer, err := gitlabWSChat(ctx, baseURL, wsToken, workflowID, namespaceID, modelIdentifier, userText)
	if err != nil {
		return "", fmt.Errorf("ws: %w", err)
	}
	logger.DebugLog("[%s] GitLab Duo: answer len=%d", endpoint.Name, len(answer))
	return answer, nil
}

// shouldRetryDuoError reports whether err looks transient enough to retry once.
// We retry on network/timeout errors but never on auth/credit/4xx errors.
func shouldRetryDuoError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Don't retry permanent failures.
	if isGitLabDuoCreditError(err) ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "404") {
		return false
	}
	// Retry on these transient signals.
	return strings.Contains(msg, "ws ") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "no answer received")
}

// ── WebSocket chat (JSON protocol) ───────────────────────────────────────────

// duoClientEvent is the JSON envelope the client sends over the WebSocket.
// Exactly one field is set per message (matches ClientEvent.toJSON in duo.exe).
type duoClientEvent struct {
	StartRequest *duoStartWorkflowRequest `json:"startRequest,omitempty"`
	Heartbeat    *duoHeartbeat            `json:"heartbeat,omitempty"`
}

type duoStartWorkflowRequest struct {
	ClientVersion      string   `json:"clientVersion,omitempty"`
	WorkflowID         string   `json:"workflowID,omitempty"`
	WorkflowDefinition string   `json:"workflowDefinition,omitempty"`
	Goal               string   `json:"goal,omitempty"`
	WorkflowMetadata   string   `json:"workflowMetadata,omitempty"`
	ClientCapabilities []string `json:"clientCapabilities,omitempty"`
}

type duoHeartbeat struct {
	Timestamp int64 `json:"timestamp"`
}

// duoAction is the JSON envelope the server sends over the WebSocket.
type duoAction struct {
	RequestID     string            `json:"requestID,omitempty"`
	NewCheckpoint *duoNewCheckpoint `json:"newCheckpoint,omitempty"`
}

type duoNewCheckpoint struct {
	Status     string   `json:"status,omitempty"`
	Checkpoint string   `json:"checkpoint,omitempty"`
	Goal       string   `json:"goal,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// gitlabWSChat dials the WebSocket, sends startRequest, reads checkpoints
// until a terminal status, and returns the assistant answer.
// GitLab Duo closes the connection after each answer (StatusNormalClosure),
// so we create a new connection per request. Context is preserved by passing
// the full conversation history as userText (see extractGitLabDuoHistory).
func gitlabWSChat(
	ctx context.Context,
	gitlabBaseURL, wsToken, workflowID, namespaceID, modelID, userText string,
) (string, error) {
	// Convert https:// → wss://.
	wsBase := strings.TrimRight(gitlabBaseURL, "/")
	wsBase = strings.Replace(wsBase, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)

	q := url.Values{}
	q.Set("workflow_definition", "chat")
	if namespaceID != "" {
		q.Set("root_namespace_id", namespaceID)
	}
	gitlabModel := toGitLabIdentifier(modelID)
	if gitlabModel != "" {
		q.Set("user_selected_model_identifier", gitlabModel)
	}
	wsURL := wsBase + "/api/v4/ai/duo_workflows/ws?" + q.Encode()

	reqID := uuid.NewString()
	logger.DebugLog("[gitlabduo] WS connecting: %s reqID=%s", wsURL, reqID)

	gitlabOrigin := gitlabBaseURL
	if u, err := url.Parse(gitlabBaseURL); err == nil {
		gitlabOrigin = u.Scheme + "://" + u.Host
	}

	dialHeaders := http.Header{
		"Authorization":        []string{"Bearer " + wsToken},
		"X-Request-Id":         []string{reqID},
		"X-Gitlab-Client-Type": []string{"node-websocket"},
		"User-Agent":           []string{"gitlab-duo-cli/8.104.0"},
		"Origin":               []string{gitlabOrigin},
	}
	if namespaceID != "" {
		dialHeaders["X-Gitlab-Root-Namespace-Id"] = []string{namespaceID}
	}

	conn, httpResp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: dialHeaders,
	})
	if err != nil {
		if httpResp != nil {
			body, _ := io.ReadAll(httpResp.Body)
			httpResp.Body.Close()
			return "", fmt.Errorf("ws dial HTTP %d: %s — %w", httpResp.StatusCode, truncate(string(body), 400), err)
		}
		return "", fmt.Errorf("ws dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20) // 8 MiB

	gitlabModel2 := toGitLabIdentifier(modelID)
	metaMap := map[string]any{
		"projectId":               "",
		"namespaceId":             "",
		"rootNamespaceId":         namespaceID,
		"selectedModelIdentifier": gitlabModel2,
	}
	metaBytes, _ := json.Marshal(metaMap)

	startEvent := duoClientEvent{
		StartRequest: &duoStartWorkflowRequest{
			ClientVersion:      "1.0",
			WorkflowID:         workflowID,
			WorkflowDefinition: "chat",
			Goal:               truncateGoal(userText),
			WorkflowMetadata:   string(metaBytes),
			ClientCapabilities: []string{
				"shell_command", "read_file_chunked", "tool_call_approval",
				"tool_call_pattern_approval", "command_timeout", "web_search",
				"incremental_streaming",
			},
		},
	}
	startBytes, _ := json.Marshal(startEvent)
	logger.DebugLog("[gitlabduo] WS sending startRequest len=%d", len(startBytes))
	if err := conn.Write(ctx, websocket.MessageText, startBytes); err != nil {
		return "", fmt.Errorf("ws write startRequest: %w", err)
	}

	// Immediate heartbeat.
	hb := duoClientEvent{Heartbeat: &duoHeartbeat{Timestamp: time.Now().UnixMilli()}}
	hbBytes, _ := json.Marshal(hb)
	_ = conn.Write(ctx, websocket.MessageText, hbBytes)

	// Periodic heartbeat goroutine.
	hbDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				hb := duoClientEvent{Heartbeat: &duoHeartbeat{Timestamp: time.Now().UnixMilli()}}
				b, _ := json.Marshal(hb)
				_ = conn.Write(ctx, websocket.MessageText, b)
			case <-hbDone:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	defer close(hbDone)

	// Read JSON frames until terminal checkpoint.
	var answer string
	for {
		_, rawMsg, err := conn.Read(ctx)
		if err != nil {
			closeCode := websocket.CloseStatus(err)
			if closeCode == websocket.StatusNormalClosure || closeCode == websocket.StatusGoingAway {
				break // server closed normally after delivering answer
			}
			if answer != "" {
				break // got answer, ignore trailing error
			}
			return "", fmt.Errorf("ws read: %w", err)
		}

		var action duoAction
		if err := json.Unmarshal(rawMsg, &action); err != nil {
			logger.DebugLog("[gitlabduo] WS frame not JSON: %v", err)
			continue
		}
		if action.NewCheckpoint == nil {
			continue
		}
		cp := action.NewCheckpoint
		logger.DebugLog("[gitlabduo] WS checkpoint status=%q len=%d", cp.Status, len(cp.Checkpoint))

		if cp.Status == "FAILED" {
			logger.Error("[gitlabduo] WS FAILED checkpoint=%s errors=%v",
				truncate(cp.Checkpoint, 600), cp.Errors)
		}
		if cp.Checkpoint != "" {
			if text := extractAnswerFromCheckpoint(cp.Checkpoint); text != "" {
				answer = text
			}
		}
		switch cp.Status {
		case "INPUT_REQUIRED", "FINISHED", "COMPLETED", "FAILED", "CANCELLED":
			goto done
		}
	}
done:
	conn.Close(websocket.StatusNormalClosure, "")
	if answer == "" {
		return "", fmt.Errorf("no answer received from GitLab Duo WebSocket")
	}
	return answer, nil
}

// ── GitLab REST helpers ──────────────────────────────────────────────────────

// gitlabGoalMaxLen is the GitLab API limit for the goal field.
const gitlabGoalMaxLen = 16384

// truncateGoal trims goal to the API limit. We keep the tail (the actual
// user question) rather than the head (which is usually system context).
func truncateGoal(goal string) string {
	runes := []rune(goal)
	if len(runes) <= gitlabGoalMaxLen {
		return goal
	}
	logger.DebugLog("[gitlabduo] goal too long (%d chars), truncating to %d", len(runes), gitlabGoalMaxLen)
	return string(runes[len(runes)-gitlabGoalMaxLen:])
}

func gitlabCreateWorkflow(ctx context.Context, baseURL, token, namespaceID, goal string) (string, error) {
	// Agent privileges enum (from duo.exe h_):
	//   1=READ_WRITE_FILES 2=READ_ONLY_GITLAB 3=READ_WRITE_GITLAB
	//   4=RUN_COMMANDS 5=USE_GIT 6=RUN_MCP_TOOLS
	// Duo CLI's default set (vRH) is all six.
	privileges := []int{1, 2, 3, 4, 5, 6}

	// Body mirrors duo.exe createWorkflow for instance >= 19.1.0. Without
	// agent_privileges / environment the Agent Platform fails the workflow
	// immediately ("There was an error processing your request...").
	body := map[string]any{
		"goal":                          truncateGoal(goal),
		"workflow_definition":           "chat",
		"environment":                   "ide",
		"allow_agent_to_request_user":   true,
		"agent_privileges":              privileges,
		"pre_approved_agent_privileges": privileges,
	}
	if namespaceID != "" {
		body["root_namespace_id"] = namespaceID
	}
	raw, err := gitlabPost(ctx, baseURL+"/api/v4/ai/duo_workflows/workflows", token, body)
	if err != nil {
		return "", err
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return "", fmt.Errorf("parse workflow response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	for _, k := range []string{"id", "workflow_id", "workflowId"} {
		if v, ok := generic[k]; ok {
			switch n := v.(type) {
			case float64:
				return fmt.Sprintf("%.0f", n), nil
			default:
				return fmt.Sprintf("%v", v), nil
			}
		}
	}
	return "", fmt.Errorf("no id in workflow response: %s", truncate(string(raw), 200))
}

type directAccessResponse struct {
	GitLabRails struct {
		BaseURL        string `json:"base_url"`
		Token          string `json:"token"`
		TokenExpiresAt string `json:"token_expires_at"`
	} `json:"gitlab_rails"`
	DuoWorkflowService struct {
		BaseURL string `json:"base_url"`
		Token   string `json:"token"`
	} `json:"duo_workflow_service"`
}

func gitlabDirectAccess(ctx context.Context, baseURL, token, namespaceID, workflowID string) (*directAccessResponse, error) {
	body := map[string]any{}
	if namespaceID != "" {
		body["root_namespace_id"] = namespaceID
	}
	if workflowID != "" {
		body["workflow_id"] = workflowID
	}
	raw, err := gitlabPost(ctx, baseURL+"/api/v4/ai/duo_workflows/direct_access", token, body)
	if err != nil {
		return nil, err
	}
	var resp directAccessResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse direct_access response: %w (body: %s)", err, truncate(string(raw), 300))
	}
	// We only strictly need gitlab_rails.token for the WebSocket; the
	// duo_workflow_service block is informational here.
	return &resp, nil
}

func gitlabPost(ctx context.Context, url, token string, body map[string]any) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := gitlabHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	return respBody, nil
}

// ── Anthropic SSE streamer ───────────────────────────────────────────────────

// streamAnthropicSSE writes a full Anthropic SSE stream to w, flushing after
// each word-level chunk so Claude Code renders text progressively.
func streamAnthropicSSE(w http.ResponseWriter, model, answer string) {
	f, canFlush := w.(http.Flusher)
	flush := func() {
		if canFlush {
			f.Flush()
		}
	}

	writeEvent := func(event string, payload map[string]any) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	}

	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	writeEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})
	writeEvent("ping", map[string]any{"type": "ping"})
	flush()

	// Split answer into word-boundary chunks and stream each with a flush.
	// We group ~3 words per chunk to balance visual smoothness vs syscall overhead.
	words := splitIntoChunks(answer, 3)
	for _, chunk := range words {
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": chunk,
			},
		})
		flush()
	}

	writeEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	})
	writeEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]any{"output_tokens": len([]rune(answer))},
	})
	writeEvent("message_stop", map[string]any{"type": "message_stop"})
	flush()
}

// splitIntoChunks splits text into chunks of ~wordsPerChunk words, preserving
// whitespace so the rendered output looks identical to the original.
func splitIntoChunks(text string, wordsPerChunk int) []string {
	if text == "" {
		return nil
	}
	var chunks []string
	var buf strings.Builder
	wordCount := 0
	i := 0
	runes := []rune(text)
	n := len(runes)

	for i < n {
		// Collect a word (non-space run).
		if runes[i] != ' ' && runes[i] != '\n' && runes[i] != '\t' {
			for i < n && runes[i] != ' ' && runes[i] != '\n' && runes[i] != '\t' {
				buf.WriteRune(runes[i])
				i++
			}
			wordCount++
		} else {
			// Collect whitespace.
			for i < n && (runes[i] == ' ' || runes[i] == '\n' || runes[i] == '\t') {
				buf.WriteRune(runes[i])
				i++
			}
		}

		if wordCount >= wordsPerChunk {
			chunks = append(chunks, buf.String())
			buf.Reset()
			wordCount = 0
		}
	}
	if buf.Len() > 0 {
		chunks = append(chunks, buf.String())
	}
	return chunks
}

// toGitLabIdentifier converts a human-friendly model label to a gitlab_identifier.
// "Claude Opus 4.7 - Anthropic" → "claude_opus_4_7"
// "Claude Opus 4.7 - Vertex"    → "claude_opus_4_7_vertex"
// Already snake_case identifiers pass through unchanged.
func toGitLabIdentifier(label string) string {
	s := strings.TrimSpace(label)
	if s == "" {
		return ""
	}
	// Already snake_case?
	if !strings.ContainsAny(s, " -.") && strings.ToLower(s) == s {
		return s
	}
	provider := ""
	base := s
	if idx := strings.LastIndex(s, " - "); idx >= 0 {
		provider = strings.ToLower(strings.TrimSpace(s[idx+3:]))
		base = strings.TrimSpace(s[:idx])
	}
	repl := strings.NewReplacer("-", " ", ".", " ")
	base = strings.ToLower(repl.Replace(base))
	parts := strings.Fields(base)
	identifier := strings.Join(parts, "_")
	switch provider {
	case "vertex":
		identifier += "_vertex"
	case "bedrock":
		identifier += "_bedrock"
	}
	return identifier
}

// isGitLabDuoCreditError reports whether err is a GitLab "insufficient credits"
// 403 (the token cannot run Duo workflows and should be deactivated).
func isGitLabDuoCreditError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "403") {
		return false
	}
	return strings.Contains(msg, "credit") ||
		strings.Contains(msg, "insufficient") ||
		strings.Contains(msg, "purchase more")
}

// truncate returns s truncated to n bytes with "…" suffix if longer.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
