package convert

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// OneMinAIRequest represents a request to 1min.AI /api/features endpoint.
type OneMinAIRequest struct {
	Type         string                `json:"type"`
	Model        string                `json:"model"`
	PromptObject OneMinAIPromptObject  `json:"promptObject"`
}

// OneMinAIPromptObject holds prompt content and settings for 1min.AI.
type OneMinAIPromptObject struct {
	Prompt    string `json:"prompt"`
	WebSearch bool   `json:"webSearch"`
}

// OneMinAIResponse represents the top-level 1min.AI API response.
type OneMinAIResponse struct {
	AiRecord *OneMinAIAiRecord      `json:"aiRecord,omitempty"`
	Status   string                 `json:"status,omitempty"`
	Error    interface{}            `json:"error,omitempty"`
	Message  string                 `json:"message,omitempty"`
	Code     interface{}            `json:"code,omitempty"`
}

// OneMinAIAiRecord contains the AI execution result details.
type OneMinAIAiRecord struct {
	UUID           string                  `json:"uuid,omitempty"`
	Model          string                  `json:"model,omitempty"`
	Type           string                  `json:"type,omitempty"`
	Status         string                  `json:"status,omitempty"`
	AiRecordDetail *OneMinAIAiRecordDetail `json:"aiRecordDetail,omitempty"`
}

// OneMinAIAiRecordDetail holds the generated results.
type OneMinAIAiRecordDetail struct {
	ResultObject   []interface{}          `json:"resultObject,omitempty"`
	ResponseObject map[string]interface{} `json:"responseObject,omitempty"`
}

// ConversationTurn represents a single role-based turn in normalized conversation history.
type ConversationTurn struct {
	Role    string // "system", "user", "assistant", "tool"
	Content string
}

// --- Request Conversion ----------------------------------------------------

// ClaudeReqToOneMinAI converts an Anthropic /v1/messages request to 1min.AI CODE_GENERATOR request.
// Returns (transformedBody, isStreaming, requestedModel, error).
func ClaudeReqToOneMinAI(claudeReq []byte, model string) ([]byte, bool, string, error) {
	var req transformer.ClaudeRequest
	if err := json.Unmarshal(claudeReq, &req); err != nil {
		return nil, false, "", fmt.Errorf("1minai: invalid claude request: %w", err)
	}

	var turns []ConversationTurn

	// 1. System prompt
	if req.System != nil {
		if sysText := extractSystemText(req.System); strings.TrimSpace(sysText) != "" {
			turns = append(turns, ConversationTurn{
				Role:    "system",
				Content: sysText,
			})
		}
	}

	// 2. Messages
	for _, msg := range req.Messages {
		switch c := msg.Content.(type) {
		case string:
			if strings.TrimSpace(c) != "" {
				turns = append(turns, ConversationTurn{
					Role:    msg.Role,
					Content: c,
				})
			}
		case []interface{}:
			var textParts []string
			var toolResultParts []string

			for _, block := range c {
				m, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				bType, _ := m["type"].(string)
				switch bType {
				case "text":
					if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
						textParts = append(textParts, text)
					}
				case "tool_result":
					toolContent := extractToolResultContent(m["content"])
					if strings.TrimSpace(toolContent) != "" {
						toolResultParts = append(toolResultParts, toolContent)
					}
				}
			}

			if len(textParts) > 0 {
				turns = append(turns, ConversationTurn{
					Role:    msg.Role,
					Content: strings.Join(textParts, "\n"),
				})
			}
			for _, tr := range toolResultParts {
				turns = append(turns, ConversationTurn{
					Role:    "tool",
					Content: tr,
				})
			}
		}
	}

	prompt := FormatOneMinAIPrompt(turns)
	if strings.TrimSpace(prompt) == "" {
		return nil, false, "", fmt.Errorf("1minai: no prompt or message content found in request")
	}

	effectiveModel := cleanOneMinAIModelName(model, req.Model)

	outReq := OneMinAIRequest{
		Type:  "CODE_GENERATOR",
		Model: effectiveModel,
		PromptObject: OneMinAIPromptObject{
			Prompt:    prompt,
			WebSearch: false,
		},
	}

	outBytes, err := json.Marshal(outReq)
	if err != nil {
		return nil, false, "", fmt.Errorf("1minai: failed to marshal request: %w", err)
	}

	return outBytes, req.Stream, req.Model, nil
}

