# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `workflow import` now remaps node and connection ids that collide with
  ids already used by other workflows (ids are globally unique in the
  store), so multiple examples can be imported into one database; remapped
  ids are reported in `--json` output. Importing a workflow whose nodes
  have no type is rejected instead of silently persisting a broken graph.
- `workflow export` now emits the documented workflow-file format, making
  export → import roundtrips lossless; the legacy export shape is still
  accepted on import.
- Example workflows (`examples/`) fixed and re-validated (all pass
  `workflow validate`); `examples/README.md` quickstart now includes the
  required `workflow activate` step before starting the daemon.
- `node schema core.set` output corrected to match the node's actual
  configuration fields.
- `workflow validate --file` now runs the same legacy-format
  normalization as `workflow import` (legacy `node_type` /
  `source_node_id` keys converted) and defaults unset connection handles
  to `main`, so any file that imports cleanly also validates — including
  the README flagship example, whose connections omit handles.
- Error routing is honest: `on_error=error_branch` with no edge wired to
  the node's `error` handle is reported instead of silently discarding
  the failure output, and runs that continue past per-node failures (via
  `on_error=continue`/`skip`) end `SUCCESS_WITH_ERRORS` rather than
  `SUCCESS`.
- `core.filter` now surfaces evaluation errors from its condition instead
  of passing items through silently.
- Store-level node saves preserve edges: `SaveWorkflowNodes` updates nodes
  in place instead of delete+reinsert (which cascaded deletes through
  `workflow_connections`); the CLI workaround that re-saved connections
  afterwards is removed.
- `workflow list` performance: workflow JSON files are parsed once and
  cached, and a SQLite expression index (migration 024) speeds up
  expression-based lookups.
- Migration healing on upgrade: a Go-side schema reconcile repairs
  drifted SQLite schemas; the vault migrations are renumbered 027/028;
  and the CLI and MCP entry points now run the vault migration, so
  databases from older installs converge without manual steps.
- Executions are stamped with their owning profile as they run, and a
  migration backfills the profile stamp onto existing execution rows.
- Browser-node helper matches more pgrep process-name variants
  (Chromium, Brave, Edge alongside Chrome) and fails fast with a clear
  error when no supported browser is running, instead of hanging.
- GUI: background polling is gated on page visibility and the assistant
  agent scan runs on page open; the legacy duplicate panel is removed;
  an empty active profile is normalized instead of erroring.
- Extension bridge port resilience: the server honors
  `MONOAGENT_EXTENSION_PORT` and the extension tries that port before
  falling back to 9323, so a custom port no longer strands the
  extension.

Repository hygiene and packaging wave: social bot implementations moved
behind the opt-in `social` build tag (default builds exclude them),
documentation restructured around the core workflow engine, repository
hygiene fixes (tracked secrets removed, dead action templates deleted,
license and community files added), and review-driven accuracy fixes to
CLI behavior and docs.

### Added

- `LICENSE` (MIT), `SECURITY.md` (vulnerability reporting, telemetry and
  crash-reporting statement, secrets-vault scope), `CONTRIBUTING.md`, and
  this `CHANGELOG.md`.
- CI workflow (`.github/workflows/ci.yml`): build, vet, and race-enabled
  tests in both default and `-tags social` build modes, plus a Wails job
  that builds the `wails-app/` desktop module.
- MCP server (stdio JSON-RPC 2.0) so AI agents can list/run workflows,
  inspect node schemas, and resolve human-in-the-loop approvals.
- CLI agent-experience improvements: `workflow run --json/--dry-run/--no-wait`,
  `workflow validate`, `node schema`, enriched `--json` output, and granular
  exit codes.
- `workflow run --no-wait` enqueues the run (status `QUEUED`), prints the
  execution id with a hint that a live engine (e.g. `monoagentcli daemon`)
  completes it, and exits 0 immediately — adopted in the docs as the
  agent pattern for fire-and-forget runs. A run that pauses at a
  human-in-the-loop node ends the wait with status `WAITING`, exit 0, and
  a `hint` field pointing at the approval queue.
