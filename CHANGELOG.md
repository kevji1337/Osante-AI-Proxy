# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project loosely
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

An audit-driven pass over the whole fork: one critical failover bug, the
unauthenticated admin API's cross-site exposure, several data races, and ~2100
lines of unreachable code. Everything below is verified by tests or a live run.

### Breaking

- **Default port is now `12710`** (was `52710`). Ports above 49152 are Windows'
  dynamic range, where Hyper-V / WSL / Docker NAT reserve 100-port blocks that
  move between reboots; landing inside one makes `bind` fail with WSAEACCES
  ("socket in a way forbidden by its access permissions") while `netstat` shows
  nothing listening. Persisted `3000` and `52710` are migrated automatically on
  startup and can still be kept with `-port` / `OSANTE_PORT`. **Clients must
  update `ANTHROPIC_BASE_URL`.**
- **`x-api-key`, `Authorization` and `Cookie` from the client are no longer
  forwarded upstream.** Only an allowlist of headers is relayed. Previously a
  caller's `ANTHROPIC_API_KEY` travelled to Google or OpenAI on the gemini and
  openai paths, which set their own auth without removing the client's.

### Security

- Admin API rejects cross-site and DNS-rebound requests (`LocalOnlyMiddleware`).
  It stays login-free, but Host must be loopback, `Sec-Fetch-Site` must be
  first-party when present, `Origin` must be loopback, and mutating verbs must
  declare `application/json` — which is what defeats the CORS simple-request
  bypass that let any visited page POST to `/api/endpoints`. Non-browser clients
  are unaffected. `Access-Control-Allow-Origin: *` removed from `/api/events`.
- Full request payloads are no longer written to the WARN log, which is served by
  the unauthenticated `/api/logs` and pasted into bug reports. Set
  `OSANTE_DEBUG_FILE=<path>` to record them deliberately.
- Request bodies are bounded (64 MiB proxy, 8 MiB credential import).
- Admin-initiated outbound calls validate the URL scheme and refuse to dial
  link-local (cloud metadata), unspecified and multicast addresses. Loopback and
  RFC1918 stay reachable on purpose — a local model server is a supported setup.
- The model name is escaped before being interpolated into the gemini path.
- `RecoveryMiddleware` is finally mounted, so a panic returns 500 instead of
  tearing down the connection.

### Fixed

- **An upstream 200 whose body could not be transformed was returned to the
  client as HTTP 200 with an empty body** — a silent success, with no failover and
  no recorded error. It now fails over to the next endpoint.
- **A client that hung up did not stop anything.** Attempts ran under a context
  derived from `context.Background()`, so pressing Esc in Claude Code left the
  retry loop walking endpoints and rotating tokens — up to
  `len(endpoints)*2 + (usable tokens - 1)` upstream calls at 90s each, spending
  real quota on a reply nobody would read. A cancelled request also no longer
  marks the endpoint and its token as failing.
- The endpoint resolver kept answering from a pre-`UpdateConfig` snapshot, so an
  endpoint added or renamed through the admin API was unaddressable via
  `X-CCN-Endpoint` / `?endpoint=` / `@endpoint` prefixes until restart.
- Data races on the config pointer in both the proxy and the admin handler; the
  getters also handed out shared mutable structs. Now `atomic.Pointer` plus copies.
- Renaming an endpoint silently did nothing: the UPDATE addressed the row by its
  *new* name, so every edit in the same request was dropped. Renames now address
  by id in a transaction and carry credentials, usage and daily stats along.
- `POST /api/endpoints/switch` reported success without switching anything.
  `/api/endpoints/current` and the SSE feed reported "first enabled" instead of the
  real rotation index.
- `busy_timeout` and `synchronous` were applied to a single pooled SQLite
  connection, leaving the rest at `busy_timeout=0` (instant SQLITE_BUSY under
  concurrent writes) and `synchronous=FULL`.
- The token pool did not rotate: selection did not claim the token, so parallel
  requests all picked the same one and burned its quota first.
- Any transport error un-quarantined a token that a previous 401 had marked
  invalid.
- `/v1/models` never authenticated in token-pool mode and always fell back to the
  built-in list; it also probed endpoints serially with no time budget.
- Toast notifications had no CSS at all — every one of ~40 call sites reported into
  nothing.
- An API key pasted while *editing* an endpoint was accepted, wiped and never
  stored. Both create and edit now add it to the token pool, idempotently.
- Malformed upstream SSE events were discarded indistinguishably from
  "nothing to emit"; they are now reported.
- `GetEndpointTotalStats` failed for any endpoint without stats; `/stats` always
  returned `{}`; the `done` trace phase was never recorded; log/error truncation
  cut multi-byte runes in half.

### Changed

- Go 1.27, refreshed dependencies, and `nhooyr.io/websocket` → the maintained
  `github.com/coder/websocket`.
- CI actually lints now: `golangci-lint-action@v9` (first Node 24 release) with
  golangci-lint v2.13.1 and a v2 config. Added a gofmt gate, `-race`,
  `govulncheck` and a coverage artifact; every action moved to its Node 24 major.
  `errorlint`, `nilerr` and `unconvert` enabled after clearing their backlog.
