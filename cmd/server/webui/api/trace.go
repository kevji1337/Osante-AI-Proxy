package api

import (
	"net/http"
	"strconv"
	"strings"
)

// handleTrace returns the most recent request traces from the proxy's
// in-memory ring. Used by the "Inspector" admin view to visualise per-stage
// timings (received → transformed → upstream sent → ... → done).
//
// Query params:
//   - limit: max records to return (default 50, max 200)
func (h *Handler) handleTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	records := h.proxy.TraceSnapshot(limit)

	WriteSuccess(w, map[string]interface{}{
		"records": records,
		"count":   len(records),
	})
}
