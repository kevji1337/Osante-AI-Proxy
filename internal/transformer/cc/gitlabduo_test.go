package cc

import "testing"

func TestToGitLabIdentifier(t *testing.T) {
	cases := map[string]string{
		// Claude Haiku 4.5
		"Claude Haiku 4.5 - Anthropic": "claude_haiku_4_5",
		"Claude Haiku 4.5 - Bedrock":   "claude_haiku_4_5_bedrock",
		"Claude Haiku 4.5 - Vertex":    "claude_haiku_4_5_vertex",
		// Claude Sonnet 4.5 / 4.6
		"Claude Sonnet 4.5 - Anthropic": "claude_sonnet_4_5",
		"Claude Sonnet 4.5 - Vertex":    "claude_sonnet_4_5_vertex",
		"Claude Sonnet 4.5 - Bedrock":   "claude_sonnet_4_5_bedrock",
		"Claude Sonnet 4.6 - Anthropic": "claude_sonnet_4_6",
		"Claude Sonnet 4.6 - Vertex":    "claude_sonnet_4_6_vertex",
		"Claude Sonnet 4.6 - Bedrock":   "claude_sonnet_4_6_bedrock",
		// Claude Opus 4.5 / 4.6 / 4.7 / 4.8
		"Claude Opus 4.5 - Anthropic": "claude_opus_4_5",
		"Claude Opus 4.5 - Vertex":    "claude_opus_4_5_vertex",
		"Claude Opus 4.6 - Anthropic": "claude_opus_4_6",
		"Claude Opus 4.6 - Vertex":    "claude_opus_4_6_vertex",
		"Claude Opus 4.6 - Bedrock":   "claude_opus_4_6_bedrock",
		"Claude Opus 4.7 - Anthropic": "claude_opus_4_7",
		"Claude Opus 4.7 - Vertex":    "claude_opus_4_7_vertex",
		"Claude Opus 4.7 - Bedrock":   "claude_opus_4_7_bedrock",
		"Claude Opus 4.8 - Anthropic": "claude_opus_4_8",
		"Claude Opus 4.8 - Vertex":    "claude_opus_4_8_vertex",
		"Claude Opus 4.8 - Bedrock":   "claude_opus_4_8_bedrock",
		// Gemini
		"Gemini 3.5 Flash - Vertex": "gemini_3_5_flash_vertex",
		// GPT-5.x
		"GPT-5.1 - OpenAI":      "gpt_5_1",
		"GPT-5-Codex - OpenAI":  "gpt_5_codex",
		"GPT-5.2-Codex - OpenAI": "gpt_5_2_codex",
		"GPT-5.3-Codex - OpenAI": "gpt_5_3_codex",
		"GPT-5-Mini - OpenAI":   "gpt_5_mini",
		"GPT-5.2 - OpenAI":      "gpt_5_2",
		"GPT-5.4 - OpenAI":      "gpt_5_4",
		"GPT-5.4-Mini - OpenAI": "gpt_5_4_mini",
		"GPT-5.4-Nano - OpenAI": "gpt_5_4_nano",
		"GPT-5.5 - OpenAI":      "gpt_5_5",
		// Already-snake_case input passthrough
		"claude_opus_4_7":        "claude_opus_4_7",
		"claude_opus_4_7_vertex": "claude_opus_4_7_vertex",
		// Empty / whitespace
		"":    "",
		"   ": "",
	}

	for input, want := range cases {
		got := toGitLabIdentifier(input)
		if got != want {
			t.Errorf("toGitLabIdentifier(%q) = %q, want %q", input, got, want)
		}
	}
}
