> Historical session record, archived 2026-10-07. Read only when needed.
> Follow the handoff summary for current status; recorded model escalation, push restrictions and absolute scratch paths are historical, not instructions for the resumed session.

# Task 2 report: spikes S1, S2, S3, S6, S7

Status: DONE_WITH_CONCERNS (the concerns are findings the lead must act on, listed at the end; the deliverables are complete).
Docs commit: `c9d4f646 docs(account): spike findings S1, S2, S3, S6 and S7` on `feat/monoes-account-gate` (parent `eb54672c`), one file, not pushed.
Landing worktree: `$MONOAGENT_CHECKOUT/.claude/worktrees/landing-server`, branch `feat/account-gate-server` at `dbb9f58` (`origin/main` `dd232ae`, re-fetched 2026-10-07 and unmoved, plus Task 1's three commits, which touch only the deploy workflow and its test). Nothing committed or pushed there.

## What I did, per step

**Step 1 (the worktree, per the lead's deviation 1).** No dev server was running in it and nothing listened on 3107. `PUPPETEER_SKIP_DOWNLOAD=1 npm ci --no-audit --no-fund`: `added 784 packages in 14s`. npm 12 blocked 7 dependency install scripts (esbuild x4, sharp, unrs-resolver, workerd: not covered by `allow-scripts` in `~/.npmrc`); tsx, wrangler/workerd and `next dev` all worked regardless. `git rev-parse --git-path info/exclude` resolves to the common directory, `.../landing-deploy-hardening/.git/info/exclude`; I appended `scripts/spikes/` there and checked it with `git check-ignore -v` (the rule matches) and `git status --short` (empty).

**Step 2.** The shell refuses any command that names `.env.local` (even `ls`), so I created it with the Write tool: two lines, `BETTER_AUTH_SECRET` (a throwaway 64-hex value) and `BETTER_AUTH_URL="http://localhost:3107"`. `npm run db:migrate:local`: the table ends `0016_library_votes_comments.sql ✅`. `npx next dev -p 3107` in the background: `Environments: .env.local`, `Ready in 653ms`; `curl .../api/auth/ok`: `200`.

**Step 3.** `tests/helpers/oauth-api.ts` written verbatim from the brief (untracked; Task 3 commits it).

**Step 4.** `scripts/spikes/seed-audience.sql` verbatim. `wrangler d1 execute --local --file`: `"success": true` for both statements. The query returned exactly one row: `https://monoes.me/api/monoagent`, `MonoAgent`, `allowed_scopes` null, `disabled` 0; a second query showed the link `monoagent` to `https://monoes.me/api/monoagent`. I also counted `jwks` rows before anything signed: 0.

**Step 5, S6.** `npx tsx scripts/spikes/s6-claims.ts`, output structure (no token printed):
```
== no resource (today's client): status 200, expires_in 3600, refresh token issued
access: opaque, 32 characters
id: JWT header={"alg":"EdDSA","kid":"hHJpzWkAnY38Gmqg60v8hdbif5GuILw5"}
  iss=http://localhost:3107/api/auth  aud(string)="monoagent"
  exp-iat=36000 claims=acr,at_hash,aud,auth_time,exp,iat,iss,sub
== resource sent on authorize and on the token request: status 200, expires_in 3600, refresh token issued
access: JWT header={"typ":"at+jwt","alg":"EdDSA","kid":"hHJpzWkAnY38Gmqg60v8hdbif5GuILw5"}
  iss=http://localhost:3107/api/auth  aud(array)=["https://monoes.me/api/monoagent","http://localhost:3107/api/auth/oauth2/userinfo"]
  azp=monoagent client_id=monoagent scope="openid profile email offline_access library:read library:write" plan=undefined
  exp-iat=3600 claims=aud,azp,client_id,exp,iat,iss,jti,scope,sid,sub
id: (same as above)
```
Exactly the brief's expectation. A later one-off check (`npx tsx --eval`, claim names and the JSON type of `sid` only): `sid` is a string on a browser-flow token and JSON `null` on a token refreshed after `POST /api/auth/sign-out`.

**Step 6, S2 and S3.** `npx tsx scripts/spikes/s2-s3-refresh.ts`: every line the brief lists, identical (`refresh(R1, resource) 200 ok access=JWT`; `R1 again 400 invalid_grant (invalid refresh token)`; `R2 400 invalid_grant (session not found)`; bound chain without resource `200 access=JWT`; plain chain `200 opaque`, then with resource `200 JWT`; another resource `400 invalid_target (... is not configured)`; `1 before the block, 1 after`; blocked refresh `200 JWT`; new web sign-in `401`; old web session authorize `200 JWT`; sign-out, revoke-sessions, change-password `200` each, and each refresh after them `200 ok access=JWT`).

**Step 6b, S7.** (a) The three greps, with every match recorded in the findings (S7, "The reading"): definition `introspect-6ew7sakf.mjs:1498`; calls `:2160` (refresh grant), `authorize-Crqw4_bR.mjs:3506` and `:3527` (`revokeRefreshToken`); also the import `authorize-Crqw4_bR.mjs:2` and the export list `introspect-6ew7sakf.mjs:2551`; `createRefreshToken` `:1545`; `authorizationCodeId`/`sessionId` hand-on `:2178`/`:2179`; `revokeRefreshToken` `authorize-Crqw4_bR.mjs:3493`; the schema `src/lib/db/schema.ts:358-390`. I then read the whole refresh path (`createUserTokens`, `createRefreshToken`, the window functions, both grant handlers, the revoke endpoint, the session-delete hook) and Better-Auth's hook dispatch, the router and the drizzle adapter.
(b) `npx tsx scripts/spikes/s7-family.ts` (verbatim), output:
```
rotate A (A1 -> A2) 200 ok / rotate B (B1 -> B2) 200 ok
== two chains, each rotated once: 4 rows
  A1 revoked=yes rotated=yes session=30962350 code=995d7500 reference=null
  B1 revoked=yes rotated=yes session=30962350 code=ac30e265 reference=null
  A2 revoked=no  rotated=no  session=30962350 code=995d7500 reference=null
  B2 revoked=no  rotated=no  session=30962350 code=ac30e265 reference=null
exchange C1 (issued without a resource) 200 ok
== 6 rows: the above plus C1 yes/yes and C2 no/no, both code=359af026, session=30962350
sign-out: 200
== after the web session ended: 6 rows, identical but session=null on every row
replay A1 400 invalid_grant
== after the replay of A1: 0 refresh-token rows
B2, the other sign-in 400 invalid_grant
revoke D1 (rotated): 400
== after the revoke of a rotated token: 0 refresh-token rows
E1, the other sign-in 400 invalid_grant
```
(Row labels are mine, matched by fingerprint.) As the brief predicted, except that the revoke route answered 400, not 200 (see the surprises).
(c) Extra probes, `scripts/spikes/s7-probe.ts` (added beyond the brief's file list; excluded from git; structure and fingerprints only), run twice with the same structure (the second run's full output is in the scratchpad, `s7-probe-run2.txt`):
- P1: the repository's `sha256Base64Url` of a refresh token finds its row: true; it equals `node:crypto` SHA-256 base64url: true.
- P2: a login with the resource writes 0 access rows; a plain login writes 1, whose `refresh_id` is its refresh row and whose `authorization_code_id` equals that row's; after a rotation 2; after a replay 0 access and 0 refresh rows.
- P3: revoke of a live token `200 (empty body)`, the other sign-in's row intact; that revoked token presented to the refresh grant `400 invalid_grant (invalid refresh token)`, 0 rows, the other sign-in `400 (session not found)`; revoked again at the revoke route `400 {"error_description":"token not found","error":"invalid_request"}`, 0 rows; a rotated token at the revoke route without a hint `400 token not found`, with `token_type_hint=refresh_token` `400 refresh token revoked`, 0 rows each, the other sign-in dead each time; an unknown token `400 token not found`.
- P4: exchanging authorization code X a second time `400 invalid_grant (invalid code)` deleted exactly X's two rows (X1 and X2 share a code) and left the two other sign-ins; Y1 then `200`.
- P5: one sign-in per web session: two different `session` values and two different codes.
- P6: `GET /api/auth/token` `200`, header `{"alg":"EdDSA","kid":<same kid>}` (no `typ`), `iss` and `aud` `http://localhost:3107`, no `azp`, 900 seconds.
- P7: two refresh rows inserted exactly as Task 7 plans (id, hashed token, client, user, scopes, expiry, created): the one without `authorization_code_id` refreshes `200 JWT` and its successor also has `code=null`; the one with `email-claim:<uuid>` refreshes `200 JWT` and its successor carries it.
- P8: after revoke-sessions and after change-password (revokeOtherSessions) the rows keep `revoked=no` and their code, `session=null`; both sign-ins then refresh `200`.
- P9: two concurrent refreshes of one token, three trials: one `200`, the other `400 invalid_grant (invalid refresh token)`; the other sign-in `200` each time.

**Step 7, S1.** `npx tsx scripts/spikes/s1-key.ts`: `JWKS: hHJpzWkAnY38Gmqg60v8hdbif5GuILw5 OKP/Ed25519 EdDSA`; `access token: alg=EdDSA typ=at+jwt kid=<same>`; `verifies with only the published public key (node:crypto, Ed25519): true`; `id token signed by the same key: true`; `after a refresh: 200, kid <same>`; `jwks table rows: 1 before, 1 after` (0 before S6 signed). The grep of `types.d.mts`: four matches, lines 68, 177, 199, 209. A structural query of the row: `kid` 32 characters, `EdDSA`/`Ed25519`, `expires_at` null, private key not plain JSON. I read `sign.mjs`, `adapter.mjs`, `utils.mjs`, `index.mjs` and confirmed each statement the template makes, with line numbers (in the findings). Nothing in `src`, `tests` or `public` reads `set-auth-jwt`.
Production, the two public GETs only: discovery `issuer` `https://monoes.me/api/auth` (token, revoke and jwks endpoints under it); `/api/auth/jwks`: one key, `GB6kESA9qO98637VArEGR2EjW6wSyYqO` (32 characters) `OKP/Ed25519 EdDSA`.

**Step 8.** The findings file in the brief's structure, every line the run contradicted corrected and said under its spike; committed with two commands (`git add`, then `git commit -m "docs(account): spike findings S1, S2, S3, S6 and S7" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`). The repository's pre-write secret scanner refused the file once, reading a prose phrase that assigned a hash call to the word for the column as a hard-coded credential (a false positive); I rephrased that bullet.

**Step 9.** After stopping the server: `git checkout -- next-env.d.ts` (`next dev` had rewritten one line); `tsconfig.tsbuildinfo` was never touched (no `tsc` ran). `git status --short` shows only `?? tests/helpers/oauth-api.ts`; ignored: `.env.local`, `.next/`, `.wrangler/`, `scripts/spikes/`. Per the lead's deviation 4, the server is stopped (not left running): I stopped my background task and checked that port 3107 is free and that none of my four processes (npm exec, next dev, next-server, workerd) or any process from this worktree remains.

## Files created

- Landing worktree, untracked: `tests/helpers/oauth-api.ts` (for Task 3 to commit).
- Landing worktree, excluded from git: `scripts/spikes/seed-audience.sql`, `s6-claims.ts`, `s2-s3-refresh.ts`, `s7-family.ts`, `s1-key.ts` (all verbatim from the brief), and `s7-probe.ts` (my extra probes, P1-P9).
- Landing worktree, git-ignored: `.env.local`; the local D1 in `.wrangler/state` now holds migrations 0000-0016, the seeded audience row and client link, one `jwks` key, test accounts `gate…@example.com` and their tokens (including two hand-inserted refresh rows from P7).
- mono-agent docs worktree, committed: `docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md` (98 lines).
- Scratchpad (`$LOCAL_CLAUDE_SCRATCH/-Users-morteza-Desktop-monoes-mono-agent/ab93060e-329b-4819-88ef-4bab24fc04a4/scratchpad/`): `landing-dev-3107.log`, `s7-probe-run2.txt` (no token values).

## Restarting the server (Tasks 3 to 8)

```bash
cd $MONOAGENT_CHECKOUT/.claude/worktrees/landing-server
npx next dev -p 3107          # .env.local (BETTER_AUTH_URL=http://localhost:3107) and the local D1 are in place
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3107/api/auth/ok   # expect 200
# after stopping it: git checkout -- next-env.d.ts   (next dev rewrites it)
# only if .wrangler/state is ever lost:
#   npm run db:migrate:local
#   npx wrangler d1 execute monoes-community --local --file scripts/spikes/seed-audience.sql
```

Keep `.env.local` as it is: the local `jwks` row's private half is encrypted with its `BETTER_AUTH_SECRET`. A regenerated secret makes every signing fail until that row is deleted (`npx wrangler d1 execute monoes-community --local --command "DELETE FROM jwks"`; the plugin then creates a new key with a new `kid`).

## Surprises (library and plan)

1. `invalidateRefreshFamily` is named for a family but deletes every refresh token of the client and the user. The provider does have a real family delete, keyed on `authorization_code_id`, on another path: `revokeTokensIssuedForAuthorizationCode` (`introspect-6ew7sakf.mjs:1528`), run on an authorization-code replay (measured, P4).
2. The revoke route has no reuse window and punishes any revoked row before its own client check (`authorize-Crqw4_bR.mjs:3505-3512`). It answers 400, not RFC 7009's 200, for an unknown or already revoked token, because its `error.name === "BAD_REQUEST"` test (`:3603`) never matches: better-call names every error `APIError`. mono-agent's `account logout` must read that 400 as "already gone".
3. A token revoked by `/oauth2/revoke` has no `rotated_at`, so it is never inside Task 3's window: presenting it again ends every MonoAgent token of the account today (P3), and only its family after the hook.
4. Two refreshes of one token at the same moment: the compare-and-set loser gets `invalid_grant` (`:1579-1594`). Measured without a window (P9, the unmodified server); that it holds with Task 3's 300 seconds is by reading only (the loser read the row while it was live, so it never reaches the window check; the findings' "with or without the window" rests on that reading). To mono-agent that is a refusal (D27): the client must serialize refreshes per install. This touches the B plans' guard and refresher design, not the server.
5. On web-session deletion the provider's hook revokes the session's refresh tokens that lack `offline_access`, and its opaque access-token rows (`authorize-Crqw4_bR.mjs:259-260`). MonoAgent's refresh tokens carry `offline_access` and survive; today's opaque MonoAgent access token of that session is marked revoked (read, not measured).
6. `sid` becomes JSON `null` on tokens refreshed after the web session ends; the template said `sid` appears "only on tokens from the browser flow". B1a never reads `sid`.
7. The brief's grep of `index.d.mts` for options finds nothing; in 1.7.1 the option types live in `dist/oauth-1Ud-hvZY.d.mts`. `extensions[].grants` cannot take over `refresh_token` (`introspect-6ew7sakf.mjs:1283`).
8. Plan: Task 7's planned refresh-token insert (Task 7 step 4 item 3) writes no `authorizationCodeId`, so its chains would have no family key (P7). The template's Constants row listed `plan` (absent before Task 3) and its S3 "Result, after Task 4 (verified by its specs)" describes a future state; I relabeled it as the plan's expectation, not part of this run.
9. Environment: the session's working directory was changed twice by the harness during the run (once to `.claude/worktrees`, once to the docs worktree); I used absolute paths throughout, and nothing was affected.

## Concerns for the lead

- Task 7 needs one more field (`authorizationCodeId: \`email-claim:${claim.id}\``) or the family hook cannot scope its chains; the hook must also never key a delete on null (the adapter turns null into `IS NULL`).
- The revoke route's compare-and-set race (`authorize-Crqw4_bR.mjs:3527`) still ends the whole account after the hook, unless the hook also performs MonoAgent's live refresh-token revocations itself; only a `patch-package` covers it natively.
- One `db.batch` makes the hook's two deletes atomic, but it does not serialize against a concurrent rotation of the same family: a refresh whose compare-and-set succeeded just before the batch can insert its successor just after it, a live row carrying the ended family's `authorization_code_id`. The window is narrow and the provider's own deletes share it (its TODO, `introspect-6ew7sakf.mjs:1487-1494`). The findings' "one D1 transaction, unlike the provider's two statements" does not close it; Task 3 should treat it as a residual.
- Concurrent refreshes from one install produce a real `invalid_grant` (surprise 4): a client concern for the B plans.
- The owner check of S7 answer 2 (null `authorization_code_id` rows in production) is read-only and was not run.

## FINDINGS FOR THE LEAD

1. S6: as the brief expected. With `resource`: EdDSA `at+jwt` JWT, `aud` = [audience, userinfo], `azp`/`client_id` `monoagent`, 3600 s; without: opaque, 32 letters. Corrections: no `plan` on unmodified main; `sid` is JSON null after the web session ends (B1a never reads it).
2. S1: one lazily created Ed25519 `jwks` row (0 rows before the first signing), 32-character kid, verifies with the public JWK alone, ID token same kid; the four `types.d.mts` matches (68, 177, 199, 209) are there, so Task 6's primary path stands. Production today: kid `GB6kESA9qO98637VArEGR2EjW6wSyYqO`, issuer `https://monoes.me/api/auth`.
3. S2/S3: every expected line reproduced. New: revoking a live token is 200; presenting a revoked token again (refresh grant or revoke route) ends every MonoAgent token of the account, and Task 3's window never covers it (no `rotated_at`). The revoke route answers 400 for unknown or revoked tokens. Ending web sessions only nulls `session_id`.
4. S7, what identifies a family: `oauth_refresh_token.authorization_code_id`, the SHA-256 of the authorization code (`introspect-6ew7sakf.mjs:1894`). It is equal along a chain and differs between sign-ins; it is copied at every rotation (`:2178`) and through D23 adoption, and it survives sign-out, revoke-sessions and change-password. `session_id` is unusable (shared by every sign-in of one web session, nulled when it ends); `reference_id` is always null. The provider already ends exactly one family by this column on an auth-code replay (`revokeTokensIssuedForAuthorizationCode`, `:1528`, measured).
5. S7, how it revokes today: `invalidateRefreshFamily(ctx, clientId, userId)` (`:1498`) deletes every refresh row of the client and user, and their access rows. It is called by the refresh grant (`:2160`, revoked and outside the window) and twice in `revokeRefreshToken` (`authorize-Crqw4_bR.mjs:3506`: any revoked row, no window; `:3527`: a lost compare-and-set). Measured on both paths: 6 rows to 0, the other sign-in `invalid_grant`.
6. (Superseded: see Fix round 1 and the findings' "Corrected S7 answer 5".) S7, the smallest change: a Better-Auth `hooks.before` in the `betterAuth({...})` options of `getAuth` in `src/lib/auth.ts` (logic in a new module), for `client_id` `monoagent` on `/oauth2/token` (`refresh_token` grant) and `/oauth2/revoke`. No migration, no owner step. It finds the row by the `sha256Base64Url` of the presented value. Only where the provider would punish (row exists, own client, not expired, resource and scope subsets, revoked, outside `isWithinRefreshTokenReuseInterval`), it deletes in one `db.batch` the access rows, then the refresh rows, of (client, user, `authorization_code_id`), and throws `APIError("BAD_REQUEST", {error:"invalid_grant", error_description:"invalid refresh token"})` (on the revoke route, today's 400 bodies). Inside the window it steps aside, so the 300 s retry answer stays. The dead row is in the deleted set, so a dead token is punished once.
7. Required with it: Task 7's planned refresh insert has no `authorization_code_id` (P7: its chain stays null); add `authorizationCodeId: \`email-claim:${claim.id}\``. Never key a delete on null (the adapter makes it `IS NULL`); for a null key, delete only the presented row. Owner read-only check: `SELECT count(*) FROM oauth_refresh_token WHERE client_id='monoagent' AND authorization_code_id IS NULL` (expect 0).
8. (Residuals superseded: see Fix round 1.) Other candidates: the provider has no option or hook for this. A family column buys nothing, since the provider writes rows via `adapter.create`, which runs no `databaseHooks`, and it needs a migration. `patch-package` covers all three call sites, the `:3527` race included, but must be redone at every upgrade; a root `postinstall` does run under npm 12 and in CI. Residuals of the hook: that race, unless the hook also performs MonoAgent's live revocations; and a rotation racing the batch, which can leave one live successor in the ended family (narrow; the provider's own deletes share it).
9. Access tokens: JWTs have no rows, so they stay valid up to 3600 s after their family ends, as today. Opaque rows carry `refresh_id` and the code, and go with their family. Other families are untouched. Task 3's spec "a replayed refresh token after the window ... takes the account's newer MonoAgent refresh token with it" must become "its own family's newer token; another sign-in keeps working".

