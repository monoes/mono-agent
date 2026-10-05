# Mandatory monoes.me Account — Plan Index

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (the execution method the owner chose: multi-agent) to implement the plans listed here task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Read this index first: §3 is the contract every plan is written against.

**Goal:** Nobody uses mono-agent without a monoes.me account signed in on the machine: official builds refuse to run until a valid, server-signed session exists, and stop when monoes.me disables the account or has been unreachable for 24 hours.

**Architecture:** A new package `internal/account` holds one machine-wide session (a signed EdDSA JWT access token plus a keyring-sealed refresh token) verified offline against keys pinned in the binary. One `account.Guard` per process gives every gate a cached verdict. Three layers enforce it: a default-deny CLI gate before cobra runs, checks inside the engine and runners, and refusals at every door (HTTP API, `/v1`, webhook server, extension bridge, MCP). monoes.me (`monoes-landing`) gets a small change set so tokens are audience-bound JWTs and blocking revokes them.

**Tech Stack:** Go 1.26 (stdlib `crypto/ed25519`, no new dependency), cobra, SQLite (existing), Wails + React (desktop), TypeScript with Better-Auth and Drizzle on Cloudflare Workers (server, separate repository).

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (decisions D1 to D28, §1 to §12). Plans argue from the spec; executors read both.

## 1. The plans

Twelve plans, one file each, all in `docs/mastermind/plans/` and named `2026-10-05-monoes-account-gate-<id>.md`. Each produces working, tested software on its own, and every phase that merges before release R is dormant (spec D22): it changes nothing a user can see, except the new `account` commands, `library login` and `library logout` using the machine session (a legacy per-profile login is still read until adoption), and an additive `account` object in `GET /health` and in the extension bridge's `ping`.

| Id | File suffix | Repository | Depends on | Delivers |
|---|---|---|---|---|
| A | `a-server` | `monoes/monoes-landing` | nothing (spikes S1, S2, S3, S6 first) | audience-bound JWTs, `plan` claim, blocking that revokes tokens, a pinnable signing key, deploy hardening |
| B1a | `b1a-core` | mono-agent | nothing | `internal/account` offline core: verify, states, clock guard, store, sealer, guard, `accounttest` |
| B1b | `b1b-signin` | mono-agent | B1a | refresher, login flows, `account` commands, `library` aliases, adoption, `devaccount`, fake-server signing |
| B2 | `b2-cli-gate` | mono-agent | B1a, B1b | the CLI gate, the inventory test, the `login_required` JSON, the `doctor` row, the late refresher in `run()`, spike S5 |
| B3a | `b3a-runners` | mono-agent | B1a | layer 2 checks, guard wiring in the serving commands, the daemon's locked mode, heartbeat field |
| B3b | `b3b-doors` | mono-agent | B1a | refusals at the HTTP API, `/v1`, org receiver, webhook server, extension bridge, MCP |
| B4 | `b4-desktop` | mono-agent | B1b, B2 | desktop bindings, the `AccountGate` (fails closed), the grace and warn banners, the desktop's read-only guard |
| B4b | `b4b-extension` | mono-agent | B3b | the Chrome extension side panel's "Sign in to MonoAgent" message |
| B5a | `b5a-rollout` | mono-agent | B1a to B4b | the enforcement date and the pin of the production key, the warnings, the adoption wiring (`library.AdoptIntoAccount` from the root pre-run, one try per database), update restarts the daemon, the `doctor` stale-daemon row, the release guard, spike S4, the readiness checklist |
| B5b | `b5b-docs` | mono-agent | B5a (claims text) | the claims rewrite, CHANGELOG, `ref` pages; ships in the same push as B5a |
| B5c | `b5c-smoke` | mono-agent | B1a to B4b | the real-binary smoke (a `devaccount` build, a fake monoes.me, one operation per entry point) and its CI job |
| B5d | `b5d-license` | mono-agent | nothing | the license workstream: copyright and dependency audits, then the owner-gated license swap and releases-only repository |

Merge order (every merge to master releases, so order matters): A first, then B1a, B1b, then B2, B3a, B3b, B4, B4b in any order, then B5c (test-only; with `MONOAGENT_DEV_ENFORCE_FROM` it proves the whole gate while production builds are still dormant), then release R: **B5a and B5b merged together as one push**, because `release.yml` takes the release notes from `CHANGELOG.md` at the tagged commit and R must not ship ahead of its docs. B5d is independent of R. R is the first release with a date and implicit calls to monoes.me, and it is not cut until plan A is deployed to production (including the owner-run steps O2 to O5) and the production signing key's public half is pinned in `internal/account/keys.go` (`pinnedKeys`; `keys_default.go` only holds `extraKeys()`): without that pinned key every real token verifies as `key_unknown` and every install would lock.

