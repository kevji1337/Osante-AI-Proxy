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
| `OSANTE_BIND`      | `127.0.0.1` (loopback only)   |
| `OSANTE_DATA_DIR`  | `~/.Osante`                   |
| `OSANTE_DB_PATH`   | `$OSANTE_DATA_DIR/osante.db`  |
| `OSANTE_LOG_LEVEL` | `1` (INFO)                    |

The admin API and the web UI are unauthenticated — BasicAuth has been removed
completely. The default `OSANTE_BIND=127.0.0.1` enforces loopback-only; set
`OSANTE_BIND=0.0.0.0` (or `::`) only if you explicitly want LAN access. The
proxy logs a WARN on non-loopback binds.

## Dependencies

- Go 1.24+
- SQLite via `modernc.org/sqlite` (pure Go)

## Obsidian Memory

> **MUST-DO:** every read/write of project memory goes through the
> **`obsidian` skill** (invoke via the `Skill` tool). Do **not** shell out
> to `obsidian ...` directly and do **not** edit files under
> `C:\programming\Obsidian Memories\Osante Proxy\` with `Write` / `Edit`.
> All updates land under the vault folder **`Osante Proxy/`** — never write
> Osante notes anywhere else in the vault.

Project memory lives in the Obsidian vault `Obsidian Memories` under the folder
`Osante Proxy/`. Access it through the **`obsidian` skill** (invoke via the
`Skill` tool, not by shelling out to `obsidian` directly — the skill loads the
full command reference and conventions). The skill talks to a running Obsidian
desktop instance via its plugin.

### Files maintained in the vault

| File | Purpose |
|---|---|
| `Osante Proxy/context.md` | Project overview: stack, entry points, key modules, current state. Update when architecture shifts. |
| `Osante Proxy/decisions.md` | Decision log — one entry per non-trivial choice, ISO date prefix. |
| `Osante Proxy/todo.md` | Open tasks and questions carried between sessions. |
| `Osante Proxy/daily/Proxy-YYYY-MM-DD.md` | Per-session journal — what was done, what's pending. |

### Session protocol

**At the start of every session:** invoke the `obsidian` skill, then read the
context file:

```
obsidian vault="Obsidian Memories" read path="Osante Proxy/context.md"
```

If the file doesn't exist, create it with a starter overview via the same
skill (`create path="Osante Proxy/context.md" content="..."`). Optionally also
read `todo.md` and the latest `daily/*.md` to pick up unfinished threads.

**At the end of a non-trivial session:** append the summary via the
`obsidian` skill — `append path="Osante Proxy/daily/Proxy-<сегодня>.md"
content="..."` — and update `context.md` / `decisions.md` if new
architectural decisions were made. If new open questions surfaced, add
them to `todo.md`. Create the daily file first with `create` if it
doesn't exist yet.

### Error handling

The skill needs the Obsidian desktop app to be running. If a command returns
"Obsidian is not running" or connection refused:

- **Do not fail the session.** Continue with whatever the user asked for.
- **Surface the error once,** so the user can launch Obsidian if they want
  memory updated. Do not retry silently.
- Missed updates can be reapplied manually at the start of the next session.

### Shell quoting gotcha

When passing markdown bodies to `create` / `append`, **never** inline a large
content string directly in a bash heredoc/command — backticks and `$(...)`
patterns in the markdown will be interpreted by the shell. Instead:

1. Write the content to a temp file (or directly into the vault path on disk).
2. Use the skill to verify with `read` / `file` afterwards.

The vault lives on disk at `C:\programming\Obsidian Memories\`, so writing
files there directly is a valid alternative for bulk content.
