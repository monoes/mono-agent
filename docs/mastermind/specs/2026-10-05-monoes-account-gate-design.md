# Mandatory monoes.me Account — Design Spec

Date: 2026-10-05
Status: Draft for the owner's review. The design (§4 to §11) was presented in sections and approved in conversation on 2026-10-05: the open list, the cancel-versus-finish rule, the rollout, the headless and developer paths and the license workstream included. The decisions marked "lead" in §2 are details this document adds and need a read.
Branch: `feat/monoes-account-gate`, cut from master `f4441a2a` (v0.106.1). The work spans two repositories: this one (the client) and `monoes/monoes-landing` (monoes.me, the server, §5).
Plan: twelve plans and an index, `docs/mastermind/plans/2026-10-05-monoes-account-gate-*.md`. Writing them against the real code and server corrected parts of this spec: the amendments are in §13 and override the earlier text where they differ.

## 1. Goal

Nobody uses mono-agent without a monoes.me account that is signed in on the machine. The official builds (the CLI, the daemon, the desktop app, the HTTP and `/v1` APIs, the MCP server and the extension bridge) refuse to do anything until a valid, server-signed session exists, and stop when monoes.me disables the account or has been unreachable for 24 hours.

Why (the owner picked all four): know who uses it; cut off abusive accounts; leave room for paid plans; control the official app.

Boundary: this gates the official builds. It is a client-side check in a public MIT repository, so a build from source or a patched binary can remove it, and releases before the enforcement release stay ungated. The license will change and the source may be closed soon (§10); that raises the cost of removal and, depending on the terms chosen, makes it a license violation, but does not make it impossible. No product value moves to monoes.me beyond what the library already serves (web automations, orgs, workflows).

Acceptance (§11 turns each into tests):

1. After the enforcement date, with no valid session, every gated command exits 4 with `login_required` and does nothing else (no first-run writes, no database open), and every door of §6.3 refuses.
2. Signed in, then blocked on monoes.me: locked within one refresh interval (about an hour), and work in flight is cancelled.
3. Signed in, monoes.me unreachable: works until 24 hours after the newest token was issued, then locked; work in flight at that moment finishes.
4. Before the enforcement date nothing locks, and once a date is set every surface warns.
5. A release binary built with the `devaccount` tag cannot ship.

## 2. Decisions register

| # | Decision | From |
|---|---|---|
| D1 | The reasons for the gate are all four: know who uses it, cut off abusive accounts, leave room for paid plans, control the official app. | user |
| D2 | Strictness: official builds only. No value moves to monoes.me beyond web automations, orgs and workflows. The license changes; closing the source is possible soon and nothing here waits for it. | user |
| D3 | The offline grace is 24 hours from the last successful contact with monoes.me. | user |
| D4 | One machine-wide session, signed by monoes.me and verified on the machine against keys pinned in the binary. No network call per command; the daemon, the APIs and MCP check again while they run. | user |
| D5 | monoes.me answering *no* (blocked, revoked) ends access at once. monoes.me *unreachable* is tolerated until 24 hours after the newest token was issued. | user |
| D6 | Default-deny, one gate before any command runs. Open: `version`, `help`, `completion`, `ref`, `update`, `doctor` (with `doctor fix`), `setup` and the `account` commands. Everything else is gated, purely local commands included. | user |
| D7 | Three layers: the CLI gate; checks in `handleExecution`, `monomind.Exec` and `ActionExecutor.executeDef`; the doors (HTTP API, `/v1`, webhook server, extension bridge, MCP). | user |
| D8 | A locked daemon stays up, starts nothing new, stops the org services it manages, reports the state and resumes by itself. In-flight work is cancelled when monoes.me refused, and left to finish when it was only unreachable at the 24 hours. | user |
| D9 | Server first. Then one client release with the enforcement date built in, about three weeks after it ships: warnings before, enforcement after, no second release. Older versions stay ungated. | user |
| D10 | No credential through an environment variable and no unattended machine tokens in v1. Headless sign-in is `account login --email`, kept in the data directory. | user |
| D11 | Developers and CI run the real binary built with `-tags devaccount`. Releases never carry the tag. | user |
| D12 | License and distribution are their own workstream (§10). The gate ships first and works under any license. | user |
| D13 | The proof is an EdDSA-signed JWT access token verified with the standard library: no new dependency, one accepted algorithm, nothing negotiated from the token's header. | lead |
| D14 | Checked claims: the issuer, the audience, the client claim, `sub`, `iat`, `exp`, and the `kid` against the pinned set. The exact claim names and values come from a real minted token (spike S6), not from this document. A token whose lifetime exceeds 24 hours, or whose `iat` is more than 5 minutes ahead, is refused. `plan` is read and not enforced. | lead |
| D15 | The grace runs from the signed `iat` of the newest token. A clock guard refuses a clock that went back; a freshly signed token resets it. | lead |
| D16 | The session lives outside the profile vaults, in `~/.monoagent/account/`: `session.json` (the signed access token and state, readable without the keychain) and `refresh.enc` (the refresh token, sealed under the keyring key, touched only when a refresh is due), behind a lock file. | lead |
| D17 | One `account.Guard` per process. Long-running processes also refresh at half the token lifetime and cancel their work when the session is refused. | lead |
| D18 | Keys are pinned in the binary as a set (current and next) with a rotation procedure. How the server signs with a pinnable key is spike S1; the fallback is a JWKS cached over TLS. | lead |
| D19 | Server changes are limited to audience-bound JWTs, a constant `plan` claim, blocking that revokes tokens, a pinnable signing key and a deploy workflow that only deploys `main` (§5). | lead |
| D20 | The CLI gate runs in `main()` before cobra executes, from a per-command annotation, not from the root `PersistentPreRun`. A test pins the command tree and the open list. | lead |
| D21 | `account login`, `logout` and `status`; `library login`, `logout` and `status` become aliases of the same session. | lead |
| D22 | The enforcement date is a constant in `internal/account`, judged against the guarded clock. Before it nothing locks. Until the release that sets it the constant is the zero time, which means dormant: nothing locks, nothing warns and nothing is called implicitly (no adoption, no refresh, no background refresher), so earlier phases can ship safely. | lead |
| D23 | Once the gate is no longer dormant, a library login that already exists is adopted only if its refresh token can be exchanged for an audience-bound JWT (spike S2); otherwise the user signs in once more. Whatever the server answers to that attempt, it is never read as a refusal. | lead |
| D24 | `devaccount` adds a development signing key and lets the gate honor `MONOES_BASE_URL`. The release workflow fails if a binary carries the tag. With no guard installed `Require` fails closed, except inside a test binary (`testing.Testing()`), where a strict hook turns the exception off for the gate-site tests; unit tests swap keys through a hook that panics outside a test binary. | lead |
| D25 | The `doctor` row is `core.monoes_account`; the existing `accounts` group is about third-party platform logins. | lead |
| D26 | Documentation is part of the work (§10 step 5): every claim of local-first, no telemetry, no phone-home or offline use is rewritten to what is true. It lands no later than the first phase that makes implicit calls to monoes.me (B5). | lead |
| D27 | A refusal is only `invalid_grant` answered to a refresh-token grant. Every other failure (other OAuth errors, any 4xx or 5xx, no network, a timeout) is not a decision about the account and counts as unreachable, so the grace applies. monoes.me deploys on every push, and one bad deploy must not log out every online install. | lead, after the review of this spec |
| D28 | A running daemon keeps the code it started with, so `update` and `update --app` restart a registered daemon, and `doctor` flags a daemon whose heartbeat version differs from the binary's (§8). Without that, release R would not gate the process that runs the schedules. | lead, after the review of this spec |

