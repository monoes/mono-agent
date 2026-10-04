# Freebuff runtime compatibility

Monoagent delegates agent discovery, models, execution, auth and capability
reporting to monomind. Keep that boundary: a direct Freebuff subprocess in
monoagent would duplicate the runner protocol and bypass confinement checks.

Prepare monoagent to consume a future `freebuff` runner from monomind:

- Pin the executable through `FREEBUFF_CLI_BIN`, like other agent binaries.
- Add Freebuff last in config generation's runtime preference list, preserving
  the existing choices when multiple runtimes are installed.
- Display the product name as Freebuff in desktop runtime labels and coder
  context. Install from monomind's `npm install -g freebuff` recipe.
- Verify installation and coder readiness remain driven by scan metadata.
- Leave model discovery and cost reporting to monomind; do not invent models
  or infer token prices for Freebuff's included access.

Upstream limitation: CodebuffAI/freebuff at
`eaf90999faed783fd1ba3d2e8544d853bf2282fc`, `cli/src/cli-args.ts`, exposes
`login`, `--continue`, `--cwd`, and `--trust-agents`, but no prompt/print/JSON
mode. `initialPrompt` is always null for Freebuff. Monomind must establish a
supported noninteractive transport before claiming execution support. This
change alone does not make Freebuff executable from monoagent.

Alternatives considered: implement a runner here (violates the ownership
boundary); automate the terminal UI (fragile and cannot honestly report
confinement or structured events); extend monomind (chosen).

Develop only in `/Users/morteza/Desktop/monoes/mono-agent-freebuff`, branch
`feat/freebuff-runtime`, based on origin/master. Do not edit, reset, stage,
commit or change dependencies in the other session's checkout.

Verification uses temporary fake monomind/executable fixtures, not live AI
calls or changes to the user's installation. Open a monoagent PR with the
dependency explicitly stated and a concrete monomind implementation issue.