Spikes S1 to S6 (spec §12) and where they run: S1, S2, S3, S6 in plan A (they need a local server); S5 in B2; S4 in B5a. Their findings go to `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md` (created by plan A's Task 2, since deploy hardening is its first change; later plans append). Where a finding changes a constant, only the one named file changes (`internal/account/claims.go`), never several.

Plan A has an ordering hazard: `monoes-landing`'s `deploy.yml` runs `wrangler deploy` on `pull_request` as well as on `push` to `main`, so any pull request there deploys to production. Hardening that workflow is therefore Plan A's first change, landed alone before any other Plan A pull request is opened. The spikes run against a local server only and nothing they produce is pushed.

## 2. Global constraints

Every task in every plan includes these implicitly. Values are copied from the spec.

- Go is `go 1.26.0`; `internal/account` adds no third-party dependency (D13): `crypto/ed25519` and a small strict JWS parser only. One accepted algorithm (EdDSA); the verifier ignores `jku`, `jwk` and `x5u` headers and never negotiates from the header.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Storage (§4.6): `~/.monoagent/account/` (directory 0700) with `session.json`, `refresh.enc` and `session.lock` (files 0600). One session per OS user, shared by all profiles, whatever `--db-path` says.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- Nothing on disk until a write: `OpenStore`, `NewDefaultGuard`, `Status`, `Require`, `EnsureFresh`, `CurrentStatus` and `Evaluate` create no file or directory when no session exists, because `scripts/doctor-smoke.sh` asserts that `doctor` on a fresh HOME writes nothing and `run()` installs a guard for every command, open ones included. The directory, `session.json`, `refresh.enc` and `session.lock` appear only on a login or a refresh. The high-water mark `hw` is written only when a session already exists, by the guard, at most once a minute.
- Process globals (`enforceFrom`, the trusted keys, the installed guard, the strict flag) are guarded by a `sync.RWMutex` and read only through accessors. The `*ForTest` hooks and `accounttest.Install` are for tests that do not call `t.Parallel()`; CI's Linux jobs run `-race`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Open commands (D6): `version`, `help`, `completion`, cobra's hidden `__complete` and `__completeNoDesc`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), `library login`, `library logout`, `library status`. Everything else is gated, except the serving commands:
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build, and a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me, `library login` against another host refuses and names `-tags devaccount`, and the session token is sent only to the host that issued it. A local monoes.me dev server needs a `-tags devaccount` build.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

## 3. The frozen contract

Plans use these names and signatures exactly. A plan may add package-internal helpers freely. A plan that needs a contract change does not make it: it lists the request under a final heading `Contract change requests` (name, reason, proposed signature) and stays consistent with this section as written. B1a and B1b own the package; everyone else only consumes it.

### 3.1 File ownership in `internal/account`

| File | Owner | Holds |
|---|---|---|
| `doc.go`, `claims.go`, `state.go`, `rollout.go`, `session.go`, `errors.go` | B1a | constants, states, `Status`, `Evaluate`, `EnforceFrom`, `Session`, errors |
| `keys.go`, `keys_default.go`, `keys_devaccount.go`, `verify.go` | B1a (the dev key itself is added to `keys_devaccount.go` by B1b) | pinned keys, JWS verification |
| `store.go`, `lock_unix.go`, `lock_windows.go`, `sealer.go`, plus one exported key-store accessor in `internal/secrets` | B1a | the on-disk session, the cross-process lock, the sealer (the keyring primitives in `internal/secrets` are unexported, so B1a adds a small exported accessor there and changes no existing behavior) |
| `guard.go`, `testhooks.go` | B1a | the guard and the test seams |
| `accounttest/*.go` | B1a | fixtures every other plan's tests use |
| `refresh.go`, `login.go`, `defaultguard.go`, `host_default.go`, `host_devaccount.go` | B1b | the network side |
| `rollout_override.go`, `rollout_override_default.go`, `rollout_override_devaccount.go` | B5c | `MONOAGENT_DEV_ENFORCE_FROM`: read only in the file compiled under `-tags devaccount` (the default stub has no `os.Getenv`; a test pins it). These files compile into every release binary, so the table names their owner |
| `internal/library/adopt.go` | B1b | `library.AdoptIntoAccount`: adoption lives in `internal/library` because it must read the library's vault entry and `internal/account` may not import `internal/library` |

