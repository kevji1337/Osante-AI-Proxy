package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

func TestOneMinAIRoutingAndNormalization(t *testing.T) {
	testCases := []struct {
		name        string
		baseURL     string
		expectedURL string
	}{
		{
			name:        "Standard root URL without trailing slash",
			baseURL:     "https://api.1min.ai",
			expectedURL: "https://api.1min.ai/api/features",
		},
		{
			name:        "Standard root URL with trailing slash",
			baseURL:     "https://api.1min.ai/",
			expectedURL: "https://api.1min.ai/api/features",
		},
		{
			name:        "URL with /api/features included",
			baseURL:     "https://api.1min.ai/api/features",
			expectedURL: "https://api.1min.ai/api/features",
		},
		{
			name:        "URL with /api/features/ included",
			baseURL:     "https://api.1min.ai/api/features/",
			expectedURL: "https://api.1min.ai/api/features",
		},
		{
			name:        "URL with /api prefix",
			baseURL:     "https://api.1min.ai/api",
			expectedURL: "https://api.1min.ai/api/features",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := config.Endpoint{
				Name:        "1minAI",
				APIUrl:      tc.baseURL,
				AuthMode:    config.AuthModeAPIKey,
				Transformer: "1minai",
				Model:       "gpt-5.6-sol",
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Authorization", "Bearer client-secret-token")
			req.Header.Set("x-api-key", "client-anthropic-token")
			req.Header.Set("X-Api-Key", "client-anthropic-token-2")

			body := []byte(`{"type":"CODE_GENERATOR","model":"gpt-5.6-sol","promptObject":{"prompt":"test","webSearch":false}}`)
			proxyReq, err := buildProxyRequest(req, endpoint, "1min-api-key-12345", body, "cx_chat_1minai", "gpt-5.6-sol", nil)
			if err != nil {
				t.Fatalf("buildProxyRequest failed: %v", err)
			}

			if proxyReq.URL.String() != tc.expectedURL {
				t.Errorf("expected URL %q, got %q", tc.expectedURL, proxyReq.URL.String())
			}

			// Verify Auth Headers
			if proxyReq.Header.Get("API-KEY") != "1min-api-key-12345" {
				t.Errorf("expected API-KEY header 1min-api-key-12345, got %q", proxyReq.Header.Get("API-KEY"))
			}
			if proxyReq.Header.Get("Content-Type") != "application/json" {
				t.Errorf("expected Content-Type application/json, got %q", proxyReq.Header.Get("Content-Type"))
			}

			// Verify that client auth headers were stripped
			if auth := proxyReq.Header.Get("Authorization"); auth != "" {
				t.Errorf("Authorization header must be removed for 1min.AI, got %q", auth)
			}
			if xak := proxyReq.Header.Get("x-api-key"); xak != "" {
				t.Errorf("x-api-key header must be removed for 1min.AI, got %q", xak)
			}
			if xak := proxyReq.Header.Get("X-Api-Key"); xak != "" {
				t.Errorf("X-Api-Key header must be removed for 1min.AI, got %q", xak)
			}
		})
	}
}

func TestPrepareOneMinAITransformers(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "OneMinEndpoint",
		APIUrl:      "https://api.1min.ai",
		Transformer: "1minai",
		Model:       "gpt-5.6-sol",
	}

	// 1. Claude client format -> cc_1minai
	ccTrans, err := prepareTransformerForClient(ClientFormatClaude, endpoint, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("prepare cc transformer failed: %v", err)
	}
	if ccTrans.Name() != "cc_1minai" {
		t.Errorf("expected cc_1minai, got %s", ccTrans.Name())
	}

	// 2. OpenAI Chat client format -> cx_chat_1minai
	chatTrans, err := prepareTransformerForClient(ClientFormatOpenAIChat, endpoint, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("prepare chat transformer failed: %v", err)
	}
	if chatTrans.Name() != "cx_chat_1minai" {
		t.Errorf("expected cx_chat_1minai, got %s", chatTrans.Name())
	}

	// 3. OpenAI Responses client format -> cx_resp_1minai
	respTrans, err := prepareTransformerForClient(ClientFormatOpenAIResponses, endpoint, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("prepare resp transformer failed: %v", err)
	}
	if respTrans.Name() != "cx_resp_1minai" {
		t.Errorf("expected cx_resp_1minai, got %s", respTrans.Name())
	}
}