// OpenAIReqToOneMinAI converts an OpenAI Chat Completions request to 1min.AI CODE_GENERATOR request.
// Returns (transformedBody, isStreaming, requestedModel, error).
func OpenAIReqToOneMinAI(openaiReq []byte, model string) ([]byte, bool, string, error) {
	var req transformer.OpenAIRequest
	if err := json.Unmarshal(openaiReq, &req); err != nil {
		return nil, false, "", fmt.Errorf("1minai: invalid openai chat request: %w", err)
	}

	var turns []ConversationTurn

	for _, msg := range req.Messages {
		contentStr := extractOpenAIContent(msg.Content)
		if strings.TrimSpace(contentStr) != "" {
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			if role == "developer" {
				role = "system"
			}
			turns = append(turns, ConversationTurn{
				Role:    role,
				Content: contentStr,
			})
		}
	}

	prompt := FormatOneMinAIPrompt(turns)
	if strings.TrimSpace(prompt) == "" {
		return nil, false, "", fmt.Errorf("1minai: no prompt or message content found in request")
	}

	effectiveModel := cleanOneMinAIModelName(model, req.Model)

	outReq := OneMinAIRequest{
		Type:  "CODE_GENERATOR",
		Model: effectiveModel,
		PromptObject: OneMinAIPromptObject{
			Prompt:    prompt,
			WebSearch: false,
		},
	}

	outBytes, err := json.Marshal(outReq)
	if err != nil {
		return nil, false, "", fmt.Errorf("1minai: failed to marshal request: %w", err)
	}

	return outBytes, req.Stream, req.Model, nil
}

