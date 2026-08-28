# Contributing to Mono Agent

Thanks for your interest in improving Mono Agent.

## Prerequisites

- **Go 1.25** or newer (the version pinned in [`go.mod`](go.mod))

## Build & Test

```bash
# Build the CLI (produces ./monoagentcli)
go build ./cmd/monoagentcli

# Run the test suite
go test ./...

# Vet
go vet ./...
```

### Social-platform nodes (opt-in build tag)

Social-platform browser bots and their action templates are **not compiled
into the default binary**. They live behind the `social` build tag:

```bash
go build -tags social ./...
go vet -tags social ./...
go test -tags social ./...
```

CI runs both modes — make sure your change builds and tests green in **both**
default and `-tags social` modes.

## Code Style

- Format with `gofmt` (or `go fmt ./...`) before submitting — CI and review
  expect canonical formatting.
- Match the style of the package you are touching; keep files under ~500 lines.
- No new dependencies without discussion — prefer the standard library.

## Pull Requests

- **Small diffs.** One logical change per PR; split unrelated work into
  separate PRs.
- **Tests required** for new CLI commands, node types, and bug fixes
  (regression test first where practical). Tests must not require Chrome or
  network access.
- Describe what changed and why; link any related issues.
- Verify before opening: `gofmt` clean, `go build ./...`,
  `go test ./...`, and the same with `-tags social`.

## Security

Found a security issue? Please do not open a public issue — see
[SECURITY.md](SECURITY.md) for how to report it responsibly.
