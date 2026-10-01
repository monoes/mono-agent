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
The passphrase is read from stdin/an interactive prompt (or a chmod-600
file named by `MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE`, see below), never
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

Commands that *also* read their own input from stdin (`secret add` reading
the value, `secret add|update --stdin-json`, `secret import`'s export
passphrase, `jev key set`, `node run --stdin`, `jev --request -`, the
`mcp` server) never read the KEK passphrase from stdin: they prompt on the
controlling terminal (`/dev/tty`) instead. Where there is no terminal
(the desktop app, a service), store the passphrase in a file only you can
read and point `MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE` at it:

```bash
install -m 600 /dev/null ~/.config/monoagent-keyring-pass
$EDITOR ~/.config/monoagent-keyring-pass   # passphrase on the first line
export MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=~/.config/monoagent-keyring-pass
```

The variable holds a path, never the passphrase; a file readable by group
or others is refused. When it is set it wins over stdin and the terminal.
Without it, a command that has no source fails with an error naming it.

Without an environment variable, the same can be configured once with
`monoagentcli secret keyring set-passphrase` (passphrase on stdin, or typed
at a no-echo prompt) — or from the desktop app's **Settings › Vault
keyring**, shown only on hosts with no OS keychain. It writes
`~/.monoagent/keyring-passphrase` (mode 0600, `~/.monoagent` 0700), after
checking the passphrase unlocks every existing file keyring; a passphrase
that doesn't is rejected and nothing is written. `secret keyring status`
reports the key store (`os`/`file`/`unavailable`) and the passphrase
file's state without prompting; `secret keyring clear-passphrase` deletes
it. The passphrase is looked up in this order: the file named by
`MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE`, `~/.monoagent/keyring-passphrase`,
stdin, the terminal. Keeping the passphrase next to the keyring means
anyone who can read your home directory as you can open the vault — the
same trade-off as the environment-variable file.

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
   example `login` (OAuth flows), `update` (release check/download) or
   `library` (the monoes.me library: sign-in, browsing, downloads you ask
   for, and uploads you publish; the host is `MONOES_BASE_URL` or
   https://monoes.me)
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

## Org automations and autonomy

Org roles are AI agents run by monomind. Letting them call workflows, and
letting a model resolve their approvals, crosses a trust boundary. The
layers, from strongest:

1. **Grant rows.** Which automations a role may run, how often, and
   whether a call needs a decision live in mono-agent's database
   (`org_grants`). The org file is only a display copy: any role whose file
   write reaches `.monomind/` can edit it, so saves and the daemon strip
   grants, tool providers, and endpoints no row backs, and never create
   rows from the file.
2. **Grant-mode MCP server.** `mcp --grant` serves only the granted tools,
   takes its scope from the grant row, refuses a grant used by another org
   or role (checked against monomind's `MONOMIND_ORG_NAME`/`ROLE`, which it
   requires — an unset variable refuses the call rather than skipping the
   check), caps calls per run and per day — a cap it cannot read refuses
   too — and returns redacted, size-bounded outputs.
   It runs no engine and holds no vault access; the daemon runs the
   workflow.
3. **monomind policy.** `denyTools`, `approvalTools` (managed by mono-agent
   for `approval: required` grants and `org_start`), budgets, and the
   message fence, which is turned on the first time an org gains an
   automation.
4. **Automation-role endpoints.** `POST /org-endpoint/{id}` on the daemon's
   API. The id is a 130-bit capability, checked in constant time;
   non-loopback callers need the endpoint's 0600 credential file (on
   Windows: owned by the daemon user, SYSTEM or Administrators, with no
   ACE granting anyone else access). A
   delivery runs only after the same `messageId` appears on the org's bus
   addressed to that role, so a role that read the id from the org file
   cannot POST around `org_send`. Rotation keeps the old id for 5 minutes.
