# AGENTS.md — mono-agent for AI agents

Entry point for AI agents (Claude Code, Codex, Cursor, …) discovering or
driving this tool. Everything here is verified against the CLI source in
`cmd/monoagentcli/`. When in doubt, trust the CLI (`monoagentcli ref`) over
this file — it ships with the binary and cannot drift.

## What this is

**mono-agent** is a local-first workflow automation engine — an n8n
alternative packed into a single Go binary (`monoagentcli`). Build workflows
from nodes (triggers, HTTP, databases, AI, comms, services), run them on
schedule/webhook/manual, and manage everything from the CLI or an optional
desktop GUI (`wails-app/`).

- All state lives in `~/.monoagent/` (SQLite + JSON workflow files). Every
  command works from any directory — there is no per-project setup. One
  exception: `monoagentcli init` also copies skill files into
  `~/.claude/skills/` when `~/.claude` exists (a local file copy; no
  network).
- No telemetry: no analytics, phone-home checks, or usage counters. What
  can leave the machine (see [SECURITY.md](SECURITY.md) for the full
  statement): the API calls your workflows make, commands you explicitly
  invoke that talk to an external service (e.g. `login`, `update`), and
  opt-in crash reporting. Crash reports are written to local files under
  `~/.monoagent/crashes/` by default and are only filed to GitHub when
  `MONOAGENT_CRASH_REPORT=1` is set **and** the `monomind` CLI is on PATH.
- Services are automated via their **official APIs** (OAuth/API-key
  connections). Some nodes use **browser automation on the user's own
  logged-in session** (e.g. Gemini image generation — no API key needed).
- **Social platform nodes** (Instagram, LinkedIn, X, TikTok, Hacker News,
  Product Hunt) are NOT in the default build — they require an opt-in
  compile: `go build -tags social`. In a default build those node types
  are absent, not merely disabled.

## Start here

```bash
monoagentcli ref            # the offline manual — read this first
monoagentcli --help         # command list; the root help includes an agents note
```

`ref` subcommands (all offline, always current with the binary):

| Topic | What it covers |
|---|---|
| `ref commands` | Every CLI command with flags and examples |
| `ref nodes` | Every node type, grouped by category |
| `ref node <type>` | Deep docs for one node type (config, inputs, outputs) |
| `ref workflow` | Workflow JSON format and connection model |
| `ref expressions` | `{{ }}` template syntax and built-in functions |
| `ref examples` | Common workflow patterns |
| `ref templates` | The bundled ready-to-run workflow templates |
| `ref connections` | Profiles, OAuth, credential resolution — **read before touching `--profile` or credentials** |
| `ref crawling` | Automating sites with no built-in node type |

Prefer `ref` over guessing from `--help` alone.

## Legacy top-level commands

Older top-level commands include `message`, `comment`, `search`, `list`,
`template`, `crawl`, `people`, `login`, `logout`, `connect`, `config`,
`export`, `status`, `init`, `run`, `action`, and `schedule`. They are
oriented toward the optional social build (`-tags social`) and the CRM
features. The social-oriented ones (`message`, `comment`, `search`, `list`,
`template`) are hidden from the default `--help` output but still work when
invoked directly; they appear in `--help` only in `-tags social` builds.
General automation lives under `workflow` and `node`; prefer those.

## Machine-readable output

The global `--json` flag works on most commands. Place it before the
subcommand: `monoagentcli --json workflow search`.

```bash
# What can this already do? Returns ready-to-run commands per hit.
monoagentcli --json workflow search <query>

# JSON schema for a node type (exit 2 on unknown type).
monoagentcli node schema <type>

# Validate a workflow without running it (exit 3 on invalid).
monoagentcli workflow validate <id>
monoagentcli workflow validate --file <workflow.json>

# Run controls:
monoagentcli workflow run <id> --dry-run    # validate + print execution plan, no run
monoagentcli workflow run <id> --no-wait    # print execution id, exit 0
monoagentcli workflow run <id> --timeout 30m
monoagentcli --json workflow run <id>       # execution record + per-node output items
```

Typical agent loop: `workflow search --json` → inspect template with
`workflow templates show <id>` → `workflow run --dry-run` → real run with
`--json` → read per-node outputs.

> **Importing a workflow is equivalent to executing code.** Workflows can
> run shell commands (`system.execute_command`), inline JavaScript
> (`core.code`), and template expressions against local files. Only import
> workflows from sources you trust.

## MCP server

```bash
monoagentcli mcp    # stdio JSON-RPC MCP server
```

