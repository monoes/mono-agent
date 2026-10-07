# Account/login gate handoff — 2026-10-07

Start here on another machine. This is an unfinished checkpoint, not a release or merge approval. No enforcement date or production signing key is enabled. Keep the user-selected model throughout the resumed session; historical model escalation instructions are superseded.

## Branches and completed work

| Branch | Code checkpoint | Status |
| --- | --- | --- |
| feat/monoes-account-gate | c9d83abf plus this handoff | Authoritative amended plans/spec/index/spike findings; includes the previously uncommitted B3b plan amendments |
| feat/account-core | 02592963 | B1a complete, reviewed and locally verified; OS account CI job added, initially informational |
| feat/account-b1b | 4732956b | Tasks 1–2 complete/reviewed. Task 3 JWT/control libraryfake implemented, review still needed |
| feat/account-b3a | 2a477432 | Task 1 engine entry-point gate implemented; review still needed |
| feat/account-b3b | 79f9aea8 | Task 1 complete/reviewed. Task 2 HTTP gate implemented and review fix committed; scoped re-review still needed |
| monoes-landing: feat/account-gate-deploy-hardening | dbb9f58 | Main-only deploy hardening built; unmerged |
| monoes-landing: feat/account-gate-server | checkpoint of dbb9f58 plus OAuth helper/spike sources | Server production Task 3 not started; local spikes and findings complete |

The docs branch is the authority for plans. Copies in code branches are older. Read the plan from a separate checkout/worktree of `feat/monoes-account-gate`, or with `git show origin/feat/monoes-account-gate:docs/mastermind/plans/<file>`.

## Exact next steps

- B1b: review Task 3 at 4732956b, then Tasks 4–12: refresh transport and Settled semantics, login/email client, logout/adoption, defaults/dev key, library session/refresh/adoption, CLI error mapping and account commands/aliases. Preserve endpoint host pinning and serialized refresh/logout per install. Deferred findings are in the archived B1b ledger.
- B3a: review Task 1 at 2a477432, then Tasks 2, 3, 3b, 4–8: triggers/queued runs, cancellation on refusal, next-node interruption, agent/browser gate, refresher startup, heartbeat/revalidation, org services and caller survey. Regenerate Tasks 3/3b briefs from amended docs (R18).
- B3b: scoped re-review of 841f57f7..79f9aea8 (HEAD/OPTIONS coverage and escaped path classification), then Tasks 3–9 including 7b: OpenAPI, org receiver, /v1 both listeners, webhooks, extension bridge, capture post-write/indexing suppression and MCP. Data pushes remain accepted while locked; no summary/classification/indexing runs then. Do a final bypass-focused review before claiming this security boundary complete.
- A/server: Task 2 spikes S1/S2/S3/S6/S7 and corrections complete in docs commits c9d4f646, 2e5ad3bd, 56d02843. Latest family rule is in plan A at befbc987/b3dde4dc and neighbors. Build Tasks 3–8: audience/plan JWTs, refresh-token families, blocking, library/community auth, signing-key publishing, email-code token flow and public docs. Replay ends the family, not every login of an account. Pin the caller from raw request text; validate BOM/+json behavior on workerd.
- B2 CLI: all five tasks remain, based on B1b.
- B4 desktop and B4b extension account UI: all tasks remain, based on sign-in/CLI and doors respectively.
- B5c integration smoke remains; B5a rollout plus B5b docs must ship together only after prerequisites; B5d license audit remains with actual license/repository changes explicitly owner-gated.

## Integration / owner steps

Preserve the lane branches; do not blindly cherry-pick everything from master. Merge order is A/server prerequisites, B1a, B1b, then B2/B3a/B3b/B4/B4b, B5c, then B5a+B5b together. Merge authoritative docs deliberately and resolve stale plan copies from the docs branch. Existing account residuals and rulings are in the spec/index and archived program ledger.

Owner-run work remains: landing main protection and deployment environment restrictions, D1 production migrations, signing secrets/public-key verification/rotation, enforcement date and release, flipping account-os CI from informational after a green run, real signed-binary smoke and any license/public-repository decisions. Do not deploy or activate enforcement merely to test this checkpoint.

## Verification at handoff

Previous B1a final gates were green per the session ledger; they were not all rerun during this checkpoint. Current `GOMAXPROCS=2 go test ./internal/httpapi -run 'Account|Door' -count=1` passed on feat/account-b3b. No code changes were made to completed core/lane implementations while preparing the handoff. The docs diff is checked for whitespace. The server OAuth helper passes Node TypeScript syntax checking and deployment tests pass 4/4. Full cross-machine/integration verification remains.

## Resume

Fetch `monoes/mono-agent` and `monoes/monoes-landing`. Recreate worktrees from the named remote branches, not from local temporary agent names. Continue from the matching issue/plan; historical scratch directories are not required. Archived ledgers/reports under `docs/handoffs/2026-10-07-account-records/` are reference material, not a prompt to reload wholesale.
