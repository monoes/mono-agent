> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Pre-flight scan: B3b doors (2026-10-07)

Plan: `.claude/worktrees/feat+monoes-account-gate/docs/mastermind/plans/2026-10-05-monoes-account-gate-b3b-doors.md` (2098 lines, 9 tasks). "plan :N" below is a line of that file.
Base: `feat/account-core` @ dbe36f97 (master f4441a2a + B1a). B1a touched no file this plan edits (`git diff --name-only f4441a2a..dbe36f97` outside `internal/account`: docs and `internal/secrets/account_kek*`, `keyring_passfile.go` only).

How it was checked. Read the plan, index §2-§4, spec §6.3, §6.4, D7, D22, D27 and §13, ledger R1-R7, and every cited line of the code. Then the plan was applied mechanically to a scratch export (`git archive dbe36f97`), with the Edit tool's rule (old text = context + `-` lines, found exactly once): 44 blocks (11 created files, 33 diff edits) plus Task 2's prose Edit 5, 0 misses. In that tree:
- `gofmt -l .` prints nothing; `go vet ./...` is clean (`./internal/secrets/` included: no import cycle, index §3.6/A15; the door packages are outside `go list -deps -test ./internal/secrets/`); `go build ./...`, `-tags nosocial` and `-tags devaccount` build.
- RED: each task's test files alone, over the earlier tasks, fail exactly as the step says (one wording exception, F8).
- GREEN: all 26 new tests pass, with `-race` where the plan asks; the seven packages pass in full (accountdoor, httpapi 6 s, orgbridge 15 s, openaiapi 25 s, workflow 15 s, mcp 39 s, extension 30 s); `TestDaemonRoutesKeepTheOrgReceiverAndMountV1OnlyOnLoopback` and `TestStartV1OffLoopbackServesHTTPSWithTheGeneratedCertificate` pass.
- Mutations: all 17 of the plan's checks are caught (http-gate only by its second test, F5).
- OpenAPI: `@redocly/cli` 2.49.0 from the local npx cache (nothing downloaded): "Your API description is valid", the one `operation-4xx-response` warning on `/health`, exit 0, before and after the edits.

## 1. Task table

| Task | Result |
|---|---|
| 1 accountdoor, doortest | agrees. Step 3's expected list names `undefined: Refusal` (plan :199): no test references `Refusal`, so it never appears (F8) |
| 2 HTTP API door | agrees (8 edits apply; RED `s.handler undefined`, `undefined: accountGate`). Test-quality notes F5 (plan :433-434), F6 (plan :412) |
| 3 OpenAPI | agrees (lint as expected before and after). Edit 1's sentence overstates (plan :665-669, F10) |
| 4 org receiver | agrees (RED: the two locked subtests get 202) |
| 5 `/v1` door | agrees (RED: all four fail). The stream row is a 401 because the stream commits only after `StreamCommitAfter`, 5 s by default (`internal/openaiapi/config.go:27`, `:176-177`) |
| 6 webhook server | agrees on its own branch; collides with B3a at merge (F1). "469 after the edits" (plan :1165) is 468 |
| 7 extension bridge | agrees (RED `undefined: CodeAccountLocked`; the 6 tests pass under `-race`). Open list misses `/monoagent/resolve` (F2) |
| 8 MCP door | agrees (RED: locked subtests fail). Dead branch in the test (plan :1931-1932, F7) |
| 9 verify | agrees: every command reproduced, nothing failed (the index §4 flake list was not needed) |

## 2. Pair table

