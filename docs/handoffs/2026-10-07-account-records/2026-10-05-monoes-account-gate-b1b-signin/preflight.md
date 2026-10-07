> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Pre-flight: plan B1b (sign-in), 2026-10-07

Plan: `.claude/worktrees/feat+monoes-account-gate/docs/mastermind/plans/2026-10-05-monoes-account-gate-b1b-signin.md` (docs branch `eb54672c`, 6,836 lines, 12 tasks). Cited below as `plan:N`.
Base it consumes: `feat/account-core` @ `dbe36f97` (master `f4441a2a` + B1a). Tip files are cited as `file:N` under `internal/account/` unless a path is given.
Read: the whole plan; index §2, §3, §4 (`index:N`); spec §2, §4, §7, §8, §13 with A18-A25 and R1-R7 (`spec:N`); the program ledger's rulings R1-R7; the B1a package with its source-scanning tests; every master file the plan cites.

## How it was checked (no repository and no plan changed)
- **Replay A, all twelve tasks at once** on a `git archive dbe36f97` export in the session scratchpad, by a script that applies every Create / Replace-the-whole-content / replace-this-text / replace-or-delete-this-block / replace-or-delete-the-declaration instruction in plan order and refuses an anchor that does not match exactly once. All 69 instructions matched exactly once; the abbreviated blocks have the line counts the plan states. `go build ./...` clean; `go vet` of account and library/... clean, default and `-tags devaccount`.
- Tests on replay A: `go test -race` of `internal/account` (all of B1a's tests and B1b's together) and `accounttest`: ok (75 s). `-tags devaccount -race` of `internal/account`: ok. `-race` of `internal/library` and `libraryfake`: ok; `-tags devaccount`: ok. The CLI tests of Tasks 10-12 (`TestLibrary.*`, the account tests, the login-required tests, 19 tests) with `-race`: ok.
- **Replay B, task by task** on a fresh export: each task's `_test.go` edits first, then `go vet` of its package (its Step 2), then its code. Every Step 2 failed with exactly the first error line the plan prints (oauth_test.go:37:15, fake_test.go:46:14, refresh_test.go:24:21, login_test.go:21:70, adopt_test.go:24:21, defaultguard_test.go:17:20, session_test.go:18:77, adopt_test.go:95:26, login_required_test.go:15:9, account_test.go:120:11). Task 12 Step 3 failed exactly its four named tests, and the regression guard passed. `gofmt -l` empty after every task. Step 8 (whole `cmd/monoagentcli`, 273 s): fails only `TestCaptureTaskFilesOnTheBoard`, `TestCoderConversationFolders`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, all four on index §4's list (index:412).
- Task 6 Step 7: each of its five mutations of `logout.go` fails exactly the tests and messages it names.
- Three probes beyond the plan (throwaway tests, removed afterwards): P1 a library refresh whose vault save fails, P2 `revoke` without its plain-HTTP guard, P3 a library login whose refresh token monoes.me has already rotated away. The fixes recommended for findings 1-3 were applied to replay B and tested there.
- Scratch only (the session scratchpad, not in any repository): the replay scripts `apply_plan.py` (all tasks), `apply_task.py` (one task, tests or rest), `failfirst.sh`, `mutate.py`, the probes `zz_preflight*_test.go.txt`, and the logs, all in `$LOCAL_CLAUDE_SCRATCH/-Users-morteza-Desktop-monoes-mono-agent/ab93060e-329b-4819-88ef-4bab24fc04a4/scratchpad/`. To replay an amended plan, run `python3 -I apply_plan.py <plan> <fresh git-archive export of dbe36f97>`.

