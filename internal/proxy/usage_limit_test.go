package proxy

import (
	"net/http"
	"testing"
	"time"
)

func TestIsUsageLimitError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{"402 with usage limit reached", http.StatusPaymentRequired, `{"error":"Usage limit reached, will reset on tomorrow at 3:00 AM (UTC+8)"}`, true},
		{"402 with lowercase usage limit", http.StatusPaymentRequired, "usage limit", true},
		{"402 without usage limit phrase", http.StatusPaymentRequired, `{"error":"Card declined"}`, false},
		{"non-402 with usage limit text", http.StatusForbidden, "usage limit reached", false},
		{"200 OK", http.StatusOK, "", false},
		{"empty body 402", http.StatusPaymentRequired, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUsageLimitError(tc.statusCode, tc.body)
			if got != tc.want {
				t.Fatalf("isUsageLimitError(%d, %q) = %v, want %v", tc.statusCode, tc.body, got, tc.want)
			}
		})
	}
}

// TestParseUsageLimitCooldown verifies the reset-time parser handles every
// variant FreeModel has been observed to emit: today/tomorrow, AM/PM,
// explicit timezone offsets, missing timezone, and garbage. Result is always
// clamped to (now, now+24h].
func TestParseUsageLimitCooldown(t *testing.T) {
	// Pin "now" to a fixed instant so absolute-time math is deterministic.
	// 2026-06-08 12:00:00 UTC == 2026-06-08 20:00:00 UTC+8.
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		body string
		// We don't always know the exact instant the parser will return (default
		// fallback uses now+5h, parsed values depend on tz math), so each case
		// either asserts an exact target or an expected duration window.
		assert func(t *testing.T, got time.Time)
	}{
		{
			name: "tomorrow 3:37 AM UTC+8",
			body: "Usage limit reached, will reset on tomorrow at 3:37 AM (UTC+8)",
			// Tomorrow at 03:37 in UTC+8 == 2026-06-09 03:37 +0800 == 2026-06-08 19:37 UTC.
			assert: assertEq(time.Date(2026, 6, 8, 19, 37, 0, 0, time.UTC)),
		},
		{
			name: "today 3:00 PM UTC+8 (later today)",
			body: "Usage limit reached, will reset on today at 3:00 PM (UTC+8)",
			// 15:00 in UTC+8 == 07:00 UTC, but now in UTC+8 is 20:00, so "today at 15:00"
			// has already passed → bumps to next day. 2026-06-09 15:00 +0800 == 07:00 UTC.
			assert: assertEq(time.Date(2026, 6, 9, 7, 0, 0, 0, time.UTC)),
		},
		{
			name: "explicit UTC-5",
			body: "will reset at 9:00 AM (UTC-5)",
			// 09:00 in UTC-5 == 14:00 UTC. now is 12:00 UTC, still in the future,
			// no day bump.
			assert: assertEq(time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)),
		},
		{
			name: "missing timezone defaults to UTC",
			body: "will reset at 11:30 PM",
			// 23:30 UTC, no day bump (12:00 now).
			assert: assertEq(time.Date(2026, 6, 8, 23, 30, 0, 0, time.UTC)),
		},
		{
			name:   "time already passed today → next day",
			body:   "will reset on today at 6:00 AM (UTC+0)",
			assert: assertEq(time.Date(2026, 6, 9, 6, 0, 0, 0, time.UTC)),
		},
		{
			name:   "no reset phrase → fallback to 5h",
			body:   `{"error":"Card declined"}`,
			assert: assertDuration(now, defaultUsageLimitCooldown),
		},
		{
			name:   "garbage body → fallback",
			body:   "????",
			assert: assertDuration(now, defaultUsageLimitCooldown),
		},
		{
			name:   "reset phrase but unparsable time → fallback",
			body:   "will reset at unknown time later",
			assert: assertDuration(now, defaultUsageLimitCooldown),
		},
		{
			name: "12 AM means midnight (hour=0)",
			body: "will reset on tomorrow at 12:00 AM (UTC+0)",
			// 00:00 next day UTC.
			assert: assertEq(time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "12 PM means noon (hour=12)",
			body:   "will reset on tomorrow at 12:00 PM (UTC+0)",
			assert: assertEq(time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseUsageLimitCooldown(tc.body, now)
			if !got.After(now) {
				t.Fatalf("cooldown %s is not after now %s", got, now)
			}
			if got.Sub(now) > 24*time.Hour {
				t.Fatalf("cooldown %s exceeds 24h from now %s", got, now)
			}
			tc.assert(t, got)
		})
	}
}

