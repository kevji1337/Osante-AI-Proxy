package main

import (
	"net/http"

	"github.com/kevji1337/Osante-AI-Proxy/cmd/server/webui"
	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

// registerWebUI registers the Web UI routes
func registerWebUI(mux *http.ServeMux, cfg *config.Config, p *proxy.Proxy, storage *storage.SQLiteStorage) error {
	ui := webui.New(cfg, p, storage)
	return ui.RegisterRoutes(mux)
}