- `start.bat`: detects reserved and occupied ports before starting, waits for
  `/health` before opening the browser, no longer forces DEBUG on every launch,
  and takes `--debug`, `--debug-file`, `--no-build`, `--no-ui`, `--port`, `--help`.
- Dockerfile builds with `CGO_ENABLED=0` (this project uses pure-Go SQLite, so
  gcc and sqlite-dev were never needed) and no longer hard-codes a regional Go
  proxy.
- One SQLite write per proxied request instead of four.

### Added

- `OSANTE_DEBUG_FILE` — records full request and response bodies to a file.
  `Logger.EnableDebugFile` previously had no caller, so all 23 `DebugLog` sites
  were dead code.
- **Console-less operation on Windows.** `start-background.bat` builds a
  GUI-subsystem binary and starts it detached, so the proxy runs entirely behind
  the web UI. `POST /api/actions/shutdown` and the dashboard's SHUT DOWN button
  stop it, since there is no terminal to Ctrl+C; `start-background.bat --stop`
  does the same from a script.

### Removed

- `internal/terminal` (~540 lines, 11 `exec.Command` calls built from config
  strings) and `internal/session` (~690 lines) — neither was imported anywhere.
- The storage backup/merge/archive half, which ran `ATTACH DATABASE` on an
  arbitrary pooled connection.
- The reflection layer behind `StatsStorage` (seven `FieldByName` lookups per
  recorded stat, twice per request) and `storage.Storage`, a ~30-method interface
  with no implementations.
- `internal/transformer/tool_chain.go` — 155 lines making their own recursive HTTP
  calls with no context and no caller.

### Internal

- Integers above 2^53 in a request body were silently rounded: three separate
  rewrites each decoded the body into `map[string]interface{}`, which turns every
  number into a float64. They now run as one pass with `json.Decoder.UseNumber`.
- Test coverage where the audit had changed code and there was none:
  `internal/config` 0 → 55%, `internal/logger` 0 → 85%, `internal/tokencount`
  0 → 65%, `cx/chat` and `cx/responses` 0 → 64%.
- The proxy-client cache is bounded, closing idle connections when it clears.
- The admin API's Test and Fetch Models calls carry the browser request's context,
  so closing the tab stops the probe instead of leaving it to time out.
- More coverage on the paths this pass touched: the 402 failover branches (token
  cooldown vs endpoint cooldown), the retry budget, and the endpoint/credential
  admin handlers — `internal/proxy` 21.5% → 35.1%, `cmd/server/webui/api`
  12.5% → 31.7%.

## [0.1.0] — 2026-06-17

The first tagged release of Osante Proxy. Headline feature: **GitLab Duo
support for Claude Code** via a reverse-engineered Workflow protocol.

### Added

- **GitLab Duo transformer (`gitlabduo`).** Implements the full `/api/v4/ai/duo_workflows/*`
  REST + WebSocket protocol the official `duo` CLI uses, including:
  - JSON `ClientEvent` framing over WebSocket (`startRequest`, `heartbeat`).
  - `agent_privileges`, `environment: "ide"`, `pre_approved_agent_privileges`
    and the rest of the parameters required for `chat` workflows.
  - Conversation context preserved by serialising the Anthropic `messages[]`
    history (plus a condensed system prompt) into the workflow `goal`.
  - Per-word Anthropic SSE streaming so Claude Code renders progressively.
  - Esc/Ctrl+C cancels the in-flight workflow instead of burning a Duo credit.
  - In-flight request deduplication for parallel Claude Code retries
    (SHA-256 of endpoint+token+goal).
  - One automatic retry on transient WS / network errors.
  - Automatic deactivation of tokens that hit `403 insufficient credits`, with
    the reason surfaced in the UI's Token Pool table.
  - Model picker (Fetch Models) exposing the full GitLab Duo catalogue,
    normalised to the snake_case `gitlab_identifier` format on the wire.
  - Test button hitting `GET /api/v4/version` as a lightweight health check.
- **Custom `.exe` icon and version metadata** via `goversioninfo`.
- **CI / Release workflows** building for Linux, macOS (Intel & ARM64) and
  Windows, with `softprops/action-gh-release` attaching binaries to GitHub
  Releases on every `v*` tag.

### Changed

- Documentation rewritten to put GitLab Duo front and centre; added a protocol
  diagram and setup walkthrough.
- Token Pool failure handling reworked so transient and permanent failures are
  distinguished and logged accordingly.

### Notes

- This release inherits the codebase from [ccNexus](https://github.com/lich0821/ccNexus)
  by @lich0821 with substantial reworks (English-only UI, token pool as the
  sole auth mode, usage-limit failover, log viewer, default port 52710, etc.)
  carried over from the unreleased fork history.

[Unreleased]: https://github.com/kevji1337/Osante-AI-Proxy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/kevji1337/Osante-AI-Proxy/releases/tag/v0.1.0
