# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project loosely
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
