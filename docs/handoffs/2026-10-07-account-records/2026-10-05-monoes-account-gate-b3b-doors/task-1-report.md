> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# B3b Task 1 report: the words every door says, and the test table

Status: DONE_WITH_CONCERNS (two questions for the controller, section 6; no defect in the code).

Worktree `$MONOAGENT_CHECKOUT/.claude/worktrees/feat+account-b3b`, branch `feat/account-b3b`, base HEAD `02592963`.
Commit: `a295037d` (`a295037db103542e3e718d98b4212e221a24cc0d`) `feat(account): the words every door says to a locked caller`.

## 1. What I implemented

The three files of the brief, with the code of the brief (it is identical to Task 1 of the authoritative plan `docs/mastermind/plans/2026-10-05-monoes-account-gate-b3b-doors.md` in the docs worktree: `diff` of the extracted task text against the brief printed nothing).

| File | Lines | What it holds |
|---|---|---|
| `internal/accountdoor/accountdoor.go` | 71 | `Message` (= `account.LoginRequiredMessage`), `Code` (`"login_required"`), `Summary` and `SummaryOf` (state, reason, `valid_until` omitted when zero, enforced; never the user), `WriteUnauthorized` (401, `Content-Type: application/json`, no `WWW-Authenticate`, body `{"error":"login_required","login_required":true,"account":{"state","reason"}}`; the account is the verdict a `*account.LoginRequiredError` carries, any other error is described by `account.CurrentStatus()`) |
| `internal/accountdoor/accountdoor_test.go` | 88 | `TestWriteUnauthorizedBody`, `TestWriteUnauthorizedDescribesAnyOtherErrorByTheCurrentVerdict`, `TestNoBodyNamesTheUser`, `TestRequireInEveryState` (5 subtests) |
| `internal/accountdoor/doortest/doortest.go` | 30 | `Row` and `Modes`: signed in, grace, dormant (allowed; dormant is not enforced), locked/no login, locked/refused (refused) |

Byte-compare of each file with the n-th ```go block of the brief (`scratchpad/verbatim-check.sh`), run before and after the mutation checks:

```
IDENTICAL  block 1  ->  internal/accountdoor/doortest/doortest.go
IDENTICAL  block 2  ->  internal/accountdoor/accountdoor_test.go
IDENTICAL  block 3  ->  internal/accountdoor/accountdoor.go
```

I checked the brief against the real code before writing: every symbol it consumes exists with the shape it says (`account.Status`, `State`, `Reason`, `User`, `LoginRequiredError{Status}`, `Require`, `CurrentStatus`, `IsLoginRequired`, `LoginRequiredMessage`, `accounttest.Install` and the five modes), and `omitzero` is fine (`go.mod` says go 1.26.0; `account.Status` already uses it). Nothing contradicted the brief, so there was no reason to ask.

## 2. TDD evidence

Order of the brief: table (Step 1), test (Step 2), RED (Step 3), implementation (Step 4), GREEN (Step 5), commit (Step 6).

RED (Step 3, before `accountdoor.go` existed), the brief's exact command:

```
$ go test ./internal/accountdoor/... -count=1 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'
internal/accountdoor/accountdoor_test.go:19:5: undefined: Message
internal/accountdoor/accountdoor_test.go:19:75: undefined: Code
internal/accountdoor/accountdoor_test.go:20:39: undefined: Message
internal/accountdoor/accountdoor_test.go:20:48: undefined: Code
internal/accountdoor/accountdoor_test.go:23:2: undefined: WriteUnauthorized
internal/accountdoor/accountdoor_test.go:43:2: undefined: WriteUnauthorized
internal/accountdoor/accountdoor_test.go:60:2: undefined: WriteUnauthorized
internal/accountdoor/accountdoor_test.go:61:29: undefined: SummaryOf
internal/accountdoor/accountdoor_test.go:69:26: undefined: SummaryOf
FAIL	github.com/monoes/mono-agent/internal/accountdoor [build failed]
FAIL
```

This is the failure the brief expects (`[build failed]`, `undefined: Message`, `Code`, `WriteUnauthorized`, `SummaryOf`).

GREEN (Step 5), the brief's exact command:

```
$ go test ./internal/accountdoor/... -count=1 -race 2>&1 | grep -E '^(ok|FAIL|--- FAIL|    --- FAIL|internal/|cmd/)'
ok  	github.com/monoes/mono-agent/internal/accountdoor	1.639s
```

Verbose run of the same (`-count=1 -race -v`, filtered to results):

```
--- PASS: TestWriteUnauthorizedBody (0.00s)
--- PASS: TestWriteUnauthorizedDescribesAnyOtherErrorByTheCurrentVerdict (0.11s)
--- PASS: TestNoBodyNamesTheUser (0.00s)
--- PASS: TestRequireInEveryState (0.19s)
    --- PASS: TestRequireInEveryState/signed_in (0.13s)
    --- PASS: TestRequireInEveryState/grace (0.04s)
    --- PASS: TestRequireInEveryState/dormant (0.00s)
    --- PASS: TestRequireInEveryState/locked,_no_login (0.00s)
    --- PASS: TestRequireInEveryState/locked,_refused (0.02s)
