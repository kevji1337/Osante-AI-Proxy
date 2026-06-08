package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// handleClearCooldowns wipes endpoint-level cooldowns (the in-memory map) AND
// token-pool cooldowns (persisted in SQLite). Used by the Dashboard "CLEAR
// COOLDOWNS" quick-action.
func (h *Handler) handleClearCooldowns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	epClearedCount := h.proxy.ClearAllCooldowns()

	var tokenCleared int64
	if h.storage != nil {
		n, err := h.storage.ClearAllTokenCooldowns()
		if err != nil {
			logger.Error("Failed to clear token cooldowns: %v", err)
			WriteError(w, http.StatusInternalServerError, "Failed to clear token cooldowns")
			return
		}
		tokenCleared = n
	}
	logger.Info("Admin action: cleared %d endpoint cooldowns + %d token cooldowns", epClearedCount, tokenCleared)
	WriteSuccess(w, map[string]interface{}{
		"endpoints_cleared":  epClearedCount,
		"tokens_cleared":     tokenCleared,
	})
}

// handleFlushStats deletes daily_stats and credential_usage rows. Endpoints
// + credentials are preserved. This is destructive; the UI confirms first.
func (h *Handler) handleFlushStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.storage == nil {
		WriteError(w, http.StatusServiceUnavailable, "Storage unavailable")
		return
	}
	deleted, err := h.storage.DeleteAllStats()
	if err != nil {
		logger.Error("Failed to flush stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to flush stats")
		return
	}
	logger.Warn("Admin action: flushed %d daily_stats rows + credential_usage", deleted)
	WriteSuccess(w, map[string]interface{}{
		"daily_stats_deleted": deleted,
	})
}

// handleExportBackup serves a JSON dump of the current configuration:
// endpoints (without API keys), runtime state, and proxy config. The user
// downloads this for safekeeping or to bootstrap a fresh install.
//
// API keys + credential tokens are NEVER included — this is "config
// portability", not credential export.
func (h *Handler) handleExportBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	endpoints := h.config.GetEndpoints()
	// Strip the api_key field — backups shouldn't bleed secrets.
	safeEndpoints := make([]map[string]interface{}, 0, len(endpoints))
	for _, ep := range endpoints {
		safeEndpoints = append(safeEndpoints, map[string]interface{}{
			"name":        ep.Name,
			"apiUrl":      ep.APIUrl,
			"authMode":    ep.AuthMode,
			"enabled":     ep.Enabled,
			"transformer": ep.Transformer,
			"model":       ep.Model,
			"remark":      ep.Remark,
		})
	}

	payload := map[string]interface{}{
		"exported_at":     time.Now().UTC().Format(time.RFC3339),
		"format_version":  1,
		"port":            h.config.GetPort(),
		"endpoints":       safeEndpoints,
		"note":            "API keys and credential tokens are NOT included. Re-import endpoints manually after restore.",
	}

	filename := fmt.Sprintf("osante-backup-%s.json", time.Now().Format("2006-01-02-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}
