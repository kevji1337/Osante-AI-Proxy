package cc

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractGitLabAnswer covers every response shape GitLab Duo has been observed
// to return. Getting this wrong shows up as an empty assistant turn in Claude Code
// rather than an error, so each shape is pinned.
func TestExtractGitLabAnswer(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "quoted string", body: `"just the text"`, want: "just the text"},
		{name: "response field", body: `{"response":"from response"}`, want: "from response"},
		{name: "content field", body: `{"content":"from content"}`, want: "from content"},
		{name: "message field", body: `{"message":"from message"}`, want: "from message"},
		{
			name: "openai-shaped choices",
			body: `{"choices":[{"message":{"content":"from choices"}}]}`,
			want: "from choices",
		},
		{
			// An upstream error must surface verbatim rather than as a blank reply.
			name: "error field",
			body: `{"error":"insufficient credits"}`,
			want: "insufficient credits",
		},
		{
			// Anything unrecognized falls back to the raw body, so the user at least
			// sees what came back.
			name: "unknown shape falls back to the raw body",
			body: `{"unexpected":{"nested":true}}`,
			want: `{"unexpected":{"nested":true}}`,
		},
		{name: "plain text", body: `not json at all`, want: "not json at all"},
		{name: "empty", body: ``, want: ""},
		{name: "whitespace only", body: "   \n\t ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractGitLabAnswer([]byte(tc.body)); got != tc.want {
				t.Errorf("extractGitLabAnswer(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}

	// Precedence: response wins over the other candidates when several are present.
	both := `{"response":"preferred","content":"ignored","message":"ignored"}`
	if got := extractGitLabAnswer([]byte(both)); got != "preferred" {
		t.Errorf("with several fields present, got %q, want the response field", got)
	}
}

func TestBuildAnthropicJSON(t *testing.T) {
	out, err := buildAnthropicJSON("Claude Sonnet 4.5", "the answer")
	if err != nil {
		t.Fatalf("buildAnthropicJSON: %v", err)
	}

	var got struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not a Messages API response: %v (%s)", err, out)
	}

	if got.Type != "message" || got.Role != "assistant" {
		t.Errorf("type/role = %q/%q, want message/assistant", got.Type, got.Role)
	}
	if !strings.HasPrefix(got.ID, "msg_") {
		t.Errorf("id = %q, want a msg_ prefix", got.ID)
	}
	if got.Model != "Claude Sonnet 4.5" {
		t.Errorf("model = %q, want the echoed model", got.Model)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "the answer" {
		t.Errorf("content block is wrong: %+v", got.Content)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", got.StopReason)
	}
	// Duo gives no usage numbers, so output tokens are estimated; the point is that
	// a non-empty answer never reports zero.
	if got.Usage.OutputTokens <= 0 {
		t.Errorf("output_tokens = %d for a non-empty answer", got.Usage.OutputTokens)
	}
}

// TestBuildAnthropicSSE checks the event sequence Claude Code parses. A missing or
// out-of-order event shows up as a hung or truncated reply in the client.
func TestBuildAnthropicSSE(t *testing.T) {
	answer := strings.Repeat("word ", 40)
	out := string(buildAnthropicSSE("Claude Sonnet 4.5", answer))

	wantOrder := []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	}
	pos := -1
	for _, want := range wantOrder {
		at := strings.Index(out, want)
		if at < 0 {
			t.Fatalf("stream is missing %q:\n%s", want, out)
		}
		if at <= pos {
			t.Errorf("%q appears out of order", want)
		}
		pos = at
	}

	// The text has to arrive in text_delta chunks and reassemble to the answer.
	var rebuilt strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			continue
		}
		if evt.Type == "content_block_delta" && evt.Delta.Type == "text_delta" {
			rebuilt.WriteString(evt.Delta.Text)
		}
	}
	if rebuilt.String() != answer {
		t.Errorf("reassembled text does not match the answer:\n  got:  %q\n  want: %q", rebuilt.String(), answer)
	}

	// Every data line must be valid JSON, or the client's parser gives up mid-stream.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if !json.Valid([]byte(payload)) {
			t.Errorf("data line is not valid JSON: %s", payload)
		}
	}
}

func TestBuildAnthropicSSEWithEmptyAnswer(t *testing.T) {
	// An empty answer still has to produce a well-formed, terminated stream —
	// otherwise the client waits forever.
	out := string(buildAnthropicSSE("m", ""))
	for _, want := range []string{"event: message_start", "event: message_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty answer produced a stream without %q:\n%s", want, out)
		}
	}
}

func TestSplitChunks(t *testing.T) {
	tests := []struct {
		in    string
		size  int
		want  int
		joins string
	}{
		{in: "abcdef", size: 2, want: 3, joins: "abcdef"},
		{in: "abcde", size: 2, want: 3, joins: "abcde"},
		{in: "abc", size: 10, want: 1, joins: "abc"},
		{in: "", size: 4, want: 0, joins: ""},
	}
	for _, tc := range tests {
		got := splitChunks(tc.in, tc.size)
		if len(got) != tc.want {
			t.Errorf("splitChunks(%q, %d) produced %d chunks, want %d (%q)", tc.in, tc.size, len(got), tc.want, got)
		}
		if strings.Join(got, "") != tc.joins {
			t.Errorf("splitChunks(%q, %d) does not reassemble: %q", tc.in, tc.size, got)
		}
	}

	// Multi-byte runes must not be split in half.
	cyrillic := strings.Repeat("я", 10)
	for _, chunk := range splitChunks(cyrillic, 3) {
		if !json.Valid([]byte(`"` + chunk + `"`)) {
			t.Errorf("chunk %q is not valid UTF-8 in a JSON string", chunk)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Errorf("estimateTokens(\"\") = %d, want 0", got)
	}
	short := estimateTokens("a few words here")
	long := estimateTokens(strings.Repeat("a few words here ", 20))
	if short <= 0 {
		t.Errorf("non-empty text estimated at %d tokens", short)
	}
	if long <= short {
		t.Errorf("longer text did not estimate higher: %d vs %d", long, short)
	}
}