func assertEq(want time.Time) func(*testing.T, time.Time) {
	return func(t *testing.T, got time.Time) {
		t.Helper()
		if !got.Equal(want) {
			t.Fatalf("got %s, want %s", got.UTC(), want)
		}
	}
}

func assertDuration(now time.Time, want time.Duration) func(*testing.T, time.Time) {
	return func(t *testing.T, got time.Time) {
		t.Helper()
		actual := got.Sub(now)
		if actual != want {
			t.Fatalf("got duration %s, want %s", actual, want)
		}
	}
}

func TestIsClientDisconnectError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"random error", errString("connection refused"), false},
		{"broken pipe", errString("write tcp ...: broken pipe"), true},
		{"connection reset by peer", errString("read tcp ...: connection reset by peer"), true},
		{"windows wsasend forcibly closed", errString("write tcp 127.0.0.1:52710->127.0.0.1:63962: wsasend: An existing connection was forcibly closed by the remote host."), true},
		{"windows wsarecv", errString("read tcp ...: wsarecv: An existing connection was forcibly closed."), true},
		{"use of closed network connection", errString("use of closed network connection"), true},
		{"context canceled", errString("context canceled"), true},
		{"upper case", errString("Broken Pipe"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isClientDisconnectError(tc.err)
			if got != tc.want {
				t.Fatalf("isClientDisconnectError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestLooksLikeJSONBody(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"", false},
		{"   ", false},
		{`{"model":"x"}`, true},
		{"  \n\t{\"x\":1}", true},
		{"[1,2,3]", true},
		{"<html>error</html>", false},
		{"plain text", false},
		{"42", false},                // bare number, not an object/array
		{"\"just a string\"", false}, // bare string
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			got := looksLikeJSONBody([]byte(tc.body))
			if got != tc.want {
				t.Fatalf("looksLikeJSONBody(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestShouldLogUsageLimit verifies the 10s dedup window collapses concurrent
// duplicate log calls but lets later ones through after the window expires.
// We can't manipulate time.Now from inside, so we exploit the in-process
// state directly.
func TestShouldLogUsageLimit(t *testing.T) {
	p := &Proxy{usageLimitLogged: make(map[string]time.Time)}

	// First call — log it.
	if !p.shouldLogUsageLimit("Freemodel", 14) {
		t.Fatal("first call should log")
	}
	// Immediate repeat — suppress.
	if p.shouldLogUsageLimit("Freemodel", 14) {
		t.Fatal("immediate repeat should be suppressed")
	}
	// Different credential — log it independently.
	if !p.shouldLogUsageLimit("Freemodel", 15) {
		t.Fatal("different credential should log")
	}
	// Same credential, different endpoint — also independent.
	if !p.shouldLogUsageLimit("byesu", 14) {
		t.Fatal("different endpoint should log")
	}
	// Force-expire the original entry and try again.
	p.usageLimitLogMu.Lock()
	p.usageLimitLogged["usage_limit|Freemodel|14"] = time.Now().Add(-time.Hour)
	p.usageLimitLogMu.Unlock()
	if !p.shouldLogUsageLimit("Freemodel", 14) {
		t.Fatal("after window expiry the same pair should log again")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
