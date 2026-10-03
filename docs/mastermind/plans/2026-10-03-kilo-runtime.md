# Kilo Code Runtime Implementation Plan

> **For agentic workers:** Use `mastermind-execute` to implement this plan task-by-task.

**Goal:** Prepare monoagent for a monomind Kilo Code runner and file its implementation issue.

**Architecture:** Reuse Agent Exec Protocol, scan recipes and capability gating.
**Tech Stack:** Go, JavaScript, GitHub CLI.

## Constraints

- Work only in `/Users/morteza/Desktop/monoes/mono-agent-kilo`.
- Runtime/executable `kilo`; override `KILO_CLI_BIN`; package `@kilocode/cli`.
- Preserve existing preferences. Do not claim live runner support.

## Steps

- [x] Add pinning tests in `internal/monomind/kilo_test.go` and scan-based selection tests in `internal/config/agentgen_test.go`; run `go test ./internal/monomind ./internal/config -run Kilo -count=1` and confirm failures before implementation.
- [x] Add `{"kilo", "KILO_CLI_BIN"}` to `internal/monomind/pin_runtimes.go` and append `"kilo"` to `runtimePriority` in `internal/config/agentgen.go`; rerun the tests to PASS.
- [x] Add Kilo Code labels to `wails-app/frontend/src/lib/runtimeLabels.js` and `cmd/monoagentcli/chat_coder_journal.go`.
- [x] Extend `runtimeLabels.test.js` and add `cmd/monoagentcli/kilo_runtime_test.go` using the existing installation/coder fixtures for Kilo's npm recipe, unknown-runner refusal and scan capability gating.
- [x] Open monomind issue #601 with verified source references, protocol mapping, permissions and acceptance criteria; document the dependency in `docs/kilo-runtime.md` and AGENTS.md.
- [x] Run affected Go suites, race checks, CLI/frontend builds and runtime-label tests. All selected checks passed; known baseline exclusions are documented in the PR. Independent review found no actionable defects.
- [x] Review the diff, commit/push only `feat/kilo-runtime`, and open a monoagent PR linking the issue: https://github.com/monoes/mono-agent/pull/324.
