package webui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/kevji1337/Osante-AI-Proxy/cmd/server/webui/api"
	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

//go:embed ui
var uiFS embed.FS

// WebUI represents the web management interface
type WebUI struct {
	cfg        *config.Config
	apiHandler *api.Handler
}

// New creates a new WebUI instance
func New(cfg *config.Config, p *proxy.Proxy, storage *storage.SQLiteStorage) *WebUI {
	return &WebUI{
		cfg:        cfg,
		apiHandler: api.NewHandler(cfg, p, storage),
	}
}

// SetShutdownFunc lets the host expose POST /api/actions/shutdown. Without it that
// endpoint reports itself unavailable, which is the right answer for a build that
// has no way to stop itself.
func (w *WebUI) SetShutdownFunc(fn func(reason string)) {
	w.apiHandler.SetShutdownFunc(fn)
}

// RegisterRoutes registers all web UI routes to the provided mux.
//
// The admin API and the static UI are served without authentication (loopback,
// single user), but the API is wrapped in two guards: LocalOnlyMiddleware
// rejects cross-site / DNS-rebound requests, and RecoveryMiddleware turns a
// panic in a handler into a 500 instead of a torn-down connection.
func (w *WebUI) RegisterRoutes(mux *http.ServeMux) error {
	mux.Handle("/api/", api.RecoveryMiddleware(
		api.LocalOnlyMiddleware(http.HandlerFunc(w.apiHandler.ServeHTTP))))

	uiSubFS, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return err
	}

	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(uiSubFS))))

	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})

	return nil
}
