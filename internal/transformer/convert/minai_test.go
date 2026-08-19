package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

func TestClaudeReqToOneMinAI(t *testing.T) {
	claudeReq := []byte(`{
		"model": "claude-3-7-sonnet-20250219",
		"system": "You are a specialized code assistant.",
		"messages": [
			{"role": "user", "content": "Hello, write a binary search algorithm in Go"},
			{"role": "assistant", "content": "func BinarySearch(arr []int, target int) int { return -1 }"},
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "Now update it with the actual search logic"},
					{
						"type": "tool_result",
						"tool_use_id": "call_123",
						"content": "linter: no errors"
					}
				]
			}
		],
		"stream": true,
		"max_tokens": 1024,
		"tools": [
			{
				"name": "bash",
				"description": "Run shell command",
				"input_schema": {"type": "object"}
			}
		]
	}`)

	transformed, isStream, origModel, err := ClaudeReqToOneMinAI(claudeReq, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("ClaudeReqToOneMinAI failed: %v", err)
	}

	if !isStream {
		t.Errorf("expected isStream=true, got false")
	}
	if origModel != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected origModel=claude-3-7-sonnet-20250219, got %s", origModel)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(transformed, &rawMap); err != nil {
		t.Fatalf("failed to unmarshal transformed JSON: %v", err)
	}

	if rawMap["type"] != "CODE_GENERATOR" {
		t.Errorf("expected type=CODE_GENERATOR, got %v", rawMap["type"])
	}
	if rawMap["model"] != "gpt-5.6-sol" {
		t.Errorf("expected model=gpt-5.6-sol, got %v", rawMap["model"])
	}
	if _, hasConvID := rawMap["conversationId"]; hasConvID {
		t.Errorf("conversationId must not be present in transformed request")
	}
	if _, hasTools := rawMap["tools"]; hasTools {
		t.Errorf("tools field must not be present in transformed request")
	}

	promptObj, ok := rawMap["promptObject"].(map[string]interface{})
	if !ok {
		t.Fatalf("promptObject missing or not map: %v", rawMap["promptObject"])
	}

	if ws, ok := promptObj["webSearch"].(bool); !ok || ws {
		t.Errorf("expected webSearch=false, got %v", promptObj["webSearch"])
	}

	prompt, ok := promptObj["prompt"].(string)
	if !ok || prompt == "" {
		t.Fatalf("prompt missing or empty: %v", promptObj["prompt"])
	}

	if !strings.Contains(prompt, "[SYSTEM]") || !strings.Contains(prompt, "You are a specialized code assistant.") {
		t.Errorf("prompt missing system instruction: %s", prompt)
	}
	if !strings.Contains(prompt, "[USER]") || !strings.Contains(prompt, "Hello, write a binary search algorithm in Go") {
		t.Errorf("prompt missing first user message: %s", prompt)
	}
	if !strings.Contains(prompt, "[ASSISTANT]") || !strings.Contains(prompt, "func BinarySearch") {
		t.Errorf("prompt missing assistant message: %s", prompt)
	}
	if !strings.Contains(prompt, "Now update it with the actual search logic") {
		t.Errorf("prompt missing second user message: %s", prompt)
	}
	if !strings.Contains(prompt, "[TOOL RESULT]") || !strings.Contains(prompt, "linter: no errors") {
		t.Errorf("prompt missing tool result: %s", prompt)
	}
}

