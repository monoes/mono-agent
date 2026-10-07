# Mandatory monoes.me Account — Part A: the monoes.me server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** monoes.me issues mono-agent an audience-bound JWT access token signed with a key the client can pin, answers `invalid_grant` for a blocked account, ends only the family of a refresh token that is replayed after its reuse window (ruling R1 of 2026-10-07), serves the library with that token, and deploys only from `main`.

**Architecture:** The `monoagent` OAuth client gets an RFC 8707 resource (a database row plus a client link) so the `@better-auth/oauth-provider` mints JWTs when a `resource` is sent and leaves every other request as it was. The `jwt()` plugin signs with a static Ed25519 key supplied as a Worker secret through its `adapter` option. A block revokes the account's tokens and web sessions; `customAccessTokenClaims` adds the `plan` claim and refuses blocked accounts; `getRequestAuth` verifies the JWT beside opaque tokens; a Better-Auth before hook ends only the family of a replayed MonoAgent refresh token.

**Tech Stack:** Next.js 16 on Cloudflare Workers (OpenNext), Better-Auth 1.7.1 with `@better-auth/oauth-provider` 1.7.1, Drizzle on D1, `jose`, TypeScript; tests are `node:test` unit tests (`npm test`) and browserless Playwright specs; GitHub Actions.

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (§5 entirely, D18, D19, D27, §4.1 and §4.7 server halves, spikes S1, S2, S3, S6, and S7 for ruling R1). Index: `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§3.4 item 10). Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

## Owner-run steps

No agent runs these: each changes GitHub or Cloudflare, so the owner does. The task named in the last column holds the exact commands and the expected output. Every other step of this plan changes nothing outside a local checkout or a pull request, and `deploy.yml` never runs a migration or sets a secret.

| Step | When | The owner runs | What it changes in production | Task |
|---|---|---|---|---|
| **O1** | After Task 1's pull request shows a check named `check`; before the other tasks merge | `gh api -X PUT repos/monoes/monoes-landing/branches/main/protection --input branch-protection.json` | GitHub only, no Worker change: `main` then requires the `check` status and refuses force-push and deletion. Undo: `gh api -X DELETE repos/monoes/monoes-landing/branches/main/protection` | 1, step 9 |
| **O2** | After Task 3 merges; before any mono-agent release sends a `resource` | `npx wrangler d1 migrations apply monoes-community --remote` | D1: adds one `oauth_resource` row (the audience) and one `oauth_client_resource` link (`monoagent`), and applies every other migration still pending, which the command lists before it asks. No table is altered or dropped. Without these rows a `resource` is answered `invalid_target`. A last, read-only query counts MonoAgent refresh tokens without a family key (expected 0) | 3, step 19 |
| **O3** | Before Task 6's pull request merges | `npx tsx scripts/generate-signing-key.ts \| npx wrangler secret put MONOAGENT_JWT_PRIVATE_JWK` | Worker secrets: adds `MONOAGENT_JWT_PRIVATE_JWK`, the Ed25519 private key, which the Worker cannot give back (keep a copy in a password manager). Nothing changes until Task 6 deploys; from then on audience-bound access tokens are signed with this key and not with the database key. Its `kid` and public key go to the mono-agent release R. Without it, the deployed Task 6 answers every token request that signs with a 500 | 6, step 11 |
| **O4** | After Task 6 deploys | `curl -s https://monoes.me/api/auth/jwks`, then one sign-in with today's client | Nothing (read-only): exactly one key is published and its `kid` is the one from O3 | 6, step 12 |
| **O5** | At each key rotation, not before | The runbook: put the new public key first (`wrangler secret put MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK`), then the new private key (`MONOAGENT_JWT_PRIVATE_JWK`), later `wrangler secret delete MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK` | Worker secrets: the signing key changes; the old public key stays published while the secret exists, so tokens issued before the swap keep verifying | 6, step 13 |

## Global Constraints

Copied from the index (§2, §3.2, §3.4, §4), verbatim where marked, adapted to this repository where it says so; the lines that bind this plan.

- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace. A grant whose outcome is unknown (the request may have been processed, so monoes.me may have rotated the refresh token) or whose answer could not be saved (A24(d)) is retried within 240 seconds and after that is never presented again: this machine drops its refresh token and the reason is `unconfirmed` (A24, §3.6), a grace reason that ends as `locked(unconfirmed)`; the other installs of the account are untouched.
- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard re-checks `session.json`'s mtime lazily inside `Status`, at most once per 5 seconds (no goroutine for a non-refresher guard; spec A8). The refresh request carries `resource=<Audience>`.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Frozen values this plan must produce (index §3.2): `Issuer = "https://monoes.me/api/auth"`, `Audience = "https://monoes.me/api/monoagent"`, `ClientID = "monoagent"`.
- Library compatibility (index §3.4 item 10): once `library` commands use the machine session, every `/api/library/*` call carries the audience-bound JWT. The server's token check (`getAuthenticatedUser`) must accept that JWT (scopes `library:read` and `library:write` included) alongside today's opaque tokens; plan A proves it with a server test that mints one and calls `/api/library/me` and a list endpoint.
- Commit with two separate commands: `git add <files>`, then `git commit -m "<subject>" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`. Never use bare `git stash`. Tests and logs never print a token, refresh token or key: inspect structure (lengths, claim names), not values.
- Nothing is pushed and no pull request is opened unless the owner says so; nothing merges without the owner. (Adapted: in this repository every merge to `main` deploys to production.)

## Review Focus

Failure modes the spec implies that no task's tests would otherwise exercise, most likely to bite first. Each is pinned by a test in the task that owns the code.