// OpenAI2ReqToOneMinAI converts an OpenAI Responses request to 1min.AI CODE_GENERATOR request.
// Returns (transformedBody, isStreaming, requestedModel, error).
func OpenAI2ReqToOneMinAI(openai2Req []byte, model string) ([]byte, bool, string, error) {
	var req transformer.OpenAI2Request
	if err := json.Unmarshal(openai2Req, &req); err != nil {
		return nil, false, "", fmt.Errorf("1minai: invalid responses api request: %w", err)
	}

	var turns []ConversationTurn

	// 1. System instructions
	if strings.TrimSpace(req.Instructions) != "" {
		turns = append(turns, ConversationTurn{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	// 2. Input
	switch input := req.Input.(type) {
	case string:
		if strings.TrimSpace(input) != "" {
			turns = append(turns, ConversationTurn{
				Role:    "user",
				Content: input,
			})
		}
	case []interface{}:
		for _, itemVal := range input {
			itemMap, ok := itemVal.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := itemMap["role"].(string)
			if role == "" {
				role = "user"
			}
			if role == "developer" {
				role = "system"
			}

			var textParts []string
			if contentList, ok := itemMap["content"].([]interface{}); ok {
				for _, partVal := range contentList {
					if partMap, ok := partVal.(map[string]interface{}); ok {
						if pType, ok := partMap["type"].(string); ok {
							switch pType {
							case "input_text", "output_text", "text":
								if t, ok := partMap["text"].(string); ok && strings.TrimSpace(t) != "" {
									textParts = append(textParts, t)
								}
							case "tool_result":
								if out, ok := partMap["output"].(string); ok && strings.TrimSpace(out) != "" {
									textParts = append(textParts, out)
								}
							}
						}
					}
				}
			}
			if len(textParts) > 0 {
				turns = append(turns, ConversationTurn{
					Role:    role,
					Content: strings.Join(textParts, "\n"),
				})
			}
		}
	}

	prompt := FormatOneMinAIPrompt(turns)
	if strings.TrimSpace(prompt) == "" {
		return nil, false, "", fmt.Errorf("1minai: no prompt or message content found in request")
	}

	effectiveModel := cleanOneMinAIModelName(model, req.Model)

	outReq := OneMinAIRequest{
		Type:  "CODE_GENERATOR",
		Model: effectiveModel,
		PromptObject: OneMinAIPromptObject{
			Prompt:    prompt,
			WebSearch: false,
		},
	}

	outBytes, err := json.Marshal(outReq)
	if err != nil {
		return nil, false, "", fmt.Errorf("1minai: failed to marshal request: %w", err)
	}

	return outBytes, req.Stream, req.Model, nil
}

const maxOneMinAIPromptBytes = 450000 // 450 KB safe limit (1min.AI API Gateway hard limits at 500 KB)

// FormatOneMinAIPrompt serializes conversation turns into a single deterministic prompt string.
// Single user message is preserved without wrapping tags; multi-turn / system prompts are serialized with role tags.
// Automatically prunes older middle turns if total conversation size exceeds 1min.AI API limits.
func FormatOneMinAIPrompt(turns []ConversationTurn) string {
	if len(turns) == 0 {
		return ""
	}

	if len(turns) == 1 && turns[0].Role == "user" {
		c := strings.TrimSpace(turns[0].Content)
		if len(c) > maxOneMinAIPromptBytes {
			c = c[:maxOneMinAIPromptBytes]
		}
		return c
	}

	// Calculate total raw size
	totalSize := 0
	for _, t := range turns {
		totalSize += len(t.Content)
	}

	// If total exceeds maxOneMinAIPromptBytes, prune turns
	finalTurns := turns
	if totalSize > maxOneMinAIPromptBytes && len(turns) > 2 {
		var sysTurns []ConversationTurn
		var otherTurns []ConversationTurn
		for _, t := range turns {
			if t.Role == "system" {
				sysTurns = append(sysTurns, t)
			} else {
				otherTurns = append(otherTurns, t)
			}
		}

		sysBytes := 0
		for _, st := range sysTurns {
			sysBytes += len(st.Content)
		}
		// Budget for other turns
		budget := maxOneMinAIPromptBytes - sysBytes
		if budget < 20000 {
			budget = 20000
		}

		// Take turns from newest to oldest
		var keptOthers []ConversationTurn
		accum := 0
		for i := len(otherTurns) - 1; i >= 0; i-- {
			turnLen := len(otherTurns[i].Content)
			if accum+turnLen > budget && len(keptOthers) > 0 {
				break
			}
			keptOthers = append([]ConversationTurn{otherTurns[i]}, keptOthers...)
			accum += turnLen
		}

		finalTurns = make([]ConversationTurn, 0, len(sysTurns)+len(keptOthers)+1)
		finalTurns = append(finalTurns, sysTurns...)
		finalTurns = append(finalTurns, ConversationTurn{
			Role:    "system",
			Content: "[NOTE: Earlier context turns were compacted to fit 1min.AI API payload limits]",
		})
		finalTurns = append(finalTurns, keptOthers...)
	}

	var sb strings.Builder
	for _, turn := range finalTurns {
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}

		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}

		roleTag := strings.ToUpper(turn.Role)
		switch turn.Role {
		case "tool":
			roleTag = "TOOL RESULT"
		case "assistant":
			roleTag = "ASSISTANT"
		case "system":
			roleTag = "SYSTEM"
		case "user":
			roleTag = "USER"
		}

		sb.WriteString("[" + roleTag + "]\n" + content)
	}

	res := sb.String()
	if len(res) > maxOneMinAIPromptBytes+5000 {
		res = res[:maxOneMinAIPromptBytes+5000]
	}
	return res
}

func extractOpenAIContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, partVal := range c {
			if partMap, ok := partVal.(map[string]interface{}); ok {
				if text, ok := partMap["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// --- Response Extraction ---------------------------------------------------

// ExtractOneMinAIAnswer extracts the generated text response from 1min.AI JSON body.
func ExtractOneMinAIAnswer(body []byte) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", fmt.Errorf("1min.ai returned empty response body")
	}

	var resp OneMinAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// If not JSON or malformed
		return "", fmt.Errorf("1min.ai: malformed json response: %w", err)
	}

	// 1. Check for top-level error / failure messages
	if resp.Error != nil {
		switch e := resp.Error.(type) {
		case string:
			if strings.TrimSpace(e) != "" {
				return "", fmt.Errorf("1min.ai error: %s", e)
			}
		case map[string]interface{}:
			if msg, ok := e["message"].(string); ok && msg != "" {
				return "", fmt.Errorf("1min.ai error: %s", msg)
			}
		}
	}
	if resp.Status != "" && !strings.EqualFold(resp.Status, "SUCCESS") && !strings.EqualFold(resp.Status, "OK") {
		if resp.Message != "" {
			return "", fmt.Errorf("1min.ai returned status %s: %s", resp.Status, resp.Message)
		}
		return "", fmt.Errorf("1min.ai returned status %s", resp.Status)
	}

	// 2. Check aiRecord
	if resp.AiRecord == nil {
		if resp.Message != "" {
			return "", fmt.Errorf("1min.ai error: %s", resp.Message)
		}
		return "", fmt.Errorf("1min.ai: missing aiRecord in response")
	}

	if resp.AiRecord.Status != "" && !strings.EqualFold(resp.AiRecord.Status, "SUCCESS") && !strings.EqualFold(resp.AiRecord.Status, "OK") {
		return "", fmt.Errorf("1min.ai record status: %s", resp.AiRecord.Status)
	}

	if resp.AiRecord.AiRecordDetail == nil {
		return "", fmt.Errorf("1min.ai: missing aiRecordDetail in response")
	}

	if len(resp.AiRecord.AiRecordDetail.ResultObject) == 0 {
		return "", fmt.Errorf("1min.ai: empty resultObject in response")
	}

	// 3. Extract and join result strings
	var results []string
	for _, item := range resp.AiRecord.AiRecordDetail.ResultObject {
		switch val := item.(type) {
		case string:
			if val != "" {
				results = append(results, val)
			}
		case map[string]interface{}:
			if text, ok := val["text"].(string); ok && text != "" {
				results = append(results, text)
			} else if content, ok := val["content"].(string); ok && content != "" {
				results = append(results, content)
			} else {
				if raw, err := json.Marshal(val); err == nil {
					results = append(results, string(raw))
				}
			}
		default:
			str := fmt.Sprint(val)
			if str != "" && str != "<nil>" {
				results = append(results, str)
			}
		}
	}

	if len(results) == 0 {
		return "", fmt.Errorf("1min.ai: resultObject contained no text results")
	}

	return strings.Join(results, "\n"), nil
}

// --- Response Transformation: Claude ---------------------------------------

// OneMinAIRespToClaude converts 1min.AI response to Anthropic /v1/messages JSON response.
func OneMinAIRespToClaude(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	out := map[string]interface{}{
		"id":    msgID,
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": answer,
			},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": estimateTokensHeuristic(answer),
		},
	}

	return json.Marshal(out)
}

