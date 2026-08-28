# mono-agent — Shared Agent Instructions

> Prepended to every agent prompt in this repo. Stack: **Go**.

## Project Overview

- **Repo:** `github.com/monoes/mono-agent` — local-first workflow automation engine (n8n alternative), single Go binary `monoagentcli`
- **Language:** Go (no npm — any `npm install` instructions are stale; ignore them)
- **Source:** root `cmd/`, `internal/`, `data/` packages
- **Tests:** co-located (`*_test.go`)
- **CI:** GitHub Actions

## How to Run

```bash
# Build (no separate dependency-install step — Go modules handle it)
go build ./...
go build ./cmd/monoagentcli            # the CLI binary

# Tests
go test ./...

# Lint / format
go vet ./...
gofmt -l .

# Opt-in build with social platform nodes (Instagram/LinkedIn/X/TikTok)
go build -tags social ./cmd/monoagentcli
```

Runtime state (workflows, vault, logins) is global under `~/.monoagent/` —
CLI commands work from any directory. See root **AGENTS.md** for full
agent-facing guidance on driving the CLI.

## Critical Constraints
- **Never** modify files outside your assigned task scope
- **Always** run tests before reporting a task complete
- **Never** commit secrets, credentials, or .env files
- **Always** write tests alongside implementation (TDD)
- Prefer editing existing files over creating new ones
- Keep commits small and descriptive (conventional commits format)
- Keep files under **500 lines** — split when approaching the limit
- NEVER save working files to the root directory

## Code Quality Non-Negotiables
- No commented-out code in committed files
- No `TODO` comments without a linked issue
- All public functions/methods must have typed signatures
- Errors must be handled explicitly — never silently swallowed
- Remove debug logs before committing

## Go Best Practices
- Errors are values — always handle them, never `_` a returned error in production
- Use `context.Context` as the first parameter of any function that does I/O
- Prefer table-driven tests with `t.Run`
- Keep interfaces small — prefer 1-3 methods
- Use `defer` for cleanup but be aware of loop-defer pitfalls

## CI / CD
- All code must pass CI before merging — do not bypass checks
- CI runs build/vet/test in BOTH modes: default and `-tags social`
- Never commit secrets or API keys — use environment variables from the CI secret store
- Write commit messages that pass the conventional commits format: `type(scope): description`

## Agent Collaboration Rules
- Write a brief ## Handoff Context block when completing a task in a chain
- Include: files changed, key decisions, what the next task needs to know
- If BLOCKED, stop immediately and report with full context — do not guess
- Facts about the CLI must be verified from source (`cmd/monoagentcli/`) or
  `monoagentcli ref` — never invent commands or flags
