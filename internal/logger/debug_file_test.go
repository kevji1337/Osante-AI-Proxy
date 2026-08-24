package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDebugFileSink covers the debug sink end to end. It had no caller at all
// until OSANTE_DEBUG_FILE was wired up in main, which made every DebugLog in the
// tree a permanent no-op — so this also guards against it going dead again.
func TestDebugFileSink(t *testing.T) {
	l := NewLogger(16)
	path := filepath.Join(t.TempDir(), "debug.log")

	if l.DebugEnabled() {
		t.Fatal("DebugEnabled() is true before any sink was attached")
	}
	// A no-op DebugLog must not panic or create a file.
	l.DebugLog("dropped %d", 1)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file created without EnableDebugFile: %v", err)
	}

	if err := l.EnableDebugFile(path); err != nil {
		t.Fatalf("EnableDebugFile: %v", err)
	}
	if !l.DebugEnabled() {
		t.Error("DebugEnabled() is false after EnableDebugFile")
	}

	l.DebugLog("payload=%s", "hello")
	l.Close()

	if l.DebugEnabled() {
		t.Error("DebugEnabled() is true after Close")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "payload=hello") {
		t.Errorf("debug log does not contain the entry: %q", got)
	}
	if strings.Contains(got, "dropped") {
		t.Errorf("an entry written before the sink existed was recorded: %q", got)
	}

	// Writing after Close must be a no-op again, not a panic on a closed file.
	l.DebugLog("after close")
}

// TestEnableDebugFileReplacesPreviousSink guards the file swap: EnableDebugFile
// closes the old handle before taking the new one.
func TestEnableDebugFileReplacesPreviousSink(t *testing.T) {
	l := NewLogger(16)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")

	if err := l.EnableDebugFile(first); err != nil {
		t.Fatalf("first: %v", err)
	}
	l.DebugLog("to-first")
	if err := l.EnableDebugFile(second); err != nil {
		t.Fatalf("second: %v", err)
	}
	l.DebugLog("to-second")
	l.Close()

	firstData, _ := os.ReadFile(first)
	secondData, _ := os.ReadFile(second)
	if !strings.Contains(string(firstData), "to-first") {
		t.Errorf("first sink lost its entry: %q", firstData)
	}
	if strings.Contains(string(firstData), "to-second") {
		t.Errorf("entry went to the replaced sink: %q", firstData)
	}
	if !strings.Contains(string(secondData), "to-second") {
		t.Errorf("second sink did not receive the entry: %q", secondData)
	}
}

func TestEnableDebugFileRejectsUnwritablePath(t *testing.T) {
	l := NewLogger(16)
	// A path whose parent does not exist cannot be opened.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "debug.log")
	if err := l.EnableDebugFile(bad); err == nil {
		t.Fatal("EnableDebugFile succeeded on an unwritable path")
	}
	if l.DebugEnabled() {
		t.Error("DebugEnabled() is true after a failed EnableDebugFile")
	}
}
