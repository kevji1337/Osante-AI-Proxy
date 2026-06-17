package cc

// GitLab Duo Chat completions transformer.
//
// Translates between Claude Code's Anthropic-style /v1/messages traffic and
// GitLab's REST endpoint POST /api/v4/chat/completions:
//
//   Request:  Claude messages[] -> { "content": "<last user text>",
//                                    "additional_context": [] }
//   Response: GitLab returns the answer as a JSON-encoded string
//             (e.g. "To define class in ruby...").
//             We wrap it in either a non-streaming Anthropic message or a
//             synthesised Anthropic SSE stream, depending on whether the
//             original Claude Code request had stream=true.
//
// GitLab's REST endpoint is NOT an SSE endpoint (streaming is only available
// via GraphQL subscriptions), so for stream=true we emit the Anthropic SSE
// frames ourselves once we have the full answer.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// GitLabDuoTransformer implements transformer.Transformer for GitLab Duo Chat.
type GitLabDuoTransformer struct {
	model string

	// stream is captured from the incoming Claude request so we can pick the
	// right output shape in TransformResponse.
	stream bool

	// originalModel is the model the client asked for (used to echo it back
	// in the synthesised Anthropic response).
	originalModel string
}

// NewGitLabDuoTransformer builds a transformer with an optional model
// override. The model field is informational only — GitLab Duo picks its own
// backend model server-side.
func NewGitLabDuoTransformer(model string) *GitLabDuoTransformer {
	return &GitLabDuoTransformer{model: model}
}

func (t *GitLabDuoTransformer) Name() string {
	return "cc_gitlabduo"
}

// --- Request -------------------------------------------------------------

