package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// handleEvents handles Server-Sent Events for real-time updates
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// No Access-Control-Allow-Origin: this is an unauthenticated admin feed on
	// loopback, and "*" let any site the user visited subscribe to it.

	// Create a flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	// Send initial connection message
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"connected\",\"message\":\"Connected to Osante Proxy events\"}\n\n")
	flusher.Flush()

	// Create ticker for periodic updates
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Listen for client disconnect
	ctx := r.Context()

	logger.Debug("[SSE] Client connected")

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			logger.Debug("[SSE] Client disconnected")
			return
		case <-ticker.C:
			// Send stats update
			stats := h.proxy.StatsSnapshot()

			// The real rotation index, not "first enabled" — listEndpoints
			// already reports this value, and the two disagreeing confused the
			// UI about which endpoint is actually serving traffic.
			currentEndpoint := h.proxy.GetCurrentEndpointName()

			event := map[string]interface{}{
				"type":            "stats",
				"timestamp":       time.Now().Unix(),
				"stats":           stats,
				"currentEndpoint": currentEndpoint,
			}

			data, err := json.Marshal(event)
			if err != nil {
				logger.Error("[SSE] Failed to marshal event: %v", err)
				continue
			}

			// Send event
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}
	}
}