1. A replayed or copied refresh token (a second machine, a copied session file, a retry after a crash) makes the unmodified provider delete every MonoAgent refresh token of that account, so every install would lock as refused. Ruling R1 of 2026-10-07: a replay of a rotated-away or revoked refresh token ends only that token's family, the sign-in it comes from, and every other sign-in of the account keeps working. Task 3's before hook does it (`src/lib/refresh-family.ts`, keyed on `authorization_code_id`, spike S7's "Corrected S7 answer 5"): on the refresh grant it acts on the stored row, and MonoAgent's refresh-token revocations it does itself, reading each request as the provider reads it. Only the client's own retry of a lost answer, within 300 seconds (the owner's choice; the client's retry window, 240 seconds, sits inside it), is answered with the same response instead. Pinned in Task 3 by "a retry inside the 300-second reuse window gets the same answer and ends nothing" and by `tests/account-gate-family.spec.ts`, each test ending with the account's other sign-in still refreshing: "a replay after the window is invalid_grant and ends only that sign-in: another sign-in of the account keeps refreshing" and the same replay with a trailing space in `grant_type` or `client_id=monoagent&client_id=`; "a dead token is punished once: presenting it again ends nothing, and the sign-ins made before and since survive" (a machine whose disk is full cannot save its `refused` marker and presents the dead token at every due command, spec A21); the eight "the revoke route ends only the family of a rotated token, inside the reuse window too" (no hint, the hint `refresh_token`, an unknown hint, a `Bearer ` prefix, spaces around the token, a repeated `client_id`, a `+json` media type, a leading U+FEFF) and its expired variant; "a revoke of a live token (account logout) answers 200 and ends its own sign-in; presenting it again ends nothing"; "another client's revoked token presented as monoagent's at the revoke route ends nothing"; "the opaque access tokens of an ended family go with it; another sign-in's keep working"; "a chain without a family key ends alone: a null authorization_code_id never keys a delete". In CI by `src/lib/refresh-family.test.ts`; in Task 4 by the rotated token a block must not leave replayable; and in Task 7 by the email-code chain's family key. Accepted residuals (Task 3, design): a rotation racing the hook's delete can leave one live successor in an ended family (a second sweep narrows it); a request whose twin is still in flight (a retry inside the window before the first answer is stored, two refreshes at once, a logout racing a refresh) gets `invalid_grant`, which mono-agent reads as a refusal though no family ends, so the B plans serialise refresh and logout per install and never retry a request that may still be in flight; the window's edge holds while the hook's and the provider's clock reads are under 10 seconds apart; JWT access tokens of an ended family live out their hour; and a provider upgrade that changes what the hook mirrors can bring back the account-wide delete without failing CI, so such an upgrade runs the family spec first. The client keeps A24 and A25 as defence in depth until the owner has measured the deployed server.
2. A refresh token not used for 30 days answers `invalid_grant`, which the client reads as a refusal, not as unreachable. Pinned in Task 3 by "a refresh token past its 30 days is invalid_grant".
3. A deploy without the signing secret, or with a malformed one, must answer a loud 500 and must never sign with a key no release pins; it must not break `get-session`. Pinned in Task 6 by the `signingKeyAdapter` tests ("fails closed", "reports a malformed key when it is used") and by `disableSettingJwtHeader`.
4. A verified JWT whose user was deleted, a token from another client or for another audience, an unsigned or foreign-key token: none may authenticate a library or community call. Pinned in Task 5 by "refuses a verified token whose user no longer exists" and the `verifyMonoagentAccessToken` cases.
5. An emailed code with a `resource` from a client that is not MonoAgent, or for another audience, must be refused `invalid_target` without using the code up, a blocked account must get no token from that route either, and without a `resource` the access token must stay the opaque one it always was. Pinned in Task 7 by the claim spec and, in CI, by two route unit tests.

## Observed during planning

A local server (`next dev`, local D1) was driven with real PKCE flows on 2026-10-05; production was read through two public GETs. The full tables are the findings template of Task 2.
- **S6, a token with a `resource` sent:** a JWT, header `{"typ":"at+jwt","alg":"EdDSA","kid":...}`; `iss` is `https://monoes.me/api/auth` (production's discovery document agrees); `aud` is an array `["https://monoes.me/api/monoagent","<issuer>/oauth2/userinfo"]` (a string only without `openid`); `azp` and `client_id` are both `monoagent`; `exp - iat` is 3600; claims `aud azp client_id exp iat iss jti scope sid sub` (`plan` after Task 3). Without a `resource` the access token is opaque (32 letters). The frozen `Issuer`, `Audience` and `ClientID` need no change.
- **S1:** the default key is one non-rotating Ed25519 database row (production: `kid` `GB6kESA9qO98637VArEGR2EjW6wSyYqO`); a key supplied through `adapter.getJwks` signs, is published, and no row is generated.
- **S2:** an old refresh token exchanged with `resource` gives an audience-bound JWT; refresh tokens rotate; on the unmodified provider, presenting a rotated one ends every MonoAgent session of the account (with the 300-second reuse window of Task 3, only once the window has passed). Ruling R1 of 2026-10-07 requires that such a replay end only the family of the reused token: spike S7 (Task 2, 2026-10-07) found the family in `authorization_code_id`, and Task 3's before hook ends only that family, on the refresh grant and on the revoke route.
- **S3:** the refresh grant never looked at a blocked user (200) and a live web session could mint new tokens; ending a web session does not touch refresh tokens.

---

## How this plan is executed

- **Who and where.** One implementer in a fresh clone of `github.com/monoes/monoes-landing` (public; not another session's working copy, which is behind origin), task by task, or hand the whole plan to the Claude session that works in that repository. The one cross-repository output is the findings file of Task 2: written into the mono-agent worktree (path in Task 2) or handed to the lead as text.
- **One small pull request per change.** Task 1 first and alone: today `deploy.yml` runs `wrangler deploy` on `pull_request`, so any pull request deploys to production. Task 2 makes no pull request and nothing it makes is pushed. Tasks 3 to 8 each get a branch and a pull request, opened only when the owner says so, cut from `origin/main` once the tasks they build on have merged (Task 4 builds on Task 3; Task 6's rehearsal needs Task 5).
- **Merge order:** 1, 3, 4, 5, then the owner puts the signing key in place (Task 6, step O3), then 6, 7, 8. The mono-agent release R may ship only after every task has deployed and O2 to O4 are done.
- **OWNER-RUN steps** (O1 to O5) are the table at the top of this plan. No agent runs them.
- **Backward compatibility.** Today's client sends no `resource`; every task keeps that request working and carries a regression test: Task 3's first spec test (opaque token, same response), Task 4's plain-chain refresh, Task 5's opaque-token cases, Task 6's unpinned mode, Task 7's claim without `resource` (the opaque token unchanged).
- **Before every commit** `git status --short` lists only the files the task names. `next dev` and `tsc` rewrite two tracked files, `next-env.d.ts` and `tsconfig.tsbuildinfo`: run `git checkout -- next-env.d.ts tsconfig.tsbuildinfo`, never stage them. Every task ends with `npm test` (`ℹ fail 0`), `npx tsc --noEmit` and `npx eslint <changed files>` (no output).
- **Specs** use no browser: `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/<spec> --reporter=line` against the Task 2 server. CI's `check` runs the unit tests and the build only, so the implementer runs the specs before a pull request is offered. Existing UI specs need one (`npx playwright install chromium`, or `E2E_CHROMIUM="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"`) and one can flake under load: rerun a failure alone first.
- **Secrets.** `.env.local` and the key files hold secrets: never print them. If your harness refuses commands that name `.env.local`, run those in your own terminal.

---

### Task 1: Deploy hardening (alone, first)

**Files:**
- Create: `src/lib/deploy-workflow.test.ts`
- Modify: `.github/workflows/deploy.yml` (the whole file, 33 lines on `main`)

**Interfaces:** Consumes nothing. Produces two jobs. `check` runs for every event (install, `npm test`, OpenNext build) and holds no Cloudflare credential; its name, `check`, is the required status check. `deploy` needs `check` and runs only for a push to `main` or a manual run on `main`.

- [ ] **Step 1: Confirm nothing else is in flight.** Until this lands, any open pull request deploys on its next push.

```bash
gh pr list -R monoes/monoes-landing --state open --json number,title
```

Expected: `[]`.

- [ ] **Step 2: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-deploy-hardening origin/main`.

- [ ] **Step 3: Write the failing test.** Create `src/lib/deploy-workflow.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The Cloudflare token in this workflow deploys code to monoes.me, and what monoes.me signs is
// what every mono-agent install trusts. Pull requests build and test; only main deploys.
const text = readFileSync(new URL("../../.github/workflows/deploy.yml", import.meta.url), "utf8");

// The text of each top-level job, keyed by job id (jobs sit at two-space indentation).
function jobs(): Record<string, string> {
  const out: Record<string, string> = {};
  let current = "";
  for (const line of text.slice(text.indexOf("\njobs:\n") + 7).split("\n")) {
    const header = line.match(/^  ([A-Za-z][\w-]*):\s*$/);
    if (header) out[(current = header[1])] = "";
    else if (current) out[current] += `${line}\n`;
  }
  return out;
}

describe(".github/workflows/deploy.yml", () => {
  const { check, deploy } = jobs();

  it("triggers only on a push to main, a pull request to main and a manual run", () => {
    const on = text.slice(text.indexOf("\non:\n") + 1).split("\n").slice(1).join("\n").split(/\n(?=\S)/)[0];
    assert.deepEqual([...on.matchAll(/^  ([a-z_]+):/gm)].map((m) => m[1]).sort(), ["pull_request", "push", "workflow_dispatch"]);
    assert.match(on, /push:\n\s+branches: \[main\]/);
  });

  it("builds and tests every event, in a job that holds no Cloudflare credentials", () => {
    assert.ok(check, "a `check` job exists");
    assert.ok(!check.includes("if:"), "check has no condition: it runs for pull requests too");
    assert.match(check, /run: npm test\b/);
    assert.match(check, /opennextjs\/cloudflare build/);
    assert.ok(!/wrangler|CLOUDFLARE|secrets\./.test(check));
  });

  it("deploys from the deploy job only, after check, and only for main", () => {
    assert.ok(deploy, "a `deploy` job exists");
    assert.match(deploy, /needs: check/);
    const condition = deploy.match(/^\s+if: (.+)$/m)?.[1] ?? "";
    for (const part of ["github.ref == 'refs/heads/main'", "github.event_name == 'push'", "github.event_name == 'workflow_dispatch'"]) assert.ok(condition.includes(part), part);
    assert.ok(!condition.includes("pull_request"));
    assert.equal(text.match(/wrangler deploy/g)?.length, 1);
    assert.equal(text.match(/secrets\.CLOUDFLARE_API_TOKEN/g)?.length, 1);
    assert.ok(deploy.includes("wrangler deploy") && deploy.includes("CLOUDFLARE_API_TOKEN"));
  });
});
```

- [ ] **Step 4: Run it against today's workflow.** `node --experimental-strip-types --test src/lib/deploy-workflow.test.ts`. Expected: FAIL with `ℹ pass 1` and `ℹ fail 2`: `AssertionError [ERR_ASSERTION]: a `check` job exists` and `The input did not match the regular expression /needs: check/`.

- [ ] **Step 5: Replace `.github/workflows/deploy.yml`:**

```yaml
name: Deploy to Cloudflare Pages

# Pull requests build and test; they never deploy. Only a push to main, or a
# manual run on main, reaches the deploy job. The Cloudflare token deploys code
# to monoes.me, and what monoes.me signs is what every mono-agent install trusts,
# so nothing but main may use it (see src/lib/deploy-workflow.test.ts).
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  workflow_dispatch:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'

      - run: npm ci

      - run: npm test

      - run: npx @opennextjs/cloudflare build

      - name: Generate Markdown-for-Agents assets
        run: node scripts/generate-markdown-assets.mjs

  deploy:
    needs: check
    if: github.ref == 'refs/heads/main' && (github.event_name == 'push' || github.event_name == 'workflow_dispatch')
    runs-on: ubuntu-latest
    permissions:
      contents: read
      deployments: write
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'

      - run: npm ci

      - run: npx @opennextjs/cloudflare build

      - name: Generate Markdown-for-Agents assets
        run: node scripts/generate-markdown-assets.mjs

      - name: Deploy to Cloudflare Workers
        run: npx wrangler deploy
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}
          CLOUDFLARE_ACCOUNT_ID: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
```

- [ ] **Step 6: Run the test, parse the file, run the suite.**

```bash
node --experimental-strip-types --test src/lib/deploy-workflow.test.ts
python3 -c "import yaml; d = yaml.safe_load(open('.github/workflows/deploy.yml')); print(list(d['jobs']))"
npm test
```

Expected: `ℹ pass 3`; `['check', 'deploy']`; `ℹ fail 0`.

- [ ] **Step 7: Commit.**

```bash
git add .github/workflows/deploy.yml src/lib/deploy-workflow.test.ts
git commit -m "ci(deploy): deploy only from main; pull requests build and test" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 8: Offer the branch to the owner.** When the owner says to open the pull request: `git push -u origin feat/account-gate-deploy-hardening`, then `gh pr create --title "ci(deploy): deploy only from main" --body "Pull requests build and test; only main deploys."`. When the checks have run, `gh pr checks <number>` expected: `check` pass and `deploy` skipping. The owner merges; the merge runs `check`, then `deploy`, as before.

- [ ] **Step 9 (OWNER-RUN, O1): protect `main`.** `main` is unprotected today (the protection endpoint answers 404). Run this after step 8 showed a check named `check`. Write `branch-protection.json` outside the repository with all four top-level keys the endpoint requires:

```json
{
  "required_status_checks": { "strict": true, "contexts": ["check"] },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
```

```bash
gh api -X PUT repos/monoes/monoes-landing/branches/main/protection --input branch-protection.json
gh api repos/monoes/monoes-landing/branches/main/protection --jq '.required_status_checks.contexts, .allow_force_pushes.enabled'
```

Expected: `["check"]`, then `false`. `enforce_admins` is false so the owner can still recover from a broken check; set it to true to close that door too. Separate decision, not part of this plan: `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` are repository secrets, readable by a workflow any collaborator adds on any branch; moving them into a GitHub Environment limited to `main` closes that.

### Task 2: Spikes S1, S2, S3, S6, S7 (local only; nothing is pushed)

**Files:** everything here stays untracked and nothing is pushed. `scripts/spikes/` is excluded from git in step 1 so it cannot be committed by accident; the helper `tests/helpers/oauth-api.ts` is committed by Task 3.
- Create: `tests/helpers/oauth-api.ts`, `scripts/spikes/seed-audience.sql`, `scripts/spikes/s6-claims.ts`, `scripts/spikes/s2-s3-refresh.ts`, `scripts/spikes/s7-family.ts`, `scripts/spikes/s1-key.ts`
- Write, in the mono-agent repository: `/Users/morteza/Desktop/monoes/mono-agent/.claude/worktrees/feat+monoes-account-gate/docs/mastermind/specs/2026-10-05-monoes-account-gate-spike-findings.md` (or hand the text to the lead if that path is out of reach)

**Interfaces:** Produces the findings file (its Constants table is what B1a's `internal/account/claims.go` is checked against, and its S7 section is what the lead writes Task 3's family steps from) and the helper that Tasks 3 to 7 import: `AUDIENCE`, `MONOAGENT_SCOPES`, `signUp`, `signIn`, `pkce`, `authorize`, `exchange`, `refresh`, `login`, `decodeJwt`, `isJwt`, `verifiesAgainstJwks`, `bearer`, `withDb`, `setUserRole`, `seedClaimRequest`. Consumes nothing.

- [ ] **Step 1: Clone and install.** Stop any dev server in the old clone first: `npm ci` replaces `node_modules` and kills it.

```bash
WORK="$(mktemp -d)"; git clone https://github.com/monoes/monoes-landing "$WORK/monoes-landing" && cd "$WORK/monoes-landing"
PUPPETEER_SKIP_DOWNLOAD=1 npm ci --no-audit --no-fund
echo "scripts/spikes/" >> "$(git rev-parse --git-path info/exclude)"
```

Expected: `added 784 packages` (the count may differ).

- [ ] **Step 2: Run the server locally.** The repository runs on Next dev with a local D1 (`getCloudflareContext` reads `.wrangler/state`), so `npm run db:migrate:local` is the whole database setup. `BETTER_AUTH_URL` must be in `.env.local`, because the app reads `process.env` and `.dev.vars` alone is not read there (sign-up then answers `INVALID_ORIGIN`). Use a port other than 3000 so it never meets another dev server, and make the URL match it: the origin check and every token's `iss` come from it.

```bash
printf 'BETTER_AUTH_SECRET="%s"\nBETTER_AUTH_URL="http://localhost:3107"\n' "$(openssl rand -hex 32)" > .env.local
npm run db:migrate:local
npx next dev -p 3107          # leave it running in a second terminal
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3107/api/auth/ok
```

Expected: a table ending `0016_library_votes_comments.sql ✅`, then `200`.

- [ ] **Step 3: Write the shared helper** `tests/helpers/oauth-api.ts` (the whole MonoAgent OAuth flow over plain HTTP, no browser):

```ts
// The MonoAgent OAuth flow over plain HTTP, no browser: sign up, authorize (PKCE), consent,
// token, refresh. tests/helpers/library-auth.ts drives the same flow through the UI; these
// specs need no page, so they run without a browser. The dev server's BETTER_AUTH_URL must
// equal the baseURL (the default on port 3000): it ends up in every token's `iss`.
import { createHash, createPublicKey, randomBytes, verify } from "node:crypto";
import { getPlatformProxy } from "wrangler";
import { drizzle } from "drizzle-orm/d1";
import { eq } from "drizzle-orm";
import { emailClaimRequest, user } from "../../src/lib/db/schema";
import { sha256Base64Url } from "../../src/lib/community/hash-token";

export const AUDIENCE = "https://monoes.me/api/monoagent";
export const MONOAGENT_SCOPES = "openid profile email offline_access library:read library:write";
const LOOPBACK = "http://127.0.0.1:53682/callback";
const PASSWORD = "TestPass1234";

export type Account = { baseURL: string; cookie: string; userId: string; email: string };
export type TokenResponse = {
  access_token?: string;
  refresh_token?: string;
  id_token?: string;
  token_type?: string;
  expires_in?: number;
  scope?: string;
  error?: string;
  error_description?: string;
};
export type Result = { status: number; body: TokenResponse };

const cookiesOf = (res: Response) => res.headers.getSetCookie().map((c) => c.split(";")[0]).join("; ");

async function webAuth(baseURL: string, path: string, body: object, email: string): Promise<{ status: number; account?: Account }> {
  const res = await fetch(new URL(path, baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: new URL(baseURL).origin },
    body: JSON.stringify(body),
  });
  if (!res.ok) return { status: res.status };
  const { user: found } = (await res.json()) as { user: { id: string } };
  return { status: res.status, account: { baseURL, cookie: cookiesOf(res), userId: found.id, email } };
}

export async function signUp(baseURL: string): Promise<Account> {
  const email = `gate${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}@example.com`;
  const r = await webAuth(baseURL, "/api/auth/sign-up/email", { email, password: PASSWORD, name: email.split("@")[0] }, email);
  if (!r.account) throw new Error(`sign-up failed: ${r.status}`);
  return r.account;
}

export const signIn = (baseURL: string, email: string) => webAuth(baseURL, "/api/auth/sign-in/email", { email, password: PASSWORD }, email);

export function pkce() {
  const verifier = randomBytes(32).toString("base64url");
  return { verifier, challenge: createHash("sha256").update(verifier).digest("base64url") };
}

type AuthorizeOptions = { resource?: string; scope?: string; clientId?: string; redirectUri?: string };

/** authorize + consent: the authorization code, or a thrown "error: description" from an error redirect. */
export async function authorize(account: Account, challenge: string, o: AuthorizeOptions = {}): Promise<string> {
  const redirectUri = o.redirectUri ?? LOOPBACK;
  const url = new URL("/api/auth/oauth2/authorize", account.baseURL);
  url.search = new URLSearchParams({
    client_id: o.clientId ?? "monoagent",
    response_type: "code",
    redirect_uri: redirectUri,
    scope: o.scope ?? MONOAGENT_SCOPES,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: randomBytes(8).toString("hex"),
    ...(o.resource ? { resource: o.resource } : {}),
  }).toString();

  const first = await fetch(url, { headers: { Cookie: account.cookie }, redirect: "manual" });
  // A client that is not a browser is told where to go as JSON.
  const json = first.status === 200 ? ((await first.json().catch(() => ({}))) as { url?: string }) : {};
  const location = first.headers.get("location") ?? json.url ?? "";
  const codeFrom = (target: string) => {
    const query = new URL(target, account.baseURL).searchParams;
    if (query.get("error")) throw new Error(`${query.get("error")}: ${query.get("error_description") ?? ""}`);
    return query.get("code") ?? "";
  };
  if (!location) throw new Error(`authorize: no redirect (${first.status})`);
  if (location.startsWith(redirectUri) || location.includes("error=")) return codeFrom(location);

  // The consent page posts the query string it was opened with.
  const consent = await fetch(new URL("/api/auth/oauth2/consent", account.baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: new URL(account.baseURL).origin, Cookie: account.cookie },
    body: JSON.stringify({ accept: true, oauth_query: new URL(location, account.baseURL).search.slice(1) }),
  });
  const { url: target } = (await consent.json()) as { url?: string };
  if (!consent.ok || !target) throw new Error(`consent failed: ${consent.status}`);
  return codeFrom(target);
}

async function tokenRequest(baseURL: string, form: Record<string, string>): Promise<Result> {
  const res = await fetch(new URL("/api/auth/oauth2/token", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form),
  });
  return { status: res.status, body: (await res.json().catch(() => ({}))) as TokenResponse };
}

export const exchange = (baseURL: string, code: string, verifier: string, resource?: string, redirectUri = LOOPBACK, clientId = "monoagent") =>
  tokenRequest(baseURL, { grant_type: "authorization_code", code, redirect_uri: redirectUri, client_id: clientId, code_verifier: verifier, ...(resource ? { resource } : {}) });

export const refresh = (baseURL: string, refreshToken: string, resource?: string) =>
  tokenRequest(baseURL, { grant_type: "refresh_token", refresh_token: refreshToken, client_id: "monoagent", ...(resource ? { resource } : {}) });

/** A full MonoAgent login. `resource` goes on the authorize and the token request, as the new client sends it. */
export async function login(baseURL: string, o: { resource?: string; scope?: string; account?: Account } = {}) {
  const account = o.account ?? (await signUp(baseURL));
  const { verifier, challenge } = pkce();
  const code = await authorize(account, challenge, { resource: o.resource, scope: o.scope });
  return { account, ...(await exchange(baseURL, code, verifier, o.resource)) };
}

export function decodeJwt(token: string) {
  const [header, payload] = token.split(".");
  const part = (p: string) => JSON.parse(Buffer.from(p, "base64url").toString("utf8")) as Record<string, unknown>;
  return { header: part(header), payload: part(payload) };
}

/** A boolean, so an assertion on it never prints the token when it fails. */
export const isJwt = (token: string | undefined) => token?.split(".").length === 3;

/** What the Go client does: Ed25519 over "header.payload" with the public key published at /api/auth/jwks. */
export async function verifiesAgainstJwks(baseURL: string, token: string): Promise<boolean> {
  const { keys } = (await (await fetch(new URL("/api/auth/jwks", baseURL))).json()) as { keys: { kid: string; kty: string; crv: string; x: string }[] };
  const key = keys.find((k) => k.kid === decodeJwt(token).header.kid);
  const [h, p, s] = token.split(".");
  const publicKey = key && createPublicKey({ key: { kty: key.kty, crv: key.crv, x: key.x }, format: "jwk" });
  return !!publicKey && verify(null, Buffer.from(`${h}.${p}`), publicKey, Buffer.from(s, "base64url"));
}

export const bearer = (token: string | undefined): Record<string, string> => ({ Authorization: `Bearer ${token}` });

// ---- the local D1, as tests/feature-voting.spec.ts reaches it ----

export async function withDb<T>(fn: (db: ReturnType<typeof drizzle>) => Promise<T>): Promise<T> {
  const { env, dispose } = await getPlatformProxy<CloudflareEnv>({ envFiles: [] });
  try {
    return await fn(drizzle(env.COMMUNITY_DB));
  } finally {
    await dispose();
  }
}

export const setUserRole = (userId: string, role: "admin" | "moderator") =>
  withDb((db) => db.update(user).set({ role, updatedAt: new Date() }).where(eq(user.id, userId)));

/** Seeds an email_claim_request row, as tests/oauth-claim.spec.ts does: no test can read the emailed code. */
export async function seedClaimRequest(o: { email: string; scope: string; code: string; clientId?: string }) {
  const codeHash = await sha256Base64Url(o.code);
  await withDb((db) =>
    db.insert(emailClaimRequest).values({
      id: crypto.randomUUID(),
      email: o.email,
      codeHash,
      clientId: o.clientId ?? "monoagent",
      scope: o.scope,
      attempts: 0,
      expiresAt: new Date(Date.now() + 10 * 60 * 1000),
      createdAt: new Date(),
    }),
  );
}
```

- [ ] **Step 4: Register the audience locally.** S6 needs the `resource` to exist. Task 3 adds it as a migration; here it is applied by hand and stays out of git. Create `scripts/spikes/seed-audience.sql`:

```sql
-- The audience a MonoAgent access token is bound to (RFC 8707 `resource`), and
-- the only OAuth client allowed to ask for it. The oauth-provider plugin
-- answers a `resource` that is not a row here with invalid_target and, with its
-- default enforcePerClientResources, one the client is not linked to.
-- allowed_scopes stays NULL on purpose: a non-null list narrows the scopes of
-- every token issued for the resource instead of only validating them.
INSERT OR IGNORE INTO `oauth_resource` (
	`id`, `identifier`, `name`, `access_token_ttl`, `refresh_token_ttl`,
	`signing_algorithm`, `signing_key_id`, `allowed_scopes`, `custom_claims`,
	`dpop_bound_access_tokens_required`, `disabled`, `created_at`, `updated_at`,
	`policy_version`, `metadata`
) VALUES (
	'monoagent-resource', 'https://monoes.me/api/monoagent', 'MonoAgent',
	NULL, NULL, NULL, NULL, NULL, NULL,
	0, 0, 1791206580558, 1791206580558,
	1, '{}'
);
INSERT OR IGNORE INTO `oauth_client_resource` (
	`id`, `client_id`, `resource_id`, `metadata`, `created_at`
) VALUES (
	'monoagent-client-resource', 'monoagent', 'https://monoes.me/api/monoagent',
	'{}', 1791206580558
);
```

```bash
npx wrangler d1 execute monoes-community --local --file scripts/spikes/seed-audience.sql
npx wrangler d1 execute monoes-community --local --command "SELECT identifier, name, allowed_scopes, disabled FROM oauth_resource"
```

Expected: `"success": true`, then one row: `https://monoes.me/api/monoagent`, `MonoAgent`, `allowed_scopes` null, `disabled` 0.

- [ ] **Step 5: S6, the shape of a real token.** Create `scripts/spikes/s6-claims.ts`:

```ts
// S6: what do real tokens look like, with and without `resource`?
//   npx tsx scripts/spikes/s6-claims.ts
import { AUDIENCE, decodeJwt, login, type Result } from "../../tests/helpers/oauth-api";

const base = process.env.SPIKE_BASE_URL ?? "http://localhost:3107";

// Structure only: claim names, shapes, the public identifiers. No token value is printed.
function describe(label: string, token: string | undefined) {
  if (!token?.includes(".")) return console.log(`${label}: ${token ? `opaque, ${token.length} characters` : "absent"}`);
  const { header, payload } = decodeJwt(token);
  const aud = payload.aud;
  console.log(`${label}: JWT header=${JSON.stringify(header)}`);
  console.log(`  iss=${payload.iss}  aud(${Array.isArray(aud) ? "array" : typeof aud})=${JSON.stringify(aud)}`);
  if (payload.azp) console.log(`  azp=${payload.azp} client_id=${payload.client_id} scope="${payload.scope}" plan=${payload.plan}`);
  console.log(`  exp-iat=${(payload.exp as number) - (payload.iat as number)} claims=${Object.keys(payload).sort().join(",")}`);
}

function show(title: string, r: Result) {
  console.log(`== ${title}: status ${r.status}${r.body.error ? ` ${r.body.error}: ${r.body.error_description}` : ""}, expires_in ${r.body.expires_in}, refresh token ${r.body.refresh_token ? "issued" : "absent"}`);
  describe("access", r.body.access_token);
  describe("id", r.body.id_token);
}

async function main() {
  show("no resource (today's client)", await login(base));
  show("resource sent on authorize and on the token request", await login(base, { resource: AUDIENCE }));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Run `npx tsx scripts/spikes/s6-claims.ts`. Expected on unmodified `main` (no token value is printed; the `kid` and port vary): the first login prints `access: opaque, 32 characters`; the second prints `access: JWT header={"typ":"at+jwt","alg":"EdDSA","kid":...}`, `aud(array)=["https://monoes.me/api/monoagent","http://localhost:3107/api/auth/oauth2/userinfo"]`, `azp=monoagent client_id=monoagent`, `exp-iat=3600` and claims `aud,azp,client_id,exp,iat,iss,jti,scope,sid,sub` (no `plan` yet).

- [ ] **Step 6: S2 and S3, refresh with a resource, rotation, blocking.** Create `scripts/spikes/s2-s3-refresh.ts`:

```ts
// S2 and S3: what does the refresh grant do with a `resource`, with rotation and replay,
// with a block, and with the end of a web session?
//   npx tsx scripts/spikes/s2-s3-refresh.ts
import { eq } from "drizzle-orm";
import { oauthRefreshToken, user } from "../../src/lib/db/schema";
import { AUDIENCE, authorize, exchange, login, pkce, refresh, signIn, withDb, type Account, type Result } from "../../tests/helpers/oauth-api";

const base = process.env.SPIKE_BASE_URL ?? "http://localhost:3107";
const origin = new URL(base).origin;

function line(label: string, r: Result) {
  const kind = r.body.access_token ? (r.body.access_token.includes(".") ? "JWT" : "opaque") : "-";
  console.log(`${label.padEnd(44)} ${r.status} ${r.body.error ?? "ok"} access=${kind}${r.body.error_description ? ` (${r.body.error_description})` : ""}`);
}

async function post(path: string, cookie: string, body: object = {}) {
  const res = await fetch(new URL(path, base), { method: "POST", headers: { "Content-Type": "application/json", Origin: origin, Cookie: cookie }, body: JSON.stringify(body) });
  return res.status;
}

const setBlocked = (id: string, on: boolean) => withDb((db) => db.update(user).set({ blockedAt: on ? new Date() : null }).where(eq(user.id, id)));
const refreshRows = (id: string) => withDb(async (db) => (await db.select({ id: oauthRefreshToken.id }).from(oauthRefreshToken).where(eq(oauthRefreshToken.userId, id))).length);

async function main() {
  console.log("== S2: a refresh token issued without a resource, exchanged with one ==");
  const a = await login(base);
  const bound = await refresh(base, a.body.refresh_token!, AUDIENCE);
  line("refresh(R1, resource)", bound);
  line("refresh(R1 again): the rotated token", await refresh(base, a.body.refresh_token!, AUDIENCE));
  line("refresh(R2): the replay killed the family", await refresh(base, bound.body.refresh_token!, AUDIENCE));
  const c = await login(base, { resource: AUDIENCE });
  line("a bound chain, refresh without resource", await refresh(base, c.body.refresh_token!));
  const d = await login(base);
  const d2 = await refresh(base, d.body.refresh_token!);
  line("a plain chain, refresh without resource", d2);
  line("the same chain, then with the resource", await refresh(base, d2.body.refresh_token!, AUDIENCE));
  line("another resource", await refresh(base, (await login(base)).body.refresh_token!, "https://example.com/other"));

  console.log("== S3: a block (blocked_at only, what the route does today) ==");
  const b = await login(base, { resource: AUDIENCE });
  const uid = b.account.userId;
  const before = await refreshRows(uid);
  await setBlocked(uid, true);
  console.log(`refresh-token rows for the user: ${before} before the block, ${await refreshRows(uid)} after`);
  line("refresh(blocked user, resource)", await refresh(base, b.body.refresh_token!, AUDIENCE));
  console.log(`new web sign-in: ${(await signIn(base, b.account.email)).status}`);
  const p = pkce();
  try {
    line("authorize through the old web session", await exchange(base, await authorize(b.account, p.challenge, { resource: AUDIENCE }), p.verifier, AUDIENCE));
  } catch (err) {
    console.log(`authorize through the old web session: ${(err as Error).message}`);
  }
  await setBlocked(uid, false);

  console.log("== S3: does ending the monoes.me web session end a MonoAgent refresh token? ==");
  const e = await login(base, { resource: AUDIENCE });
  console.log(`sign-out: ${await post("/api/auth/sign-out", e.account.cookie)}`);
  line("refresh after sign-out", await refresh(base, e.body.refresh_token!, AUDIENCE));
  const f = await login(base, { resource: AUDIENCE });
  const second = (await signIn(base, f.account.email)).account as Account;
  console.log(`revoke-sessions: ${await post("/api/auth/revoke-sessions", second.cookie)}`);
  line("refresh after revoke-sessions", await refresh(base, f.body.refresh_token!, AUDIENCE));
  const g = await login(base, { resource: AUDIENCE });
  console.log(`change-password (revokeOtherSessions): ${await post("/api/auth/change-password", g.account.cookie, { currentPassword: "TestPass1234", newPassword: "TestPass5678", revokeOtherSessions: true })}`);
  line("refresh after change-password", await refresh(base, g.body.refresh_token!, AUDIENCE));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Run `npx tsx scripts/spikes/s2-s3-refresh.ts`. Expected on unmodified `main`, the lines that matter:

```
refresh(R1, resource)                        200 ok access=JWT
refresh(R1 again): the rotated token         400 invalid_grant access=- (invalid refresh token)
refresh(R2): the replay killed the family    400 invalid_grant access=- (session not found)
a plain chain, refresh without resource      200 ok access=opaque
another resource                             400 invalid_target access=- (requested resource https://example.com/other is not configured)
refresh-token rows for the user: 1 before the block, 1 after
refresh(blocked user, resource)              200 ok access=JWT
new web sign-in: 401
authorize through the old web session        200 ok access=JWT
refresh after sign-out / revoke-sessions / change-password   200 ok access=JWT (each)
```

- [ ] **Step 6b: S7, what the family of a refresh token is (ruling R1 of 2026-10-07).** R1 adopts refresh-token families: a replay of a rotated-away refresh token after the reuse window must end only the family of the reused token (the sign-in it comes from), not every MonoAgent refresh token of the account. This step measures and changes nothing; the lead writes Task 3's steps for the family from what it finds, before Task 3 is dispatched.

(a) Read how the installed provider revokes today:

```bash
grep -rn "invalidateRefreshFamily" node_modules/@better-auth/oauth-provider/dist/
grep -rn "async function createRefreshToken\|authorizationCodeId: refreshToken.authorizationCodeId\|sessionId: refreshToken.sessionId\|async function revokeRefreshToken" node_modules/@better-auth/oauth-provider/dist/
grep -n "oauth_refresh_token" -A 30 src/lib/db/schema.ts
```

Expected for 1.7.1 (read while planning; the file names carry a build hash): the definition `async function invalidateRefreshFamily(ctx, clientId, userId)`, which deletes every `oauthRefreshToken` row of that client and user and the `oauthAccessToken` rows that point at them through `refreshId`; and three calls: one in the refresh grant (a revoked token presented once `isWithinRefreshTokenReuseInterval` is false) and two in `revokeRefreshToken` (the revoke route, for a token that is already revoked, and for one a concurrent call revoked first). The refresh grant hands the presented token's `referenceId`, `authorizationCodeId` and `sessionId` on to the token it issues. The table has no column named for a family: `session_id` (a foreign key with `ON DELETE SET NULL`), `authorization_code_id`, `reference_id`, `revoked`, `rotated_at`. Write the file names and line numbers of every match into the findings.

(b) Measure two sign-ins of one account. Create `scripts/spikes/s7-family.ts`:

```ts
// S7 (ruling R1 of 2026-10-07): what names the family of a refresh token, and what does a replay
// after the reuse window end today?
//   npx tsx scripts/spikes/s7-family.ts
import { createHash } from "node:crypto";
import { eq } from "drizzle-orm";
import { oauthRefreshToken } from "../../src/lib/db/schema";
import { AUDIENCE, login, refresh, withDb, type Result } from "../../tests/helpers/oauth-api";

const base = process.env.SPIKE_BASE_URL ?? "http://localhost:3107";
const origin = new URL(base).origin;

// A short fingerprint of an identifier: equal values show as equal, and no value is printed.
const tag = (v: string | null | undefined) => (v ? createHash("sha256").update(v).digest("hex").slice(0, 8) : "null");

const line = (label: string, r: Result) => console.log(`${label.padEnd(48)} ${r.status} ${r.body.error ?? "ok"}`);

// Every refresh-token row of the user, with the columns that could name a family.
async function rows(userId: string, label: string) {
  const all = await withDb(async (db) =>
    await db
      .select({
        id: oauthRefreshToken.id,
        revoked: oauthRefreshToken.revoked,
        rotatedAt: oauthRefreshToken.rotatedAt,
        sessionId: oauthRefreshToken.sessionId,
        authorizationCodeId: oauthRefreshToken.authorizationCodeId,
        referenceId: oauthRefreshToken.referenceId,
      })
      .from(oauthRefreshToken)
      .where(eq(oauthRefreshToken.userId, userId)),
  );
  console.log(`== ${label}: ${all.length} refresh-token rows`);
  for (const r of all) {
    console.log(`  row ${tag(r.id)} revoked=${r.revoked ? "yes" : "no"} rotated=${r.rotatedAt ? "yes" : "no"} session=${tag(r.sessionId)} code=${tag(r.authorizationCodeId)} reference=${tag(r.referenceId)}`);
  }
}

async function main() {
  // One account, two sign-ins: two installs, two chains, each rotated once.
  const a = await login(base, { resource: AUDIENCE });
  const b = await login(base, { resource: AUDIENCE, account: a.account });
  const uid = a.account.userId;
  line("rotate A (A1 -> A2)", await refresh(base, a.body.refresh_token!, AUDIENCE));
  const b2 = await refresh(base, b.body.refresh_token!, AUDIENCE);
  line("rotate B (B1 -> B2)", b2);
  await rows(uid, "two chains, each rotated once");

  // An older token issued without a resource, exchanged with one (D23 adoption, S2).
  const c = await login(base, { account: a.account });
  line("exchange C1 (issued without a resource)", await refresh(base, c.body.refresh_token!, AUDIENCE));
  await rows(uid, "after the exchange of a token issued without a resource");

  // The end of the web session must not end a family (S3).
  const out = await fetch(new URL("/api/auth/sign-out", base), {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: origin, Cookie: a.account.cookie },
    body: "{}",
  });
  console.log(`sign-out: ${out.status}`);
  await rows(uid, "after the web session ended");

  // A replay of A1 after the window (the unmodified server's window is 0 seconds).
  line("replay A1", await refresh(base, a.body.refresh_token!, AUDIENCE));
  await rows(uid, "after the replay of A1");
  line("B2, the other sign-in", await refresh(base, b2.body.refresh_token!, AUDIENCE));

  // The revoke route, given a rotated token.
  const d = await login(base, { resource: AUDIENCE });
  const e = await login(base, { resource: AUDIENCE, account: d.account });
  await refresh(base, d.body.refresh_token!, AUDIENCE);
  const revoke = await fetch(new URL("/api/auth/oauth2/revoke", base), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ token: d.body.refresh_token!, client_id: "monoagent" }),
  });
  console.log(`revoke D1 (rotated): ${revoke.status}`);
  await rows(d.account.userId, "after the revoke of a rotated token");
  line("E1, the other sign-in", await refresh(base, e.body.refresh_token!, AUDIENCE));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Run `npx tsx scripts/spikes/s7-family.ts`. What the unmodified server should print, from S2, S3 and the reading in (a); the run decides every line, and which columns are equal inside a chain is exactly what it is for: 4 rows after the two rotations (each sign-in's first token `revoked=yes rotated=yes`, its successor `no no`), 6 after the exchange, still 6 after the sign-out (S3), then `replay A1 ... 400 invalid_grant`, 0 rows, and `B2, the other sign-in ... 400 invalid_grant` (S2: today a replay ends every MonoAgent refresh token of the account); for the revoke route, 0 rows and `E1, the other sign-in ... 400 invalid_grant` if `revokeRefreshToken` behaves as (a) reads.

(c) The questions S7 answers, each in the findings with the evidence (a line of (a) or of the run):
1. Which column, if any, holds one value for every token of a chain (a sign-in's first refresh token and each token rotated from it) and a different value for another sign-in of the same user and client: `authorization_code_id`, `session_id`, `reference_id`, or none?
2. Is that value set on every path that issues a MonoAgent refresh token: the authorization-code exchange, the refresh grant (carried from the presented token), the exchange of an older token issued without a `resource` (D23 adoption, S2), and the email-code route of Task 7 (not merged yet: read `src/app/api/auth/agent/claim/verify/route.ts` and the provider function Task 7 makes it call, and say what that path would store)?
3. Does it survive what must not end a family: a rotation, and the end of the web session (sign-out, revoke-sessions, change-password: S3 showed that the refresh tokens survive, and `session_id` is set to null when the session row goes)?
4. What does a replay after the window end today, and on which paths: the refresh grant and the revoke route (`revokeRefreshToken`)? How many rows of the other sign-in survive (none, if (a) reads right)?
5. What is the smallest change that ends only the reused token's family on both paths, and what does each candidate cost? The candidates: an option or hook of the provider 1.7.1, if it has one (`grep -n "invalidateRefreshFamily\|Reuse\|hooks" node_modules/@better-auth/oauth-provider/dist/index.d.mts`); a wrapper in front of `/oauth2/token` and `/oauth2/revoke` (a Better-Auth `hooks.before`) that finds a revoked token presented after its window, deletes only its family and answers `invalid_grant` before the provider sees it; a family column, that is a new column and a migration (an owner step like O2), written when a sign-in issues its first refresh token and copied at each rotation; a patch of the installed package (`patch-package`), the last resort. For each: does it survive a provider upgrade, does it need a migration, and does it keep Task 3's 300-second reuse window answering a retry with the first answer?
6. After the change, what becomes of the access tokens of the ended family (their `refresh_id`) and of the other families' (untouched)? And does Task 3's "a dead token is punished once" still hold: the reused token's own row goes with its family, so a later presentation finds no row and ends nothing more?

- [ ] **Step 7: S1, which key signs and can it be pinned.** Create `scripts/spikes/s1-key.ts`:

```ts
// S1: which key signs, where does it live, and can a verifier that has only the
// public key check the signature? Run it before and after pinning a key:
//   npx tsx scripts/spikes/s1-key.ts [expected-kid]
// With an expected kid it also asserts the tokens and the JWKS carry exactly that key.
import { count } from "drizzle-orm";
import { jwks } from "../../src/lib/db/schema";
import { AUDIENCE, decodeJwt, login, refresh, verifiesAgainstJwks, withDb } from "../../tests/helpers/oauth-api";

const base = process.env.SPIKE_BASE_URL ?? "http://localhost:3107";
const jwksRows = () => withDb(async (db) => (await db.select({ n: count() }).from(jwks))[0].n);

async function main() {
  const expected = process.argv[2];
  const rowsBefore = await jwksRows();
  const { keys } = (await (await fetch(new URL("/api/auth/jwks", base))).json()) as { keys: { kid: string; kty: string; crv: string; alg: string }[] };
  console.log(`JWKS: ${keys.map((k) => `${k.kid} ${k.kty}/${k.crv} ${k.alg}`).join("; ")}`);

  const r = await login(base, { resource: AUDIENCE });
  const { header } = decodeJwt(r.body.access_token!);
  console.log(`access token: alg=${header.alg} typ=${header.typ} kid=${header.kid}`);
  console.log(`verifies with only the published public key (node:crypto, Ed25519): ${await verifiesAgainstJwks(base, r.body.access_token!)}`);
  console.log(`id token signed by the same key: ${decodeJwt(r.body.id_token!).header.kid === header.kid}`);
  const again = await refresh(base, r.body.refresh_token!);
  console.log(`after a refresh: ${again.status}, kid ${decodeJwt(again.body.access_token!).header.kid}`);
  console.log(`jwks table rows: ${rowsBefore} before, ${await jwksRows()} after (a row appears only when the plugin generates a key)`);

  if (expected) {
    const ok = header.kid === expected && keys.length >= 1 && keys[0].kid === expected;
    console.log(`PINNED KEY CHECK: ${ok ? "PASS" : "FAIL"} (expected ${expected})`);
    process.exit(ok ? 0 : 1);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Run `npx tsx scripts/spikes/s1-key.ts`. Expected on unmodified `main`: one JWKS key `<32-character kid> OKP/Ed25519 EdDSA`; the access token's `kid` is the same; `verifies with only the published public key (node:crypto, Ed25519): true`; `id token signed by the same key: true`; `after a refresh: 200`; `jwks table rows: 1 before, 1 after` (`0 before, 1 after` on a database that has not signed anything yet).

Check the options Task 6 relies on exist in the installed plugin: `grep -n "getJwks?:\|createJwk?:\|disablePrivateKeyEncryption?:\|disableSettingJwtHeader?:" node_modules/better-auth/dist/plugins/jwt/types.d.mts`. Expected: four matches (lines 68, 177, 199, 209). Four matches is the precondition of Task 6; fewer means use its fallback.

- [ ] **Step 8: Write the findings.** Create the findings file named in **Files**, with this exact structure. Where a run printed something different from a line below, the run wins: correct the line and say what differed under that spike's Result.

````markdown
# Mandatory monoes.me Account — Spike Findings (S1, S2, S3, S6, S7)

Date: 2026-10-05 (the planning run; Task 2 re-runs every script and re-dates this file).
Written by plan A, Task 2. Later plans append their own spikes (S4, S5) under their own headings.
Server: `monoes/monoes-landing` at `main` (better-auth 1.7.1, @better-auth/oauth-provider 1.7.1), run locally: `next dev`, local D1, real HTTP flows. Production was read twice, by public GETs: `/api/auth/.well-known/oauth-authorization-server` and `/api/auth/jwks`.

## Constants

B1a updates `internal/account/claims.go` from this table. Every value equals the frozen value in the plan index §3.2, so `claims.go` needs no change.

| Fact | Observed | Frozen constant |
|---|---|---|
| Issuer `iss` | `https://monoes.me/api/auth`: the Better-Auth base URL plus `/api/auth`. Production's discovery document says the same. | `Issuer` |
| Audience `aud` | An array `["https://monoes.me/api/monoagent", "<issuer>/oauth2/userinfo"]`, because MonoAgent requests `openid`; a plain string for a request without `openid`. Test membership. | `Audience` |
| Client claim | `azp` and `client_id` are both `monoagent`. | `ClientID` |
| Header | `alg` `EdDSA` (Ed25519), `typ` `at+jwt`, `kid`. | one accepted algorithm |
| `kid` | Pinned mode: the RFC 7638 thumbprint of the public key (43 characters). A plugin-generated key: a random 32-character id (production today: `GB6kESA9qO98637VArEGR2EjW6wSyYqO`). | `keys_default.go` |
| Lifetime | `exp - iat` is 3600; `expires_in` is 3600. | `MaxTokenLife` is only an upper bound |
| Claims present | `aud azp client_id exp iat iss jti plan scope sid sub`. `sid` only on tokens from the browser flow. `plan` is `free`. | |
| JWT or opaque | A JWT exactly when a `resource` is sent (on authorize and token, or on any refresh); otherwise opaque (32 letters), `expires_in` 3600. | |
| Refresh token | Opaque. Rotates on every refresh. Expires 30 days after it was issued, so every rotation slides the expiry. A retry of the same request within 300 seconds of the rotation is answered again with the same response (Task 3's `refreshTokenReuseInterval`); the unmodified server has no window. The client retries a lost answer at once and for as long as 240 seconds after the first send (spec A24, `pendingRetryWindow`), never after. | |
| Refresh-token family | S7 writes this row: the column that names a family (or the one Task 3 adds), set on every path that issues a MonoAgent refresh token. A replay of a rotated-away token after the reuse window ends only that family (ruling R1); unmodified, the provider ends every refresh token of the client and the user. | none: the client keeps A24 and A25 as defence in depth and changes no constant |
| Same key, not access tokens | The ID token (`aud` `monoagent`, the access tokens' `iss`, 10 hours, no `typ`) and the session JWT of `GET /api/auth/token` (`iss` and `aud` are `https://monoes.me`, no `azp`, no `typ`) carry the same `kid`. | B1a keeps the issuer, audience and client checks: they tell these apart, the audience by design (an ID token's `aud` is the client id, and OIDC lets it carry `azp`); Task 3's spec pins both audiences. Only access tokens carry `typ: at+jwt`, so after this spike a `typ` check in the client's verifier is possible defense in depth; B1a does not make it |

## S6: what does a real token look like?

Method: `scripts/spikes/s6-claims.ts`, a full PKCE login with and without `resource`.
Result: the table above. Without `resource` the access token is opaque and the response is what it always was.
Consequence: no constant changes. The verifier must accept `aud` as an array.

## S1: can the `jwt()` plugin sign with a configured key, or can its key be pinned?

Method: read the plugin (`dist/plugins/jwt/types.d.mts`, `sign.mjs`, `index.mjs`); `scripts/spikes/s1-key.ts` on the unmodified server; Task 6 runs it again with a pinned key.
Result:
- Default: one Ed25519 key, one `jwks` row (id is the `kid`; `expires_at` is null because `rotationInterval` is unset), created lazily at the first signing or `/jwks` request, private half encrypted with `BETTER_AUTH_SECRET`, published at `/api/auth/jwks`. A verifier holding only the public JWK checks a token (`node:crypto`, Ed25519: true). The generated key can be exported and pinned.
- The plugin signs with whatever `adapter.getJwks` returns (its newest live key) and `/jwks` publishes it; `adapter.createJwk` may refuse; `jwks.disablePrivateKeyEncryption` lets the private half come from a secret. A custom `jwt.sign` needs `jwks.remoteUrl`, which disables the plugin's `/jwks` and makes the provider fetch its own keys over HTTP: not used.
- The plugin also signs a session JWT on every `get-session` (the `set-auth-jwt` header) unless `disableSettingJwtHeader` is set; nothing in the repository reads it.
Consequence: primary, a key from the secret `MONOAGENT_JWT_PRIVATE_JWK` through `adapter` (Task 6), plus an optional `MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK` during a rotation. Fallback, pin the existing database key; it cannot do spec §4.7 step 1, because the next key is unknown until the server creates it. `disableSettingJwtHeader: true` limits the key's blast radius to the token endpoint.

## S2: can a refresh token issued without `resource` be exchanged with one?

Method: `scripts/spikes/s2-s3-refresh.ts`, section S2.
Result: yes. 200 and an audience-bound JWT, a rotated refresh token, and the audience then sticks (a later refresh without `resource` stays a JWT). A chain with no `resource` stays opaque. Another resource: `invalid_target`. Presenting a rotated token again: `invalid_grant`, and the provider then deletes every MonoAgent refresh token of that account (the newer one answers `session not found`). That is the unmodified server, whose reuse interval is 0; Task 3 sets 300 seconds.
Consequence: D23 adoption needs no new sign-in. Adoption must delete the library vault's copy of the refresh token after the exchange: any later use of the old copy ends the account's sessions on every machine, once the 300-second reuse window of Task 3 has passed (inside it the provider answers the same request again with the same response, which is what covers a lost answer or a restart). The same holds for a copied session file and for a crash between the server's rotation and the client's write: spec §4.4's "the user signs in again (accepted)" means every install of the account is locked as refused until each signs in. `account logout` revokes one token (`/oauth2/revoke`, 200) and leaves the others alone, until the revoked token is presented again.

## S3: does the refresh grant refuse a blocked user, and does the provider rotate?

Method: `scripts/spikes/s2-s3-refresh.ts`, section S3 (block by SQL, which is all the route did).
Result, before Task 4: the provider rotates refresh tokens, and the refresh grant never looks at the user. A block deletes nothing (1 refresh row before, 1 after); the refresh of a blocked user answers 200 with a fresh JWT; a new web sign-in is refused (401); a web session that was still alive authorizes and gets fresh tokens (200). Ending the web session (sign-out, revoke-sessions, change-password) does not touch MonoAgent refresh tokens (200 each), so signing out of monoes.me never locks an install.
Result, after Task 4 (verified by its specs): the block deletes the user's tokens and web sessions, and the refresh answers `invalid_grant`; so does an audience-bound grant for a blocked user whose tokens survived. Grants without a `resource` are covered by the deletion only.
Consequence: only a block, `/oauth2/revoke`, 30 days without use, or a replay ends a MonoAgent refresh token. A machine offline for more than 30 days gets `invalid_grant`: locked as refused, not as unreachable.

## S7: what is the family of a refresh token? (ruling R1 of 2026-10-07)

Method: the reading of Task 2 step 6b (a), with the file names and line numbers it printed, and `scripts/spikes/s7-family.ts`.
Result: the answers to the six questions of step 6b (c), in their order, each with its evidence: (1) the column that names a family, or none; (2) on which of the four paths it is set; (3) whether it survives a rotation and the end of the web session; (4) what a replay after the window ends today, on the refresh grant and on the revoke route, with the row counts the run printed; (5) the smallest change that ends only the reused token's family on both paths, with what each candidate costs; (6) what becomes of the access tokens, and whether a dead token is still punished once.
Consequence: the family key and the change Task 3 makes. The lead writes Task 3's family steps from this section before Task 3 is dispatched.

## Decisions taken (the lead, 2026-10-05; the fourth by ruling R1 of 2026-10-07)

1. `refreshTokenReuseInterval: 300` (Task 3), the owner's to lower, together with the client's `pendingRetryWindow`. The client retries a lost response for as long as 240 seconds after the first send (spec A24), so a 60-second window would leave the later retries presenting a rotated token and ending every session of the account. Inside the window a replay of a used token, a thief's included, gets the same response; it does not cover an explicitly revoked token (`/oauth2/revoke`, a block), which answers `invalid_grant` at once.
2. The email-code route (Task 7) is the one server change beyond spec §5. For the MonoAgent client's `offline_access` claim it returns a `refresh_token`, which the client trades at the token endpoint with `resource` (B1b's design), and it honors a `resource` in the verify body (one call, the token endpoint's own answer). Without `resource` the opaque access token is unchanged; a blocked account is refused.
3. Extras approved: `disableSettingJwtHeader`; deleting a blocked account's web sessions; the optional previous-key secret for a rotation; the audience as a database row plus a client link (migration 0017). Spec §5 item 1 says `oauthProvider` "accepts the resource"; it is a row and a link, not an option.
4. Refresh-token families (ruling R1 of 2026-10-07): a replay of a rotated-away refresh token after the reuse window ends only that token's family, not every MonoAgent refresh token of the account, which removes the 5xx trade-off of the client's A24 and shrinks each of its residuals to "that install signs itself out". S7 measures what names a family; the lead writes Task 3's steps for it from S7's findings before Task 3 is dispatched. The client keeps A24 and A25 as defence in depth and changes no constant until the server has shipped and been measured.
````

- [ ] **Step 9: Check what is left behind.** `git status --short` shows `?? tests/helpers/oauth-api.ts` and, if `next dev` or `tsc` ran, the two tracked files they rewrite (`next-env.d.ts`, `tsconfig.tsbuildinfo`): restore them with `git checkout -- next-env.d.ts tsconfig.tsbuildinfo`. The excluded `scripts/spikes/` and `.env.local` do not appear. Nothing is committed or pushed. Leave the server running for the next tasks.

### Task 3: Audience-bound JWTs, the `plan` claim, the refresh reuse window and refresh-token families

**Refresh-token families (ruling R1 of 2026-10-07).** Requirement: a refresh token presented again after the provider rotated it away and its reuse window passed, or after it was revoked, ends only that token's family (the sign-in it comes from), not every MonoAgent refresh token of the account; the other sign-ins of the account, every other install, keep working. Spike S7 (Task 2) found what names a family and the smallest change; steps 11 to 17 make it, with no migration and no owner step, so the server change ships with this task. The client keeps A24 and A25 as defence in depth and changes no constant until the owner has measured the deployed server.

**Files:**
- Create: `drizzle/0017_monoagent_audience.sql`, `drizzle/meta/0017_snapshot.json` (generated), `src/lib/monoagent-token.ts`, `src/lib/monoagent-token.test.ts`, `src/lib/access-token-claims.ts`, `src/lib/access-token-claims.test.ts`, `src/lib/refresh-family.ts`, `src/lib/refresh-family.test.ts`, `tests/account-gate-tokens.spec.ts`, `tests/account-gate-family.spec.ts`, `tests/helpers/oauth-api.ts` (written in Task 2)
- Modify: `drizzle/meta/_journal.json` (one entry, generated), `src/lib/auth.ts` (imports, lines 1-7; the `oauthProvider({` options, lines 73-79; a `hooks` option after the `plugins` array, step 14)

**Interfaces:** Produces `MONOAGENT_AUDIENCE`, `MONOAGENT_CLIENT_ID`, `authIssuer(): string` (`src/lib/monoagent-token.ts`); `PLAN_FREE`, `accessTokenClaims(): { plan: string }` (`src/lib/access-token-claims.ts`; Task 4 gives it the user); a registered `resource` row and its link to the `monoagent` client; `endReplayedFamily(db: Db, ctx)`, the before hook, built from `refreshTokenOf`, `revokedTokenOf`, `tokenRouteActs`, `revokeCaller` and `endFamily` (`src/lib/refresh-family.ts`). Consumes the `oauthProvider` options `accessTokenExpiresIn`, `refreshTokenReuseInterval` and `customAccessTokenClaims`; Better-Auth's `hooks.before`, `createAuthMiddleware` and `APIError` (`better-auth/api`); `stripAccessTokenAuthorizationScheme` (`better-auth/oauth2`, what the provider's revoke route strips the token with); `sha256Base64Url`, which equals the provider's stored token hash (S7).

**Design.** The provider mints a JWT access token exactly when the request carries a `resource`. With its default `enforcePerClientResources` the resource must be a row of `oauth_resource` and the client must be linked to it in `oauth_client_resource`; the audience is therefore a migration, not an option (S6 showed `invalid_target ... is not configured` without it). The link is what keeps a dynamically registered client, which anyone can create, from obtaining the audience. `allowed_scopes` stays NULL: a list would narrow every token's scopes.

`refreshTokenReuseInterval: 300` (the owner's value; the provider's default is 0). A client whose refresh answer was lost presents the token again, at once and for as long as 240 seconds after the first send (mono-agent's `pendingRetryWindow`, spec A24), and that token is one the provider has already rotated: with no window the retry is a replay, which the unmodified provider punishes by deleting every MonoAgent refresh token of the account, locking every install (S2), and the family hook below by ending that install's sign-in. Inside the window the provider answers the same request again with the same response it gave the first time; the trade-off is that a replay of a used token inside those five minutes, a thief's included, gets that same response. The client's 240 seconds depend on this number and the two change together: the owner may lower it only together with `pendingRetryWindow`, which must stay below it (the 60 seconds between them cover the length of the call and clocks that run at different rates). It does not cover a token that was explicitly revoked (`/oauth2/revoke`, a block), which answers `invalid_grant` at once, and after the window a replay ends the replayed token's family (below).

**Refresh-token families (S7).** The table has no family column, but `oauth_refresh_token.authorization_code_id` is one: the stored SHA-256 of the authorization code a sign-in started from, written by the code exchange, copied at every rotation and by D23's exchange of an older token, kept when the web session ends (sign-out, revoke-sessions, change-password), and different for every sign-in. `session_id` is not one: several sign-ins share a web session, and it becomes null when the session ends. Unmodified, the provider's `invalidateRefreshFamily(ctx, clientId, userId)` deletes every refresh token of the client and the user, and their opaque access tokens: from the refresh grant (a revoked token presented outside its window) and from the revoke route (any revoked token, with no window, and a live one whose compare-and-set it loses). S7 measured 6 rows to 0 on both paths, the other sign-in then `invalid_grant`. No provider option changes that, so `getAuth` gets a Better-Auth `hooks.before` (`src/lib/refresh-family.ts`) that follows the findings' "Corrected S7 answer 5" (the review of the spike, `2e5ad3bd` and `56d02843`: a hook keyed on the plain request misses the shapes the provider reads differently). It runs before every endpoint and returns at once for every other path.
- On `/oauth2/token`, for a refresh grant as the provider reads it (its schema trims `grant_type`; it hashes `refresh_token` as sent), the hook decides on the stored row alone and never on the request's `client_id`, which the provider reads from the raw form (`client_id=monoagent&client_id=` is `monoagent` to it and empty to `ctx.body`). It acts when the row is a revoked, unexpired `monoagent` token outside the reuse window, the window judged 10 seconds early: the hook and the provider read the clock at different moments, and mono-agent never retries after 240 seconds. Acting is: end the family, then answer the provider's own `invalid_grant` (`invalid refresh token`). Otherwise the hook returns and the provider answers as before; inside the window that is the stored answer, so the 300-second retry is untouched. The provider's other checks (resources, scopes, client validation) are not mirrored, which can only end a dead token's family where the provider would have ended nothing.
- On `/oauth2/revoke`, unless the hint is exactly `access_token` (the provider reads any other hint as none), the hook reads the token as the provider does (trimmed, a `Bearer` or `DPoP` scheme stripped: `stripAccessTokenAuthorizationScheme`), finds its row, then reads the caller as the provider does: the single non-empty `client_id` of the raw request text (`new URLSearchParams(await ctx.request.clone().text())`; two or more are refused), never `ctx.body`, which better-call builds differently for a repeated field, a `+json` media type or a leading U+FEFF; only an `auth.api` call, which has no request, is read from its body. For a `monoagent` caller the hook revokes the token itself, so the provider's `revokeRefreshToken` never runs for MonoAgent: a `monoagent` token, live or revoked, inside the window or not, expired or not, ends its family, and the answer is the provider's (200 with an empty body for a live token, a truthy `Response` that ends the pipeline; 400 `invalid_request` with `token not found`, or `refresh token revoked` under that hint, for a revoked one). Deleting instead of marking `revoked` leaves no revoked row behind, so a refresh racing a logout finds no row, or loses its compare-and-set, and ends nothing. Another client's token presented as `monoagent`'s, which the provider punishes by ending every MonoAgent token of its user, ends nothing.
- Ending a family: in one `db.batch` (one D1 transaction), the family's opaque access tokens (by `refresh_id`, which also catches Task 7's access row), then its refresh tokens, the presented one included; then the refresh-token delete once more. A row without a key ends alone, because a filter on a null `authorization_code_id` matches nothing in SQL (`= NULL`) and every keyless chain through the provider's adapter (`IS NULL`); the provider writes a key on every path, Task 7 writes `email-claim:<claim id>`, and step 19 counts keyless rows in production. JWT access tokens are not rows: those of an ended family stay valid until `exp`, at most an hour, as before. Every other client keeps the provider's behavior.

A dead token is punished once. Its own row goes with its family, so a later presentation finds no row and answers `invalid_grant` (`session not found`) without touching anything, and whatever a machine presents can end only the sign-in that token belongs to. A client whose disk is full cannot save its `refused` marker (spec A21) and presents the same token at every due command: the sign-ins made before and since survive, which the family spec pins.

Accepted residuals (Review Focus 1; the findings' S7 residuals). (1) A rotation that races the delete: a refresh whose compare-and-set won just before the batch inserts its successor just after it, a live token in an ended family (the provider's own deletes have the same gap, its TODO `invalidate-family-race`); the second sweep narrows it, nothing closes it. (2) A request whose twin is still in flight: a retry inside the window that reads the row after the first request's rotation but before its answer is stored gets `invalid_grant`, and so does the loser of two refreshes of one token at the same moment (S7, 3 of 3 trials) or of a logout racing a refresh. No family ends, but mono-agent reads a refusal: the B plans serialise refresh and logout per install and never retry a request that may still be in flight. (3) The window's edge is closed only while the hook's and the provider's reads of the clock are less than 10 seconds apart. (4) The hook mirrors the provider's internals (the default token hash, the column names, the window rule, the request reading above): an upgrade that changes them can bring back the account-wide delete without failing CI, which runs only the unit tests, so a pull request that upgrades `@better-auth/oauth-provider` runs `tests/account-gate-family.spec.ts` before it merges. The U+FEFF shape was measured on Node (`next dev`) only, not on workerd, which serves production; the hook reads the raw text exactly as the provider does, in the same runtime.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-audience origin/main`.

- [ ] **Step 2: Write the failing unit tests.** `src/lib/monoagent-token.test.ts`:

```ts
import { describe, it, afterEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { MONOAGENT_AUDIENCE, MONOAGENT_CLIENT_ID, authIssuer } from "./monoagent-token.ts";

const original = process.env.BETTER_AUTH_URL;
afterEach(() => {
  if (original === undefined) delete process.env.BETTER_AUTH_URL;
  else process.env.BETTER_AUTH_URL = original;
});

describe("the MonoAgent token constants", () => {
  it("are the values mono-agent pins", () => {
    assert.equal(MONOAGENT_AUDIENCE, "https://monoes.me/api/monoagent");
    assert.equal(MONOAGENT_CLIENT_ID, "monoagent");
  });

  it("make the issuer the Better-Auth base URL plus its base path", () => {
    process.env.BETTER_AUTH_URL = "https://monoes.me";
    assert.equal(authIssuer(), "https://monoes.me/api/auth");
    delete process.env.BETTER_AUTH_URL;
    assert.equal(authIssuer(), "http://localhost:3000/api/auth");
  });
});

describe("migration 0017_monoagent_audience.sql", () => {
  const read = () => readFileSync(new URL("../../drizzle/0017_monoagent_audience.sql", import.meta.url), "utf8");

  it("registers exactly the audience the code verifies, and links only the monoagent client to it", () => {
    const sql = read();
    assert.ok(sql.includes(`'${MONOAGENT_AUDIENCE}'`));
    assert.match(sql, /INSERT OR IGNORE INTO `oauth_resource`/);
    assert.match(sql, /INSERT OR IGNORE INTO `oauth_client_resource`[\s\S]*'monoagent'/);
    assert.equal(sql.match(/INSERT OR IGNORE INTO `oauth_client_resource`/g)?.length, 1);
  });

  it("leaves allowed_scopes NULL, because a list would narrow every token's scopes", () => {
    const sql = read();
    const resourceInsert = sql.slice(sql.indexOf("INSERT OR IGNORE INTO `oauth_resource`"), sql.indexOf("--> statement-breakpoint"));
    const values = resourceInsert.slice(resourceInsert.indexOf("VALUES")).replace(/\s+/g, " ");
    assert.match(values, /'MonoAgent', NULL, NULL, NULL, NULL, NULL, NULL, 0, 0,/);
  });
});

describe("the refresh-token reuse window", () => {
  // mono-agent presents a refresh token whose answer was lost again, at once, for as long as 240 seconds after the
  // first send (pendingRetryWindow in internal/account/guard.go, spec A24), and the provider answers that repeat only
  // inside this window. The two numbers change together, and the client's must stay below this one.
  it("is the 300 seconds the owner approved: the client's 240-second retry window (A24) sits inside it", () => {
    const auth = readFileSync(new URL("./auth.ts", import.meta.url), "utf8");
    assert.ok(/refreshTokenReuseInterval: 300,/.test(auth), "src/lib/auth.ts sets refreshTokenReuseInterval: 300");
  });
});
```

`src/lib/access-token-claims.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { accessTokenClaims, PLAN_FREE } from "./access-token-claims.ts";

describe("accessTokenClaims", () => {
  it("puts everyone on the free plan until plans exist", () => {
    assert.equal(PLAN_FREE, "free");
    assert.deepEqual(accessTokenClaims(), { plan: "free" });
  });
});
```

- [ ] **Step 3: Run them.** `node --experimental-strip-types --test src/lib/monoagent-token.test.ts src/lib/access-token-claims.test.ts`. Expected: FAIL, `Error [ERR_MODULE_NOT_FOUND]: Cannot find module '<repo>/src/lib/monoagent-token.ts'` (and the same for `access-token-claims.ts`), `ℹ pass 0`.

- [ ] **Step 4: Create the two modules.** `src/lib/monoagent-token.ts`:

```ts
// What a MonoAgent access token looks like. mono-agent verifies the same values
// offline (internal/account/claims.go in github.com/monoes/mono-agent).

// The audience (RFC 8707 `resource`) MonoAgent tokens are bound to: an
// identifier, not an endpoint. drizzle/0017_monoagent_audience.sql registers it.
export const MONOAGENT_AUDIENCE = "https://monoes.me/api/monoagent";

// The one public OAuth client allowed to ask for that audience.
export const MONOAGENT_CLIENT_ID = "monoagent";

// The `iss` of every token: Better-Auth's base URL plus its base path.
export function authIssuer(): string {
  return `${process.env.BETTER_AUTH_URL ?? "http://localhost:3000"}/api/auth`;
}
```

`src/lib/access-token-claims.ts`:

```ts
// Until paid plans exist everyone is on the free plan, and there is no column
// for it. mono-agent reads this claim and does not enforce it.
export const PLAN_FREE = "free";

// Runs whenever the oauth-provider mints a JWT access token: the
// authorization-code and refresh grants that carry a `resource`.
export function accessTokenClaims(): { plan: string } {
  return { plan: PLAN_FREE };
}
```

Run the same command. Expected: `ℹ pass 3`, `ℹ fail 3`. The claims test and the two constants tests pass; the two `migration 0017` tests fail with `ENOENT: no such file or directory, open '<repo>/drizzle/0017_monoagent_audience.sql'`; the reuse-window test fails with `AssertionError [ERR_ASSERTION]: src/lib/auth.ts sets refreshTokenReuseInterval: 300`.

- [ ] **Step 5: Create the migration with the repository's own tool, then replace its empty SQL.** The tool writes the journal entry and the snapshot, which a hand-made file would lack (the next `db:generate` would number its output 0017 too).

```bash
CLOUDFLARE_ACCOUNT_ID=x CLOUDFLARE_DATABASE_ID=x CLOUDFLARE_D1_TOKEN=x npx drizzle-kit generate --custom --name=monoagent_audience
```

If `main` already holds a `0017_*` migration by now (another session works in this repository), the tool prints the next free number: use it wherever this plan says 0017 (the file, the path in `monoagent-token.test.ts`, the comment in `monoagent-token.ts`, the commit, O2). Expected: `[✓] Your SQL migration file ➜ drizzle/0017_monoagent_audience.sql`; `git status --short` shows `M drizzle/meta/_journal.json`, `?? drizzle/0017_monoagent_audience.sql`, `?? drizzle/meta/0017_snapshot.json`. Overwrite `drizzle/0017_monoagent_audience.sql`:

```sql
-- The audience a MonoAgent access token is bound to (RFC 8707 `resource`), and
-- the only OAuth client allowed to ask for it. The oauth-provider plugin
-- answers a `resource` that is not a row here with invalid_target and, with its
-- default enforcePerClientResources, one the client is not linked to.
-- allowed_scopes stays NULL on purpose: a non-null list narrows the scopes of
-- every token issued for the resource instead of only validating them.
INSERT OR IGNORE INTO `oauth_resource` (
	`id`, `identifier`, `name`, `access_token_ttl`, `refresh_token_ttl`,
	`signing_algorithm`, `signing_key_id`, `allowed_scopes`, `custom_claims`,
	`dpop_bound_access_tokens_required`, `disabled`, `created_at`, `updated_at`,
	`policy_version`, `metadata`
) VALUES (
	'monoagent-resource', 'https://monoes.me/api/monoagent', 'MonoAgent',
	NULL, NULL, NULL, NULL, NULL, NULL,
	0, 0, 1791206580558, 1791206580558,
	1, '{}'
);
--> statement-breakpoint
INSERT OR IGNORE INTO `oauth_client_resource` (
	`id`, `client_id`, `resource_id`, `metadata`, `created_at`
) VALUES (
	'monoagent-client-resource', 'monoagent', 'https://monoes.me/api/monoagent',
	'{}', 1791206580558
);
```

Run the unit tests again: `ℹ pass 5`, `ℹ fail 1` (the reuse-window test waits for step 7). Apply it to the local database: `npm run db:migrate:local`, expected `0017_monoagent_audience.sql ✅` (the rows exist from Task 2's seed, and `INSERT OR IGNORE` leaves them).

- [ ] **Step 6: Write the failing spec.** Create `tests/account-gate-tokens.spec.ts` (it also pins today's client, that an ID token and a session JWT, signed with the same key, never carry the audience, the reuse window, the 30-day expiry of the Review Focus, and that only `monoagent` gets the audience; what a replay after the window ends is step 13's spec):

```ts
import { test, expect } from "@playwright/test";
import { eq } from "drizzle-orm";
import { oauthRefreshToken } from "../src/lib/db/schema";
import { AUDIENCE, authorize, bearer, decodeJwt, isJwt, login, pkce, refresh, signUp, verifiesAgainstJwks, withDb } from "./helpers/oauth-api";

// monoes.me gives MonoAgent an audience-bound JWT when the client sends a
// `resource`, and leaves every client that sends none exactly as it was.
// mono-agent's internal/account verifies these claims offline.

test("without a resource (today's client) the response is unchanged: opaque access token, refresh token, id token", async ({ baseURL }) => {
  const r = await login(baseURL!);
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  expect(isJwt(r.body.access_token), "opaque: not a JWT").toBe(false);
  expect(r.body.token_type).toBe("Bearer");
  expect(r.body.expires_in).toBe(3600);
  expect(r.body.scope).toBe("openid profile email offline_access library:read library:write");
  expect(r.body.refresh_token, "offline_access should yield a refresh token").toBeTruthy();
  expect(decodeJwt(r.body.id_token!).payload.aud).toBe("monoagent");

  const me = await fetch(new URL("/api/library/me", baseURL), { headers: bearer(r.body.access_token) });
  expect(me.status).toBe(200);

  const again = await refresh(baseURL!, r.body.refresh_token!);
  expect(again.status, JSON.stringify(again.body)).toBe(200);
  expect(isJwt(again.body.access_token), "still opaque after a refresh without a resource").toBe(false);
});

test("with the MonoAgent resource the access token is an audience-bound JWT", async ({ baseURL }) => {
  const r = await login(baseURL!, { resource: AUDIENCE });
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  const token = r.body.access_token!;
  const { header, payload } = decodeJwt(token);

  expect(header.alg).toBe("EdDSA");
  expect(header.typ).toBe("at+jwt");
  expect(typeof header.kid).toBe("string");
  // When E2E_SIGNING_KID is set the server runs with the pinned key and must sign with exactly it.
  if (process.env.E2E_SIGNING_KID) expect(header.kid).toBe(process.env.E2E_SIGNING_KID);

  expect(payload.iss).toBe(`${new URL(baseURL!).origin}/api/auth`);
  // The audience is an array (the userinfo endpoint is added because openid is requested) or a string.
  expect([payload.aud].flat()).toContain(AUDIENCE);
  expect(payload.azp).toBe("monoagent");
  expect(payload.client_id).toBe("monoagent");
  expect(payload.plan).toBe("free");
  expect(typeof payload.sub).toBe("string");
  expect((payload.exp as number) - (payload.iat as number)).toBe(3600);
  expect(r.body.expires_in).toBe(3600);
  expect(String(payload.scope).split(" ")).toEqual(expect.arrayContaining(["library:read", "library:write"]));

  expect(await verifiesAgainstJwks(baseURL!, token), "verifies with the published public key").toBe(true);
  expect(r.body.refresh_token).toBeTruthy();
});

test("an ID token and a session JWT, signed with the same key, never carry the MonoAgent audience", async ({ baseURL }) => {
  // mono-agent tells an access token from the other JWTs this key signs by its claims (spec §4.1). The ID token
  // has the same issuer and, by OIDC, may carry `azp`, so its audience (the client id) is what keeps a 10-hour ID
  // token from passing as a MonoAgent access token; the jwt() plugin's session JWT has the base URL as its audience.
  // Neither carries `typ: at+jwt`, which a header check in the client could add as defense in depth.
  const r = await login(baseURL!, { resource: AUDIENCE });
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  const id = decodeJwt(r.body.id_token!);
  expect([id.payload.aud].flat()).not.toContain(AUDIENCE);
  expect(id.header.typ).not.toBe("at+jwt");

  const res = await fetch(new URL("/api/auth/token", baseURL), { headers: { Cookie: r.account.cookie } });
  expect(res.status).toBe(200);
  const { token } = (await res.json()) as { token?: string };
  expect(isJwt(token), "the jwt() plugin's session JWT").toBe(true);
  const session = decodeJwt(token!);
  expect([session.payload.aud].flat()).not.toContain(AUDIENCE);
  expect(session.header.typ).not.toBe("at+jwt");
});

test("a refresh token issued without the resource can be exchanged with it, and the audience then sticks", async ({ baseURL }) => {
  const first = await login(baseURL!);
  const bound = await refresh(baseURL!, first.body.refresh_token!, AUDIENCE);
  expect(bound.status, JSON.stringify(bound.body)).toBe(200);
  expect([decodeJwt(bound.body.access_token!).payload.aud].flat()).toContain(AUDIENCE);
  expect(bound.body.refresh_token === first.body.refresh_token, "refresh tokens rotate").toBe(false);

  const sticky = await refresh(baseURL!, bound.body.refresh_token!);
  expect(sticky.status, JSON.stringify(sticky.body)).toBe(200);
  expect([decodeJwt(sticky.body.access_token!).payload.aud].flat()).toContain(AUDIENCE);
});

test("a valid refresh is never answered invalid_grant: that answer is a refusal to mono-agent", async ({ baseURL }) => {
  const r = await login(baseURL!, { resource: AUDIENCE });
  for (let i = 0, token = r.body.refresh_token!; i < 3; i++) {
    const next = await refresh(baseURL!, token, AUDIENCE);
    expect(next.status, JSON.stringify(next.body)).toBe(200);
    expect(next.body.error).toBeUndefined();
    token = next.body.refresh_token!;
  }
});

test("a retry inside the 300-second reuse window gets the same answer and ends nothing", async ({ baseURL }) => {
  // The client's answer was lost, so it presents the same refresh token again.
  const first = await login(baseURL!, { resource: AUDIENCE });
  const second = await refresh(baseURL!, first.body.refresh_token!, AUDIENCE);
  expect(second.status).toBe(200);
  const retry = await refresh(baseURL!, first.body.refresh_token!, AUDIENCE);
  expect([retry.status, retry.body.refresh_token === second.body.refresh_token, retry.body.access_token === second.body.access_token]).toEqual([200, true, true]);
  expect((await refresh(baseURL!, second.body.refresh_token!, AUDIENCE)).status, "the newer token still works").toBe(200);
});

test("a refresh token past its 30 days is invalid_grant, which mono-agent reads as a refusal", async ({ baseURL }) => {
  const r = await login(baseURL!, { resource: AUDIENCE });
  await withDb((db) => db.update(oauthRefreshToken).set({ expiresAt: new Date(Date.now() - 1000) }).where(eq(oauthRefreshToken.userId, r.account.userId)));
  const expired = await refresh(baseURL!, r.body.refresh_token!, AUDIENCE);
  expect([expired.status, expired.body.error]).toEqual([400, "invalid_grant"]);
});

test("only the monoagent client can obtain the audience, and no other resource exists", async ({ baseURL }) => {
  const account = await signUp(baseURL!);

  const reg = await fetch(new URL("/api/auth/oauth2/register", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ redirect_uris: ["https://example-agent.test/callback"], token_endpoint_auth_method: "none", grant_types: ["authorization_code", "refresh_token"] }),
  });
  const { client_id: dynamicId } = (await reg.json()) as { client_id: string };
  const dynamic = { clientId: dynamicId, redirectUri: "https://example-agent.test/callback", scope: "community:read" };

  await expect(authorize(account, pkce().challenge, { ...dynamic, resource: AUDIENCE })).rejects.toThrow(/invalid_target/);
  // Dynamic registration keeps working: a client that asks for no resource gets its code.
  expect(await authorize(account, pkce().challenge, dynamic)).toBeTruthy();

  await expect(authorize(account, pkce().challenge, { resource: "https://example.com/other" })).rejects.toThrow(/invalid_target/);
  const plain = await login(baseURL!);
  const other = await refresh(baseURL!, plain.body.refresh_token!, "https://example.com/other");
  expect(other.status).toBe(400);
  expect(other.body.error).toBe("invalid_target");
});
```

Run `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-tokens.spec.ts --reporter=line`. Expected: `2 failed, 6 passed`. One failure is `with the MonoAgent resource the access token is an audience-bound JWT` at `expect(payload.plan).toBe("free")`, `Expected: "free"`, `Received: undefined`. The other is `a retry inside the 300-second reuse window gets the same answer and ends nothing`, whose retry answers `400` (`invalid_grant`) where `200` is expected, because the window is not configured yet. (Audience, the audiences and header types of the ID token and the session JWT, rotation and expiry already work through the provider; the spec pins them so a provider upgrade that changes them is noticed.)

- [ ] **Step 7: Wire `src/lib/auth.ts`.** After line 7 (`import * as schema from "@/lib/db/schema";`) add:

```ts
import { accessTokenClaims } from "@/lib/access-token-claims";
```

and replace the `oauthProvider({...})` call (lines 73-79) with:

```ts
      oauthProvider({
        loginPage: "/community/login",
        consentPage: "/community/oauth/consent",
        scopes: [...OAUTH_SCOPES],
        allowDynamicClientRegistration: true,
        allowUnauthenticatedClientRegistration: true,
        accessTokenExpiresIn: 3600,
        // A refresh whose answer was lost is retried with the same token; inside this many seconds the
        // provider answers it again with the same response, where outside it the retry is a replay of a
        // used token. mono-agent retries for as long as 240 seconds (pendingRetryWindow in
        // internal/account/guard.go, spec A24): lower this only together with it.
        refreshTokenReuseInterval: 300,
        customAccessTokenClaims: () => accessTokenClaims(),
      }),
```

Run the unit command of step 3 once more: `ℹ pass 6`, `ℹ fail 0`.

- [ ] **Step 8: Run the spec.** The same command. Expected: `8 passed`.

- [ ] **Step 9: Checks.** `npm test` (`ℹ fail 0`), `npx tsc --noEmit`, `npx eslint src/lib/auth.ts src/lib/monoagent-token.ts src/lib/access-token-claims.ts tests/helpers/oauth-api.ts tests/account-gate-tokens.spec.ts` (no output).

- [ ] **Step 10: Commit.**

```bash
git add drizzle/0017_monoagent_audience.sql drizzle/meta/0017_snapshot.json drizzle/meta/_journal.json src/lib/auth.ts src/lib/monoagent-token.ts src/lib/monoagent-token.test.ts src/lib/access-token-claims.ts src/lib/access-token-claims.test.ts tests/helpers/oauth-api.ts tests/account-gate-tokens.spec.ts
git commit -m "feat(oauth): audience-bound JWT access tokens for the monoagent client" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 11: Write the failing unit tests of the family hook (ruling R1).** What they pin is the design paragraph "Refresh-token families (S7)" above. CI runs these and not the spec of step 13, so they cover how the hook reads a request, each decision it makes, and the delete statements as SQL. `src/lib/refresh-family.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { drizzle } from "drizzle-orm/d1";
import * as schema from "./db/schema.ts";

register(
  `data:text/javascript,
  export function resolve(specifier, context, next) {
    if (specifier === "@/lib/db/schema") return next("./db/schema.ts", context);
    if (specifier === "@/lib/community/hash-token") return next("./community/hash-token.ts", context);
    if (specifier === "@/lib/monoagent-token") return next("./monoagent-token.ts", context);
    return next(specifier, context);
  }`,
  import.meta.url,
);

const { endFamily, refreshTokenOf, revokeCaller, revokedTokenOf, tokenRouteActs } = await import("./refresh-family.ts");

const FORM = "application/x-www-form-urlencoded";
const revokeRequest = (body: string, contentType = FORM) =>
  new Request("https://monoes.test/api/auth/oauth2/revoke", { method: "POST", headers: { "Content-Type": contentType }, body });

describe("what a request presents, read as the provider reads it", () => {
  it("is a refresh at the token route whatever spaces surround grant_type, and the token as sent", () => {
    assert.equal(refreshTokenOf({ grant_type: "refresh_token", refresh_token: "r1" }), "r1");
    assert.equal(refreshTokenOf({ grant_type: "refresh_token ", refresh_token: " r1 " }), " r1 ");
    assert.equal(refreshTokenOf({ grant_type: "authorization_code", code: "c" }), undefined);
    assert.equal(refreshTokenOf({ grant_type: "refresh_token" }), undefined);
  });

  it("is a refresh token at the revoke route under any hint but access_token, trimmed and without its scheme", () => {
    assert.equal(revokedTokenOf({ token: "r1" }), "r1");
    assert.equal(revokedTokenOf({ token: "Bearer r1", token_type_hint: "x" }), "r1");
    assert.equal(revokedTokenOf({ token: " r1 ", token_type_hint: "refresh_token" }), "r1");
    assert.equal(revokedTokenOf({ token: "r1", token_type_hint: "access_token" }), undefined);
    assert.equal(revokedTokenOf({}), undefined);
  });
});

describe("revokeCaller", () => {
  it("reads the single non-empty client_id of the raw request text, as the provider does", async () => {
    assert.equal(await revokeCaller(revokeRequest("token=t&client_id=monoagent"), {}), "monoagent");
    assert.equal(await revokeCaller(revokeRequest("token=t&client_id=monoagent&client_id="), { client_id: "" }), "monoagent");
    assert.equal(await revokeCaller(revokeRequest("token=t&client_id=monoagent&client_id=other"), {}), undefined);
    assert.equal(await revokeCaller(revokeRequest("token=t"), { client_id: "monoagent" }), undefined);
  });

  it("finds the client_id where better-call's parsed body has none: a +json media type, a leading U+FEFF", async () => {
    const json = revokeRequest(JSON.stringify({ token: "t", note: "&client_id=monoagent&" }), `${FORM}+json`);
    assert.equal(await revokeCaller(json, { token: "t", note: "&client_id=monoagent&" }), "monoagent");
    assert.equal(await revokeCaller(revokeRequest("﻿client_id=monoagent&token=t"), { "﻿client_id": "monoagent" }), "monoagent");
  });

  it("leaves the request readable for the provider, and reads an auth.api call's body", async () => {
    const request = revokeRequest("token=t&client_id=monoagent");
    await revokeCaller(request, {});
    assert.equal(await request.text(), "token=t&client_id=monoagent");
    assert.equal(await revokeCaller(undefined, { client_id: "monoagent" }), "monoagent");
    assert.equal(await revokeCaller(undefined, { client_id: "" }), undefined);
  });
});

describe("tokenRouteActs", () => {
  const now = new Date("2026-10-07T12:00:00Z");
  const seconds = (n: number) => new Date(now.getTime() + n * 1000);
  // A MonoAgent refresh token rotated away ten minutes ago: its 300-second reuse window has passed.
  const rotated = {
    id: "row-1",
    token: "hash",
    clientId: "monoagent",
    sessionId: null,
    userId: "user-1",
    referenceId: null,
    authorizationCodeId: "code-1",
    resources: ["https://monoes.me/api/monoagent"],
    requestedUserInfoClaims: null,
    expiresAt: seconds(30 * 24 * 3600),
    createdAt: seconds(-3600),
    revoked: seconds(-600),
    rotatedAt: seconds(-600),
    rotationReplayResponse: null,
    rotationReplayExpiresAt: seconds(-300),
    authTime: null,
    confirmation: null,
    scopes: ["openid", "offline_access", "library:read"],
  };

  it("acts on a MonoAgent token rotated away and presented after the reuse window, or revoked with no rotation", () => {
    assert.equal(tokenRouteActs(rotated, now), true);
    assert.equal(tokenRouteActs({ ...rotated, rotatedAt: null, rotationReplayExpiresAt: null }, now), true);
  });

  it("leaves the reuse window to the provider, judged 10 seconds early", () => {
    assert.equal(tokenRouteActs({ ...rotated, rotationReplayExpiresAt: seconds(60) }, now), false);
    assert.equal(tokenRouteActs({ ...rotated, rotationReplayExpiresAt: seconds(10) }, now), false);
    assert.equal(tokenRouteActs({ ...rotated, rotationReplayExpiresAt: seconds(9) }, now), true);
  });

  it("leaves a live, expired or missing token, and another client's, to the provider", () => {
    assert.equal(tokenRouteActs({ ...rotated, revoked: null, rotatedAt: null, rotationReplayExpiresAt: null }, now), false);
    assert.equal(tokenRouteActs({ ...rotated, expiresAt: now }, now), false);
    assert.equal(tokenRouteActs({ ...rotated, expiresAt: null }, now), false);
    assert.equal(tokenRouteActs({ ...rotated, clientId: "some-agent" }, now), false);
    assert.equal(tokenRouteActs(undefined, now), false);
  });
});

describe("endFamily", () => {
  // Never executed: the statements are only rendered.
  const db = drizzle({} as never, { schema });

  it("deletes the family's access tokens, then its refresh tokens, keyed on client, user and authorization_code_id", () => {
    const [access, refresh] = endFamily(db, { id: "row-1", clientId: "monoagent", userId: "user-1", authorizationCodeId: "code-1" }).map((s) => s.toSQL());
    const family = `"oauth_refresh_token"."client_id" = ? and "oauth_refresh_token"."user_id" = ? and "oauth_refresh_token"."authorization_code_id" = ?`;
    assert.equal(access.sql, `delete from "oauth_access_token" where "oauth_access_token"."refresh_id" in (select "id" from "oauth_refresh_token" where (${family}))`);
    assert.equal(refresh.sql, `delete from "oauth_refresh_token" where (${family})`);
    for (const { params } of [access, refresh]) assert.deepEqual(params, ["monoagent", "user-1", "code-1"]);
  });

  it("never keys a delete on a null authorization_code_id: such a row ends alone", () => {
    for (const { sql, params } of endFamily(db, { id: "row-1", clientId: "monoagent", userId: "user-1", authorizationCodeId: null }).map((s) => s.toSQL())) {
      assert.ok(!sql.includes("authorization_code_id"), sql);
      assert.deepEqual(params, ["row-1"]);
    }
  });
});
```

Run `node --experimental-strip-types --test src/lib/refresh-family.test.ts`. Expected: FAIL, `Error [ERR_MODULE_NOT_FOUND]: Cannot find module '<repo>/src/lib/refresh-family.ts'`, `ℹ tests 1`, `ℹ fail 1`.

- [ ] **Step 12: Create `src/lib/refresh-family.ts`.**

```ts
import { and, eq, inArray } from "drizzle-orm";
import { APIError } from "better-auth/api";
import { stripAccessTokenAuthorizationScheme } from "better-auth/oauth2";
import type { Db } from "@/lib/db";
import { oauthAccessToken, oauthRefreshToken } from "@/lib/db/schema";
import { sha256Base64Url } from "@/lib/community/hash-token";
import { MONOAGENT_CLIENT_ID } from "@/lib/monoagent-token";

// Refresh-token families (ruling R1 of 2026-10-07; spike S7, "Corrected S7 answer 5"). When a refresh token
// it rotated away is presented after the reuse window, or a revoked one is presented at all,
// @better-auth/oauth-provider 1.7.1 deletes every refresh token of the client and the user
// (invalidateRefreshFamily): one stale copy of a MonoAgent sign-in would sign every install of the account
// out. This before hook ends only the presented token's family instead: the chain of one sign-in, named by
// authorization_code_id (the stored hash of the authorization code, copied at every rotation; the email-code
// route writes `email-claim:<claim id>`). On the token route it acts where the provider would punish; at the
// revoke route it revokes MonoAgent's refresh tokens itself, so the provider's revocation never runs for them.

type RefreshRow = typeof oauthRefreshToken.$inferSelect;
type Body = Record<string, unknown>;

// The reuse window is judged 10 seconds early: the hook and the provider read the clock at different
// moments, and mono-agent never retries after 240 seconds, so no honest retry falls in the last 10 of the 300.
const WINDOW_MARGIN_MS = 10_000;

// The refresh token a token-route request presents, as the provider reads it: its schema trims grant_type,
// and it hashes refresh_token as sent.
export function refreshTokenOf(body: Body): string | undefined {
  return String(body.grant_type ?? "").trim() === "refresh_token" && typeof body.refresh_token === "string" ? body.refresh_token : undefined;
}

// The refresh token a revoke request presents, as the provider reads it: none when the hint is exactly
// access_token (any other hint counts as none), and the value trimmed and stripped of "Bearer " or "DPoP ".
export function revokedTokenOf(body: Body): string | undefined {
  if (body.token_type_hint === "access_token" || typeof body.token !== "string") return undefined;
  return stripAccessTokenAuthorizationScheme(body.token) || undefined;
}

// Whether the refresh grant would end the account for this stored row: a revoked, unexpired MonoAgent token
// presented outside the reuse window. Decided on the row alone, never on the request's client_id, which the
// provider reads from the raw form (a repeated client_id is one value to it and another to ctx.body); it only
// punishes a row of the caller's own client. Its other checks (resources, scopes, client validation) are not
// mirrored, which can only end a dead token's family where the provider would have ended nothing.
export function tokenRouteActs(row: RefreshRow | undefined, now: Date): row is RefreshRow {
  if (!row || row.clientId !== MONOAGENT_CLIENT_ID || !row.revoked || !row.expiresAt || row.expiresAt <= now) return false;
  return !(row.rotatedAt && row.rotationReplayExpiresAt && row.rotationReplayExpiresAt.getTime() >= now.getTime() + WINDOW_MARGIN_MS);
}

// The revoke route's caller, read as the provider reads it: the single non-empty client_id of the raw request
// text (two or more are refused). Never ctx.body, which better-call builds differently for a repeated field, a
// "+json" media type or a leading U+FEFF. Only an auth.api call, which has no request, is read from its body.
export async function revokeCaller(request: Request | undefined, body: Body): Promise<string | undefined> {
  if (!request) return typeof body.client_id === "string" && body.client_id ? body.client_id : undefined;
  const ids = new URLSearchParams(await request.clone().text()).getAll("client_id").filter((id) => id.length > 0);
  return ids.length === 1 ? ids[0] : undefined;
}

// The deletes that end the row's family, for one db.batch: its opaque access tokens, then its refresh tokens,
// the presented one included, so a later presentation finds no row and ends nothing more. A row without a
// family key ends alone: a filter on a null authorization_code_id would match nothing in SQL (`= NULL`), or
// every keyless chain of the user through the provider's adapter (`IS NULL`).
export function endFamily(db: Db, row: Pick<RefreshRow, "id" | "clientId" | "userId" | "authorizationCodeId">) {
  const family = row.authorizationCodeId
    ? and(
        eq(oauthRefreshToken.clientId, row.clientId),
        eq(oauthRefreshToken.userId, row.userId),
        eq(oauthRefreshToken.authorizationCodeId, row.authorizationCodeId),
      )
    : eq(oauthRefreshToken.id, row.id);
  const members = db.select({ id: oauthRefreshToken.id }).from(oauthRefreshToken).where(family);
  return [db.delete(oauthAccessToken).where(inArray(oauthAccessToken.refreshId, members)), db.delete(oauthRefreshToken).where(family)] as const;
}

const rowOf = async (db: Db, value: string) =>
  (await db.select().from(oauthRefreshToken).where(eq(oauthRefreshToken.token, await sha256Base64Url(value))).limit(1))[0];

async function endFamilyOf(db: Db, row: RefreshRow) {
  await db.batch(endFamily(db, row));
  // A rotation whose compare-and-set won just before the batch inserts its successor just after it: sweep again.
  await endFamily(db, row)[1];
}

// The before hook of getAuth (src/lib/auth.ts). It returns nothing for every request it leaves to the provider.
export async function endReplayedFamily(db: Db, ctx: { path?: string; body?: unknown; request?: Request }) {
  const body = (ctx.body ?? {}) as Body;
  if (ctx.path === "/oauth2/token") {
    const value = refreshTokenOf(body);
    const row = value === undefined ? undefined : await rowOf(db, value);
    if (!tokenRouteActs(row, new Date())) return;
    await endFamilyOf(db, row);
    throw new APIError("BAD_REQUEST", { error_description: "invalid refresh token", error: "invalid_grant" });
  }
  if (ctx.path !== "/oauth2/revoke") return;
  const value = revokedTokenOf(body);
  const row = value === undefined ? undefined : await rowOf(db, value);
  if (!row || (await revokeCaller(ctx.request, body)) !== MONOAGENT_CLIENT_ID) return;
  // Live or revoked, inside the window or not: a MonoAgent token revoked here ends its family. Another client's
  // token presented as monoagent's ends nothing (the provider would end every MonoAgent token of its user).
  if (row.clientId === MONOAGENT_CLIENT_ID) await endFamilyOf(db, row);
  if (!row.revoked) return new Response(null, { status: 200 });
  throw new APIError("BAD_REQUEST", { error_description: body.token_type_hint === "refresh_token" ? "refresh token revoked" : "token not found", error: "invalid_request" });
}
```

Run the same command. Expected: `ℹ tests 10`, `ℹ pass 10`, `ℹ fail 0`.

- [ ] **Step 13: Write the failing family spec.** Create `tests/account-gate-family.spec.ts`. Two sign-ins of one account stand for two installs (`install` fails at once when a sign-in fails, which a busy dev server can cause, so it is never read as a punishment later); `endWindow` ends the 300-second window in the database instead of waiting.

```ts
import { test, expect } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { eq } from "drizzle-orm";
import { oauthRefreshToken } from "../src/lib/db/schema";
import { sha256Base64Url } from "../src/lib/community/hash-token";
import { AUDIENCE, MONOAGENT_SCOPES, authorize, bearer, exchange, login, pkce, refresh, withDb, type Account } from "./helpers/oauth-api";

// Ruling R1 of 2026-10-07: a refresh token that monoes.me rotated away, presented again after the
// reuse window, or a revoked one, ends the sign-in it comes from (its refresh-token family) and
// nothing else. Every other sign-in of the account, every other install, keeps working. The provider
// alone would end every MonoAgent refresh token of the account; src/lib/refresh-family.ts narrows it.
// Each test ends with the account's other sign-in still refreshing.

const FORM = "application/x-www-form-urlencoded";
const form = (pairs: [string, string][]) => new URLSearchParams(pairs).toString();

// The window is 300 seconds; end it in the database instead of waiting.
const endWindow = (userId: string) =>
  withDb((db) => db.update(oauthRefreshToken).set({ rotationReplayExpiresAt: new Date(Date.now() - 1000) }).where(eq(oauthRefreshToken.userId, userId)));

// One sign-in of the account, as one install makes it. A sign-in that failed on a busy server would show
// up later as a refused refresh and read like a punishment, so it fails here.
async function install(baseURL: string, account?: Account, resource: string | null = AUDIENCE) {
  const r = await login(baseURL, { account, ...(resource ? { resource } : {}) });
  expect(r.status, "a sign-in").toBe(200);
  return r;
}

// A raw request to the token or the revoke route, for the shapes the helpers do not send.
async function post(baseURL: string, route: "token" | "revoke", body: string, contentType = FORM) {
  const res = await fetch(new URL(`/api/auth/oauth2/${route}`, baseURL), { method: "POST", headers: { "Content-Type": contentType }, body });
  const text = await res.text();
  let parsed: { error?: string; error_description?: string; refresh_token?: string } = {};
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {}
  return { status: res.status, text, body: parsed };
}

// Two sign-ins of one account; the first rotated once (A1, then A2), the second (B) untouched.
async function twoSignIns(baseURL: string) {
  const a = await install(baseURL);
  const b = await install(baseURL, a.account);
  const a2 = await refresh(baseURL, a.body.refresh_token!, AUDIENCE);
  expect(a2.status, "the rotation of A1").toBe(200);
  return { account: a.account, a1: a.body.refresh_token!, a2: a2.body.refresh_token!, b: b.body.refresh_token! };
}

// A's family has ended and B, the other sign-in, still refreshes.
async function onlyTheFamilyEnded(baseURL: string, s: { a2: string; b: string }) {
  expect((await refresh(baseURL, s.a2, AUDIENCE)).status, "the replayed sign-in's newer token goes with it").toBe(400);
  expect((await refresh(baseURL, s.b, AUDIENCE)).status, "another sign-in of the account keeps refreshing").toBe(200);
}

test("a replay after the window is invalid_grant and ends only that sign-in: another sign-in of the account keeps refreshing", async ({ baseURL }) => {
  // A second machine with a copied session file, or a retry after a long outage, presents a used refresh token.
  const s = await twoSignIns(baseURL!);
  await endWindow(s.account.userId);
  const replay = await refresh(baseURL!, s.a1, AUDIENCE);
  expect([replay.status, replay.body.error]).toEqual([400, "invalid_grant"]);
  await onlyTheFamilyEnded(baseURL!, s);
});

// The provider trims grant_type and reads client_id from the raw form, so neither shape may slip past the hook.
for (const [name, body] of [
  ["grant_type with a trailing space", (t: string) => form([["grant_type", "refresh_token "], ["refresh_token", t], ["client_id", "monoagent"], ["resource", AUDIENCE]])],
  ["client_id=monoagent&client_id=", (t: string) => `${form([["grant_type", "refresh_token"], ["refresh_token", t], ["client_id", "monoagent"], ["resource", AUDIENCE]])}&client_id=`],
] as const) {
  test(`a replay after the window with ${name} ends only that sign-in`, async ({ baseURL }) => {
    const s = await twoSignIns(baseURL!);
    await endWindow(s.account.userId);
    const replay = await post(baseURL!, "token", body(s.a1));
    expect([replay.status, replay.body.error]).toEqual([400, "invalid_grant"]);
    await onlyTheFamilyEnded(baseURL!, s);
  });
}

test("a dead token is punished once: presenting it again ends nothing, and the sign-ins made before and since survive", async ({ baseURL }) => {
  // A client whose disk is full cannot save its `refused` marker (spec A21) and presents the same dead token at
  // every due command.
  const s = await twoSignIns(baseURL!);
  await endWindow(s.account.userId);
  const punished = await refresh(baseURL!, s.a1, AUDIENCE);
  expect([punished.status, punished.body.error]).toEqual([400, "invalid_grant"]);

  const since = await install(baseURL!, s.account);
  for (let i = 0; i < 3; i++) {
    // The dead token's row went with its family: there is nothing left to punish.
    const repeat = await refresh(baseURL!, s.a1, AUDIENCE);
    expect([repeat.status, repeat.body.error, repeat.body.error_description], `presentation ${i + 2} of the dead token`).toEqual([400, "invalid_grant", "session not found"]);
  }
  expect((await refresh(baseURL!, s.b, AUDIENCE)).status, "the sign-in made before survives").toBe(200);
  expect((await refresh(baseURL!, since.body.refresh_token!, AUDIENCE)).status, "the sign-in made since survives").toBe(200);
});

// The revoke route has no reuse window; it reads any hint but access_token as none, trims the token and strips
// "Bearer ", and reads its caller from the raw form. A1 was rotated a moment ago, inside the reuse window.
for (const [name, body, contentType, description] of [
  ["no hint", (t: string) => form([["token", t], ["client_id", "monoagent"]]), FORM, "token not found"],
  ["the hint refresh_token", (t: string) => form([["token", t], ["client_id", "monoagent"], ["token_type_hint", "refresh_token"]]), FORM, "refresh token revoked"],
  ["an unknown hint", (t: string) => form([["token", t], ["client_id", "monoagent"], ["token_type_hint", "x"]]), FORM, "token not found"],
  ["a Bearer prefix", (t: string) => form([["token", `Bearer ${t}`], ["client_id", "monoagent"]]), FORM, "token not found"],
  ["spaces around the token", (t: string) => form([["token", ` ${t} `], ["client_id", "monoagent"]]), FORM, "token not found"],
  ["client_id=monoagent&client_id=", (t: string) => `${form([["token", t], ["client_id", "monoagent"]])}&client_id=`, FORM, "token not found"],
  ["a +json media type, client_id only in the raw text", (t: string) => JSON.stringify({ token: t, note: "&client_id=monoagent&" }), `${FORM}+json`, "token not found"],
  ["a U+FEFF before client_id", (t: string) => `﻿${form([["client_id", "monoagent"], ["token", t]])}`, FORM, "token not found"],
] as const) {
  test(`the revoke route ends only the family of a rotated token, inside the reuse window too (${name})`, async ({ baseURL }) => {
    const s = await twoSignIns(baseURL!);
    const revoked = await post(baseURL!, "revoke", body(s.a1), contentType);
    expect([revoked.status, revoked.body.error, revoked.body.error_description]).toEqual([400, "invalid_request", description]);
    await onlyTheFamilyEnded(baseURL!, s);
  });
}

test("the revoke route ends only the family of an expired rotated token", async ({ baseURL }) => {
  // A copied session file's token, rotated away and since expired.
  const s = await twoSignIns(baseURL!);
  const hash = await sha256Base64Url(s.a1);
  await withDb((db) => db.update(oauthRefreshToken).set({ expiresAt: new Date(Date.now() - 1000) }).where(eq(oauthRefreshToken.token, hash)));
  const revoked = await post(baseURL!, "revoke", form([["token", s.a1], ["client_id", "monoagent"]]));
  expect([revoked.status, revoked.body.error]).toEqual([400, "invalid_request"]);
  await onlyTheFamilyEnded(baseURL!, s);
});

test("a revoke of a live token (account logout) answers 200 and ends its own sign-in; presenting it again ends nothing", async ({ baseURL }) => {
  const s = await twoSignIns(baseURL!);
  const revoked = await post(baseURL!, "revoke", form([["token", s.a2], ["client_id", "monoagent"]]));
  expect([revoked.status, revoked.text]).toEqual([200, ""]);
  const again = await refresh(baseURL!, s.a2, AUDIENCE);
  expect([again.status, again.body.error, again.body.error_description], "the revoked token's row is gone").toEqual([400, "invalid_grant", "session not found"]);
  expect((await refresh(baseURL!, s.b, AUDIENCE)).status, "another sign-in of the account keeps refreshing").toBe(200);
});

test("another client's revoked token presented as monoagent's at the revoke route ends nothing", async ({ baseURL }) => {
  // The provider punishes the caller's client for the row's user, whatever client the row belongs to.
  const a = await install(baseURL!);
  const redirectUri = "https://example-agent.test/callback";
  const reg = await fetch(new URL("/api/auth/oauth2/register", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ redirect_uris: [redirectUri], token_endpoint_auth_method: "none", grant_types: ["authorization_code", "refresh_token"] }),
  });
  const { client_id: clientId } = (await reg.json()) as { client_id: string };
  const p = pkce();
  const code = await authorize(a.account, p.challenge, { clientId, redirectUri, scope: "community:read offline_access" });
  const d1 = (await exchange(baseURL!, code, p.verifier, undefined, redirectUri, clientId)).body.refresh_token!;
  const rotate = (t: string) => post(baseURL!, "token", form([["grant_type", "refresh_token"], ["refresh_token", t], ["client_id", clientId]]));
  const d2 = await rotate(d1);
  expect(d2.status, "the other client's rotation").toBe(200);

  const revoked = await post(baseURL!, "revoke", form([["token", d1], ["client_id", "monoagent"]]));
  expect([revoked.status, revoked.body.error]).toEqual([400, "invalid_request"]);
  expect((await refresh(baseURL!, a.body.refresh_token!, AUDIENCE)).status, "the account's MonoAgent sign-in keeps refreshing").toBe(200);
  expect((await rotate(d2.body.refresh_token!)).status, "the other client's sign-in keeps refreshing").toBe(200);
});

test("the opaque access tokens of an ended family go with it; another sign-in's keep working", async ({ baseURL }) => {
  // Today's client: without a resource the access tokens are rows, linked to their refresh token by refresh_id.
  const first = await install(baseURL!, undefined, null);
  const other = await install(baseURL!, first.account, null);
  const second = await refresh(baseURL!, first.body.refresh_token!);
  expect(second.status).toBe(200);
  await endWindow(first.account.userId);
  expect((await refresh(baseURL!, first.body.refresh_token!)).status).toBe(400);
  const me = async (token?: string) => (await fetch(new URL("/api/library/me", baseURL), { headers: bearer(token) })).status;
  expect(await me(second.body.access_token), "the ended family's access token").toBe(401);
  expect(await me(other.body.access_token), "another sign-in's access token").toBe(200);
});

test("a chain without a family key ends alone: a null authorization_code_id never keys a delete", async ({ baseURL }) => {
  // Every chain the provider issues carries a key, and the email-code route writes one; a row written without it
  // must not take the account's other keyless chains with it, as a delete keyed on IS NULL would.
  const browser = await install(baseURL!);
  const keyless = async () => {
    const value = randomBytes(32).toString("base64url");
    const hash = await sha256Base64Url(value);
    await withDb((db) =>
      db.insert(oauthRefreshToken).values({
        id: crypto.randomUUID(),
        token: hash,
        clientId: "monoagent",
        userId: browser.account.userId,
        scopes: MONOAGENT_SCOPES.split(" "),
        expiresAt: new Date(Date.now() + 30 * 24 * 60 * 60 * 1000),
        createdAt: new Date(),
      }),
    );
    return value;
  };
  const a = await keyless();
  const b = await keyless();
  expect((await refresh(baseURL!, a, AUDIENCE)).status).toBe(200);
  await endWindow(browser.account.userId);
  const replay = await refresh(baseURL!, a, AUDIENCE);
  expect([replay.status, replay.body.error]).toEqual([400, "invalid_grant"]);
  expect((await refresh(baseURL!, a, AUDIENCE)).body.error_description, "the replayed row itself is gone").toBe("session not found");
  expect((await refresh(baseURL!, b, AUDIENCE)).status, "another keyless chain survives").toBe(200);
  expect((await refresh(baseURL!, browser.body.refresh_token!, AUDIENCE)).status, "the browser sign-in survives").toBe(200);
});
```

Run `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-family.spec.ts --reporter=line`. Expected: `17 failed`. Without the hook the provider deletes every MonoAgent refresh token of the account, so each test fails where another sign-in has to survive: `another sign-in of the account keeps refreshing` (`Expected: 200`, `Received: 400`) in the three token-route replays and the nine revoke-route tests; `the sign-in made before survives`; `the account's MonoAgent sign-in keeps refreshing` (another client's token); `another sign-in's access token` (`Expected: 200`, `Received: 401`); `another keyless chain survives`; and in the live revoke, `the revoked token's row is gone`, because the provider marks the token revoked instead of deleting its family, so presenting it again is punished (`invalid refresh token`, not `session not found`). The spec's `withDb` and the dev server write the same local D1 file: on a busy machine a write can fail with `SQLITE_BUSY` (`database is locked`), or a test can outlast its 3 minutes. Rerun such a failure alone (`-g "<title>"`, with `--timeout=900000` on a slow machine) before you read anything into it; the runs that proved this plan, on a loaded machine, hit both. If sign-ups start answering 500 and the server log shows a failed D1 query (`fetch failed`, `other side closed`), the local D1 behind `next dev` has broken: restart `next dev` and run again.

- [ ] **Step 14: Wire the hook into `src/lib/auth.ts`.** After line 8 (`import { accessTokenClaims } from "@/lib/access-token-claims";`, added in step 7) add:

```ts
import { createAuthMiddleware } from "better-auth/api";
import { endReplayedFamily } from "@/lib/refresh-family";
```

and after the line `    ],` that closes the `plugins: [` array, before `    user: {`, add:

```ts
    // A MonoAgent refresh token presented after it was rotated away or revoked ends only the sign-in it
    // comes from, its refresh-token family, not every MonoAgent sign-in of the account (ruling R1 of
    // 2026-10-07). It runs before the oauth-provider's endpoints; see src/lib/refresh-family.ts.
    hooks: {
      before: createAuthMiddleware((ctx) => endReplayedFamily(db, ctx)),
    },
```

Run `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-family.spec.ts tests/account-gate-tokens.spec.ts --reporter=line`. Expected: `25 passed`, the family spec's 17 and the tokens spec's 8. The window's retry (`a retry inside the 300-second reuse window gets the same answer and ends nothing`) still gets the provider's stored answer: the hook steps aside inside the window.

- [ ] **Step 15: Prove the tests bite.** Make one change at a time, run the unit file of step 11 and the spec test named, see it fail as shown, then revert the change (`git diff --stat` shows nothing for the file). The dev server recompiles at its next request: send one (`curl -s -o /dev/null http://localhost:3107/api/auth/ok`) before a spec run.

| Change | Unit test that fails | Spec test that fails, and where |
|---|---|---|
| `auth.ts` without the `hooks` option (step 14 undone) | none: the unit file does not load `auth.ts` | all 17 family tests, as in step 13 |
| the family keyed on client and user only (drop `eq(oauthRefreshToken.authorizationCodeId, row.authorizationCodeId)`) | `deletes the family's access tokens, then its refresh tokens, keyed on client, user and authorization_code_id` | `a replay after the window is invalid_grant and ends only that sign-in: ...`: `another sign-in of the account keeps refreshing`, `Received: 400` |
| `grant_type` compared as sent (`String(body.grant_type ?? "").trim() === "refresh_token"` becomes `body.grant_type === "refresh_token"`) | `is a refresh at the token route whatever spaces surround grant_type, and the token as sent` | `a replay after the window with grant_type with a trailing space ends only that sign-in`: `another sign-in of the account keeps refreshing` |
| the revoke route's caller read from `ctx.body` (`revokeCaller` always returns the body's `client_id`) | both raw-text `revokeCaller` tests | the revoke tests with `client_id=monoagent&client_id=`, a `+json` media type and a U+FEFF: `another sign-in of the account keeps refreshing` |
| the window judged at the moment of the request (`now.getTime() + WINDOW_MARGIN_MS` becomes `now.getTime()`) | `leaves the reuse window to the provider, judged 10 seconds early` | none: a spec cannot time a request into the window's last 10 seconds |
| only an absent or `refresh_token` hint read as a refresh (`body.token_type_hint === "access_token"` becomes `(body.token_type_hint !== undefined && body.token_type_hint !== "refresh_token")`) | `is a refresh token at the revoke route under any hint but access_token, trimmed and without its scheme` | `the revoke route ... (an unknown hint)`: `another sign-in of the account keeps refreshing` |
| the revoke route's token used as it came (`stripAccessTokenAuthorizationScheme(body.token)` becomes `body.token`) | the same test | `the revoke route ... (a Bearer prefix)` and `(spaces around the token)`: `another sign-in of the account keeps refreshing` |
| a keyless row keyed on `isNull(oauthRefreshToken.authorizationCodeId)` with its client and user | `never keys a delete on a null authorization_code_id: such a row ends alone` | `a chain without a family key ends alone: ...`: `another keyless chain survives`, `Received: 400` |
| no window on the token route (the last line of `tokenRouteActs` becomes `return true;`) | `leaves the reuse window to the provider, judged 10 seconds early` | tokens spec, `a retry inside the 300-second reuse window gets the same answer and ends nothing`: the retry answers `400` |
| the presented row kept (the refresh delete becomes `.where(and(family, ne(oauthRefreshToken.id, row.id)))`) | both `endFamily` tests | `a dead token is punished once: ...`: `presentation 2 of the dead token` answers `invalid refresh token`, not `session not found`; the live revoke: the token still refreshes (`200`) after its own revocation |
| another client's row deleted too (`if (row.clientId === MONOAGENT_CLIENT_ID)` dropped before `endFamilyOf`) | none: the glue is not unit-tested | `another client's revoked token presented as monoagent's ...`: `the other client's sign-in keeps refreshing`, `Received: 400` |
| a live token left to the provider (`if (!row.revoked) return;` before the delete) | none | the live revoke: `the revoked token's row is gone` answers `invalid refresh token`: the provider marked the token revoked, so presenting it again was punished |
| no access-token delete (`endFamily` returns only the refresh delete) | `deletes the family's access tokens, ...` | none, and that is expected: D1 enforces `refresh_id`'s `ON DELETE CASCADE`, so the access rows go with their refresh tokens anyway. The explicit delete mirrors the provider, which deletes them itself, and does not depend on the database enforcing the key |

- [ ] **Step 16: Checks.** `npm test` (`ℹ fail 0`), `npx tsc --noEmit`, `npx eslint src/lib/auth.ts src/lib/refresh-family.ts src/lib/refresh-family.test.ts tests/account-gate-family.spec.ts` (no output). `git status --short` lists those four files, and the two that `next dev` and `tsc` rewrite: restore those (`git checkout -- next-env.d.ts tsconfig.tsbuildinfo`).

- [ ] **Step 17: Commit.**

```bash
git add src/lib/auth.ts src/lib/refresh-family.ts src/lib/refresh-family.test.ts tests/account-gate-family.spec.ts
git commit -m "feat(oauth): a replayed monoagent refresh token ends only its own sign-in" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 18: Offer the branch to the owner** (push and `gh pr create` only when told). Its two commits ship together, so the family rule deploys with the audience. Once it has deployed, the client still keeps A24 and A25 and changes no constant until the owner has measured the deployed server (ruling R1).

- [ ] **Step 19 (OWNER-RUN, O2): apply the migration to production.** `deploy.yml` never runs migrations, so after this merges, and before any mono-agent release sends a `resource`, run in the repository (the README documents the command):

```bash
npx wrangler d1 migrations apply monoes-community --remote
npx wrangler d1 execute monoes-community --remote --command "SELECT identifier, name, allowed_scopes, disabled FROM oauth_resource"
npx wrangler d1 execute monoes-community --remote --command "SELECT client_id, resource_id FROM oauth_client_resource"
npx wrangler d1 execute monoes-community --remote --command "SELECT count(*) AS keyless FROM oauth_refresh_token WHERE client_id = 'monoagent' AND authorization_code_id IS NULL"
```

`migrations apply --remote` applies every pending migration, not only this one: read the list it prints before you confirm. Expected: the list holds `0017_monoagent_audience.sql` and nothing you did not expect; the second returns `https://monoes.me/api/monoagent`, `MonoAgent`, `null`, `0`; the third returns `monoagent`, `https://monoes.me/api/monoagent`. Without these rows a `resource` is answered `invalid_target` and no new client can sign in. The fourth only reads: expected `keyless` `0`, every MonoAgent chain has a family key (S7 found that the provider writes one on every path). A keyless chain still ends alone when it is replayed, but its successor, keyless too, survives: tell the lead if the count is not 0.

### Task 4: Blocking that holds

**Files:**
- Create: `src/lib/community/revoke-oauth-access.ts`, `src/lib/community/revoke-oauth-access.test.ts`, `tests/account-gate-block.spec.ts`
- Modify: `src/lib/access-token-claims.ts` and `src/lib/access-token-claims.test.ts` (both replaced), `src/lib/auth.ts` (the `customAccessTokenClaims` line from Task 3), `src/app/api/community/admin/users/[id]/block/route.ts` (imports, lines 1-5; the update, lines 21-27)

**Interfaces:** Produces `revokeOAuthAccess(db: Db, userId: string)`, the delete statements for the user's `oauth_access_token`, `oauth_refresh_token` and `session` rows, meant for `db.batch`; and `accessTokenClaims(user: Record<string, unknown> | null | undefined): { plan: string }`, which throws `APIError("BAD_REQUEST", { error: "invalid_grant", error_description: "this account is blocked" })` for a blocked user. Consumes `Db` (`@/lib/db`), the three schema tables, `APIError` from `better-auth/api`.

**Design (S3).** Before this task a block is two columns: the refresh grant accepts a blocked user (200), and a web session that is still alive can authorize and mint new tokens. Two layers fix it. The deletion makes every refresh find no row (`invalid_grant`, `session not found`), covers opaque chains too, and kills the live cookie. The claims guard makes an audience-bound grant for a blocked user answer `invalid_grant` even when the block was made some other way than the route; it runs before the refresh token is rotated, so nothing is consumed. Unblocking deletes nothing: the user signs in again. The deletion also takes the rotated refresh-token rows, so a token rotated a moment ago, inside Task 3's reuse window, no longer has a stored answer to replay: the spec presents one after the block. Nothing introspects tokens here, but the provider re-derives these claims at opaque-token introspection, so introspecting a blocked user's token now errors.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-blocking origin/main`. Task 3 must already be merged.

- [ ] **Step 2: Write the failing unit tests.** Replace `src/lib/access-token-claims.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { accessTokenClaims, PLAN_FREE } from "./access-token-claims.ts";

describe("accessTokenClaims", () => {
  it("puts everyone on the free plan until plans exist", () => {
    assert.equal(PLAN_FREE, "free");
    assert.deepEqual(accessTokenClaims({ id: "u1", blockedAt: null }), { plan: "free" });
    assert.deepEqual(accessTokenClaims({ id: "u1" }), { plan: "free" });
  });

  it("has no user to look at for a machine-to-machine token", () => {
    assert.deepEqual(accessTokenClaims(undefined), { plan: "free" });
    assert.deepEqual(accessTokenClaims(null), { plan: "free" });
  });

  it("refuses a blocked account with invalid_grant, the answer mono-agent reads as a refusal", () => {
    assert.throws(
      () => accessTokenClaims({ id: "u1", blockedAt: new Date() }),
      (err: { body?: { error?: string; error_description?: string }; statusCode?: number }) => {
        assert.equal(err.statusCode, 400);
        assert.equal(err.body?.error, "invalid_grant");
        assert.match(err.body?.error_description ?? "", /blocked/);
        return true;
      },
    );
  });
});
```

Create `src/lib/community/revoke-oauth-access.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { getTableName } from "drizzle-orm";
import { SQLiteSyncDialect } from "drizzle-orm/sqlite-core";

register(
  `data:text/javascript,
  export function resolve(specifier, context, next) {
    if (specifier === "@/lib/db/schema") return next("../db/schema.ts", context);
    return next(specifier, context);
  }`,
  import.meta.url,
);

const { revokeOAuthAccess } = await import("./revoke-oauth-access.ts");

// A db whose delete(table).where(condition) just records what it was asked.
const recordingDb = {
  delete: (table: unknown) => ({ where: (condition: unknown) => ({ table, condition }) }),
};

describe("revokeOAuthAccess", () => {
  it("deletes the user's OAuth access tokens, refresh tokens and web sessions, and only theirs", () => {
    const statements = revokeOAuthAccess(recordingDb as never, "user-1") as unknown as { table: never; condition: never }[];
    assert.deepEqual(
      statements.map((s) => getTableName(s.table)),
      ["oauth_access_token", "oauth_refresh_token", "session"],
    );
    const dialect = new SQLiteSyncDialect();
    for (const statement of statements) {
      const query = dialect.sqlToQuery(statement.condition);
      assert.match(query.sql, /"user_id" = \?/);
      assert.deepEqual(query.params, ["user-1"]);
    }
  });
});
```

- [ ] **Step 3: Run them.** `node --experimental-strip-types --test src/lib/access-token-claims.test.ts src/lib/community/revoke-oauth-access.test.ts`. Expected: FAIL; the claims test `refuses a blocked account with invalid_grant` reports `Missing expected exception`, and the revoke test `Cannot find module '<repo>/src/lib/community/revoke-oauth-access.ts'`.

- [ ] **Step 4: Write the failing spec.** Create `tests/account-gate-block.spec.ts`:

```ts
import { test, expect } from "@playwright/test";
import { eq } from "drizzle-orm";
import { user } from "../src/lib/db/schema";
import { AUDIENCE, authorize, bearer, exchange, login, pkce, refresh, setUserRole, signIn, signUp, withDb } from "./helpers/oauth-api";

// A refresh answered invalid_grant is, to mono-agent, "monoes.me said no": it
// deletes its refresh token and locks. So a block must make exactly that the
// answer, for the token chains that exist and for anything minted afterwards.

async function setBlocked(baseURL: string, adminCookie: string, userId: string, blocked: boolean) {
  const res = await fetch(new URL(`/api/community/admin/users/${userId}/block`, baseURL), {
    method: "PATCH",
    headers: { "Content-Type": "application/json", Origin: new URL(baseURL).origin, Cookie: adminCookie },
    body: JSON.stringify({ blocked }),
  });
  return res.status;
}

test("blocking holds: refresh is invalid_grant, old tokens stop, unblocking lets the user back in", async ({ baseURL }) => {
  const admin = await signUp(baseURL!);
  await setUserRole(admin.userId, "admin");
  const target = await signUp(baseURL!);
  const bound = await login(baseURL!, { resource: AUDIENCE, account: target });
  const plain = await login(baseURL!, { account: target });
  expect((await refresh(baseURL!, bound.body.refresh_token!, AUDIENCE)).status, "not blocked yet").toBe(200);
  const live = await login(baseURL!, { resource: AUDIENCE, account: target });

  expect(await setBlocked(baseURL!, admin.cookie, target.userId, true)).toBe(200);

  for (const [name, chain, resource] of [
    ["audience-bound chain", live.body.refresh_token!, AUDIENCE],
    ["plain chain", plain.body.refresh_token!, undefined],
    // Rotated a moment ago, so inside the reuse window: its stored answer must not outlive the block.
    ["rotated token inside the reuse window", bound.body.refresh_token!, AUDIENCE],
  ] as const) {
    const refused = await refresh(baseURL!, chain, resource);
    expect(refused.status, name).toBe(400);
    expect(refused.body.error, name).toBe("invalid_grant");
  }

  // The JWT already issued lives out its hour, but every route refuses a blocked account meanwhile.
  const old = await fetch(new URL("/api/library/me", baseURL), { headers: bearer(live.body.access_token) });
  expect(old.status).toBe(403);
  expect(((await old.json()) as { error: { code: string } }).error.code).toBe("blocked");
  // The opaque token's row is gone.
  expect((await fetch(new URL("/api/library/me", baseURL), { headers: bearer(plain.body.access_token) })).status).toBe(401);

  // No new sign-in, and the web session that was alive can no longer authorize a token.
  expect((await signIn(baseURL!, target.email)).status).toBe(401);
  await expect(authorize(target, pkce().challenge, { resource: AUDIENCE })).rejects.toThrow();

  expect(await setBlocked(baseURL!, admin.cookie, target.userId, false)).toBe(200);
  const back = await signIn(baseURL!, target.email);
  expect(back.status).toBe(200);
  const again = await login(baseURL!, { resource: AUDIENCE, account: back.account });
  expect(again.status, JSON.stringify(again.body)).toBe(200);
  expect((await refresh(baseURL!, again.body.refresh_token!, AUDIENCE)).status).toBe(200);
});

test("a blocked user whose tokens survived is refused invalid_grant when the grant carries the resource", async ({ baseURL }) => {
  // A block made some other way than the route (a script, a console) deletes nothing. The chain is
  // audience-bound, so the claims guard answers; a chain without a resource is only covered by the deletion.
  const target = await signUp(baseURL!);
  const bound = await login(baseURL!, { resource: AUDIENCE, account: target });
  await withDb((db) => db.update(user).set({ blockedAt: new Date() }).where(eq(user.id, target.userId)));

  const refused = await refresh(baseURL!, bound.body.refresh_token!, AUDIENCE);
  expect(refused.status).toBe(400);
  expect(refused.body.error).toBe("invalid_grant");

  const { verifier, challenge } = pkce();
  const code = await authorize(target, challenge, { resource: AUDIENCE });
  const viaSession = await exchange(baseURL!, code, verifier, AUDIENCE);
  expect(viaSession.status).toBe(400);
  expect(viaSession.body.error).toBe("invalid_grant");
});
```

Run `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-block.spec.ts --reporter=line`. Expected: both tests fail; the first at `audience-bound chain: expect(received).toBe(expected)`, `Expected: 400`, `Received: 200`.

- [ ] **Step 5: Implement.** Replace `src/lib/access-token-claims.ts`:

```ts
import { APIError } from "better-auth/api";

// Until paid plans exist everyone is on the free plan, and there is no column
// for it. mono-agent reads this claim and does not enforce it.
export const PLAN_FREE = "free";

// Runs whenever the oauth-provider mints a JWT access token: the
// authorization-code and refresh grants that carry a `resource`. A blocked
// account is refused with invalid_grant, which is the one answer mono-agent
// reads as "monoes.me said no" (every other failure only starts its offline
// grace). The refresh grant itself never looks at the user's state, and grants
// without a `resource` (opaque tokens) never reach this function: the block
// route deleting the user's tokens, and every route's own blocked check, cover
// those. The provider also re-derives these claims when it introspects an opaque
// token, so introspecting a blocked user's token now errors; nothing here does.
export function accessTokenClaims(user: Record<string, unknown> | null | undefined): { plan: string } {
  if (user?.blockedAt) {
    throw new APIError("BAD_REQUEST", { error: "invalid_grant", error_description: "this account is blocked" });
  }
  return { plan: PLAN_FREE };
}
```

Create `src/lib/community/revoke-oauth-access.ts`:

```ts
import { eq } from "drizzle-orm";
import type { Db } from "@/lib/db";
import { oauthAccessToken, oauthRefreshToken, session } from "@/lib/db/schema";

/**
 * What ends everything a blocked user can still use, as statements to run in
 * the same batch as the block itself:
 * - their OAuth refresh tokens, so a refresh finds no row and answers
 *   invalid_grant (the one answer mono-agent reads as "monoes.me said no"),
 * - their OAuth access tokens (opaque ones are rows; audience-bound JWTs live
 *   out their hour and every route refuses a blocked user meanwhile),
 * - their web sessions, so a cookie that is still alive cannot authorize a
 *   new token.
 * Unblocking deletes nothing: the user signs in again from scratch.
 */
export function revokeOAuthAccess(db: Db, userId: string) {
  return [
    db.delete(oauthAccessToken).where(eq(oauthAccessToken.userId, userId)),
    db.delete(oauthRefreshToken).where(eq(oauthRefreshToken.userId, userId)),
    db.delete(session).where(eq(session.userId, userId)),
  ];
}
```

In `src/lib/auth.ts` replace `customAccessTokenClaims: () => accessTokenClaims(),` with:

```ts
        customAccessTokenClaims: ({ user }) => accessTokenClaims(user),
```

In `src/app/api/community/admin/users/[id]/block/route.ts` add after line 5 (`import { user } from "@/lib/db/schema";`):

```ts
import { revokeOAuthAccess } from "@/lib/community/revoke-oauth-access";
```

and replace lines 23-27 (`const updated = await db ... .returning({ id: user.id });`) with:

```ts
  const setBlocked = db
    .update(user)
    .set({ blockedAt, blockedBy: body.blocked ? session.user.id : null, updatedAt: new Date() })
    .where(eq(user.id, id))
    .returning({ id: user.id });
  // One batch (one transaction on D1): a block that did not also revoke would leave
  // the user a refresh token and a live web session.
  const [updated] = await db.batch([setBlocked, ...(body.blocked ? revokeOAuthAccess(db, id) : [])]);
```

The lines after it (`if (updated.length === 0) { ... 404 }`) stay as they are.

- [ ] **Step 6: Run the unit tests and the spec.** The unit command of step 3: `ℹ pass 4`. The spec: `2 passed`. Re-run `tests/account-gate-tokens.spec.ts`: `8 passed` (a valid refresh is still never `invalid_grant`).

- [ ] **Step 7: Checks.** `npm test`, `npx tsc --noEmit`, `npx eslint src/lib/access-token-claims.ts src/lib/auth.ts src/lib/community/revoke-oauth-access.ts "src/app/api/community/admin/users/[id]/block/route.ts" tests/account-gate-block.spec.ts`. Expected: `ℹ fail 0`, no other output.

- [ ] **Step 8: Commit.**

```bash
git add src/lib/access-token-claims.ts src/lib/access-token-claims.test.ts src/lib/auth.ts src/lib/community/revoke-oauth-access.ts src/lib/community/revoke-oauth-access.test.ts "src/app/api/community/admin/users/[id]/block/route.ts" tests/account-gate-block.spec.ts
git commit -m "feat(auth): a block revokes the account's OAuth tokens and sessions; refresh answers invalid_grant" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 9: Offer the branch to the owner.** The `public/auth.md` and docs page statements that a block "revokes further OAuth token use" become true with this change; Task 8 describes it precisely.

### Task 5: The library and community routes accept the audience-bound JWT

**Files:**
- Create: `src/lib/community/verify-access-jwt.ts`, `src/lib/community/verify-access-jwt.test.ts`, `tests/account-gate-library.spec.ts`
- Modify: `package.json` (line 26) and `package-lock.json` (line 17), the `jose` dependency; `src/lib/community/get-authenticated-user.ts` (imports, lines 1-5; after line 62; append at the end); `src/lib/community/get-authenticated-user.test.ts` (resolver hook, the import line, append); `src/test-stubs.d.ts` (lines 4-7)

**Interfaces:** Produces `looksLikeJwt(token: string): boolean` and `verifyMonoagentAccessToken(token: string, now?: Date): Promise<{ userId: string; scopes: string[] } | null>`; `getRequestAuth` and `getAuthenticatedUser` keep their signatures and now resolve a JWT bearer. Consumes `getAuth().api.getJwks()`, `MONOAGENT_AUDIENCE`, `MONOAGENT_CLIENT_ID`, `authIssuer()` (Task 3), `jose`.

**Design.** Every route authenticates through `getRequestAuth` (community, library, MCP), so this is one change point. An opaque token is looked up by hash, unchanged. A JWT has no row: it is verified against this server's own JWKS for the issuer, the audience, expiry, one algorithm (EdDSA), `typ` `at+jwt` and the `monoagent` client claims. Those checks also reject the session JWT of `/api/auth/token` and the ID token, which carry the same key (S6). A token with three dot-separated parts is a JWT; opaque tokens (the provider's random letters, the claim route's base64url) have no dot, which Task 3's spec asserts. A blocked account is returned, as for opaque tokens, and every route already refuses it.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-library-jwt origin/main`. Task 3 must already be merged.

- [ ] **Step 2: Declare `jose`.** Production code now imports it directly (better-auth already depends on it, deduped at 6.2.9). Stop the dev server, then make two one-line edits by hand: in `package.json` after line 26 (`"isomorphic-dompurify": "^3.22.0",`) add `    "jose": "^6.2.9",`, and in `package-lock.json` after line 17 (the same entry under the root package's `dependencies`) add `        "jose": "^6.2.9",`. Do not run `npm install`: npm 12 also rewrites unrelated `dev` flags in the lockfile. Verify the pair is consistent and restart the server:

```bash
PUPPETEER_SKIP_DOWNLOAD=1 npm ci --no-audit --no-fund && grep '"version"' node_modules/jose/package.json && git diff --stat package.json package-lock.json
```

Expected: `added 784 packages`, `"version": "6.2.9"`, and two files changed with one insertion each.

- [ ] **Step 3: Write the failing tests.** `src/lib/community/verify-access-jwt.test.ts`:

```ts
import { describe, it, before } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { SignJWT, exportJWK, generateKeyPair, type JWK } from "jose";

// The verifier reads this server's own JWKS through getAuth().api.getJwks(); the stub serves
// whatever the test put in globalThis.
register(
  `data:text/javascript,
  export function resolve(specifier, context, next) {
    if (specifier === "@/lib/auth") {
      return {
        url: "data:text/javascript,export const getAuth = () => ({ api: { getJwks: async () => { if (globalThis.__jwksError) throw new Error('no key'); return globalThis.__jwks; } } });",
        shortCircuit: true,
      };
    }
    if (specifier === "@/lib/monoagent-token") return next("../monoagent-token.ts", context);
    return next(specifier, context);
  }`,
  import.meta.url,
);

const { looksLikeJwt, verifyMonoagentAccessToken } = await import("./verify-access-jwt.ts");
const { MONOAGENT_AUDIENCE, MONOAGENT_CLIENT_ID, authIssuer } = await import("../monoagent-token.ts");

const KID = "test-kid";
let privateKey: CryptoKey;
let publicJwk: JWK;

before(async () => {
  process.env.BETTER_AUTH_URL = "https://monoes.me";
  const pair = await generateKeyPair("EdDSA", { extractable: true });
  privateKey = pair.privateKey;
  publicJwk = { ...(await exportJWK(pair.publicKey)), kid: KID, alg: "EdDSA" };
  globalThis.__jwks = { keys: [publicJwk] };
  globalThis.__jwksError = false;
});

type Over = { iss?: string; aud?: string | string[]; sub?: string; azp?: string; client_id?: string; iat?: number; exp?: number };

// A token as the provider mints it for MonoAgent (the real audience shape: the audience, then the
// userinfo endpoint, because openid is requested); `over` changes one thing at a time.
async function mint(over: Over = {}, header: Record<string, unknown> = {}, key: CryptoKey = privateKey) {
  const now = Math.floor(Date.now() / 1000);
  const c = { iss: authIssuer(), aud: [MONOAGENT_AUDIENCE, `${authIssuer()}/oauth2/userinfo`], sub: "user-1", azp: MONOAGENT_CLIENT_ID, client_id: MONOAGENT_CLIENT_ID, iat: now, exp: now + 3600, ...over };
  const jwt = new SignJWT({ azp: c.azp, client_id: c.client_id, scope: "openid library:read library:write" })
    .setProtectedHeader({ alg: "EdDSA", kid: KID, typ: "at+jwt", ...header })
    .setIssuer(c.iss)
    .setAudience(c.aud)
    .setIssuedAt(c.iat)
    .setExpirationTime(c.exp);
  if (c.sub) jwt.setSubject(c.sub);
  return jwt.sign(key);
}

describe("looksLikeJwt", () => {
  it("tells a JWT from an opaque token by its three parts", () => {
    assert.deepEqual(["a.b.c", "kJ3x9QwY2LmN8aBcDeFgHiJkLmNoPqRs", "a.b", "a.b.c.d"].map(looksLikeJwt), [true, false, false, false]);
  });
});

describe("verifyMonoagentAccessToken", () => {
  it("accepts a token minted for the audience and returns its user and scopes", async () => {
    assert.deepEqual(await verifyMonoagentAccessToken(await mint()), { userId: "user-1", scopes: ["openid", "library:read", "library:write"] });
    assert.equal((await verifyMonoagentAccessToken(await mint({ aud: MONOAGENT_AUDIENCE })))?.userId, "user-1");
  });

  it("refuses a token that is wrong in any one way", async () => {
    const past = Math.floor(Date.now() / 1000) - 7200;
    const stranger = await generateKeyPair("EdDSA");
    const cases: [string, string][] = [
      ["another audience", await mint({ aud: "https://monoes.me/api/other" })],
      ["another issuer", await mint({ iss: "https://evil.example/api/auth" })],
      ["expired", await mint({ iat: past, exp: past + 3600 })],
      ["another client (azp)", await mint({ azp: "someone-else" })],
      ["another client (client_id)", await mint({ client_id: "someone-else" })],
      ["no subject", await mint({ sub: "" })],
      ["typ JWT, like a session JWT", await mint({}, { typ: "JWT" })],
      ["a key this server does not publish", await mint({}, {}, stranger.privateKey)],
      ["an unknown kid", await mint({}, { kid: "unknown-kid" })],
    ];
    for (const [name, token] of cases) assert.equal(await verifyMonoagentAccessToken(token), null, name);
  });

  it("accepts one algorithm only: no HMAC keyed with the public key, no alg none", async () => {
    const hmac = await new SignJWT({ azp: MONOAGENT_CLIENT_ID, client_id: MONOAGENT_CLIENT_ID })
      .setProtectedHeader({ alg: "HS256", kid: KID, typ: "at+jwt" })
      .setIssuer(authIssuer())
      .setAudience(MONOAGENT_AUDIENCE)
      .setSubject("user-1")
      .setExpirationTime("1h")
      .sign(new TextEncoder().encode(String(publicJwk.x)));
    const b64 = (v: object) => Buffer.from(JSON.stringify(v)).toString("base64url");
    const none = `${b64({ alg: "none", typ: "at+jwt" })}.${b64({ iss: authIssuer(), aud: MONOAGENT_AUDIENCE, sub: "user-1" })}.`;
    assert.deepEqual([await verifyMonoagentAccessToken(hmac), await verifyMonoagentAccessToken(none)], [null, null]);
  });

  it("verifies a token signed by either of two published keys, as during a rotation", async () => {
    const old = await generateKeyPair("EdDSA", { extractable: true });
    globalThis.__jwks = { keys: [publicJwk, { ...(await exportJWK(old.publicKey)), kid: "old-kid", alg: "EdDSA" }] };
    try {
      assert.equal((await verifyMonoagentAccessToken(await mint({}, { kid: "old-kid" }, old.privateKey)))?.userId, "user-1");
      assert.equal((await verifyMonoagentAccessToken(await mint()))?.userId, "user-1");
    } finally {
      globalThis.__jwks = { keys: [publicJwk] };
    }
  });

  it("refuses everything when the signing key is unavailable, instead of throwing", async () => {
    const token = await mint();
    globalThis.__jwksError = true;
    try {
      assert.equal(await verifyMonoagentAccessToken(token), null);
    } finally {
      globalThis.__jwksError = false;
    }
  });
});
```

`src/test-stubs.d.ts`: after line 7 (`var __stubCloudflareContext: () => unknown;`) add the four globals the new tests use:

```ts
  var __stubJwt: unknown;
  var __jwtCalls: number;
  var __jwks: unknown;
  var __jwksError: boolean;
```

`src/lib/community/get-authenticated-user.test.ts`: in the `register(...)` resolver add this case before the `@/lib/db` one:

```js
    if (specifier === "@/lib/community/verify-access-jwt") {
      return { url: "data:text/javascript,export const looksLikeJwt = (t) => t.split('.').length === 3; export const verifyMonoagentAccessToken = async () => { globalThis.__jwtCalls = (globalThis.__jwtCalls ?? 0) + 1; return globalThis.__stubJwt; };", shortCircuit: true };
    }
```

change the import line to `const { getAuthenticatedUser, getRequestAuth } = await import("./get-authenticated-user.ts");` and append to the end of the file:

```ts
describe("getAuthenticatedUser with an audience-bound MonoAgent token (a JWT)", () => {
  const request = (token: string) => new Request("http://localhost/api/library/me", { headers: { Authorization: `Bearer ${token}` } });
  const jwt = request("header.payload.signature");
  const verified = (scopes: string[]) => (globalThis.__stubJwt = { userId: "u6", scopes });
  const userRow = (row: unknown) => (globalThis.__stubDb = () => ({ select: mock.fn(() => selectChain(row ? [row] : [])) }));
  const member = { id: "u6", username: "agentuser", role: "member", blockedAt: null };

  it("resolves the user the verified token names, with the token's scopes, and checks the scope", async () => {
    globalThis.__stubSession = null;
    verified(["library:read", "library:write"]);
    userRow(member);
    const result = await getRequestAuth(jwt, "library:write");
    assert.deepEqual([result?.user.id, result?.scopes], ["u6", ["library:read", "library:write"]]);
    verified(["library:read"]);
    assert.equal(await getAuthenticatedUser(jwt, "library:write"), null);
  });

  it("rejects a token the verifier refuses without reading the database, and one whose user is gone", async () => {
    globalThis.__stubSession = null;
    globalThis.__stubJwt = null;
    globalThis.__stubDb = () => {
      throw new Error("the database must not be read");
    };
    assert.equal(await getAuthenticatedUser(jwt, "library:read"), null);
    verified(["library:read"]);
    userRow(undefined);
    assert.equal(await getAuthenticatedUser(jwt, "library:read"), null, "a verified token for a deleted user");
  });

  it("still returns a blocked user, as it does for opaque tokens: the routes refuse them", async () => {
    globalThis.__stubSession = null;
    verified(["library:read"]);
    userRow({ ...member, blockedAt: new Date() });
    assert.ok((await getAuthenticatedUser(jwt, "library:read"))?.user.blockedAt);
  });

  it("never sends an opaque token to the JWT verifier", async () => {
    globalThis.__stubSession = null;
    globalThis.__jwtCalls = 0;
    verified(["library:read"]);
    userRow(undefined);
    assert.equal(await getAuthenticatedUser(request("kJ3x9QwY2LmN8aBcDeFgHiJkLmNoPqRs"), "library:read"), null);
    assert.equal(globalThis.__jwtCalls, 0);
  });
});
```

`tests/account-gate-library.spec.ts`:

```ts
import { test, expect } from "@playwright/test";
import { generateKeyPairSync, sign } from "node:crypto";
import { AUDIENCE, authorize, bearer, decodeJwt, exchange, isJwt, login, pkce, signUp } from "./helpers/oauth-api";

// Once mono-agent's `library` commands use the machine session, every
// /api/library call carries the audience-bound JWT. The server must accept it
// beside the opaque tokens it has always accepted.

async function status(baseURL: string, path: string, token?: string, init: RequestInit = {}) {
  const headers = { ...(token ? bearer(token) : {}), ...(init.headers as Record<string, string> | undefined) };
  return (await fetch(new URL(path, baseURL), { ...init, headers })).status;
}

test("library endpoints accept the audience-bound JWT beside opaque tokens", async ({ baseURL }) => {
  const jwt = await login(baseURL!, { resource: AUDIENCE });
  const opaque = await login(baseURL!);
  expect(isJwt(jwt.body.access_token), "a JWT").toBe(true);
  expect(isJwt(opaque.body.access_token), "opaque").toBe(false);

  for (const token of [jwt.body.access_token!, opaque.body.access_token!]) {
    const me = await fetch(new URL("/api/library/me", baseURL), { headers: bearer(token) });
    expect(me.status).toBe(200);
    const body = (await me.json()) as { user: { email: string }; scopes: string[] };
    expect(body.scopes).toEqual(expect.arrayContaining(["library:read", "library:write"]));
    expect(await status(baseURL!, "/api/library/items?scope=mine", token)).toBe(200);
  }
  expect(await status(baseURL!, "/api/library/me")).toBe(401);
});

test("a read-only JWT lists but cannot upload (403 insufficient_scope)", async ({ baseURL }) => {
  const r = await login(baseURL!, { resource: AUDIENCE, scope: "openid library:read" });
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  expect(await status(baseURL!, "/api/library/items?scope=mine", r.body.access_token)).toBe(200);

  const form = new FormData();
  form.set("kind", "org");
  form.set("file", new Blob([JSON.stringify({ name: "x", roles: [{ id: "boss" }] })]), "x.json");
  const res = await fetch(new URL("/api/library/items", baseURL), { method: "POST", headers: bearer(r.body.access_token), body: form });
  expect(res.status).toBe(403);
  expect(((await res.json()) as { error: { code: string } }).error.code).toBe("insufficient_scope");
});

test("a damaged, unsigned or foreign JWT is a 401", async ({ baseURL }) => {
  const r = await login(baseURL!, { resource: AUDIENCE });
  const [h, p, s] = r.body.access_token!.split(".");

  const damaged = `${h}.${p}.${s.slice(0, -2)}${s.endsWith("AA") ? "BB" : "AA"}`;
  expect(await status(baseURL!, "/api/library/me", damaged)).toBe(401);

  const b64 = (v: object) => Buffer.from(JSON.stringify(v)).toString("base64url");
  const unsigned = `${b64({ alg: "none", typ: "at+jwt" })}.${p}.`;
  expect(await status(baseURL!, "/api/library/me", unsigned)).toBe(401);

  // The right claims under a key this server does not publish.
  const { header, payload } = decodeJwt(r.body.access_token!);
  const forged = `${b64(header)}.${b64(payload)}`;
  const foreign = `${forged}.${sign(null, Buffer.from(forged), generateKeyPairSync("ed25519").privateKey).toString("base64url")}`;
  expect(await status(baseURL!, "/api/library/me", foreign)).toBe(401);
});

test("a dynamically registered client still gets an opaque token and the library accepts it, as before", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  const reg = await fetch(new URL("/api/auth/oauth2/register", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ redirect_uris: ["https://example-agent.test/callback"], token_endpoint_auth_method: "none", grant_types: ["authorization_code", "refresh_token"] }),
  });
  const { client_id: clientId } = (await reg.json()) as { client_id: string };
  const { verifier, challenge } = pkce();
  const code = await authorize(account, challenge, { clientId, redirectUri: "https://example-agent.test/callback", scope: "library:read" });
  const t = await exchange(baseURL!, code, verifier, undefined, "https://example-agent.test/callback", clientId);
  expect(t.status, JSON.stringify(t.body)).toBe(200);
  // Opaque, as for every client that sends no resource; it works as it always has.
  expect(isJwt(t.body.access_token), "opaque").toBe(false);
  expect(await status(baseURL!, "/api/library/me", t.body.access_token)).toBe(200);
});
```

- [ ] **Step 4: Run them, red.** `node --experimental-strip-types --test src/lib/community/verify-access-jwt.test.ts src/lib/community/get-authenticated-user.test.ts`. Expected: FAIL; `Cannot find module '<repo>/src/lib/community/verify-access-jwt.ts'` for the first file and, in the second, `ℹ fail 3` of the new JWT tests (the opaque path hashes the JWT and finds no row). Spec: `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-library.spec.ts --reporter=line`. Expected: `2 failed, 2 passed`: the JWT tests fail at `expect(me.status).toBe(200)`, `Expected: 200`, `Received: 401`.

- [ ] **Step 5: Implement.** `src/lib/community/verify-access-jwt.ts`:

```ts
import { createLocalJWKSet, jwtVerify, type JSONWebKeySet } from "jose";
import { getAuth } from "@/lib/auth";
import { MONOAGENT_AUDIENCE, MONOAGENT_CLIENT_ID, authIssuer } from "@/lib/monoagent-token";

