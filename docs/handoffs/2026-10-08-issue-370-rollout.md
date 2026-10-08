# Issue 370: dormant B5a rollout review

Reviewed latest master `ab14b469` in the issue-363-370-365 worktree against the authoritative B5a plan from `origin/feat/monoes-account-gate`. Enforcement remains dormant (`internal/account/rollout.go`: `time.Time{}`); no production key/date, license, account access, deployment or release was changed.

## Findings and changes

- Task 4b was already implemented: shared `stopGrace=35s`, launchd `ExitTimeOut`, systemd `TimeoutStopSec=90s`, Windows restart wait, and platform tests. Kept existing implementation. Stale daemon restart/update (#431) and first-run adoption (#430) are already on master.
- Task 5b was missing. Release signs both loose macOS CLIs, bundled CLI and app with `--options runtime`, signs the bundle inside out, verifies signatures, checks runtime flags and rejects dyld output. Command execution is captured separately, so a failed CLI invocation cannot masquerade as a successful zero-line check. Added a macOS proof script and release step that reject linker/ad hoc signatures, accept hardened signatures, verify a signed bundle through zip/unzip, and reject a CLI replaced after signing.
- Task 7 was missing. Desktop subprocess tests and manual capture/org/browser/automation/library-packaging consumers now use `devaccount` with a future date. Doctor smoke keeps the default build and excludes only the intentionally failing account row from its healthy-core assertion, retaining schema checks. The audit found a new consumer absent from the old plan: `tasks-mcp-smoke.sh` starts gated MCP under `env -i`; its documented build now uses `devaccount`, and `run()` explicitly carries the future-date override. The S4 consumer table is appended to the spike findings.

- Final packaging review found third-party `NOTICE` absent from artifacts. Release now publishes a checksummed standalone `NOTICE` for loose CLI/Windows assets, includes it inside the signed macOS app, Linux tarball and extension zip, and compares every archived copy to the source before checksumming. Existing licenses remain unchanged.

- Full past-date rehearsal exposed two remaining setup-test assumptions: the healthy-core test counted the unsigned-in account row as a failure, and the fix-loop test expected exactly three local fixes. Both now explicitly set a dormant test date with `account.SetEnforceFromForTest`, preserving their local-setup subject and restoring the global date through cleanup. No production rollout date was changed.

## Verification

Passed:

- `go test ./internal/autostart -count=1`.
- Desktop real-binary export/import, workflow bindings and image watcher tests (100.7 seconds).
- `bash scripts/check-hardened-runtime-test.sh` on macOS: linker/ad hoc signatures produce 81 dyld lines and are rejected; hardened signatures produce zero lines and pass; bundle zip/unzip passes and replacing its CLI is rejected.
- Signing the real locally built CLI with the hardened runtime and executing it with `DYLD_PRINT_LIBRARIES=1`: `flags=0x10002(adhoc,runtime)`, no dyld output.
- Release YAML parses; every macOS `run` block passes `bash -n`; adapted scripts pass `bash -n`.
- `bash scripts/check-release-tags-test.sh`.
- Tasks MCP real-binary smoke.
- Doctor smoke with `MONOAGENT_DEV_ENFORCE_FROM=2020-01-01T00:00:00Z`: `doctor smoke: ok`. Local macOS required `SHELL_SESSIONS_DISABLE=1` to prevent system shell startup writing session history, plus a temporary bounded `timeout` wrapper because GNU `timeout` is absent. Those are existing local baseline constraints; neither was changed in the repository.
- Scoped `git diff --check`.
- Both corrected setup tests pass on the dormant tree and disposable past-date source copy, with no skips.
- Executed the actual release Linux/extension packaging and flatten/checksum blocks against scratch fixtures: exact notice copies pass, a Linux archive missing `NOTICE` is rejected. All release bash blocks parse. Hardened signed bundle proof also verifies the resource notice survives zip/unzip.

Root reviewer evidence: the full dormant master baseline, final focused CLI tests, full desktop suite, real-binary account smoke (135.841 seconds), account race checks (98.305 seconds), default/nosocial builds and vet passed. The complete frontend suite passed with two workers: 167 files, 1,795 tests; task extension checks passed 51 tests. The root reviewer built the app with the completed frontend, signed the real app and bundled rebuilt CLI with NOTICE, verified runtime signatures, rendered `ref account`, and confirmed zero dyld output. UI/bridge/live-account/update checks remain owner-run.

The corrected full CLI package passed on the disposable past-date tree (139.417 seconds; only the intentional `TestAccountDocsMatchDormantRollout` pin skipped). All other packages passed the full rehearsal except the expected production-key guard. The account package passed a past-date rerun (78.421 seconds) excluding only `TestEnforcedBuildPinsAKey`. Full Wails tests passed on dormant and past-date trees (87.008 and 86.930 seconds). The disposable date was never applied to the working branch.

One initial parallel verification attempt exhausted temporary disk space during linking and was interrupted. It is not counted as a pass; completed limited-concurrency runs above supply the final evidence.

Past-date rehearsal intentionally trips `TestAccountDocsMatchDormantRollout` (documentation/date pin) and `TestEnforcedBuildPinsAKey` (production-key readiness). These guards are preserved. They demonstrate that activation is blocked until owner-provided key/date and matching documentation; passing isolated rehearsal packages must not be read as production readiness.

## Owner checks remaining before activation/release

The issue can ship these dormant preparatory changes. Production rollout still requires the real owner to confirm server deployment/migration and refresh-reuse policy, production signing key/fixture, issuer/audience claims, historical release compatibility with pending markers, a production sign-in/library/adoption dry run, all S1–S6 spike evidence, and a date at least three weeks ahead agreed with matching release documentation. None can be substituted with development-key tests.

After the first hardened release, smoke the actual downloaded app: UI/pages, workflow from app and CLI, extension bridge, and the preceding release's `update --app` path. Local stand-ins prove signing mechanics, not runtime feature compatibility. Release R must include the matching documentation, green CI/account smoke/release guard and owner approval; do not activate enforcement from this review alone.
