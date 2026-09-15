# CLAUDE.md

## Project

mono-agent (`github.com/monoes/mono-agent`) — local-first workflow
automation engine (n8n alternative) in a single Go binary: `monoagentcli`.
Full agent guidance lives in **AGENTS.md** (root) — read that first.

## Build / test / lint

```bash
go build ./...                        # build everything
go build ./cmd/monoagentcli           # the CLI binary
go build -tags social ./cmd/monoagentcli   # opt-in build incl. social platform nodes
go test ./...                         # run tests (no Chrome required)
go vet ./...                          # lint
gofmt -l .                            # formatting check
```

The desktop GUI (`wails-app/`) is optional and needs the Wails toolchain.

## Notes

- Default builds exclude social platform nodes — use `-tags social` when
  working on `internal/bot/` or social node code.
- CLI state lives in `~/.monoagent/` (global, not per-repo).
- Commit messages: conventional style (`type(scope): description`).
- Never commit secrets or `.env` files.
# monomind:start instructions:claude
# Monomind

Use the `monomind` MCP tools for graph navigation, impact analysis, memory, and organization work.
For multi-step work, load only the applicable `mastermind-*` skill; do not load all workflows at once.
If MCP is unavailable, run `npx -y monomind@latest doctor` and use `npx -y monomind@latest` commands.
# monomind:end instructions:claude
