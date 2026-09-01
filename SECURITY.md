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
available, the key-encryption key (KEK) is stored in a per-profile file at
`~/.monoagent/vault/.file-keyring-<profileID>` (permissions 0600) instead
of the OS keyring, and the CLI prints a warning whenever it uses it. This
is deliberately opt-in and still weaker than a real OS keychain's
process-scoped access control — but the KEK is no longer stored raw.

The file holds the KEK **wrapped** with AES-256-GCM under a key derived
from an operator-supplied passphrase via argon2id (same tuning as the
vault export/import format — see `internal/secrets/export.go`), in a JSON
envelope alongside its salt and nonce (`internal/secrets/filekeyring.go`).
The passphrase is read from stdin/an interactive prompt only, never
accepted as a CLI flag or environment variable — the same anti-argv
pattern `secret add` and vault export/import already use, since both flags
and env vars leak through shell history and process listings. Without the
correct passphrase, the file alone (e.g. leaked via a backup or a
misconfigured volume) is no longer sufficient to decrypt the vault —
unlike the pre-hardening raw-KEK format. Payloads remain AES-256-GCM
encrypted either way; the passphrase adds a second factor specifically for
the KEK-at-rest.

**Migration from the old raw-KEK format:** a vault created before this
hardening has its file-based KEK stored as 32 raw bytes with no wrapping.
The first read after upgrading detects this (the file isn't valid JSON but
is exactly 32 bytes), prints a migration warning, prompts once for a new
passphrase, and re-wraps the *same* KEK bytes into the new envelope format
in place — existing `vault_keys` rows (and therefore every already-stored
secret) keep decrypting correctly, since only the KEK's on-disk
representation changes, not its value. Set the new passphrase when
prompted; there is no separate manual migration step required.

Use the fallback only where no keyring exists (headless CI, containers);
without the `MONOAGENT_ALLOW_FILE_KEYRING` variable set, secret operations
fail closed.

**Headless/CI use:** pipe the passphrase via stdin from a masked CI
secret — never as an argument or plain environment variable:

```bash
echo "$VAULT_PASSPHRASE" | monoagentcli secret reveal my-key --reveal
```

