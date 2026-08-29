## Summary

<!-- What changed and why. One logical change per PR; link related issues. -->

Closes #

## Type of change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Documentation only
- [ ] Breaking change (fix or feature that changes existing behavior —
      call out the migration path below)

## Checklist

- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] If touching code gated behind the `social` build tag:
      `go build -tags social ./...` and `go test -tags social ./...` pass
      (CI runs both modes)
- [ ] `gofmt -l .` is clean
- [ ] CHANGELOG.md has an entry under **Unreleased**
- [ ] Docs updated — [AGENTS.md](../AGENTS.md) too if agent-facing
      behavior (commands, flags, exit codes, MCP tools, env vars) changed
- [ ] Security-relevant changes (secrets, vault, network, crash reporting,
      sandboxing) are flagged here and reviewed against
      [SECURITY.md](../SECURITY.md) — if unsure, tick it and explain
- [ ] Tests added for new commands/node types and bug fixes
      (regression test first where practical); tests need neither Chrome
      nor network access

<!-- Notes for reviewers: anything non-obvious, alternative approaches
     considered, or follow-up work this PR deliberately does not do. -->