## Findings that need a ruling or a plan fix (detail in parts 3 and 5)
1. **DRIFT, safety (A24(d)), Task 8.** `library.Client.refresh` (plan:4874-4877) returns a failed vault save after an answered grant and keeps the spent token: the vault still holds it, and the same call's 401 retry (tip `internal/library/client.go:182-188`, after `currentToken` falls back to the old token, tip `internal/library/auth.go:56`) presents it again. P1: refreshes 1, replays 1, vault still holds the spent token. Index §4 (index:419) counts "an answered grant whose new refresh token could not be saved" as unknown, and asks for "a test per consumer ... with a key store that fails after the answer"; the library adapter has neither. Fix: on a `setToken` failure after an answered grant, `dropLogin`, return the error, and add a test with a `TokenStore` whose `Save` fails (expect `Replays == 0` and no second token request). **Proven on replay B:** with `c.dropLogin(gctx)` before that `return`, P1 shows replays 0 and the spent token gone from the vault. That holds when the vault's delete works; when the delete fails too, the vault keeps the token, a residual of A21's kind, but this process presents nothing more. `go test -race ./internal/library/...` and the CLI `TestLibrary.*` and account tests still pass. The behaviour predates B1b, but Task 8 is the task that claims A24 coverage for this path, and index §4 asks for this test, so **amend the plan before Task 8 is dispatched**. It sits on the transport and adoption surface that the ledger puts under adversarial review.
2. **DRIFT, ruling, Task 8.** P3: a vault login whose refresh token monoes.me already rotated away gets `invalid_grant`. `GrantSettled` reads a complete 4xx as settled (plan:2069-2074), so `refresh` keeps the login (plan:4868-4872), and every later command presents it again (P3: replays 1, then 2). Plan:53 claims "A dead or spent refresh token is never presented again"; adoption drops on `invalid_grant` (plan:5455-5456); index:401 states the adapter's rule only for unknown and settled outcomes. The behaviour predates B1b. Before R1 ships, each later presentation ends every refresh token of the account. Recommend `dropLogin` on `invalid_grant` too, plus a test. **Proven on replay B:** `if !settled || (errors.As(err, &ae) && ae.Code == "invalid_grant") { c.dropLogin(gctx) }`, with `errors` imported in auth.go, stops the second presentation (P3: replays stay at 1, and the vault no longer keeps the dead token). The same suites pass with no test edit. `TestReadRefreshesOnceOn401` keeps its counts, because the fake answers `invalid_grant` before it counts a refresh.
3. **DRIFT, Task 6.** `TestLogoutNeverRevokesOverPlainHTTP` (plan:2956-2978) no longer tests what it names. Its store holds only `refresh.enc`, and `revocable(nil, nil)` is false (plan:3411-3416), so no revocation is tried whatever the scheme. P2: removing `if checkHost(c.Host) != nil { return }` (plan:3454-3456) leaves every logout test green. Review Focus 4 (plan:40) says this test pins "a refresh token never travels in the clear". Fix: save a readable, revocable `session.json` in that test first (for example `{V: 1, Host: "http://monoes.example", AccessToken: "x"}`). Do it as a sibling case, because the test's second half needs no `session.json`. **Proven:** that sibling case passes as is and fails under M6 (`1 requests went out`); the plan's test still passes under M6.
4. **DRIFT, ruling.** Index §2 (index:53) says "a default build honors `MONOES_BASE_URL` nowhere: the library talks only to monoes.me". The plan keeps `library.BaseURL()` honoring it (tip `internal/library/types.go:60-65`; plan:51 and plan:4060-4062: "MONOES_BASE_URL still redirects the library"), and implements spec A18's three concrete rules (spec:356). No plan owns making the library ignore it, and doing so would break every CLI library test (`newLibFixture` sets it, tip `cmd/monoagentcli/library_test.go:30`). Recommend ruling that index §2 means A18's three rules.
5. **DRIFT, ruling, Task 11.** `describeStatus` (plan:6096-6112) writes its own text for six locked reasons. They differ from B1a's frozen reason lines (`errors.go:43-62`); only `unconfirmed` matches. Index §3.4 (index:355) says a plan that prints one "takes it from there". Either print `(&account.LoginRequiredError{Status: st}).Error()` for locked states, or rule that `account status` is not a refusal.
6. **DRIFT, process.** The B1b worktree will be cut from `feat/account-core`, which carries stale copies of the plans: `dbe36f97:docs/mastermind/plans/2026-10-05-monoes-account-gate-b1b-signin.md` is 5,497 lines and has no `GrantSettled`, and its index is 410 lines against 439. Every brief must give the absolute path of the docs-branch plan (`eb54672c`), never the copy in the implementer's own tree.
7. **COSMETIC, stale rationale that would be committed.** The `SetSealerForTest` seam is justified by "internal/secrets remembers the account key it first made for the whole process (getOrCreateKEK, keyring.go:93)" (plan:61, 3636, 3713-3716, 3942-3949, 5773-5775, 6820, 6833). At the tip, `secrets.AccountKEK` reads the key store afresh at every call (`internal/secrets/account_kek.go:50-55`, `:69-103`, through the non-memoized `fetchOrCreateKEK`, `keyring.go:145`) and never uses `getOrCreateKEK`. The seam is harmless and still keeps the CLI tests independent of the mock keyring, but three comments that would be committed state something false: the `defaultguard.go` doc, the `defaultguard_test.go` comment and the `library_test.go` comment. Reword them. The same claim is in **index §3.6 (index:387)**, as the reason other plans must call `SetSealerForTest`; the index wins on precedence, so correct it there too, or other plans' briefs will repeat it.
8. COSMETIC: see part 5 (D4-D11).

