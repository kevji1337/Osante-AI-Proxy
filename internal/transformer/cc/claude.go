package cc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// ClaudeTransformer is a passthrough transformer for Claude Code → Claude endpoint
// with input_tokens fallback for message_delta events
type ClaudeTransformer struct {
	model string
}

// NewClaudeTransformer creates a new passthrough transformer
func NewClaudeTransformer() *ClaudeTransformer {
	return &ClaudeTransformer{}
}

// NewClaudeTransformerWithModel creates a transformer with model override
func NewClaudeTransformerWithModel(model string) *ClaudeTransformer {
	return &ClaudeTransformer{model: model}
}

func (t *ClaudeTransformer) Name() string {
	return "cc_claude"
}

func (t *ClaudeTransformer) TransformRequest(req []byte) ([]byte, error) {
	if t.model == "" {
		return req, nil
	}

	// Claude-to-Claude needs no conversion; the only job here is stamping the
	// endpoint's model override onto the body. Anything that is not a JSON object
	// (health probes, count_tokens oddities, empty bodies) has no model field to
	// stamp, so pass it through untouched rather than failing the request. Probing
	// the shape first keeps that decision explicit instead of hiding it behind a
	// discarded Unmarshal error.
	if !isJSONObject(req) {
		return req, nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(req, &data); err != nil {
		return nil, fmt.Errorf("claude request body is not valid JSON: %w", err)
	}

	data["model"] = t.model
	return json.Marshal(data)
}

// isJSONObject reports whether body looks like a JSON object, i.e. whether it can
// carry a "model" field at all.
func isJSONObject(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func (t *ClaudeTransformer) TransformResponse(resp []byte, isStreaming bool) ([]byte, error) {
	return resp, nil
}

func (t *ClaudeTransformer) TransformResponseWithContext(resp []byte, isStreaming bool, ctx *transformer.StreamContext) ([]byte, error) {
	if ctx == nil {
		return resp, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(resp))
	var result bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data:") {
			jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(jsonData), &event); err == nil {
				eventType, _ := event["type"].(string)

				if eventType == "message_start" {
					// Cache input_tokens from message_start (only if > 0, keep estimate otherwise)
					if msg, ok := event["message"].(map[string]interface{}); ok {
						if usage, ok := msg["usage"].(map[string]interface{}); ok {
							if input, ok := usage["input_tokens"].(float64); ok && int(input) > 0 {
								ctx.InputTokens = int(input)
							}
						}
					}
				} else if eventType == "message_delta" {
					// Fallback: fill input_tokens if 0
					if usage, ok := event["usage"].(map[string]interface{}); ok {
						modified := false

						if input, ok := usage["input_tokens"].(float64); ok && int(input) == 0 && ctx.InputTokens > 0 {
							usage["input_tokens"] = ctx.InputTokens
							modified = true
						}

						// Fallback: fill output_tokens if 0
						if output, ok := usage["output_tokens"].(float64); ok && int(output) == 0 && ctx.OutputTokens > 0 {
							usage["output_tokens"] = ctx.OutputTokens
							modified = true
						}

						if modified {
							modifiedData, _ := json.Marshal(event)
							result.WriteString("data: ")
							result.Write(modifiedData)
							result.WriteString("\n")
							continue
						}
					}
				}
			}
		}

		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.Bytes(), nil
}
