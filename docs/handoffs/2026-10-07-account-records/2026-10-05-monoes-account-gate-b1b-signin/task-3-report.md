> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Task 3 report: libraryfake signs JWT access tokens and can fail, block and tell time

Status: DONE (no blocking concern; four minor notes at the end)

Worktree `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b`, branch `feat/account-b1b`, started at `a4cabd40`.

## Commit

`4732956b feat(libraryfake): sign JWT access tokens with the dev key, and fail, block and tell time on request`

Exactly five files, 573 insertions, 20 deletions: `fake.go` (M), `oauth.go` (M, replaced whole), `control.go` (new), `jwt.go` (new), `fake_test.go` (new). `git status --short` listed only these five before the commit and lists nothing after it.

## What I implemented

The brief's text, byte for byte (proof below):

- `fake_test.go`: six tests (JWT only when the audience is sent and the audience sticks to its chain; clock dates tokens and Block revokes; replaying a rotated refresh token revokes the account; a lost answer leaves the refresh token spent; OnToken runs and the fake still answers; the emailed code in each of its shapes).
- `fake.go`: the six small edits (grant gets `resource` and `spent`; Server gets `Replays` and the control fields; `blocked` map in `New`; `s.now()` in `ExpireAccessTokens` and `caller`; `/revoke` marks a refresh token spent).
- `oauth.go`: authorize (resource check, blocked user gets `access_denied`), `issue` (signs a JWT when the grant has a resource and the fake is not opaque), `token` (hook, log of requests with the Authorization header read as "redacted", resource handling, replay counting, RefreshLost), `verifyEmail` (signed / to trade / opaque shapes).
- `control.go`: `RefreshMode` and its eight values, `SetClock`, `OnToken`, `SetRefreshMode`, `SetOpaqueTokens`, `SetEmailOpaque`, `SetEmailTrade`, `Block`, `Unblock`, `TokenRequest`, `TokenRequests`, `LastResource`, `NewGrant`, `refuse`, `hangUp`.
- `jwt.go`: `TrustKey` and the EdDSA signer (typ `at+jwt`, kid `accounttest.DevKID`, `aud` an array holding `account.Audience`, `azp` and `client_id` both `account.ClientID`).

Line counts: `fake.go` 420, `oauth.go` 191, `control.go` 186, `jwt.go` 44, `fake_test.go` 223. All under 500.

## Verification that the text is the brief's text

`python3 -I` script (kept in the scratchpad, not in the repo) extracted the 17 go blocks of `task-3-brief.md`, applied the six replacement pairs to `git show HEAD:internal/library/libraryfake/fake.go` and byte-compared:

```
go blocks in the brief: 17
IDENTICAL fake_test.go
IDENTICAL oauth.go
IDENTICAL control.go
IDENTICAL jwt.go
replacement 1..6: old text occurs 1 time(s) in the committed fake.go   (each of the six)
IDENTICAL fake.go (HEAD + the six replacements)
RESULT: ALL VERBATIM
```

`gofmt -l` named nothing, so no whitespace fix was needed: there is no hand edit and no `gofmt -w` anywhere in this commit.

## TDD evidence

Baselines before any change (unmodified fake):
- `go test ./internal/library/ -count=1 -race` -> `ok  github.com/monoes/mono-agent/internal/library  1.752s`
- the 12 `cmd/monoagentcli` library tests (command below) -> `ok ... 13.127s`

RED (only `fake_test.go` written, nothing else changed):

```
go test ./internal/library/libraryfake/ -count=1
# github.com/monoes/mono-agent/internal/library/libraryfake_test [github.com/monoes/mono-agent/internal/library/libraryfake.test]
internal/library/libraryfake/fake_test.go:46:14: undefined: libraryfake.TrustKey
internal/library/libraryfake/fake_test.go:47:16: fake.NewGrant undefined (type *libraryfake.Server has no field or method NewGrant)
internal/library/libraryfake/fake_test.go:52:111: fake.LastResource undefined (type *libraryfake.Server has no field or method LastResource)
internal/library/libraryfake/fake_test.go:57:85: fake.LastResource undefined (type *libraryfake.Server has no field or method LastResource)
internal/library/libraryfake/fake_test.go:61:19: fake.NewGrant undefined (type *libraryfake.Server has no field or method NewGrant)
internal/library/libraryfake/fake_test.go:78:14: undefined: libraryfake.TrustKey
internal/library/libraryfake/fake_test.go:80:7: fake.SetClock undefined (type *libraryfake.Server has no field or method SetClock)
internal/library/libraryfake/fake_test.go:81:16: fake.NewGrant undefined (type *libraryfake.Server has no field or method NewGrant)
internal/library/libraryfake/fake_test.go:88:7: fake.Block undefined (type *libraryfake.Server has no field or method Block)
internal/library/libraryfake/fake_test.go:89:162: fake.Replays undefined (type *libraryfake.Server has no field or method Replays)
internal/library/libraryfake/fake_test.go:89:162: too many errors
FAIL	github.com/monoes/mono-agent/internal/library/libraryfake [build failed]
FAIL
```