## 3. Verified facts

Checked 2026-10-05: the client at master `f4441a2a` (v0.106.1), by a read-only survey of the code (no binary run), and the server (`monoes-landing`, `main`) through its source on GitHub. Function names are stable; line numbers drift. Not examined: monomind's internals, and the token lifetimes of the deployed server (the docs page shows `expires_in: 3600`; the deployed server may differ from `main`).

**The existing monoes.me login.**
- `internal/library` (`auth.go`, `store.go`, `types.go`) is an OAuth 2.1 authorization-code and PKCE client: public client id `monoagent`, loopback redirect `127.0.0.1:<random>/callback`, scopes `openid profile email offline_access library:read library:write`. The headless alternative is an emailed code (`POST /api/auth/agent/claim` and `/claim/verify`). The host is `https://monoes.me`, overridable by `MONOES_BASE_URL` (plain http only for a loopback host).
- The token (access, refresh, expiry, user) is stored per profile and per host in the profile's vault: entry `monoes-library`. Refresh is lazy (within a minute of expiry, and once after a 401); nothing refreshes in the background. When a refresh fails the stored token is still returned ("let the server decide").
- Only `library *` reads it. `requireLogin` refuses locally with exit 4 and `login_required: true` under `--json`, and the desktop's library dialog already shows a login gate (`components/library/LogInToMonoesButton.jsx`, `services/library.js`). Since v0.88.0 (PR #211) monoes.me refuses anonymous library reads: there the server is the enforcement.
- Exit codes are 0 to 4 (`cmd/monoagentcli/exitcodes.go`: `cliError`, `errAuthConnection`, and `loginRequiredError` with `JSONErrorFields`). `reportCommandError` adds `{"error": …}` on stdout only for `org` and `status` under `--json`; other commands wrap their own errors.

**CLI structure.**
- `main()` runs `newRootCmd().ExecuteContext`. The only global hook is the root `PersistentPreRun` (`runClaudeFirstRunCheck`, `nodemgr.Activate`, `bootAutomationsFor`). It returns no error; `setup` and `doctor` replace it instead of chaining; `library` and `org` set only `PersistentPostRun`; `EnableTraverseRunHooks` is not used. The first-run check runs on every command, `version` included.
- There are 41 top-level commands. `login` and `logout` capture social-platform sessions and have nothing to do with monoes.me. There is no `account` command.
- The database is opened per command by `initDB` (migrations on every call), after the pre-run. A gate that reads a profile vault would have to open the database first.

**Choke points.**
- Every workflow execution (schedule, webhook, manual through `TriggerWorkflow`, `workflow run`, MCP `workflow_run` or `POST /workflows/{id}/run`, org bridge, HIL resume, retry) ends in `WorkflowEngine.handleExecution` (`internal/workflow/engine.go`), the queue's handler. A refused execution is recorded with `persistExecutionFinished(…, "FAILED", msg)`; `CancelExecution(id)` exists.
- Every agent turn started by this repo goes through `monomind.Exec` (`internal/monomind/exec.go`): chat, coder, the dynamic org, `agent`, the `/v1` gateway, `agent.ask`, summaries, matching and validators.
- Paths that skip both: `node run`, `login`, `crawl`, `capture page`, `connect oauth`, `application apply` and `send`, `automation test`. Their browser actions reach `ActionExecutor.executeDef` (`internal/action/executor.go`). Purely local commands (people, secret, image, profile, config) touch neither.
- Org runs execute in an external `monomind org serve` process and reach mono-agent only through `mcp --grant`, which runs workflows in the daemon (so through `handleExecution`).

**Doors.**
- The daemon is single-instance, with one engine for all profiles and a heartbeat file written every 10 seconds (`internal/daemonhb.Heartbeat`). It starts the HTTP API, the extension bridge and the org services. Autostart is a launchd KeepAlive agent, a systemd user unit or a Windows task; the Docker `ENTRYPOINT` is `daemon`.
- The HTTP API (127.0.0.1:9322) enforces a bearer in `Server.auth`; `GET /health` is open. `/v1` and the org receiver are mounted as extra routes with their own auth, and the dedicated `/v1` listener is optional.
- The webhook server (default 127.0.0.1:9321) is started by every engine start, with an HMAC per trigger.
- The extension bridge (9222, falling back to 9323) is a loopback WebSocket with a shared secret in `~/.monoagent/extension.token`; the first frame authenticates (`authenticate`, close code 4401).
- MCP is stdio, in modes read-only, `--allow-mutations`, `--api-only` and `--grant <id>` (spawned by monomind per org role).
- The desktop app (`wails-app`) does not embed the engine: about 298 bound methods, every execution a `monoagentcli` subprocess found by `findMonoAgentCLI`, a few direct database reads, and a `backgroundUpdateCheck` at startup and every 24 hours. Its shell has no login or onboarding gate today.

**Updates and distribution.** `update`, `update --app`, `doctor`'s `core.update`, `install.sh` and the desktop's update check read `https://api.github.com/repos/monoes/mono-agent/releases/latest` and download release assets plus `SHA256SUMS.txt`; a private repository breaks all of them. Every push to master auto-releases (`release.yml`); CI uses no secrets. Docker images are built from source only.

**Documentation claims.** README, AGENTS.md, SECURITY.md, SUPPORT.md, `docs/COMPARISON.md` and the locale strings say local-first, no telemetry, no phone-home checks, works offline. The desktop's 24-hour update check already contradicts the phone-home claim.

**Tests and CI.** `cmd/monoagentcli` tests use `testhome.Main` (a throwaway HOME) and 12 of them build `newRootCmd()`. `internal/library/libraryfake` fakes monoes.me (PKCE, refresh rotation, the email code). `scripts/doctor-smoke.sh` asserts that a plain `doctor` on a fresh HOME writes nothing. Go is 1.26 (`testing.Testing()` exists); `go.mod` has no JWT library.

**The server** (public repository, Next.js on Cloudflare Workers).
- Better-Auth with the `jwt()` and `oauthProvider` plugins (v1.7.1), Google and email-and-password sign-in. `OAUTH_SCOPES` include `library:*` and `community:*`. `oauthProvider` is configured without `accessTokenExpiresIn`, `customAccessTokenClaims` or an audience list.
- Per the provider's source, an audience-bound JWT access token is minted when the client sends a `resource` indicator; with none, the access token is opaque. The client sends none today, so today's access tokens are opaque (S6 confirms).
- Blocking (`PATCH /api/community/admin/users/[id]/block`) sets `user.blockedAt` and `blockedBy` only. Enforcement is per route (`middleware.ts`, the community routes) plus a `session.create.before` hook that stops new sessions. Nothing revokes OAuth tokens on a block, and whether the refresh grant refuses a blocked user is untested (S3).
- `.github/workflows/deploy.yml` runs `wrangler deploy` on a push to `main` and also on `pull_request`, with no condition on the deploy step.