## Fix round 1 (after task-2-review-1.md), 2026-10-07

Status: DONE. Docs commit: `2e5ad3bd docs(account): correct spike S7's act-condition after the review` (a new commit on `feat/monoes-account-gate`, on top of `8354c255`; `c9d4f646` untouched; one file, +96/-20; not pushed). The docs worktree had uncommitted edits by other sessions (`b1b-signin.md`, `b3b-doors.md`): I committed with a pathspec, so they stayed out and untouched. Items 6 and 8 of FINDINGS FOR THE LEAD above are superseded by the findings' "Corrected S7 answer 5". Of the concerns above, the corrected rule closes the second (the `:3527` race). The third, a rotation racing the batch, remains as residual 1, narrowed by a second sweep. The fourth, concurrent refreshes, still stands and now also covers in-flight retries and logout races (residuals 2 and 3).

### What changed in the findings

- **S7 answer 5:** the hook's row in the candidates table now covers the refresh grant and both `revokeRefreshToken` call sites. The old "What the hook does" block is replaced by a paragraph saying why it failed and what still holds from it.
- **New subsection "### Corrected S7 answer 5"** inside S7, so the heading order of step 8 is kept. It holds:
  - the measured table;
  - the request handling the rule mirrors, with source lines;
  - the rule in seven points (token route: decide on the row; revoke route: the hook owns MonoAgent's refresh-token revocation and normalizes like the provider);
  - the Task 3 specs;
  - the residuals, adding the review's two (a logout racing a refresh in one install; a retry arriving while the first request is still in flight), the window edge, and JWT lifetime;
  - the list "Plan text that must change", with plan A `:2721-2722`, `:2727-2728` and `:2761` added to Task 3's and Task 7's lines.
- **Constants rows:**
  - "Refresh-token family" names the corrected rule and the B plans' serialization.
  - "Refresh token" no longer says "with or without the window": the measurement was without a window, and inside it, by reading, a loser that reads before the answer is stored also gets `invalid_grant` (`:1881-1886`, `:2155-2158`).
- **S2:** the same window wording, and the consequence now names logout races and in-flight retries.
- **S6:** points to `s7-probe.ts P10`.
- **S7 answer 4:** adds R1, R2, C1 and C2.
- **Null-key note:** the adapter's `IS NULL` against drizzle's `= NULL` (`drizzle-orm/sql/expressions/conditions.js:20-22`); both make the presented-row branch necessary.
- **Date line:** names the fix round.

### Two refinements to the review's rule

Both come from the source check.
1. The provider refuses two or more non-empty `client_id` values (`utils-B77ebneW.mjs:559-561`), so the hook steps aside then. The review's "every non-empty client_id" would have handled them.
2. The hook's 200 must be a truthy return, such as `new Response(null, { status: 200 })`: only a truthy object short-circuits (`dispatch.mjs:91-103`).

Also confirmed: `refresh_token` is not trimmed by the token schema, so the token-route hook must hash it as sent (T3).

### Source checks (all as the review states)

- better-call keeps the last of a repeated field (`utils.mjs:36-38`).
- Validation runs inside the endpoint (`context.mjs:10-17`).
- The token schema trims only `grant_type` (`authorize-Crqw4_bR.mjs:4580`; `refresh_token` at `:4588` is not trimmed).
- The revoke schema transforms neither `token` nor `token_type_hint` (`:4850-4851`).
- `cloneRequest: true` on both endpoints (`:4578`, `:4844`).
- The hint rule (`:3567`) and the strip (`:3573`). `stripAccessTokenAuthorizationScheme` is exported by `better-auth/oauth2`. Called live: `"  Bearer   abc  "` and `"  abc  "` give `"abc"`, and `"Basic abc"` stays whole.
- Client validation (`utils-B77ebneW.mjs:636-657`).

### Commands run

```bash
cd $MONOAGENT_CHECKOUT/.claude/worktrees/landing-server
npx next dev -p 3107                              # background; Ready, /api/auth/ok 200
npx tsx scripts/spikes/s7-bypass.ts               # run 1: R1, R2 printed, then SQLITE_BUSY (see below)
npx tsx scripts/spikes/s7-bypass.ts               # run 2: all 12 cases (about 6 minutes)
npx tsx scripts/spikes/s7-probe.ts P10            # the sid check, alone
# stopped the server (TaskStop), checked port 3107 free and no process of this worktree left
git checkout -- next-env.d.ts                     # git status --short: only ?? tests/helpers/oauth-api.ts
```

Run 1 failed on its third case with `SQLITE_BUSY`. Each `withDb` call started its own workerd proxy against the SQLite file the dev server also holds, at about 2.5 minutes per case. The thrown query error printed one stored token hash (the database form, not a token), so I deleted that output file. Run 2 uses one platform proxy for the whole run and an idempotent retry around each database call, and prints only an error's first line.

The rows column is read through that long-lived proxy. It agrees with B1's refresh (the server's own view) in all 12 rows, so the proxy read fresh data.

