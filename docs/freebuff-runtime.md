# Freebuff runtime

Freebuff support requires a monomind release with a working `freebuff`
runner. Monoagent uses monomind's scan and Agent Exec Protocol for runtime
discovery, installation, models, chat, workflows and orgs.

The runner is tracked in [monomind#600](https://github.com/monoes/monomind/issues/600).
As of 2026-10-03, the official Freebuff CLI argument parser exposes an
interactive interface with login, continuation and working-directory options,
but no prompt/print/JSON mode. See the
[verified upstream parser](https://github.com/CodebuffAI/freebuff/blob/eaf90999faed783fd1ba3d2e8544d853bf2282fc/cli/src/cli-args.ts).
Installing that CLI alone does not enable automated execution in monoagent.

## Once monomind supplies the runner

```sh
monoagentcli agent scan --json
# Continue only if the scan includes the freebuff runner.
monoagentcli agent install freebuff
freebuff login
monoagentcli agent test freebuff
monoagentcli chat --runtime freebuff -- 'Hello'
```

The official npm package and executable are both `freebuff`. Monoagent uses
the scan's installation recipe (`npm install -g freebuff`) and login hint;
it does not maintain a second runtime registry. An older monomind that does
not list Freebuff refuses `agent install freebuff` as an unknown runtime.

Monoagent pins Freebuff's executable by absolute path through
`FREEBUFF_CLI_BIN`, including an operator-specified override, to prevent a
project's version-manager configuration from redirecting execution. The
monomind runner must use that same override for both discovery and execution.

Config generation can choose Freebuff when the scan lists it as installed
and no earlier preferred runtime is installed. Set `MONOAGENT_AI_RUNTIME=freebuff`
to explicitly select it once the runner exists. Existing runtime preferences
are preserved.

The desktop displays the runtime as Freebuff. Model choices, streaming,
resume, tool access, confinement and coder readiness come from monomind's
reported capabilities. Coder mode is unavailable unless monomind reports
full-access support and supplies the required protocol capabilities. No model
catalog or prices are inferred from Freebuff's product name.

The compatibility tests use temporary executable and monomind fixtures.
They establish monoagent's behavior with a future runner; they do not
establish live Freebuff execution, authentication or confinement support.
