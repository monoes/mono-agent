# 2026-08-28 Trust & Hygiene Record

Factual record of changes made to this repository in the 2026-08-28
hygiene and documentation-accuracy effort. This replaces the interim
execution checklist as the permanent record. Dates are illustrative for
in-flight work; each item below describes the end state.

## What changed and why

### Repository hygiene

- Untracked a 20 MB stray binary (`diag_comment_like`) and `.claude-flow/`
  runtime state; both are now gitignored.
- Removed committed secrets and scraped data files from tracking
  (`monoes_apis/list_models.py`, LinkedIn search HTML/JSON samples,
  `monoes_apis/todo`). A leaked API key was found in history and revoked
  separately by the owner (see Owner Actions).
- Moved root screenshots into `docs/screenshots/`; moved
  `import_edge_passwords.py` into `scripts/`.
- Sanitized `monoes_apis/deploy_full.sh`: hardcoded key, IP, and Route53
  zone replaced with required environment variables; debug-mode write
  removed.
- Deleted dead browser-action templates in `data/actions/` for platforms
  with no live implementation (alternativeto, betalist, capterra,
  facebook, futurepedia, g2, indiehackers, lobsters, medium, pinterest,
  quora, threads, tildes).
- Removed message-variant rotation (randomized wording per recipient)
  from social DM actions, as a matter of policy.
- Dropped the unused `go-rod/stealth` dependency; module path renamed to
  `github.com/monoes/mono-agent`.

### License and community files

- Added `LICENSE` (MIT), `SECURITY.md` (vulnerability reporting, telemetry
  and crash-reporting statement, secrets-vault scope), `CONTRIBUTING.md`
  (build/test commands including `-tags social`, PR guidelines), and
  `CHANGELOG.md` (Keep-a-Changelog format).

### CI

- Added `.github/workflows/ci.yml`: build, vet, and race-enabled tests in
  both default and `-tags social` modes; later extended with a Wails job
  that builds the `wails-app/` desktop module (its Go module definition
  was fixed so it builds as its own module).

### Opt-in social build

- `internal/bot/{instagram,linkedin,x,tiktok,hackernews,producthunt}` gated
  behind `//go:build social`. In default builds those node types are
  absent, not merely disabled; registry/login/crawl surfaces degrade with
  a "rebuild with -tags social" message.
- The Gemini browser bot stays ungated (AI-service access, not engagement
  automation).
- Legacy social CLI commands (`message`, `comment`, `search`, `list`,
  `template`) are hidden from default `--help` output; still invokable,
  and visible in `--help` in `-tags social` builds.

### Documentation accuracy (review findings, fixed)

- Telemetry statements in README/AGENTS/SECURITY/COMPARISON aligned with
  actual behavior (see Crash reporting below).
- README Quick Start corrected to the real flow (`workflow import --file`
  + `workflow run` instead of a templates-inspect command).
- README architecture tree corrected (`trigger_manager.go`,
  `webhook_server.go`); integration-test command corrected to
  `-tags "integration,social"`.
- Social-platform lists completed: all six gated platforms named
  (Instagram, LinkedIn, X, TikTok, Hacker News, Product Hunt) with their
  action sets.
- `docs/USAGE_POLICY.md` vote-manipulation wording corrected to match the
  code: no vote/review actions in the browser-automation layer; the Reddit
  service node exposes official Reddit API (OAuth) endpoints, including
  votes, for use against your own account under Reddit's API terms.
- `examples/README.md` webhook quickstart corrected: `workflow activate`
  before starting the daemon; the daemon must be started (or restarted)
  after activation.
- `docs/COMPARISON.md`: Activepieces MCP claim qualified as "not verified
  here", matching the Windmill cell.
- `docs/superpowers/README.md` added, marking those plans/specs as
  historical design records (approaches such as stealth/anti-detection
  and astroturf-style actions were removed from the product — see
  CHANGELOG).

### CLI behavior changes (review fixes)

- Crash reporting: default writes a local file under
  `~/.monoagent/crashes/`; GitHub filing only when
  `MONOAGENT_CRASH_REPORT=1` is set and `monomind` is on PATH; the `npx`
  fallback path was removed.
