package proxy

import (
	"testing"
	"unicode/utf8"
)

// TestTruncateStringKeepsValidUTF8 guards the log/trace/credential truncation
// helper: a plain value[:max] cuts multi-byte runes in half, which then renders
// as mojibake in the log feed and the credentials table.
func TestTruncateStringKeepsValidUTF8(t *testing.T) {
	// Cyrillic is 2 bytes per rune, so an odd byte limit lands mid-rune.
	input := "ошибка апстрима: превышен лимит"
	for _, max := range []int{1, 5, 11, 20, 33} {
		got := truncateString(input, max)
		if !utf8.ValidString(got) {
			t.Errorf("truncateString(%q, %d) = %q, which is not valid UTF-8", input, max, got)
		}
	}

	if got := truncateString("short", 100); got != "short" {
		t.Errorf("value below the limit was modified: %q", got)
	}
}