- Example workflows (`examples/`) and Docker/install distribution files.
- `workflow templates run` now honors `--json`.
- Sandbox resource caps: HTTP node bodies capped at 64 MB by default
  (configurable); `core.code` nodes run with a 30 s default timeout
  (configurable via `timeout_seconds`) and return at most 10,000 items of
  16 MB each per execution — an engine-level memory ceiling is not yet
  enforced by the vendored JS runtime; `system.execute_command` output
  capped at 10 MB per channel (stdout and stderr). Stored outputs are
  persisted in full but display-truncated at 4 KB.
- Webhook server environment overrides: `MONOAGENT_WEBHOOK_ADDR` sets the
  bind address (`host:port`, default `127.0.0.1:9321`) and
  `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS` sets a comma-separated CORS
  allowlist (default: no CORS headers). Docker port mappings for webhook
  triggers now work without host networking.
- Opt-in file-based keyring fallback for headless environments:
  `MONOAGENT_ALLOW_FILE_KEYRING=1` stores the vault key-encryption key in
  a per-profile file `~/.monoagent/vault/.file-keyring-<profileID>`
  (0600) with a loud warning, so `secret add` works in CI/containers;
  without the variable the vault fails closed on machines with no OS
  keyring.
- Node schema coverage completed: every default-build node type now
  resolves a schema — 16 added (`image.*`, `service.reddit`,
  `service.devto`, `service.discord`, `service.bluesky`,
  `service.mastodon`, `service.hashnode`, `service.producthunt`,
  `gemini.chat_session`, `gemini.chat_session_many`).
- GUI (`wails-app`): ImportWorkflow/ExportWorkflow bindings, subprocess
  PID verification, and a cancellable RunNode.
- `update` verifies downloads against the release `SHA256SUMS.txt` and
  hard-fails on a checksum mismatch or a missing checksums file.
- Per-profile encrypted vault: each profile's secrets are sealed with
  their own key-encryption key under per-profile vault folders, entries
  are re-encrypted when they move between profiles, and the file-keyring
  fallback is likewise per profile
  (`~/.monoagent/vault/.file-keyring-<profileID>`).
- Profile folders: the GUI settings gain per-profile folder management.
- Assistant chat sessions: `chat --history-id <session>` persists and
  resumes named sessions.
- Assistant tools (explicit opt-in): `chat --tools monoagent` exposes
  workflow/vault/people/actions/comms tooling to the model;
  `--tools monoagent,runs` additionally opts in to run/execution tools.
  Off by default; the GUI settings toggle mirrors the gate and also
  defaults to off. `get_workflow` output is redacted, delete-class tools
  write sidecar backups, synced message content carries provenance
  fences, and tool-call timeouts derive from the caller's context.

### Changed

- Go module path renamed to `github.com/monoes/mono-agent`.
- README and agent docs restructured around the core workflow engine;
  social-platform actions documented as an opt-in integration for your
  own accounts.
- Social engagement bots (browser automation for social platforms) moved
  behind the opt-in `social` build tag; default builds exclude them.
- Legacy social CLI commands (`message`, `comment`, `search`, `list`,
  `template`) are hidden from default `--help` output; they remain
  invokable and appear in `--help` in `-tags social` builds.
- Crash reporting defaults to local files under `~/.monoagent/crashes/`;
  filing to GitHub requires `MONOAGENT_CRASH_REPORT=1` and the `monomind`
  CLI on PATH (the `npx` fallback was removed).
- `{{ $env.* }}` template access now requires `MONOAGENT_ALLOW_ENV_TEMPLATES=1`.
- Output items are redacted in `--json` and MCP output (values of keys
  such as token/secret/password/authorization/cookie are masked);
  `--full-outputs` opts out on the CLI.
- `secret add` stdin now accepts values with or without a trailing newline.
- Exit codes aligned with behavior: `hil approve`/`hil reject`,
  `secret rm`/`secret update`, and `workflow delete` return 2 on unknown
  ids; a run ending `CANCELLED` exits 1.
