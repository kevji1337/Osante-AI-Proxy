package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// payloadRewrite describes the mutations to apply to an outgoing request body.
//
// The three rewrites used to be independent functions, each doing its own
// Unmarshal -> map[string]interface{} -> Marshal cycle, and up to four of them ran
// per attempt. Beyond the wasted work, that shape had two real defects: encoding
// numbers into interface{} turns them into float64, so integers above 2^53 came out
// changed, and re-marshaling a map loses the original key order, which makes a
// logged body hard to compare against what the client actually sent.
type payloadRewrite struct {
	// Model, when non-empty, replaces the top-level "model" field.
	Model string
	// CleanToolCalls drops trailing assistant tool_use blocks that have no input.
	// Claude Code emits those when a tool call is interrupted, and upstreams reject
	// the request outright.
	CleanToolCalls bool
	// CodexResponses adds the fields chatgpt.com/backend-api/codex requires on
	// /v1/responses.
	CodexResponses bool
}

func (r payloadRewrite) isNoop() bool {
	return strings.TrimSpace(r.Model) == "" && !r.CleanToolCalls && !r.CodexResponses
}

// rewriteRequestPayload applies every requested mutation in a single decode/encode
// pass and returns the new body. The original slice is returned unchanged when
// there is nothing to do, when the body is not a JSON object, or when re-encoding
// fails — a rewrite is best-effort and must never lose the request.
//
// The returned error is informational: callers that care log it, callers that do
// not can ignore it and use the returned body.
func rewriteRequestPayload(body []byte, rw payloadRewrite) ([]byte, error) {
	if rw.isNoop() {
		return body, nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Arrays and non-JSON bodies (health probes, already-encoded SSE) have no
		// top-level fields to rewrite.
		return body, nil
	}

	// UseNumber keeps integers exact: without it every number becomes a float64 and
	// values above 2^53 are silently rounded on the way out.
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var payload map[string]interface{}
	if err := dec.Decode(&payload); err != nil {
		return body, fmt.Errorf("request body is not a JSON object: %w", err)
	}

	changed := false
	if model := strings.TrimSpace(rw.Model); model != "" {
		payload["model"] = model
		changed = true
	}
	if rw.CleanToolCalls && dropIncompleteToolCalls(payload) {
		changed = true
	}
	if rw.CodexResponses {
		payload["store"] = false
		payload["stream"] = true
		if _, ok := payload["instructions"]; !ok {
			payload["instructions"] = ""
		}
		changed = true
	}

	if !changed {
		return body, nil
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, fmt.Errorf("failed to re-encode the request body: %w", err)
	}
	return updated, nil
}

// dropIncompleteToolCalls removes tool_use blocks with no input from the trailing
// assistant message, reporting whether anything changed.
//
// Only the trailing run of assistant messages is inspected: an incomplete tool
// call can only be the one that was interrupted, and earlier turns are history the
// upstream already accepted.
func dropIncompleteToolCalls(payload map[string]interface{}) bool {
	messages, ok := payload["messages"].([]interface{})
	if !ok {
		return false
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			return false
		}
		content, ok := msg["content"].([]interface{})
		if !ok {
			return false
		}

		kept := make([]interface{}, 0, len(content))
		dropped := false
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				kept = append(kept, block)
				continue
			}
			if blockType, _ := blockMap["type"].(string); blockType == "tool_use" {
				if input, hasInput := blockMap["input"]; !hasInput || input == nil {
					dropped = true
					continue
				}
			}
			kept = append(kept, block)
		}

		if !dropped {
			return false
		}
		if len(kept) == 0 {
			payload["messages"] = append(messages[:i], messages[i+1:]...)
		} else {
			msg["content"] = kept
			payload["messages"] = messages
		}
		return true
	}
	return false
}
