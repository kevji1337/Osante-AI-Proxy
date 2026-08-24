package cc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

func TestClaudeTransformRequestStampsTheModel(t *testing.T) {
	body := []byte(`{"model":"from-client","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	t.Run("no override passes the body through untouched", func(t *testing.T) {
		out, err := NewClaudeTransformer().TransformRequest(body)
		if err != nil {
			t.Fatalf("TransformRequest: %v", err)
		}
		if string(out) != string(body) {
			t.Errorf("body was rewritten with no override set:\n  in:  %s\n  out: %s", body, out)
		}
	})

	t.Run("override replaces the model", func(t *testing.T) {
		out, err := NewClaudeTransformerWithModel("endpoint-model").TransformRequest(body)
		if err != nil {
			t.Fatalf("TransformRequest: %v", err)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("result is not JSON: %v (%s)", err, out)
		}
		if got["model"] != "endpoint-model" {
			t.Errorf("model = %v, want endpoint-model", got["model"])
		}
		// Everything else has to survive the round trip.
		if got["max_tokens"] != float64(16) || got["messages"] == nil {
			t.Errorf("the rest of the request was lost: %s", out)
		}
	})

	// Health probes and other non-object traffic reach transformers too. There is
	// no model field to stamp, so the body passes through rather than failing the
	// request — this is the deliberate branch that replaced a discarded error.
	t.Run("non-object bodies pass through", func(t *testing.T) {
		tr := NewClaudeTransformerWithModel("endpoint-model")
		for _, in := range []string{"", "   ", "not json", `[1,2,3]`} {
			out, err := tr.TransformRequest([]byte(in))
			if err != nil {
				t.Errorf("body %q: unexpected error %v", in, err)
			}
			if string(out) != in {
				t.Errorf("body %q was rewritten to %q", in, out)
			}
		}
	})

	t.Run("a truncated object is reported", func(t *testing.T) {
		out, err := NewClaudeTransformerWithModel("m").TransformRequest([]byte(`{"model":`))
		if err == nil {
			t.Fatalf("a truncated JSON object was accepted, out=%q", out)
		}
	})
}

// TestClaudeStreamTokenFallback covers the one piece of real logic in this
// otherwise-passthrough transformer: Claude sends input_tokens in message_start
// and sometimes zeroes in the final message_delta. Without the fallback the proxy
// records a request that cost nothing, so the stats under-count.
func TestClaudeStreamTokenFallback(t *testing.T) {
	tr := NewClaudeTransformer()

	ctx := transformer.NewStreamContext()
	start := "data: " + `{"type":"message_start","message":{"usage":{"input_tokens":1234,"output_tokens":0}}}` + "\n"
	if _, err := tr.TransformResponseWithContext([]byte(start), true, ctx); err != nil {
		t.Fatalf("message_start: %v", err)
	}
	if ctx.InputTokens != 1234 {
		t.Fatalf("input tokens were not cached from message_start: %d", ctx.InputTokens)
	}

	ctx.OutputTokens = 77 // as the proxy's own estimate would set it
	delta := "data: " + `{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	out, err := tr.TransformResponseWithContext([]byte(delta), true, ctx)
	if err != nil {
		t.Fatalf("message_delta: %v", err)
	}
	if !strings.Contains(string(out), `"input_tokens":1234`) {
		t.Errorf("input_tokens were not backfilled into message_delta: %s", out)
	}
	if !strings.Contains(string(out), `"output_tokens":77`) {
		t.Errorf("output_tokens were not backfilled into message_delta: %s", out)
	}
}

func TestClaudeStreamLeavesRealUsageAlone(t *testing.T) {
	tr := NewClaudeTransformer()
	ctx := transformer.NewStreamContext()
	ctx.InputTokens = 1
	ctx.OutputTokens = 2

	delta := "data: " + `{"type":"message_delta","usage":{"input_tokens":500,"output_tokens":600}}` + "\n"
	out, err := tr.TransformResponseWithContext([]byte(delta), true, ctx)
	if err != nil {
		t.Fatalf("TransformResponseWithContext: %v", err)
	}
	if !strings.Contains(string(out), `"input_tokens":500`) || !strings.Contains(string(out), `"output_tokens":600`) {
		t.Errorf("real upstream usage was overwritten by the estimates: %s", out)
	}
}

func TestClaudeStreamWithoutContextIsPassthrough(t *testing.T) {
	tr := NewClaudeTransformer()
	sse := []byte("event: message_delta\ndata: {\"type\":\"message_delta\"}\n\n")
	out, err := tr.TransformResponseWithContext(sse, true, nil)
	if err != nil {
		t.Fatalf("TransformResponseWithContext: %v", err)
	}
	if string(out) != string(sse) {
		t.Errorf("body changed with a nil context:\n  in:  %q\n  out: %q", sse, out)
	}
}

// TestClaudeStreamPreservesUnrelatedLines guards against the scanner dropping
// content: every non-data line, and every event it does not rewrite, has to come
// back out.
func TestClaudeStreamPreservesUnrelatedLines(t *testing.T) {
	tr := NewClaudeTransformer()
	ctx := transformer.NewStreamContext()

	sse := strings.Join([]string{
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
		"",
		": keep-alive comment",
		"data: [DONE]",
		"",
	}, "\n")

	out, err := tr.TransformResponseWithContext([]byte(sse), true, ctx)
	if err != nil {
		t.Fatalf("TransformResponseWithContext: %v", err)
	}
	for _, want := range []string{"event: content_block_delta", `"text":"hello"`, ": keep-alive comment", "data: [DONE]"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
}

func TestCCTransformerNames(t *testing.T) {
	// request.go routes on these strings; a rename silently changes the upstream
	// path a request takes.
	names := map[string]string{
		"cc_claude":    NewClaudeTransformer().Name(),
		"cc_openai":    NewOpenAITransformer("m").Name(),
		"cc_openai2":   NewOpenAI2Transformer("m").Name(),
		"cc_gemini":    NewGeminiTransformer("m").Name(),
		"cc_gitlabduo": NewGitLabDuoTransformer("m").Name(),
		"cc_1minai":    NewOneMinAITransformer("m").Name(),
	}
	for want, got := range names {
		if got != want {
			t.Errorf("Name() = %q, want %q", got, want)
		}
	}
}