No BLOCKS: every task can be done as written, and its tests pass on the real tip.

## 1. Task table (12 rows)
| Task | Result | Note |
|---|---|---|
| T1 account/oauth.go | agrees | tests match code; Step 2 as printed (plan:234); `-race` ok. COSMETIC D7, D8 |
| T2 library delegates | agrees | 7 edits unique; library tests ok before and after. Note: the code exchange no longer runs under the login-timeout context (tip auth.go:207 vs plan:568-583); harmless |
| T3 libraryfake | agrees | 6 fake.go edits unique (plan:955-1048); Step 2 as printed (plan:945); fake and library tests ok with `-race`. COSMETIC D5 |
| T4 refresher | agrees | all 19 Settled rows and 7 fake rows pass on dbe36f97; Step 2 as printed (plan:1897) |
| T5 sign-in client | agrees | Step 2 as printed (plan:2365); `-race` ok |
| T6 logout, adoption | agrees | Step 2 as printed (plan:3344); Step 7's 5 mutations fail as listed. DRIFT finding 3 |
| T7 host, default guard, dev key | agrees | Step 2 as printed (plan:3885); `devPublicKeyHex` (plan:4101) = public half of `accounttest/devkey.go:17` (recomputed: b2f5cb7a…4328cff); both new hooks pass B1a's source scanners; package ok default and devaccount under `-race`. COSMETIC finding 7 |
| T8 session source | agrees with itself | Step 2 as printed (plan:4606); `-race` ok. DRIFT findings 1 and 2; plan:4150 says Logout's "head" changes but Step 4 replaces it whole (plan:4882-4898) |
| T9 AdoptIntoAccount | agrees | Step 2 as printed (plan:5357); ok. COSMETIC D4 |
| T10 login_required.go | agrees | anchors library.go:124-139 and 153-154 exact; Step 2 as printed (plan:5627); ok |
| T11 account commands | agrees | Step 2 as printed (plan:5948); `-race` ok. DRIFT finding 5 |
| T12 aliases | agrees | Step 3 fails exactly its 4 named tests; `-race` ok; Step 8 as predicted. COSMETIC D6 |

