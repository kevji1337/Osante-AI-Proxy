package logger

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestRingBufferKeepsTheNewestEntries(t *testing.T) {
	const cap = 4
	l := NewLogger(cap)
	l.SetConsoleLevel(ERROR) // keep test output quiet

	for i := 0; i < cap*3; i++ {
		l.Log(INFO, "entry-%d", i)
	}

	entries := l.GetLogs()
	if len(entries) != cap {
		t.Fatalf("ring holds %d entries, want the cap %d", len(entries), cap)
	}
	// The oldest kept entry is the (3*cap - cap)-th one.
	wantFirst := "entry-" + strconv.Itoa(cap*3-cap)
	if entries[0].Message != wantFirst {
		t.Errorf("oldest kept entry = %q, want %q", entries[0].Message, wantFirst)
	}
	wantLast := "entry-" + strconv.Itoa(cap*3-1)
	if entries[len(entries)-1].Message != wantLast {
		t.Errorf("newest entry = %q, want %q", entries[len(entries)-1].Message, wantLast)
	}
}

func TestMinLevelDropsQuietEntries(t *testing.T) {
	l := NewLogger(16)
	l.SetConsoleLevel(ERROR)
	l.SetMinLevel(WARN)

	l.Log(DEBUG, "debug")
	l.Log(INFO, "info")
	l.Log(WARN, "warn")
	l.Log(ERROR, "error")

	got := l.GetLogs()
	if len(got) != 2 {
		t.Fatalf("recorded %d entries, want 2 (WARN and ERROR)", len(got))
	}
	if got[0].Message != "warn" || got[1].Message != "error" {
		t.Errorf("recorded the wrong entries: %q, %q", got[0].Message, got[1].Message)
	}
	if l.GetMinLevel() != WARN {
		t.Errorf("GetMinLevel() = %v, want WARN", l.GetMinLevel())
	}
}

func TestGetLogsByLevelFilters(t *testing.T) {
	l := NewLogger(16)
	l.SetConsoleLevel(ERROR)

	l.Log(DEBUG, "d")
	l.Log(INFO, "i")
	l.Log(ERROR, "e")

	if got := len(l.GetLogsByLevel(INFO)); got != 2 {
		t.Errorf("GetLogsByLevel(INFO) returned %d entries, want 2", got)
	}
	if got := len(l.GetLogsByLevel(ERROR)); got != 1 {
		t.Errorf("GetLogsByLevel(ERROR) returned %d entries, want 1", got)
	}
}

func TestSubscribeDelivers(t *testing.T) {
	l := NewLogger(16)
	l.SetConsoleLevel(ERROR)

	id, ch := l.Subscribe(4)
	l.Log(INFO, "streamed")

	select {
	case entry := <-ch:
		if entry.Message != "streamed" {
			t.Errorf("subscriber got %q", entry.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber received nothing")
	}

	l.Unsubscribe(id)
	// The channel is closed on unsubscribe, so a further Log must not panic on a
	// send to a closed channel.
	l.Log(INFO, "after unsubscribe")
	if _, open := <-ch; open {
		t.Error("channel still delivers after Unsubscribe")
	}
	// Unsubscribing twice must not double-close.
	l.Unsubscribe(id)
}

// TestSlowSubscriberDoesNotBlockLogging pins the non-blocking fan-out: a subscriber
// that never drains loses entries, but must not stall the logger — every request
// path logs, so a stalled logger would stall the proxy.
func TestSlowSubscriberDoesNotBlockLogging(t *testing.T) {
	l := NewLogger(64)
	l.SetConsoleLevel(ERROR)

	id, _ := l.Subscribe(1) // buffer of one, never drained
	defer l.Unsubscribe(id)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			l.Log(INFO, "flood-%d", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging blocked on a subscriber that is not draining")
	}

	if got := len(l.GetLogs()); got != 50 {
		t.Errorf("recorded %d entries, want 50 — the ring must be unaffected by subscribers", got)
	}
}

func TestConcurrentLoggingIsSafe(t *testing.T) {
	l := NewLogger(256)
	l.SetConsoleLevel(ERROR)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				l.Log(INFO, "w%d-%d", w, i)
			}
		}(w)
	}
	// Concurrent readers, to exercise the RLock paths alongside the writers.
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = l.GetLogs()
			}
		}()
	}
	wg.Wait()

	if got := len(l.GetLogs()); got != 200 {
		t.Errorf("recorded %d entries, want 200", got)
	}
}
