package main

import (
	"net/http"

	"github.com/kevji1337/Osante-AI-Proxy/cmd/server/webui"
	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

// registerWebUI registers the Web UI routes. shutdown, when non-nil, backs
// POST /api/actions/shutdown — the only way to stop the server when it runs
// without a console window.
func registerWebUI(mux *http.ServeMux, cfg *config.Config, p *proxy.Proxy, storage *storage.SQLiteStorage, shutdown func(reason string)) error {
	ui := webui.New(cfg, p, storage)
	if shutdown != nil {
		ui.SetShutdownFunc(shutdown)
	}
	return ui.RegisterRoutes(mux)
}