## 2. Pair table (19 rows)
| Pair | Produced → consumed | Result |
|---|---|---|
| T1 → T2 | `OAuthEndpoints{Authorization,Token,Revocation}Endpoint`, `DiscoverEndpoints(ctx, *http.Client, string) (*OAuthEndpoints, error)`, `AuthorizeOptions{Open, OnURL, Timeout, Label, SuccessText}`, `AuthorizeInBrowser(ctx, string, url.Values, AuthorizeOptions) (*AuthorizeResult, error)`, `AuthorizeResult{Code, Redirect, Verifier}` (plan:268-338) → library auth.go and client.go (plan:544-583, 618) | agrees |
| T1 → T4, T5, T6 | `DiscoverEndpoints` → `tokenEndpoint` (plan:1979), `Login` (plan:2502-2509), `revoke` (plan:3459) | agrees |
| T3 → T4-T12 tests | `New`, `TrustKey(testing.TB)`, `NewGrant(string) (access, refresh)`, `SetRefreshMode` and its 8 modes, `SetOpaqueTokens`, `SetEmailOpaque`, `SetEmailTrade`, `Block`/`Unblock(userID)`, `SetClock`, `OnToken(func())`, `LastResource()`, `TokenRequests() []TokenRequest{Form, Header}`, `Replays`/`Refreshes int`, `AccessTTL`, `Requests` | agrees (every consumer compiles and passes) |
| T3 ↔ T4 | `refuse()` answers before it reads the token (plan:1406-1425), so only `RefreshLost` spends `rt`, and it is the last row (plan:1599) | agrees |
| T4 → T5 | `newHTTPClient(time.Duration)`, `checkHost(string) error`, `oauthCode(string) string`, `NewRefresher(string) Refresher` (plan:2426, 2499, 2475, 2579) | agrees |
| T4 → T6 | `NewRefresher` with the exact `TransientError.Settled` → `exchangeOlder` (plan:3556-3569) | agrees |
| T4 → T8 | `GrantSettled(written bool, status int, complete bool) bool` (plan:2069) → `refreshGrant` (plan:4666-4673) | agrees (finding 2 is about what it means for `invalid_grant`) |
| T5 → T6 | `Client{Host, HTTP, Store, Now}`, `(*Client).session(ctx, *TokenSet, *User) (*Session, error)`, `commit(*Session, string) error`, `*unusableError` (plan:2596-2619 → 3572-3587) | agrees |
| T5 → T7 | `NewClient(string, Store) *Client`, `LoginOptions`, the four Client methods → package functions (plan:4002-4049) | agrees |
| T6 → T9 | `(*Client).Adopt(ctx, func() (string, *User, bool), func(AdoptResult)) (AdoptResult, error)`; `AdoptResult{Adopted, Status, Tokens, Dead, Unconfirmed}` (plan:3487-3520 → 5438-5469) | agrees |
| T7 → T9 | `DefaultStore() (Store, error)`, `Host() string`, `NewDefaultGuard() (*Guard, error)`, `SetHostForTest`, `SetSealerForTest` (plan:5016-5025, 5422-5427) | agrees |
| T7 → T11 | `Login`, `SendEmailCode`, `VerifyEmailCode`, `Logout`, `NewDefaultGuard`, `Host`, `ErrBadCode` (plan:6026-6203); the hooks in `newLibFixture` (plan:5776-5778) | agrees |
| T2 ↔ T8 (auth.go, client.go) | T8's anchors (client.go:35-46, auth.go:16-37, 125-137, 228) are T2's output, checked on a replay of T1-T2 | agrees |
| T8 → T12 | `AccountSession{Guard, Store, Offline}`, `Client.Session`, `(*Client).LogoutLegacy(ctx) error` (plan:4712-4749, 4793, 4902) → plan:6598-6642, 6686, 6749 | agrees |
| T10 → T11, T12 | `newLoginRequiredError`, `libraryLoginRequired`, `isLoginRequired`; `libErr` → `libraryLoginRequired()` (plan:5706) | agrees |
| T10 ↔ T12 (library.go) | T12's anchors (3-20, 37, 54-59, 61-66, 68-84, 205, 228, 271, 334, 387) are T10's output, checked on a replay of T1-T11 | agrees |
| T11 → T12 | `signInFlags.add`, `signIn(cmd, cfg, *signInFlags, string) (Status, bool, error)` (plan:6151) → plan:6716; `(*libFixture).backdate` (account_test.go, plan:5833) → library_test.go (plan:6389, 6414) | agrees |
| T11 ↔ T12 (library_test.go) | T12's anchors (3-17, 142-175, 177-193, 319-358) are T11's output, checked | agrees |
| B1a ↔ T7 (keys_devaccount.go) | B1a stub `extraKeys() []Key { return nil }` (keys_devaccount.go:1-8) replaced whole; same signature; it takes no `globalsMu` (keys_default.go:5-8) | agrees |

