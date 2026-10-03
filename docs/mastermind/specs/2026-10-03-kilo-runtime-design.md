# Kilo Code runtime compatibility

Use runtime id `kilo`, executable `kilo`, and override `KILO_CLI_BIN`.
Monomind owns runtime discovery, installation recipes, execution, model
listing and access capabilities. Monoagent supplies executable pinning,
config-generation fallback and product labels, as with other CLI agents.

Kilo-Org/kilocode main at `76bcfd40be616a72f4697b3041565f322245b462`
(2026-10-03) provides `kilo run --format json`, piped stdin, `--model`,
`--session`, `--dir` and `--variant`. Package `@kilocode/cli` exposes `kilo`
and `kilocode`. Prefer the documented `kilo` binary. Authenticate with
`kilo auth login`. `kilo models` lists provider/model ids; its verbose mode
mixes id lines and formatted JSON, so it is not a JSON-document endpoint.

Extend monomind rather than duplicating a native runner in monoagent.
Kilo's OpenCode ancestry may help implementation but does not prove the
same event, auth or confinement contracts. Do not alias Kilo to OpenCode.
Default headless permission handling, `--auto`, user/project settings and
daemon reuse require independent verification in the runner.

Monoagent changes: pin `KILO_CLI_BIN`; append `kilo` after all existing
config-runtime preferences; display Kilo Code; verify scan-driven install
and coder readiness. Do not add speculative model ids or cost assumptions.
Document the monomind dependency and eventual usage. Actual Kilo execution
remains unavailable until monomind implements the runner.

All changes belong to `/Users/morteza/Desktop/monoes/mono-agent-kilo`, branch
`feat/kilo-runtime` from origin/master, independent of the Freebuff PR and
other active sessions. Use fake executables/monomind for tests; no live AI,
global runtime installs or credential changes.
