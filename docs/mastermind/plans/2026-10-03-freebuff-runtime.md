# Freebuff Runtime Implementation Plan

> **For agentic workers:** Use `mastermind-execute` to implement this plan task-by-task.

**Goal:** Prepare monoagent for monomind's future Freebuff runtime and file the runner dependency.

**Architecture:** Reuse Agent Exec Protocol and monomind scan capabilities. Add only monoagent-owned executable pinning, fallback selection and presentation.

**Tech Stack:** Go, React/JavaScript, GitHub CLI.

## Global Constraints

- Work only in `/Users/morteza/Desktop/monoes/mono-agent-freebuff`.
- Preserve existing runtime priority; append `freebuff`.
- Use runtime id `freebuff`, executable `freebuff`, override `FREEBUFF_CLI_BIN`.
- Do not advertise execution before monomind supports it.

## Task 1: Executable pin and config fallback

- [x] Add tests in `internal/monomind/freebuff_test.go` for absolute and relative executable overrides, missing executables and shim fallback refusal.
- [x] Add a fake scan/exec config test in `internal/config/agentgen_test.go` for Freebuff-only and existing-runtime priority cases.
- [x] Run `go test ./internal/monomind ./internal/config -run Freebuff`; confirmed failures before implementation.
- [x] Append `{"freebuff", "FREEBUFF_CLI_BIN"}` to `runtimeBinEnv` in `internal/monomind/pin_runtimes.go`.
- [x] Append `"freebuff"` to `runtimePriority` in `internal/config/agentgen.go`.
- [x] Rerun the tests; PASS, including race checks.

## Task 2: Presentation and protocol compatibility

- [x] Add `freebuff: 'Freebuff'` in `wails-app/frontend/src/lib/runtimeLabels.js` and the same product name in `cmd/monoagentcli/chat_coder_journal.go`.
- [x] Extend `runtimeLabels.test.js` with the Freebuff label.
- [x] Test a scan-supplied `freebuff` npm recipe in `cmd/monoagentcli/agent_install_test.go`, including an old scan that does not know Freebuff.
- [x] Extend `cmd/monoagentcli/coder_runtimes_test.go` to verify scanned Freebuff remains unready without full access and becomes ready only when that capability is supplied.
- [x] Document the dependency and usage in `docs/freebuff-runtime.md` and link it from AGENTS.md.
- [x] Run package and frontend checks. Freebuff tests and frontend tests/build pass. Config and monomind suites pass with two existing host-discovery failures excluded; the full CLI suite also has failures, recorded in the PR. Go build passes.

## Task 3: Review and handoff

- [x] Review the diff for confinement, executable selection and accidental writes outside the worktree. Independent read-only review found no actionable defects.
- [x] Open monoes/monomind issue with source evidence, runner contracts and acceptance criteria: https://github.com/monoes/monomind/issues/600.
- [x] Commit and push only this branch; open a monoagent PR linking that issue and stating no live Freebuff execution was verified: https://github.com/monoes/mono-agent/pull/323.

Baseline comparison: an untouched origin/master export reproduced all four
full-CLI failures (capture-task paths, coder conversation-folder paths,
coder-root paths and cancellation of a defunct process). The two excluded
config/monomind discovery failures occurred before production changes.
