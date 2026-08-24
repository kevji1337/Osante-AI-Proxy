package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// openAIChatRequest is what a Codex Chat client sends: this package's job is to
// convert it into each upstream's dialect.
const openAIChatRequest = `{"model":"gpt-4","max_tokens":64,"messages":[` +
	`{"role":"system","content":"be brief"},` +
	`{"role":"user","content":"what model are you?"}]}`

func allTransformers() map[string]transformer.Transformer {
	const model = "target-model"
	return map[string]transformer.Transformer{
		"claude":  NewClaudeTransformer(model),
		"openai":  NewOpenAITransformer(model),
		"openai2": NewOpenAI2Transformer(model),
		"gemini":  NewGeminiTransformer(model),
		"1minai":  NewOneMinAITransformer(model),
	}
}

// TestTransformRequestKeepsTheConversation is the invariant that matters for every
// target: whatever the wire format, the user's text has to survive the conversion.
// Losing it silently produces an upstream answer to an empty prompt.
func TestTransformRequestKeepsTheConversation(t *testing.T) {
	for name, tr := range allTransformers() {
		t.Run(name, func(t *testing.T) {
			out, err := tr.TransformRequest([]byte(openAIChatRequest))
			if err != nil {
				t.Fatalf("TransformRequest: %v", err)
			}
			if len(out) == 0 {
				t.Fatal("TransformRequest produced an empty body")
			}
			if !json.Valid(out) {
				t.Fatalf("TransformRequest produced invalid JSON: %s", out)
			}
			if !strings.Contains(string(out), "what model are you?") {
				t.Errorf("the user message did not survive the conversion: %s", out)
			}
		})
	}
}

func TestTransformerNamesMatchTheRegistry(t *testing.T) {
	// These names are what request.go switches on to pick the upstream path, so a
	// rename here silently routes requests to the wrong endpoint.
	want := map[string]string{
		"claude":  "cx_chat_claude",
		"openai":  "cx_chat_openai",
		"openai2": "cx_chat_openai2",
		"gemini":  "cx_chat_gemini",
		"1minai":  "cx_chat_1minai",
	}
	for key, tr := range allTransformers() {
		if got := tr.Name(); got != want[key] {
			t.Errorf("%s transformer Name() = %q, want %q", key, got, want[key])
		}
	}
}

// TestTransformResponseRejectsBrokenJSON is the other half of the nilerr fix: a
// converter fed a malformed upstream reply must report an error, never an empty
// success that the proxy would forward as a blank answer.
//
// passthroughResponses lists the transformers whose TransformResponse hands the
// body back untouched — those cannot and should not inspect it.
func TestTransformResponseRejectsBrokenJSON(t *testing.T) {
	passthroughResponses := map[string]bool{"openai": true}

	broken := []byte(`{"choices":`)
	for name, tr := range allTransformers() {
		t.Run(name, func(t *testing.T) {
			out, err := tr.TransformResponse(broken, false)

			if passthroughResponses[name] {
				if err != nil {
					t.Fatalf("passthrough transformer returned an error: %v", err)
				}
				if string(out) != string(broken) {
					t.Errorf("passthrough transformer altered the body: %s", out)
				}
				return
			}

			if err == nil {
				t.Fatalf("malformed upstream JSON was accepted, out=%q", out)
			}
			if len(out) != 0 {
				t.Errorf("output should be empty alongside the error, got %q", out)
			}
		})
	}
}

// TestTransformRequestOnNonJSON covers the traffic that is not a real client
// request at all — health probes, misdirected GETs, truncated bodies. A converter
// must reject it; a passthrough must hand it back unchanged. What none of them may
// do is report success with an empty body, which the proxy would send upstream as
// a blank request.
func TestTransformRequestOnNonJSON(t *testing.T) {
	passthroughRequests := map[string]bool{"openai": true}

	body := []byte("not json at all")
	for name, tr := range allTransformers() {
		t.Run(name, func(t *testing.T) {
			out, err := tr.TransformRequest(body)

			if passthroughRequests[name] {
				if err != nil {
					t.Fatalf("passthrough transformer returned an error: %v", err)
				}
				if string(out) != string(body) {
					t.Errorf("passthrough transformer altered the body: %q", out)
				}
				return
			}

			if err == nil {
				t.Fatalf("a non-JSON body was accepted, out=%q", out)
			}
			if len(out) != 0 {
				t.Errorf("output should be empty alongside the error, got %q", out)
			}
		})
	}
}