// Opaque access tokens are random letters and digits, so a token with three
// dot-separated parts is a JWT.
export function looksLikeJwt(token: string): boolean {
  return token.split(".").length === 3;
}

/**
 * Verifies an audience-bound MonoAgent access token (a JWT the oauth-provider
 * minted for a `resource`): signature against this server's own JWKS, issuer,
 * audience, expiry, one algorithm, the at+jwt type and the monoagent client.
 * Returns who it is for and its scopes, or null for anything else. Whether the
 * account is blocked is the caller's business, as it is for opaque tokens.
 */
export async function verifyMonoagentAccessToken(
  token: string,
  now: Date = new Date(),
): Promise<{ userId: string; scopes: string[] } | null> {
  let jwks: JSONWebKeySet;
  try {
    jwks = (await getAuth().api.getJwks()) as JSONWebKeySet;
  } catch (error) {
    console.error("access token verification: no signing key available", error instanceof Error ? error.message : error);
    return null;
  }
  try {
    const { payload } = await jwtVerify(token, createLocalJWKSet(jwks), {
      issuer: authIssuer(),
      audience: MONOAGENT_AUDIENCE,
      algorithms: ["EdDSA"],
      typ: "at+jwt",
      currentDate: now,
    });
    if (payload.azp !== MONOAGENT_CLIENT_ID || payload.client_id !== MONOAGENT_CLIENT_ID) return null;
    if (typeof payload.sub !== "string" || payload.sub.length === 0) return null;
    const scopes = typeof payload.scope === "string" ? payload.scope.split(" ").filter(Boolean) : [];
    return { userId: payload.sub, scopes };
  } catch {
    return null;
  }
}
```

In `src/lib/community/get-authenticated-user.ts` add after line 5 (`import { sha256Base64Url } from "@/lib/community/hash-token";`):

```ts
import { looksLikeJwt, verifyMonoagentAccessToken } from "@/lib/community/verify-access-jwt";
```

add after line 62 (`if (!bearerValue) return null;`):

```ts

  // An audience-bound MonoAgent token is a JWT with no stored row to look up.
  if (looksLikeJwt(bearerValue)) return jwtRequestAuth(bearerValue, requiredScope);
