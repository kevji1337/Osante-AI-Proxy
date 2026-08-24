package tokencount

import (
	"strings"
	"testing"
)

func TestEstimateOutputTokens(t *testing.T) {
	if got := EstimateOutputTokens(""); got != 0 {
		t.Errorf("empty text = %d tokens, want 0", got)
	}
	// Anything non-empty is at least one token.
	if got := EstimateOutputTokens("a"); got != 1 {
		t.Errorf("single char = %d tokens, want 1", got)
	}

	// Longer text costs more.
	short := EstimateOutputTokens(strings.Repeat("word ", 10))
	long := EstimateOutputTokens(strings.Repeat("word ", 100))
	if long <= short {
		t.Errorf("longer text did not cost more: short=%d long=%d", short, long)
	}
}

// TestEstimateTextDensityByScript covers the CJK heuristic: the estimator assumes
// ~4 chars per token for Latin text and ~1.5 for Chinese, so the same rune count
// must cost noticeably more in Chinese.
func TestEstimateTextDensityByScript(t *testing.T) {
	const runes = 200
	latin := EstimateOutputTokens(strings.Repeat("a", runes))
	chinese := EstimateOutputTokens(strings.Repeat("中", runes))

	if chinese <= latin {
		t.Errorf("Chinese text (%d) should cost more than Latin (%d) for %d runes", chinese, latin, runes)
	}

	// Cyrillic is not part of the heuristic and is treated like Latin — pinning it
	// so a future change to the ratio is a deliberate decision, not a surprise.
	cyrillic := EstimateOutputTokens(strings.Repeat("я", runes))
	if cyrillic != latin {
		t.Errorf("Cyrillic (%d) is estimated like Latin (%d) today; update this test if the heuristic changes", cyrillic, latin)
	}
}

func TestEstimateInputTokensCountsEveryPart(t *testing.T) {
	base := EstimateInputTokens(&CountTokensRequest{Model: "m"})
	if base <= 0 {
		t.Fatalf("an empty request cost %d tokens, want the base overhead", base)
	}

	withMessage := EstimateInputTokens(&CountTokensRequest{
		Model:    "m",
		Messages: []MessageParam{{Role: "user", Content: "hello there"}},
	})
	if withMessage <= base {
		t.Errorf("a message did not add tokens: base=%d withMessage=%d", base, withMessage)
	}

	withSystem := EstimateInputTokens(&CountTokensRequest{
		Model:    "m",
		System:   "you are a helpful assistant",
		Messages: []MessageParam{{Role: "user", Content: "hello there"}},
	})
	if withSystem <= withMessage {
		t.Errorf("a system prompt did not add tokens: %d vs %d", withSystem, withMessage)
	}

	withTools := EstimateInputTokens(&CountTokensRequest{
		Model:    "m",
		Messages: []MessageParam{{Role: "user", Content: "hello there"}},
		Tools: []Tool{{
			Name:        "read_file",
			Description: "Read a file from disk",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	})
	if withTools <= withMessage {
		t.Errorf("a tool definition did not add tokens: %d vs %d", withTools, withMessage)
	}
}

// TestEstimateInputTokensHandlesBlockContent exercises the structured content path:
// Claude Code sends content as an array of typed blocks, not a bare string.
func TestEstimateInputTokensHandlesBlockContent(t *testing.T) {
	req := &CountTokensRequest{
		Model: "m",
		Messages: []MessageParam{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "look at this"},
				map[string]any{"type": "tool_result", "content": "the result"},
			},
		}},
	}
	blocks := EstimateInputTokens(req)

	textOnly := EstimateInputTokens(&CountTokensRequest{
		Model:    "m",
		Messages: []MessageParam{{Role: "user", Content: "look at this"}},
	})
	if blocks <= textOnly {
		t.Errorf("block content (%d) should cost at least as much as the text alone (%d)", blocks, textOnly)
	}
}

// TestEstimateInputTokensSurvivesOddContent guards the estimator against the shapes
// a misbehaving client can send: it must return a number, not panic.
func TestEstimateInputTokensSurvivesOddContent(t *testing.T) {
	for name, content := range map[string]any{
		"nil":            nil,
		"number":         42,
		"bool":           true,
		"empty array":    []any{},
		"nested map":     map[string]any{"unexpected": map[string]any{"deep": true}},
		"array of nulls": []any{nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := EstimateInputTokens(&CountTokensRequest{
				Model:    "m",
				Messages: []MessageParam{{Role: "user", Content: content}},
			})
			if got <= 0 {
				t.Errorf("estimate = %d, want a positive count", got)
			}
		})
	}
}