5. **Loop control.** Every crossing carries a trace; hops a header claims
   are never trusted below the recorded count, and repeat limits key on
   the target, which a caller cannot forge. `run_config`'s `max_hops` and
   `max_repeats` are read from the org file, so they are clamped to hard
   ceilings — raising them there cannot switch loop control off.
   A chain survives a trip out over HTTP and back through a webhook: the
   HTTP request node sends the run's chain as `X-Monoagent-Trace`, signed
   with a per-machine key (`~/.monoagent/trace.key`, 0600,
   `internal/tracesig`), and the webhook server continues the chain only
   when that signature verifies, recording the crossing (`webhook_in`) and
   refusing the request (429) past the hop limit. The signature covers
   chain, hop and an expiry (one hour), so a replayed token cannot lower
   the hop and stops verifying after the hour. A webhook
   body can never set the chain: the server drops the reserved
   `monoagent_trace` field and gives a run without a verified header a
   fresh chain. The token names only a chain and hop. It goes only to
   this machine (loopback, or the host of `MONOAGENT_WEBHOOK_ADDR`; with a
   wildcard bind, any of the machine's interface addresses or its host
   name, compared and never resolved) unless
   a node sets `propagate_trace`, and never follows a cross-host redirect:
   a third party holding one could push its chain to the hop limit. A
   `webhook_in` crossing takes the signed hop as is (a run's fan-out
   requests are siblings at hop + 1; a loop's return is one hop deeper each
   round); the signed hop is the deeper of the run's and the one its item
   reached on the chain. It is held to the `max_hops` of the org the chain
   started in (the defaults for a chain no org started), and to 200 runs of
   one workflow a minute (429 past that), so replaying a token cannot grow
   `org_bridge_calls` without bound: past either limit only the first
   refusal per minute is recorded. `trigger.org` runs are admitted too
   (`org_event`, or `event_start` on a fresh chain), under the same
   200-a-minute limit and the org's `max_hops`, so a loop that goes out
   through a role's tool event and comes back by a path the ledger does not
   record still climbs. A tool event that is not a granted call (Bash, file
   reads) continues the role's chain only when that workflow's own
   trigger.org run started the chain; an audit workflow is not put on a
   role's chain, and `org_event` rows never raise a chain's depth. The key
   is refused if other users can read it; delete it and restart the daemon
   to rotate it.
6. **Workdir confinement of automation file paths (C-46).** Automations run
   in the daemon, outside monomind's per-role workdir confinement. So the
   grant handler puts the calling role's workdir (computed from the
   profile root in the database and the org's `run_config.workspace`,
   never from the provider's environment) in the run's trigger data as
   `org.workdir`, under `org`, which the role's arguments cannot set. An
   automation role's run gets the sending role's workdir the same way. The
   engine confines every file-touching node of such a run
   (`internal/fsconfine`): spreadsheet read/write, write file, image nodes
   and their output folders, image vault save, email and Outlook
   attachments, Slack and Telegram uploads, FTP local paths, YouTube and
   Drive uploads, browser upload steps, and Gemini reference images. A
   path is resolved relative to the workdir, `..` and symlinks are
   followed, a dangling symlink is refused, and anything that resolves
   outside the workdir is refused with `path escapes org workdir` before
   the file is opened or any request is sent. Runs without `org.workdir`
   behave exactly as before. `org automation list`, `org grant add|list`,
   `org effective-tools`, and the GUI grant dialog flag workflows whose
   file nodes take their paths from the run's input.
7. **Autonomy.** The level, decider, tiers, and policy are enforced from
   `org_autonomy`; an edit to the org file can only lower the level. Tiers
   are computed from the grant row and the workflow's nodes before any
   decider runs, agent-written text sits inside an untrusted fence in the
   decider prompt, a decider never resolves its own request, and every
   routed item is recorded in `org_decisions`. `org autonomy pause` drops
   an org to manual at once.

**Known gaps (accepted for now):**

- **Bash.** A role allowed Bash can run `monoagentcli` with your rights and
  bypass its grants. A role's first grant pre-fills `denyTools: ["Bash"]`
  and the GUI warns when Bash is re-enabled. The org/role check in layer 2
  is no barrier to it either: grant ids are not secrets (every role's
  provider args in the org file carry one), and a role that can run
  commands can also set `MONOMIND_ORG_NAME`/`ROLE` to match a sibling's
  grant. That check catches a copied id and a misconfigured provider, not a
  role that already has a shell.
- **File paths in automation input (C-46), what confinement does not
  cover.** Layer 6 holds file nodes to the role's workdir; it does not
  hold `system.execute_command`, which runs a shell command with your
  rights — a granted workflow that passes its input to a command can reach
  any file, as a role with Bash can (the grant dialog names such nodes).
  A process that swaps a directory for a symlink between the check and the
  open is not stopped. The workdir is read from the org file, so a role
  that can write that file (workspace `repo`) can widen it — the same
  edit widens its own confinement at the next org start. A message from
  an org this profile folder cannot resolve is held to the profile folder,
  and a message from a person is not confined.