- `wails-app` Go module fixed so the desktop app builds as its own module.
- `monoes_apis/` development scripts extracted to the private repository
  `monoes/monoes-apis`; this tree no longer carries them.

### Removed

- Dead browser-action templates for platforms with no live implementation
  (alternativeto, betalist, capterra, facebook, futurepedia, g2,
  indiehackers, lobsters, medium, pinterest, quora, threads, tildes) from
  `data/actions/`.
- Hardcoded deployment credentials and identifiers from
  `monoes_apis/deploy_full.sh` (now required environment variables).
- Message-variant rotation removed from social DM actions.
- Typo simulation removed from humanized typing in social actions; the
  randomized pacing between keystrokes is kept.
- `go-rod/stealth` dependency dropped (unused).

### Security

- `scripts/import_edge_passwords.py` now pipes passwords via stdin to
  `monoagentcli secret add` instead of exposing them as command-line
  arguments (visible in process lists).
- Output redaction (above) keeps credential-shaped values out of `--json`
  and MCP results unless `--full-outputs` is passed.
- Assistant tool surface is gated and value-safe: tools are off unless
  explicitly enabled, vault tools return metadata only (values never
  reach the model), deletes are sidecar-backed, and synced message
  content is provenance-fenced (see SECURITY.md for the residual
  injection risk).

Review-fix rounds 2–4 are itemized in
[docs/plans/2026-08-28-trust-hygiene-record.md](docs/plans/2026-08-28-trust-hygiene-record.md).

## [0.31.0] - 2026-08-25

### Added

- Orgs UI (Phase 3) and an Agents-first rework of the AI settings page.
- `agent.ask` workflow node; `ai.*` nodes deprecated in favor of it (Phase 2).
- Phase 1 of delegating AI chat to local agent runtimes.

### Fixed

- Agent scan now performs a handshake first; canvas mode disabled for
  general chat.
- Frontend lockfile reconciliation after merging origin/master.

## [0.30.2] - 2026-08-19

### Fixed

- Refreshed Wails build checksum after the Windows frontend build.

## [0.30.1] - 2026-08-09

### Fixed

- Workflow engine: per-item config fields now resolve correctly for
  `core.set` and `http.request`.
- Secrets vault: KEK/DEK bootstrap serialized across concurrent processes.
- Connections: Salesforce `instance_url`, Reddit/Notion OAuth exchange, and
  Linear `Bearer` prefix.
- Browser extension: local relay endpoint now requires a shared-secret token.
- CI: build the frontend before the Wails Go backend; npm install workaround
  for an upstream npm bug; lockfile regeneration.
- `monoes_apis`: patched a path traversal, disabled debug mode, and made
  `GEMINI_API_KEY` a required environment variable.

### Changed

- Release pipeline is now gated on tests passing.

## [0.30.0] - 2026-08-08

### Added

- New social/service node implementations and action templates.

## [0.29.0] - 2026-08-07

### Added

- Secrets-vault credential unification: AI provider API keys, crawler
  session cookies, and connection credentials are routed through the
  encrypted vault instead of plaintext columns.
- Vault entries cascade-delete their dependent system rows, are badged in
  the Vault UI, and are rematerialized on import.

### Fixed

- Import now preserves non-secret connection Data and provider Status.

[unreleased]: https://github.com/monoes/mono-agent/compare/v0.31.0...HEAD
[0.31.0]: https://github.com/monoes/mono-agent/compare/v0.30.2...v0.31.0
[0.30.2]: https://github.com/monoes/mono-agent/compare/v0.30.1...v0.30.2
[0.30.1]: https://github.com/monoes/mono-agent/compare/v0.30.0...v0.30.1
[0.30.0]: https://github.com/monoes/mono-agent/compare/v0.29.0...v0.30.0
[0.29.0]: https://github.com/monoes/mono-agent/compare/v0.28.1...v0.29.0
