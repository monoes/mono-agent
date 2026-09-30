# Test fixtures

`fixtures/*.ndjson` are verbatim copies of monomind's golden Agent Exec
Protocol transcripts (monomind repo: `doc/agent-exec-protocol/fixtures/`,
protocol v1 rev 4). They are the caller-side contract — mono-agent's client
tests validate its event model against them. Regenerate by copying after any
monomind protocol revision bump.

`fake-monomind.sh` is a scripted monomind stand-in for subprocess tests: it
speaks the handshake, scan, and a tool-bridged exec turn; `fake-monolith.sh`
spawns a child and ignores cancellation, for the process-group kill test.

`fake-mise.sh` and `make-shim-fixture.sh` reproduce #301: a mise-style shim
that lets a project's `.tool-versions` pick the binary it runs, and a
project with a planted node and monomind. `pin_test.go` and the CLI's org
tests use them to prove the planted binaries never run.
