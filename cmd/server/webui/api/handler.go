package api

import (
	"net/http"
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

// Handler handles API requests.
//
// The admin API is unauthenticated by design: this is a local-loopback proxy
// for a single user, the BasicAuth flow has been removed entirely.
type Handler struct {
	config  *config.Config
	proxy   *proxy.Proxy
	storage *storage.SQLiteStorage
}

// NewHandler creates a new API handler
func NewHandler(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage) *Handler {
	return &Handler{
		config:  cfg,
		proxy:   p,
		storage: s,
	}
}

// ServeHTTP implements http.Handler interface
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	switch path {
	case "/api/endpoints":
		h.handleEndpoints(w, r)
	case "/api/endpoints/current":
		h.handleCurrentEndpoint(w, r)
	case "/api/endpoints/switch":
		h.handleSwitchEndpoint(w, r)
	case "/api/endpoints/reorder":
		h.handleReorderEndpoints(w, r)
	case "/api/endpoints/fetch-models":
		h.handleFetchModels(w, r)
	case "/api/stats/summary":
		h.handleStatsSummary(w, r)
	case "/api/stats/daily":
		h.handleStatsDaily(w, r)
	case "/api/stats/weekly":
		h.handleStatsWeekly(w, r)
	case "/api/stats/monthly":
		h.handleStatsMonthly(w, r)
	case "/api/stats/trends":
		h.handleStatsTrends(w, r)
	case "/api/config":
		h.handleConfig(w, r)
	case "/api/config/port":
		h.handleConfigPort(w, r)
	case "/api/config/log-level":
		h.handleConfigLogLevel(w, r)
	case "/api/events":
		h.handleEvents(w, r)
	case "/api/logs":
		h.handleLogs(w, r)
	case "/api/logs/stream":
		h.handleLogsStream(w, r)
	case "/api/trace":
		h.handleTrace(w, r)
	case "/api/actions/clear-cooldowns":
		h.handleClearCooldowns(w, r)
	case "/api/actions/flush-stats":
		h.handleFlushStats(w, r)
	case "/api/actions/export-backup":
		h.handleExportBackup(w, r)
	default:
		if strings.HasPrefix(path, "/api/endpoints/") {
			h.handleEndpointByName(w, r)
			return
		}
		http.NotFound(w, r)
	}
}
