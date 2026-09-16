# Org × Workflow Unification — implementation contracts

Companion to `2026-09-15-org-workflow-unification.md` (v10). That plan says *what* and *why*; this
file pins the exact interfaces three parallel implementation streams build against:

- **Stream A** — mono-agent Go (`internal/**`, `cmd/**`, `data/**`, docs).
- **Stream B** — monomind M1–M5 (`packages/@monomind/cli/src/orgrt/**`, `commands/org*.ts`).
- **Stream C** — Wails GUI (`wails-app/**` only).

Where this file and the plan disagree, this file wins; each deviation is listed in §9.

---

## 1. monomind capabilities (`monomind --version --json` → `capabilities[]`)

| Capability | Ships | Unlocks in mono-agent |
|---|---|---|
| `org-tool-providers` | M1 + M3 (one release) | grants (Phase 2), boss/parent deciders, live `org send` |
| `org-endpoint-roles` | M2 | automation roles (Phase 4) |
| `org-federation` | M4 | cross-root restrictions shown as enforced |
| `org-decision-attribution` | M5 | `--by`, request-scoped approvals |

mono-agent reads these with `monomind.Capabilities(ctx)` and returns
`monomind.ErrFeatureNeedsMonomind{Capability, Feature}` when one is missing. Error text:
`<feature> needs monomind with capability "<cap>" (installed: <version>) — update monomind`.

---

## 2. monomind M-series (Stream B)

### M1 — role tool providers (`org-tool-providers`)

**Schema** (`types.ts`, all passthrough):
```ts
ToolProviderSchema = z.object({
  kind: z.literal('mcp-stdio'),
  name: z.string().regex(/^[a-z0-9][a-z0-9_-]*$/),
  command: z.string().min(1),
  args: z.array(z.string()).default([]),
  env: z.record(z.string(), z.string()).default({}),   // literal values only, never expanded
  allow: z.array(z.string()).optional(),                // MCP tool names to expose; absent = all
  prefix: z.string().regex(/^[a-z0-9][a-z0-9_]*$/).optional(),   // default: name with '-' → '_'
  timeout_ms: z.number().int().positive().default(660_000),      // per tools/call
  idle_ms: z.number().int().positive().default(300_000),
}).passthrough();
RoleSchema.tool_providers?: ToolProviderSchema[]
RolePolicySchema.approvalTools?: string[]
```

**Exposed tool name**: `<prefix>__<mcpToolName>` (e.g. `monoagent__automation_publish_post`).
On Claude it arrives as `mcp__org__<prefix>__<tool>`; `normalizeToolAction` strips `mcp__org__`, so
`approvalTools`/`autoApproveTools`/`denyTools` entries use the bare `<prefix>__<tool>` form.
`policy.allowTools` must also exempt names that start with any of the role's provider prefixes
followed by `__` (fence/Vercel runners pass bare names).

**Lifecycle**
- Tool list: at role session build, list tools once per provider config (hash of
  command+args+env+allow) — spawn, `initialize`, `tools/list`, exit — cached for the daemon's
  lifetime; convert each `inputSchema` (JSON Schema object) into a zod shape (string, number,
  integer, boolean, array, object, enum; unknown → `z.any()`; required vs optional honoured).
- Calls: spawn lazily on the first call; reuse; exit after `idle_ms` without calls; on crash
  restart once per session, then return `ERROR: tool provider <name> unavailable: <reason>`.
- Kill all provider processes on session end and `stopOrg`.

**Provider process env**: `process.env` + `provider.env` + `MONOMIND_ORG_NAME`, `MONOMIND_ORG_RUN`,
`MONOMIND_ORG_ROLE`, `MONOMIND_ORG_ROOT` (daemon root).

**Trace**: every `tools/call` request carries
```json
"_meta": { "trace": { "org": "growth", "run": "run-…", "role": "lead", "chain_id": "chn_…", "hop": 2 } }
```
`chain_id`/`hop` come from the most recent message delivered to that role whose body contains a line
matching `^\[trace (chn_[A-Za-z0-9_-]+) hop=(\d+)\]`; otherwise a fresh `chn_` + 20 random
`[a-z0-9]` and `hop: 0`. The `tool` bus event emitted by `policy.decide` for that call carries
`data.chain_id` (and `data.hop`).

**Result mapping**: MCP `content[]` text parts joined with `\n`; `isError: true` → prefix
`ERROR: `. Non-text parts → `[<type> omitted]`.

