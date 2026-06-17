# Contributing to Osante Proxy

Thanks for your interest in contributing! This is a small, dependency-light,
pure-Go project — the bar for PRs is correctness, readability, and not adding
weight that doesn't pay for itself.

## Quick checklist

Before opening a PR, please:

1. **Run the standard checks.**
   ```bash
   go vet ./...
   go test ./... -count=1
   golangci-lint run
   gofmt -s -w .
   ```
2. **Keep dependencies pure-Go.** No CGO. Storage is SQLite via
   `modernc.org/sqlite`. Avoid new heavy dependencies unless there is a strong
   case.
3. **Match the existing style.** Look at neighbouring files for naming,
   logging, error handling and comment density.
4. **Update tests.** Anything that touches request parsing, the GitLab Duo
   protocol, or the token pool should ship with a test next to it
   (`*_test.go`).
5. **Update `CHANGELOG.md`.** Add a bullet under `[Unreleased]` describing
   your change in user-facing terms.

## How to report a bug

Please include:

- Your OS and architecture (`os/arch`).
- The Osante Proxy version (`./osante-proxy --version` if available, or the
  tag/commit you built from).
- The transformer in use (`gitlabduo`, `openai`, `gemini`, …) and the
  endpoint configuration (redact tokens).
- Relevant logs. The web UI's **Logs** tab and `GET /api/logs` capture the
  in-memory ring buffer.

A short reproducer beats a long description.

## How to propose a feature

For anything non-trivial, open an issue first to discuss the design. For
small, self-contained improvements (bug fixes, log polish, additional
transformer fields, etc.) a PR is fine without prior discussion.

## Code layout (where things live)

```
cmd/server/             headless HTTP server + embedded admin Web UI
internal/proxy/         proxy core, request pipeline, GitLab Duo handler
internal/transformer/   per-provider request/response transformers
  ├── cc/               Claude Code-shaped front-side variants
  ├── cx/               Codex CLI-shaped front-side variants
  └── convert/          common conversion helpers
internal/storage/       SQLite-backed endpoints / credentials / stats
internal/{config,logger,session,terminal,tokencount}
public/                 static assets (icon, etc.)
```

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