### Files

- New, excluded from git: `scripts/spikes/s7-bypass.ts`.
- Changed, excluded from git: `scripts/spikes/s7-probe.ts`. It gains P10 (`sidAfterSignOut`), called at the end of the full run, or alone with the argument `P10`. P1 to P9 were not re-run this round.
- Scratchpad: `s7-bypass-run2.txt` (the table), `s7-probe-p10.txt`, `landing-dev-3107-fix1.log`. None holds a token value.

### The table (s7-bypass.ts, run 2; no token values)

| case | hook keyed on the plain request (c9d4f646 rule) | corrected rule (computed) | provider (measured): answer; MonoAgent rows; B1 |
|---|---|---|---|
| R1 revoke: A1 rotated <300 s ago (window set by SQL) | inside the window: steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R2 revoke: A1 rotated and expired (expires_at by SQL) | expired: steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R3 revoke: client_id=monoagent&client_id= | client_id="": steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R4 revoke: token_type_hint=x | token_type_hint="x": steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R5 revoke: token=Bearer <A1> | no row for the value as sent: steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R6 revoke: token=' <A1> ' (spaces) | no row for the value as sent: steps aside | owns it: ends A's family, 400 | 400 invalid_request (token not found); rows 3 -> 0; B1 400 invalid_grant |
| R7 revoke: another client's revoked row, client_id=monoagent | another client's row: steps aside | owns it: deletes nothing, 400 | 400 invalid_request (token not found); rows 3 -> 0; the other client's rows 2 -> 2; B1 400 invalid_grant |
| T1 token: grant_type='refresh_token ' (trailing space) | grant_type="refresh_token ": steps aside | acts: ends A's family, 400 invalid_grant | 400 invalid_grant (invalid refresh token); rows 3 -> 0; B1 400 invalid_grant |
| T2 token: client_id=monoagent&client_id= | client_id="": steps aside | acts: ends A's family, 400 invalid_grant | 400 invalid_grant (invalid refresh token); rows 3 -> 0; B1 400 invalid_grant |
| T3 token: refresh_token=' <A1> ' (spaces), control | no row for the value as sent: steps aside | no row: steps aside | 400 invalid_grant (session not found); rows 3 -> 3; B1 200 ok |
| C1 token: A1 rotated <300 s ago (window set by SQL), control | inside the window: steps aside | inside the window: steps aside | 400 invalid_grant (invalid refresh token); rows 3 -> 3; B1 200 ok |
| C2 token: A1 rotated and expired, control | expired: steps aside | expired: steps aside | 400 invalid_grant (invalid refresh token); rows 3 -> 3; B1 200 ok |