```

and append at the end of the file (after the closing brace of `getRequestAuth`, line 104):

```ts
async function jwtRequestAuth(
  token: string,
  requiredScope?: string,
): Promise<{ user: AuthenticatedUser; scopes: string[] } | null> {
  const verified = await verifyMonoagentAccessToken(token);
  if (!verified) return null;
  if (requiredScope && !verified.scopes.includes(requiredScope)) return null;

  const [row] = await getDb()
    .select({ id: user.id, username: user.username, role: user.role, blockedAt: user.blockedAt })
    .from(user)
    .where(eq(user.id, verified.userId))
    .limit(1);
  if (!row) return null;

  return { user: row as AuthenticatedUser, scopes: verified.scopes };
}
```

- [ ] **Step 6: Run everything.** The unit command of step 4: all pass (`ℹ pass 18`, `ℹ fail 0`). The spec: `4 passed`. The existing OAuth specs need a browser; with one available also run `tests/library-oauth.spec.ts tests/oauth.spec.ts tests/mcp.spec.ts` (all pass: today's client and MCP use opaque tokens, unchanged).

- [ ] **Step 7: Checks.** `npm test`, `npx tsc --noEmit`, `npx eslint src/lib/community/verify-access-jwt.ts src/lib/community/get-authenticated-user.ts tests/account-gate-library.spec.ts`: `ℹ fail 0`, no other output.

- [ ] **Step 8: Commit.**

```bash
git add package.json package-lock.json src/lib/community/verify-access-jwt.ts src/lib/community/verify-access-jwt.test.ts src/lib/community/get-authenticated-user.ts src/lib/community/get-authenticated-user.test.ts src/test-stubs.d.ts tests/account-gate-library.spec.ts
git commit -m "feat(auth): accept audience-bound MonoAgent JWTs beside opaque tokens" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 9: Offer the branch to the owner.**