Import rule, to avoid cycles: `internal/account` imports `internal/secrets` (for the sealer) and the standard library, and nothing else of this repository: not `internal/library`, `internal/workflow`, `internal/monomind`, `internal/action`, `internal/httpapi` or `cmd`. Everything else may import `internal/account`, `internal/library` included (its aliases and adapter use the machine session). B1b decides whether the PKCE and email-code flow moves into `account` or into a neutral package both import; `internal/library`'s existing login API stays source-compatible so its tests keep passing.

### 3.2 Go API (package `account`)

```go
// ---- claims.go (B1a). S6 confirms or corrects these in this one file. ----
const (
	HostURL        = "https://monoes.me"
	Issuer         = "https://monoes.me/api/auth"
	Audience       = "https://monoes.me/api/monoagent"
	ClientID       = "monoagent"
	GraceWindow    = 24 * time.Hour
	MaxTokenLife   = 24 * time.Hour
	ClockSkew      = 5 * time.Minute
	RefreshMargin  = 5 * time.Minute
	NegativeCache  = time.Minute
	ConnectTimeout = 2 * time.Second
	PollInterval   = 5 * time.Second
	LateRefresher  = 5 * time.Minute // a non-serving process starts its refresher after this
)

// ---- state.go (B1a) ----
type State string

const (
	StateOK     State = "ok"
	StateGrace  State = "grace"
	StateLocked State = "locked"
)

type Reason string

const (
	ReasonNone               Reason = ""
	ReasonNotLoggedIn        Reason = "not_logged_in"
	ReasonExpired            Reason = "expired"
	ReasonRefused            Reason = "refused"
	ReasonClockRollback      Reason = "clock_rollback"
	ReasonClockSkew          Reason = "clock_skew"
	ReasonKeyUnknown         Reason = "key_unknown"
	ReasonInvalid            Reason = "invalid"
	ReasonUnreachable        Reason = "unreachable"         // grace: why it was not refreshed
	ReasonServerError        Reason = "server_error"        // grace
	ReasonKeyringUnavailable Reason = "keyring_unavailable" // grace
)

type User struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

// Status is the verdict and the JSON of `account status --json` (spec §7).
type Status struct {
	V           int       `json:"v"` // always 1
	State       State     `json:"state"`
	Reason      Reason    `json:"reason"`
	User        *User     `json:"user,omitempty"`
	Plan        string    `json:"plan"`
	IssuedAt    time.Time `json:"issued_at,omitzero"`
	ValidUntil  time.Time `json:"valid_until,omitzero"`  // the access token's exp
	GraceUntil  time.Time `json:"grace_until,omitzero"`  // iat + GraceWindow
	EnforceFrom time.Time `json:"enforce_from,omitzero"`
	Enforced    bool      `json:"enforced"`
}

// Allowed reports whether work may run: true when !Enforced, or State is ok or grace.
func (s Status) Allowed() bool

// Evaluate is the pure verdict of a stored session at a time: no I/O.
// It verifies the token (Verify), applies the clock guard and the grace rule,
// and fills Enforced and EnforceFrom from rollout.go. A nil session is
// locked(not_logged_in); a session whose State is "refused" is locked(refused).
func Evaluate(sess *Session, now time.Time) Status

// ---- rollout.go (B1a defines; B5a sets the date) ----
// The date lives in an unexported variable in rollout.go (`var enforceFrom = time.Time{}`,
// the zero time = dormant, D22). B5a edits that one initializer. Everything else
// reads it through the accessors, which take the globals' lock.
func EnforceDate() time.Time
func Enforced(now, hw time.Time) bool // !EnforceDate().IsZero() && max(now, hw) >= EnforceDate()

// ---- session.go / store.go (B1a) ----
type Session struct {
	V           int       `json:"v"`
	Host        string    `json:"host"`
	AccessToken string    `json:"access_token"`
	User        *User     `json:"user,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	HW          time.Time `json:"hw,omitzero"`
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastResult  string    `json:"last_result,omitempty"` // "ok", "unreachable", "server_error", "keyring_unavailable", "refused"
	State       string    `json:"state,omitempty"`       // "" or "refused"
	Reason      string    `json:"reason,omitempty"`
}