## 3. Contract drift: `internal/account`, `accounttest`, `internal/secrets` against the tip
### 3a. Identifiers (42 rows; ✓ means name, signature and behaviour as the plan uses them)
| Identifier | Plan | Tip | Result |
|---|---|---|---|
| `HostURL`, `Issuer`, `Audience`, `ClientID`, `ConnectTimeout` | 1478-1480, 1935, 2020, 3765 | claims.go:11-14, 20 | ✓ |
| `State`, `StateOK/Grace/Locked` | 2217, 4731, 5419 | state.go:6-12 | ✓ |
| `Reason` and the 12 reasons (`NotLoggedIn`, `Refused`, `Unreachable`, `ServerError`, `Unconfirmed`, `Invalid`, `KeyUnknown`, `ClockSkew`, …) | 1593-1599, 3015, 6089-6110 | state.go:15-30 | ✓ |
| `User{ID, Email, Username}` | 2642-2665, 4738, 5446 | state.go:33-37 | ✓ |
| `Status{V, State, Reason, User, Plan, ValidUntil, GraceUntil, …}` | 4737, 5862, 6661-6666 | state.go:40-51 | ✓ |
| `Session` (with `PendingSince`, `LastResult`, `HW`, `LastAttempt`) | 2241, 2920, 3204, 3415-3448, 4224 | session.go:13-25 | ✓ |
| `stateRefused` (unexported) | 3415, 3544 | session.go:30 | ✓ |
| `NewSession(host, accessToken string, user *User, now time.Time) (*Session, error)`; sets HW=iat, LastAttempt=now, LastResult "ok" | 2605 (test 2226 expects those) | session.go:37-49 | ✓ |
| `Evaluate(*Session, time.Time) Status` | 2636, 2817, 3589 | session.go:57 | ✓ |
| `EnforceDate() time.Time` | 5416 | rollout.go:12 | ✓ |
| `Store` (8 methods) | 2615-2618, 3379-3397, 3521-3526 | store.go:48-57 | ✓ |
| `Store.Lock`: waits until ctx ends, creates dir and lock file | 3379, 3521 (after the `stored()` pre-check, plan:3421-3428) | store.go:318-353 | ✓ (no bound: D9) |
| `OpenStore(dir string, s Sealer) Store` | 2174, 3990 | store.go:183 | ✓ |
| `DefaultDir() (string, error)` | 3986 | store.go:165 | ✓ |
| `SaveRefresh`/`LoadRefresh` surface the sealer's own error (callKeyStore) | tests 2280, 3182, 5237 (`errors.Is(err, ErrKeyringUnavailable)`) | store.go:122-161, 256-289 | ✓ |
| `Sealer` | 2270-2271, 5224-5225 | sealer.go:21 | ✓ |
| `ErrKeyringUnavailable` | 2120, 6213 | sealer.go:17 | ✓ |
| `NewKeyringSealer()`, `NewInteractiveKeyringSealer()`, `NewMemorySealer()` | 3975-3977, 2174 | sealer.go:64, 80, 108 | ✓ |
| `Key{KID, Public}`, `TrustedKeys()` | 1465, 3794 | keys.go:11, 32 | ✓ |
| `pinKey(kid, publicHex string) Key`, `extraKeys() []Key` | 4104 | keys.go:69, keys_devaccount.go:8 | ✓ |
| `Verify(string, time.Time) (*Receipt, error)`: EdDSA, kid pinned, iss, aud, azp/client_id, sub, iat/exp ≤ 24 h | 742, 1570, 2598; fake's token plan:1472-1483 | verify.go:74, 88-190 | ✓ (the fake's JWT passes every check) |
| `Receipt{Sub, IssuedAt, ExpiresAt}` | 757, 2226 | verify.go:15-21 | ✓ |
| `VerifyError{Reason}` | 2443-2451, 2585-2586 | verify.go:30 | ✓ |
| `TokenSet{AccessToken, RefreshToken}` (an empty RefreshToken = unknown) | 2052-2055 (never returned empty) | guard.go:10-15 | ✓ |
| `Refresher` (nil set on error; no typed nil; must not rely on the caller's cancellation) | 1963-2056 | guard.go:17-50 | ✓ (its own 10 s client timeout ends a grant before the guard's 20 s; read as unknown; index:391 accepts it) |
| `RefusedError{Description}` | 2047 | errors.go:67 | ✓ |
| `TransientError{Reason, Settled, Err}` + `Unwrap` | 2008-2053 | errors.go:96-117 | ✓ (Settled set exactly; R2 holds) |
| `GuardOptions{Store, Refresher, Now, Poll}`, `NewGuard` | 4012, 4197 | guard.go:55, 113 | ✓ |
| `(*Guard).Status`, `Refresh`, `EnsureFresh`, `Close` | 4727-4729, 5465, 5169, 6050 | guard.go:138, guard_refresh.go:67, guard_refresh.go:55, guard.go:290 | ✓ |
| `refreshCallTimeout` (20 s) | 3554 | guard.go:66 | ✓ |
| `requireTestBinary`, `testStateEnv` | 3929-3930, 3952-3953 | testhooks.go:20-23 | ✓ (the scanners `fortest_source_more_test.go:38` and `parallel_more_test.go:91` pass with both new hooks) |
| `SetTrustedKeysForTest`, `SetEnforceFromForTest` | 1465, 2807, 5018 | testhooks.go:33, 50 | ✓ |
| `LoginRequiredError{Status}`, `.Error()`, `LoginRequiredMessage` | 5669, 5600 | errors.go:10-21 | ✓ |
| `(*LoginRequiredError).JSONErrorFields()` | mirrored at 5660-5663 | errors.go:29-35 | ✓ same keys; values typed, not `string(...)`: D10 |
| `IsLoginRequired(error) bool` | 5681 | errors.go:38 | ✓ |
| `CurrentStatus() Status` | 5675 | process.go:82 | ✓ |
| `accounttest.DevKeyPair()`, `accounttest.DevKID` | 1457, 1465, 1477, 3792 | accounttest/devkey.go:9, 21 | ✓ |
| `secrets.AccountKEK` (through the sealers) | rationale at 61, 3636, … | internal/secrets/account_kek.go:55 | ✗ claim stale: finding 7 |
| B1a import-rule test | 49 ("only stdlib and internal/secrets") | hygiene_test.go:16, 40-50 (also x/sys/windows in two files) | ✓ passes; paraphrase incomplete (cosmetic) |
| `doc.go` file list | not touched | doc.go:23-55 | 8 new files left out; keys_devaccount.go's line (doc.go:33) still describes the stub (cosmetic) |
| B1b's own `GrantSettled` | 2069-2074 | (new) | the rule of index:397, 419 exactly; a complete 4xx `invalid_grant` returns true, so each caller tests the refusal first (the refresher does, plan:2046; the library does not: finding 2) |
| Proving base | 6818 (`85d1393b`) | tip `dbe36f97` is 16 commits later (F-1 to F-7 in the guard) | no drift: replay A passes on the tip |

