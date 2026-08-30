# Security Policy

Mono Agent is a local-first workflow automation tool. This document explains
how to report vulnerabilities, what is in scope, and how your data is handled.

## Reporting a Vulnerability

Please report vulnerabilities privately — do **not** open a public GitHub issue.

- **GitHub private vulnerability reporting** (preferred):
  https://github.com/monoes/mono-agent/security/advisories/new
- **Email**: security@monoes.me

Include reproduction steps, affected commands/versions, and impact assessment
where possible. We aim to acknowledge reports within 72 hours and will
coordinate a fix and disclosure timeline with you. Good-faith research into
the security of this project is welcomed; we will not pursue legal action
against reporters who avoid service degradation, data exfiltration, and
privacy violations.

## Supported Versions

| Version | Supported |
| ------- | --------- |
| latest release | ✅ |
| older releases | ❌ — please update before reporting |

## Scope

Mono Agent is a desktop/CLI automation platform that runs entirely on your
machine. In scope:

- The `monoagentcli` binary and the Go packages under `internal/`
- The workflow engine, node implementations, and CLI surface
- The MCP server (stdio JSON-RPC) for AI agents
- The encrypted secrets vault (see below)
- The assistant chat tool surface (`monoagentcli chat --tools` — see
  [Assistant tools](#assistant-tools-chat---tools) below)
- The browser-extension bridge and bundled deployment scripts

Out of scope:

- Vulnerabilities in third-party services Mono Agent can connect to
  (report those to the service operator)
- Issues in user-authored workflows or user-installed action templates
- Credential leaks caused by how a user configured their own environment

## Secrets Vault

Credentials (API keys, OAuth tokens, website logins) are stored in an
encrypted vault, never in plaintext on disk. The design is described in
[docs/superpowers/specs/2026-07-13-secrets-vault-design.md](docs/superpowers/specs/2026-07-13-secrets-vault-design.md)
and implemented in [`internal/secrets`](internal/secrets):

- Data is encrypted with AES-256-GCM using per-profile data-encryption keys
- Key-encryption keys are backed by the OS keyring (macOS Keychain,
  Windows Credential Manager, Linux Secret Service) via go-keyring
- Plaintext values are only decrypted in memory when a workflow runs or when
  you explicitly run `monoagentcli secret reveal <name> --reveal`

### File-based keyring fallback (weaker posture)

When `MONOAGENT_ALLOW_FILE_KEYRING=1` is set and no OS keyring is
available, the key-encryption key is stored in a per-profile file at
`~/.monoagent/vault/.file-keyring-<profileID>` (permissions 0600)
instead of the OS keyring, and the CLI prints a warning whenever it uses
it. This is a
weaker posture and is deliberately opt-in: any process running as the same
user, or anyone with read access to the disk volume or its backups, can
retrieve the KEK and decrypt the vault — key and ciphertext then live on
the same volume. Payloads remain AES-256-GCM encrypted, but the OS
keyring's process-scoped access control is lost. Use the fallback only
where no keyring exists (headless CI, containers); without the variable
set, secret operations fail closed.

## Assistant tools (chat `--tools`)

The `chat` command can expose a monoagent tool surface to the assistant
model. Guardrails:

- Tools are **off by default**; enabling them is explicit (CLI
  `--tools monoagent`; run/execution tools additionally require
  `--tools monoagent,runs`). The GUI settings toggle mirrors the gate
  and also defaults to off.
- Vault tooling returns entry **metadata only** — secret values are
  never returned to the model.
- Workflow definitions returned by `get_workflow` are redacted for
  credential-shaped values.
- Destructive (delete-class) tools write a sidecar backup of the
  affected record before deleting.
- Message content synced from connected mail accounts is wrapped in
  provenance fences so the model can attribute it.
- Tool-call timeouts derive from the caller's context, so cancelled
  sessions stop in-flight tool work.

**Residual risk, stated honestly:** prompt injection cannot be fully
eliminated when assistant context includes synced message content — a
crafted message can attempt to steer the model toward whatever tools are
enabled. The gates above bound the blast radius, not the attempt. The
recommendation is to keep tools off (the default) when chatting over
mail synced from sources you do not trust, and to enable `runs` only in
trusted sessions.

## Telemetry and crash reporting

**Default: no telemetry.** There are no analytics, phone-home checks, or
usage counters, and Mono Agent makes no outbound calls on its own behalf.

**Crash reporting is local by default.** If the CLI crashes, it writes a
crash report to a file under `~/.monoagent/crashes/` on your machine —
nothing is transmitted. Filing a crash report to GitHub happens only when
**both** of the following are true:

- the environment variable `MONOAGENT_CRASH_REPORT=1` is set, and
- the `monomind` CLI is installed and on `PATH`.

Without either condition, crash data stays in the local file. There is no
automatic network fallback for crash reporting.

**Complete list of exceptions** — the only situations in which Mono Agent
makes network requests:

1. API calls made by workflows you run, against services you configured
   (HTTP nodes, service nodes, browser nodes, etc.)
2. Commands you explicitly invoke that talk to an external service — for
   example `login` (OAuth flows) or `update` (release check/download)
3. Opt-in crash reporting as described above

Everything else — workflow definitions, execution history, the secrets
vault, CRM data, and crash reports — stays on your machine.

## Resource limits

**Importing or running a workflow is equivalent to executing code** —
workflows can run shell commands, inline JavaScript, and HTTP calls, so
only import workflows from sources you trust. To bound the blast radius of
untrusted or misbehaving workflows, runs are resource-capped: HTTP node
bodies are limited to 64 MB by default (configurable); `core.code` nodes
run with a 30 s default timeout (configurable) and process at most 10,000
items of 16 MB per item — an engine-level memory ceiling is not yet
enforced by the vendored JS runtime; `system.execute_command` output is
capped at 10 MB per channel (stdout and stderr). Stored outputs are
persisted in full but display-truncated at 4 KB.