// Store is the on-disk session. Mutating methods are safe across processes only under Lock.
type Store interface {
	Load() (*Session, error)                              // nil, nil when there is no session.json
	Save(*Session) error                                  // atomic, 0600
	LoadRefresh() (string, error)                         // "", nil when none; ErrKeyringUnavailable when the key store cannot be opened
	SaveRefresh(token string) error
	DeleteRefresh() error
	Lock(ctx context.Context) (unlock func(), err error)  // exclusive, cross-process
	Mtime() (time.Time, error)                            // of session.json; zero time when absent
	Dir() string
}

func DefaultDir() (string, error)             // ~/.monoagent/account via os.UserHomeDir
func OpenStore(dir string, s Sealer) Store    // dir "" means DefaultDir()

// Sealer seals the refresh token under a key from the OS keyring or the file-keyring fallback, as the vault does.
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

var ErrKeyringUnavailable = errors.New("account: key store unavailable")

func NewKeyringSealer() Sealer // production
func NewMemorySealer() Sealer  // tests

// ---- keys.go / verify.go (B1a) ----
type Key struct {
	KID    string
	Public ed25519.PublicKey
}

func TrustedKeys() []Key // the pinned set; under devaccount also the dev key

type Receipt struct {
	Sub       string
	Plan      string // "free" when the token has no plan claim
	IssuedAt  time.Time
	ExpiresAt time.Time
	KID       string
}

type VerifyError struct{ Reason Reason } // ReasonInvalid, ReasonKeyUnknown or ReasonClockSkew
func (e *VerifyError) Error() string

func Verify(token string, now time.Time) (*Receipt, error)

// ---- errors.go (B1a) ----
// LoginRequiredError is what every gate returns. Its message starts with
// "Log in to monoes.me first: monoagentcli account login".
type LoginRequiredError struct{ Status Status }

func (e *LoginRequiredError) Error() string
func IsLoginRequired(err error) bool // errors.As

// ---- the refresh seam (B1a defines, B1b implements) ----
type TokenSet struct{ AccessToken, RefreshToken string }

// Refresher performs the OAuth refresh-token grant with resource=Audience.
// It returns *RefusedError only for invalid_grant (D27), *TransientError for every other failure.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*TokenSet, error)
}
type RefusedError struct{ Description string }
type TransientError struct {
	Reason Reason // ReasonUnreachable or ReasonServerError
	Err    error
}

// ---- guard.go (B1a) ----
type GuardOptions struct {
	Store     Store
	Refresher Refresher        // nil: never refreshes
	Now       func() time.Time // default time.Now
	Poll      time.Duration    // default PollInterval
}

type Guard struct{ /* unexported */ }

func NewGuard(o GuardOptions) *Guard
func (g *Guard) Status() Status                              // the current verdict; recomputed from the cached session and Now
func (g *Guard) Require(ctx context.Context) error           // nil when Status().Allowed(); else *LoginRequiredError
func (g *Guard) EnsureFresh(ctx context.Context) (Status, error) // implicit refresh when due; a no-op when dormant
func (g *Guard) Refresh(ctx context.Context) (Status, error)     // explicit refresh when due, even when dormant (used by `account status`)
func (g *Guard) StartRefresher(ctx context.Context)          // idempotent; a no-op when dormant
func (g *Guard) OnRefused(fn func(Status))                   // called once when the verdict becomes locked(refused)
func (g *Guard) Close()

// Process-wide guard.
func Install(g *Guard)
func Current() *Guard // nil when none is installed
// Require uses the installed guard. With none installed it returns a
// LoginRequiredError(locked, not_logged_in), except inside a test binary
// (testing.Testing()) unless StrictForTest is active.
func Require(ctx context.Context) error

// CurrentStatus is nil-safe: the installed guard's Status(), or, with none
// installed, a locked(not_logged_in) Status with Enforced and EnforceFrom filled
// in. Doors and `/health` use it; processes that must react to a change (the
// daemon) poll it every PollInterval.
func CurrentStatus() Status

// ---- testhooks.go (B1a): each panics unless testing.Testing() ----
func SetTrustedKeysForTest(t testing.TB, keys []Key)
func SetEnforceFromForTest(t testing.TB, at time.Time)
func StrictForTest(t testing.TB)
func InstallForTest(t testing.TB, g *Guard) // installs and restores on cleanup

