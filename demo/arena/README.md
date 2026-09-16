# Org Arena — build notes

Source of truth for the demo design: the published playbook
(https://claude.ai/artifact/PVqaEa1ufDxt1YmGrSNQEr). This directory holds
the buildable artifacts from phase 1 ("Cast").

## What's here

- `orgs/forge.json`, `orgs/anvil.json`, `orgs/herald.json` — the three org
  configs. Copy (or symlink) them into `.monomind/orgs/` before running:

  ```bash
  cp demo/arena/orgs/*.json .monomind/orgs/
  monomind org validate
  ```

- `scripts/auto-approve.sh` — dev-time watcher. Polls each org's pending
  tool approvals and grants them automatically, and prints cross-org
  messages, file assets, gates, and questions as they happen. Run it
  alongside the orgs during any manual test or rehearsal:

  ```bash
  ./demo/arena/scripts/auto-approve.sh forge anvil herald
  ```

## Phase 1 status: done, smoke-tested live on 2026-09-16

`org validate` and `org run <org> --dry-run` pass for all three. A real
5-minute run (`--budget-usd 3` each, ~$1.15 actual total across all three)
confirmed the scenario mechanics work as designed:

- Anvil's CTO pitched `herald:editor` unprompted before Forge did, matching
  the "move fast" personality written into its responsibilities.
- Herald's editor replied to both studios and ran the review protocol
  without being told the exact wording.
- Both Dev roles independently found the real node convention in this repo
  (`internal/nodes/control/`, following the existing `wait.go` pattern —
  not `internal/workflow/` as originally guessed) and wrote a registered,
  tested `delay_until.go`.
- The 3-minute idle watchdog fired on Herald's editor and it self-recovered
  by nudging both studios — confirms the pass-3 fix in the playbook.

Two real defects found and fixed as a result:

1. **`max_turns_per_message: 40` was too tight.** Forge's Dev got stuck
   debugging a hanging test and hit `error_max_turns` — burned $0.25 with
   no usable output. Raised to 60 in all three configs.
2. **Bash tool calls need human approval by default; Write and Edit do
   not.** Unattended, this stalls a role indefinitely — exactly what
   happened to Anvil's Dev in the smoke test until it was manually
   approved. Added `policy.autoApproveTools: ["Bash"]` to every role that
   touches the filesystem (Forge/Anvil's dev and qa, Herald's reviewer).
   `auto-approve.sh` remains as a safety net for any tool not covered.

Neither studio reached the reviewer handoff inside 5 minutes — expected,
since even a clean implementation with tests took the whole window. This
supports the run-of-show pacing (first loot around minute 10, judging
around minute 19-21) rather than contradicting it.

## Not yet built (phases 2-5)

- Event bridge (tail all three `org events --follow`, score, broadcast).
- Canvas map, achievements, ticker.
- Vote page for audience chaos cards and `ask_human` polls.
- Rehearsals, a branched fallback run, host script.