### Task 6: A signing key the client can pin (S1)

**What the findings must show first (Task 2, S1).** (1) The default key is one non-rotating Ed25519 key that a verifier holding only the public JWK can check, so the fallback below works. (2) The installed plugin exposes `adapter.getJwks`, `adapter.createJwk`, `jwks.disablePrivateKeyEncryption` and `disableSettingJwtHeader`: the four-match grep of Task 2 step 7. If either is false, stop and do the fallback (end of this task) instead of steps 2 to 10.

**Files:**
- Create: `src/lib/signing-key.ts`, `src/lib/signing-key.test.ts`, `scripts/generate-signing-key.ts`
- Modify: `src/lib/auth.ts` (the imports; the line that destructures `process.env`; the line `jwt(),`)

**Interfaces:** Produces `SIGNING_KEY_ENV = "MONOAGENT_JWT_PRIVATE_JWK"`, `PREVIOUS_KEY_ENV = "MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK"`, `parsePrivateJwk(raw): { kty: "OKP"; crv: "Ed25519"; x: string; d: string }`, `parsePublicJwk(raw)`, `jwkThumbprint(jwk): Promise<string>`, `loadSigningKey(raw): Promise<Jwk>`, `signingKeyAdapter(raw: string | undefined, production: boolean, previous?: string): JwtOptions["adapter"] | undefined`. Consumes the `Jwk` and `JwtOptions` types of `better-auth/plugins` and `sha256Base64Url`.