| Tasks | Produced -> consumed | Result |
|---|---|---|
| 1 -> 2 | `WriteUnauthorized(w, err)` (plan :271), `SummaryOf` (:241), `doortest.Row{Name,Mode,Refused,State,Reason,Enforced}` (:93-104) -> plan :331-342, :617, every mode loop | agrees |
| 1 -> 4 | `WriteUnauthorized`, `doortest.Modes` -> plan :855, :780 | agrees |
| 1 -> 5 | `accountdoor.Code`, `Message` -> `errLoginRequired()` (plan :1125) | agrees |
| 1 -> 6 | `accountdoor.Code` -> `writeJSONError(w, 503, accountdoor.Code)` (plan :1305) | agrees |
| 1 -> 7 | `Message`, `Summary`, `SummaryOf` -> account_door.go (plan :1724, :1732-1734, :1743) and ping (:1761) | agrees |
| 1 -> 8 | `Message` -> tool error text (plan :2007) | agrees |
| 2 <-> 3 | 401 flat body, `/health` `account` -> `Error{login_required, account}`, `AccountState` (plan :716-728) | agrees (wording F10) |
| 2 <-> 4 | gate covers `/org-endpoint/{id}` behind httpapi; receiver checks a direct mount; one body from `WriteUnauthorized` | agrees |
| 2 <-> 5 | `openWhileLocked` stands aside for `/v1`, `/v1/...` (plan :604); `Handler()` returns the gated handler (:561); mutation http-v1 (:1157) edits T2's exact text `strings.HasPrefix(p, "/v1/")` | agrees (caught) |
| 3 <-> 4, 5 | `/org-endpoint` 401 text (plan :685-688), `/v1` 401 row (:696) describe T4 and T5 | agrees |
| 6 <-> 9, 7 <-> 9 | Task 9 step 3 `-race` names exactly T6's 2 and T7's 6 tests (plan :2048) | agrees |
| 6 <-> B3a (cross-plan) | both declare `lockedLogEvery` in package `workflow`: B3b plan :1292 (`internal/workflow/webhook_account.go`), B3a plan :524 (`internal/workflow/account_gate.go`) | DISAGREES (F1) |
| 7 -> B4b (cross-plan) | refusal frame without `ok`, `code: account_locked`; ping `data.account{state,reason,valid_until,enforced}` -> b4b plan :59, :93-96 | agrees |
| 2, 4, 5, 6, 7 -> B5c (cross-plan) | the smoke's door table, b5c plan :1852-1858, :1896-1912 | agrees |
| 7 -> B3a (cross-plan) | pushes accepted on the premise that whatever runs on them is gated where it runs (plan :1389, :1720, :2088) | DISAGREES with the code (F3) |

## 3. Contract drift (internal/account, accounttest, secrets)

| Identifier as the plan uses it | Real code at dbe36f97 | Result |
|---|---|---|
| `account.LoginRequiredMessage` (plan :224, literal pinned :138) | `internal/account/errors.go:10`, same sentence | match |
| `account.LoginRequiredError{Status}` (plan :142, :250, :1054) | `errors.go:14` | match |
| `account.IsLoginRequired(err) bool` (plan :188, :1135) | `errors.go:38` (errors.As) | match |
| `account.Require(ctx) error` (plan :187, :571, :589, :854, :1100, :1300, :1728, :2005) | `process.go:57`; returns nil or `*LoginRequiredError` only (`guard.go:153-158`, `process.go:68-76`); no guard in a test binary: nil unless strict | match |
| `account.CurrentStatus() Status` (plan :253, :617, :1733) | `process.go:82` | match |
| `account.Status` fields `State, Reason, User, ValidUntil, Enforced` (plan :159-162, :242) | `state.go:40-51` | match |
| `account.State`, `StateLocked`, `StateGrace` (plan :142, :160, :234) | `state.go:6`, `:11`, `:10` | match |
| `account.Reason`, `ReasonRefused`, `ReasonUnreachable`, `ReasonNotLoggedIn` (plan :142, :160, :174) | `state.go:15`, `:21`, `:26`, `:19` | match |
| `account.User{ID, Email, Username}` (plan :161) | `state.go:33-37` | match |
| `accounttest.Install(t, Mode) *account.Guard` (plan :68, every door test) | `accounttest/install.go:35`; strict guard, date a day before its fixed clock (`fixture.go:48`, `clock.go:18`) | match |
| modes `SignedIn, InGrace, LockedNoLogin, LockedRefused, Dormant` and their verdicts (plan :110-114) | `install.go:14-18`; ok, grace(unreachable), locked(not_logged_in), locked(refused); Dormant saves no session (`install.go:69-70`), so locked(not_logged_in) with `enforced:false`, which doortest leaves unchecked (plan :112) | match |
| "at most one fixture per test" | `install.go:25-27`; plan :412 calls Install twice in one test (F6) | behaviour note |
| `secrets.List(ctx, db, profile)`, `Entry.Name` (plan :464-469) | `internal/secrets/secrets.go:237`, `:19` | match |

