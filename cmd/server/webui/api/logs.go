package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// handleLogs returns the in-memory ring buffer of recent log entries. Used by
// the Web UI Logs tab.
//
// Query params:
//   - level: optional minimum level (DEBUG / INFO / WARN / ERROR), default INFO
//   - limit: optional maximum number of entries to return, default 500, max 1000
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	level := parseLogLevel(strings.TrimSpace(r.URL.Query().Get("level")), logger.INFO)
	limit := 500
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	entries := logger.GetLogger().GetLogsByLevel(level)
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	WriteSuccess(w, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}

// handleLogsStream is a Server-Sent Events firehose of log entries as they
// are recorded. The client reads it for the duration of the Logs view —
// new entries arrive within tens of milliseconds instead of waiting for the
// next 3s poll.
//
// Query params:
//   - level: minimum level to deliver (default INFO; entries below are dropped server-side)
func (h *Handler) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	level := parseLogLevel(strings.TrimSpace(r.URL.Query().Get("level")), logger.INFO)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering if proxied
	w.WriteHeader(http.StatusOK)

	subID, ch := logger.GetLogger().Subscribe(256)
	defer logger.GetLogger().Unsubscribe(subID)

	// Send a hello so the client knows the connection is live.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			if entry.Level < level {
				continue
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			// SSE format: `data: <json>\n\n`. No event type so client onmessage fires.
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// parseLogLevel maps a case-insensitive level string to a LogLevel. Unknown
// values fall back to the supplied default.
func parseLogLevel(s string, fallback logger.LogLevel) logger.LogLevel {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return logger.DEBUG
	case "INFO":
		return logger.INFO
	case "WARN", "WARNING":
		return logger.WARN
	case "ERROR":
		return logger.ERROR
	}
	return fallback
}