**Design.** Three modes. Secret unset, base URL not https (local, CI, tests): `undefined`, the plugin keeps its own database key, exactly as today. Secret unset, base URL https (a real deployment): fail closed; `getJwks` and `createJwk` throw, so token issuance answers 500 with `MONOAGENT_JWT_PRIVATE_JWK is not set; refusing to sign with a key nobody pins`, never a silent key no release pins. Secret set: `getJwks` returns exactly that key, the `kid` is its RFC 7638 thumbprint, and `createJwk` refuses. During a rotation the optional public half of the previous key is also returned with an `expiresAt` just in the past: the plugin skips expired keys when it picks the signing key, while `/jwks` keeps publishing it for its 30-day grace, so tokens issued before the swap still verify here. `disableSettingJwtHeader: true` stops the plugin signing a session JWT on every `get-session` (nothing reads it); without it a missing or malformed key would break every authenticated page, not only the token endpoint.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-signing-key origin/main`. Tasks 3 to 5 must already be merged.

- [ ] **Step 2: Write the failing unit tests.** `src/lib/signing-key.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { generateKeyPairSync } from "node:crypto";
import { SignJWT, importJWK, jwtVerify } from "jose";

register(
  `data:text/javascript,
  export function resolve(specifier, context, next) {
    if (specifier === "@/lib/community/hash-token") return next("./community/hash-token.ts", context);
    return next(specifier, context);
  }`,
  import.meta.url,
);

