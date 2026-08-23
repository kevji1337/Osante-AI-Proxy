package main

//go:generate goversioninfo -platform-specific=false -64=true -o resource_windows_amd64.syso versioninfo.json

import (
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

func main() {
	// Parse command line flags
	portFlag := flag.Int("port", 0, "Force specific port (locked, cannot be changed via API)")
	flag.Parse()
	dataDir := resolveDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Error("Failed to create data dir %s: %v", dataDir, err)
		os.Exit(1)
	}

	dbPath := os.Getenv("OSANTE_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "osante.db")
	}

	sqliteStorage, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		logger.Error("Failed to open SQLite storage: %v", err)
		os.Exit(1)
	}
	defer func() { _ = sqliteStorage.Close() }()

	cfg, err := loadConfig(sqliteStorage)
	if err != nil {
		logger.Error("Unable to load configuration: %v", err)
		os.Exit(1)
	}

	// This build supports only the token pool auth mode. Migrate any legacy
	// api_key endpoints in the DB to token_pool, seeding their existing apiKey
	// as the first pool token when the pool is otherwise empty.
	if err := migrateAllEndpointsToTokenPool(sqliteStorage); err != nil {
		logger.Warn("Token-pool migration encountered errors: %v", err)
	}

	// Handle -port CLI flag (overrides config and locks port)
	if *portFlag > 0 {
		cfg.Port = *portFlag
		cfg.LockPort()
		logger.Info("Port locked to %d via CLI flag", *portFlag)
	}

	// Basic Auth has been permanently disabled — no password generation or prompt.

	applyEnvOverrides(cfg)
	setLogLevels(cfg.GetLogLevel())

	if err := cfg.Validate(); err != nil {
		logger.Error("Invalid configuration: %v", err)
		os.Exit(1)
	}

	deviceID, err := sqliteStorage.GetOrCreateDeviceID()
	if err != nil {
		logger.Warn("Failed to get device ID: %v, using default", err)
		deviceID = "default"
	}

	statsAdapter := proxy.NewSQLiteStatsStorage(sqliteStorage)
	p := proxy.New(cfg, statsAdapter, sqliteStorage, deviceID)

	// Create HTTP mux
	mux := http.NewServeMux()

	// Initialize and register Web UI (optional plugin)
	// If webui package is not available, this will be skipped at compile time
	if err := registerWebUI(mux, cfg, p, sqliteStorage); err != nil {
		logger.Warn("Web UI not available: %v", err)
	} else {
		logger.Info("Web UI available at /ui/")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartWithMux(mux)
	}()

	logger.Info("Osante Proxy headless API listening on %s:%d (data dir: %s, db: %s)", proxy.ResolveBindHost(), cfg.GetPort(), dataDir, dbPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("Received signal %s, shutting down", sig.String())
		if err := p.Stop(); err != nil {
			logger.Warn("Graceful shutdown failed: %v", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Proxy server stopped with error: %v", err)
			os.Exit(1)
		}
	}

	logger.Info("Osante Proxy stopped")
}

func resolveDataDir() string {
	if dir := os.Getenv("OSANTE_DATA_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".Osante")
	}
	return "/data"
}