No mismatch of name, signature, return or panic behaviour. Other packages consumed (httpapi `NewServer`/`Options`/`Handler`/`Close`, orggrant `NewStore`/`CreateEndpoint`/`NewEndpointID`/`Tool`, workflow, capture `List`/`ArtifactReadable`, monomind `ExecOptions`/`Event`/`TurnResult`, go-keyring `MockInit`, gorilla/websocket, zerolog) compile and run in the scratch tree.

## 4. Real-code references (all checked)

| Plan cites | Result |
|---|---|
| plan :53-54: `cmd/monoagentcli/{daemon,httpapi,mcp,extension_serve}.go`; `internal/apiconfig/probe.go:63`; `cmd/monoagentcli/doctor_env_services.go:49` | exist; probe.go:63 and doctor :49-52 read the status only. Note: probe.go:66 also expects `GET /v1/models` without a key to answer 401, which T5's refusal keeps |
| T2: server.go 3-14, 27-32, 48, 74-77, 94, 155-157, 172, 201; server_test.go 37, 66, 75; token.go 20 | all exact |
| T3: openapi.yaml 18-19, 44, 301-303, 538, 736-737, 759-762; ci.yml job `openapi-lint` (:101, :110); `mcp_command_test.go` pins only the `mcp` paragraph of `ref api` (:113, :216) | all exact; no Go test reads openapi.yaml |
| T4: receiver.go 19, 107-108 (Register :98); orgbridge_test.go 18; receiver_test.go 19; `CreateEndpoint` (orggrant/endpoints.go:86), `NewEndpointID` (ids.go:32) | all exact |
| T5: auth.go 3-11 (block ends :12), 46-54; errors.go 7-17, 78, 142; helpers_test.go 39; http_helpers_test.go 20; chat_test.go 22; errors_test.go 15; serve_test.go 22, 43; serve.go `Handler` :15 | all exact (`stop` returns an error, see F12) |
| T6: webhook_server.go 18, 72, 271-275; `NewWebhookServer` :137, `Register` :237, `writeJSONError` :453 | all exact; 458 -> 468 lines |
| T7: request.go 84-90, 122, 246-250, 284-286 (488 -> 495 lines); server.go 710-714; cdp.go 13, 318-323, 375-382; capture.go:376 (ext-push text, unique); capture_test.go 34, 140, 165, 173; request_test.go 25, 53; cdp_test.go 54, 96, 114, 124, 145; recording_test.go 22, 38; token.go 100; remote.go 119, 140; `chrome-extension/capture_bridge.js:111-117` | all exact |
| T8: mcp/server.go 19, 374 (dispatch :339-371 has only initialize, ping, tools/list, tools/call); server_test.go 19, 46, 73, 94, 104, 108; grant_test.go 28; grant.go 33 | all exact |
| T9: api_gateway_test.go:420, :448; index §4 failure list | exist, pass |
| Inventory (plan :2065-2089): api_gateway.go:439; httpapi/server.go:123-139 (12 routes), :123-143 order; receiver.go:98; register.go:9-12; mcp/server.go:374; library/auth.go:209; connections/oauth.go:118 | all exact; the list is incomplete (F2, §7) |

## 5. Plan-mandated defects

