> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Task 2 report: the HTTP API door (B3b)

Status: DONE_WITH_CONCERNS (non-blocking, see Concerns: the `/v1` gap until Task 5, and a latent decode mismatch that needs a ruling). Commit `841f57f7` on `feat/account-b3b` (base `a295037d`): `feat(httpapi): refuse every route but /health while the monoes.me account is locked`. Worktree `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b3b`, clean after the commit (`git status --short` empty, `git diff HEAD` empty).

## What I implemented

Exactly the brief's eight edits to `internal/httpapi/server.go`, in order, and the brief's test file `internal/httpapi/account_door_test.go`.

- Edit 1: imports `strings`, `internal/account`, `internal/accountdoor`.
- Edit 2/3/4/5: `Server.handler http.Handler`; `NewServer` sets `s.handler = accountGate(s.mux)`; `Handler()` and `Serve` use `s.handler`.
- Edit 6: `Server.auth` refuses with `account.Require` before `ensureToken` (no vault work for a locked server).
- Edit 7: `accountGate(next)` and `openWhileLocked(r)`, placed before `bearerCredential`. Open while locked: `GET`/`HEAD /health` (exact path) and `/v1`, `/v1/...`. Everything else gets the 401 of index §3.4 item 5 from `accountdoor.WriteUnauthorized`.
- Edit 8: `GET /health` gains `"account": accountdoor.SummaryOf(account.CurrentStatus())`.

Verification that I applied the brief verbatim: the test file is byte-identical to the brief's code block (`diff` of brief lines 16-228 against the file printed "IDENTICAL"); `git diff` of `server.go` before the commit is the eight diffs of the brief and nothing else; `gofmt -l internal/httpapi` prints nothing; every mutation search text occurs once (the variable is `err` in `auth` and `refusal` in `accountGate`, so the http-auth text is unique); the text `strings.HasPrefix(p, "/v1/")` that Task 5's http-v1 mutation edits is as the brief has it.

Sizes: `server.go` 242 -> 282 lines; `account_door_test.go` 213 lines (new). Both under 500.

## TDD evidence

### RED (before any edit of `server.go`)