const { SIGNING_KEY_ENV, PREVIOUS_KEY_ENV, parsePrivateJwk, jwkThumbprint, loadSigningKey, signingKeyAdapter } = await import("./signing-key.ts");

function throwawayKey() {
  const jwk = generateKeyPairSync("ed25519").privateKey.export({ format: "jwk" }) as { kty: string; crv: string; x: string; d: string };
  return { jwk, raw: JSON.stringify({ kty: jwk.kty, crv: jwk.crv, x: jwk.x, d: jwk.d }) };
}
const publicHalf = (k: { jwk: { x: string } }) => JSON.stringify({ kty: "OKP", crv: "Ed25519", x: k.jwk.x });
const keysOf = async (raw: string | undefined, production: boolean, previous?: string) =>
  (await signingKeyAdapter(raw, production, previous)!.getJwks!({} as never)) ?? [];

describe("the key", () => {
  it("is named by its RFC 7638 thumbprint (the RFC 8037 appendix A.3 vector)", async () => {
    assert.equal(
      await jwkThumbprint({ crv: "Ed25519", kty: "OKP", x: "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo" }),
      "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k",
    );
  });

  it("is refused when it is not an Ed25519 private JWK, and the message never echoes it", () => {
    const { jwk } = throwawayKey();
    const secretLooking = "SECRET-LOOKING-d-VALUE-0123456789abcdefghijklmnopqrstuv";
    const bad = [
      "not json",
      JSON.stringify({ kty: "RSA", crv: "Ed25519", x: jwk.x, d: jwk.d }),
      JSON.stringify({ kty: "OKP", crv: "P-256", x: jwk.x, d: jwk.d }),
      JSON.stringify({ kty: "OKP", crv: "Ed25519", d: jwk.d }),
      JSON.stringify({ kty: "OKP", crv: "Ed25519", x: "short", d: jwk.d }),
      JSON.stringify({ kty: "OKP", crv: "Ed25519", x: jwk.x }),
      JSON.stringify({ kty: "OKP", crv: "Ed25519", x: jwk.x, d: secretLooking }),
    ];
    for (const raw of bad) {
      assert.throws(() => parsePrivateJwk(raw), (err: Error) => {
        assert.ok(err.message.includes(SIGNING_KEY_ENV) && !err.message.includes(secretLooking) && !err.message.includes(jwk.d));
        return true;
      });
    }
  });

  it("signs and verifies, which is what mono-agent does with the public half, and keeps d out of it", async () => {
    const { raw, jwk } = throwawayKey();
    const key = await loadSigningKey(raw);
    assert.equal(key.id, await jwkThumbprint(jwk));
    assert.deepEqual(JSON.parse(key.publicKey), { kty: "OKP", crv: "Ed25519", x: jwk.x });
    const token = await new SignJWT({ plan: "free" })
      .setProtectedHeader({ alg: "EdDSA", kid: key.id, typ: "at+jwt" })
      .setIssuer("https://monoes.me/api/auth")
      .setExpirationTime("1h")
      .sign(await importJWK(JSON.parse(key.privateKey), "EdDSA"));
    const { payload, protectedHeader } = await jwtVerify(token, await importJWK(JSON.parse(key.publicKey), "EdDSA"), {
      algorithms: ["EdDSA"],
      issuer: "https://monoes.me/api/auth",
    });
    assert.deepEqual([payload.plan, protectedHeader.kid], ["free", key.id]);
  });
});

describe("signingKeyAdapter", () => {
  it("leaves the plugin's own database key alone in development and tests", () => {
    assert.equal(signingKeyAdapter(undefined, false), undefined);
  });

  it("fails closed in production without the key: nothing is signed with a key nobody pins", async () => {
    const adapter = signingKeyAdapter(undefined, true)!;
    await assert.rejects(adapter.getJwks!({} as never), new RegExp(SIGNING_KEY_ENV));
    await assert.rejects(adapter.createJwk!({} as never, {} as never), /never generated/);
  });

  it("reports a malformed key when it is used, not when the server starts", async () => {
    await assert.rejects(keysOf("{}", false), new RegExp(SIGNING_KEY_ENV));
  });

  it("publishes exactly the configured key and refuses to generate another", async () => {
    const { raw, jwk } = throwawayKey();
    for (const production of [false, true]) {
      assert.deepEqual((await keysOf(raw, production)).map((k) => k.id), [await jwkThumbprint(jwk)]);
      await assert.rejects(signingKeyAdapter(raw, production)!.createJwk!({} as never, {} as never), /never generated/);
    }
  });

  it("publishes the previous public key during a rotation, but never signs with it", async () => {
    const current = throwawayKey();
    const previous = throwawayKey();
    const keys = await keysOf(current.raw, true, publicHalf(previous));
    assert.deepEqual(keys.map((k) => k.id), [await jwkThumbprint(current.jwk), await jwkThumbprint(previous.jwk)]);
    // The plugin signs with the newest key that has not expired...
    assert.deepEqual(keys.filter((k) => !k.expiresAt || k.expiresAt > new Date()).map((k) => k.id), [await jwkThumbprint(current.jwk)]);
    // ...and /jwks keeps publishing an expired key for 30 days after its expiresAt.
    assert.ok(keys.every((k) => !k.expiresAt || k.expiresAt.getTime() + 30 * 24 * 3600 * 1000 > Date.now()));
    assert.equal(JSON.parse(keys[1].privateKey).d, undefined, "no private half for the previous key");
    assert.equal((await keysOf(current.raw, true, publicHalf(current))).length, 1, "the same key is listed once");
  });

  it("refuses a previous key that is not a public Ed25519 JWK, and never echoes it", async () => {
    const current = throwawayKey();
    for (const bad of ["not json", JSON.stringify({ kty: "OKP", crv: "Ed25519", x: "short" }), current.raw]) {
      await assert.rejects(keysOf(current.raw, true, bad), (err: Error) => err.message.includes(PREVIOUS_KEY_ENV) && !err.message.includes(current.jwk.d));
    }
  });
});
```

Run `node --experimental-strip-types --test src/lib/signing-key.test.ts`. Expected: FAIL, `Cannot find module '<repo>/src/lib/signing-key.ts'`.

- [ ] **Step 3: Implement.** `src/lib/signing-key.ts`:

```ts
import type { Jwk, JwtOptions } from "better-auth/plugins";
import { sha256Base64Url } from "@/lib/community/hash-token";

// The key every MonoAgent access token is signed with. mono-agent releases pin
// the matching public key (internal/account/keys.go in github.com/monoes/mono-agent),
// so this key is chosen by an operator and supplied as a secret; it is never a
// row the jwt plugin generates by itself. Generate one with
// scripts/generate-signing-key.ts.
export const SIGNING_KEY_ENV = "MONOAGENT_JWT_PRIVATE_JWK";

// During a rotation: the key tokens were signed with until the swap, public half
// only. It is published at /jwks, so this server still accepts the tokens issued
// in the last hour, and it never signs.
export const PREVIOUS_KEY_ENV = "MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK";

type Ed25519PrivateJwk = { kty: "OKP"; crv: "Ed25519"; x: string; d: string };

// 32 bytes, unpadded base64url.
const KEY_PART = /^[A-Za-z0-9_-]{43}$/;

// The messages never contain the value: this module only ever sees a private key.
export function parsePrivateJwk(raw: string): Ed25519PrivateJwk {
  let jwk: Partial<Ed25519PrivateJwk>;
  try {
    jwk = JSON.parse(raw) as Partial<Ed25519PrivateJwk>;
  } catch {
    throw new Error(`${SIGNING_KEY_ENV} is not JSON`);
  }
  if (jwk?.kty !== "OKP" || jwk.crv !== "Ed25519") throw new Error(`${SIGNING_KEY_ENV} is not an Ed25519 (OKP) JWK`);
  if (typeof jwk.x !== "string" || !KEY_PART.test(jwk.x)) throw new Error(`${SIGNING_KEY_ENV} has no valid public part (x)`);
  if (typeof jwk.d !== "string" || !KEY_PART.test(jwk.d)) throw new Error(`${SIGNING_KEY_ENV} has no valid private part (d)`);
  return { kty: jwk.kty, crv: jwk.crv, x: jwk.x, d: jwk.d };
}

export function parsePublicJwk(raw: string): Omit<Ed25519PrivateJwk, "d"> {
  let jwk: Partial<Ed25519PrivateJwk>;
  try {
    jwk = JSON.parse(raw) as Partial<Ed25519PrivateJwk>;
  } catch {
    throw new Error(`${PREVIOUS_KEY_ENV} is not JSON`);
  }
  if (jwk?.kty !== "OKP" || jwk.crv !== "Ed25519" || typeof jwk.x !== "string" || !KEY_PART.test(jwk.x)) {
    throw new Error(`${PREVIOUS_KEY_ENV} is not an Ed25519 (OKP) public JWK`);
  }
  if ("d" in jwk) throw new Error(`${PREVIOUS_KEY_ENV} must be the public half only`);
  return { kty: jwk.kty, crv: jwk.crv, x: jwk.x };
}

// RFC 7638 thumbprint of the public half: members in lexicographic order, no
// whitespace. The `kid` of every token is this value.
export function jwkThumbprint(jwk: { crv: string; kty: string; x: string }): Promise<string> {
  return sha256Base64Url(`{"crv":"${jwk.crv}","kty":"${jwk.kty}","x":"${jwk.x}"}`);
}

export async function loadSigningKey(raw: string): Promise<Jwk> {
  const jwk = parsePrivateJwk(raw);
  return {
    id: await jwkThumbprint(jwk),
    publicKey: JSON.stringify({ kty: jwk.kty, crv: jwk.crv, x: jwk.x }),
    privateKey: JSON.stringify(jwk),
    createdAt: new Date(0),
    alg: "EdDSA",
    crv: "Ed25519",
  };
}

// Published but never chosen to sign: the plugin skips keys whose expiresAt has
// passed when it picks the signing key, and /jwks keeps publishing them for its
// 30-day grace period, counted from expiresAt.
async function loadPreviousKey(raw: string): Promise<Jwk> {
  const jwk = parsePublicJwk(raw);
  return {
    id: await jwkThumbprint(jwk),
    publicKey: JSON.stringify(jwk),
    privateKey: "{}",
    createdAt: new Date(0),
    expiresAt: new Date(Date.now() - 1000),
    alg: "EdDSA",
    crv: "Ed25519",
  };
}

/**
 * The jwt plugin's `adapter` option when the signing key is configured:
 * `getJwks` returns that key (the plugin signs with it, and /jwks publishes it),
 * plus the previous public key during a rotation, and `createJwk` refuses, so no
 * key is ever generated.
 *
 * With no key configured, local development and tests keep the plugin's own
 * database key (undefined = the plugin default). Production fails closed: a
 * deploy without the secret must not silently sign with a key no mono-agent
 * release pins.
 */
export function signingKeyAdapter(
  raw: string | undefined,
  production: boolean,
  previous?: string,
): JwtOptions["adapter"] | undefined {
  if (!raw && !production) return undefined;
  const missing = () => new Error(`${SIGNING_KEY_ENV} is not set; refusing to sign with a key nobody pins`);
  return {
    getJwks: async () => {
      if (!raw) throw missing();
      const current = await loadSigningKey(raw);
      if (!previous) return [current];
      const old = await loadPreviousKey(previous);
      return old.id === current.id ? [current] : [current, old];
    },
    createJwk: async () => {
      throw new Error(`signing keys are configured through ${SIGNING_KEY_ENV}, never generated`);
    },
  };
}
```

Run the test again: `ℹ pass 9`.

- [ ] **Step 4: The generator.** `scripts/generate-signing-key.ts` prints the private key to stdout only, so it can be piped and never shown, and the kid and public key to stderr:

```ts
// Generates a MonoAgent access-token signing key (Ed25519).
//
//   npx tsx scripts/generate-signing-key.ts | npx wrangler secret put MONOAGENT_JWT_PRIVATE_JWK
//
// stdout: the PRIVATE key as a JWK, nothing else, so it can be piped into
// `wrangler secret put` and never appears on screen or in a log.
// stderr: the kid and the PUBLIC key, for internal/account/keys.go in
// github.com/monoes/mono-agent, and the public JWK that goes into
// MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK once a later key has replaced this one.
import { generateKeyPairSync } from "node:crypto";
import { jwkThumbprint } from "../src/lib/signing-key";

async function main() {
  const { privateKey } = generateKeyPairSync("ed25519");
  const jwk = privateKey.export({ format: "jwk" }) as { kty: string; crv: string; x: string; d: string };
  const kid = await jwkThumbprint(jwk);
  process.stdout.write(JSON.stringify({ kty: jwk.kty, crv: jwk.crv, x: jwk.x, d: jwk.d }));
  process.stderr.write(`\nkid:        ${kid}\npublic key: ${jwk.x} (Ed25519, base64url)\n`);
  process.stderr.write(`public JWK: ${JSON.stringify({ kty: jwk.kty, crv: jwk.crv, x: jwk.x })}\n`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Generate a throwaway local key outside the repository and look only at stderr:

```bash
KEYDIR="$(mktemp -d)"
npx tsx scripts/generate-signing-key.ts > "$KEYDIR/key-a.json" 2> "$KEYDIR/key-a.txt"; cat "$KEYDIR/key-a.txt"
```

Expected: `kid:` (43 characters), `public key:` (43 characters), `public JWK: {"kty":"OKP","crv":"Ed25519","x":"..."}`.

- [ ] **Step 4b: Red at the HTTP level.** Give the server the key and ask for it, before the wiring exists:

```bash
printf "MONOAGENT_JWT_PRIVATE_JWK='%s'\n" "$(cat "$KEYDIR/key-a.json")" >> .env.local
```

Restart the dev server (Ctrl-C it, then `npx next dev -p 3107` again; only one `next dev` can run per directory) and run:

```bash
E2E_BASE_URL=http://localhost:3107 E2E_SIGNING_KID=<the kid from key-a.txt> npx playwright test tests/account-gate-tokens.spec.ts --reporter=line
```

Expected: `1 failed, 7 passed`; the failure is `expect(header.kid).toBe(process.env.E2E_SIGNING_KID)`, `Received:` a 32-character id (the plugin's own key).

- [ ] **Step 5: Wire `src/lib/auth.ts`.** After the import of `accessTokenClaims` add:

```ts
import { PREVIOUS_KEY_ENV, SIGNING_KEY_ENV, signingKeyAdapter } from "@/lib/signing-key";
```

after the `const { BETTER_AUTH_SECRET: sec, ... } = process.env;` line add:

```ts
  // A public https base URL means a real deployment, which must not run without the pinned key.
  const signingKey = signingKeyAdapter(
    process.env[SIGNING_KEY_ENV],
    (url ?? "").startsWith("https://"),
    process.env[PREVIOUS_KEY_ENV] || undefined,
  );
```

and replace the line `jwt(),` with:

```ts
      jwt({
        adapter: signingKey,
        jwks: signingKey ? { disablePrivateKeyEncryption: true } : undefined,
        // The plugin signs a session JWT on every get-session call by default; nothing reads it.
        disableSettingJwtHeader: true,
      }),
```

- [ ] **Step 6: Pinned mode, green.** The server hot-reloads. Run the spec of step 4b again: `8 passed`. Then `npx tsx scripts/spikes/s1-key.ts <kid>`. Expected:

```
JWKS: <kid> OKP/Ed25519 EdDSA
access token: alg=EdDSA typ=at+jwt kid=<kid>
verifies with only the published public key (node:crypto, Ed25519): true
id token signed by the same key: true
after a refresh: 200, kid <kid>
jwks table rows: N before, N after (a row appears only when the plugin generates a key)
PINNED KEY CHECK: PASS (expected <kid>)
```

(`N` is whatever the table already held, 1 after Task 2; the same number before and after: no row is added.)

- [ ] **Step 7: Rehearse a rotation.** Create `scripts/spikes/rotation.ts`:

```ts
// Rehearses a signing-key rotation against the local server:
//   npx tsx scripts/spikes/rotation.ts mint     (server signs with key A)
//   npx tsx scripts/spikes/rotation.ts check refresh   (after the swap to key B; `refresh` also rotates the old refresh token)
//   npx tsx scripts/spikes/rotation.ts check           (after dropping key A)
// Tokens are throwaway local ones; the state file stays in this directory and is never committed.
import { readFileSync, writeFileSync } from "node:fs";
import { AUDIENCE, bearer, decodeJwt, login, refresh } from "../../tests/helpers/oauth-api";

const base = process.env.SPIKE_BASE_URL ?? "http://localhost:3107";
const state = new URL("./.rotation-state.json", import.meta.url);

async function kids(): Promise<string[]> {
  const { keys } = (await (await fetch(new URL("/api/auth/jwks", base))).json()) as { keys: { kid: string }[] };
  return keys.map((k) => k.kid);
}

async function main() {
  if (process.argv[2] === "mint") {
    const r = await login(base, { resource: AUDIENCE });
    writeFileSync(state, JSON.stringify({ access: r.body.access_token, refresh: r.body.refresh_token }));
    console.log(`minted under kid ${decodeJwt(r.body.access_token!).header.kid}; JWKS: ${(await kids()).join(", ")}`);
    return;
  }
  const old = JSON.parse(readFileSync(state, "utf8")) as { access: string; refresh: string };
  console.log(`JWKS now: ${(await kids()).join(", ")}`);
  const fresh = await login(base, { resource: AUDIENCE });
  console.log(`a fresh token carries kid ${decodeJwt(fresh.body.access_token!).header.kid}`);
  const me = await fetch(new URL("/api/library/me", base), { headers: bearer(old.access) });
  console.log(`the token minted before the swap on /api/library/me: ${me.status}`);
  if (process.argv[3] === "refresh") {
    const again = await refresh(base, old.refresh, AUDIENCE); // rotates it: do this once
    console.log(`its refresh token: ${again.status}, new kid ${again.body.access_token ? decodeJwt(again.body.access_token).header.kid : "-"}`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
```

Phase 1, the server signs with key A: `npx tsx scripts/spikes/rotation.ts mint`. Phase 2, swap to key B with A published as previous (restart the server with these two variables on its command line; they beat the file):

```bash
npx tsx scripts/generate-signing-key.ts > "$KEYDIR/key-b.json" 2> "$KEYDIR/key-b.txt"
MONOAGENT_JWT_PRIVATE_JWK="$(cat "$KEYDIR/key-b.json")" MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK='<the "public JWK:" line of key-a.txt>' npx next dev -p 3107
npx tsx scripts/spikes/rotation.ts check refresh
```

Expected: `JWKS now: <B>, <A>`; `a fresh token carries kid <B>`; `the token minted before the swap on /api/library/me: 200`; `its refresh token: 200, new kid <B>`. Phase 3, drop A (restart with `MONOAGENT_JWT_PRIVATE_JWK` set to B and `MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK=` empty), `npx tsx scripts/spikes/rotation.ts check`. Expected: `JWKS now: <B>`; a fresh token carries `<B>`; the old token answers `401`.

- [ ] **Step 8: Fail closed.** A server that declares an https base URL and has no key must not sign. Stop the 3107 server first (Next 16 allows one `next dev` per directory), then:

```bash
BETTER_AUTH_URL=https://monoes.me MONOAGENT_JWT_PRIVATE_JWK= npx next dev -p 3108      # second terminal
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3108/api/auth/jwks
```

Expected: `500`, and the server log holds `MONOAGENT_JWT_PRIVATE_JWK is not set; refusing to sign with a key nobody pins`. Stop it and restart it with `MONOAGENT_JWT_PRIVATE_JWK="$(cat "$KEYDIR/key-a.json")"` added to the same command line: `curl -s http://localhost:3108/api/auth/jwks` returns exactly one key whose `kid` is key A's. Stop it.

- [ ] **Step 9: Unpinned mode is today's behavior.** Restart the 3107 server with an empty `MONOAGENT_JWT_PRIVATE_JWK=` on its command line (an empty value beats the file) and run the three account-gate specs without `E2E_SIGNING_KID`: `tests/account-gate-tokens.spec.ts tests/account-gate-library.spec.ts tests/account-gate-block.spec.ts`. Expected: all pass (this is how a fresh checkout and CI run).

- [ ] **Step 10: Checks and commit.** `npm test` (`ℹ fail 0`), `npx tsc --noEmit`, `npx eslint src/lib/auth.ts src/lib/signing-key.ts scripts/generate-signing-key.ts`.

```bash
git add src/lib/auth.ts src/lib/signing-key.ts src/lib/signing-key.test.ts scripts/generate-signing-key.ts
git commit -m "feat(auth): sign access tokens with a pinned Ed25519 key from a Worker secret" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 11 (OWNER-RUN, O3): put the key in place BEFORE this pull request merges.** After the merge a deployment without the secret refuses every token request that signs (every login with `openid`, today's client included) with a 500. The Worker's secret store cannot be read back, so the private half exists only there: a lost secret means a new key and a client release.

```bash
npx tsx scripts/generate-signing-key.ts | npx wrangler secret put MONOAGENT_JWT_PRIVATE_JWK
npx wrangler secret list
```

`wrangler secret put` reads the value from piped stdin and never prints it. Write down the `kid` and `public key` the generator printed on stderr: they go into `internal/account/keys_default.go` (B1a). Expected: `secret list` includes `MONOAGENT_JWT_PRIVATE_JWK`.

- [ ] **Step 12 (OWNER-RUN, O4): check after the deploy.**

```bash
curl -s https://monoes.me/api/auth/jwks
```

Expected: exactly one key and its `kid` is the one from O3. Then sign in once with the current MonoAgent client (`monoagentcli library login`) to see that today's flow still answers. To roll back, revert the pull request: signing returns to the database key (`GB6kESA9qO98637VArEGR2EjW6wSyYqO` at planning time); the secret may stay.

- [ ] **Step 13 (OWNER-RUN, O5): the rotation runbook** (spec §4.7). Keep the next key's private half in a password manager from the moment it is generated (the Worker cannot give it back), never in git.
  1. `npx tsx scripts/generate-signing-key.ts > next.json 2> next.txt`, outside the repository.
  2. Ship its `kid` and public key in a mono-agent release (`internal/account/keys.go`, current plus next).
  3. Wait the agreed period (eight weeks suggested): clients that never updated lock with `key_unknown`.
  4. Swap, previous first so tokens issued before the swap keep verifying: `echo '<the public JWK of the old key>' | npx wrangler secret put MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK`, then `npx wrangler secret put MONOAGENT_JWT_PRIVATE_JWK < next.json`.
  5. Check `curl -s https://monoes.me/api/auth/jwks`: the new `kid` first, the old one second.
  6. After at least one hour (the longest access token lifetime), `npx wrangler secret delete MONOAGENT_JWT_PREVIOUS_PUBLIC_JWK`, and ship a release that drops the old key.

**Fallback (use it only if the precondition at the top fails): pin the key the plugin already generated.** Production publishes one non-rotating Ed25519 key (`https://monoes.me/api/auth/jwks`, `kid` `GB6kESA9qO98637VArEGR2EjW6wSyYqO` at planning time; its public half is public). (a) B1a pins that `kid` and public key. (b) Add `drizzle/0018_pin_monoagent_signing_key.sql` (generated with `drizzle-kit generate --custom --name=pin_monoagent_signing_key`) containing `UPDATE oauth_resource SET signing_key_id = '<that kid>' WHERE identifier = 'https://monoes.me/api/monoagent';`: audience-bound tokens are then always signed with that key even if another key row ever appears. (c) Never set `jwks.rotationInterval`; add a test that reads `src/lib/auth.ts` and fails if it contains `rotationInterval`. (d) The weakness: spec §4.7 step 1 cannot happen, because the next key is unknown until the server creates it, so a rotation locks clients that have not updated for as long as the gap between the two releases.

### Task 7: The email-code route issues the MonoAgent refresh token and honors `resource` (headless sign-in)

**Beyond the spec's list, and why.** Spec D10 and §9 make headless sign-in `account login --email`, and §5 / D19 list no server change for it; the lead approved this task as the one server change beyond that list. Today `POST /api/auth/agent/claim/verify` inserts a one-hour opaque access token by hand and returns it alone (`src/app/api/auth/agent/claim/verify/route.ts`, lines 92-112): no refresh token, no JWT, and `resource` is ignored. A gate session is a refresh token, so that path cannot start one and `account login --email` would end in an error. The route now serves a client in either of two ways, and the client plans may use either. (1) A MonoAgent claim that includes `offline_access` (every claim the client sends) gets a `refresh_token` in the answer: a row of the table the provider's refresh grant reads, which the client trades at the token endpoint with `resource`, like an adopted old session (S2). (2) A body that carries `resource` gets that trade done at once through `auth.api.oauth2Token`, and the answer is the token endpoint's own. Either way the JWT, its claims, the signing key and the blocked-account guard are the provider's, and the opaque token of the plain answer stays what it was. If the exchange fails, the refresh row written for it is unreachable (its value never left the server) and expires in 30 days.

Three things to know before shipping. A correct code used to buy one hour and now buys a self-renewing session (each rotation slides the 30 days); the existing guards stay: a 6-digit code, 5 attempts per code, 10 minutes, 3 outstanding codes per email per hour. Today's released client already asks for `offline_access` (`internal/library/types.go:24`, sent at `internal/library/auth.go:333`) and parses a `refresh_token` in this answer (`internal/library/auth.go:118-127`, `:350-363`), so after this task it stores one after an emailed-code login (`tokenFrom`, `:158-160`), as it does after a browser login, and refreshes it at the token endpoint with no `resource` (`:176-178`); the claim spec pins that refresh. And a blocked account is refused here too; before, the route did not look.

**Files:**
- Create: `tests/account-gate-claim.spec.ts`
- Modify: `src/app/api/auth/agent/claim/verify/route.ts` (imports, lines 1-5; line 7; line 33; before line 43; lines 87-90; after line 96; after line 101; lines 107-112), `src/app/api/auth/agent/claim/verify/route.test.ts` (resolver, lines 12 and 16; appended tests), `public/auth.md` (line 49)

**Interfaces:** the request body gains an optional `resource`. The answers:

| Request | Answer |
|---|---|
| client `monoagent`, claim scope includes `offline_access`, no `resource` | `200` as today (`access_token` opaque for one hour, `token_type`, `expires_in: 3600`, `scope`) plus `refresh_token` |
| the same with `resource` equal to `https://monoes.me/api/monoagent` | `200` with the token endpoint's own answer: `access_token` (an audience-bound JWT), `refresh_token`, `token_type: "Bearer"`, `expires_in: 3600`, `expires_at`, `scope`, `id_token`; header `Cache-Control: no-store`. If the provider refuses, its OAuth error body and status |
| any other client, or a claim without `offline_access` | `200` as today: opaque token, no `refresh_token`; for a claim without `offline_access` a `resource` equal to the audience is ignored |
| any other `resource`, or the audience asked for by another client | `400 {"error":"invalid_target"}`, answered before the code is looked at, so the code is not used up |
| a blocked account | `400 {"error":"invalid_or_expired_code"}`, as for every failure |

Consumes `oauthRefreshToken` (schema), `MONOAGENT_AUDIENCE`, `MONOAGENT_CLIENT_ID`, `getAuth().api.oauth2Token`, `isAPIError` from `better-auth/api`.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-claim-resource origin/main`. Task 3 must already be merged.

- [ ] **Step 2: Write the failing spec.** `tests/account-gate-claim.spec.ts` (the emailed code cannot be read by a test, so it seeds the claim row as `tests/oauth-claim.spec.ts` does):

```ts
import { test, expect } from "@playwright/test";
import { eq } from "drizzle-orm";
import { oauthResource, user } from "../src/lib/db/schema";
import { AUDIENCE, MONOAGENT_SCOPES, bearer, decodeJwt, isJwt, refresh, seedClaimRequest, signUp, withDb } from "./helpers/oauth-api";

// The headless sign-in (`account login --email` in mono-agent) is the emailed code. It must end
// where the browser flow ends, with an audience-bound JWT and a refresh token: the MonoAgent client
// gets a refresh token to trade at the token endpoint, or, sending a `resource`, the token endpoint's
// own answer at once. Every other client is answered exactly as before.

type Answer = { access_token?: string; refresh_token?: string; expires_in?: number; scope?: string; token_type?: string; error?: string };

async function verifyCode(baseURL: string, email: string, code: string, extra: object = {}, clientId = "monoagent") {
  const res = await fetch(new URL("/api/auth/agent/claim/verify", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, code, client_id: clientId, ...extra }),
  });
  return { status: res.status, body: (await res.json()) as Answer };
}

async function registerDynamicClient(baseURL: string) {
  const res = await fetch(new URL("/api/auth/oauth2/register", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ redirect_uris: ["https://example-agent.test/callback"], token_endpoint_auth_method: "none", grant_types: ["authorization_code", "refresh_token"] }),
  });
  return ((await res.json()) as { client_id: string }).client_id;
}

