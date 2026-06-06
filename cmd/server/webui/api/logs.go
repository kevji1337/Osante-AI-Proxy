package api

import (
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