Command (the brief's, with `-v` added so the full log could be kept):
`go test ./internal/httpapi/ -run '^(TestAuthenticatedRoutesAreRefusedWhileLocked|TestHealthIsOpenInEveryStateAndReportsTheAccount|TestLockedServerRefusesEveryPathButHealth|TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused|TestAuthRefusesALockedAccountBeforeTheVault|TestServeServesTheGatedHandler)$' -count=1 -v`

The file compiled (no build error) and the brief's `grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'` view was:

```
--- FAIL: TestAuthenticatedRoutesAreRefusedWhileLocked (122.24s)
    --- FAIL: TestAuthenticatedRoutesAreRefusedWhileLocked/locked,_no_login (4.93s)
    --- FAIL: TestAuthenticatedRoutesAreRefusedWhileLocked/locked,_refused (30.54s)
--- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount (110.98s)
    --- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount/signed_in (9.97s)
    --- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount/grace (3.08s)
    --- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount/dormant (0.97s)
    --- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount/locked,_no_login (0.85s)
    --- FAIL: TestHealthIsOpenInEveryStateAndReportsTheAccount/locked,_refused (2.40s)
--- FAIL: TestLockedServerRefusesEveryPathButHealth (29.39s)
    --- FAIL: TestLockedServerRefusesEveryPathButHealth/read-only (14.19s)
    --- FAIL: TestLockedServerRefusesEveryPathButHealth/mutations_allowed (15.08s)
--- FAIL: TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused (1.50s)
    --- FAIL: TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused/locked,_no_login (0.03s)
    --- FAIL: TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused/locked,_refused (0.05s)
--- FAIL: TestAuthRefusesALockedAccountBeforeTheVault (1.47s)
--- FAIL: TestServeServesTheGatedHandler (1.71s)
FAIL	github.com/monoes/mono-agent/internal/httpapi	269.212s
```

This is exactly the list of Step 2 (all six tests; every subtest of the health test; only the two locked subtests of the first and fourth; both subtests of the third). The signed-in, grace and dormant subtests of the first and fourth tests PASSED in RED, as they should. The failure messages are the right ones: `got 404 404 page not found` / `405` / `307` where the 401 is wanted, `got 200 []` (the good bearer is served on a locked server), `the extra route ran 1 times, want 0`, `a locked server minted its bearer token: the vault was opened for a refused request`, `GET /nope over the listener = 404 map[], want the login_required 401`, and `GET /health = 200 {` with no account (5 times). The durations above are the machine under load (see Concerns), not the tests.

The preflight's RED line (`s.handler undefined`, `undefined: accountGate`) belongs to the older plan text: with the amended brief the tests build on the unmodified package, as Step 2 says.

### GREEN (after the eight edits)

1. The six tests, same command: `ok  	github.com/monoes/mono-agent/internal/httpapi	5.413s`. All 6 tests and their 17 subtests PASS.
2. The whole package, `go test ./internal/httpapi/ -count=1`: `ok ... 201.428s` (first run, machine at load average ~55), then `ok ... 12.949s` (second run, same load level; the brief expects "about ten"), then on the committed tree after all mutation work `ok ... 41.345s` (load average 75 at that moment). The existing tests install no guard, so `account.Require` fails open in the test binary (D24) and they pass unchanged, as the brief says.
3. Extras: `go build ./...` exit 0; `go vet ./internal/httpapi/` exit 0; `gofmt -l internal/httpapi` empty; the out-of-package tests that build this server with `ExtraRoutes` and drive `Handler()`, `go test ./internal/openaiapi/ -run '^(TestGatewayMountsNextToTheLegacyHTTPAPI|TestAKeyDoesNotOpenALegacyRoute)$' -count=1`: `ok ... 31.938s` (the first of them took 29.6 s, the machine again).
4. After the last mutation restore, the six tests once more on the committed tree: `ok ... 4.467s`.

Not run: `-race` (the brief asks for it for Tasks 6 and 7, not here). No new test calls `t.Parallel()`; `accounttest.Install` is called once per test or once per subtest.

## Mutation checks (the brief's four), made with the Edit tool, restored with `git checkout -- internal/httpapi/server.go`

| Mutation | Command (the brief's `-run`) | Result |
|---|---|---|
| http-auth: `if err := account.Require(r.Context()); err != nil && false {` | `TestAuthRefusesALockedAccountBeforeTheVault` | CAUGHT: `--- FAIL`; two failures: the refusal is the bearer's own 401 (not login_required) and `a locked server minted its bearer token` |
| http-gate: `s.handler = s.mux` | `TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused` + `TestLockedServerRefusesEveryPathButHealth` | CAUGHT by BOTH tests: `/read-only`, `/mutations_allowed`, and `/locked,_no_login`, `/locked,_refused` fail; the three allowed-state subtests pass. (The preflight's F5 said the ExtraRoutes test passed under this mutation in the old plan text; the amended brief builds the server through `NewServer`, and the mutation now fails it, as intended.) |
| http-serve: `Handler:      s.mux,` in `Serve` | `TestServeServesTheGatedHandler` | CAUGHT: `GET /nope over the listener = 404 map[], want the login_required 401` |
| http-health: `"account_":` | `TestHealthIsOpenInEveryStateAndReportsTheAccount` | CAUGHT: all five subtests fail (`GET /health = 200 {` without an account) |

Each restore left `git status --short` empty (checked after each, and `git diff HEAD` empty after the last).

## Verification beyond the brief (all restored or deleted; nothing of it is committed)

Four extra probes on `openWhileLocked`, to see whether each edge-case row of `TestLockedServerRefusesEveryPathButHealth` / the health test is load-bearing (all six tests run each time):

| Probe | Change | Result |
|---|---|---|
| A | the `/health` case returns `true` (no method check) | CAUGHT: `POST /health` gets 405 instead of 401 (both subtests of the locked-paths test) |
| B | drop `\|\| r.Method == http.MethodHead` | CAUGHT: `HEAD /health = 401, want 200` in the two locked subtests of the health test |
| C | `HasPrefix(p, "/v1/")` -> `HasPrefix(p, "/v1")` | CAUGHT: `/v1x` gets the mux's 404 (both subtests of the locked-paths test) |
| D | `p == "/health"` -> `strings.HasPrefix(p, "/health")` | CAUGHT: `/health/` gets the mux's 404 (both subtests of the locked-paths test) |

Spelling sweep (a temporary test file `zz_probe_tmp_test.go`, deleted after the run, never committed): a locked server (`LockedNoLogin`) with door-less `ExtraRoutes` (`/extra`, `/extra/{rest...}`, `POST /org-endpoint/{id}`, and a stand-in `/v1/{rest...}`), 52 request targets x 6 methods (`GET HEAD POST OPTIONS DELETE get`): `/health` and its encodings (`/%68ealth`), `//health`, `/./health`, `/health;x`, `/health?x`, `/HEALTH`; `/v1`, `/v1/`, `/V1/...`, `/%76%31/...`; dot-segments after `/v1` in every encoding (`%2e%2e`, `%2E%2E`, `..%2F`, `%2e%2e%2f`, `%2F..%2F`); absolute-form targets; `/extra`, `/%65xtra`, `//extra`; every protected route. Result: **no door-less handler was reached in any spelling (BYPASS = 0)**. The only rows that were neither the login_required 401 nor a redirect were `GET`/`HEAD` on the legal spellings of `/health` (200), and the paths whose decoded form is under `/v1/` (they reached the stand-in or the mux's 404, which is the plan: the `/v1` gateway has its own door, Task 5). Every redirect was followed once: the target was refused. Teeth check: with the http-gate mutation the same sweep reports 85 bypasses and fails, so the 0 is real evidence.

## Files changed

- `internal/httpapi/server.go` (modified, +47 -6)
- `internal/httpapi/account_door_test.go` (new, 213 lines)

`internal/accountdoor`, `internal/account`, `openapi.yaml` and everything else untouched (`openapi.yaml` is Task 3's).

## Self-review findings

1. The code and the test file are the brief's, byte for byte (see the verification above). I found nothing in the brief that contradicts the real code: every cited line number and signature matched (`server.go` 3-14, 27-32, 48, 74-77, 94, 155-157, 172, 201; `server_test.go` 37, 66, 75; `token.go` 20; `accounttest.Install`, `doortest.Modes`, `account.Require`, `account.CurrentStatus`, `accountdoor.WriteUnauthorized`/`SummaryOf`). `NewServer` is the only constructor of `Server` (a nil `handler` cannot occur) and `ensureToken` is reached only from `auth`.
2. Preflight F5 and F6 are already fixed in the amended brief and I confirmed it empirically: the ExtraRoutes test is caught by the http-gate mutation; no test installs two fixtures (the read-only/mutations loop uses subtests).
3. Every clause of `openWhileLocked` is pinned by some test (probes A-D) and `accountGate`, `Serve`, `auth` and `/health` by the brief's four mutations.
4. Minor (doc, left verbatim as the brief has it): `accountGate`'s comment says "every request but GET /health" while the code, and the health test, also leave `HEAD /health` open.
5. Minor (test): `TestServeServesTheGatedHandler` uses `http.Get` with no client timeout, so a handler that blocked forever would hang the test until the 10-minute `go test` limit instead of failing. Not realistic here; the only explicit time bound in the new tests is the brief's 15 s wait for `Serve` to stop, which no run came near.

## Concerns (none blocks the task)

- Intermediate state, as the plan intends: from this commit until Task 5 lands, the gate deliberately stands aside for `/v1` and `/v1/...`, and nothing behind it checks the account yet (`Gateway.auth` is Task 5). That is not a regression (before this commit nothing gated the main mux at all), but the program's rule "every door refuses a locked caller" is not met for `/v1` on the main mux until Task 5 is merged.
- Observation for the controller and the final adversarial review (not a contract change request): the gate classifies the DECODED path (`r.URL.Path`, so `%2F` is `/`), while the mux routes on the ESCAPED path and does not decode `%2F`. Today this is harmless: `/v1%2Fmodels` is open for the gate and a 404 from the mux, and the sweep found no bypass. It would matter only if an `ExtraRoutes` registrant ever added a root or wildcard-first-segment pattern (`/`, `/{x...}`), which would receive `/v1%2F...` as one segment while the gate calls it `/v1/...`. The registrants today are `GET /v1/models`, `GET /v1/models/{id...}`, `POST /v1/chat/completions`, `POST /v1/images/generations` (`internal/openaiapi/register.go:9-12`) and `POST /org-endpoint/{id}` (`internal/orgbridge/receiver.go:98`): all with a literal first segment. The wiring only delegates to those two and registers no pattern of its own (read, not assumed): `daemonRoutes` (`cmd/monoagentcli/api_gateway.go:439`) calls `composeRoutes` (`:445`, a loop over registrars) with `orgs.registerRoutes` (`cmd/monoagentcli/daemon_org.go:89`, which calls `Receiver.Register`) and `apiRuntime.mainMount` (`:317`, which calls `Gateway.Mount`); `cmd/monoagentcli/httpapi.go:77` passes `mainMount` alone; `api_gateway.go`, `daemon_org.go`, `httpapi.go` and `daemon.go` hold no other `Handle`/`HandleFunc`. A stricter `openWhileLocked` (classify by `r.URL.EscapedPath()`, or treat an encoded slash as not open) would close it; the brief fixes the code, so I did not change it.
- An authenticated request now runs `account.Require` twice (the gate, then `auth`), by design.
- The `openapi.yaml` schema of `GET /health` does not list `account` yet: Task 3.
- Environment: the machine ran at load average 50-75 while this task ran (other lanes' builds and `-race` suites). The first RED run took 269 s and the first whole-package run 201 s, the reruns 13 s and 41 s. No test of mine failed under load and none is timing-sensitive; I did not retry anything until it passed (the only reruns are the planned ones above).

## Contract change requests

None.

---

## Fix round 1 of 5 (review rv-b3b-t2, ruling R22): commit `79f9aea8`

Status: DONE. One commit on top of `841f57f7`: `79f9aea8 fix(httpapi): pin HEAD and OPTIONS and classify the escaped path at the door` (2 files, +20 -10; `server.go` 284 lines, `account_door_test.go` 221). Tree clean after it (`git status --short` empty, `git diff HEAD` empty). Concern 2 of the first report above (the decode mismatch) is closed by this commit; the `/v1` gap until Task 5 (concern 1) is unchanged and planned.

### What changed, by item

1. IMPORTANT (tests). `TestLockedServerRefusesEveryPathButHealth` has the rows `{"HEAD","/nope"}`, `{"OPTIONS","/workflows"}`, `{"OPTIONS","/health"}`. `TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused` calls `HEAD /extra` as well as `GET /extra`: HEAD matches the `GET /extra` pattern, so an allowed mode reaches the handler twice and a refused mode zero times (`want` is 2 or 0; the allowed modes passing also proves that HEAD does reach a GET pattern, so the `reached` check is not vacuous). `HEAD /workflows` is not added, as ruled. The table loop is the brief's plain loop, so a failing row prints no request name: the failure counts below identify the rows.
2. R22. `openWhileLocked` switches on `r.URL.EscapedPath()` instead of `r.URL.Path` (one token; `strings.HasPrefix(p, "/v1/")` is unchanged, so Task 5's http-v1 mutation text is intact). The rows `GET /v1%2Fmodels` and `GET /v1%2F..%2Fworkflows` are added. The only addition beyond the four items: two comment lines on `openWhileLocked` that say why the escaped path is judged (they document the R22 token, so that nobody "simplifies" it back).
3. The `accountGate` comment says "GET and HEAD /health" (reflowed, 5 lines).
4. `TestServeServesTheGatedHandler` uses `&http.Client{Timeout: 10 * time.Second}` instead of `http.Get`: a refusal that never comes now fails the test instead of hanging it (10 s is far above a loopback round trip, also at a load average above 100, and below the 15 s wait for `Serve` to stop).

### TDD evidence

Test-first: the test file was changed first, `server.go` was still the committed code. The named-row output in the RED and mutant blocks below comes from an interim shape of the test (each table row run as a subtest, which names it) that I took out before the report because it went beyond the four items. The committed shape is the plain loop, and it was re-verified by counts in the block after the mutation table. `server.go` is the same in both shapes.

RED, the new tests against the old gate (decoded path), `go test ./internal/httpapi/ -run '^(the six tests)$' -count=1 -v`. Only the two R22 rows fail, in both servers. Everything else passes, the three HEAD/OPTIONS rows and the extended ExtraRoutes test included: they pin what the code already does.

```
--- FAIL: TestLockedServerRefusesEveryPathButHealth (1.16s)
        --- FAIL: .../read-only/GET_/v1%2Fmodels (0.00s)
        --- FAIL: .../read-only/GET_/v1%2F..%2Fworkflows (0.00s)
        --- FAIL: .../mutations_allowed/GET_/v1%2Fmodels (0.00s)
        --- FAIL: .../mutations_allowed/GET_/v1%2F..%2Fworkflows (0.00s)
FAIL	github.com/monoes/mono-agent/internal/httpapi	4.633s
account_door_test.go:113: got 404 404 page not found      (4 times: the mux's 404 after the gate stood aside)
--- PASS: .../read-only/HEAD_/nope, .../OPTIONS_/workflows, .../OPTIONS_/health (and the same three in mutations_allowed)
--- PASS: TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused (all five modes)
```

GREEN after the one-token change, same command: `ok  github.com/monoes/mono-agent/internal/httpapi  3.418s`; the six tests pass and the five new rows PASS in both servers (interim shape). On the committed shape (the plain loop): `ok ... 1.377s`, the six tests pass.

Mutant of item 1 (`openWhileLocked` returns true for HEAD and OPTIONS: `if r.Method == http.MethodHead || r.Method == http.MethodOptions { return true }` before the switch), made on the interim shape, same six tests. FAIL exactly:

```
TestLockedServerRefusesEveryPathButHealth/{read-only,mutations_allowed}/HEAD_/nope          got 404 404 page not found      (2)
TestLockedServerRefusesEveryPathButHealth/{read-only,mutations_allowed}/OPTIONS_/workflows  got 405 Method Not Allowed      (2)
TestLockedServerRefusesEveryPathButHealth/{read-only,mutations_allowed}/OPTIONS_/health     got 405 Method Not Allowed      (2)
TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused/{locked,_no_login | locked,_refused}
    the extra route ran 1 times, want 0 (2); got 200 ... want the login_required 401 (2)
```

Restored with `git checkout`; the real code passes them (GREEN above).

The four brief mutations, each with its `-run`, each restored after. http-auth, http-serve and http-health were run on the interim shape (the tests they run did not change with the amend); http-gate was run again on the committed shape:

| Mutation | Result |
|---|---|
| http-auth | CAUGHT: `got 401 {` (the bearer's own 401) and `a locked server minted its bearer token` |
| http-gate | CAUGHT by both tests, on the committed shape too: `TestLockedServer.../read-only` and `/mutations_allowed`, ExtraRoutes `/locked,_no_login` and `/locked,_refused` fail; the three allowed modes pass |
| http-serve | CAUGHT: `GET /nope over the listener = 404 map[], want the login_required 401` |
| http-health | CAUGHT: all five subtests |

Re-verified on the committed shape (`79f9aea8`, the plain loop, where a failing row has no name and the counts identify it):

- the six tests: `ok ... 1.377s`;
- HEAD/OPTIONS mutant: `TestLockedServerRefusesEveryPathButHealth` fails in each server with exactly one `got 404 404 page not found` (`HEAD /nope`) and two `got 405 Method Not Allowed` (`OPTIONS /workflows`, `OPTIONS /health`), nothing else; the two locked subtests of the ExtraRoutes test fail with `the extra route ran 1 times, want 0` and `got 200 ... want the login_required 401`, one each;
- R22 revert (`EscapedPath()` back to `Path`): exactly two `got 404 404 page not found` per server (the two `%2F` rows), nothing else fails;
- the whole package `go test ./internal/httpapi/ -count=1`: `ok ... 4.202s`; `gofmt -l internal/httpapi` silent; `go vet ./internal/httpapi/` exit 0; the tree clean and `git diff HEAD` empty after every restore.

Before the amend (interim shape, the same `server.go`): the whole package `ok ... 6.586s` and `ok ... 4.156s`; `go build ./...` exit 0; `go test ./internal/openaiapi/ -run '^(TestGatewayMountsNextToTheLegacyHTTPAPI|TestAKeyDoesNotOpenALegacyRoute)$' -count=1`: ok.

### Beyond the request (a temporary test file, deleted, not committed)

The spelling sweep of the first report, rerun on the final code and widened with a door-less ROOT CATCH-ALL (`mux.HandleFunc("/", ...)` through ExtraRoutes), which is the hazard R22 exists for: 59 targets x 6 methods, BYPASS = 0 (neither the root handler nor `/extra` nor `/org-endpoint/{id}` ever ran for a locked caller). Teeth check: with the old line put back (`r.URL.Path`) the same sweep reports 42 bypass rows = 7 spellings x 6 methods, all with `%2F` after `v1` (`/v1%2Fmodels`, `/v1%2fmodels`, `/v1%2F`, `/v1%2F..%2Fextra`, `/v1%2f..%2fextra`, `/v1%2Fmodels%2F..%2F..%2Fextra`, `http://localhost/v1%2Fmodels`): they reached the root handler while locked. So the hazard was real the day someone registers a root or wildcard route through ExtraRoutes, and R22 closes it. Nothing registers one today (see the first report).

### Behaviour to know (fail closed, a consequence of R22)

Every percent-encoded spelling of the open paths themselves is now refused while locked: `/%68ealth` (it was a 200) and `/%76%31/models` (it was let through to the gateway). No client sends them, and nothing changes while the account is ok, grace or dormant. The open set is now literally `/health` (GET, HEAD), `/v1` and `/v1/...` as the client wrote them.

### Not done, as told

The review's Minor 3 (`OPTIONS *`) is untouched. Nothing else changed. Suggestion, not done: the table loop could run each row as a subtest (`t.Run(method+" "+path, ...)`) so that a failing row names itself; I tried it, it went beyond the four items, so I took it out.