P10:
```
== P10: sid on an access token refreshed after the web session ended
  the browser sign-in's access token: sid is string
  sign-out 200, then a refresh 200: sid is JSON null
```

### State left

- The server is stopped. Restart it with the commands in "Restarting the server" above.
- Landing worktree: `git status --short` shows only `?? tests/helpers/oauth-api.ts`. Nothing was committed or pushed there.
- Docs: `2e5ad3bd`, not pushed.

## Fix round 2 (after task-2-rereview-1.md, New Error 1), 2026-10-07

Status: DONE. Docs commit: `56d02843 docs(account): pin how the revoke route's caller is read (spike S7)`. It is a new commit on `2e5ad3bd`, findings file only, +23/-6, not pushed. Other sessions' uncommitted plan edits (four files) were left out by a pathspec commit. This round is text only, with no new measurements: the re-reviewer measured both shapes.

Source re-read before editing:
- `utils-B77ebneW.mjs:557-558`: the provider's gate (the lowercased content type contains the form type) and its read, `new URLSearchParams(await request.clone().text())`.
- `better-call/dist/utils.mjs:3`, `:24`: the JSON regex, tested before the form branch, matches `application/x-www-form-urlencoded+json`.
- The probes `rva2-parse.mjs` and `rva2-e2e.ts` sit next to the re-review, with the three cases it quotes.

