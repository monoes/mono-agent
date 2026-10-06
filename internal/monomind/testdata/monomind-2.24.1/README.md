# monomind 2.24.1 golden fixtures

Recorded 2026-10-05 from monomind v2.24.1 (`monomind --version --json`) in a
scratch HOME and project; read by `golden_2241_test.go`. A fake monomind
replays them, so the client functions run unchanged against real output.

The org is `org-sec.json`: a sections org (`requires.sections: 1`, sections
`drafting` publishing a `note` document and `review` consuming it, a root
`lead`). No model was called: its roles use the `codex` runtime pointed
(`CODEX_CLI_BIN`) at `fake-codex.py`, a scripted stand-in that prints codex
`exec --json` events with tool-call fences. monomind's real runtime did
everything else: it routed the messages, ran the real document store
(`org_doc_publish`, `org_doc_read`, `org_doc_decide`), and wrote the bus.

## Real (recorded output of monomind 2.24.1)

| File | What it is |
|---|---|
| `version.json` | `monomind --version --json` |
| `validate-valid.txt` | `org validate sec`, exit 0, 3 warnings |
| `validate-invalid.txt` | `org validate bad`, exit 1: a sections org still carrying `loops` and `sections.review.mode` |
| `list-running.json`, `list-all.json` | `org list --format json`, mid-run (one org) and with three orgs (stopped, crashed scheduled, never run) |
| `status-running.json`, `status-stopped.json`, `status-all-running.json` | `org status [sec] --format json`, mid-run and after `org_complete` |
| `events.ndjson` | `org events sec --run <id>`: the whole bus of the completed run, 57 lines |
| `doc-events.jsonl`, `doc-snapshot.json`, `doc-notices.jsonl` | the run's document store: `.monomind/orgs/sec/docs/<run>/` (hash-chained event log, state snapshot, delivery notices) |
| `schedule-audit.jsonl`, `schedule-audit-bus.ndjson` | `.monomind/orgs/sched/schedule-audit.jsonl` and the same line on the run's bus, from `org serve` with `schedule: "1m"` and a run that outlasted its interval |
| `org-sec.json` | the org definition |
| `fake-codex.py` | the scripted stand-in runtime (not monomind output) |

## Real, budgeted run (recorded 2026-10-06, monomind 2.24.1)

A second recording: `budget-org.json` is a sections org with USD budgets
(`sections.<n>.budget.usd` 0.05 / 0.04, role `budget_usd`, `run_config.budget_usd`
0.15) whose roles use the `grok` runtime pointed (`GROK_CLI_BIN`) at
`fake-grok.py`, a scripted stand-in that speaks grok's `streaming-messages-json`
and reports a `total_cost_usd` per invocation (`FAKE_COSTS`, a made-up price, so
the dollar amounts are scripted; monomind's metering, section budget evaluation,
warnings and soft closure are real). The codex stand-in cannot report cost
(monomind's codex runner meters tokens only), which is why #349's run has `cost_usd: null`.

| File | What it is |
|---|---|
| `budget-org.json` | the org definition |
| `budget-events.ndjson` | `org events bud --run <id>`: usage events with `cost_usd`, `section-budget-warning` / `section-budget-closed` audit events (drafting, review) and the org-level warning |
| `budget-report.txt` | `org report bud --run <id>`: monomind's own "Section budgets" table, the ground truth the Go derivation is tested against |
| `budget-costs.json`, `budget-status.json` | `org costs --format json` and `org status --format json` for the run (status still has no cost or section data) |
| `budget-run-stdout.txt` | stdout of `org run bud --yes`: the pre-run cost estimate with its `stale rates` line, then the run's end line |
| `run-estimate-abort.txt` | output of `org run sec --yes --budget-usd=-1`: the estimate read without starting a run (monomind prints it, then aborts "before any tokens are spent") |

## Synthetic (hand-built)

| File | What it is |
|---|---|
| `events-unknown-kind.ndjson` | `events.ndjson` with two unknown event kinds (`section_status`, `hologram-from-the-future`) and a known event carrying unknown fields spliced in. 2.24.1 emits none of these; it exists so unknown kinds and fields stay non-fatal. |
| `../../../orgbridge/testdata/documents-synthetic/` | **Synthetic, not monomind output.** The recorded run has no reject, rework or exhausted-cap case, so these are built from monomind 2.24.1's source (`orgrt/documents/store-types.ts`, `state.ts`, `relay.ts`, `rework.ts`, `notice-journal.ts`) and carry the same shapes: `events.jsonl` (a correctly hash-chained store: note-1 rejected once then accepted; note-2 rejected twice = cap of 2 exhausted and frozen; note-3 exhausted then accepted by a root `override`; note-4 pending; one `refused` line), `notices.jsonl` (delivered relay and `rework-exhausted` keys), `org-rework.json` (cap on the consuming section `review`, a `deliverable_files` entry). The panel's bus fixture, `wails-app/frontend/src/components/orgdesigner/__fixtures__/documents-bus.ndjson`, is synthetic too and adds an old `loop` event and an unknown kind. |

## Not captured

`org status --format json` in 2.24.1 has no per-section state and no cost (and `org costs` / `org report --format json` have no section breakdown; the section table is text-only), and there is no
`sections` capability in the handshake: section and document state is read
from the document store files and the `org-docs` messages on the bus.
`org status` mid-run was recorded with `idle_stop_*` fields; a run that is
paused or has abandoned roles was not recorded.
