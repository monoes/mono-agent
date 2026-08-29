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
