# Osante Proxy

A personal open-source fork of [ccNexus](https://github.com/lich0821/ccNexus), trimmed down and reworked for my own setup. Not affiliated with the upstream project — this is "the build I actually run", kept here so the bits I rely on don't drift.

A local HTTP proxy for **Claude Code** and **Codex CLI** that rotates a pool of API tokens, fails over on rate limits, and exposes a small web admin.

## What's different from upstream

- **Token Pool is the only auth mode.** The single-key `api_key` mode is gone from the UI. Legacy endpoints auto-migrate to a pool on first start (the existing key becomes the first token).
- **Usage-limit failover.** When an upstream returns `HTTP 402 "Usage limit reached"`, the offending token is put on cooldown (parsed from the reset time, fallback 5h) and the same request is retried on the next token without the client seeing a failure. Whole-endpoint cooldown only kicks in when no pool is in play.
- **English-only web UI.** Chinese locale, language switcher, and Basic Auth have all been removed. The admin API is open on loopback by design.
- **Logs tab.** The in-memory log ring buffer is exposed via `GET /api/logs` and rendered as a live tail in the web UI.
- **Per-token Test button + live cooldown countdown** inside the Token Pool modal.
- **Default port `52710`** instead of `3000` (old `3000` configs are auto-migrated on first launch).
- **Quieter logs.** Generic gateway 404s and tool-call cleanup failures on empty bodies are demoted to `DEBUG`.

## Quick start

### Build

```bash
cd cmd/server
go build -ldflags="-s -w" -o osante-proxy.exe .
```

Requires Go 1.24+.

### Run

```bash
./osante-proxy.exe
```

Listens on `127.0.0.1:52710` by default. Data dir: `~/.Osante/`, db: `~/.Osante/osante.db`.

### Web admin

Open <http://127.0.0.1:52710/ui/>.

1. Add an endpoint (URL + transformer + model).
2. Click **Token Pool** on the endpoint row and paste tokens either as JSON or one per row via the simple form.
3. Done. Requests rotate through the pool LRU-style; tokens that hit usage limits sit out their cooldown automatically.

### Wire up Claude Code

```
ANTHROPIC_BASE_URL=http://127.0.0.1:52710
ANTHROPIC_AUTH_TOKEN=anything
```

A ready-made launcher script is at `C:\tweaks\osante.bat`.

### Wire up Codex CLI

`~/.codex/config.toml`:

```toml
model_provider = "osante"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.osante]
name = "Osante Proxy"
base_url = "http://127.0.0.1:52710/v1"
wire_api = "responses"
```

## Environment variables

| Var                 | Default               | Notes                             |
|---------------------|-----------------------|-----------------------------------|
| `OSANTE_PORT`       | `52710`               | Listen port                       |
| `OSANTE_DATA_DIR`   | `~/.Osante`           | Data directory                    |
| `OSANTE_DB_PATH`    | `$OSANTE_DATA_DIR/osante.db` | SQLite db path             |
| `OSANTE_LOG_LEVEL`  | `1` (INFO)            | `0` DEBUG / `1` INFO / `2` WARN / `3` ERROR |

CLI `-port N` locks the port so it can't be changed via the API.

## Layout

```
cmd/server/      headless HTTP server + embedded admin Web UI
internal/        proxy core, transformers, storage, logger, config
```

## License

[MIT](LICENSE), same as upstream.