// ---- b1b: network side ----
type LoginOptions struct {
	Open    func(url string) error // opens the browser; nil does not
	OnURL   func(url string)       // told the URL before Open runs
	Timeout time.Duration          // default 5 minutes
}

func Login(ctx context.Context, o LoginOptions) (Status, error)                   // PKCE + loopback; stores the session
func SendEmailCode(ctx context.Context, email string) error
func VerifyEmailCode(ctx context.Context, email, code string) (Status, error)
func Logout(ctx context.Context) error                                            // revokes best effort, deletes session and refresh token
func NewDefaultGuard() (*Guard, error)                                            // production store, sealer and refresher

// Adoption is not in this package (import rule, §3.1). In internal/library/adopt.go (B1b):
//   func AdoptIntoAccount(ctx context.Context, db *sql.DB, g *account.Guard) (adopted bool, err error)
// It reads a profile's `monoes-library` vault entry, tries to exchange its refresh token
// through g's Refresher for an audience-bound JWT, and saves the session. A no-op while
// dormant. Whatever the server answers, the outcome is "adopted" or "sign in once more"
// and never a refusal (D23).
```

### 3.3 `accounttest` (B1a): the fixtures every other plan's tests use

```go
package accounttest

type Mode int

const (
	SignedIn      Mode = iota // enforced, a valid token, state ok
	InGrace                   // enforced, an expired token inside the 24 hours, state grace(unreachable)
	LockedNoLogin             // enforced, no session, locked(not_logged_in)
	LockedRefused             // enforced, session marked refused, locked(refused)
	Dormant                   // EnforceFrom is the zero time
)

// Install builds a guard in the given mode over a temp store with a throwaway
// key, trusts that key, sets EnforceFrom for the mode, installs the guard as
// the process guard (strict: no test-binary fail-open) and restores all of it
// on cleanup. It returns the guard.
func Install(t testing.TB, m Mode) *account.Guard

// Fixture is the lower-level form for B1a's own tests and for fake servers.
type Fixture struct {
	Private ed25519.PrivateKey
	Key     account.Key
	Clock   *Clock
}

func New(t testing.TB) *Fixture // trusts Key and sets EnforceFrom in the past
func (f *Fixture) Token(o TokenOptions) string // a signed JWT valid at Clock.Now() unless o says otherwise

type TokenOptions struct {
	Sub, Plan, Issuer, ClientClaim string
	Audience                       []string
	IssuedAt                       time.Time // zero: Clock.Now()
	Lifetime                       time.Duration // zero: 1 hour
	KID                            string
	Alg                            string // zero: EdDSA
}

type Clock struct{ /* Now() time.Time, Set(time.Time), Advance(time.Duration) */ }

func DevKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) // the fixed development pair: the public half is trusted by `-tags devaccount` builds (B1b), the private half signs for libraryfake
```

`accounttest.Install` and the `*ForTest` hooks change process globals: they take the package's lock, and a test that uses them must not call `t.Parallel()`.

### 3.4 JSON, text and wire contracts

1. `account status --json` prints `account.Status`. Exit 0 for `ok` and `grace`, 4 for `locked`.
2. A refused gated command (CLI gate or any command that returns `*account.LoginRequiredError`) exits 4. When `--json` appears anywhere in the arguments it also prints on stdout one document: `{"error":"<message>","code":"auth_or_connection","login_required":true,"account":{"state":"locked","reason":"<reason>"}}`. The type `loginRequiredError` moves from `cmd/monoagentcli/library.go` to `cmd/monoagentcli/login_required.go` (B1b), with constructor `newLoginRequiredError(st account.Status) error`, `JSONErrorFields()` carrying `login_required` and `account`, and `isLoginRequired(err)` unchanged in meaning.
3. Gate text on stderr: locked `Log in to monoes.me first: monoagentcli account login` (plus a reason line); grace `monoes.me is unreachable; this login works offline until <RFC3339 local time>`; warn `A monoes.me login will be required from <date>: monoagentcli account login`. Never on stdout.
4. Heartbeat: `daemonhb.Heartbeat` gains `Account *AccountState \`json:"account,omitempty"\`` with `AccountState{State string \`json:"state"\`; Reason string \`json:"reason,omitempty"\`; ValidUntil time.Time \`json:"valid_until,omitzero"\`; Enforced bool \`json:"enforced"\`}` (B3a). The same four-key object `{state, reason, valid_until, enforced}` is what `GET /health` and the bridge `ping` report, so a warn-period `locked` state (nothing refused) can be told from a real lock.
5. HTTP API and org receiver (B3b): status 401, body `{"error":"login_required","login_required":true,"account":{"state":"…","reason":"…"}}`. `/v1` (B3b) keeps its OpenAI-style envelope (`internal/openaiapi/errors.go`: `apiError` and `writeError`, `{"error":{"message","type","param","code"}}`): status 401, error `code` `login_required`, the fixed message `Log in to monoes.me first: monoagentcli account login`, and never the flat body above, because OpenAI SDKs parse the envelope. `GET /health` stays open and gains `"account":{"state":"…","reason":"…","valid_until":"…","enforced":false}`. B3b gates the whole HTTP mux with a default-deny wrapper, because `ExtraRoutes` (the org receiver and `/v1`) mount outside `Server.auth`.
6. Webhook server (B3b): 503 `{"error":"login_required"}` with `Retry-After: 60`; no execution is created.
7. Extension bridge (B3b): every request except `ping` gets a reply frame `{"kind":"reply","id":…,"error":"<LoginRequiredMessage>","code":"account_locked"}` with NO `ok` key (`Reply.OK` is omitempty, `internal/extension/request.go:122`), so readers test `!reply.ok` or the code; `ping` data carries `"account":{"state","reason","valid_until","enforced"}`. The relay answers 503 (its client reads 401 and 403 as a pairing mismatch) and the CDP socket is refused at connect and per command. Pushed captures, recordings and binding pushes stay ACCEPTED while locked: the extension drops its queued copy once the socket takes a frame (`chrome-extension/capture_bridge.js:111-117`) and installed extensions cannot be updated centrally; nothing runs on that data while locked (summaries and `record.analyze` are refused where they run). Tightening this needs an extension release first. `GET /monoagent/health` carries no account object.
8. MCP (B3b): `tools/call` returns `isError: true` with the text `Log in to monoes.me first: monoagentcli account login`; `initialize` and `tools/list` still answer.
9. Layer 2 (B3a): `handleExecution` records `FAILED` with an error text starting `login_required:`; `monomind.Exec` and `ActionExecutor.executeDef` return `*account.LoginRequiredError`.
10. Library compatibility (A and B1b): once `library` commands use the machine session, every `/api/library/*` call carries the audience-bound JWT. The server's token check (`getAuthenticatedUser`) must accept that JWT (scopes `library:read` and `library:write` included) alongside today's opaque tokens; plan A proves it with a server test that mints one and calls `/api/library/me` and a list endpoint. The client's library adapter sends the session's token only when `Session.Host` equals the library base URL, so a `MONOES_BASE_URL` pointed elsewhere never receives it.

### 3.5 The CLI gate (B2)

- `cmd/monoagentcli/account_gate.go` holds one classification table keyed by command path (`"doctor"`, `"doctor fix"`, `"library login"`, …), a command inherits its nearest listed ancestor, the root with no subcommand and `-h`, `--help`, `help` and `completion` are open, everything else is gated. `applyClassification(root)` sets `cmd.Annotations["monoagent.account"]` to `open` or `gated` from the table at start-up (one table, not 41 edited files, so phases do not conflict; D20 asks for annotations, and this produces them).
- `main()` becomes `func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }` with `run(args []string, stdout, stderr io.Writer) int` so the gate is testable; `run` installs the guard (`account.NewDefaultGuard`, then `account.Install`) and gates before `ExecuteContext`. Tests that call `newRootCmd().Execute()` directly bypass the gate by design; layer 2 still applies to them.
- The late refresher belongs to B2: `run()` starts the guard's refresher with `time.AfterFunc(account.LateRefresher, …)` for every process still running after five minutes (idempotent with `StartRefresher`). The serving commands (`daemon`, `httpapi`, `mcp`, `extension serve`) start it at once in their own `PreRun` (B3a), so B2 owns `main.go` and B3a never edits it.
- Classification has three values, `open`, `gated` and `serve` (the annotation `monoagent.account`); the serve set is pinned next to the open set in `TestEveryCommandIsClassified`. B2 also cancels the command context when a gated one-shot command's login is refused (`cancelWhenRefused`) and owns the edit to `cmd/monoagentcli/exitcodes.go` that maps any `*account.LoginRequiredError` to exit 4; `run()` prefers an already-installed guard (tests install their own).

### 3.6 Contract amendments settled during planning

Exact signatures are in the B1a plan; these are the additions and readings agreed with the plan writers.