// OneMinAIRespToClaudeSSE converts 1min.AI response to synthesized Anthropic SSE stream.
func OneMinAIRespToClaudeSSE(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var sb strings.Builder

	writeEvent := func(event string, payload map[string]interface{}) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", event, raw)
	}

	writeEvent("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})

	writeEvent("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	})

	for _, chunk := range splitChunksRunes(answer, 24) {
		writeEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]interface{}{
				"type": "text_delta",
				"text": chunk,
			},
		})
	}

	writeEvent("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": 0,
	})

	writeEvent("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": estimateTokensHeuristic(answer),
		},
	})

	writeEvent("message_stop", map[string]interface{}{
		"type": "message_stop",
	})

	return []byte(sb.String()), nil
}

// --- Response Transformation: OpenAI Chat Completions -----------------------

// OneMinAIRespToOpenAI converts 1min.AI response to OpenAI /v1/chat/completions JSON response.
func OneMinAIRespToOpenAI(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	outTokens := estimateTokensHeuristic(answer)
	respID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	out := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": answer,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": outTokens,
			"total_tokens":      outTokens,
		},
	}

	return json.Marshal(out)
}

// OneMinAIRespToOpenAISSE converts 1min.AI response to synthesized OpenAI Chat completions SSE stream.
func OneMinAIRespToOpenAISSE(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	respID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	created := time.Now().Unix()
	var sb strings.Builder

	// Chunk 1: Role initialization
	chunk1 := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"role":    "assistant",
					"content": "",
				},
				"finish_reason": nil,
			},
		},
	}
	d1, _ := json.Marshal(chunk1)
	fmt.Fprintf(&sb, "data: %s\n\n", d1)

	// Chunk 2..N: Content deltas
	for _, chunk := range splitChunksRunes(answer, 24) {
		chunkEvent := map[string]interface{}{
			"id":      respID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"content": chunk,
					},
					"finish_reason": nil,
				},
			},
		}
		dc, _ := json.Marshal(chunkEvent)
		fmt.Fprintf(&sb, "data: %s\n\n", dc)
	}

	// Final Chunk: finish_reason & usage
	outTokens := estimateTokensHeuristic(answer)
	finalChunk := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": outTokens,
			"total_tokens":      outTokens,
		},
	}
	df, _ := json.Marshal(finalChunk)
	fmt.Fprintf(&sb, "data: %s\n\n", df)
	sb.WriteString("data: [DONE]\n\n")

	return []byte(sb.String()), nil
}

