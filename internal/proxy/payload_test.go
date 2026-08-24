package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRewritePreservesLargeIntegers is the reason this single-pass rewriter exists
// in the first place. Decoding into map[string]interface{} without UseNumber turns
// every number into a float64, so an integer above 2^53 came back changed — and the
// old pipeline ran that round-trip up to three times per attempt.
func TestRewritePreservesLargeIntegers(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1, not representable as a float64
	body := []byte(`{"model":"old","max_tokens":` + big + `,"messages":[]}`)

	out, err := rewriteRequestPayload(body, payloadRewrite{Model: "new"})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(string(out), big) {
		t.Errorf("large integer was altered by the rewrite:\n  in:  %s\n  out: %s", body, out)
	}
	if !strings.Contains(string(out), `"model":"new"`) {
		t.Errorf("model override did not apply: %s", out)
	}
}

func TestRewriteNoopReturnsInputUnchanged(t *testing.T) {
	body := []byte(`{"model":"m","messages":[]}`)
	out, err := rewriteRequestPayload(body, payloadRewrite{})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// Same backing array: an empty rewrite must not re-serialize at all.
	if &out[0] != &body[0] {
		t.Errorf("a no-op rewrite re-encoded the body: %s", out)
	}
}

func TestRewriteLeavesNonObjectBodiesAlone(t *testing.T) {
	for _, body := range []string{`[{"role":"user"}]`, ``, `event: ping`, `   `} {
		out, err := rewriteRequestPayload([]byte(body), payloadRewrite{Model: "m"})
		if err != nil {
			t.Errorf("body %q: unexpected error %v", body, err)
		}
		if string(out) != body {
			t.Errorf("body %q was rewritten to %q", body, out)
		}
	}
}

func TestRewriteReportsMalformedObject(t *testing.T) {
	out, err := rewriteRequestPayload([]byte(`{"model":`), payloadRewrite{Model: "m"})
	if err == nil {
		t.Fatal("a truncated object produced no error")
	}
	if string(out) != `{"model":` {
		t.Errorf("body must be returned unchanged on error, got %q", out)
	}
}

// TestDropIncompleteToolCalls covers the tool_use cleanup: Claude Code emits a
// tool_use block with no input when a call is interrupted, and upstreams reject the
// whole request over it.
func TestDropIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantMessages int
		wantDropped  bool
	}{
		{
			name:         "incomplete block removed, sibling text kept",
			body:         `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"1"}]}]}`,
			wantMessages: 2,
			wantDropped:  true,
		},
		{
			name:         "message dropped when nothing survives",
			body:         `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"1"}]}]}`,
			wantMessages: 1,
			wantDropped:  true,
		},
		{
			name:         "complete tool_use is left alone",
			body:         `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"1","input":{"a":1}}]}]}`,
			wantMessages: 1,
			wantDropped:  false,
		},
		{
			name:         "trailing user message means nothing to clean",
			body:         `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"1"}]},{"role":"user","content":"hi"}]}`,
			wantMessages: 2,
			wantDropped:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := rewriteRequestPayload([]byte(tc.body), payloadRewrite{CleanToolCalls: true})
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if changed := string(out) != tc.body; changed != tc.wantDropped {
				t.Errorf("body changed = %v, want %v\n  out: %s", changed, tc.wantDropped, out)
			}

			var decoded struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if len(decoded.Messages) != tc.wantMessages {
				t.Errorf("messages = %d, want %d: %s", len(decoded.Messages), tc.wantMessages, out)
			}
			if strings.Contains(string(out), `"tool_use"`) && tc.wantDropped && !strings.Contains(tc.name, "sibling") {
				t.Errorf("incomplete tool_use survived: %s", out)
			}
		})
	}
}

func TestRewriteCodexResponsesFields(t *testing.T) {
	out, err := rewriteRequestPayload([]byte(`{"model":"gpt-5-codex"}`), payloadRewrite{CodexResponses: true})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["store"] != false {
		t.Errorf(`store = %v, want false`, got["store"])
	}
	if got["stream"] != true {
		t.Errorf(`stream = %v, want true`, got["stream"])
	}
	if _, ok := got["instructions"]; !ok {
		t.Error("instructions field was not added")
	}
}