## 4. The session

### 4.1 The proof

The access token is a JWS in compact form, `alg` EdDSA (Ed25519), with a `kid`. Required claims:

| Claim | Value |
|---|---|
| Issuer (`iss`) | the authorization server's issuer, a constant of the binary. Better-Auth derives it from its base URL, which includes the auth base path, so it is probably `https://monoes.me/api/auth` and not the bare host; S6 reads it from a real token |
| Audience (`aud`) | a string or an array; must contain the mono-agent audience, an absolute URI chosen in Part A (proposed `https://monoes.me/api/monoagent`; an identifier, not an endpoint) |
| Client (`azp`, or `client_id` as RFC 9068 names it) | `monoagent`; whichever of the two the provider emits |
| `sub` | the user id |
| `iat`, `exp` | issue and expiry; `exp - iat` is at most 24 hours |
| `plan` | optional string, `free` when absent; read, not enforced in v1 |

The client claim is checked because the server allows dynamic, unauthenticated client registration: a token minted for some other client must not pass. The names and values above are proposals until S6 has read them from a real token: a wrong constant would make the verifier reject every real token, so B1's constants come from S6. The refresh token is opaque (`offline_access`).

### 4.2 Verification

`account.Verify(token, now)` returns a receipt (`sub`, `plan`, `issued_at`, `expires_at`) or a typed reason: `invalid` (structure, signature or claims), `key_unknown` (`kid` not in the pinned set), `clock_skew` (`iat` more than 5 minutes ahead). It accepts one algorithm and only pinned keys, ignores `jku`, `jwk` and `x5u` headers, and uses `crypto/ed25519` with a small strict JWS parser (no new dependency: D13).

### 4.3 States

| State | When |
|---|---|
| `ok` | a receipt verified and `now < exp` |
| `grace` | a receipt verified, `now >= exp`, `now < iat + 24h`, and no refresh has been refused; the reason says why it was not refreshed: `unreachable`, `server_error` or `keyring_unavailable` |
| `locked` | anything else, with a reason: `not_logged_in`, `expired` (past `iat + 24h`), `refused` (monoes.me answered no), `clock_rollback`, `clock_skew`, `key_unknown`, `invalid` |

Before the enforcement date (D22) `Require` returns nil whatever the state, and the state is still computed and reported.

### 4.4 Refresh

- **When.** A CLI process refreshes when the token has under 5 minutes left, or has expired and the last attempt was more than a minute ago (`last_attempt` and `last_result` in `session.json` are the negative cache), with a 2-second connect timeout, so an offline CLI call pays at most that once a minute. Long-running processes refresh at half the token lifetime and, while unreachable, retry with backoff (30 seconds doubling to 5 minutes).
- **What counts as a refusal (D27).** Only `invalid_grant` answered to a refresh-token grant. Every other failure is not a decision about the account and counts as unreachable for the state, so the grace applies: no network or a timeout (`unreachable`), and any other OAuth error (`invalid_client`, `invalid_target`, `invalid_scope`, …) or a 4xx or 5xx answer (`server_error`). monoes.me deploys on every push, and one bad deploy must not log out every online install or cancel their work. On a refusal the process deletes `refresh.enc`, writes `state: refused` into `session.json` (keeping the user for the message) and the state becomes `locked(refused)` until a new sign-in. The adoption attempt of D23 never produces a refusal: whatever it is answered, the outcome is "sign in once more".
- **A keyring that cannot be opened** (unavailable, or access denied) counts as unreachable for the state, so the grace applies; `account status` and `doctor` report it as `keyring_unavailable` so it is not mistaken for a network problem.
- **The request** carries `resource=<audience>`.
- **Concurrency.** An exclusive lock on `account/session.lock` surrounds read, refresh and write. After taking it the process re-reads the session and skips the network call if another process already refreshed (a newer `iat`). The rotated refresh token is written before the lock is released. A crash between the server's rotation and that write loses the refresh token and the user signs in again (accepted).

### 4.5 Clock guard

`session.json` keeps `hw`, the highest time the gate has seen (written at most once a minute). `now < hw - 5 minutes` is `locked(clock_rollback)`. A newly verified token resets `hw` to its `iat`: server time is authoritative, and this is also the way out after a legitimately wrong clock. The enforcement date is judged against `max(now, hw)`.

### 4.6 Storage

```
~/.monoagent/account/
  session.json   0600  {"v":1,"host","access_token","user":{"id","email","username"},"plan",
                        "hw","last_attempt","last_result","state","reason"}
  refresh.enc    0600  the refresh token, AES-256-GCM under a KEK from the OS keyring
                       (or the file-keyring fallback), as the vault does it
  session.lock   0600  lock file
```

Outside the vault because the gate runs before the database is opened, the daemon serves every profile, the vault is per profile and keyring-bound, and a keychain call per command would bring back issue #54 (macOS keychain prompts in background runs). `session.json` is readable with no keychain call and holds a token that expires within the hour. There is one session per OS user, shared by all of that user's profiles, and the path is `~/.monoagent` whatever `--db-path` says.

### 4.7 Pinned keys and rotation

`internal/account/keys.go` embeds a set of `{kid, Ed25519 public key}`: the current key and the next. Rotation: (1) a client release ships the next key; (2) after a fixed waiting period (eight weeks suggested, the owner's call: the server cannot see which versions are installed, since nothing reports them) the server starts signing with it; (3) a later release drops the old key. A client that missed step 1 locks with `key_unknown` and says to run `update` (open). A compromised key follows the same path, faster. How Better-Auth's `jwt()` plugin (which keeps and rotates its own keys in the database) signs with a key we can pin is spike S1; the fallback is a JWKS fetched over TLS from `https://monoes.me/api/auth/jwks`, cached on disk for 24 hours, with the `kid` checked against it. That is weaker, since a CA the user trusts could substitute it.

### 4.8 What this does not stop (accepted)

Patched or source builds; a session file copied to another machine (v1 counts accounts, not devices); a clock set back before the first check (the guard only helps after one); a user who controls the clock and also edits the account folder (below); a blocked user who stays online keeps access for up to the token lifetime, about an hour, unless the refresh refuses sooner; a user who blackholes monoes.me gets up to 24 hours; a monoes.me outage longer than a day locks every install.

The clock guard (§4.5, A23, A25) stops an accidental or naive rollback: a clock set back by hand, a dead battery. It does not stop a user who edits or deletes files in `~/.monoagent/account/` and also controls the clock, because `hw` lives in a file that user can write. Restoring an old `session.json` without its `hw`, deleting `refresh.enc` and freezing the clock inside that token's window `[iat, exp - 5 minutes)` makes the token judge `ok` indefinitely: no refresh ever falls due, so nothing is presented and nothing is refused. Any token that was once valid works, anyone's, a refused account's included. Deleting the token-less record of A23 or A25 likewise un-enforces the date when the clock is then set back before it. On macOS the clock need not change for the whole system: the release workflow signs the macOS binaries ad hoc without the hardened runtime (`release.yml`, job `build-cli-macos`; the CLI inside the app keeps only Go's linker signature), so dyld honours `DYLD_INSERT_LIBRARIES`, and a ten-line `clock_gettime` interposer gives one process a clock of its own. The third security review made a release binary with no record on disk judge `enforced: false` that way; the same binary signed with `codesign --options runtime` ignored the variable and refused. B5a's Task 5b signs every macOS binary with the hardened runtime. A Linux release binary is static Go and cannot be interposed that way, and Windows needs an administrator to change the clock. All of it is the class of patching the binary, accepted above. A26 proposes a build floor that would close the rollback for releases built after the date; it is the owner's decision and is not adopted.