func loadConfig(sqliteStorage *storage.SQLiteStorage) (*config.Config, error) {
	adapter := storage.NewConfigStorageAdapter(sqliteStorage)
	cfg, err := config.LoadFromStorage(adapter)
	if err != nil {
		logger.Warn("Failed to load config from storage, using default: %v", err)
		cfg = config.DefaultConfig()
		if saveErr := cfg.SaveToStorage(adapter); saveErr != nil {
			logger.Warn("Failed to persist default config: %v", saveErr)
		}
	}

	// Seed a default endpoint when none are configured to avoid boot failure
	if len(cfg.Endpoints) == 0 {
		logger.Warn("No endpoints found; seeding a default endpoint")
		cfg.Endpoints = config.DefaultConfig().Endpoints
		if saveErr := cfg.SaveToStorage(adapter); saveErr != nil {
			logger.Warn("Failed to persist seeded endpoint: %v", saveErr)
		}
	}

	// Migrate persisted ports that are known-bad defaults:
	//
	//   3000  - the original default; fights every other dev service on :3000.
	//   52710 - the previous default. It sits in the Windows dynamic port range,
	//           where Hyper-V / WSL / Docker NAT reserve shifting 100-port blocks;
	//           landing in one makes bind fail with WSAEACCES even though nothing
	//           is listening. config.DefaultPort is below that range.
	//
	// Either can be kept explicitly with -port / OSANTE_PORT.
	if legacy := cfg.GetPort(); legacy == 3000 || legacy == 52710 {
		logger.Warn("Migrating persisted port %d → %d (set -port %d or OSANTE_PORT=%d to keep the old port)",
			legacy, config.DefaultPort, legacy, legacy)
		cfg.UpdatePort(config.DefaultPort)
		if saveErr := cfg.SaveToStorage(adapter); saveErr != nil {
			logger.Warn("Failed to persist migrated port: %v", saveErr)
		}
	}
	return cfg, nil
}

// applyEnvOverrides applies OSANTE_* environment overrides on top of the stored
// configuration.
//
// Precedence: -port flag > OSANTE_PORT > value saved in the database. The flag
// wins because it calls cfg.LockPort(), and UpdatePort is a no-op on a locked
// port — that used to make OSANTE_PORT silently disappear, so say so out loud.
func applyEnvOverrides(cfg *config.Config) {
	if portStr := os.Getenv("OSANTE_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			if cfg.IsPortLocked() {
				logger.Warn("Ignoring OSANTE_PORT=%d: port is locked to %d by the -port flag", port, cfg.GetPort())
			} else {
				cfg.UpdatePort(port)
			}
		} else {
			logger.Warn("Invalid OSANTE_PORT value %q: %v", portStr, err)
		}
	}

	if levelStr := os.Getenv("OSANTE_LOG_LEVEL"); levelStr != "" {
		if level, err := strconv.Atoi(levelStr); err == nil {
			cfg.UpdateLogLevel(level)
		} else {
			logger.Warn("Invalid OSANTE_LOG_LEVEL value %q: %v", levelStr, err)
		}
	}
}

func setLogLevels(level int) {
	if level < 0 {
		return
	}
	logger.GetLogger().SetMinLevel(logger.LogLevel(level))
	logger.GetLogger().SetConsoleLevel(logger.LogLevel(level))
}

// migrateAllEndpointsToTokenPool walks every endpoint in the DB and, if its
// auth mode is anything other than token_pool, switches it to token_pool. The
// previous apiKey (if any) is seeded as the first pool token so the endpoint
// stays usable without a manual import step.
func migrateAllEndpointsToTokenPool(s *storage.SQLiteStorage) error {
	endpoints, err := s.GetEndpoints()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range endpoints {
		ep := &endpoints[i]
		if config.IsTokenPoolAuthMode(ep.AuthMode) {
			continue
		}
		previousKey := strings.TrimSpace(ep.APIKey)
		ep.AuthMode = config.AuthModeTokenPool
		ep.APIKey = ""
		ep.UpdatedAt = now
		if err := s.UpdateEndpoint(ep); err != nil {
			logger.Warn("Failed to migrate endpoint %s to token_pool: %v", ep.Name, err)
			continue
		}
		if previousKey == "" {
			continue
		}
		existing, err := s.GetEndpointCredentials(ep.Name)
		if err != nil {
			logger.Warn("Failed to read credentials for %s during migration: %v", ep.Name, err)
			continue
		}
		if len(existing) > 0 {
			continue
		}
		cred := &storage.EndpointCredential{
			EndpointName: ep.Name,
			ProviderType: "api_key",
			AccountID:    "legacy-api-key",
			AccessToken:  previousKey,
			Status:       "active",
			Enabled:      true,
			Remark:       "Migrated from endpoint apiKey",
		}
		if err := s.SaveEndpointCredential(cred); err != nil {
			logger.Warn("Failed to seed token pool for %s during migration: %v", ep.Name, err)
			continue
		}
		logger.Info("Migrated endpoint %s to token_pool (seeded api key as first token)", ep.Name)
	}
	return nil
}