- `account.Require` with no guard installed returns nil while dormant or before the enforcement date; once enforced it returns `LoginRequiredError(locked, not_logged_in)`, except in a test binary unless `StrictForTest`. D22 wins over the older sentence in §3.2.
- Added: `account.LoginRequiredMessage`; `account.NewInteractiveKeyringSealer()` (the explicit sign-in uses it; the implicit `NewKeyringSealer` never prompts for the file keyring's passphrase); `account.NewSession(host, accessToken, user, now)`; `secrets.AccountKEK(create, interactive bool)`; `account.Install(nil)` and `account.InstallForTest(t, nil)` (remove the guard, restore on cleanup); `(*LoginRequiredError).JSONErrorFields()` returning `login_required`, `code: auth_or_connection` and `account{state, reason}`; more `accounttest` helpers (`InstallWithFixture`, `NewClock`, `DefaultNow`, `(*Fixture).Sign`, `DevKID`).
- `Session.LastResult` may be `key_unknown` (a refreshed token with an unpinned `kid` is not stored; the grace shows `server_error` and ends as `locked(key_unknown)`).
- The CLI path applies the one-minute negative cache to every refresh attempt, and the guard polls lazily inside `Status` (no goroutine for a non-refresher guard).
- Server facts that bind the client: the provider rotates refresh tokens, and presenting an already-rotated one ends every MonoAgent session of the account. So a refresh writes `refresh.enc` before `session.json`, deletes the dead token if saving the new one fails, adoption deletes the library vault's copy after the exchange, and nothing leaves a stale refresh token behind. The server keeps a reuse window (`refreshTokenReuseInterval`, 300 seconds).
- `MONOAGENT_DEV_ENFORCE_FROM` is read only in a file compiled under `-tags devaccount` (a default stub has no `os.Getenv`); it can force enforcement or a warn period and never relaxes anything. B5c owns it.
- B5a's daemon-restart code runs in-process (`autostart.Installer.Restart`), never by spawning the gated `monoagentcli daemon restart`.
- B1b adds, beyond §3.2 (exact signatures in the B1b plan): the shared browser sign-in moves into `internal/account` (`DiscoverEndpoints`, `AuthorizeInBrowser`; `internal/library` delegates and its public login API stays source-compatible), `account.Host()` (`HostURL`, or `MONOES_BASE_URL` in a `-tags devaccount` build only), `account.SetSealerForTest`, and in `libraryfake`: `TokenRequests`, `Replays`, `SetEmailOpaque`, `Block`, `SetClock`, `LastResource` and the refresh-failure switches. A test that signs in through the default store more than once per test binary must call `account.SetSealerForTest`, because `internal/secrets` remembers the account key per process while tests re-make the mock keyring.
- The email sign-in needs plan A's Task 7 merged first (the verify route must return a refresh token); until then `account login --email` ends in `ErrEmailSessionUnavailable`. B1b's adoption (`library.AdoptIntoAccount`) tries each profile in order: a dead login moves on to the next profile and its vault copy is deleted; a login that got no verdict stops the call and keeps its copy.
- Import rule exception (spec A15): `internal/monomind`, and the packages in the same import cycle (`internal/secrets/blob_test.go` imports `internal/storage`, which imports `monomind`; `account` imports `secrets`), cannot import `internal/account`. `Exec` is gated through the hook `monomind.SetAccountGate` (and `monomind.ErrNoAccountGate`) that `cmd/monoagentcli` installs in an `init`; a binary without the hook is refused by `Exec`. `daemonhb.AccountState` uses plain strings for the same reason. Plans must run `go vet ./...` after adding an `internal/account` import to any package.
- B3a also gates `RetryExecution` and `ResumeExecution` (a paused run waits, with a hold, and does not fail while locked), and the serving commands gate in `PreRun`.

## 4. Rules for everyone who writes or executes a plan

- **Repository state.** Plans are written against master `f4441a2a` (v0.106.1), read from the worktree `.claude/worktrees/feat+monoes-account-gate`. Every file path, function and line in a plan must exist; confirm with `grep` or `sed -n` before writing it.
- **Test commands.** Targeted and explicit: `go test ./internal/account/ -run TestVerify -count=1`. Never a loose `-run 'Doc'` (it also matches `TestAutomationDoctorJSON`, which hangs for minutes under a narrow `-run`). Add `-race` for `internal/account` and the CLI tests the gate touches. Build checks use `go build ./...`, `go vet ./...`, `gofmt -l .`, and for the variants `go build -tags nosocial -o /dev/null ./cmd/monoagentcli` and `go build -tags devaccount -o /dev/null ./cmd/monoagentcli`. **Never** a bare `go build ./cmd/monoagentcli` in the repo root: it overwrites the owner's gitignored `./monoagentcli`.
- **Known failures on pristine master (macOS).** A red `go test ./...` is judged against this list, not investigated: `cmd/monoagentcli` `TestCaptureTaskFilesOnTheBoard`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCoderConversationFolders`; `internal/capturetask` `TestCreateAttachesEveryArtifact`, `TestCreateRecordsTheRealPathNotASymlink`; `internal/config` `TestGenerateConfigFailsFastWhenMonomindMissing`; `internal/monomind` `TestFindAll_ListsShadowedCopies`. Load flakes that pass alone: `internal/connections` `TestMigrateConnectionsToVault_SkipsRowLockedByAnotherProcess`, `internal/mcp` `TestGrantWaitTimeoutNote` and `TestManyUpdateCallsAtOnceAllLand`, `internal/dynorg` `TestIsolatedWritersRunInParallelInTheirOwnWorktrees`, `cmd/monoagentcli` `TestAgentTestGoDeadline`. A failure not on this list is yours until proven otherwise (export `HEAD` with `git archive HEAD | tar -x -C <fresh dir>` and run the test there).
- **Shell limits in agent worktrees.** The Bash tool refuses chained git (`git add … && git commit …`), `git -C <other dir>`, heredocs, `rm -r` and `rm -rf`. Commit with two separate commands: `git add <files>`, then `git commit -m "<subject>" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`. Write multi-line files with the Write tool. A refused long command goes into a script file in the scratchpad and runs as `bash script.sh`. Never use bare `git stash`.
- **Tokens.** Tests and logs never print a token, refresh token or key. Inspect structure (lengths, claim names), not values.
- **Disk.** Tests use `internal/testhome` (a throwaway `HOME`); do not run shadow-HOME smoke tests that re-download Go caches (about 1.7 GB each).
- **Pushing.** Nothing is pushed and no pull request is opened unless the owner says so. Nothing merges without the owner. Every merge to master releases.

## 5. Execution (multi-agent)

The owner chose multi-agent execution, so superpowers:subagent-driven-development runs this index:

1. **One implementer per plan**, in its own git worktree (`isolation: "worktree"`) cut from master, or from the tip of the plan it depends on (B1b on B1a; B2, B3a, B3b and B4 on B1b once B1 is merged; B5a and B5b on the merged B-phases). Plan A runs in a clone of `monoes-landing`, or is handed to the Claude session that works in that repository.
2. **A fresh read-only reviewer after each task**, and a whole-branch review per plan. B1a and B3b are security-critical and get a second, independent review that tries to bypass the gate.
3. **Parallelism.** After B1a merges, B1b, B3a and B3b run at once; after B1b merges, B2 and B4 run at once. A runs from the start (its spikes S1, S2, S3, S6 are the earliest blocking facts).
4. **Integration** by rebase in the merge order of §1. The lead runs the full verification (§4 commands, both build variants, `-race` on the account package) before each plan is offered to the owner.
5. **The owner decides** when to push, open pull requests and merge. B5b's license and distribution tasks 3 and 4 (swap the license, move release assets) wait for the owner's terms and a go; the audits (tasks 1 and 2) can run any time.

## 6. Rules for the plan writers

- Write exactly one file: your own plan. Never touch the spec, this index, another plan or any source file; no commits and no pushes.
- Use the contract of §3 exactly. If you need a change, add it under a final heading `Contract change requests` (name, reason, proposed signature) and keep your plan consistent with §3 as written.
- Read the real code at master `f4441a2a` before citing it; every path, function and line you write exists. B1a and B1b prove their code: build and run it in a scratch export (`git archive HEAD | tar -x -C <fresh scratchpad dir>`), so the plan's code compiles and its tests pass as written. Other plans prove their riskiest snippets the same way.
- Follow the writing-plans format: the header, Global Constraints copied from §2, Review Focus, tasks with Files, Interfaces and steps in TDD order, no placeholders, commits as two separate commands.
- Final reply: short. The plan's path, the number of tasks, one coverage line per spec section, the contract change requests, and any fact that contradicts the spec or this index, with file:line. Never paste plan text.