Each CLI invocation that actually needs the KEK (decrypting a secret via
`secret reveal`/`secret get`, adding one via `secret add`, or a `workflow
run`/`daemon` process resolving `@secret:` refs) prompts for the
passphrase on stdin the first time that process touches it; further
operations *within that same process* — e.g. every `@secret:` resolution
during one `daemon` run — reuse the in-memory KEK without prompting again
(the OS-keychain path memoizes identically — see
`internal/secrets/keyring.go`'s `getOrCreateKEK`). Separate CLI
invocations are separate processes, so each one prompts once. Store
`VAULT_PASSPHRASE` as a masked/protected CI secret and pipe it into each
invocation that needs it.

This implies a stdin conflict for commands that *also* read their own
payload from stdin (`secret add --stdin-json`, `secret update
--stdin-json`): the KEK passphrase and the command's JSON payload cannot
both come from the same stdin stream in one invocation. For those, use
`--field`/`--value` (still never `--value` for real secrets outside of
disposable CI fixtures — the JSON payload flags land in shell history the
same way argv secrets do) or a bootstrap step that avoids `--stdin-json`.

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

## Release governance

`release.yml` triggers on every push to `master`, and its `release` job
(which publishes the GitHub Release) targets a `release` GitHub Environment.
This is only an enforced approval gate once the following are configured in
repo settings — until then it is a no-op:

1. **Settings → Environments → New environment**, named `release`, with
   **Required reviewers** set to at least one maintainer.
2. **Settings → Branches → Branch protection rule** for `master`: require
   pull request review before merging, require status checks to pass
   (`test`, and the `mcp-pin-guard`/`vuln-scan` CI jobs), and disallow
   force pushes and branch deletion.
3. **Settings → Tags → New rule**: protect `v*` so a published release tag
   cannot be silently moved or replaced.

## Verifying a release

Every release publishes `SHA256SUMS.txt` alongside the binaries, and the
release workflow attaches a [SLSA build provenance
attestation](https://github.com/actions/attest-build-provenance) to every
file it produces (including the checksum file itself), signed via GitHub's
OIDC-backed Sigstore integration — no maintainer-held key involved. Verify a
downloaded artifact was actually built by this repo's release workflow from
the commit it claims:

```bash
gh attestation verify monoagentcli-darwin-arm64 -R monoes/mono-agent
```

This proves the artifact's hash matches what GitHub Actions produced for a
specific commit in this repository — it does **not** yet carry a personal
code-signing identity (see below), so on macOS/Windows you will still see an
unidentified-developer warning until that lands.

### Code signing (in progress)

The macOS CLI binary is currently signed ad-hoc (`codesign --sign -`), which
satisfies Gatekeeper's local-execution requirement but carries no verifiable
publisher identity, and Windows binaries are not yet Authenticode-signed.
Real Developer ID signing + notarization, and Authenticode signing, are
tracked as a follow-up (MA-07) pending the relevant certificates. Once
available, the required repository secrets are:

| Secret | Purpose |
| --- | --- |
| `APPLE_CERT_P12` | Base64-encoded Developer ID Application certificate (.p12) |
| `APPLE_CERT_PASSWORD` | Password for the above .p12 |
| `APPLE_TEAM_ID` | Apple Developer Team ID |
| `APPLE_NOTARY_APPLE_ID` | Apple ID used for notarization |
| `APPLE_NOTARY_PASSWORD` | App-specific password for that Apple ID |
| `WINDOWS_CERT_PFX` | Base64-encoded Authenticode code-signing certificate (.pfx) |
| `WINDOWS_CERT_PASSWORD` | Password for the above .pfx |

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

## Webhook trigger surface

The webhook trigger server (`internal/workflow/webhook_server.go`) binds
`127.0.0.1:9321` by default — loopback-only, same-user trust, plain HTTP
with no extra ceremony (the same trust model the Chrome extension bridge
uses). Override the bind with `MONOAGENT_WEBHOOK_ADDR` when triggers need
to be reachable from another machine (a Docker container, a VM, a LAN box).

**Any non-loopback bind is always served over TLS — there is no way to
serve a remote bind in the clear.** Webhook payloads can carry
caller-attached auth headers/tokens, so an unencrypted remote listener
would leak them on the wire. If no certificate is configured, the server
auto-generates and caches a self-signed one (ECDSA P-256, ~13 months
validity, covering `localhost`/`127.0.0.1`/`::1` only) under
`~/.monoagent/webhook-tls/` (key file mode 0600) on first bind, and reuses
it until it expires. Callers connecting by LAN hostname or IP must skip
certificate verification against this self-signed cert, or trust it
explicitly. For a real certificate, set both `MONOAGENT_WEBHOOK_TLS_CERT`
and `MONOAGENT_WEBHOOK_TLS_KEY` to its path/key path — setting only one of
the pair is a startup error rather than a silent fallback.

**TLS protects the payload in transit; it does not authenticate the
caller.** The webhook server itself has no built-in auth beyond TLS — the
existing pattern is workflow-level: give each webhook trigger node an
`auth_header`/`auth_token` pair or an `hmac_secret` (see
`examples/README.md`), which the server checks per-request
(`X-Hub-Signature-256` for HMAC, or a constant-time comparison for the
static header token). Anyone who can reach a remotely-bound port and
guesses the trigger's `<path>` segment can otherwise fire it. Pair this
with `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS` (a comma-separated origin
allowlist) if browser-based callers need to reach it; without it the
server emits no CORS headers at all, so cross-origin browser requests are
blocked outright — see [Runtime environment variables in
AGENTS.md](AGENTS.md#runtime-environment-variables).