GREEN (Step 5, the brief's command, on the final committed state):

```
go test ./internal/library/libraryfake/ ./internal/library/ -count=1 -race
ok  	github.com/monoes/mono-agent/internal/library/libraryfake	1.279s
ok  	github.com/monoes/mono-agent/internal/library	1.695s
```

The six new tests, `-race -v`: `TestAccessTokensAreJWTsOnlyWhenTheAudienceIsSent`, `TestClockDatesTokensAndBlockRevokesThem`, `TestReplayingARotatedRefreshTokenRevokesTheAccount`, `TestALostAnswerLeavesTheRefreshTokenSpent`, `TestOnTokenRunsWhenATokenRequestArrivesAndTheFakeStillAnswers`, `TestEmailVerifyAnswersLikeTheTokenEndpointWhenTheAudienceIsAsked`: all `--- PASS` (each under 0.1 s, no sleeps in them).

Step 6: `go vet ./internal/library/... && gofmt -l internal/library` -> no output.

## Checks beyond the brief

1. Other consumers of the changed fake. `cmd/monoagentcli/library_test.go` and `library_roundtrip_test.go` import the fake and drive exactly the routes this task changes (email verify, revoke, refresh). I re-ran the 12 tests by exact name (no loose pattern, so `TestAutomationDoctorJSON` cannot match) before and after:
   `go test ./cmd/monoagentcli/ -count=1 -run '^(TestLibraryLoginStatusLogout|TestLibraryEmailLogin|TestLibraryInstallAutomationTrust|TestLibraryInstallRejectsBadSHA256|TestLibraryErrorsExitCodes|TestLibraryReadsNeedALogin|TestLibraryRead401RefreshesThenAsksForLogin|TestLibraryWorkflowInstallAndUpdate|TestLibraryOrgInstallCollision|TestLibraryPublishRoundTrips|TestLibraryAdoptsSeededBuiltins|TestLibraryForkIsNotAdoptedOrSilentlyReplaced)$' -v`
   After the change: all 12 `--- PASS`, `ok  github.com/monoes/mono-agent/cmd/monoagentcli  25.507s`. So "without a resource it behaves as before" holds for every consumer I found; there is no plan-level contradiction.
2. No import cycle: `go list -deps ./internal/library/libraryfake` shows `secrets`, `account`, `accounttest` and itself, nothing else of this repo. Only three test files import the fake (`internal/library/library_test.go`, the two `cmd/monoagentcli` files), so `testing` (imported by `jwt.go` for `TrustKey`) never reaches the binary. No `func init()` in `accounttest` or `secrets` non-test files; `internal/testhome` is not a dependency of the fake.
3. Flake check, small on purpose: `go test ./internal/library/libraryfake/ -count=5 -race` -> `ok` (1.746s). The machine load average was about 32 during the session.
4. Scope kept: `internal/account` untouched, `keys_devaccount.go` untouched (its comment says B1b wires the dev key there; that is not this task, `TrustKey` is the test-side equivalent), no TestMain added to `libraryfake`.

## Deviations from the brief

- One: the brief's commit command has a subject and the trailer only. The lane rules require subject, a one-paragraph why, then the trailer, so I kept the brief's subject and the trailer byte for byte and put a why paragraph between them. `git add` and `git commit` ran as two commands.
- None in code, tests or commands. The brief's sample RED lines (`46:14`, `47:16`, `52:111`, `89:162 too many errors`) match the real output.

## Self-review

- Lock order is `mu` then `cmu`; `cmu` is a leaf (nothing takes `mu` while holding it, except a clock function a test supplies, which runs under `cmu`). `OnToken`'s hook runs before `mu` is taken, so a hook may call `Block`, `SetRefreshMode`, `SetClock` and the others without deadlock (the brief says "with no lock held"; the code matches).
- A panic inside the token handler releases `mu` (deferred unlock), so a recovered handler panic cannot wedge the fake (checked, see note 1).
- The lost-answer test's synchronization (`SetRefreshMode` takes `mu`, so the lost request's writes are visible) is sound under `-race`; five repeated `-race` runs were clean.
- Existing behavior that changed for callers that never send a resource (all existing tests still pass): `claim/verify` without a resource now answers an opaque access token beside a refresh token and adds `offline_access` to the scopes (it answered an access token only); a rotated or `/revoke`d refresh token is now marked spent, so presenting it again counts a replay and revokes every token of that user (before, a plain `invalid_grant`).

## Concerns (all minor, nothing blocks Task 4)

1. `NewGrant` of an unknown username builds a grant with a nil user. I ran it (throwaway test, deleted, never committed): the first audience refresh of that chain panics at `jwt.go:40` (`g.user.ID`), net/http recovers it, the client sees `EOF`, a stack trace goes to stderr, and the fake's lock is not left held. The replay branch reads `old.user.ID` the same way. A test-authoring error only (the fake knows `monoes`, `ada` and whatever a test adds), but the symptom is confusing. Not changed: the brief's code is verbatim.
2. Four verbatim `Fatalf` messages print a whole response `body` (`fake_test.go:69`, `:90`, `:207`, `:210`). They fire only when the fake wrongly answers a refusal with a 200, and then `body` holds fake tokens, which brushes against the lane rule on printing tokens. The tokens are throwaway (`at-N`, `rt-N`, or a JWT signed by the public development key for a fake user). Test left as the brief gives it.
3. Read from the code, not run: rotating a refresh token also makes the fake reject the previous access token, because one grant object holds both and `caller()` checks `g.revoked`. That is the behavior from before this task, unchanged. A real monoes.me JWT would stay valid until `exp`, so a later test that calls the fake's `/api/library/*` with a pre-rotation JWT would get 401 here where monoes.me would answer 200.
4. `Server.Replays` and `Refreshes` stay plain exported ints read without a lock, as `Refreshes` always was. Safe in the tests because the HTTP round trip orders the access; a test that reads them while a request is in flight must take another fake call first (the lost-answer test shows how).

## Contract change requests

None.

## Files

- `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b/internal/library/libraryfake/fake.go`
- `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b/internal/library/libraryfake/oauth.go`
- `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b/internal/library/libraryfake/control.go`
- `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b/internal/library/libraryfake/jwt.go`
- `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b1b/internal/library/libraryfake/fake_test.go`