- **Level full.** At `full` a model decides irreversible items (gates,
  grants whose workflows send email or post) with no person involved. The
  fence and tiers reduce prompt-injection risk; they do not remove it.
  Choose `full` only for orgs whose automations you would accept being run
  on a wrong decision.
- **Cross-root messaging.** Orgs under the same profile folder can always
  message each other. Restricting messages between profile folders needs
  monomind with capability `org-federation`.

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

## OpenAI-compatible API surface

`monoagentcli httpapi` and `monoagentcli daemon` serve `GET /v1/models`,
`GET /v1/models/{id}` and `POST /v1/chat/completions` over the agent
runtimes installed on the machine (`internal/openaiapi/`). A request is a
real agent turn run through `monomind agent exec`; this section is what that
means for exposure. Setup: `examples/openai-api-quickstart.md`.

**Credentials.** A key is `sk-ma-` plus 32 random bytes and belongs to one
profile. Only its SHA-256 is stored (table `api_keys`), so it is shown once,
at creation, and verifying a request needs no vault and no keyring. There is
no authentication cache: `api key revoke` and `org teardown-profile` take
effect on the next request. A request authenticates as the key's profile and
as nothing else; a key of another profile is "not found" in every command
except `api key list --all-profiles`, which lists every profile's keys
(metadata only, never a secret) to whoever can run it on this machine.
The legacy HTTP API token (vault entry `httpapi-token`) is a separate
credential for the other routes: a key never opens them and the token never
opens `/v1`. Treat a key like a password; it has no scopes and no expiry.