// --- Response Transformation: OpenAI Responses (/v1/responses) ---------------

// OneMinAIRespToOpenAI2 converts 1min.AI response to OpenAI Responses JSON response.
func OneMinAIRespToOpenAI2(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	outTokens := estimateTokensHeuristic(answer)
	respID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	out := map[string]interface{}{
		"id":     respID,
		"object": "response",
		"status": "completed",
		"model":  model,
		"output": []map[string]interface{}{
			{
				"type": "message",
				"id":   msgID,
				"role": "assistant",
				"content": []map[string]interface{}{
					{
						"type": "output_text",
						"text": answer,
					},
				},
			},
		},
		"usage": map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": outTokens,
			"total_tokens":  outTokens,
		},
	}

	return json.Marshal(out)
}

// OneMinAIRespToOpenAI2SSE converts 1min.AI response to synthesized OpenAI Responses SSE stream.
func OneMinAIRespToOpenAI2SSE(respBytes []byte, model string) ([]byte, error) {
	answer, err := ExtractOneMinAIAnswer(respBytes)
	if err != nil {
		return nil, err
	}

	outTokens := estimateTokensHeuristic(answer)
	respID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	var sb strings.Builder
	writeEvent := func(payload map[string]interface{}) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(&sb, "data: %s\n\n", raw)
	}

	// 1. response.created
	writeEvent(map[string]interface{}{
		"type": "response.created",
		"response": map[string]interface{}{
			"id":     respID,
			"object": "response",
			"status": "in_progress",
			"model":  model,
		},
	})

	// 2. response.output_item.added
	writeEvent(map[string]interface{}{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]interface{}{
			"type":    "message",
			"id":      msgID,
			"role":    "assistant",
			"status":  "in_progress",
			"content": []interface{}{},
		},
	})

	// 3. response.content_part.added
	writeEvent(map[string]interface{}{
		"type":          "response.content_part.added",
		"output_index":  0,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": "",
		},
	})

	// 4. response.output_text.delta
	for _, chunk := range splitChunksRunes(answer, 24) {
		writeEvent(map[string]interface{}{
			"type":          "response.output_text.delta",
			"output_index":  0,
			"content_index": 0,
			"delta":         chunk,
		})
	}

	// 5. response.output_text.done
	writeEvent(map[string]interface{}{
		"type":          "response.output_text.done",
		"output_index":  0,
		"content_index": 0,
	})

	// 6. response.content_part.done
	writeEvent(map[string]interface{}{
		"type":          "response.content_part.done",
		"output_index":  0,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
		},
	})

	// 7. response.output_item.done
	writeEvent(map[string]interface{}{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]interface{}{
			"type":   "message",
			"id":     msgID,
			"role":   "assistant",
			"status": "completed",
		},
	})

	// 8. response.completed
	writeEvent(map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":     respID,
			"object": "response",
			"status": "completed",
			"model":  model,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": outTokens,
				"total_tokens":  outTokens,
			},
		},
	})

	sb.WriteString("data: [DONE]\n\n")

	return []byte(sb.String()), nil
}