- `$env` template access requires `MONOAGENT_ALLOW_ENV_TEMPLATES=1`.
- Output items redacted (token/secret/password/authorization/cookie-shaped
  values masked) in `--json` and MCP output; `--full-outputs` CLI opt-out.
- `secret add` stdin accepts values with or without a trailing newline.
- Exit codes: `hil approve`/`hil reject`, `secret rm`/`secret update`, and
  `workflow delete` return 2 on unknown ids; a run ending `CANCELLED`
  exits 1.
- `workflow templates run` honors `--json`.

## Verification (plain results)

- `go build ./...` and `go vet ./...` green in both default and
  `-tags social` modes.
- `go test ./...` green in both modes (unit tests, no Chrome required).
- CLI smoke: `ref`, `workflow validate` over all `examples/*.json`,
  `node schema core.if`, `run --dry-run`, MCP `tools/list` roundtrip over
  pipes, `secret add` via stdin, exit-code spot checks. 24/26 passed in
  the earlier wave; the keychain check is environment-blocked on headless
  CI (no OS keyring) and documented in AGENTS.md.
- Tracked-secrets grep (`AIza`/token patterns) clean on the working tree;
  LICENSE present; CI YAML parses; .gitignore effective; deleted action
  templates gone.
- Docs greps: no stealth/evade/under-the-radar/bulk_following terms in
  README; social action names (e.g. `auto_reply`) appear in README only
  inside the opt-in social build section (~line 383), by design; telemetry
  claims qualified everywhere they appear.

## Follow-ups (deferred deliberately)

- Store-level `SaveWorkflowNodes` edge-preserving fix (current
  delete+reinsert cascades `workflow_connections`; the CLI re-saves
  connections as a workaround).
- Webhook port env override for Docker (`docker-compose.yml` hardcodes
  9321).
- Headless keychain bypass for `secret add` (CI/containers have no OS
  keyring).
- Daemon SIGTERM graceful shutdown; gate the Chrome probe in headless
  environments.
- Decide the fate of `monoes_apis/` dev scripts living in the repo root.
- `go.sum` tidy if not already done.

## Owner actions (not executable by agents)

1. Revoke the leaked API key found in git history (revoked separately;
   verify it is dead).
2. After merge: single `git filter-repo` pass (the key, `diag_comment_like`,
   `cmd/monoes/monoes`, `wails-app/wails-app`, LinkedIn data) +
   `push --force-with-lease`; then `gh repo edit` with the new description
   and topics.
3. Confirm the CI workflow runs green once pushed.

## Round 2 record (2026-08-29)

Second wave, from a 12-reviewer audit (RA8–RA12 findings) plus follow-up
verification. Same rules as above: each item describes the end state.

### What changed and why

- Sandbox resource caps added: HTTP node bodies capped at 64 MB by
  default (configurable); `core.code` nodes limited to 512 MB memory and
  30 s CPU by default, with at most 10,000 items of 16 MB each per
  execution; `system.execute_command` output capped at 10 MB per channel
  (stdout/stderr); stored outputs persisted in full, display-truncated at
  4 KB.
- Daemon lifecycle fixes: SIGTERM graceful shutdown; Chrome probe gated
  in headless environments.
- Credential profile scoping: credentials resolve strictly within their
  own profile.
- Service-node fixes: pagination and string-escaping corrections.
- GUI (`wails-app`) fixes.
- MCP server concurrency fixes.
- Typo simulation removed from humanized typing in social actions; the
  randomized pacing between keystrokes is kept.
- README Quick Start now includes the `workflow activate` step before the
  daemon is started.

### Verification (plain results)

- Round-1 verification results above still hold for that wave's items.
- Round-2 end state recorded in CHANGELOG `[Unreleased]`; resource limits
  documented in AGENTS.md and SECURITY.md with matching numbers.
- FEATURE_n8n.md labeled as a porting reference, not a status tracker;
  README and docs/COMPARISON.md link phrasing aligned to that.

## Round 3 record (2026-08-29)

