package convert

import (
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// TestStreamConvertersReportBrokenJSON pins the behavior these converters had
// backwards: a malformed SSE payload from the upstream used to be discarded with
// (nil, nil), i.e. indistinguishable from "nothing to emit for this event". The
// proxy logs a transform error and skips the event, so a broken upstream stayed
// completely invisible in the logs.
func TestStreamConvertersReportBrokenJSON(t *testing.T) {
	// A well-formed SSE frame whose data is not valid JSON.
	broken := []byte("event: message_delta\ndata: {\"unterminated\":\n\n")

	cases := map[string]func() ([]byte, error){
		"ClaudeStreamToOpenAI": func() ([]byte, error) {
			return ClaudeStreamToOpenAI(broken, transformer.NewStreamContext(), "gpt-4")
		},
		"ClaudeStreamToOpenAI2": func() ([]byte, error) {
			return ClaudeStreamToOpenAI2(broken, transformer.NewStreamContext())
		},
		"ClaudeStreamToGemini": func() ([]byte, error) {
			return ClaudeStreamToGemini(broken, transformer.NewStreamContext())
		},
		"GeminiStreamToOpenAI": func() ([]byte, error) {
			return GeminiStreamToOpenAI(broken, transformer.NewStreamContext(), "gpt-4")
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := call()
			if err == nil {
				t.Fatalf("malformed SSE data returned no error (out=%q)", out)
			}
			if out != nil {
				t.Errorf("output should be nil alongside the error, got %q", out)
			}
			if !strings.Contains(err.Error(), "not valid JSON") {
				t.Errorf("error does not explain the cause: %v", err)
			}
		})
	}
}

// TestStreamConvertersIgnoreNonDataFrames confirms the error path did not swallow
// the legitimate "nothing to emit" case: a frame with no data line is still a
// silent no-op, not an error.
func TestStreamConvertersIgnoreNonDataFrames(t *testing.T) {
	comment := []byte(": keep-alive\n\n")
	if out, err := ClaudeStreamToOpenAI(comment, transformer.NewStreamContext(), "gpt-4"); err != nil || out != nil {
		t.Errorf("keep-alive frame: out=%q err=%v, want (nil, nil)", out, err)
	}
}
