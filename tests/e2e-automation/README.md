# Automation e2e scripts

End-to-end checks for browser automation packages and record → action:
the real `monoagentcli` binary, the real extension in a real (headless)
Chromium, a private bridge, and a small fixture site. They are **not** part
of CI; run them by hand before and after a release.

## Safety

- Everything runs under a scratch directory (`$E2E_WORK`, default
  `~/scratch/automation-e2e-run`) with its own `HOME`. Nothing reads or
  writes your real `~/.monoagent`.
- The bridge is a **private** one on `$E2E_BRIDGE_PORT` (default 9232). The
  scripts refuse port 9222, which is your own bridge and holds your browser's
  extension connection.
- The browser is a separate headless Chromium with its own profile. Two fake
  hosts are mapped to 127.0.0.1: `crm.e2e.test` (the fixture) and
  `evil.e2e.test` (for off-domain checks).
- `xdg-open` is replaced by a stub that only logs, so nothing opens in your
  desktop browser.
- `teardown.sh` stops only the processes `setup.sh` started, by PID.

## Requirements

Go (to build the binary), Node ≥ 22 (global `fetch`/`WebSocket`), Python 3,
and Chromium or Chrome on `PATH` (or `E2E_CHROME`). A real AI run
(`E2E_REAL_AI=1`) also needs `monomind` with a working claude runtime.

## Run

```bash
cd tests/e2e-automation
E2E_BUILD=1 ./setup.sh        # build the binary, start fixture + bridge + browser, pair, sign in
./regress-cli.sh              # packages and CLI, no browser clicks
./regress-flow.sh             # record → analyze → verify → save → run, security, panel, rerecord, trust
./teardown.sh
```

Each check prints `PASS name` or `FAIL name :: want [..] got [..]`. Each
script ends with `N failure(s)` and exits 1 when N > 0.

To test a release, point the scripts at that release's binary and extension:

```bash
git worktree add ~/scratch/wt-e2e-vX.Y.Z vX.Y.Z
(cd ~/scratch/wt-e2e-vX.Y.Z && go build -o ~/scratch/monoagentcli-vX.Y.Z ./cmd/monoagentcli)
export E2E_BIN=~/scratch/monoagentcli-vX.Y.Z E2E_EXTENSION_DIR=~/scratch/wt-e2e-vX.Y.Z/chrome-extension
export E2E_WORK=~/scratch/automation-e2e-vX.Y.Z      # a fresh work dir per run
./setup.sh && ./regress-cli.sh; ./regress-flow.sh; ./teardown.sh
```

Use a fresh `E2E_WORK` for each full run: `regress-flow.sh` creates the
`e2e-crm` package with `--new` and expects it not to exist yet.

## Settings (env)

| Variable | Default | Meaning |
|---|---|---|
| `E2E_WORK` | `~/scratch/automation-e2e-run` | scratch root: HOME, logs, fixtures, results |
| `E2E_HOME` | `$E2E_WORK/home` | HOME for the CLI and the bridge |
| `E2E_BIN` | `$E2E_WORK/monoagentcli` | binary under test (`E2E_BUILD=1` builds it from this checkout) |
| `E2E_EXTENSION_DIR` | `<repo>/chrome-extension` | extension loaded into the test browser |
| `E2E_BRIDGE_PORT` | `9232` | private bridge (9222 is refused) |
| `E2E_CDP_PORT` | `9447` | test browser's DevTools port |
| `E2E_FIXTURE_PORT` | `18765` | fixture site |
| `E2E_CHROME` | first of chromium, chrome on PATH | browser binary |
| `E2E_REC` | — | reuse this recording id instead of recording a new one |
| `E2E_REAL_AI` | — | `1` also runs one real `record analyze` (spends AI budget) |
| `V2` | — | `V2=1 ./setup.sh` starts the fixture with the name field renamed, so recorded selectors miss |

## What is where

| File | Role |
|---|---|
| `env.sh` | settings, port guard, `m` (run the CLI), `check`, `j` |
| `setup.sh`, `teardown.sh` | start / stop the stack (PID files in `$E2E_WORK/pids`) |
| `fixture/server.mjs` | fixture CRM: login (password), new-contact form + toast, contacts list, upload page; logs requests to `$E2E_WORK/requests.log` (never the password) |
| `cdp.mjs`, `browser.mjs` | DevTools helpers; recording through the real side panel, panel review, the rerecord picker, extension→Go requests |
| `make_fixtures.py` | generates the test packages, unsafe archives, a login draft and 0600 inputs files |
| `stub-monomind.mjs` | stand-in for `monomind` (`MONOMIND_BIN`) that answers `record analyze` with a fixed draft |
| `regress-cli.sh` | package lifecycle, fixture tests, local packages, install review, unsafe archives, workflow import without overwriting local edits (copyOf/copyReason, `--overwrite`, `--replace`), legacy packages (narrow domain suggestions, http startUrl, local-only), partial bundles (`--automation-domains`, `--use-suggested-domains`), `differs` and `--replace-automations` |
| `regress-flow.sh` | the browser scenarios, including the side panel's re-recorded-selector recovery and `extension status` for a client with the wrong pairing token |

Results (JSON outputs, screenshots under `shots/`, prompts the stub
received) stay in `$E2E_WORK` for inspection.
