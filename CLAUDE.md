# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project overview

**Osante Proxy** is a smart API endpoint rotation proxy designed for Claude Code and Codex CLI.

**Core features:**
- Multi-endpoint rotation with automatic failover
- API format conversion (Claude ↔ OpenAI ↔ Gemini)
- Token Pool management (automatic rotation, refresh, failure isolation)
- Real-time stats and monitoring
- WebDAV cloud sync (desktop only)
- Web UI on `/ui/` (English only, no auth)

**Two run modes:**
- **Server mode** (active): headless HTTP API proxy (`cmd/server/`)
- **Desktop mode** (Wails v2 GUI, `cmd/desktop/`) — not the focus of current work

## Common dev commands

### Build / run

```bash
# Build the server
cd cmd/server && go build -ldflags="-s -w" -o osante-proxy.exe .

# Run
cd cmd/server && go run main.go
# or the built binary:
./osante-proxy.exe
```

### Tests

```bash
go test ./... -count=1
go test -v ./internal/proxy/...
go test -v ./internal/transformer/convert/...
```

### Docker

```bash
docker build -f cmd/server/Dockerfile -t osante .
cd cmd/server && docker-compose up -d
```

### Code quality

```bash
go fmt ./...
go vet ./...
go mod tidy
```

## Architecture

### Directory layout

```
Osante-AI-Proxy/
├── cmd/
│   ├── desktop/          # Wails desktop app (not actively maintained here)
│   └── server/           # Headless HTTP server (active)
│       ├── main.go
│       └── webui/        # Embedded web admin (Go + vanilla JS)
└── internal/
    ├── proxy/            # HTTP proxy core + failover + token pool rotation
    ├── transformer/      # API format converters
    ├── storage/          # SQLite-backed persistence
    ├── config/           # Configuration
    ├── webdav/           # WebDAV sync (desktop)
    └── logger/           # Logging with in-memory ring buffer (exposed via /api/logs)
```

### Key components

**Proxy** (`internal/proxy/proxy.go`)
- Manages multiple endpoints, automatic failover.
- Tracks current endpoint, active requests, per-endpoint runtime state (cooldown, last error).
- Pool-aware request loop in `proxy_request.go` retries either on the same endpoint (token pool exhaustion) or the next endpoint (whole endpoint cooldown).

**Usage-limit handling** (`internal/proxy/usage_limit.go`, `proxy_request.go::handlePaymentRequired`)
- HTTP 402 + body containing "usage limit" puts:
  - the **token** into cooldown when the endpoint is in token-pool mode (retry on same endpoint, next token);
  - the **endpoint** into cooldown otherwise (retry on next endpoint).
- Cooldown until reset time parsed from the body (UTC+8 aware), or default 5 hours.

**Transformer** (`internal/transformer/`)
- Converts between Claude, OpenAI (Chat + Response), Gemini.
- Supports streaming/SSE.

**Storage** (`internal/storage/sqlite.go`)
- SQLite in WAL mode.
- Stores endpoints, credentials (token pool), usage stats, app config.

### Key file paths

- Database: `~/.Osante/osante.db` (legacy installs: `~/.ccNexus/ccnexus.db` — auto-fallback for backwards compat)
- Auth modes: `internal/config/config.go` (token_pool is the only mode the server build exposes)
- Proxy routes: `internal/proxy/proxy.go::Start` (`/`, `/v1/messages/count_tokens`, `/v1/models`, `/health`, `/stats`)

## Endpoint configuration

### Auth mode

The server build supports only `token_pool`. The Web UI no longer exposes
the auth-mode selector; new endpoints are created in token-pool mode, and any
legacy `api_key` endpoint is migrated on startup (the existing apiKey becomes
the first token of the pool).

### Transformer types

- `claude` — Claude API
- `openai` — OpenAI Chat API
- `openai2` — OpenAI Response API
- `gemini` — Google Gemini API

## API routes

The proxy serves:

- `/` — main proxy route (all upstream traffic)
- `/v1/messages/count_tokens` — token counting
- `/v1/models` — model list (cached)
- `/health` — health check
- `/stats` — stats data

Web admin / JSON API routes are under `/api/...` and `/ui/`.

## Environment variables

Server mode (preferred names; legacy `CCNEXUS_*` still works as fallback):

- `OSANTE_PORT` — override default port
- `OSANTE_LOG_LEVEL` — log level
- `OSANTE_DB_PATH` — SQLite db path
- `OSANTE_DATA_DIR` — data dir (default `~/.Osante`)
- `OSANTE_BASIC_AUTH_USERNAME` / `OSANTE_BASIC_AUTH_PASSWORD` — basic auth is currently disabled in the server build, kept for forward compat

## Dependencies

- Go 1.24+
- Wails v2 (desktop)
- Node.js 18+ (desktop frontend)
- SQLite (`modernc.org/sqlite`, pure Go)
