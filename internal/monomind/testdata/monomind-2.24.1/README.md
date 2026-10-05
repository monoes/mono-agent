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

## Synthetic (hand-built)

| File | What it is |
|---|---|
| `events-unknown-kind.ndjson` | `events.ndjson` with two unknown event kinds (`section_status`, `hologram-from-the-future`) and a known event carrying unknown fields spliced in. 2.24.1 emits none of these; it exists so unknown kinds and fields stay non-fatal. |
| `synthetic-org-budget.json`, `synthetic-events-budget.ndjson` | A sections org with budgets (`sections.<n>.budget.usd`, role `budget_usd`, `run_config.budget_usd`) and a run's bus: `usage` events carrying `cost_usd`, and the `section-budget-warning` / `section-budget-closed` audit events with the `data` fields (`scope`, `spentUsd`, `allocationUsd`, `closed`, `held`) written from monomind's source (`orgrt/documents/section-budget-run.ts`). **Not a recording**: no budgeted run was recorded in #344, and the runtime was not run to produce it. It pins `SectionBudgets` (`org_budget_test.go`); replace it with a recording when one exists. |

## Not captured

`org status --format json` in 2.24.1 has no per-section state and no cost (and `org costs` / `org report --format json` have no section breakdown; the section table is text-only), and there is no
`sections` capability in the handshake: section and document state is read
from the document store files and the `org-docs` messages on the bus.
`org status` mid-run was recorded with `idle_stop_*` fields; a run that is
paused or has abandoned roles was not recorded.