test("a claim with the resource returns an audience-bound JWT and a refresh token", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code: "123456" });
  const claimed = await verifyCode(baseURL!, account.email, "123456", { resource: AUDIENCE });
  expect([claimed.status, claimed.body.error]).toEqual([200, undefined]);
  expect([claimed.body.token_type, claimed.body.expires_in]).toEqual(["Bearer", 3600]);
  expect(claimed.body.scope).toContain("library:write");

  expect(isJwt(claimed.body.access_token), "the access token is a JWT").toBe(true);
  const { header, payload } = decodeJwt(claimed.body.access_token!);
  expect([header.alg, header.typ]).toEqual(["EdDSA", "at+jwt"]);
  expect([payload.aud].flat()).toContain(AUDIENCE);
  expect([payload.sub, payload.azp, payload.plan]).toEqual([account.userId, "monoagent", "free"]);

  const me = await fetch(new URL("/api/library/me", baseURL), { headers: bearer(claimed.body.access_token) });
  expect(me.status).toBe(200);
  // The refresh token is a real one: the token endpoint rotates it like any other.
  const next = await refresh(baseURL!, claimed.body.refresh_token!, AUDIENCE);
  expect([next.status, isJwt(next.body.access_token)]).toEqual([200, true]);
});

test("without a resource the opaque token is unchanged and a refresh token comes with it: the token endpoint trades it for a JWT, and today's client refreshes it as before", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code: "654321" });
  const claimed = await verifyCode(baseURL!, account.email, "654321");
  expect(claimed.status).toBe(200);
  expect(isJwt(claimed.body.access_token), "the opaque token it always returned").toBe(false);
  expect(claimed.body.expires_in).toBe(3600);
  expect(claimed.body.refresh_token).toBeTruthy();
  expect((await fetch(new URL("/api/library/me", baseURL), { headers: bearer(claimed.body.access_token) })).status).toBe(200);

  const bound = await refresh(baseURL!, claimed.body.refresh_token!, AUDIENCE);
  expect(bound.status, JSON.stringify(bound.body)).toBe(200);
  const { payload } = decodeJwt(bound.body.access_token!);
  expect([[payload.aud].flat().includes(AUDIENCE), payload.sub]).toEqual([true, account.userId]);

  // The released client stores that refresh token too and, an hour later, refreshes it with no resource.
  // (A second code: a token already traded above is inside its reuse window, not a fresh chain.)
  await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code: "654322" });
  const again = await verifyCode(baseURL!, account.email, "654322");
  const plain = await refresh(baseURL!, again.body.refresh_token!);
  const rotated = Boolean(plain.body.refresh_token) && plain.body.refresh_token !== again.body.refresh_token;
  expect([plain.status, isJwt(plain.body.access_token), rotated]).toEqual([200, false, true]);
});

test("a resource for another audience, or from another client, is invalid_target and does not burn the code", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code: "111111" });
  const wrong = await verifyCode(baseURL!, account.email, "111111", { resource: "https://example.com/other" });
  expect([wrong.status, wrong.body.error]).toEqual([400, "invalid_target"]);
  expect((await verifyCode(baseURL!, account.email, "111111", { resource: AUDIENCE })).status, "the code was not used up").toBe(200);

  const clientId = await registerDynamicClient(baseURL!);
  const stranger = await signUp(baseURL!);
  await seedClaimRequest({ email: stranger.email, scope: "community:read offline_access", code: "222222", clientId });
  const asked = await verifyCode(baseURL!, stranger.email, "222222", { resource: AUDIENCE }, clientId);
  expect([asked.status, asked.body.error]).toEqual([400, "invalid_target"]);
  const plain = await verifyCode(baseURL!, stranger.email, "222222", {}, clientId);
  expect([plain.status, plain.body.refresh_token === undefined]).toEqual([200, true]);
});

test("a claim without offline_access has no refresh token to give or exchange: the resource is ignored", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await seedClaimRequest({ email: account.email, scope: "library:read", code: "333333" });
  const claimed = await verifyCode(baseURL!, account.email, "333333", { resource: AUDIENCE });
  expect([claimed.status, isJwt(claimed.body.access_token), claimed.body.refresh_token === undefined]).toEqual([200, false, true]);
});

test("a blocked account cannot claim a token, with or without a resource", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await withDb((db) => db.update(user).set({ blockedAt: new Date() }).where(eq(user.id, account.userId)));
  for (const [code, extra] of [["444444", {}], ["555555", { resource: AUDIENCE }]] as const) {
    await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code });
    const claimed = await verifyCode(baseURL!, account.email, code, extra);
    expect([claimed.status, claimed.body.error]).toEqual([400, "invalid_or_expired_code"]);
  }
});

test("an exchange the provider refuses comes back as its OAuth error, not a 500", async ({ baseURL }) => {
  const account = await signUp(baseURL!);
  await seedClaimRequest({ email: account.email, scope: MONOAGENT_SCOPES, code: "666666" });
  const setDisabled = (disabled: boolean) => withDb((db) => db.update(oauthResource).set({ disabled }).where(eq(oauthResource.identifier, AUDIENCE)));
  await setDisabled(true);
  try {
    const claimed = await verifyCode(baseURL!, account.email, "666666", { resource: AUDIENCE });
    expect([claimed.status, claimed.body.error]).toEqual([400, "invalid_target"]);
  } finally {
    await setDisabled(false);
  }
});
```

Run `E2E_BASE_URL=http://localhost:3107 npx playwright test tests/account-gate-claim.spec.ts --reporter=line`. Expected: `5 failed, 1 passed`; the passing one is `a claim without offline_access has no refresh token to give or exchange: the resource is ignored`. The first test fails at `the access token is a JWT` (`Expected: true`, `Received: false`) and the second at `expect(claimed.body.refresh_token).toBeTruthy()` (`Received: undefined`).

- [ ] **Step 3: Update the route's unit tests.** In `src/app/api/auth/agent/claim/verify/route.test.ts` add these two cases before the `@/lib/db` one (line 12), so the route's new imports resolve:

```js
    if (specifier === "@/lib/auth") {
      return { url: "data:text/javascript,export const getAuth = () => ({});", shortCircuit: true };
    }
    if (specifier === "@/lib/monoagent-token") return next("../../../../../../lib/monoagent-token.ts", context);
```

In the `@/lib/db/schema` stub (line 16) replace `export const oauthAccessToken = {}; export const user = {};` with `export const oauthAccessToken = {}; export const oauthRefreshToken = {}; export const user = {};`. Append, after a blank line, at the end of the file (CI runs these, not the spec):

```ts
// The stubbed database is an empty object: a request that reached it would throw, so these pass
// only if the answer is given before the code is looked at (and so before it can be used up).
describe("claim verify resource", () => {
  const post = async (body: object) => {
    const { POST } = await import("./route.ts");
    return POST(
      new Request("https://monoes.test/api/auth/agent/claim/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "a@example.com", code: "123456", client_id: "monoagent", ...body }),
      }),
    );
  };

  it("answers invalid_target for another audience", async () => {
    const res = await post({ resource: "https://example.com/other" });
    assert.deepEqual([res.status, await res.json()], [400, { error: "invalid_target" }]);
  });

  it("answers invalid_target when another client asks for the MonoAgent audience", async () => {
    const res = await post({ client_id: "some-agent", resource: "https://monoes.me/api/monoagent" });
    assert.deepEqual([res.status, await res.json()], [400, { error: "invalid_target" }]);
  });
});
```

Run `node --experimental-strip-types --test src/app/api/auth/agent/claim/verify/route.test.ts`. Expected: FAIL, `ℹ pass 6`, `ℹ fail 2`: the new tests with `TypeError: db.select is not a function`, because the route does not judge `resource` yet and reaches the stubbed database.

- [ ] **Step 4: Implement** in `src/app/api/auth/agent/claim/verify/route.ts`. Line numbers are those of `main`; make the edits in this order, bottom to top, and they stay valid.

1. Replace lines 107-112 (the closing `return NextResponse.json({ ... });`) with:

```ts
  return NextResponse.json({
    access_token: /* value */ rawToken,
    token_type: "Bearer",
    expires_in: TOKEN_TTL_MS / 1000,
    scope: claim.scope,
    ...(refresh ? { refresh_token: refresh.raw } : {}),
  });
```

2. After line 101 (`    userId: matchedUser.id,`, inside the `oauthAccessToken` insert) add `    refreshId: refresh?.id,`.
3. After line 96 (the blank line after `const expiresAt = new Date(now.getTime() + TOKEN_TTL_MS);`) add, followed by a blank line:

```ts
  // MonoAgent's headless sign-in has to end where the browser flow ends: with an audience-bound JWT
  // and a refresh token. For the MonoAgent client and an offline_access claim the route hands out a
  // refresh token (the row the provider's refresh grant reads), which the client trades at the token
  // endpoint with `resource`. A client that sends `resource` here has that trade done for it, in the
  // token endpoint's own answer, so the JWT, its claims, the signing key and the blocked-account
  // guard are the provider's. Any other client is answered as it always was.
  let refresh: { id: string; raw: string } | null = null;
  if (clientId === MONOAGENT_CLIENT_ID && scopes.includes("offline_access")) {
    refresh = { id: crypto.randomUUID(), raw: generateOpaqueToken() };
    await db.insert(oauthRefreshToken).values({
      id: refresh.id,
      token: await sha256Base64Url(refresh.raw),
      clientId,
      userId: matchedUser.id,
      scopes,
      expiresAt: new Date(now.getTime() + REFRESH_TTL_MS),
      createdAt: now,
    });
    if (resource) {
      try {
        const minted = await getAuth().api.oauth2Token({
          body: { grant_type: "refresh_token", refresh_token: refresh.raw, client_id: clientId, resource },
        });
        return NextResponse.json(minted, { headers: { "Cache-Control": "no-store" } });
      } catch (error) {
        if (isAPIError(error)) return NextResponse.json(error.body, { status: error.statusCode });
        throw error;
      }
    }
  }
```

4. Replace lines 87-90 (the `matchedUser` select and its `if (!matchedUser) { return invalidOrExpired(); }`) with:

```ts
  const [matchedUser] = await db
    .select({ id: user.id, blockedAt: user.blockedAt })
    .from(user)
    .where(eq(user.email, email))
    .limit(1);
  if (!matchedUser || matchedUser.blockedAt) {
    return invalidOrExpired();
  }
```

5. Before line 43 (`  const db = getDb();`) add, followed by a blank line:

```ts
  // RFC 8707, as on the token endpoint: only MonoAgent may ask for the MonoAgent audience. Judged
  // on the request alone, so a wrong value is answered before the code is looked at or burned.
  const resource = body?.resource;
  if (resource !== undefined && (resource !== MONOAGENT_AUDIENCE || clientId !== MONOAGENT_CLIENT_ID)) {
    return NextResponse.json({ error: "invalid_target" }, { status: 400 });
  }
```

6. Replace line 33 (`    | { email?: unknown; code?: unknown; client_id?: unknown }`) with `    | { email?: unknown; code?: unknown; client_id?: unknown; resource?: unknown }`.
7. After line 7 (`const TOKEN_TTL_MS = 60 * 60 * 1000;`) add:

```ts
// The oauth-provider's own refreshTokenExpiresIn default (30 days).
const REFRESH_TTL_MS = 30 * 24 * 60 * 60 * 1000;
```

8. Replace line 4 with `import { emailClaimRequest, oauthAccessToken, oauthRefreshToken, user } from "@/lib/db/schema";`, after line 5 add the next two lines, and after line 1 (`import { NextResponse } from "next/server";`) add `import { isAPIError } from "better-auth/api";`:

```ts
import { getAuth } from "@/lib/auth";
import { MONOAGENT_AUDIENCE, MONOAGENT_CLIENT_ID } from "@/lib/monoagent-token";
```

In `public/auth.md` line 49 replace `On success: `200 { "access_token": string, "token_type": "Bearer", "expires_in": 3600, "scope": string }`. Codes expire` with ``On success: `200 { "access_token": string, "token_type": "Bearer", "expires_in": 3600, "scope": string }`. For the `monoagent` client and a claim whose scope includes `offline_access`, the answer also carries a `refresh_token`; adding `"resource": "https://monoes.me/api/monoagent"` to the body makes the answer the token endpoint's own instead: an audience-bound JWT `access_token` and that `refresh_token`. Codes expire``.

- [ ] **Step 5: Run.** The unit file of step 3: `ℹ pass 8`, `ℹ fail 0`. The spec: `6 passed`. `npm test`: `ℹ fail 0`. With a browser available, `tests/oauth-claim.spec.ts` (3 tests) still passes: the old flow is unchanged.

- [ ] **Step 6: Checks and commit.** `npx tsc --noEmit`; `npx eslint "src/app/api/auth/agent/claim/verify/route.ts" "src/app/api/auth/agent/claim/verify/route.test.ts" tests/account-gate-claim.spec.ts` (no output).

```bash
git add src/app/api/auth/agent/claim/verify/route.ts src/app/api/auth/agent/claim/verify/route.test.ts public/auth.md tests/account-gate-claim.spec.ts
git commit -m "feat(auth): the email-code route issues the monoagent refresh token and honors resource" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Offer the branch to the owner.**

### Task 8: The public docs describe the audience and the claims

**Files:**
- Create: `src/lib/docs/authentication.test.ts`
- Modify: `src/app/docs/authentication/page.tsx` (insert before line 222; edit lines 244-245)

**Interfaces:** Consumes `MONOAGENT_AUDIENCE`, `MONOAGENT_CLIENT_ID` (Task 3). Produces a section `#monoagent-tokens` on `/docs/authentication`. The test ties the page to the constants and to the claim names mono-agent verifies, so the docs cannot drift from the code.

- [ ] **Step 1: Branch.** Fetch first, or the branch starts from a stale `main` (two separate commands): `git fetch origin`, then `git switch -c feat/account-gate-docs origin/main`. Tasks 3 to 7 must already be merged.

- [ ] **Step 2: Write the failing test.** `src/lib/docs/authentication.test.ts`:

```ts
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { MONOAGENT_AUDIENCE, MONOAGENT_CLIENT_ID } from "../monoagent-token.ts";

const page = readFileSync(new URL("../../app/docs/authentication/page.tsx", import.meta.url), "utf8");

describe("the authentication docs page", () => {
  it("documents the audience, the client and every claim mono-agent verifies", () => {
    const mentioned = [
      MONOAGENT_AUDIENCE,
      `client_id=${MONOAGENT_CLIENT_ID}`,
      "https://monoes.me/api/auth",
      "/api/auth/jwks",
      "EdDSA",
      "at+jwt",
      "exp - iat",
      "invalid_target",
      "invalid_grant",
      "refresh_token",
    ];
    for (const text of mentioned) assert.ok(page.includes(text), `the page mentions ${text}`);
  });
});
```

Run `node --experimental-strip-types --test src/lib/docs/authentication.test.ts`. Expected: FAIL, `AssertionError [ERR_ASSERTION]: the page mentions https://monoes.me/api/monoagent` (the page has none of the audience, `at+jwt`, `exp - iat`, `invalid_target` or `invalid_grant` today).

- [ ] **Step 3: Add the section.** Insert before line 222 (`<h2 id="headless-agents-no-browser" ...>`) in `src/app/docs/authentication/page.tsx`:

```tsx
      <h2 id="monoagent-tokens" className="mb-3 mt-10 text-lg font-semibold text-espresso">
        MonoAgent tokens: audience and claims
      </h2>
      <p className="text-[15px] leading-relaxed text-espresso/75">
        Send <code className="rounded bg-ivory-parchment px-1.5 py-0.5 font-mono text-[13px]">resource=https://monoes.me/api/monoagent</code>{" "}
        (RFC&nbsp;8707) on the authorization request, the token request and every refresh, and the access token is a signed
        JWT bound to that audience instead of an opaque string. Send no <code>resource</code> and nothing changes: the token
        is opaque, as it always was. Only the <code>monoagent</code> client may ask for this resource; any other client is
        answered <code>invalid_target</code>. A refresh token that was issued without a <code>resource</code> can be
        exchanged with one.
      </p>
      <CodeBlock
        label="curl"
        code={`curl -X POST https://monoes.me/api/auth/oauth2/token \\
  -d "grant_type=refresh_token" \\
  -d "client_id=monoagent" \\
  -d "refresh_token=YOUR_REFRESH_TOKEN" \\
  -d "resource=https://monoes.me/api/monoagent"
# -> { "access_token": "eyJ...", "refresh_token": "...", "expires_in": 3600, ... }`}
      />
      <ul className="mt-3 list-disc space-y-2 pl-5 text-sm text-espresso/75">
        <li>
          Header: <code>alg</code> is <code>EdDSA</code> (Ed25519), <code>typ</code> is <code>at+jwt</code>, and{" "}
          <code>kid</code> names a key published at{" "}
          <code className="rounded bg-ivory-parchment px-1.5 py-0.5 font-mono text-[13px]">GET /api/auth/jwks</code>.
        </li>
        <li>
          <code>iss</code> is <code>https://monoes.me/api/auth</code>. <code>aud</code> is an array that contains{" "}
          <code>https://monoes.me/api/monoagent</code>, next to the userinfo endpoint when <code>openid</code> is requested.
        </li>
        <li>
          <code>azp</code> and <code>client_id</code> are <code>monoagent</code>. <code>sub</code> is the user id.{" "}
          <code>scope</code> holds the granted scopes, space-separated.
        </li>
        <li>
          <code>iat</code> and <code>exp</code> are one hour apart (<code>exp - iat</code> is 3600). <code>jti</code> is
          unique per token.
        </li>
        <li>
          <code>plan</code> is <code>free</code> for every account today. <code>sid</code> appears only on tokens from
          the browser flow.
        </li>
      </ul>
      <p className="mt-3 text-[15px] leading-relaxed text-espresso/75">
        Refresh tokens rotate: each refresh returns a new one and the old one stops working. Retrying a refresh whose
        answer was lost, within five minutes, returns that same answer again; presenting a used refresh token after that
        revokes every MonoAgent refresh token of that account, so sign in again. A refresh that is
        answered <code>invalid_grant</code> means the account cannot continue: its tokens were revoked, they expired
        after 30 days, or the account is blocked. Every other failure (<code>invalid_target</code>, a 5xx, no network) says
        nothing about the account. Blocking an account deletes its refresh tokens, access tokens and web sessions in one
        step; a JWT that was already issued is refused by every route and expires within the hour.
      </p>
```

In the headless section replace lines 244-245 (the sentence ``<code>{`{ access_token, token_type: "Bearer", expires_in: 3600, scope }`}</code>. Codes expire after 10 minutes and allow at most 5 attempts.``, which the file wraps over two lines) with:

```tsx
          <code>{`{ access_token, token_type: "Bearer", expires_in: 3600, scope }`}</code>. For the{" "}
          <code>monoagent</code> client and a claim whose scope includes <code>offline_access</code> the answer also carries
          a <code>refresh_token</code>, which can be exchanged at the token endpoint with the <code>resource</code> above.
          Adding that <code>resource</code> to the body gets the token endpoint&apos;s answer at once: an audience-bound
          JWT and a <code>refresh_token</code>. Codes expire after 10 minutes and allow at most 5 attempts.
```

- [ ] **Step 4: Run.** The test: `ℹ pass 1`. `npm test`: `ℹ fail 0`. With the dev server running: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3107/docs/authentication` prints `200` and `curl -s http://localhost:3107/docs/authentication | grep -c 'id="monoagent-tokens"'` prints `1`.

- [ ] **Step 5: Checks and commit.** `npx tsc --noEmit`; `npx eslint src/app/docs/authentication/page.tsx src/lib/docs/authentication.test.ts`.

```bash
git add src/app/docs/authentication/page.tsx src/lib/docs/authentication.test.ts
git commit -m "docs(auth): document the MonoAgent audience, claims, refresh and blocking" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Offer the branch to the owner.** This is the last pull request. With every task deployed and O2 to O4 done, send the lead the Constants table of the findings file: the mono-agent release R builds on it.

---

## Contract change requests

None. The server produces exactly the frozen values of the index §3.2 (`Issuer`, `Audience`, `ClientID`) and satisfies §3.4 item 10. Facts the client plans must know, all recorded in the findings file: the email-code route (Task 7) answers a MonoAgent claim that includes `offline_access` with a `refresh_token`, which `VerifyEmailCode` trades at the token endpoint with `resource` like any other (the verify request stays unchanged), and it also honors a `resource` in the verify body, answering like the token endpoint in one call; against a server without Task 7 there is no `refresh_token` and an emailed-code sign-in must end in a clear error; adoption (D23) and `account logout` must delete every local copy of a refresh token, because presenting a rotated or revoked one ends the account's sessions on every machine (S2), except that a retry of a lost answer inside 300 seconds is answered again with the same response (Task 3); a refresh token unused for 30 days answers `invalid_grant` (S3); `aud` is an array; `iss`, `aud` and the client claim are what tell an access token from the ID token and the `/api/auth/token` session JWT that share the key.