### 3b. Mismatches
- None of name, signature or return type: replay A compiles and vets on `dbe36f97`.
- Behaviour: findings 1, 2 (the library adapter against the A24 rules of index §3.6/§4), 7 (the `secrets` key claim) and D10 (map value types). The B1a guard behaves as every B1b test expects after F-1 to F-7.

## 4. Real-code references (every one cited by a task; 26 rows)
| Cited (plan) | Tip | Result |
|---|---|---|
| `internal/secrets/keyring.go:93` getOrCreateKEK (61, 3636) | keyring.go:93 | exists; the claim made about it is stale (finding 7) |
| B1a `TestImportsOnlyWhatTheImportRuleAllows` (49) | internal/account/hygiene_test.go:16 | ✓ |
| monoes-landing `src/app/api/auth/agent/claim/verify/route.ts` 92-112 (52, 2113) | landing clone, same lines: opaque token, no `refresh_token` | ✓ |
| `cmd/monoagentcli/root.go` line 110 (5742, 6224-6226) | root.go:110-111 | ✓ unique |
| T2 auth.go 3-18, 20-35, 72-116, 199-294, 318-328, 390-397 (484) | exact | ✓ (`onBase`, `writeCallbackPage` and `randomString` have no doc comment; harmless) |
| T2 client.go 3-21, 43 (485) | exact | ✓ |
| T3 libraryfake/oauth.go 1-108 (654) | 108 lines | ✓ |
| T3 fake.go 50, 82, 90, 168, 209, 231-233 (655) | exact, each anchor unique | ✓ |
| T7 keys_devaccount.go 1-8 (3642) | exact | ✓ |
| T8 client.go 36-46 and auth.go 16-37, 125-137, 228-251 "as Task 2 leaves it" (4149-4150) | verified on a T1-T2 replay | ✓ |
| T9 `library.VaultStore{DB, ProfileID, BaseURL}` (5020) | internal/library/store.go:19-22 | ✓ |
| T9 `profiledir.List(ctx, *sql.DB) ([]Profile, error)`, `Profile{ID, Name, Default}` (5428, 5475-5487) | internal/profiledir/list.go:23-38 | ✓ |
| T9 `testdb.Open(t) *storage.Database` with `.DB` (5019) | internal/testdb/testdb.go:69, internal/storage/database.go:21-22 | ✓ |
| T9 `profiles (id, name, created_at, root_dir, icon)` and the `default` row (5036) | data/migrations/011_profiles.sql:13, 028, 039 | ✓ |
| T10 library.go 124-139 and 153-154 (5532) | exact (124-138 plus the blank line) | ✓ |
| T10 `cliError`, `jsonErrorFields`, `reportCommandError` (5536) | exitcodes.go:20, automation.go:93, main.go:98 | ✓ |
| T10 `TestLibraryReadsNeedALogin`, `TestLibraryErrorsExitCodes` (5712) | cmd/monoagentcli/library_test.go:281, 246 | ✓ |
| T11 library_test.go 3-16, 25-44, and the anchor text at 30-31 (5743, 5765) | exact | ✓ |
| T11 `printLib`, `withJSONErrors`, `writeJSONTo`, `reportedError`, `openLoginURL`, `readLine`, `nonEmptyStr` (5747) | library.go:176, automation.go:56, 167, 120, library.go:187, 383, 237 | ✓ |
| T11 `runCLI`, `lastJSONObject`, `errAuthConnection`, `errInvalidInput` | setup_test.go:201, library_install.go:291, exitcodes.go:43, 39 | ✓ |
| T12 library.go "as Task 10 leaves it" (6268) | verified on a T1-T11 replay (3-20, 37, 54, 61, 68, 205, 228, 271, 334, 387) | ✓ |
| T12 library_test.go "as Task 11 leaves it" (6269) | verified (3-17, 142, 177, 319-358) | ✓ |
| T12 status command `ctx := cmd.Context()` / `t, err := c.Token(ctx)` and `offline` (6676-6689) | library.go:255-256, 245 | ✓ unique |
| T12 `initDB`, `pack`, `libList`, `libInstallResult`, `libStatus`, `noSeed`, `f.login`, `f.loginRequired` | present (replay compiles) | ✓ |
| `library.DefaultBaseURL` equals `account.HostURL`, which T12's guard relies on (6710) | internal/library/types.go:17 and claims.go:11, both `https://monoes.me` | ✓ |
| Spec sections and ids cited (§4.1-§4.8, §5, §7, §8 step 3, §9, §11-§13; D5, D10, D13, D14, D21-D24, D27; A18-A25) | spec:98-363 | ✓ all exist |

