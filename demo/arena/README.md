# Org Arena — build notes

Source of truth for the demo design: the published playbook
(https://claude.ai/artifact/PVqaEa1ufDxt1YmGrSNQEr). This directory holds
every buildable artifact from phases 1-5.

## Quick start

```bash
cd <this worktree, or wherever .monomind/ lives>
./demo/arena/scripts/run-show.sh          # budget defaults to $5/org; pass a number to override
# open http://localhost:4300/       — the map, on the big screen
# open http://localhost:4300/vote   — the crowd's page, QR-code this to phones
./demo/arena/scripts/stop-show.sh         # when the show is over
```

To rehearse and record a fallback for outage protection:

```bash
./demo/arena/scripts/run-show.sh
# ... let it run, watch it, then ...
./demo/arena/scripts/stop-show.sh
./demo/arena/scripts/branch-fallback.sh   # prints the replay command it produced
```

If a provider stalls mid-show, run the printed replay command in place of
`run-show.sh` — same bridge, same map, same vote page, fed from the
recorded run instead of live agents.

## What's here

- **`orgs/`** — the three org configs (Forge, Anvil, Herald). `run-show.sh`
  copies them into `.monomind/orgs/` before every run, so this directory,
  not the gitignored runtime copy, is the source of truth to edit.
- **`bridge/scoring.mjs`** — the scoring reducer. Pure functions
  (`applyEvent(state, org, event) -> state`), unit-tested in
  `scoring.test.mjs` (`node --test demo/arena/bridge/scoring.test.mjs`).
  Read the file's header comment first — it documents two corrections a
  live run forced onto the original design (see "Corrections" below).
- **`bridge/bridge.mjs`** — the only thing that talks to the org runtime.
  Tails `org events <org> --follow` per org (live mode) or replays a saved
  `bus.jsonl` at N× speed (replay mode), runs everything through
  `scoring.mjs`, and serves the map, the vote page, and an SSE feed at
  `/events` on one port (default 4300).
- **`web/index.html`** — the map: districts, desks colored by role status,
  couriers for messages, drone arcs for cross-org messages, loot crates for
  file assets, power bars, a leaderboard, an achievement toast, and the
  event ticker. Connects to `/events` — no build step, open it via the
  bridge's own HTTP server (not as a `file://`, so the SSE origin matches).
- **`web/vote.html`** — the crowd's page: the current `ask_human` question
  as a live-tallied yes/no poll, and the six chaos cards as a live-tallied
  ballot. Votes are informational only — see "Chaos cards" below.
- **`scripts/run-show.sh`** / **`stop-show.sh`** — launch and tear down the
  whole show (bridge + auto-approver + all three orgs, correctly staggered).
- **`scripts/auto-approve.sh`** — dev-time watcher: grants pending Bash
  approvals automatically and prints xorg/asset/gate/question events to the
  terminal. Kept as a safety net even though the org configs now set
  `policy.autoApproveTools: ["Bash"]` on every hands-on role (belt and
  braces — it covers any tool that config doesn't).
- **`scripts/branch-fallback.sh`** — snapshots the latest run of each org
  into a new branch run for outage-proof replay, and prints the exact
  `bridge.mjs replay --run-map ...` command to use it (see "Corrections").

## Chaos cards

Every card is a real runtime command the **host** runs by hand — the
bridge only tallies votes, it never acts on them:

| Card | Command |
|---|---|
| Power Cut | `monomind org pause <org>` / `org resume <org>` |
| Rumor | `monomind org inbox <org> --to <role> --from "board:chair" --subject "..." --body "..."` |
| Mole | `monomind org inbox <org> --to <role> --from "<other-org>:<role>" --subject "..." --body "..."` |
| Budget Squeeze | edit `.monomind/orgs/<org>.json`, then `monomind org reload <org>` |
| Reorg | edit an org's `roles`, then `monomind org reload <org>` |
| Second Opinion | `monomind org answer <org> <question-id> "<answer>"` |

Gate approvals: `monomind org gate-approve <org> <gate-id> "<resolution>"`.

## Corrections a live run forced onto the design

Two real defects found by running the smoke test, not by reading the
design doc (full detail in `bridge/scoring.mjs`'s header comment and the
git history of this directory):

1. **`max_turns_per_message: 40` was too tight.** A role debugging a
   hanging test hit `error_max_turns` and burned real money for no usable
   output. Raised to 60 in all three configs.
2. **A pending tool-approval and a genuine fence block emit the identical
   bus shape** (`type: "audit", reason: "decision-trace"`, same
   `data.decisionType`/`outcome`). The original design treated every
   `audit` event as a fence block (a Firewall achievement + siren). Live
   evidence showed that's wrong — most `audit` events are just routine
   Bash-approval prompts. `scoring.mjs` now only counts one as a fence
   block when `data.context`/`data.reasoning` actually mentions
   fence/scanMessages wording.
3. **`org branch <org> <run> <label>` does not create a run directory
   named `<label>`.** It generates its own `branch-<timestamp>-<hash>` id
   and prints it — the label is just a note. An early draft of
   `branch-fallback.sh` assumed the label was the directory name; the
   fallback replay command it printed pointed at a run that didn't exist.
   Fixed to parse the real id out of the command's own output.
4. **Herald (the house) was earning Diplomat/Ambassador** just from
   replying to both studios — it's the hub, so it naturally sends the most
   cross-org messages. Excluded house orgs from those two achievements the
   same way they're excluded from the leaderboard.
5. **`cardVotes` were tallied but never reached clients** — `scoring.mjs`'s
   snapshot function didn't include the field the vote page reads. Votes
   were being counted and silently never shown. Fixed and covered by a
   regression test.

All five are covered by tests in `scoring.test.mjs`, or were reproduced
live via the bridge's replay mode against the actual 2026-09-16 smoke-test
recordings before being called fixed.

## Known limitations, stated plainly

- **"Tests pass" is inferred, not verified.** The bus has no tool
  stdout/exit codes — a `go test` Bash call only ever shows up as
  `decision: "allow"` at invocation. The map can show a file landed; it
  cannot independently confirm the tests in it are green. Don't present it
  as verified on stage.
- **The map hasn't been opened in a real browser by an agent** — it was
  verified by driving the bridge's HTTP/SSE/vote API directly with `curl`
  and unit-testing the scoring logic, which confirms the data pipeline and
  every scoring rule, but not the Canvas rendering itself. Open
  `http://localhost:4300/` once before a real show to eyeball layout and
  legibility on the actual screen you'll project onto.
- **No full 25-minute rehearsal has been run.** Every mechanic has been
  proven individually (cross-org messaging, the idle watchdog, asset
  scoring, the vote/tally pipeline, branch-and-replay), but not end to end
  on the clock. Run `run-show.sh` once, in full, before the real thing.