Third wave, landed by parallel fixers. Same rules as above: each item
describes the end state.

### What changed

- Webhook server environment overrides: `MONOAGENT_WEBHOOK_ADDR` sets the
  bind address (`host:port`, default `127.0.0.1:9321`) and
  `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS` sets a CORS allowlist (default: no
  CORS headers). The Docker compose file now serves webhook triggers on a
  published port without host networking.
- File-based keyring fallback: `MONOAGENT_ALLOW_FILE_KEYRING=1` stores the
  vault key-encryption key at `~/.monoagent/vault/.file-keyring` (0600)
  with a loud warning on use; without the variable the vault fails closed
  on machines with no OS keyring. Headless/CI `secret add` is now
  possible.
- Store-level node saves preserve edges; the CLI re-save workaround is
  removed. SQLite expression-index migration 024 added; `workflow list`
  now caches parsed workflow JSON.
- Node schema coverage completed: all default-build node types resolve
  schemas (16 added: `image.*`,
  `service.{reddit,devto,discord,bluesky,mastodon,hashnode,producthunt}`,
  `gemini.chat_session`, `gemini.chat_session_many`).
- Self-update verifies the release `SHA256SUMS.txt`; a checksum mismatch
  or a missing checksums file is a hard failure.
- GUI fixes: ImportWorkflow/ExportWorkflow bindings, subprocess PID
  verification, cancellable RunNode.
- `monoes_apis/` extracted to the private repository `monoes/monoes-apis`;
  this tree cleaned.

### Deferred-items disposition

| Deferred item | Disposition |
|---|---|
| Store-level `SaveWorkflowNodes` edge preservation | Done (this round) |
| Webhook bind/CORS env overrides (`MONOAGENT_WEBHOOK_ADDR`, `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS`) | Done (this round) |
| Headless keyring bypass for `secret add` (`MONOAGENT_ALLOW_FILE_KEYRING`) | Done (this round; opt-in file keyring) |
| Daemon SIGTERM shutdown / Chrome probe gating | Done (Round 2) |
| Fate of `monoes_apis/` | Done — moved to private `monoes/monoes-apis` |
| `go.sum` tidy | Done |
| HTTP/REST API surface for external agents | Remaining (draft issue 01) |
| Webhook TLS support and remote-bind hardening docs | Remaining (draft issue 02) |
| Passphrase-wrapped KEK for file-keyring mode | Remaining (draft issue 03) |
| monomind orgs integration: commit or remove | Remaining (draft issue 04) |
| Node schemas generated from Go structs | Remaining (draft issue 05) |
| Workflow marketplace curation policy | Remaining (draft issue 06) |
| i18n for GUI and CLI help text | Remaining (draft issue 07) |
| Release code-signing and notarization | Remaining (draft issue 08) |
| Benchmark suite + CI bench job | Remaining (draft issue 09) |
| Community venue decision | Remaining (draft issue 10) |
| `core.code` engine-level memory ceiling | Remaining — vendored goja revision lacks `SetMemoryLimit` (`internal/nodes/control/code.go`) |
| `comm.email_read` IMAP dependency | Remaining — still experimental |
| Roadmap: more trigger types, workflow versioning, sub-workflow node, visual debugger, metrics dashboard | Remaining |
| Roadmap: official-API publishing for Bluesky/Mastodon | Functionality shipped (`comm.`/`service.` bluesky and mastodon nodes); the README roadmap line still lists it and is due for cleanup |
| Headless-CI vault test coverage (keychain check was environment-blocked) | Remaining — file keyring now makes it possible; CI job not yet updated |
| Owner: `git filter-repo` history rewrite + force-push, repo metadata, CI-green confirmation | Remaining (owner action, see Owner actions above) |

Draft issues 01–10 above live in `/tmp/github-issues/`; the tracking
issue for this round is `/tmp/github-issues/00-tracking-round3-changelog.md`.

## Round 4 record (2026-08-30)

Fourth wave (merge + finalize), landed by parallel fixers and recorded
from their verified end-state reports; the documentation items were
landed by the docs fixer. Same rules as above: each item describes the
end state.

