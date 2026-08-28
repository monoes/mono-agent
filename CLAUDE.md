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