// TransformRequest converts Claude's /v1/messages payload into GitLab Duo's
// chat/completions payload: { "content": "<last user message>",
// "additional_context": [] }.
func (t *GitLabDuoTransformer) TransformRequest(req []byte) ([]byte, error) {
	var src struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		System   any    `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(req, &src); err != nil {
		return nil, fmt.Errorf("gitlabduo: invalid claude request: %w", err)
	}

	t.stream = src.Stream
	t.originalModel = src.Model

	content := extractLastUserText(src.Messages)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("gitlabduo: no user message text found in request")
	}

	// Optional: prepend the system prompt if the client provided one. GitLab
	// Duo doesn't have a separate system field on this REST endpoint, so we
	// fold it into the question.
	if sys := flattenSystem(src.System); sys != "" {
		content = sys + "\n\n" + content
	}

	out := map[string]any{
		"content":            content,
		"additional_context": []any{},
	}

	// Forward the selected model to GitLab when the endpoint is configured
	// with one. The public REST docs don't describe a `model` field, but
	// GitLab's UI clearly switches between Anthropic / Vertex / Bedrock
	// variants, so we ship it through and let GitLab decide. If the field
	// is ignored, behaviour is identical to before.
	if model := strings.TrimSpace(t.effectiveOutgoingModel()); model != "" {
		out["model"] = model
	}

	return json.Marshal(out)
}

// effectiveOutgoingModel picks the model name to send to GitLab. Priority:
//  1. The model configured on the endpoint (chosen via the UI dropdown).
//  2. The model from the incoming request, but only if it isn't an
//     `@<endpoint>/...` pinning string (those are routing hints for the
//     proxy itself and have no meaning for GitLab).
//
// The chosen value is then normalised to GitLab's `gitlab_identifier`
// snake_case format (e.g. "Claude Opus 4.7 - Anthropic" -> "claude_opus_4_7").
func (t *GitLabDuoTransformer) effectiveOutgoingModel() string {
	var raw string
	if m := strings.TrimSpace(t.model); m != "" {
		raw = m
	} else if m := strings.TrimSpace(t.originalModel); m != "" && !strings.HasPrefix(m, "@") {
		raw = m
	}
	if raw == "" {
		return ""
	}
	return toGitLabIdentifier(raw)
}

// toGitLabIdentifier converts a human-friendly GitLab Duo model label (as
// shown in the chat UI / our Fetch Models dropdown) into the snake_case
// `gitlab_identifier` GitLab's REST endpoint expects.
//
// Recognised input shapes:
//
//	"Claude Opus 4.7 - Anthropic"   -> "claude_opus_4_7"
//	"Claude Opus 4.7 - Vertex"      -> "claude_opus_4_7_vertex"
//	"Claude Opus 4.7 - Bedrock"     -> "claude_opus_4_7_bedrock"
//	"Claude Sonnet 4.6 - Anthropic" -> "claude_sonnet_4_6"
//	"Claude Haiku 4.5 - Bedrock"    -> "claude_haiku_4_5_bedrock"
//	"Gemini 3.5 Flash - Vertex"     -> "gemini_3_5_flash_vertex"
//	"GPT-5.1 - OpenAI"              -> "gpt_5_1"
//	"GPT-5-Codex - OpenAI"          -> "gpt_5_codex"
//	"GPT-5.4-Mini - OpenAI"         -> "gpt_5_4_mini"
//
// If the input already looks like a snake_case identifier (no spaces, no
// dashes other than provider-suffix forms), it's returned as-is so callers
// who paste an exact identifier still get what they asked for.
func toGitLabIdentifier(label string) string {
	s := strings.TrimSpace(label)
	if s == "" {
		return ""
	}

	// Already a snake_case-looking identifier? Return as-is.
	if !strings.ContainsAny(s, " -.") && strings.ToLower(s) == s {
		return s
	}

	// Split off the provider suffix (after the last " - "). The Anthropic
	// and OpenAI hosts get no suffix in models.yml; Vertex / Bedrock do.
	provider := ""
	base := s
	if idx := strings.LastIndex(s, " - "); idx >= 0 {
		provider = strings.TrimSpace(strings.ToLower(s[idx+3:]))
		base = strings.TrimSpace(s[:idx])
	}

	// Normalise the base name into snake_case tokens.
	//   "GPT-5.4-Mini"  -> "gpt 5 4 mini"
	//   "Claude Opus 4.7" -> "claude opus 4 7"
	//   "Gemini 3.5 Flash" -> "gemini 3 5 flash"
	repl := strings.NewReplacer("-", " ", ".", " ")
	base = strings.ToLower(repl.Replace(base))
	parts := strings.Fields(base)
	identifier := strings.Join(parts, "_")

	switch provider {
	case "vertex":
		identifier += "_vertex"
	case "bedrock":
		identifier += "_bedrock"
	case "anthropic", "openai", "":
		// Default host — no suffix in models.yml.
	default:
		// Unknown provider — append it verbatim so it's at least visible
		// in upstream logs rather than silently dropped.
		identifier += "_" + strings.ReplaceAll(provider, " ", "_")
	}
	return identifier
}

// extractLastUserText walks messages in reverse and returns the textual part
// of the most recent user message. Anthropic's content can be either a plain
// string or an array of typed blocks ({type:"text", text:"..."} etc.).
func extractLastUserText(messages []struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if text := decodeAnthropicContent(messages[i].Content); text != "" {
			return text
		}
	}
	// Fallback: just take the last message regardless of role.
	if n := len(messages); n > 0 {
		return decodeAnthropicContent(messages[n-1].Content)
	}
	return ""
}

func decodeAnthropicContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String form.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array of blocks.
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		typ, _ := b["type"].(string)
		switch typ {
		case "", "text":
			if txt, ok := b["text"].(string); ok && txt != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(txt)
			}
		case "tool_result":
			// Flatten tool_result content (string | []block).
			switch c := b["content"].(type) {
			case string:
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(c)
			case []any:
				for _, inner := range c {
					if im, ok := inner.(map[string]any); ok {
						if txt, ok := im["text"].(string); ok && txt != "" {
							if sb.Len() > 0 {
								sb.WriteString("\n")
							}
							sb.WriteString(txt)
						}
					}
				}
			}
		}
	}
	return sb.String()
}

// flattenSystem turns Anthropic's polymorphic `system` field into a plain
// string. Returns "" when there is no system prompt.
func flattenSystem(system any) string {
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

// --- Response ------------------------------------------------------------

// TransformResponse turns GitLab's response into Claude Code's expected
// shape. For non-streaming requests we emit a single Anthropic Message JSON;
// for streaming requests we emit a complete Anthropic SSE stream as raw
// bytes (the proxy's non-streaming path will hand these bytes back to the
// client; response.go up-shifts the Content-Type to text/event-stream when
// it sees an SSE-shaped body).
func (t *GitLabDuoTransformer) TransformResponse(resp []byte, isStreaming bool) ([]byte, error) {
	answer := extractGitLabAnswer(resp)
	model := t.echoModel()

	if t.stream {
		return buildAnthropicSSE(model, answer), nil
	}
	return buildAnthropicJSON(model, answer)
}

// TransformResponseWithContext delegates to TransformResponse — the GitLab
// REST upstream is non-streaming, so per-event context isn't useful here.
func (t *GitLabDuoTransformer) TransformResponseWithContext(resp []byte, isStreaming bool, ctx *transformer.StreamContext) ([]byte, error) {
	return t.TransformResponse(resp, isStreaming)
}

// echoModel returns the model name to surface in the Anthropic response.
// Prefer the override configured on the endpoint, fall back to whatever the
// client asked for, fall back to a generic placeholder.
func (t *GitLabDuoTransformer) echoModel() string {
	if strings.TrimSpace(t.model) != "" {
		return t.model
	}
	if strings.TrimSpace(t.originalModel) != "" {
		return t.originalModel
	}
	return "gitlab-duo"
}

// extractGitLabAnswer normalises the upstream body into a plain string.
//
// Per the GitLab docs the success body is a JSON-encoded string, e.g.:
//
//	"To define class in ruby..."
//
// Some deployments return an object instead (e.g. {"response": "..."} or an
// OpenAI-shaped {"choices":[{"message":{"content":"..."}}]}). We try those
// shapes too, then fall back to the raw body so error payloads still flow
// through to the client.
func extractGitLabAnswer(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}

	// Quoted-string form.
	var asString string
	if err := json.Unmarshal(body, &asString); err == nil {
		return asString
	}

	// Object form.
	var asObject map[string]any
	if err := json.Unmarshal(body, &asObject); err == nil {
		if s, ok := asObject["response"].(string); ok && s != "" {
			return s
		}
		if s, ok := asObject["content"].(string); ok && s != "" {
			return s
		}
		if s, ok := asObject["message"].(string); ok && s != "" {
			return s
		}
		if choices, ok := asObject["choices"].([]any); ok && len(choices) > 0 {
			if first, ok := choices[0].(map[string]any); ok {
				if msg, ok := first["message"].(map[string]any); ok {
					if s, ok := msg["content"].(string); ok && s != "" {
						return s
					}
				}
			}
		}
		// Surface upstream error messages verbatim if present.
		if errMsg, ok := asObject["message"].(string); ok && errMsg != "" {
			return errMsg
		}
		if errMsg, ok := asObject["error"].(string); ok && errMsg != "" {
			return errMsg
		}
	}

	return trimmed
}

// buildAnthropicJSON returns a non-streaming Anthropic Messages API response.
func buildAnthropicJSON(model, answer string) ([]byte, error) {
	resp := map[string]any{
		"id":    "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []map[string]any{
			{"type": "text", "text": answer},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": estimateTokens(answer),
		},
	}
	return json.Marshal(resp)
}

// buildAnthropicSSE returns a complete Anthropic-style SSE stream as raw
// bytes. The event sequence matches what Claude Code expects:
//
//	message_start →
//	content_block_start →
//	N × content_block_delta (text_delta) →
//	content_block_stop →
//	message_delta (with stop_reason + usage) →
//	message_stop
func buildAnthropicSSE(model, answer string) []byte {
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	var sb strings.Builder
	writeEvent := func(event string, payload map[string]any) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", event, raw)
	}

	writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
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

	// Chunk the answer so the CLI renders it progressively rather than in a
	// single shot. Chunks are sized in runes, not bytes, to keep multibyte
	// characters intact.
	for _, chunk := range splitChunks(answer, 24) {
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": chunk,
			},
		})
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
		"usage": map[string]any{
			"output_tokens": estimateTokens(answer),
		},
	})

	writeEvent("message_stop", map[string]any{
		"type": "message_stop",
	})

	return []byte(sb.String())
}

func splitChunks(s string, size int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	if size <= 0 || len(runes) <= size {
		return []string{s}
	}
	out := make([]string, 0, (len(runes)+size-1)/size)
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// estimateTokens is a coarse chars/4 heuristic used when the upstream does
// not surface token usage (GitLab's REST endpoint doesn't).
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}