PASS
ok  	github.com/monoes/mono-agent/internal/accountdoor	1.640s
?   	github.com/monoes/mono-agent/internal/accountdoor/doortest	[no test files]
```

After the commit, the same GREEN command on the committed tree: `ok  	github.com/monoes/mono-agent/internal/accountdoor	1.786s`.
Also clean: `gofmt -l internal/accountdoor` (no output), `go vet ./internal/accountdoor/...`, `go build ./internal/accountdoor/...`. All three files are far under 500 lines. Nothing outside `internal/accountdoor` was touched; I did not run the repository-wide suite (lane rules).

## 3. Mutation checks (beyond the brief, scratch only, nothing left behind)

`scratchpad/mutation-check.sh` applies one mutation to a non-test file, runs `go test ./internal/accountdoor/ -count=1`, and restores the file from a saved copy. 17 mutations: 17 killed, 0 survived, 0 no-ops. Afterwards both files were `cmp`-identical to their saved copies and again byte-identical to the brief.

| Mutation | Killed by |
|---|---|
| M1 `WWW-Authenticate: Bearer` is set | TestWriteUnauthorizedBody |
| M2 the carried verdict is ignored (always the current one) | TestWriteUnauthorizedBody |
| M3 no fallback for a non-verdict error | TestWriteUnauthorizedDescribesAnyOtherErrorByTheCurrentVerdict (nil dereference panic) |
| M4 403 instead of 401 | TestWriteUnauthorizedBody, ...DescribesAnyOtherError... |
| M5 `valid_until` loses `omitzero` | TestNoBodyNamesTheUser |
| M6 `Summary` carries the user | TestNoBodyNamesTheUser |
| M6b the 401 body carries the user | TestNoBodyNamesTheUser, ...DescribesAnyOtherError... |
| M7 `Message` is another sentence | TestWriteUnauthorizedBody |
| M8 `Code` is another word | TestWriteUnauthorizedBody, ...DescribesAnyOtherError... |
| M9 `login_required` flag false | TestWriteUnauthorizedBody, ...DescribesAnyOtherError... |
| M10 `Content-Type: text/plain` | TestWriteUnauthorizedBody |
| M11 `reason` dropped from the body | TestWriteUnauthorizedBody, ...DescribesAnyOtherError... |
| T1 dormant row says refused | TestRequireInEveryState/dormant |
| T2 grace row says refused | TestRequireInEveryState/grace |
| T3 locked/refused row says allowed | TestRequireInEveryState/locked,_refused |
| T4 signed-in row says refused | TestRequireInEveryState/signed_in |
| T5 locked/no login row says allowed | TestRequireInEveryState/locked,_no_login |

## 4. The table against the real verdicts (scratch test, deleted at once; `git status` afterwards showed only `internal/accountdoor/`)

Task 1's own tests use only the table's `Refused` column. Later tasks compare door output with `State`, `Reason` and `Enforced`, so I checked those columns against `account.CurrentStatus()` for each fixture. All five rows agree. What each fixture really says:

| Row | state / reason | enforced | `SummaryOf(CurrentStatus())` |
|---|---|---|---|
| signed in | ok / "" | true | `{"state":"ok","reason":"","valid_until":"2026-10-05T13:00:00Z","enforced":true}` |
| grace | grace / unreachable | true | `{"state":"grace","reason":"unreachable","valid_until":"2026-10-05T11:00:00Z","enforced":true}` |
| dormant | locked / not_logged_in | false | `{"state":"locked","reason":"not_logged_in","enforced":false}` |
| locked, no login | locked / not_logged_in | true | `{"state":"locked","reason":"not_logged_in","enforced":true}` |
| locked, refused | locked / refused | true | `{"state":"locked","reason":"refused","enforced":true}` |

`WriteUnauthorized(rec, account.Require(ctx))` for the two refused rows:

```
401 {"error":"login_required","login_required":true,"account":{"state":"locked","reason":"not_logged_in"}}
401 {"error":"login_required","login_required":true,"account":{"state":"locked","reason":"refused"}}
```

## 5. HOME isolation (the brief adds no TestMain)

I built the race-enabled test binary once (`go test -c -race`) and ran it from an empty working directory with `HOME`, `USERPROFILE` and `TMPDIR` all pointing inside an empty sandbox: `PASS`, and afterwards the sandbox held three empty directories and zero files. So the tests wrote nothing under HOME, TMPDIR or the working directory. This is NOT evidence about the macOS keychain, which does not follow `$HOME`; see concern 1. (Binary and sandbox deleted afterwards.)

## 6. Concerns and questions for the controller

1. **No `TestMain` in `internal/accountdoor` (a ruling needed, not a defect).** `internal/account` and `internal/account/accounttest` both have a `TestMain` with `testhome.Main` and `keyring.MockInit()`, and say why: a fixture that regressed to the default store would overwrite the developer's real session or keychain. The brief's file list has no `main_test.go` for this package, and the lane rule says `git status --short` lists only the files the task names, so I did not add a fourth file. What is true today: `accounttest.Install` builds the store as `account.OpenStore(t.TempDir(), account.NewMemorySealer())` with a nil Refresher (`accounttest/install.go:50`), and, in `internal/account`, only `sealer.go` imports the keyring (the keyring sealers) and the default directory is used only by a default store (`DefaultDir()` at `store.go:186`; `guard.go` and `doc.go` just mention it in comments). These tests never build a default store or a keyring sealer. So keychain isolation, and any regression guard, rest on the memory sealer alone; there is no defense in depth in this package, and every later door package that walks `doortest` will inherit the question. If you want it, a `main_test.go` (`keyring.MockInit()` plus `testhome.Main`, as in `accounttest`) is a one-file addition to this task or to the first door task.
2. **A stale `index.lock` in this worktree's git directory, which I removed.** `git add` failed with `Unable to create '$MONOAGENT_CHECKOUT/.git/worktrees/feat+account-b3b/index.lock': File exists`. The file was zero bytes, mtime 08:07:26 (about 12 minutes old at 08:19), `lsof` showed no process holding it, and the only live git process anywhere was `git worktree remove .../agent-a8727e544ca24ff4c` (another lane). I removed it with a plain `rm` and `git add` then succeeded. I do not know what left it; if another agent or a hook runs git in this worktree, it may recur and may have been killed mid-command at 08:07.
3. **Typed-nil (minor, unreachable today).** `WriteUnauthorized` dereferences the `*account.LoginRequiredError` that `errors.As` returns, so a typed-nil `*LoginRequiredError` stored in an `error` would panic. `account.Require` cannot produce one (the package builds the error at exactly two sites, `guard.go:155` and `process.go:73`, both `&LoginRequiredError{Status: st}`, and otherwise returns a literal nil), and net/http recovers a handler panic per request, so the request would still be refused, not let through. I left the brief's code as it is.
4. **Notes for the later door tasks, from section 4.** (a) The dormant row's `State` and `Reason` are empty on purpose but the real verdict is `locked/not_logged_in` with `enforced=false`: a door test that compares `State` or `Reason` must skip rows whose `State` is empty. (b) A `not_logged_in` refusal has no second line, so its `Error()` equals `Message`.
5. **`doortest` is a non-test package that imports `accounttest`** (which imports `testing`); nothing enforces "imported only from `_test.go` files", and `go build ./...` compiles it (it did, here). That belongs to a later hygiene check, not to this task.
6. **Commit message form.** I used the lane rules' three-part form (subject, one "why" paragraph, trailer); the brief's Step 6 shows only subject and trailer. The subject is the brief's.
7. **A stray write of mine, already undone.** A placeholder file was written once to a wrong path under `$LOCAL_CLAUDE_SCRATCH/-Users-morteza/` (not the scratchpad). I removed that one file and the one empty directory I had created with it (plain `rm` and `rmdir`); another session's directory next to it was not touched.

Contract change requests: none.

Scratch scripts (not committed), all under `$LOCAL_CLAUDE_SCRATCH/-Users-morteza-Desktop-monoes-mono-agent/ab93060e-329b-4819-88ef-4bab24fc04a4/scratchpad/`: `verbatim-check.sh`, `run-scratch-table.sh` with `zz_scratch_table_test.go.txt`, `mutation-check.sh`, `home-isolation-check.sh`.