- F5 `TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused` (plan :427-450) repeats NewServer's wiring (:433-434 = Edit 3's two lines), so it pins `accountGate`, not NewServer's use of it: with the http-gate mutation it still passes (scratch run). Step 6 still fails as required only because it also runs `TestLockedServerRefusesEveryPathButHealth`. Build the server through `NewServer(Options{..., ExtraRoutes})` instead.
- F6 plan :412 installs a fixture twice in one test (loop over `mutations`), against `install.go:25-27`; harmless for LockedNoLogin (no token to verify). Use two subtests.
- F7 plan :1931-1932: `toolText` (`internal/mcp/server_test.go:108-121`) calls `t.Fatalf` on a response without `result` before the `isProtocolError` branch can report it, so that branch is dead. Check `resps[id]["error"]` first.
- F8 `Refusal` (plan :245-254) is exported with no caller outside the package, and its fallback to `CurrentStatus()` for an error that is not a `*LoginRequiredError` is unreachable from every door (they pass `account.Require`'s error) and untested.
- F11 `noteLocked` (plan :1312-1321) throttles on `time.Now().UnixNano()`: after the clock is set back the "webhook refused" line stays silent until the clock passes the stored stamp plus a minute.
- F12 Copied from existing tests, still worth a line each: plan :821 `_ = db.QueryRow(...).Scan(&n)` makes the no-execution check vacuous if the query fails (as `receiver_test.go:66-67`); plan :998 `defer stop()` drops Serve's error; Task 4's allowed rows leave the Mux tail goroutine (`<-ctx.Done()`, plan :793) running, as `receiver_test.go:42` does.
- No verbatim logic duplication in production code, no file left behind (every server, socket and temp dir has a cleanup).

## 6. Constraint check (index §2, rulings R1-R7)

- Dormant (D22): no door refuses; the dormant row of every door test pins it; the only visible change is the `account` object of `/health` and `ping`. Holds.
- Nothing on disk until a write: the doors call only `Require` and `CurrentStatus` (a stat and a read at most once per poll, no write). Holds.
- Process globals: no new test calls `t.Parallel()`; Install is used only in sequential tests or subtests. Holds (F6 aside).
- Never print a token or key: no test prints one (the httpapi bearer is printed only when it is "wrong" or empty, plan :360). Holds.
- Files under 500 lines: new files are 30 to 286 lines; `internal/orgbridge/receiver.go` grows 515 -> 524 and `internal/extension/server.go` 805 -> 810 (both over before the plan) (F9).
- Commits: conventional subjects, `git add` and `git commit` as two commands, mutation restores with `git checkout --`, no stash. Holds.
- Only B5b edits README, AGENTS, ... : the plan edits none; `openapi.yaml` is not on that list. Holds.
- Import rule (index §3.6, A15): verified with `go list -deps -test ./internal/secrets/` and `go vet ./...`. Holds.
- Index §3.4 items 5-8 (wire shapes): match, and B4b and B5c read the same shapes.
- R1, R2, R3, R5, R6, R7: no bearing on the doors.
- R4: no conflict. The Architecture line (plan :7) "work in flight finishes (spec §6.4)" predates R4, which amends §6.4 to "while the account is ok or grace" (engine runs now end at their next node); the doors' own claim, no stream is ever cut, still holds (F13).
- Stale copies: `feat/account-core`, the base of the B3b worktree, carries older copies of this plan, the index and the spec. Its copy of this plan differs in 12 lines (§2 constraints before A24/A25; Task 3 Edit 6's reason list without `unconfirmed`) (F4).

## 7. Door inventory (non-test Go at dbe36f97: `net.Listen`, `ListenAndServe`, `http.Server{`, `HandleFunc`/`Handle(`, `Upgrade(`/`Upgrader`, `tls.Listen`, Unix/packet listeners, grpc, net/rpc, native messaging, stdin JSON-RPC loops)

| Entry | Code | Covered by the plan |
|---|---|---|
| HTTP API mux: `/health` + 12 routes | httpapi/server.go:123-139; served via `Serve` (daemon.go:258, httpapi.go:115) | accountGate + auth (T2) |
| ExtraRoutes `POST /org-endpoint/{id}` | orgbridge/receiver.go:98 | gate + `Receiver.ServeHTTP` (T4) |
| ExtraRoutes `/v1` x4 on the main mux (loopback only, api_gateway.go:439) | openaiapi/register.go:9-12 | `Gateway.auth` (T5) |
| dedicated `/v1` listener: `/health` + `/v1` x4 | api_gateway.go:346, :368 -> serve.go:15-21 | `Gateway.auth`; `/health` open (T5) |
| webhook server | workflow/webhook_server.go:163 | `refuseWhileLocked` (T6) |
| bridge `/monoagent` socket: request, recording, binding, response/capture frames | extension/server.go:272, :443, readLoop :777-803 | requests refused; the rest accepted on purpose (T7) |
| bridge `/monoagent/relay` | server.go:274 | refused after the token (T7) |
| bridge `/monoagent/cdp` | server.go:276, cdp.go:331 | refused at connect and per command (T7) |
| bridge `/monoagent/health`, `/auth`, `/pair`, `/pair/exchange`, `/browsers` | server.go:273, 275, 277, 278, 280 | open, pinned by `TestTheOpenEndpointsOfALockedBridge` |
| **bridge `/monoagent/resolve`** | server.go:279 -> relay_routes.go:87 | **neither named nor pinned (F2).** Token-gated, read-only (which browser a profile resolves to); answers 200 to a locked bridge (scratch probe) |
| MCP stdio | mcp/server.go:151, :339-371 | `tools/call` refused (T8); no other method exists |
| library sign-in callback | library/auth.go:209-261 | open on purpose (the login itself) |
| connections OAuth callback | connections/oauth.go:85-124 | open on purpose |
| desktop asset handler `vaultImageHandler` | wails-app/main.go:113 | not named; served in-process by the Wails asset server, no listener; the desktop is B4's. Informational |

Not found: grpc, net/rpc, Unix sockets, named pipes, Chrome native messaging, other stdin protocols (the stdin readers are prompts of gated commands: secret_export.go:86, application_apply.go:43, workflow.go:1109, connections/manager.go:47).

Not a door, but the premise of the accepted pushes (F3): `installCaptureSummaries` (cmd/monoagentcli/extension_summary.go:89-93) runs three after-write hooks on every capture the bridge writes, pushed ones included: the summary (`monomind.Exec`, gated by B3a), the page-kind classifier (`capture_classify.go:92` -> `jevconf.NewClient` :177 -> `internal/jev/client.go:257`, `:303`: a plain `http.Client` call to TypeSafe Jev, made when the profile enabled the `capture` surface) and indexing (`captureindex` -> `monomind.IngestCapture`, captureindex.go:72 -> docsync.go:80 -> `runIngest`, docsync.go:117: `exec.CommandContext(bin, "mcp", "exec", "-t", "knowledge_ingest", ...)`, a monomind subprocess). Verified: neither goes through `monomind.Exec`, so they pass none of B3a's gates (`handleExecution`, `Exec`, `executeDef`, the per-node check), and neither the B3a plan nor spec A17 lists them. (Related, harmless: plan :2087 says the gateway's Jev picks go through `monomind.Exec`; they use the same Jev HTTP client (`internal/openaiapi/auto_jev.go:44`), but run only inside a request (`request.go:74`) that the `/v1` door already let in.)

## 8. Findings

- F1 BLOCKS (the integration, not the task): B3b plan :1292 (`internal/workflow/webhook_account.go`) and B3a plan :524 (`internal/workflow/account_gate.go`) both declare `lockedLogEvery` in package `workflow`. Each branch builds alone; the second to merge fails with "lockedLogEvery redeclared in this block". Fix before Task 6 is dispatched: rename B3b's to `webhookLockedLogEvery` (plan :1169, :1261, :1272, :1291-1292, :1315).
- F2 DRIFT (plan fix): `/monoagent/resolve` (extension/server.go:279) is missing from the open list (plan :1389, :1716-1717, :2080), from index §3.4 item 7, and from the map of `TestTheOpenEndpointsOfALockedBridge` (plan :1669-1672). Add `"/monoagent/resolve": http.StatusOK` and name it beside `/monoagent/browsers`.
- F3 DRIFT (needs a ruling): "whatever runs on pushed data is refused where it runs" (plan :1389, :1720, :2088; index §3.4 item 7) is false for the classifier and indexing hooks (§7). Owner: B3a (gate the after-write hook at extension_summary.go:89-93), or accept them as known edges in spec A17.
- F4 DRIFT (action for the lead): the B3b worktree's own `docs/mastermind/plans/...-b3b-doors.md`, index and spec (inherited from `feat/account-core`) are stale copies (§6). Sync them on `feat/account-core` before cutting the B3b worktree, or tell every implementer that the dispatched `feat+monoes-account-gate` path is the authority.
- F5-F8, F11, F12 COSMETIC (§5). F9 COSMETIC: two files already over 500 lines grow (§6). F10 COSMETIC: OpenAPI Edit 1 (plan :665-669) says every path but `GET /health` answers 401 while locked; `HEAD /health` is open too, a `/v1` path that is no route still gets the mux's 404/405 (the gate stands aside for `/v1/*`), and the dedicated listener answers 404 to unknown paths. F13 COSMETIC: plan :7 still cites §6.4's "work in flight finishes", which R4 amended (§6).