func TestOpenAIReqToOneMinAI(t *testing.T) {
	openaiReq := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "You are an expert coder"},
			{"role": "user", "content": "Write quicksort in python"},
			{"role": "assistant", "content": "def quicksort(arr): pass"},
			{"role": "tool", "content": "test output: pass", "tool_call_id": "call_abc"},
			{"role": "user", "content": "Looks good"}
		],
		"stream": false
	}`)

	transformed, isStream, origModel, err := OpenAIReqToOneMinAI(openaiReq, "")
	if err != nil {
		t.Fatalf("OpenAIReqToOneMinAI failed: %v", err)
	}

	if isStream {
		t.Errorf("expected isStream=false, got true")
	}
	if origModel != "gpt-4o" {
		t.Errorf("expected origModel=gpt-4o, got %s", origModel)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(transformed, &rawMap); err != nil {
		t.Fatalf("failed to unmarshal transformed JSON: %v", err)
	}

	if rawMap["type"] != "CODE_GENERATOR" {
		t.Errorf("expected type=CODE_GENERATOR, got %v", rawMap["type"])
	}
	if rawMap["model"] != "gpt-4o" {
		t.Errorf("expected fallback model=gpt-4o, got %v", rawMap["model"])
	}
	if _, hasConvID := rawMap["conversationId"]; hasConvID {
		t.Errorf("conversationId must not be present in transformed request")
	}

	promptObj := rawMap["promptObject"].(map[string]interface{})
	prompt := promptObj["prompt"].(string)

	if !strings.Contains(prompt, "[SYSTEM]\nYou are an expert coder") {
		t.Errorf("missing system turn in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "[USER]\nWrite quicksort in python") {
		t.Errorf("missing user turn in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "[ASSISTANT]\ndef quicksort(arr): pass") {
		t.Errorf("missing assistant turn in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "[TOOL RESULT]\ntest output: pass") {
		t.Errorf("missing tool result in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "[USER]\nLooks good") {
		t.Errorf("missing final user turn in prompt: %s", prompt)
	}
}

func TestOpenAI2ReqToOneMinAI(t *testing.T) {
	openai2Req := []byte(`{
		"model": "gpt-5-codex",
		"instructions": "Be concise.",
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [
					{"type": "input_text", "text": "What is the capital of France?"}
				]
			}
		],
		"stream": true
	}`)

	transformed, isStream, origModel, err := OpenAI2ReqToOneMinAI(openai2Req, "claude-sonnet-5")
	if err != nil {
		t.Fatalf("OpenAI2ReqToOneMinAI failed: %v", err)
	}

	if !isStream {
		t.Errorf("expected isStream=true, got false")
	}
	if origModel != "gpt-5-codex" {
		t.Errorf("expected origModel=gpt-5-codex, got %s", origModel)
	}

	var req OneMinAIRequest
	if err := json.Unmarshal(transformed, &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if req.Type != "CODE_GENERATOR" {
		t.Errorf("expected type=CODE_GENERATOR, got %s", req.Type)
	}
	if req.Model != "claude-sonnet-5" {
		t.Errorf("expected model=claude-sonnet-5, got %s", req.Model)
	}
	if req.PromptObject.WebSearch {
		t.Errorf("expected webSearch=false, got true")
	}

	if !strings.Contains(req.PromptObject.Prompt, "[SYSTEM]\nBe concise.") {
		t.Errorf("missing system instructions: %s", req.PromptObject.Prompt)
	}
	if !strings.Contains(req.PromptObject.Prompt, "[USER]\nWhat is the capital of France?") {
		t.Errorf("missing user question: %s", req.PromptObject.Prompt)
	}
}

func TestOneMinAIRespToClaude(t *testing.T) {
	minResp := []byte(`{
		"aiRecord": {
			"uuid": "29a3d3f4-ef1e-4a16-abd9-7353c8e67339",
			"model": "gpt-5.6-sol",
			"type": "CODE_GENERATOR",
			"status": "SUCCESS",
			"aiRecordDetail": {
				"resultObject": [
					"Hello from 1min.AI! Here is your code."
				],
				"responseObject": {}
			}
		}
	}`)

	// Test Non-streaming Claude Response
	claudeJSON, err := OneMinAIRespToClaude(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToClaude failed: %v", err)
	}

	var resp transformer.ClaudeResponse
	if err := json.Unmarshal(claudeJSON, &resp); err != nil {
		t.Fatalf("failed to unmarshal Claude response: %v", err)
	}

	if resp.Type != "message" || resp.Role != "assistant" {
		t.Errorf("unexpected response type or role: %s / %s", resp.Type, resp.Role)
	}
	if resp.Model != "gpt-5.6-sol" {
		t.Errorf("expected model gpt-5.6-sol, got %s", resp.Model)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected stop_reason end_turn, got %s", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	block := resp.Content[0].(map[string]interface{})
	if block["text"] != "Hello from 1min.AI! Here is your code." {
		t.Errorf("unexpected text content: %v", block["text"])
	}

	// Test Streaming Claude Response
	claudeSSE, err := OneMinAIRespToClaudeSSE(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToClaudeSSE failed: %v", err)
	}
	sseStr := string(claudeSSE)
	if !strings.Contains(sseStr, "event: message_start") ||
		!strings.Contains(sseStr, "event: content_block_start") ||
		!strings.Contains(sseStr, "event: content_block_delta") ||
		!strings.Contains(sseStr, "event: content_block_stop") ||
		!strings.Contains(sseStr, "event: message_delta") ||
		!strings.Contains(sseStr, "event: message_stop") {
		t.Errorf("Claude SSE stream missing required events: %s", sseStr)
	}
	if !strings.Contains(sseStr, "Hello from 1min.AI!") {
		t.Errorf("Claude SSE stream missing text: %s", sseStr)
	}
}

func TestOneMinAIRespToOpenAI(t *testing.T) {
	minResp := []byte(`{
		"aiRecord": {
			"uuid": "29a3d3f4-ef1e-4a16-abd9-7353c8e67339",
			"model": "gpt-5.6-sol",
			"type": "CODE_GENERATOR",
			"status": "SUCCESS",
			"aiRecordDetail": {
				"resultObject": [
					"Part 1 text",
					"Part 2 text"
				],
				"responseObject": {}
			}
		}
	}`)

	// Test Non-streaming OpenAI Chat Completion
	openaiJSON, err := OneMinAIRespToOpenAI(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToOpenAI failed: %v", err)
	}

	var resp transformer.OpenAIResponse
	if err := json.Unmarshal(openaiJSON, &resp); err != nil {
		t.Fatalf("failed to unmarshal OpenAI response: %v", err)
	}

	if resp.Object != "chat.completion" {
		t.Errorf("expected object chat.completion, got %s", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %s", resp.Choices[0].FinishReason)
	}
	expectedContent := "Part 1 text\nPart 2 text"
	if resp.Choices[0].Message.Content != expectedContent {
		t.Errorf("expected content %q, got %q", expectedContent, resp.Choices[0].Message.Content)
	}

	// Test Streaming OpenAI Chat SSE
	openaiSSE, err := OneMinAIRespToOpenAISSE(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToOpenAISSE failed: %v", err)
	}
	sseStr := string(openaiSSE)
	if !strings.Contains(sseStr, "chat.completion.chunk") {
		t.Errorf("expected chat.completion.chunk in SSE: %s", sseStr)
	}
	if !strings.Contains(sseStr, "data: [DONE]") {
		t.Errorf("expected [DONE] at end of SSE: %s", sseStr)
	}
	if !strings.Contains(sseStr, "Part 1 text") {
		t.Errorf("expected text chunks in SSE: %s", sseStr)
	}
}

func TestOneMinAIRespToOpenAI2(t *testing.T) {
	minResp := []byte(`{
		"aiRecord": {
			"uuid": "29a3d3f4-ef1e-4a16-abd9-7353c8e67339",
			"model": "gpt-5.6-sol",
			"type": "CODE_GENERATOR",
			"status": "SUCCESS",
			"aiRecordDetail": {
				"resultObject": [
					"Responses API answer"
				],
				"responseObject": {}
			}
		}
	}`)

	// Test Non-streaming OpenAI Responses API
	openai2JSON, err := OneMinAIRespToOpenAI2(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToOpenAI2 failed: %v", err)
	}

	var resp transformer.OpenAI2Response
	if err := json.Unmarshal(openai2JSON, &resp); err != nil {
		t.Fatalf("failed to unmarshal OpenAI2 response: %v", err)
	}

	if resp.Object != "response" || resp.Status != "completed" {
		t.Errorf("unexpected object or status: %s / %s", resp.Object, resp.Status)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" || resp.Output[0].Role != "assistant" {
		t.Errorf("unexpected output item type or role: %s / %s", resp.Output[0].Type, resp.Output[0].Role)
	}
	if len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Text != "Responses API answer" {
		t.Errorf("unexpected content in output: %#v", resp.Output[0].Content)
	}

	// Test Streaming OpenAI Responses SSE
	openai2SSE, err := OneMinAIRespToOpenAI2SSE(minResp, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("OneMinAIRespToOpenAI2SSE failed: %v", err)
	}
	sseStr := string(openai2SSE)
	if !strings.Contains(sseStr, "response.created") ||
		!strings.Contains(sseStr, "response.output_item.added") ||
		!strings.Contains(sseStr, "response.content_part.added") ||
		!strings.Contains(sseStr, "response.output_text.delta") ||
		!strings.Contains(sseStr, "response.output_text.done") ||
		!strings.Contains(sseStr, "response.content_part.done") ||
		!strings.Contains(sseStr, "response.output_item.done") ||
		!strings.Contains(sseStr, "response.completed") ||
		!strings.Contains(sseStr, "data: [DONE]") {
		t.Errorf("Responses SSE stream missing required events: %s", sseStr)
	}
}

func TestExtractOneMinAIAnswerEdgeCases(t *testing.T) {
	// Case 1: Empty response
	if _, err := ExtractOneMinAIAnswer([]byte("")); err == nil {
		t.Error("expected error on empty response")
	}

	// Case 2: Malformed JSON
	if _, err := ExtractOneMinAIAnswer([]byte("not json")); err == nil {
		t.Error("expected error on malformed JSON")
	}

	// Case 3: Missing aiRecord
	if _, err := ExtractOneMinAIAnswer([]byte(`{"foo": "bar"}`)); err == nil {
		t.Error("expected error on missing aiRecord")
	}

	// Case 4: Status FAILED
	statusFailed := []byte(`{
		"aiRecord": {
			"status": "FAILED",
			"aiRecordDetail": {
				"resultObject": ["some text"]
			}
		}
	}`)
	if _, err := ExtractOneMinAIAnswer(statusFailed); err == nil {
		t.Error("expected error when status is FAILED")
	}

	// Case 5: Missing aiRecordDetail
	missingDetail := []byte(`{
		"aiRecord": {
			"status": "SUCCESS"
		}
	}`)
	if _, err := ExtractOneMinAIAnswer(missingDetail); err == nil {
		t.Error("expected error on missing aiRecordDetail")
	}

	// Case 6: Empty resultObject
	emptyResult := []byte(`{
		"aiRecord": {
			"status": "SUCCESS",
			"aiRecordDetail": {
				"resultObject": []
			}
		}
	}`)
	if _, err := ExtractOneMinAIAnswer(emptyResult); err == nil {
		t.Error("expected error on empty resultObject")
	}

	// Case 7: Top-level error
	topError := []byte(`{
		"error": "Invalid API key provided",
		"code": 401
	}`)
	if _, err := ExtractOneMinAIAnswer(topError); err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("expected error with API key message, got %v", err)
	}
}