## 5. Server changes (`monoes-landing`)

The server half has its own repository and pipeline. The plan splits into Part A (server, ships first) and Part B (client), so a session in that repository can carry Part A. Every change below is backward compatible: today's client sends no `resource`, so it keeps getting what it gets now.

1. **Audience-bound JWTs.** `oauthProvider` accepts the resource `https://monoes.me/api/monoagent` for the `monoagent` client and `accessTokenExpiresIn` is set to 3600 explicitly. Acceptance: authorize, token and refresh requests with `resource` return a JWT that verifies against the signing key; without `resource` the response is unchanged. The option's real name and shape come from the provider's types (S6).
2. **The `plan` claim**, through `customAccessTokenClaims`, constant `free` for everyone. No database column until plans exist.
3. **Blocking that holds.** After a block, a refresh with that user's refresh token fails with `invalid_grant`; the block route deletes the user's OAuth tokens; unblocking lets the user sign in again. An access token already issued verifies until its `exp` (at most an hour). S3 first checks what the refresh grant does today.
4. **A pinnable signing key** (S1).
5. **Deploy hardening.** `deploy.yml` deploys only on a push to `main` and on manual dispatch; pull requests build and test but never deploy; `main` is protected against force-push with required checks. The signing key and this pipeline become the root of trust of every mono-agent install.
6. **Docs.** The public `docs/authentication` page documents the audience and the claims.
7. **Tests** in the server's suite (`tests/oauth.spec.ts` style plus unit tests): claims and audience, a refused audience, a blocked user's refresh refused, the unchanged opaque path.

## 6. Where the gate sits

### 6.1 Layer 1: the CLI gate

`internal/account` exposes a `Gate`. `main()` builds the root command, resolves the target with `root.Find(os.Args[1:])` before executing, and asks the gate.

- **Classification.** `cmd.Annotations["monoagent.account"]` is `open` or `gated`. A command inherits its nearest annotated ancestor (the root excluded); an unannotated command is gated (default-deny at run time). The root with no subcommand is open: it only prints help or the unknown-command error. `-h`, `--help`, `help` and `completion` are open.
- **Open list.** `version`, `help`, `completion`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), and `library login`, `logout` and `status` (aliases of `account`).
- **Locked, a gated command.** It fails before cobra runs anything: no first-run check, no database open. Exit 4; stderr `Log in to monoes.me first: monoagentcli account login` (with a reason-specific line for `expired`, `refused`, `clock_rollback`, `clock_skew` and `key_unknown`). When `--json` appears anywhere in the arguments the gate also prints on stdout `{"error": …, "code": "auth_or_connection", "login_required": true, "account": {"state": "locked", "reason": …}}`; it emits this itself because it runs before the commands' own JSON wrappers. `loginRequiredError` moves from `library.go` to a shared file and gains the `account` field.
- **Grace.** The command runs; one line on stderr per process, never on stdout: `monoes.me is unreachable; this login works offline until <time>`.
- **Warn period** (§8). The same style: `A monoes.me login will be required from <date>: monoagentcli account login`.
- **Inventory test.** `TestEveryCommandIsClassified` walks the tree, resolves every command's classification and compares the set of open commands with a list pinned in the test, so adding or opening a command needs a deliberate edit (the idiom of `TestEveryRegisteredNodeTypeIsClassified`). How `Find` behaves with global flags before the subcommand, aliases, hidden commands, `--` and `help <cmd>` is spike S5.

### 6.2 Layer 2: inside the engine and the runners

`account.Require(ctx)` reads the process guard's in-memory verdict (no I/O). Without an installed guard it fails closed, except inside a test binary (D24).

- `handleExecution`, after the cancelled-before-dispatch check and before the workflow is loaded. A refusal is recorded with `persistExecutionFinished(…, "FAILED", "login_required: …")` and is not retried. `TriggerWorkflow` and `TriggerWorkflowPersistOnly` check first so a caller gets the typed error; a schedule or webhook trigger that fires while locked is dropped with one log line a minute.
- `monomind.Exec`, as its first statement, returning the typed error, so chat, coder, `/v1`, `agent.ask` and every other turn fail the same way.
- `ActionExecutor.executeDef`, which covers the direct browser and action paths.

Why again behind layer 1: the daemon, MCP and the API processes outlive the CLI check and can lock while they run, and some paths reach the engine with no command at all (webhooks, schedules, org grants).

### 6.3 Layer 3: the doors

| Door | When locked |
|---|---|
| HTTP API (`Server.auth`), `/v1`, the org receiver | 401 with the existing error body plus `login_required: true` and `account`. `GET /health` stays open and reports `account: {state, reason, valid_until}`. |
| Webhook server | 503 `{"error":"login_required"}` with `Retry-After: 60`; no execution is created. |
| Extension bridge | Every request but `ping` gets an error frame with code `account_locked`; `ping` reports the state so the side panel can say "Sign in to MonoAgent". |
| MCP (stdio) | Starts, answers `initialize` and `tools/list`; every `tools/call` returns a tool error (`isError`) with the login-required text, `--grant` children included. |
| Daemon heartbeat | A new `account` field `{state, reason, valid_until}` in `daemonhb.Heartbeat`, read by the desktop and by `doctor`. |

### 6.4 When locked

