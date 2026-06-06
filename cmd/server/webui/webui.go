package webui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
	"github.com/kevji1337/Osante-AI-Proxy/cmd/server/webui/api"
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

// RegisterRoutes registers all web UI routes to the provided mux
func (w *WebUI) RegisterRoutes(mux *http.ServeMux) error {
	mux.HandleFunc("/api/", w.apiHandler.ServeHTTP)

	authConfig := api.AuthConfig{
		// Basic Auth is permanently disabled — the web UI/API are open.
		Enabled:  false,
		Username: w.cfg.BasicAuthUsername,
		Password: w.cfg.BasicAuthPassword,
	}
	authMiddleware := api.BasicAuthMiddleware(authConfig)

	uiSubFS, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return err
	}

	uiHandler := authMiddleware(http.FileServer(http.FS(uiSubFS)))
	mux.Handle("/ui/", http.StripPrefix("/ui/", uiHandler))

	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})

	return nil
}