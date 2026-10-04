# Kilo Code runtime

Monoagent is prepared to consume runtime `kilo` from monomind. The actual
runner is tracked in [monomind#601](https://github.com/monoes/monomind/issues/601).
This compatibility change alone does not enable Kilo chat, workflows or org
execution; monomind must first implement and report support for the runner.

Kilo already offers `kilo run --format json` for headless execution, piped
prompt input, model selection, session continuation and working-directory
selection. The official npm package is `@kilocode/cli`; prefer executable
`kilo` (the package also exports `kilocode`). See the
[official CLI reference](https://kilo.ai/docs/code-with-ai/platforms/cli-reference)
and [verified package source](https://github.com/Kilo-Org/kilocode/blob/76bcfd40be616a72f4697b3041565f322245b462/packages/opencode/package.json).

## Once monomind supplies the runner

```sh
monoagentcli agent scan --json
# Continue only if the scan includes kilo.
monoagentcli agent install kilo
kilo auth login
monoagentcli agent test kilo
monoagentcli chat --runtime kilo -- 'Hello'
```

Monoagent consumes monomind's scan recipe (`npm install -g @kilocode/cli`)
and login hint. An older monomind that does not know Kilo refuses the install
command as an unknown runtime; no second runtime registry is maintained here.

The `KILO_CLI_BIN` executable override is pinned by absolute path, like
other runtime binaries, to prevent project configuration from redirecting
execution. The monomind runner must honor the same override during both scan
and execution.

Selector config generation may choose Kilo when it is the only installed
preferred runtime; it is appended after existing choices. Once the runner
exists, `MONOAGENT_AI_RUNTIME=kilo` explicitly selects it. Desktop and coder
status labels display Kilo Code.

Models, reasoning variants, streaming, resume and tool access come from
monomind. Coder mode requires its full-access capability and the required
protocol capabilities. Kilo's headless interface does not by itself establish
confinement: native tools, MCP, project settings and daemon reuse need runner
verification. `--auto` is a permission approval option, not a sandbox.
No model catalog or pricing assumptions are added by monoagent.

Tests use temporary fake executables and monomind fixtures. No live Kilo
inference, credential changes or global installations are required to verify
these compatibility changes.