**What a key can make this machine do.** Every request starts one agent turn
in a slot folder of its profile, `~/.monoagent/workspaces/api/p-<hash>/slot-N`
(`<hash>` is a hash of the profile id, so two profiles never share a folder;
never the profile's own folder; emptied before and after every turn), with no
tools, no settings and the workspace-write sandbox where the runtime has
one. What that confines depends on the runtime, and
`monoagentcli api models` reports it per model instead of pretending
otherwise:

| Class | Meaning | For example |
|---|---|---|
| `chat-only` | monomind's allow-list gate is the only tool gate, and a request carries no caller tools, so no native tool is reachable | claude |
| `sandboxed` | writes are confined to the turn's folder; reads and the network are open, and the runtime's own configuration still applies | codex |
| `unconfined` | the runtime's native tools run as the OS user | antigravity, and any runtime monomind does not vouch for |

Which runtime lands in which class follows what monomind reports, so it can
change with a monomind upgrade. `sandboxed` confines writes, nothing more: a
runtime keeps the MCP servers and instructions files of the OS user it runs
as, so a key holder can ask codex to use any tool you configured for it.

The class is decided from `monomind agent scan` and fails closed: anything
not vouched for is `unconfined`. A `sandboxed` model's turn is started with
the sandbox required, so one whose sandbox cannot be applied is refused (403)
instead of running unconfined. The class is checked again when the turn
starts, against the confinement the `start` event reports (monomind 2.22
reports it), and a turn that is weaker than the listener's policy is
cancelled (403 `policy_denied`) before any of its text reaches the caller. A
streamed response is committed (status 200) on its first content or after 5
seconds of silence, so a refusal later than that is an error event in the
stream rather than a 403. A non-streaming response also carries monomind's
sandbox verdict for the turn in `X-Monoagent-Sandbox` (`sandboxed`, `scoped`,
`unsupported`, `awaiting-monomind`, `needs-monomind` or `off`), which is not
the class above; a streamed response has none.

Be precise about what is and is not guaranteed. claude's `chat-only` rests on
monomind's design (its allow-list gate); the live check
`MONOAGENT_LIVE_API_TESTS=1 go test ./internal/openaiapi -run TestLiveClaudeIsChatOnly`
tries to make it do otherwise, and until you have run it on your machine, read
the class as the design, not a measured guarantee. codex under
`workspace-write` can still read files outside its folder, and antigravity
can run a shell command with the permissions of the OS user. **Run the server
as a dedicated unprivileged OS user**, with nothing of value readable by it,
before giving a key to anyone you would not give a shell.

**Exposure.**

- `/v1` rides the main HTTP API listener only while that bind is loopback
  (default `127.0.0.1:9322`), where every runtime is served. Off-loopback the
  main listener never serves `/v1`; it logs a pointer to `--v1-addr`.
- `--v1-addr` (`MONOAGENT_API_V1_ADDR`) starts a dedicated listener that
  serves only `/v1/*` and `GET /health`: no workflow, node, HIL or org
  endpoint exists on it. Any non-loopback bind is served only over TLS, with
  no way to serve it in the clear: `MONOAGENT_API_TLS_CERT` and `_KEY`, else
  a self-signed certificate cached under `~/.monoagent/api-tls/` (key file
  mode 0600) that covers `localhost` only, so remote clients reject it until
  they trust it explicitly. Set a real certificate, or terminate TLS in a
  reverse proxy. The two variables also make a loopback `--v1-addr` bind
  speak TLS, so a proxy that forwards plain HTTP needs them unset. The webhook
  server follows the same rules (see above). `httpapi` exits when the
  listener cannot start; `daemon` prints a warning and keeps running without
  it, so check `api status` after starting it.
- The default confinement is `chat-only` off-loopback and `any` on loopback.
  Behind a reverse proxy the bind is loopback, so **set `--confinement`
  (`MONOAGENT_API_CONFINEMENT`) explicitly**; it is one value for every
  listener of the process, so it also limits the loopback main listener. A
  model above the policy is not listed, `GET /v1/models/{id}` answers 404 for
  it and a completion that names it answers 403.
- TLS protects the key in transit; it does not limit who may try one. There
  is no rate limit per caller and no lockout, so a key is only as safe as it
  is long and secret (256 bits). Put a proxy or a firewall in front of a
  listener that faces the internet. There is no CORS: browser clients are out
  of scope.

**Context keys.** A key created with `--context` adds up to five excerpts
(1,200 characters each, source base names only, never paths) from that
profile's own documents and captures to the system prompt, framed as data
whose instructions must be ignored. Captured web pages are in that knowledge
and nobody vetted them, and the framing reduces prompt injection without
removing it, so **by default a context key is served only by `chat-only`
models**: a runtime with native tools could be steered into using them.
`--context-confinement sandboxed|any` (`MONOAGENT_API_CONTEXT_CONFINEMENT`)
raises that on purpose, for example to give a coding agent on your own
machine your notes. It never goes above the listener's own `--confinement`,
and raising it accepts that a captured page could steer that runtime. The
excerpts leave the machine like any prompt, to the runtime's provider. The
personal brain and other profiles are never searched.

**Cost and abuse limits.** There are no per-key quotas: every request is a real
model turn on your subscription or account, and some runtimes report no cost.
The bound is the concurrency cap (4 turns, 429 beyond it; `--max-concurrent`),
the 2 MiB request body and the 10 minute turn timeout. A request that is
rejected (invalid, over policy, or busy) starts nothing.

**Logs and errors.** One line per chat completion names the request id, key
id, profile, model, status, duration and how many knowledge excerpts were
added, plus, for a failure, the operator-only detail (a Go error or a
runtime's error code). A failure to list models is logged the same way; a
successful listing is not logged. No line holds a prompt, an answer or a key.
A failed authentication is logged with the caller's address, never the key it
sent (a request that sent no credential at all is not logged). Error messages
sent to callers are generic for internal failures and for a runtime error
other than a setup hint, a rate limit, quota or a timeout, and every response
of the three routes carries an `X-Request-Id` to quote to the operator. A
path or method the API does not have gets Go's plain-text 404 or 405, without
one.

**What this does not cover.**

- Revocation applies to the next request: a turn already running finishes,
  within the turn timeout. Stopping the daemon or `httpapi` ends the turns in
  flight and waits for their processes to be killed.
- The agent CLIs keep session transcripts of their turns in their own stores
  (claude's `~/.claude/projects/<folder>`), prompts and answers included, as
  they do for any use. The fixed slot folders keep the number of those
  folders bounded (one per profile and slot), not their content, and keep
  one profile's apart from another's.
- Some runtimes (antigravity) pass the prompt on their command line, which
  other local users can read with `ps` while the turn runs. So does a context
  key's knowledge search, for every runtime, claude included: it runs
  `monomind mcp exec -t knowledge_search` with the first 500 characters of the
  last user message in its arguments, for up to 30 seconds, in two parallel
  processes (documents and captures). On a shared host, run the server on a
  machine or an OS user of its own.
- A turn's prompt and system prompt (which carry a context key's excerpts) are
  written to files in a private folder (mode 0700) under
  `~/.monoagent/workspaces/api/.tmp`, outside every turn's writable area, and
  removed when the turn ends.

**Not part of this surface (yet).** Image generation, OpenAI tool calling and
Jev's `auto` model are later phases. Today a request cannot hand the agent
tools of the caller's own (a non-empty `tools` is rejected); the runtime's native tools are
a separate matter, covered by the classes above.