**approvalTools**: in `checkApproval`, an action is sensitive if it is in the built-in list **or**
in `roleDef.policy.approvalTools`. `autoApproveTools` still takes precedence.

**Idle watchdog**: skip (like gates/questions) while `daemon.approvals.get(org)` has any entry with
`approved === null`.

**Hot reload** (`reloadOrgDef`): for existing roles, compare `tool_providers`, `endpoint`, `kind`,
`policy` (deep JSON). Apply by replacing those fields on the live role object and calling
`agent.policy.updatePolicy(newPolicy)` on a running role. Report as `changed` entries
`role:<id>:<field>`. Provider tool lists refresh at the role's next session start.

### M3 — operator-authenticated sender (ships with M1)

- `server.ts` `/api/xdeliver`: when the request's `x-monomind-cred` is the **operator** credential,
  call `daemon.receiveRemote(..., { operator: true })`, which skips the broker identity check and
  trusts `fromOrg:fromRole` as given (sender org need not be registered, e.g. `workflow`).
- `org inbox <org>` uses `readOperatorCredential(org)` for the live POST. New flags
  `--format json`. Output `{"v":1,"org","to","from","delivery":"live"|"queued","receipt","messageId"}`.
  Exit 0 for both; non-zero only for invalid input or unknown org.
- **messageId**: generated once per logical message at its origin (`msg-<ms>-<8 hex>`) and put at
  `data.messageId` on **every** bus event for it: both in-process `xorg` copies, sender and
  receiver copies of a remote delivery (`/api/xdeliver` body gains `messageId`), `message` events,
  and inbox-queued messages (`QueuedMessage.messageId`, re-used on drain).

### M2 — endpoint roles (`org-endpoint-roles`)

**Schema**
```ts
RoleSchema.kind?: z.enum(['agent','endpoint'])
RoleSchema.endpoint?: z.object({
  url: z.string().url(),
  credential_file: z.string().optional(),     // absolute; must be mode 0600 and owned by the daemon user
  timeout_ms: z.number().int().positive().optional(),   // reply-wait hold for the idle watchdog; default 600000
  input_hint: z.string().optional(),          // one line for the boss briefing
}).passthrough()
```
Validation (`org validate`): an endpoint role may not be root, may not have `policy`, `runtime`,
`adapter_config`, `budget_tokens`, `budget_usd`, `tool_providers`.

**Daemon**: an endpoint role never gets a session, mailbox, policy engine, slot, or budget share;
it is excluded from `pendingRoles`, `max_concurrent_agents`, respawn, idle nudges, boss selection,
per-role budget split, and `org costs|report|flow` role tables.

**Delivery** (`deliver()` and `receiveRemote()` when the target role is an endpoint role):
```
POST <endpoint.url>
content-type: application/json
authorization: Bearer <credential_file contents, trimmed>     (only if credential_file set)
{"orgName":"growth","run":"run-…","from":"lead" | "hq:ceo","to":"growth:publisher-bot",
 "subject":"…","body":"…","messageId":"msg-…"}
```
- POST timeout 15 s. 2xx → emit the usual `message`/`xorg` event (with `data.messageId`,
  `data.endpoint: true`), receipt `delivered to growth:publisher-bot (endpoint)`.
- Otherwise → `queueMessage` with `endpoint: true`, retry after 1 s, 5 s, 15 s; after the third
  failure emit `audit` `reason: 'endpoint-unreachable'` `data:{role, messageId, error}` and leave it
  queued. While the org runs, every 60 s re-attempt queued `endpoint: true` entries (other queued
  entries are left in place). `startOrg`'s drain delivers queued endpoint entries by POST.
- Insecure `credential_file` (mode not 0600 or wrong owner) → do not POST; `audit`
  `reason: 'endpoint-credential-insecure'`; queue.
- **Watchdog hold**: after a successful POST, record a wait `{role, messageId, until: now +
  (timeout_ms ?? 600000)}`; a message arriving from `<org>:<endpointRole>` (or `<endpointRole>`)
  clears that role's oldest wait. The idle watchdog skips while any wait is unexpired.
- **Boss briefing**: one line per endpoint role:
  `- <id> (<title>) is an automation, not an agent. Message it with org_send; it replies with its result.<input_hint>`

### M4 — federation (`org-federation`)

