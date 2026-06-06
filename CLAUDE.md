# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project overview

**Osante Proxy** — personal open-source fork of upstream [ccNexus](https://github.com/lich0821/ccNexus), stripped down to the server mode and tailored for a single-user local setup.

Core features:

- Multi-endpoint rotation with automatic failover
- Token Pool only (single `api_key` mode removed from the UI; legacy endpoints auto-migrate on startup)
- HTTP 402 "usage limit reached" failover — token-level cooldown inside the pool, endpoint-level cooldown only when no pool is in play
- API format conversion (Claude / OpenAI / OpenAI-Responses / Gemini)
- English-only web UI, no auth (loopback only by design)
- In-memory log ring exposed at `/api/logs` + Logs tab in the UI

The headless server lives in `cmd/server/`. There is no desktop build in this fork (the upstream Wails app was removed).

## Common dev commands

### Build / run

```bash
cd cmd/server
go build -ldflags="-s -w" -o osante-proxy.exe .
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

```
cmd/server/         headless HTTP server entry + webui (embedded vanilla-JS admin)
internal/
  proxy/            HTTP proxy core, failover loop, token-pool rotation, usage-limit handling
  transformer/      Claude ↔ OpenAI ↔ Gemini conversion (streaming + non-streaming)
  storage/          SQLite (WAL) persistence: endpoints, credentials, stats, app config
  config/           configuration types + storage adapter
  logger/           leveled logging with an in-memory ring buffer (1000 entries)
  session/          per-session context helpers
  terminal/         terminal feature detection (used by streaming output)
  tokencount/       token counting helpers
```

### Key components

- **Proxy** (`internal/proxy/proxy.go`, `proxy_request.go`) — manages endpoints, tracks current endpoint and per-endpoint runtime state. The request loop retries either on the same endpoint (token pool exhaustion) or the next endpoint (whole-endpoint cooldown).
- **Usage-limit handling** (`internal/proxy/usage_limit.go`, `proxy_request.go::handlePaymentRequired`) — HTTP 402 + body containing "usage limit" puts a token (in token-pool mode) or the endpoint (otherwise) into cooldown. Reset time is parsed from the body (UTC+8-aware), default fallback 5 hours.
- **Storage** (`internal/storage/sqlite.go`) — SQLite in WAL mode. Stores endpoints, credentials (token pool), usage stats, and app config.

### Key paths

- Database: `~/.Osante/osante.db`
- Default port: `52710` (legacy `3000` configs auto-migrate)
- Auth modes: `token_pool` only — the UI doesn't expose any other

## API routes

The proxy serves:

- `/` — main proxy route
- `/v1/messages/count_tokens` — token counting
- `/v1/models` — cached model list
- `/health` — health check
- `/stats` — stats data

Web admin / JSON API routes are under `/api/...` and `/ui/`.

## Environment variables

| Var                | Default                       |
|--------------------|-------------------------------|
| `OSANTE_PORT`      | `52710`                       |
| `OSANTE_DATA_DIR`  | `~/.Osante`                   |
| `OSANTE_DB_PATH`   | `$OSANTE_DATA_DIR/osante.db`  |
| `OSANTE_LOG_LEVEL` | `1` (INFO)                    |

The admin API and the web UI are unauthenticated — BasicAuth has been removed completely.

## Dependencies

- Go 1.24+
- SQLite via `modernc.org/sqlite` (pure Go)