## 5. Plan-mandated defects
- D1, D2, D3 are findings 1, 2 and 3 above.
- D4 (COSMETIC, low; Task 9) swallowed errors: `_ = vs.Delete(vctx)` (plan:5456) and `keepAlive`'s `_ = vs.Save(ctx, &nt)` (plan:5496). If `keepAlive` fails to save, the vault keeps the older token that the exchange just spent, and nothing deletes it. This is only reachable when monoes.me answers an opaque token, which means before plan A. Better: on a failed save, try `vs.Delete`.
- D5 (COSMETIC, Task 4) `TestRefreshRequestSendsOnlyTheGrantFields` checks `Header.Get("Authorization") != ""` (plan:1838), but the fake deletes that header before it records the request (plan:1131-1133, 1371). That half can never fail; the Cookie half stands.
- D6 (COSMETIC, Task 12 Step 8) `! (go test … | grep '^--- FAIL' | grep -v …)` (plan:6787) also exits 0 when the package does not build, or when a run panics or times out without a `--- FAIL` line. Also check that the `FAIL`/`ok` summary line is present.
- D7 (COSMETIC) goroutines in tests are not awaited, and call `t.Error` if they fail after the test ends: `go followRedirect(…)` (plan:151), the goroutine in `TestAuthorizeIgnoresAForeignStateAndKeepsWaiting` (plan:170-177), and `userBrowser` (plan:2181-2190). This is the same pattern as the existing `browser` helper at tip `internal/library/library_test.go:30-42`.
- D8 (COSMETIC) doc comments name the wrong helper: `followRedirect` is documented as "browse" (plan:127); "plays the user's userBrowser" (plan:2178); `accountStatus` is documented as "status runs" (plan:5818).
- D9 (COSMETIC) `Client.Adopt` and `Client.Logout` wait on `Store.Lock(ctx)` with no bound (plan:3379, 3521; store.go:318-345). The guard bounds its wait with `lockWaitTimeout` (guard.go:67). Adoption is implicit (B5a's root pre-run), so while another process's interactive passphrase prompt holds `session.lock`, it would block a command. B5a can pass a bounded ctx.
- D10 (COSMETIC) `loginRequiredError.JSONErrorFields` puts typed `account.State`/`account.Reason` into the map (plan:5660-5663), where B1a puts `string(...)` (errors.go:33). The JSON is identical, and no consumer plan asserts the Go types.
- D11 (COSMETIC) `refreshGrant` (plan:4650-4686) is a near-copy of `httpRefresher.Refresh` (plan:2006-2056): the trace, the body read and the `GrantSettled` calls. The plan argues the reason (no `resource`, a different error type); a reviewer may ask for a shared helper. Some test failure messages print the fake's whole answer body (plan:774, 795, 912, 915). Today those are error bodies only, but a regression to a 200 would print fake tokens.
- Nothing is left on disk: every test uses `t.TempDir()` or a temporary HOME, and closes its servers.

## 6. Constraint check (index §2 bullets; rulings R1-R7)
- Go 1.26, no new dependency, EdDSA only: ✓ (new account files import the standard library only; B1a's import-rule test passes).
- Grace, clock guard and states: B1a's; B1b builds sessions only through `NewSession` (plan:2605) ✓.
- Only `invalid_grant` is a refusal; `Settled` is exact; the unknown outcome is dropped: ✓ for the refresher and adoption; ✗ for the library adapter's answered-but-not-saved case (finding 1); its `invalid_grant` case is finding 2.
- Refresh: `ConnectTimeout` dial (plan:1935), `resource=Audience` (plan:2020) ✓.
- Storage and nothing on disk until a write: ✓ (`TestNewDefaultGuardCreatesNothing`, `TestLogoutWithNothingToForgetLeavesNoFiles`, `TestAdoptWritesNothingOnAMachineWithoutAnOlderLogin`, and `account status` on an empty HOME in `TestAccountLoginStatusLogout`; all pass).
- Dormant: adoption is a no-op (plan:5416); only explicit `account` and `library` commands call monoes.me (`AccountSession` refreshes only for an explicit library command) ✓. The A24 drop applies while dormant too, as ruled on 2026-10-06 (plan:50) ✓.
- Process globals and seams: `SetHostForTest` and `SetSealerForTest` start like B1a's hooks and carry the marker (B1a's scanners pass); the new globals have their own RWMutexes ✓; no new test calls `t.Parallel()` ✓.
- Exit 4 and the first line `Log in to monoes.me first: …`: ✓ (Task 10).
- `devaccount` and `MONOES_BASE_URL`: `account.Host()` ignores it in a default build ✓ (`TestDefaultBuildIgnoresMonoesBaseURL`); `library login` refuses another host and names `-tags devaccount` ✓; the token goes only to its host ✓. The library itself still follows `MONOES_BASE_URL` (finding 4).
- No token in output: ✓ (lengths, booleans and `sessionSummary`). D11 notes four failure paths that print a fake's answer body.
- Files under 500 lines: ✓ (the largest is `libraryfake/fake.go` at 420).
- B5b-only files (README, AGENTS.md, locales, CHANGELOG): not touched ✓.
- Commit steps: two separate commands in conventional style ✓. They hard-code `Co-Authored-By: Claude Sonnet 5.5`, as index:413 does; an implementer on another model should use its own harness's line.
- R1 (families in plan A; client rules unchanged): ✓, no client constant changes. The fake and the plan's comments still describe the pre-R1 server ("ends every refresh token of the account"); that is stricter than the server will be, so it stays correct for tests.
- R2 (every 5xx unknown): ✓ (`GrantSettled`, plan:2069-2074; the 500/502/504/524 rows).
- R3 (no build floor): ✓, nothing in B1b.
- R4: B3a's; nothing in B1b.
- R5: B1b's new account tests will also run in C0's informational Windows/macOS job. Nothing in them is Unix-only (they set `HOME` and `USERPROFILE`, and use loopback listeners and flock/LockFileEx through the store). The dial and reset rows of `TestRefresherSettledIsExactlyWhatTheClientCanKnow` may be slower on Windows (a refused loopback connect retries for about 2 s) but should classify the same way.
- R6, R7: consistent. The plan calls only the revoke route's behaviour on a rotated token "unmeasured" (plan:55), which R7 does not cover.