- `OrgDefSchema.federation?: { allow_from?: string[], allow_to?: string[] }` (`'*'` = any).
- Broker entry gains `root` (the daemon's project root). `lookupOrg` returns it.
- **Same root = one trust domain**: deliveries between orgs whose broker `root` equals the sender
  daemon's root are never restricted.
- `deliver()` to a cross-root org: if the sender def has `allow_to` and the target org is not
  listed → receipt `ERROR: federation: <from> may not send to <to>` + `audit`
  `reason:'federation-denied'`.
- `receiveRemote()`: body gains `fromRoot`; if `fromRoot` ≠ receiver root and the receiver def has
  `allow_from` not listing the sender org → reject (`federation: sender not allowed`). If the
  broker entry for the sender has a `root` and `fromRoot` disagrees with it → reject
  (`federation: root mismatch`). Operator-authenticated calls are exempt.

### M5 — decision attribution (`org-decision-attribution`)

- CLI `--by <resolver>` on `org approve|deny|answer|gate-approve|gate-reject`; API body field
  `resolvedBy` on `/api/set-approval`, `/api/answer-question`, `/api/resolve-gate`. Default `human`.
- Stored: `gates.json` `resolvedBy` (no longer hard-coded), `approvals.json` entries gain
  `requestId`, `resolvedBy`, `resolvedAt`; `questions.json` entries gain `resolvedBy`.
- Every resolution emits `{type:'audit', reason:'decision-resolved', from:<role>,
  data:{kind:'approval'|'gate'|'question', ref, resolver, verdict:'approved'|'denied'|'answered'}}`
  where `ref` = requestId | gateId | questionId.
- **Request-scoped approvals**: each new approvals entry gets `requestId: 'apr-<ms>-<8 hex>'`; the
  approval `question` bus event carries `data.requestId` and `data.input` (the same summarised input
  `policy.decide` logs). `org approve|deny <org> <role> <action> --request <id>` (API field
  `requestId`) resolves only that entry; without it, every pending entry for the pair (today's
  behaviour).
- `org approvals <org> --format json` items gain `requestId`, `resolvedBy`, `input`.

### All M-series

- Add the four capability strings to `AGENT_PROTOCOL_CAPABILITIES` as each lands.
- Passthrough test: an org JSON with `automations`, `tool_providers`, `kind`, `endpoint`,
  `children`, `federation`, `autonomy` survives `OrgDefSchema.parse` + `org validate` unchanged.

---

## 3. Wire formats shared by both repos

**Trace line** (first line of a message body written by mono-agent):
`[trace chn_<id> hop=<n>]`

**Grant-mode MCP server** (`monoagentcli mcp --grant <id> --profile <id>`): tools
`automation_<alias>` (one per granted automation), `automation_status`, `automation_output`, and
for holding grants `org_start`, `org_stop`, `org_status`, `org_report`; for decider grants
`decision_list`, `decision_resolve`.

**Endpoint URL**: `http://<api addr>/org-endpoint/ep_<26 base32>` (api addr default
`127.0.0.1:9322`).

**Reply from an automation role**: `monomind org inbox <target org> --to <target role>
--from <org>:<endpoint role> --subject "re: <subject>" --body "<trace line>\n<output>" --format json`.

---

## 4. mono-agent CLI (Stream A implements; Stream C calls)

All commands print one JSON object (`"v":1`) to stdout; errors exit non-zero with
`{"error":"…"}` on stdout when `--json`/JSON mode. `--project <root>` defaults to the **active
profile root** (`profiledir.Root`). DB-backed commands honour the global `--profile`.

### Grants and automations
```
org automation list <org>
  → {"v":1,"org","automations":[{"workflow_id","alias","owned","workflow_name","has_outbound_nodes",
      "outbound_nodes":[…],"exists":bool}]}
org automation unassigned
  → {"v":1,"workflows":[{"id","name","is_active"}]}
org automation add <org> --workflow <id> --alias <slug> [--owned]
org automation remove <org> --alias <slug>
  → {"v":1,"org","automation":{…}} / {"v":1,"org","removed":true}

org grant list <org> [--role <id>]
  → {"v":1,"org","grants":[Grant]}
org grant add <org> --role <id> --automation <alias> [--mode run|trigger|status] [--wait=true|false]
     [--timeout 600] [--approval none|required] [--max-calls-per-run 20] [--max-calls-per-day 200]
     [--max-output-bytes 16384]
  → {"v":1,"org","grant":Grant,"tool_name":"monoagent__automation_<alias>"}
org grant remove <org> --role <id> --automation <alias>
  → {"v":1,"org","removed":true}

Grant = {"id","org","role","alias","workflow_id","workflow_name","mode","wait","timeout_seconds",
  "approval","tier","max_calls_per_run","max_calls_per_day","max_output_bytes","created_at"}

org automation-role add <org> --alias <slug> --reports-to <role> [--title T] [--reply last_node|node:<name>]
  → {"v":1,"org","role":{"id","title","reports_to","workflow_id"},"endpoint_url"}
org automation-role remove <org> --role <id>
org automation-role rotate <org> --role <id>   → {"v":1,"org","role","endpoint_url","applies":"next org start"|"now"}

org effective-tools <org> --role <id>
  → {"v":1,"org","role","tools":[{"name","source":"org"|"grant"|"builtin","description"}]}
```

### Autonomy and decisions
```
org autonomy show <org>
org autonomy set <org> [--level manual|mid|full] [--decider model|boss|parent] [--decider-runtime R]
     [--decider-model M] [--fallback model] [--decider-timeout 120] [--policy TEXT | --policy-file F]
     [--tier <class>=<tier>]... [--clear-tier <class>]... [--on-decider-failure deny|human]
     [--max-decisions N] [--max-decider-usd X]
org autonomy pause <org>|--all [--for 30m]
org autonomy resume <org>|--all
  → Autonomy = {"v":1,"org","level","effective_level","paused_until",
      "decider":{"kind","runtime","model","fallback","timeout_seconds"},
      "tiers":{},"default_tiers":{"<class>":"<tier>"},"policy","on_decider_failure",
      "limits":{"max_decisions_per_run","max_decider_usd_per_run"},
      "updated_at","updated_by","daemon_running":bool}

org autonomy decisions <org> [--run <id>] [--verdict <v>] [--limit 200]
  → {"v":1,"org","decisions":[Decision]}
Decision = {"id","org","run_id","item_kind","item_ref","requester","class","tier","level","resolver",
  "verdict","answer_text","rationale","cost_usd","latency_ms","chain_id","created_at"}

org autonomy needs-you <org>
  → {"v":1,"org","items":[{"kind","ref","requester","class","tier","summary","waiting_since",
      "idle_stop_in_seconds":int|null}]}
```
(`org decisions` stays monomind's decision trace; see §9.)

### Processes, messaging, holding orgs, legacy root
```
org serve [--foreground]      → {"v":1,"root","pid","status":"started"|"already-running"}
org stop <org> | pause <org> | resume <org>   → {"v":1,"org","ok":true,…}
org send <org> --to <role> [--from <org:role>] --subject S --body B
  → {"v":1,"org","to","from","delivery":"live"|"queued","receipt","messageId"}
org rename <org> <new>        → {"v":1,"org","renamed_to"}
org delete <org> [--force]    → {"v":1,"org","deleted":true}
org group start|stop|status <holding>
  → {"v":1,"holding","children":[{"org","status","run","start","budget_share","cost_usd"}],"rollup_usd"}
org legacy list               → {"v":1,"legacy_root","orgs":[…]}
org legacy move <org>         → {"v":1,"org","moved_to"}

status [--json]  → {"v":1,"daemon":{"running","pid","api_addr","heartbeat_age_ms"},
                    "org_serve":{"running","pid","root"}}
daemon [--api=true] [--api-addr 127.0.0.1:9322] [--allow-mutations]
```

---

## 5. Wails bindings (Stream C) — all return JSON strings, all shell `monoagentcli`

| Binding | CLI |
|---|---|
| `ListOrgAutomations(org)` | `org automation list <org>` |
| `ListUnassignedAutomations()` | `org automation unassigned` |
| `AddOrgAutomation(org, workflowID, alias)` | `org automation add` |
| `RemoveOrgAutomation(org, alias)` | `org automation remove` |
| `ListOrgGrants(org)` | `org grant list` |
| `SetOrgGrant(org, specJSON)` spec = `{role, automation, mode, wait, timeout_seconds, approval, max_calls_per_run}` | `org grant add` |
| `RemoveOrgGrant(org, role, alias)` | `org grant remove` |
| `AddAutomationRole(org, specJSON)` spec = `{alias, reports_to, title, reply}` | `org automation-role add` |
| `RemoveAutomationRole(org, roleID)` | `org automation-role remove` |
| `GetEffectiveTools(org, roleID)` | `org effective-tools` |
| `GetOrgAutonomy(org)` | `org autonomy show` |
| `SetOrgAutonomy(org, specJSON)` spec = `{level, decider:{kind,model,runtime,fallback,timeout_seconds}, policy, tiers, on_decider_failure, limits}` | `org autonomy set` |
| `PauseOrgAutonomy(org, duration)` / `ResumeOrgAutonomy(org)` | `org autonomy pause|resume` |
| `ListOrgDecisionLog(org, run)` | `org autonomy decisions` |
| `ListNeedsYou(org)` | `org autonomy needs-you` |
| `StartOrgGroup(h)` / `StopOrgGroup(h)` / `OrgGroupStatus(h)` | `org group …` |
| `SendOrgMessage(org, specJSON)` | `org send` |
| `GetDaemonStatus()` | `status --json` |

Runtime events: existing `org:event` feeds the live view. No new Go→JS events are required.

---

## 6. SQLite (migration `041_org_unification.sql`)

Tables exactly as plan §6.2 (`org_grants`, `org_endpoints`, `org_bridge_calls`, `org_autonomy`,
`org_decisions`) plus:
```sql
CREATE TABLE IF NOT EXISTS org_asks (          -- org.ask node ↔ reply correlation
  id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, org_name TEXT NOT NULL, role_id TEXT NOT NULL,
  endpoint_role_id TEXT NOT NULL, execution_id TEXT NOT NULL, node_id TEXT NOT NULL,
  status TEXT NOT NULL,                        -- waiting | replied | timed_out
  reply_json TEXT, created_at TEXT NOT NULL, deadline_at TEXT NOT NULL, replied_at TEXT);
ALTER TABLE workflow_executions ADD COLUMN resume_after TEXT;   -- event-woken pauses (C-33)
```
`org_grants.tools_json` also stores `approval`, `tier`, `max_calls_per_day`, `max_output_bytes`.

---

## 7. Org JSON keys mono-agent writes (all passthrough for monomind)

As plan §6.1, plus:
- `roles[].automations[]` spec: `{alias, mode, wait, timeout_seconds, approval, input_schema?,
  max_calls_per_run, max_calls_per_day, max_output_bytes}` — **display copy**; enforcement is the
  `org_grants` row. Reconcile strips any entry without a live row.
- `roles[].tool_providers[]` generated by mono-agent for roles with ≥1 live grant: name
  `monoagent`, prefix `monoagent`, command = absolute path of `monoagentcli`,
  args `["mcp","--grant",<grant bundle id>,"--profile",<profile>]`, `allow` = the tool names,
  `timeout_ms` = max grant timeout × 1000 + 30000. One provider per role; the grant bundle id is the
  id of the role's first grant row and the server serves **all** live grants for that (org, role).
- `roles[].policy.approvalTools` maintained by mono-agent for `approval: "required"` grants
  (`monoagent__automation_<alias>`), and `org_start` for Initiators.
- Endpoint roles: `{"kind":"endpoint","type":"automation","endpoint":{"url","input_hint"},
  "automation":{"workflow_id","reply","alias"}}`.

---

## 8. Decision classes (plan §7.7) — exact strings

`tool:<Name>` · `org_complete` · `question` · `grant:<alias>` · `org_start` · `gate` · `hil:<alias>`

`--tier` keys use these strings; `tool:*` and `grant:*` are accepted wildcards (exact key wins).

---

## 9. Deviations from the plan (and why)

| Plan | Implementation | Why |
|---|---|---|
| `org decisions <org>` lists autonomy decisions (§7.7) | `org autonomy decisions <org>` | `org decisions` already exists and shows monomind's decision trace (`internal/monomind/org.go:286`). |
| One grant id per role in `tool_providers.args` | One provider per role serving all live grants for (org, role), addressed by the first grant id | A role with several grants must not fork one process per automation (C-22). |
| Reconcile inserts new grant rows on save (§6.2) | Rows are created only by explicit grant/automation-role/autonomy commands; save and startup reconcile only strip/revoke/lower | Closes C-3 fully: a full-document GUI save of an agent-edited JSON must not mint grants. |
| `org.ask` reply addressed `workflow:<exec>:<node>` | `org.ask` sends from its endpoint role with subject `ask:<id> …`; the receiver matches `ask:<id>` in the reply subject or body and resumes the execution | monomind cannot address a non-org sender; the endpoint role is the workflow's address (plan already requires it). |
| `wake_kind` marker | `resume_after` timestamp column: event-woken nodes pause with a 30 s safety window | One nullable column covers both "wait for event" and back-off (C-33). |
| Org root: CLI default `~/.monoagent` only (C-31) | CLI **and** chat/MCP tools switch to `profiledir.Root` | Chat tools used `~/.monoagent` for the default profile too. |