### What changed

- Migration healing: a Go-side schema reconcile repairs drifted SQLite
  schemas on startup; the vault migrations are renumbered 027/028; and
  the CLI and MCP entry points run the vault migration, so databases
  from older installs converge on upgrade without manual steps.
- Per-profile vault: each profile's secrets are sealed with their own
  key-encryption key under per-profile vault folders, entries are
  re-encrypted when they move between profiles, and the file-keyring
  fallback is per profile (`~/.monoagent/vault/.file-keyring-<profileID>`
  — previously a single `~/.monoagent/vault/.file-keyring`). The
  file-keyring path is corrected accordingly in AGENTS.md, SECURITY.md,
  CHANGELOG.md, and the `secret` command help.
- Executions are stamped with their owning profile as they run; a
  migration backfills the profile stamp onto existing execution rows.
- Assistant tool surface (`chat --tools`): tools are off by default;
  `--tools monoagent` opts in to workflow/vault/people/actions/comms
  tooling and `--tools monoagent,runs` additionally opts in to
  run/execution tools; the GUI settings carry an assistant-tools toggle
  that also defaults to off. Within the tool surface: `get_workflow`
  output is redacted for credential-shaped values, vault tools return
  metadata only (values never returned), delete-class tools write a
  sidecar backup of the affected record, synced message content is
  wrapped in provenance fences, and tool-call timeouts derive from the
  caller's context. Chat sessions persist and resume via
  `chat --history-id <session>`.
- Extension bridge: the extension server honors `MONOAGENT_EXTENSION_PORT`
  and keeps 9323 as the fallback port; the extension client tries the
  configured port and then 9323.
- Frontend: background polling is gated on page visibility; the
  assistant agent scan runs on page open; the legacy duplicate panel is
  removed; an empty active profile is normalized instead of erroring;
  assistant-tools settings added.
- Browser-node helper: pgrep process-name variants extended (Chromium,
  Brave, Edge alongside Chrome); the helper fails fast with a clear
  error when no supported browser is running.
- Monomind-backed `agent` and `org` top-level commands exist in the CLI;
  documentation points at their `--help` rather than duplicating it
  (partially resolves draft issue 04).
- Documentation wave (this fixer): file-keyring path corrected in four
  places; `MONOAGENT_ALLOW_ENV_TEMPLATES`, `MONOAGENT_CRASH_REPORT`, and
  `MONOAGENT_EXTENSION_PORT` added to the AGENTS.md env-var table; the
  `ref expressions` `$env` entry states the
  `MONOAGENT_ALLOW_ENV_TEMPLATES` gate; AGENTS.md gains an "Assistant
  chat & tools" section; README gains per-profile-vault and
  assistant-tool bullets plus a GUI profile-folders note; SECURITY.md
  adds the assistant-tools surface with the residual prompt-injection
  risk stated; CHANGELOG `[Unreleased]` carries the merge/finalize
  entries; the five docs/ root review artifacts carry historical-record
  markers.

### Known-cosmetic (no code change)

- `monoagentcli --version` derives its string from
  `git describe --tags --always` when not baked in via ldflags
  (`cmd/monoagentcli/main.go`), so worktree builds show stale-looking
  `v0.30.0-1-g…`-style strings until a real tag is cut; tagged release
  builds self-heal. Recorded here only; deliberately left alone.

### Verification (plain results)

- `grep -rn ".file-keyring[^-]" AGENTS.md SECURITY.md CHANGELOG.md
  cmd/monoagentcli/secret.go` returns no matches (all four call sites
  use the per-profile `.file-keyring-<profileID>` form).
- Env-var table names match the code names exactly:
  `MONOAGENT_WEBHOOK_ADDR`, `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS`,
  `MONOAGENT_ALLOW_FILE_KEYRING`, `MONOAGENT_ALLOW_ENV_TEMPLATES`,
  `MONOAGENT_CRASH_REPORT`, `MONOAGENT_EXTENSION_PORT`.
- Documentation claims were checked against the fixer end-state reports
  listed above; no claim extends beyond them.