Register it with any MCP client (stdio transport). Exposed tools:

- `workflow_list`, `workflow_get` (fetch one workflow as JSON),
  `workflow_run` (returns status + node outputs),
  `workflow_status`, `workflow_validate`
- `node_list`, `node_schema`
- `hil_list`, `hil_approve`, `hil_reject`
- `docs` (browse `ref` topics)

Prefer MCP when the host supports it; the CLI covers the same surface.
Tools carry `readOnly`/`destructive` annotations where applicable, so
hosts can gate dangerous calls.

## Human-in-the-loop from agents

HIL nodes pause a running workflow until a person approves or rejects the
data. Agents can drive that queue headlessly:

```bash
monoagentcli --json hil list                          # pending items (readonly_data + editable_data)
monoagentcli hil approve <id> --data '{"caption":"edited text"}'
monoagentcli hil reject <id>
```

`--data` overrides the item's editable fields with a JSON object (defaults
to `{}` if omitted). Rejection fails the workflow branch — surface the
consequence to the user before rejecting.

## Secrets

Secrets live in an **encrypted vault** (OS keyring-wrapped key,
AES-256-GCM payloads) — never in argv or shell history:

```bash
# Preferred: pipe the value via stdin (omit --value/--field entirely to read stdin)
printf '%s' "$TOKEN" | monoagentcli secret add --kind secret --name openai-key
```

Stdin input is accepted with **or without** a trailing newline —
`printf '%s' "$TOKEN"` and `echo "$TOKEN"` both work; a single trailing
newline is stripped.

```bash
monoagentcli secret list                 # metadata only, never values
monoagentcli secret add --kind secret --name aws \
  --field access_key_id=... --field secret_access_key=...   # still argv — avoid for high-value secrets
```

Note: `--value` exists as a shorthand but leaks through process listings
and shell history — prefer stdin or the interactive prompt for real secrets.

The vault requires the host OS keychain (macOS Keychain, Linux Secret
Service, or Windows Credential Manager) — there is no headless bypass yet,
tracked as a follow-up. On a machine without a keyring, `secret add` fails.

## Profiles

Everything (workflows, connections, people, secrets, HIL queue) is scoped
to a profile. `--profile <name>` isolates a single command; the active
default comes from `monoagentcli profile`:

```bash
monoagentcli profile list
monoagentcli --profile work workflow list     # one-off override, no switch
```

A workflow saved under one profile cannot run under another — if a run
fails with "workflow belongs to a different profile", check
`profile list` instead of recreating the workflow. Read
`monoagentcli ref connections` before writing anything that touches
profiles or credentials.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | general error — also the exit for a run that ends `CANCELLED` |
| 2 | not found (workflow, node type, HIL item, secret name, …). Includes `hil approve`/`hil reject`, `secret rm`/`secret update`, and `workflow delete` on unknown ids |
| 3 | invalid input / validation failure |
| 4 | auth or connection failure |

Branch on these instead of parsing stderr.

## Building from source

```bash
go build ./cmd/monoagentcli        # default build (no social nodes)
go build -tags social ./cmd/monoagentcli   # opt-in: Instagram/LinkedIn/X/TikTok/Hacker News/Product Hunt nodes
go test ./...                      # tests, no Chrome required
go vet ./...
gofmt -l .
```

The desktop GUI (`wails-app/`) is optional and needs the Wails toolchain;
the CLI is fully usable without it. State always lives in `~/.monoagent/`
regardless of where the binary runs from.

## Resource limits

Runs are capped to bound the blast radius of an imported or misbehaving
workflow:

- HTTP node bodies: 64 MB default (configurable).
- `core.code` nodes: 512 MB memory and 30 s CPU defaults, and at most
  10,000 items of 16 MB per item per execution.
- `system.execute_command` output: 10 MB per channel (stdout and stderr).
- Stored outputs are persisted in full but display-truncated at 4 KB.

## Honesty box — what this tool will NOT help with

This is an own-accounts automation tool. It will not assist with:

- **Engagement farming** — fake likes/comments/views inflation, vote or
  review manipulation
- **Astroturfing** — manufacturing the appearance of grassroots support
- **Mass unsolicited outreach** — spam DMs, bulk cold messages to people
  who never asked for contact

Platform automation must follow the platform's terms and use the user's own
accounts. Human-in-the-loop approval is available (and strongly recommended)
for any outbound action. See `docs/USAGE_POLICY.md` for the full policy.

If a request falls into the above categories, decline it — no workaround
advice either.