func TestOneMinAIEndToEndTransformations(t *testing.T) {
	minUpstreamResp := []byte(`{
		"aiRecord": {
			"uuid": "29a3d3f4-ef1e-4a16-abd9-7353c8e67339",
			"model": "gpt-5.6-sol",
			"type": "CODE_GENERATOR",
			"status": "SUCCESS",
			"aiRecordDetail": {
				"resultObject": [
					"Hello from 1min.AI! Here is the response."
				],
				"responseObject": {}
			}
		}
	}`)

	endpoint := config.Endpoint{
		Name:        "1min",
		APIUrl:      "https://api.1min.ai",
		Transformer: "1minai",
		Model:       "gpt-5.6-sol",
	}

	// Test Chat Completions -> 1min.AI -> Chat Completions (non-stream)
	chatTrans, err := prepareTransformerForClient(ClientFormatOpenAIChat, endpoint, "gpt-5.6-sol")
	if err != nil {
		t.Fatalf("prepare transformer failed: %v", err)
	}

	chatReqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`)
	transformedReq, err := chatTrans.TransformRequest(chatReqBody)
	if err != nil {
		t.Fatalf("transform request failed: %v", err)
	}

	var req OneMinAIReqCheck
	if err := json.Unmarshal(transformedReq, &req); err != nil {
		t.Fatalf("unmarshal 1min request failed: %v", err)
	}
	if req.Type != "CODE_GENERATOR" || req.Model != "gpt-5.6-sol" || req.PromptObject.Prompt != "Hi" {
		t.Errorf("unexpected 1min request: %+v", req)
	}

	chatResp, err := chatTrans.TransformResponse(minUpstreamResp, false)
	if err != nil {
		t.Fatalf("transform response failed: %v", err)
	}

	var chatCompletion transformer.OpenAIResponse
	if err := json.Unmarshal(chatResp, &chatCompletion); err != nil {
		t.Fatalf("unmarshal chat completion failed: %v", err)
	}
	if len(chatCompletion.Choices) == 0 || chatCompletion.Choices[0].Message.Content != "Hello from 1min.AI! Here is the response." {
		t.Errorf("unexpected chat response content: %#v", chatCompletion)
	}

	// Test Chat Completions streaming
	chatStreamReqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true}`)
	_, err = chatTrans.TransformRequest(chatStreamReqBody)
	if err != nil {
		t.Fatalf("transform stream request failed: %v", err)
	}

	chatStreamResp, err := chatTrans.TransformResponse(minUpstreamResp, false)
	if err != nil {
		t.Fatalf("transform stream response failed: %v", err)
	}
	if !looksLikeSSEBody(chatStreamResp) {
		t.Errorf("expected synthesized stream to look like SSE body, got %s", string(chatStreamResp))
	}
	if !strings.Contains(string(chatStreamResp), "data: [DONE]") {
		t.Errorf("expected [DONE] in chat stream response: %s", string(chatStreamResp))
	}
}

func TestOneMinAIDynamicModelForwarding(t *testing.T) {
	// Endpoint with NO model configured (empty model)
	endpoint := config.Endpoint{
		Name:        "1min",
		APIUrl:      "https://api.1min.ai",
		Transformer: "1minai",
		Model:       "", // Empty model!
	}

	// 1. Dynamic model passed in request by client (e.g. OmniRoute)
	chatTrans, err := prepareTransformerForClient(ClientFormatOpenAIChat, endpoint, "")
	if err != nil {
		t.Fatalf("prepare transformer failed: %v", err)
	}

	clientReq := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"Ping"}]}`)
	transformed, err := chatTrans.TransformRequest(clientReq)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	var req OneMinAIReqCheck
	if err := json.Unmarshal(transformed, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if req.Model != "deepseek-v4-pro" {
		t.Errorf("expected model deepseek-v4-pro passed dynamically from request, got %q", req.Model)
	}

	// 2. Dynamic model passed with routing prefix @1min/claude-sonnet-5
	clientReq2 := []byte(`{"model":"@1min/claude-sonnet-5","messages":[{"role":"user","content":"Ping"}]}`)
	transformed2, err := chatTrans.TransformRequest(clientReq2)
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	var req2 OneMinAIReqCheck
	if err := json.Unmarshal(transformed2, &req2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if req2.Model != "claude-sonnet-5" {
		t.Errorf("expected model claude-sonnet-5 stripped from routing prefix, got %q", req2.Model)
	}
}

type OneMinAIReqCheck struct {
	Type         string `json:"type"`
	Model        string `json:"model"`
	PromptObject struct {
		Prompt    string `json:"prompt"`
		WebSearch bool   `json:"webSearch"`
	} `json:"promptObject"`
}