- `account.Guard` is installed by `main()` for every command. The long-running commands (`daemon`, `httpapi`, `mcp`, `extension serve`, `org serve`) start its refresher at once; any other process starts it after five minutes of running, so a long `workflow run` or `chat` is covered too. The guard re-reads `session.json` when the file changes (a poll every 5 seconds), and offers `Require`, `State` and `OnRefused`.
- The daemon never exits because of the lock (launchd's KeepAlive would respawn it in a loop). Locked, it starts no execution, stops the org services it starts, reports `account: locked`, and resumes by itself when a valid session appears (a sign-in from the CLI or the app writes `session.json`).
- `refused`: `OnRefused` cancels the work in flight, not the server. The daemon calls `CancelExecution` on its running executions; a one-shot process (`workflow run`, `chat`) cancels its command context; the serving processes (`httpapi`, `mcp`, `extension serve`) keep serving and refuse each call as §6.3 says. `expired` (24 hours unreachable): nothing in flight is cancelled, nothing new starts, and the daemon keeps retrying.
- External `monomind` processes that already run (an org's agents) are not killed by mono-agent in v1 beyond what the daemon manages; their calls back into mono-agent fail.

### 6.5 The desktop app

- New bindings `AccountStatus`, `AccountLogin`, `AccountLoginCancel`, `AccountLoginEmailSend`, `AccountLoginEmailVerify` and `AccountLogout`, shelling out to `account … --json`; the `Library*` login bindings become thin aliases.
- `App.jsx` renders an `AccountGate` instead of the shell while the state is `locked`, reusing the library gate's browser sign-in and email-code form. It re-checks on window focus and on every `login_required` answer from any binding. In `grace` a dismissible banner shows the time left; in the warn period a banner shows the date.
- An `account status` call that fails or answers something the app does not know (a stale `monoagentcli` without `account`, found through `MONOAGENTCLI_BIN` or an old PATH copy, or no CLI at all) is treated as locked: the gate names the cause and offers `update`. The gate fails closed.
- `backgroundUpdateCheck` and the update dialog stay available when locked (`update` is open).
- The Go side installs a read-only guard (it re-reads `session.json`), so any layer-2 call it makes is judged. Its watchers keep running and run no workflows; every action it takes goes through a gated CLI call.

## 7. Sign-in experience

- `account login [--email <addr>]` opens the browser flow, or with `--email` sends a code (`--send` and `--code` split the steps, as `library login` does). `account logout` revokes the refresh token at monoes.me (best effort, and not while a refresh whose answer is in doubt is pending: A24) and deletes `refresh.enc` and the token. `account status [--offline] [--json]` refreshes first if due, unless `--offline`.
- `account status --json`:

```json
{"v":1,"state":"ok","reason":"","user":{"id":"…","email":"…","username":"…"},"plan":"free",
 "issued_at":"…","valid_until":"…","grace_until":"…","enforce_from":"…","enforced":true}
```

  `valid_until` is the access token's `exp`, `grace_until` is `iat + 24h`. Exit 0 for `ok` and `grace`, 4 for `locked`.
- New users sign up on monoes.me's existing sign-in page (Google or email); `account login` opens it. No new server UI.
- `doctor` gets the row `core.monoes_account`: ok, warn (grace or the warn period) or fail (locked), with the fix `account login`.
- `library` commands use the machine session; the per-profile `monoes-library` vault entry is no longer written.

## 8. Rollout

Order, because every push to master releases and the server is the dependency:

0. Spikes S1 to S6 (§12) against a local server.
1. The server release (§5). Backward compatible: current clients are unaffected.
2. One client release R: the gate, the `account` commands, the background refresher, the desktop gate, the `doctor` row, and `account.EnforceFrom` set to three weeks after the release (chosen when R is cut). Before the date the gated commands run without a session and warn (a stderr line, the desktop banner, a `doctor` warn, the heartbeat); from the date it enforces. No second release is needed; a later commit may delete the warn path.
3. Adoption. On the first run of R a library login on any profile is exchanged for a machine session when S2 allows; otherwise `account status` says to sign in. Either way nothing locks before the date.
4. Limits. Versions before R are never gated: users who stay on them keep working, and only the library (server-gated) stops for them. Raising their cost is a server decision (a minimum client version on library reads) and out of scope. The warn period reaches unattended installs only through the heartbeat and `doctor`; a user who opens neither sees it when enforcement starts (accepted).
5. A running daemon keeps the code it started with (D28). `update` replaces the binary file, but a daemon that predates R, which launchd's KeepAlive can keep up for months, stays ungated and warns nobody until it restarts, and it is the process that runs the schedules. B5 therefore makes `update` and `update --app` restart a registered daemon (through `daemon restart`; an unregistered one is told to be restarted by hand), and `doctor` flags a daemon whose heartbeat `version` differs from the binary's, with the fix `daemon restart`. The desktop's update flow does the same. An update never interrupts a run: if an execution is in flight the update says so and leaves the restart to `daemon restart`, and the `doctor` row keeps flagging the mismatch until it happens.

The date is judged on `max(now, hw)`, so setting the clock back does not postpone it, unless the user also edits the account folder (§4.8). The constant is the zero time until the release that sets it, and zero means dormant: nothing locks, nothing warns and nothing is called implicitly (no adoption, no refresh, no background refresher). The only calls to monoes.me are the ones an explicit `account` or `library` command makes, as today. That lets the earlier build phases merge, and therefore release, with no user-visible effect, and it leaves the documentation claims alone until B5, the first phase with implicit calls (D26).

**Build order for the plan.** One spec and a phased plan (the API work used the same shape), with the server as its own part:

- **Part A, the server** (§5), after spikes S1, S3 and S6. It ships alone and first.
- **B1, `internal/account`:** verify, states, store, refresh, guard, keys, the fake server's signing, the `account` commands and the `library` aliases (after S1 and S2).
- **B2, the CLI gate:** the annotations, the `main()` gate, the inventory test, the `login_required` JSON and the `doctor` row (after S5).
- **B3, layers 2 and 3 and the daemon's locked mode:** the three runner checks, the doors and the heartbeat field (after S4).
- **B4, the desktop gate** and the extension side panel's message.
- **B5, the rollout:** the enforcement date and the warnings, the post-update daemon restart and the `doctor` version-mismatch row, the release guard and CI changes, and the documentation rewrite. This is the release R above, and the first phase with implicit calls to monoes.me.

The license workstream (§10) runs beside these and blocks none of them.

## 9. Headless, Docker, developers and CI

- **Headless.** `account login --email` once, and the session persists in the data directory. In Docker that means a volume plus the file keyring, which the vault already needs without an OS keyring; `account login` fails closed without a keyring, with the message the vault gives. A locked daemon logs the exact command to run. There is no environment-variable credential and no machine token in v1 (D10): both would need a monoes.me page to issue them, which is the extra server work D2 rules out.
- **Developers and CI** build the real binary with `-tags devaccount` (`internal/account/devkeys_devaccount.go`): it also trusts a development signing key and lets the gate honor `MONOES_BASE_URL`. `release.yml` never sets the tag, and `scripts/check-release-tags.sh` runs `go version -m` on every shipped Go binary (the CLI assets, the Wails app's executable on each OS and the CLI sidecar bundled with the app) and fails the release if any of them lists it.
- **Unit tests** need no tag: `account.SetTrustedKeysForTest(t, keys, clock)` swaps the keys and the clock and panics unless `testing.Testing()`. No environment variable relaxes the gate in a default build; `MONOES_BASE_URL` redirects nothing in a default build (A18), and the gate still verifies against the pinned keys.
- **CI jobs that run the real binary** (`doctor-smoke`: `doctor` is open; `monomind-smoke`, `mcp-pin-guard` and others) are audited in the plan (S4). Any that run a gated command build with `devaccount` and sign in against `libraryfake`'s key.

## 10. License and distribution (its own workstream)

The owner chooses the license terms; this document does not. The engineering steps, in order:

1. **Copyright audit.** `git shortlog -sne` and the CONTRIBUTING terms. Code from other contributors cannot be relicensed without their agreement: replace it or ask.
2. **Dependency audit.** Go modules and the desktop's npm packages against the new terms (copyleft is the risk), and a NOTICE file with the third-party attributions, which MIT, BSD and Apache require in closed distribution too.
3. **Swap `LICENSE`** and every license mention: README, SPDX headers if any, package metadata, `wails.json`, the About screen, the docs.
4. **If the repository goes private**, release assets move to a public releases-only repository. `update`, `update --app`, `install.sh`, the desktop check, `doctor`'s `core.update` and the attestation instructions repoint; `release.yml` gains a secret to publish there (it uses none today); the Docker path (source builds only) and the CONTRIBUTING and issue links are reviewed. Public forks and clones remain, and every MIT release already published stays MIT.
5. **Rewrite the claims** in README, AGENTS.md, SECURITY.md, SUPPORT.md, COMPARISON.md and the locale strings to say what is true: data stays on the machine; a monoes.me sign-in is required; the app contacts monoes.me to sign in and refresh (the OAuth exchange and the IP address it comes from; no device id, no usage data) and to check for updates. Remove "works fully offline" and "no phone-home".

Sequencing: the gate ships first and works under any license. The relicense lands with or just before R's enforcement date, so "from this version on" is one story. Closing the source is a later, separate step with its own plan: it cannot be undone for copies already out, and it is what makes step 4 matter.

## 11. Verification

- **Fake server.** `libraryfake` gains a signing key, `resource` handling, and switches for refusal, outage and clock skew.
- **Tables.** `Verify` (each claim wrong in turn), the state machine (`iat`, `exp`, `now`, `hw` matrix; refused versus unreachable), the clock guard, the refresh lock (N processes refreshing with rotation).
- **Inventory.** `TestEveryCommandIsClassified` with the open list pinned.
- **One test per door** (§6.3), logged out and in grace, plus the CLI gate's exit code and JSON.
- **Mutation checks.** Removing the call at any gate site (the CLI gate, `handleExecution`, `monomind.Exec`, `executeDef`, `Server.auth`, `/v1`, the webhook server, the bridge, MCP) must fail a named test.
- **Real-binary smoke** (built with `devaccount`) against the fake: locked, sign in, ok, block on the fake, locked after one refresh with in-flight work cancelled; unreachable, grace, locked at 24 hours (the clock injected through the test hook).
- **The signed-in path through every entry point.** The real-binary smoke signs in and then runs one operation through each process type and entry point (`daemon`, standalone `httpapi`, `mcp`, an `mcp --grant` child, `extension serve`, `org serve`, `workflow run`, `chat`, and the desktop's Go side) and asserts that each succeeds. In a test binary `Require` fails open without a guard (D24), so a path that forgot to install one passes the unit tests and would fail closed for signed-in users in production; this smoke is what catches it. The gate-site tests also run with the strict hook and assert the refusal when no guard is installed.
- **Refusal semantics (D27).** A fake server that answers `invalid_client`, `invalid_target`, a 500 or a malformed body leaves a signed-in client in `grace` with reason `server_error`, never locked; only `invalid_grant` locks, deletes `refresh.enc` and cancels the work in flight.
- **Release guard.** `check-release-tags.sh` against a binary built with and without the tag, and against the app bundle's executable and CLI sidecar.
- **Desktop.** Vitest for the `AccountGate` states (locked, grace banner, warn banner, sign in to shell, `login_required` back to the gate), in English and Spanish.
- **Usual gates.** `gofmt`, `go vet`, `go test ./...` in both builds (default and `-tags nosocial`), `-race` on `internal/account` and the CLI tests (CI's Linux jobs run it), and the server's own suite for Part A.

## 12. Risks, spikes and out of scope

**Risks (accepted unless noted).**

- A monoes.me outage longer than 24 hours locks every install. `update` stays open; the deploy hardening of §5 and monitoring reduce the odds.
- Strictness against local-first: scheduled workflows stop after 24 hours offline and air-gapped use ends. The positioning changes (§10 step 5).
- Cancelling on a refusal can stop an outbound run midway (a half-sent batch). Accepted for the abuse case; the run records `login_required`.
- Older versions, source builds and patched binaries stay open, and so does an official build for a user who controls the clock and edits the account folder (§4.8); the license change and closing the source raise the cost only.
- A lost or compromised signing key needs a client release; clients on old releases lock with `key_unknown` until they `update`.
- A crash between a refresh-token rotation and its write means signing in again.
- Support: sign-in problems become support requests; `account status --json` and the `doctor` row carry the reason.
- Privacy: monoes.me sees each sign-in and refresh (account id, time, IP address). No device id, no usage data. Said in the docs (§10 step 5).
- The server's signing key and deploy pipeline are the root of trust of every install (§5 item 5).
- External monomind processes (orgs) already running continue until stopped.
- An old desktop app with a newer CLI (the app uses whichever `monoagentcli` it finds) shows `login_required` errors it has no screen for; `update --app` updates both.
- A bad monoes.me deploy. Only `invalid_grant` locks a client (D27), so a deploy that breaks the token endpoint leaves installs in grace. A deploy that wrongly answers `invalid_grant` would still log everyone out; the deploy hardening of §5 and a server-side test for it are the guard.

**Spikes** (each before the work that depends on it):

- S1. Can the `jwt()` plugin sign with a configured static Ed25519 key, or can its key be exported and pinned? Otherwise the JWKS fallback of §4.7.
- S2. Can an existing library refresh token be exchanged, with `resource`, for an audience-bound JWT? Otherwise one new sign-in (D23).
- S3. Does the refresh grant refuse a blocked user today, and does the provider rotate refresh tokens?
- S4. Which CI scripts and tests run the real binary, which gated commands do they use, and which processes call layer-2 functions without a guard (the desktop's Go side)?
- S5. Does `root.Find` resolve the target for every invocation form: global flags first, aliases, hidden commands, `--`, `help <cmd>`?
- S6. Mint a real token against a local server with a `resource` sent, and record the exact issuer, the shape of `aud` (string or array), the client claim (`azp` or `client_id`), `kid`, `alg` and `exp - iat`. Confirm that tokens are JWTs exactly when a `resource` is sent, and find the audience option's real name. B1's constants come from this, not from this document.

**Out of scope.** Device binding; machine tokens and environment-variable credentials; paid-plan logic or any plan enforcement; moving more value to monoes.me; killing external monomind processes; an offline mode; a minimum client version on library reads; closing the source itself.

## 13. Amendments found while planning (2026-10-05)

Writing the twelve plans against the real code and the real server corrected the text above. Where an amendment differs from an earlier section, the amendment wins. Decisions D1 to D12 (the owner's) are unchanged.

| # | Where | Amendment | Found by |
|---|---|---|---|
| A1 | D6, §6.1 | A third gate class, `serve`: `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` pass the CLI gate when locked and start, and layers 2 and 3 refuse. launchd's `KeepAlive` (`internal/autostart/autostart_darwin.go:33`) and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop, and MCP hosts must see a clear error. `org serve` is a launcher and stays gated, as do `daemon install`, `restart` and `uninstall`. | B2 |
| A2 | §6.3 HTTP API | `ExtraRoutes` (the org receiver and `/v1`) mount outside `Server.auth`, so a default-deny wrapper goes in front of the whole mux. `/v1` keeps its OpenAI error envelope with code `login_required`. | B3b |
| A3 | §6.3 bridge | The relay (answers 503) and the CDP socket also drive the browser and are covered. A refusal frame has no `ok` key. Pushed captures, recordings and binding pushes stay accepted while locked: the extension drops its queued copy once a frame is sent, and installed extensions cannot be updated centrally. Nothing runs on that data while locked; summaries and `record.analyze` are refused where they run. | B3b, B4 |
| A4 | §6.3, §6.5 | `GET /health`, the bridge `ping` and the heartbeat report `{state, reason, valid_until, enforced}`; `enforced` tells a warn-period `locked` (nothing refused) from a real lock. The desktop reads `account status`, not the heartbeat (`wails-app` reads no heartbeat). | B3b, B4 |
| A5 | §6.5 | Not every desktop action is a gated CLI call: `wails-app` calls `monograph build`, the runtime scan and model listing directly (none is engine work). With no CLI at all the gate cannot offer "update", because the updater itself runs the CLI; it offers "Try again". | B4 |
| A6 | D10, §9, §5 | The email-code route returns only a one-hour opaque token, so `account login --email` could not start a session. Plan A's Task 7 makes `POST /api/auth/agent/claim/verify` honor `resource` and return the token endpoint's answer (an audience-bound JWT plus a refresh token). It is a server change beyond §5's list, needed by D10. The headless session persists in `~/.monoagent/account/` of the OS user, whatever `--db-path` says. | A, B1b, B5b |
| A7 | §4.4, §5 | The provider rotates refresh tokens and treats a rotated one presented again as theft: it deletes every MonoAgent refresh token of that account, so every install locks as `refused`. A crash between rotation and write, a copied or restored session file, and a stale library vault copy all cause it. So `refresh.enc` is written before `session.json`; if saving the new one fails the old one stays with the A24 marker until the answer is recovered or the age rule drops it (A24(d)); adoption deletes the library vault's copy after the exchange; the docs say never to copy or restore the session directory; the server keeps a reuse window (`refreshTokenReuseInterval`, 300 seconds). Signing out of the monoes.me website does not sign out an install. | A, B1a |
| A8 | §4.4 | The one-minute negative cache applies to every CLI-path refresh attempt, not only to an expired token. The guard polls `session.json` lazily inside `Status` (one stat per poll interval) instead of every 5 seconds on a goroutine. | B1a |
| A9 | §4.3 | `Require` with no guard installed returns nil while dormant. `Session.LastResult` may be `key_unknown`: a refreshed token with an unpinned `kid` is not stored, the grace shows `server_error`, and it ends as `locked(key_unknown)`. | B1a |
| A10 | §5 | The audience is a database row plus a client link (a migration), not a provider option. Blocking must also delete the user's web sessions, because a live session could still mint tokens; today the refresh grant never looks at the user, so a blocked user's refresh answers 200. The signing key is supplied through the plugin's adapter from a secret; production's key today is one non-rotating database row, and the pinned `kid` is known only after the owner generates the key (owner-run steps O3 to O5). | A |
| A11 | §8, D28 | D28 protects from R on: the first update to R is made by the pre-R binary, which cannot restart the daemon. `update` also reconciles a stale daemon when already current, and the `doctor` row covers the transition. B5a (the date, warnings, daemon restart) and B5b (docs) merge as ONE push, because release notes come from `CHANGELOG.md` at the tagged commit. R is not cut until plan A is deployed, the production key is pinned in `internal/account/keys.go` (`pinnedKeys`) and the reuse window is live. | B5a, B5b, A |
| A12 | §9, §11 | A real binary has no clock hook, so the real-binary smoke ages the token (signed with the dev key) and uses one `devaccount`-only variable, `MONOAGENT_DEV_ENFORCE_FROM`, to force enforcement or a warn period. The smoke is its own plan, B5c, and merges before R. | B5a |
| A13 | §3 | The command tree is 41 top-level and 359 nested commands at `f4441a2a`. | B2 |
| A14 | §10 | The update check goes to GitHub, not monoes.me. There is no About screen (the Settings version row instead). `CHANGELOG.md` is at 0.100.1 while the newest tag is v0.106.1. | B5b |
| A15 | §6.2 | `internal/monomind` cannot import `internal/account`: `internal/secrets/blob_test.go` imports `internal/storage`, which imports `monomind`, and `account` imports `secrets`, so `go vet ./internal/secrets/` would fail with an import cycle (`daemonhb`, `orgdesign`, `orgsign`, `profiledir` and `shellpath` are in the same position). `Exec` is gated through a hook, `monomind.SetAccountGate`, that `cmd/monoagentcli` installs in an `init`; a binary without the hook is refused by `Exec`, and only `monoagentcli` links `Exec`. | B3a |
| A16 | §6.2 | Gating only `handleExecution` would permanently fail paused human-in-the-loop resumes and `--no-wait` rows while locked. `RetryExecution` and `ResumeExecution` are gated too, with a hold so a paused run waits and resumes after sign-in. | B3a |
| A17 | §6.2, §6.4 | Known edges, not covered by layer 2: validators run through `monomind.AgentTest`, not `Exec` (the daemon's auto re-validation waits while locked); `doctor fix`, an open command, runs a fixed `claude -p` profile init outside `Exec` (`internal/monomind/profile_init.go:129`); in-flight `/v1` and MCP turns finish instead of being cancelled (`Gateway.Shutdown` is terminal); a capture summary refused while locked is recorded as `error` and is not retried after sign-in (`internal/capturesummary/summarizer.go:218`), so a capture made while locked is stored but never summarized; the serving commands gate in `PreRun`, not `RunE`. | B3a |
| A18 | D21, D24, §9 | The machine session belongs to monoes.me. In a default build `MONOES_BASE_URL` is honored nowhere: `library login` against another host refuses and names `-tags devaccount`, `library logout` leaves the session alone, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build. Until adoption a legacy per-profile library login is still read, and `library login` and `logout` now use the machine session, so a dormant B1b release already changes where the library keeps its login. The sign-in flow moves into `internal/account` and `internal/library` delegates. | B1b |
| A19 | D23 | Adoption tries each profile's library login in order: a dead login (`invalid_grant`) moves on to the next profile and its vault copy is deleted, because a dead refresh token presented again from an older binary would end every session of the account; a login that got no verdict from monoes.me stops the call and keeps its copy for a later try. The email sign-in needs plan A's Task 7 on the server first. | B1b, B5a |
| A20 | §4.4, A7, D27 | A refresh grant, once sent, is always completed and stored. The provider rotates the refresh token when it answers, and the answer holds the only copy of the new one; a client that abandons the call (Ctrl-C, SIGTERM or SIGHUP mid-call, `Close`, a closing context) leaves the dead token on disk, and the next attempt after the 300-second reuse window makes monoes.me delete every refresh token of the account, so every install is locked as `refused`. The guard gives the `Refresher` a context detached from the caller's cancellation (`context.WithoutCancel`) with a deadline of its own (20 seconds), so `EnsureFresh`, `Refresh` and `Close` may block after a Ctrl-C while a grant is in flight: for the grant, then for the key-store write of the new refresh token (A22, at most 10 seconds), about 20 seconds in practice and 30 at the worst; nothing else cancels one. Every other grant the product performs that rotates a refresh token does the same: the adoption exchange of an older library login (D23) and the update of the vault entry after it, and any refresh the library adapter makes of a login of its own. `run()` lets go of the signals before it releases the guard, so a second Ctrl-C ends a shutdown that waits for a grant. | security review (B1a, B1b, B2, B3a, B5a) |
| A21 | §4.4, D27 | After `invalid_grant` the process deletes `refresh.enc` only once the `refused` marker is saved in `session.json`. If the marker cannot be written (a full disk, a read-only file) the token stays, the next process presents the already-refused token, learns the refusal again and retries the marker: a refusal is never forgotten because a write failed. | security review (B1a) |
| A22 | §4.4, §4.6 | Every key-store call made under the machine-wide session lock is bounded to 10 seconds, and a timeout is `keyring_unavailable` (grace). A locked keychain or an unanswered unlock dialog cannot hold the lock, and with it every process's refresh and every sign-in, or hang `Close`. A grant's new refresh token is saved only after the key store answered in time; when it did not, `refresh.enc` and the A24 marker stay and the next attempt recovers the answer (A24(d)). Every call that opens or seals the refresh token (`LoadRefresh`, `SaveRefresh`) is made under that lock: the guard's refresh, sign-in, logout and adoption take it first. | security review (B1a) |
| A23 | §4.5, §7, D5 | `account logout` never erases the clock-guard record. When a session existed it saves a session with no token, `{v: 1, host, hw: max(hw, now)}`, instead of deleting `session.json`, so the enforcement date is still judged against `max(now, hw)`: a blocked account that logs out, an open command, and sets the clock back before the date stays `locked(not_logged_in)` and enforced. A machine with nothing stored still writes nothing, the refresh token it revokes is read under the lock (never one that another process has just rotated), and the next sign-in replaces the record. §4.8's accepted cases are unchanged: a clock set back before the first check, and a user who deletes the record (a file in the account folder) and then sets the clock back. The record stops the naive clock change, not that one. | security review (B1a, B1b) |
| A24 | §4.3, §4.4, §4.6, §12, A7, A20 | A refresh whose outcome is unknown is never presented twice (adopted by the owner on 2026-10-06; it was proposed in the same review). A20 completes every grant the client sends, but an answer can still be lost after the provider has rotated the token: a process killed or crashed after the rotation, a machine that sleeps mid-call, an outage that starts mid-call (estimate: a laptop with a daemon and the lid closed has about a 7% yearly chance per machine of losing one answer, each costing every install a sign-in). Before a grant is sent the guard saves `pending_since` in `session.json`, under the session lock, and sends nothing if that save fails; the marker is set only when it was zero, so the 300-second reuse window is always measured from the first send. A definitive answer clears it (tokens, `invalid_grant`, or a failure that is settled: the request never left this machine, or monoes.me answered with an HTTP status); every other failure keeps it. B1b's transport reports which it was with `httptrace`'s `WroteRequest`, in a `Settled` flag on `TransientError` whose zero value means "outcome unknown", so a refresher that does not set it is treated as unknown. At the next attempt in any process: within 240 seconds of the stamp the retry is immediate and the server repeats its answer; later, or after the clock went back, the token is not presented: `refresh.enc` is deleted, `last_result` becomes `unconfirmed` and this machine signs in again, with no call and no revocation, one install instead of every install. `unconfirmed` is a new reason, grace first and `locked(unconfirmed)` once the 24 hours have passed; its text is `monoes.me may have received a refresh whose answer never arrived, so this machine stopped using its saved login to protect your other installs. Sign in again on this machine: monoagentcli account login`. The adoption exchange of an older library login and the library adapter's refresh follow the same rule: an unknown outcome deletes this machine's copy of that older token. A24(d), ruled on 2026-10-06 after the review of the security fixes: a grant that was answered but whose new refresh token could not be saved (the key store did not answer within A22's 10 seconds, refused, or the write failed) no longer deletes `refresh.enc`. The marker stays (`pending_since` keeps the stamp of the first send), the token stays, the attempt is recorded as `keyring_unavailable` and the error is returned; the next attempt inside the 240 seconds presents the same token again and monoes.me answers it again from its 300-second reuse window, which recovers the answer, and after the window the age rule above drops the token with no call. The earlier text of A7 and A22 deleted the dead token at once, which cost the machine its login even when the key store was back a moment later. The adoption exchange has no marker to carry that state, so an exchange that was answered whose new refresh token could not be saved counts as an unknown outcome too: this machine's copy of the older token is dropped from the vault, nothing is stored and the failure is returned. The same review ruled that a logout while `pending_since` is set (the token on disk may already be spent) forgets the login locally and does not call the revoke endpoint, and that the library adapter's refresh drops its vault login after an unknown outcome while the gate is dormant too: the one visible change of a dormant phase, which D22 leaves as it is, because the hazard it removes does not depend on a date. The contract gains `Session.PendingSince` (`pending_since`), `TransientError.Settled`, `ReasonUnconfirmed` and the constant `pendingRetryWindow` (240 seconds). | security review (B1a), owner; review of the security fixes (A24(d)) |
| A25 | §4.5, §4.6, §4.8, §8, A23 | A machine that never signed in keeps the clock-guard record too, from the enforcement date on. When there is no `session.json`, the gate is not dormant and the clock has reached the date, the guard's first pass (`EnsureFresh`, `Refresh` or the background refresher) takes the session lock and, if there is still no session, saves a session with no token, `{v: 1, host, hw: now}`, the record that A23's logout leaves. It is at most one attempt a minute per guard, a failure is never reported, and nothing is written while dormant or before the date. `max(now, hw)` then keeps the date enforced when the clock is set back before it, as it does for a machine that had a session. §4.8's accepted cases stay: a clock set back before the first check (the first check is now the first guard pass on or after the date), and a user who deletes the record and then sets the clock back. A25 is worth having because it stops the naive clock change; its claim holds for a user who does not touch the account folder. A refusal keeps the evidence too: `invalid_grant`'s `refused` marker raises `hw` to `max(hw, now)`. Consequence for tests and for the claim that nothing is written until a login: from the date on a gated command refused on an empty HOME leaves exactly `account/session.lock` and `account/session.json` (a session with no token, `hw` equal to the clock); an empty HOME stays empty while the gate is dormant or its date is ahead, and after a command that runs no guard pass (`doctor`). A login replaces the record, and the adoption of an older library login does not count the record as a login. | security review (B1a), owner |
| A26 (proposed, NOT adopted: the owner's decision) | §4.5, §4.8, D22 | A build floor. `Enforced` and the clock guard would judge `max(now, hw, buildFloor)`, where `buildFloor` is the build time that the release workflow injects with `-ldflags -X`, and the zero time (no floor) outside official builds. Benefit: every release built after the enforcement date stays enforced whatever the clock or the files say, which closes §4.8's clock-and-folder case for those releases. Cost: a machine whose clock is behind the build time shows `clock_rollback` (a dead CMOS battery, a frozen CI or container clock), and the release that sets the date and every build before it do not benefit. The workflow's build time is used, not `debug.ReadBuildInfo`'s `vcs.time`, which is the commit time and can predate the date for a hotfix built later. No plan implements it. | third security review |

A20 to A25 come from the second security review of the implemented core (B1a) and of these plans, made after the plans were written (2026-10-06). The owner adopted A24 and A25 the same day, and the review of the security fixes that followed added the ruling A24(d) (2026-10-06). A26 comes from the third security review (2026-10-06) and is a proposal: the owner decides, and no plan implements it.
