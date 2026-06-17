# Osante Proxy

> Run **Claude Code** through your **GitLab Duo** subscription. Also: rotate token pools, fail over on rate limits, and bridge OpenAI / Gemini for Codex CLI — all from one local proxy with a small web UI.

[![CI](https://github.com/kevji1337/Osante-AI-Proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/kevji1337/Osante-AI-Proxy/actions/workflows/ci.yml)
[![Release](https://github.com/kevji1337/Osante-AI-Proxy/actions/workflows/build.yml/badge.svg)](https://github.com/kevji1337/Osante-AI-Proxy/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#downloads)

---

## What this is

Osante Proxy is a local HTTP proxy that sits between AI-powered developer tools and the upstream providers. It speaks the **Anthropic `/v1/messages`** wire format on the front side, and translates to whatever the backend wants on the other side: Anthropic, OpenAI / OpenAI Responses, Google Gemini — and now **GitLab Duo Chat Workflow**.

The flagship feature is the **GitLab Duo transformer**: a clean-room implementation of the same WebSocket-backed Workflow protocol that the official `duo` CLI uses, so Claude Code (which natively only speaks Anthropic) can drive your GitLab Duo subscription as if it were Claude. Conversation history, system prompts, model selection (Claude / Sonnet / Opus / Gemini / GPT-5.x variants exposed by GitLab Duo), per-word SSE streaming and Esc-to-cancel all work end-to-end.

Beyond GitLab Duo, the proxy also handles the boring-but-essential stuff: a **token pool** with LRU rotation and automatic cooldown on `HTTP 402 Usage limit reached`, hard deactivation of tokens that hit `403 insufficient credits`, in-flight request deduplication, transient-error retries, and a no-auth loopback-only admin UI.

It's a personal fork of [ccNexus](https://github.com/lich0821/ccNexus), trimmed and reworked. Not affiliated with the upstream project — this is "the build I actually run", kept here so the parts I rely on don't drift.

## Highlights

- **GitLab Duo support for Claude Code.** New `gitlabduo` transformer drives the real `/api/v4/ai/duo_workflows/*` REST + WebSocket protocol. JSON `ClientEvent` framing, `agent_privileges` workflow, model picker, per-word SSE streaming, Esc/Ctrl+C cancellation, dedup, retry. See [GitLab Duo setup](#gitlab-duo-setup).
- **Token Pool is the only auth mode.** LRU rotation across a pool of tokens. Automatic cooldown on `402` rate limits with reset time parsed from the response (fallback 5 hours). Permanent deactivation on `insufficient credits` 403s with the reason surfaced in the UI.
- **Multi-provider transformers.** Anthropic native, OpenAI / OpenAI Responses, Gemini, and GitLab Duo — all behind the same Anthropic-shaped front door, so Claude Code or Codex CLI don't need to know which one is in use.
- **Embedded web admin.** No auth, loopback-only by design. Add endpoints, paste token pools, fetch model lists, test connectivity, watch live logs, see per-token usage and cooldown countdowns.
- **Per-word SSE streaming** so output renders progressively in Claude Code's terminal even when the upstream replies in one shot (e.g. GitLab Duo).
- **Pure Go, no CGO.** SQLite via `modernc.org/sqlite`. Single static binary on every platform.

## Quick start

### 1. Download or build

**Download** a pre-built binary for your platform from [Releases](https://github.com/kevji1337/Osante-AI-Proxy/releases), or build from source:

```bash
git clone https://github.com/kevji1337/Osante-AI-Proxy.git
cd Osante-AI-Proxy/cmd/server
go build -ldflags="-s -w" -o osante-proxy .
```

Requires Go **1.24+**.

### 2. Run

```bash
./osante-proxy
```

Listens on `127.0.0.1:52710` by default. Data dir: `~/.Osante/`, db: `~/.Osante/osante.db`.

### 3. Open the web admin

<http://127.0.0.1:52710/ui/>

1. Click **Add Endpoint**, pick a transformer (e.g. `gitlabduo`), set the URL (e.g. `https://gitlab.com`) and model.
2. Click **Token Pool** on the endpoint row and paste your tokens — one per line or as JSON.
3. Click **Test** to confirm the token works.

### 4. Wire up your tool

**Claude Code:**

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:52710
export ANTHROPIC_AUTH_TOKEN=anything
claude
```

A ready-made Windows launcher script is at `C:\tweaks\osante.bat` (used for development).

**Codex CLI** (`~/.codex/config.toml`):

```toml
model_provider = "osante"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.osante]
name = "Osante Proxy"
base_url = "http://127.0.0.1:52710/v1"
wire_api = "responses"
```

## GitLab Duo setup

GitLab Duo is the headline integration — here's the full setup.

### Prerequisites

- A GitLab account on **gitlab.com** (or a self-managed instance) with **GitLab Duo Agent Platform access** and **available Duo credits**. The `chat` workflow consumes credits per turn.
- A **Personal Access Token** with scope `api`.

### Configure

1. In the web UI click **Add Endpoint**:
   - **Name**: `gitlabduo` (or anything — just remember it)
   - **Transformer**: `gitlabduo`
   - **API URL**: `https://gitlab.com` (or your self-managed root)
   - **Model**: click **Fetch Models** and pick one (e.g. `Claude Opus 4.7 - Anthropic`). The proxy normalises display labels to GitLab's `gitlab_identifier` format (`claude_opus_4_7`, `claude_sonnet_4_6`, …) on the wire.
2. Click **Token Pool** on the endpoint and paste your PAT(s). Add multiple PATs from different accounts if you want credit failover.
3. Wire up Claude Code with the env vars above.
4. Run `claude` and chat — text streams back word by word.

### How it works under the hood

```
Claude Code  ──(Anthropic /v1/messages)──▶  Osante Proxy
                                                  │
                                                  │  1. POST /api/v4/ai/duo_workflows/workflows
                                                  │     (goal, environment, agent_privileges, …)
                                                  │
                                                  │  2. POST /api/v4/ai/duo_workflows/direct_access
                                                  │     → gitlab_rails.token + duo_workflow_service.token
                                                  │
                                                  │  3. wss://gitlab.com/api/v4/ai/duo_workflows/ws
                                                  │     ←→ JSON ClientEvent / Action frames
                                                  │       (startRequest → checkpoints → INPUT_REQUIRED)
                                                  │
                                                  ▼
                                          extract last final
                                          agent message from
                                          channel_values.ui_chat_log
                                                  │
                                                  ▼
                                          word-level Anthropic SSE
                                                  │
Claude Code ◀──(content_block_delta stream)──────┘
```

The wire format was reverse-engineered from the official `duo` binary (see `gitlab-docs.md` and `gitlab-proxy.md` in the repo for protocol notes).

### Caveats

- **No tool use.** GitLab Duo's `chat` workflow returns plain text. Claude Code's file-edit / bash-tool capabilities will not work — the assistant can describe what it would do, but it cannot actually invoke tools through the GitLab Duo backend.
- **16 384-char `goal` limit.** Long conversation histories are truncated from the head (oldest first), keeping the tail (most recent turns). Use Claude Code's `/compact` for very long sessions.
- **One credit per turn.** Each Claude Code message creates a fresh GitLab Duo workflow.

## Environment variables

| Var                 | Default               | Notes                             |
|---------------------|-----------------------|-----------------------------------|
| `OSANTE_PORT`       | `52710`               | Listen port                       |
| `OSANTE_DATA_DIR`   | `~/.Osante`           | Data directory                    |
| `OSANTE_DB_PATH`    | `$OSANTE_DATA_DIR/osante.db` | SQLite db path             |
| `OSANTE_LOG_LEVEL`  | `1` (INFO)            | `0` DEBUG / `1` INFO / `2` WARN / `3` ERROR |

CLI `-port N` locks the port so it can't be changed via the API.

## Endpoints and routes

Proxy (Anthropic-shaped):

- `POST /v1/messages` — main entry point
- `POST /v1/messages/count_tokens`
- `GET  /v1/models`
- `GET  /health`
- `GET  /stats`

Admin (loopback-only, no auth):

- `GET  /ui/` — web UI
- `*    /api/...` — JSON admin API used by the UI

## Layout

```
cmd/server/             headless HTTP server + embedded admin Web UI
internal/proxy/         proxy core, request pipeline, GitLab Duo handler
internal/transformer/   per-provider request/response transformers
internal/storage/       SQLite-backed endpoints / credentials / stats
internal/{config,logger,session,terminal,tokencount}
public/                 static assets (icon, etc.)
```

## Downloads

Pre-built binaries for **Windows**, **Linux** and **macOS (Intel & Apple Silicon)** are published on every tagged release: [Releases](https://github.com/kevji1337/Osante-AI-Proxy/releases).

To cut a new release, push a tag matching `v*`:

```bash
git tag v0.1.0
git push --tags
```

GitHub Actions builds all four platforms and attaches them to the release automatically.

## Development

```bash
go build ./...
go test ./...
go vet ./...
golangci-lint run
```

Logs land in the in-memory ring buffer and are visible in the **Logs** tab of the web UI, or via `GET /api/logs`.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the short version. The codebase tries to stay small, dependency-light, pure-Go.

If you're a maintainer of a project that quietly depends on a transformer-style proxy and the GitLab Duo integration is useful to you, drop a star ⭐ — it helps with discovery.

## Acknowledgements

- [ccNexus](https://github.com/lich0821/ccNexus) by @lich0821 — the original Anthropic-shaped proxy this project forked from.
- [Anthropic](https://www.anthropic.com/) for Claude Code's clean SSE/messages protocol.
- [GitLab](https://gitlab.com/) for the Duo Workflow API (and for being permissive about external clients).

## License

[MIT](LICENSE).