Changes in the findings ("Corrected S7 answer 5"):
- **The intro** now lists three changes from the review, not two refinements: the refusal of two or more `client_id` values, the truthy 200, and the caller resolved from the raw text alone.
- **"The request handling the rule mirrors"**:
  - the caller bullet now names the gate (`:557`) and the read (`:558`);
  - a new bullet gives the two shapes better-call parses differently (`+json` and a leading U+FEFF, written as "U+FEFF" with no literal character in the file);
  - a new bullet cites the re-review's measurement (both shapes plus the control: 400 `token not found`, the other sign-in `invalid_grant`) and its probes, and says the U+FEFF shape was measured on Node only, not on workerd.
- **Point 4** has the caller bullet replaced, as the re-review's fix says:
  - whenever `ctx.request` exists, take the non-empty `client_id` values of the provider's own read;
  - do not test the content type (passing `allowedMediaTypes` implies passing `:557`);
  - never use `formData()` or `ctx.body`;
  - two or more values: step aside; exactly one: that is the caller; none: step aside;
  - use `ctx.body.client_id` only without `ctx.request`;
  - read the raw text only after the row is found;
  - one sentence on why the same read in the same runtime makes the U+FEFF behavior irrelevant.
- **The Task 3 specs** gain the `+json` case and the leading U+FEFF case (run where the plan's other specs run). The "Plan text that must change" Task 3 bullet names both, each proving the other sign-in stays alive.
- **Residual 3**: the provider's read is now located at `:2113-2119` (tested at `:2146`), the re-reviewer's cosmetic point.
- **The date line** names both fix rounds.

Not added: the re-review's third, optional spec (an upper-case media type). The new text forbids testing the content type at all, and that is the mistake the spec would catch.