// --- Helpers ---------------------------------------------------------------

func splitChunksRunes(s string, size int) []string {
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

func estimateTokensHeuristic(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}

// Supported 1min.AI CODE_GENERATOR models per official documentation:
var knownOneMinAIModels = map[string]bool{
	// Anthropic Models
	"claude-sonnet-5":            true,
	"claude-sonnet-4-6":          true,
	"claude-sonnet-4-5-20250929": true,
	"claude-opus-4-8":            true,
	"claude-opus-4-7":            true,
	"claude-opus-4-6":            true,
	"claude-opus-4-5-20251101":   true,
	"claude-haiku-4-5-20251001":  true,
	"claude-fable-5":             true,
	// OpenAI Models
	"gpt-5.3-codex": true,
	"gpt-5.6-terra": true,
	"gpt-5.6-sol":   true,
	"gpt-5.6-luna":  true,
	"gpt-5.5-pro":   true,
	"gpt-5.5":       true,
	"gpt-5.4-nano":  true,
	"gpt-5.4-mini":  true,
	"gpt-5.4":       true,
	"gpt-5":         true,
	"gpt-4o":        true,
	"o3":            true,
	// DeepSeek Models
	"deepseek-v4-pro":   true,
	"deepseek-v4-flash": true,
	"deepseek-reasoner": true,
	"deepseek-chat":     true,
	// GoogleAI Models
	"gemini-3.5-flash":              true,
	"gemini-3.1-pro-preview":        true,
	"gemini-3.1-flash-lite-preview": true,
	"gemini-3-flash-preview":        true,
	// Alibaba Models
	"qwen3.7-plus":        true,
	"qwen3.7-max":         true,
	"qwen3.7-flash":       true,
	"qwen3.6-plus":        true,
	"qwen3.6-max-preview": true,
	"qwen3.6-flash":       true,
	"qwen3-coder-plus":    true,
	"qwen3-coder-flash":   true,
	// xAI Models
	"grok-code-fast-1": true,
	"grok-4.5":         true,
	"grok-4.3":         true,
	// zai Models
	"glm-5.2": true,
	"glm-5.1": true,
	"glm-5":   true,
}

func cleanOneMinAIModelName(model string, fallback string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		m = strings.TrimSpace(fallback)
	}
	if strings.HasPrefix(m, "@") {
		m = strings.TrimPrefix(m, "@")
	}
	if idx := strings.LastIndex(m, "/"); idx != -1 {
		m = strings.TrimSpace(m[idx+1:])
	}
	if m == "" {
		return "claude-sonnet-5"
	}

	// Exact match check against official models
	if knownOneMinAIModels[m] {
		return m
	}

	lower := strings.ToLower(m)

	// Anthropic aliases
	if strings.Contains(lower, "sonnet-5") || strings.Contains(lower, "3-7") || strings.Contains(lower, "3.7") || strings.Contains(lower, "claude-5") {
		return "claude-sonnet-5"
	}
	if strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "4.6") {
		return "claude-sonnet-4-6"
	}
	if strings.Contains(lower, "sonnet-4-5") || strings.Contains(lower, "4.5") || strings.Contains(lower, "3-5-sonnet") || strings.Contains(lower, "3.5-sonnet") {
		return "claude-sonnet-4-5-20250929"
	}
	if strings.Contains(lower, "opus-4-8") || strings.Contains(lower, "4.8") {
		return "claude-opus-4-8"
	}
	if strings.Contains(lower, "opus-4-7") || strings.Contains(lower, "4.7") {
		return "claude-opus-4-7"
	}
	if strings.Contains(lower, "opus") {
		return "claude-opus-4-8"
	}
	if strings.Contains(lower, "haiku") {
		return "claude-haiku-4-5-20251001"
	}
	if strings.Contains(lower, "fable") {
		return "claude-fable-5"
	}
	if strings.Contains(lower, "sonnet") || strings.Contains(lower, "claude") {
		return "claude-sonnet-5"
	}

	// OpenAI aliases
	if strings.Contains(lower, "gpt-5.6-terra") || strings.Contains(lower, "terra") {
		return "gpt-5.6-terra"
	}
	if strings.Contains(lower, "gpt-5.6-luna") || strings.Contains(lower, "luna") {
		return "gpt-5.6-luna"
	}
	if strings.Contains(lower, "gpt-5.6") || strings.Contains(lower, "sol") {
		return "gpt-5.6-sol"
	}
	if strings.Contains(lower, "gpt-5.5-pro") {
		return "gpt-5.5-pro"
	}
	if strings.Contains(lower, "gpt-5.5") {
		return "gpt-5.5"
	}
	if strings.Contains(lower, "gpt-5.4-nano") {
		return "gpt-5.4-nano"
	}
	if strings.Contains(lower, "gpt-5.4-mini") || strings.Contains(lower, "mini") {
		return "gpt-5.4-mini"
	}
	if strings.Contains(lower, "gpt-5.4") {
		return "gpt-5.4"
	}
	if strings.Contains(lower, "gpt-5.3") || strings.Contains(lower, "codex") {
		return "gpt-5.3-codex"
	}
	if strings.Contains(lower, "gpt-5") {
		return "gpt-5"
	}
	if strings.Contains(lower, "gpt-4o") || strings.Contains(lower, "gpt-4") {
		return "gpt-4o"
	}
	if strings.Contains(lower, "o3") {
		return "o3"
	}

	// DeepSeek aliases
	if strings.Contains(lower, "reasoner") || strings.Contains(lower, "r1") {
		return "deepseek-reasoner"
	}
	if strings.Contains(lower, "deepseek-v4-flash") {
		return "deepseek-v4-flash"
	}
	if strings.Contains(lower, "deepseek-v4") || strings.Contains(lower, "coder") {
		return "deepseek-v4-pro"
	}
	if strings.Contains(lower, "deepseek") {
		return "deepseek-chat"
	}

	// GoogleAI aliases
	if strings.Contains(lower, "gemini-3.1-pro") {
		return "gemini-3.1-pro-preview"
	}
	if strings.Contains(lower, "gemini-3.1-flash") {
		return "gemini-3.1-flash-lite-preview"
	}
	if strings.Contains(lower, "gemini-3-flash") {
		return "gemini-3-flash-preview"
	}
	if strings.Contains(lower, "gemini") {
		return "gemini-3.5-flash"
	}

	// Alibaba aliases
	if strings.Contains(lower, "qwen3-coder") || strings.Contains(lower, "qwen-coder") {
		return "qwen3-coder-plus"
	}
	if strings.Contains(lower, "qwen") {
		return "qwen3.7-plus"
	}

	// xAI aliases
	if strings.Contains(lower, "grok-code") {
		return "grok-code-fast-1"
	}
	if strings.Contains(lower, "grok-4.5") {
		return "grok-4.5"
	}
	if strings.Contains(lower, "grok") {
		return "grok-code-fast-1"
	}

	// zai aliases
	if strings.Contains(lower, "glm-5.1") {
		return "glm-5.1"
	}
	if strings.Contains(lower, "glm-5") {
		return "glm-5"
	}
	if strings.Contains(lower, "glm") {
		return "glm-5.2"
	}

	return "claude-sonnet-5"
}
