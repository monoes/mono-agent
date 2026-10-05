# OpenAI-compatible API, phase 6, stage 1: saved server settings, `api config`, `daemon restart` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** every setting of the API's server can be saved, shown, changed, removed and applied from the CLI, in the same words and with the same checks that MCP (stage 2) and the desktop app (stage 3) will use. This stage builds the foundation and the CLI: saved settings in the existing `settings` table, the precedence flag > environment > saved > default with the source of each value, the running daemon's heartbeat carrying its effective settings, `api config show|set|unset` with the exposure gate, and `daemon restart`. Stages 2 and 3 are built from the **Contract** below alone.

**Source of the decisions:** `design.md` of phase 6, decisions D36 to D43 (they join the spec's table at the end of this stage). Where this plan adds to or reads one of them differently it says so in "What this plan decides", and the report of the stage repeats it for the lead.

## Architecture

- `internal/apiconfig` (new) owns the saved document, its checks, the layered resolution, the exposure gate (`Widens`), the state of each setting against the running daemon, and the documents every surface prints: `Show`, `Apply` and `BuildStatus` return the types the CLI prints as JSON, so the MCP tools are the same code and the pipe tests can compare them byte for byte, as `api_models_list` does with `api models`. `cmd/monoagentcli` only parses flags, calls these and prints.
- `internal/openaiapi` stays the owner of what a value means: `ParsePolicy`, `ParseImageRuntimes`, `ParseToolRuntimes` and two parsers extracted from `ConfigFromEnv` (`ParseMaxConcurrent`, `ParseTurnTimeout`, same messages) are used by the flags, the environment and the saved layer, so the saved layer accepts and refuses what they do. `apiconfig` imports `openaiapi`, never the reverse.
- The saved layer reaches the existing readers as an overlay of the environment: `Overlay(saved, getenv)` answers `getenv(name)` for the API's variables with the environment's value when it is not empty and the saved value otherwise (an empty variable is an unset one, as everywhere in the code and in the tests that clear them with `t.Setenv(name, "")`). `newAPIRuntime`, `api models`, `api status` and (stage 2) `api_models_list` pass the overlay where they passed `os.Getenv`, so the flags keep winning over both (they are the explicit argument those functions already take) and nothing else about them changes. The sources come from the same per-setting rule (`ResolveKey`), which `Overlay` is built on.
- The daemon records, per setting, `{value, source}` in its heartbeat (`api_settings`); `State` compares it with the saved layer (D37).
- Restarting is `autostart.Installer.Restart` on the three backends and `autostart.RestartRegistered(ctx, installer)`, which returns the result struct and a typed error when nothing is registered. `daemon restart` and the MCP tool call that.

## Global constraints

- TDD: a failing test first for the right reason; afterwards mutation-check each behaviour test (break the production code, see the test fail at run time, restore) and report the survivors.
- Files under 500 lines; conventional commits with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`; never a bare `go build ./cmd/monoagentcli` in the repo root; plain separate git commands; never push.
- No real model call, no `launchctl`, `systemctl` or `schtasks`, no write under the real `~/.monoagent` in any test (`t.TempDir()`, `internal/testdb`, a fake `Installer`, `HOME` set to a temp dir).
- Nothing here is a secret (paths of TLS files, never their contents), but no error echoes a value except an address or a path the user typed for that setting.
- Known failing tests on macOS (not caused by this work): `TestCaptureTaskFilesOnTheBoard`, `TestCoderConversationFolders`, `TestCoderRootIsOneSharedFolder`, `TestWorkflowCancelSignalsAndMarks`, `TestCreateAttachesEveryArtifact`, `TestCreateRecordsTheRealPathNotASymlink`, `TestGenerateConfigFailsFastWhenMonomindMissing`, `TestFindAll_ListsShadowedCopies`.

## What this plan decides (beyond or differently from design.md)

Each is a choice the lead can overrule; none changes a field of the contract except where it says so.

- **P1. Unsetting is judged like any other change.** D39 says "narrowing and unsetting are never widening". Unsetting a value that was above or at its default is narrowing, which is every ordinary case. But unsetting a value *below* its default raises the effective policy: `confinement` saved as `chat-only` goes back to `any` on loopback, `image_runtimes` saved as `none` goes back to the default list. A gate that let that pass would let an MCP caller or a script widen the server by unsetting. So `Widens` compares the effective policy of the two documents, and `unset` has `--yes` and `--dry-run` as `set` does. An unset of anything at or above its default is never reported as widening. Accepted by the lead on 2026-10-05.
- **P1b. Both kinds of listener are always evaluated.** D39 (b) says "for each kind of listener the saved settings would create". That misses a network listener that the saved document does not name: the daemon's `MONOAGENT_API_V1_ADDR` may come from a service drop-in (spec 8.5 tells headless users to do exactly that), and then `api config set --confinement any` leaves both saved documents with only the loopback kind (`any` to `any`) while, after a restart, the daemon's network listener goes from `chat-only` to `any`. So `Widens` evaluates the loopback and the network kind in every document, whether or not it names a dedicated listener, and rule (a) stays about the address itself. The cost is one more `--yes`: raising `confinement` from unset to `sandboxed` or `any` is always a `confinement.network` widening (its reason says it applies if the server has a listener beyond this machine). `chat-only` never widens. `context_confinement` and `auto_confinement` are judged after the cap that `confinement` puts on each kind, as the server applies them, so raising one is a widening only on a kind where the capped class rises. `confinement` has no default value, so D39's "a value that equals the default is not a change" does not apply to it; it holds for the other nine. Kept by the lead on 2026-10-05: the extra `--yes` is the safe side.
- **P2. No prompt.** A widening `api config set|unset` without `--yes` is exit 3 with the reasons, also on a terminal: D39's wording, and one rule for scripts, the app and tests.
- **P3. `--dry-run`** on `set` and `unset` returns the document the change would give (`applied: false`, with the `widening` reasons) and writes nothing. It is how the app obtains the reasons for its dialog: its runner keeps only the exit code and the last stderr line of a failed call, so a refusal cannot carry a list.
- **P4. `overridden` is decided by the running source alone.** A running value whose source is `flag` or `env` is `overridden` whether or not a value is saved, as D37 words it: saving would have no effect until the flag or variable is removed.
- **P5. The state ignores this shell's environment.** `pending_restart` compares the daemon's value with what a restart would resolve from the saved layer alone (saved, else default), because a daemon started by the login service reads its own environment, not the shell's. `effective` and `source` in the document are what a server started from this process would use (environment, saved, default) and say whose environment that is (`environment`).
- **P6. The saved layer accepts exactly what the environment accepts, and then stores a canonical spelling.** `Validate` runs the very parsers the flags and the environment run, on the text as typed, so a padded duration (` 15m`, which `time.ParseDuration` refuses) or a padded class is refused here as it is there. Text that was accepted is stored as: a runtime list lower-cased with `agy` resolved and repeats dropped, joined by commas (their parsers trim each part, so `agy, Codex` is stored `antigravity,codex`); a duration as Go prints it with trailing zero units trimmed (`900s` is stored `15m`); an integer without sign or leading zeros (`+5` and `007`, which the environment accepts, are stored `5` and `7`). Addresses, paths and classes are stored as given. One exception on purpose, a row of the parity table with its reason: `set` refuses an empty or blank value for any key (it says to use `unset`), where the environment reads a blank runtime list as the default list. A value that equals the default is still stored (the user said it) and is listed in `changed`, but is never a widening.
- **P7. The two TLS files are one setting in two keys.** `set` and `unset` refuse a result that has one without the other (exit 3), and `Validate` reports it in a document edited by hand. The environment pair wins as a pair: if either variable is set, both come from the environment (as `tlsserve` already treats a half pair), else the saved pair, else the self-signed certificate. `set` does not read the files: a deployment may place them after saving.
- **P8. Flags.** `api config set` has the ten flags of the brief; `httpapi` and `daemon` get none: `--turn-timeout`, `--image-runtimes`, `--tool-runtimes` and the TLS files stay environment-only there, as D37 says. All ten flags of `set` are string flags, so every value is checked in one place (exit 3, naming the setting).
- **P9. The reach of the dedicated bind is ordered (the lead's decision of 2026-10-05, which closed a gap this plan first left as D39 words it).** A `v1_addr` change widens when the new bind is beyond this machine and the old one did not already reach that far. The order, least first: no listener or a loopback one; one host beyond this machine; every interface (an empty host, `0.0.0.0`, `[::]` and the other spellings of the unspecified address are one thing). Widening: from no listener or loopback to a host beyond the machine or to every interface; from one host to a different host (a new place to be reached); from one host to every interface. Not widening: the port alone, the same host, every interface to one host or to loopback (narrowing), loopback to loopback, unsetting, no change. How a host is read: loopback is the server's own test, `tlsserve.IsLoopbackAddr` (the name `localhost`, an IP address of 127.0.0.0/8 or `::1`; an empty host is not loopback); any other host that is not an IP address is a specific host beyond the machine, and two names are one host only when they are the same name (read in lower case and without a trailing dot), because the gate cannot know what a name resolves to, so a name and its address are two hosts; an IP address is one host with every other spelling of it (IPv6 written another way, an IPv4 address as IPv4-mapped IPv6); an address with a zone (`fe80::1%eth0`) is read as a name, as `net.ParseIP` does not take it. Text that is not an address is read as not saved. The key stays `v1_addr`; the reason says which of the three it is.
- **P10. Damaged documents, and the way out (the way out, its gate and its exit code added on 2026-10-05 at the lead's request).** A row that cannot be decoded is an `ErrDamaged`: its value is not a JSON object (an empty value included), its `v` is not a whole number of at least 1 (a missing `v` is read as 1), or a field this binary knows has the wrong type. It is exit 3 (invalid saved data, which the user can fix) for every command that reads it (`show`, `set`, `unset <key>`, `api models`, `api status`, the server's start), mapped in the one place that maps the errors of `apiconfig` to exit codes, and every message starts the same way, `the saved settings are damaged` (`apiconfig.DamagedMessage`), says what is wrong with the row (and never what it held) and names the repair with its gate: `monoagentcli api config unset --all --yes` removes them. So a caller that has only the exit code and the last line of stderr (the desktop app) can tell damage from any other failure and offer the repair. That command (`apiconfig.Apply` with `All`, which MCP and the app call) removes such a row inside the same `BEGIN IMMEDIATE` transaction, but only with confirmation: what the row limited cannot be told, so removing it is a widening of unknown size, with the one `Widening` of the key `saved_settings` (`The saved settings cannot be read, so what they limited cannot be told: removing them returns every setting to its default, which may reach further.`). Without `Confirm` and without `DryRun` that is a `*WideningError` (the CLI: exit 3, `Pass --yes`) and nothing is written; a dry run reports the same widening (`applied: false`, `removed_unreadable_row: true`) and writes nothing; confirmed, it removes the row and says so: `removed_unreadable_row: true` in the result document (additive, present only when true), with the widening in `widening`, and in the text output `Removed: the saved settings row, which could not be read.` on stdout and a note on stderr. A row with a higher `v` than this binary knows is an `ErrTooNew` and the exception: nothing removes it, `unset --all` included, because that would lose what a newer version saved; the error is exit 1 (it is not damage, and is not offered a reset) and says to use the version that wrote it, or to remove the row by hand with `sqlite3 <database> "delete from settings where key = 'api_gateway_config'"` (`apiconfig.RemoveRowSQL`; the database is the one the command opened, `~/.monoagent/monoagent.db` unless `--db-path` names another). A failing database is neither (exit 1 as before, and never taken for damage), and never a reason to remove anything. An invalid value in a row that parses (a hand edit) is reported by `show` (`problems`), refuses the server start (exit 3, naming the setting and `api config unset`), and does not stop `unset` from removing it; `set` looks only at the settings it sets and at the TLS pair. `show` of a row that cannot be decoded is the exit-3 error above and not a document: `problems[]` holds what a row that parses has wrong in it.
- **P11. `api status --json` keeps its document.** Its `confinement_source: "environment"` now means this process's environment, then the saved settings, then the defaults; the human note says so. The builder moves to `internal/apiconfig` (`BuildStatus`), with the probes, so the MCP tool `api_status` is the same code.
- **P12. D40's "after the daemon's own graceful stop" is not claimed.** The service managers end the process their own way (`launchctl kickstart -k` kills the running instance), and whether `schtasks /end` stops the process the task's `cmd` wrapper started was not verified on Windows. The command says that it interrupts what the daemon is running, and no document of this stage says a graceful stop happens. Accepted by the lead on 2026-10-05.
- **P13. Two small changes outside the file list, found when the documents were checked against the running CLI** (each with its test): the messages that say image generation or tool calling is switched off named only the environment variable, which sent the operator to the wrong place when the saved list is `none`, so they now also name `monoagentcli api config`; and the note of `api models` says it evaluates the saved settings too. Accepted by the lead on 2026-10-05.

## File structure

New: `internal/apiconfig/` (`settings.go` keys, `Settings`, `Defaults`, `Validate`; `store.go` `Load`, `Update`; `resolve.go` `ResolveKey`, `ResolveAll`, `Overlay`, `EnvWithSaved`; `widen.go`; `state.go`; `report.go` `Show`; `apply.go` `Apply`; `status.go` and `probe.go` `BuildStatus`; a test file for each); `internal/autostart/restart.go`; `cmd/monoagentcli/api_config.go` (the three commands and their text) and `daemon_restart.go`.

Modified: `internal/openaiapi/config.go` (two parsers and the defaults exported), `internal/tlsserve/tlsserve.go` (`CertFile`, `KeyFile`), `internal/daemonhb/heartbeat.go` (`api_settings`), `internal/autostart/` (`Restart` on the interface and the three backends), `cmd/monoagentcli/` (`api_gateway.go`, `api_models.go`, `api_status.go` shrinks to flags and text, `api.go`, `daemon.go`, `daemon_install.go` stays, `doctor_env_test.go`'s fake gets `Restart`), and the documents of task 17.

## Tasks

- [x] **1.** This plan. `docs(api): the plan of phase 6, stage 1: saved server settings`.
- [x] **2.** `openaiapi`: `ParseMaxConcurrent`, `ParseTurnTimeout`, `DefaultMaxConcurrent`, `DefaultTurnTimeout`, `MinTurnTimeout` exported, `ConfigFromEnv` uses them with its messages unchanged (the existing tests are the proof).
- [x] **3.** `apiconfig` settings: the ten keys, `Settings`, `Defaults`, `Validate`, the canonical spellings. Table test: one list of cases (padded, signed, empty and blank values among them) run against the flags and the environment (through `newAPIRuntime`) and against `Validate`, which must accept and refuse the same; the one row where `set` differs on purpose is a blank value (P6).
- [x] **4.** `apiconfig` store: `Load`, `Update` in one `BEGIN IMMEDIATE` transaction, unknown fields kept, a higher `v` refused. Test: many parallel updates of different fields, under `-race`, lose nothing.
- [x] **5.** `apiconfig` resolution: `ResolveKey`, `ResolveAll`, `Overlay`, `EnvWithSaved`.
- [x] **6.** `apiconfig.Widens`: table test of every rule of D39 as P1 and P1b read it: narrowing, unsetting (above, at and below the default), both kinds of listener in every document (including a network listener the document does not name), loopback and non-loopback addresses.
- [x] **7.** `tlsserve.Config.CertFile/KeyFile`: the environment pair wins as a pair, else the explicit pair.
- [x] **8.** `newAPIRuntime` and `startV1` read the saved layer; no saved settings changes nothing.
- [x] **9.** `api models` and `api status` read the saved layer.
- [x] **10.** `BuildStatus` moves to `apiconfig` (the document and the existing tests unchanged).
- [x] **11.** The heartbeat carries `api_settings`; the daemon writes it.
- [x] **12.** `State` and `Show` (the report).
- [x] **13.** `Apply` (validation, the gate run inside the transaction on the row it replaces, dry run, atomic). A test makes two callers race: one raises `confinement`, the other widens `v1_addr`; neither is judged on a stale row.
- [x] **14.** `api config show|set|unset`.
- [x] **15.** `autostart` `Restart` on the interface and the three backends (a fake runner on macOS and Linux as for `Status`; Windows gets a runner variable and a wait for the daemon's lock to be released between `/end` and `/run`, both replaceable, and its test is compiled with `GOOS=windows go vet` since it cannot run here). The existing `fakeAutostart` of the doctor tests gets a `Restart`.
- [x] **16.** `autostart.RestartRegistered` and `daemon restart` (the CLI takes its installer from a package variable that every test replaces).
- [x] **17.** Documents: `ref api`, AGENTS.md, SECURITY.md, CHANGELOG, the quickstart, the spec (section 11 and D36 to D43), this plan ticked.
- [x] **18.** Verification, the smoke run of the built CLI under a temp `HOME`, the report.

---

# Contract

Normative for stages 2 and 3. Field names, keys and codes below do not change after this commit without a new commit that says so and why. Consumers ignore fields they do not know.

## Setting keys

Ten settings, in this order in every document. `default` is the canonical text of the default (`""` = no value). A value is text in the syntax of its environment variable; `none` is valid for the two runtime lists.

| key | server flag (`httpapi`, `daemon`) | environment variable | default | accepted |
|---|---|---|---|---|
| `v1_addr` | `--v1-addr` | `MONOAGENT_API_V1_ADDR` | `` (no dedicated listener) | `host:port` with a numeric port 0 to 65535, host may be empty (`:9443`) |
| `tls_cert_file` | none | `MONOAGENT_API_TLS_CERT` | `` (self-signed off loopback) | a path; only together with `tls_key_file` |
| `tls_key_file` | none | `MONOAGENT_API_TLS_KEY` | `` | a path; only together with `tls_cert_file` |
| `confinement` | `--confinement` | `MONOAGENT_API_CONFINEMENT` | `` (`any` on loopback, `chat-only` off it) | `chat-only`, `sandboxed`, `any` |
| `context_confinement` | `--context-confinement` | `MONOAGENT_API_CONTEXT_CONFINEMENT` | `chat-only` | the same three |
| `auto_confinement` | `--auto-confinement` | `MONOAGENT_API_AUTO_CONFINEMENT` | `chat-only` | the same three |
| `max_concurrent` | `--max-concurrent` | `MONOAGENT_API_MAX_CONCURRENT` | `4` | whole number 1 to 64 |
| `turn_timeout` | none | `MONOAGENT_API_TURN_TIMEOUT` | `10m` | a Go duration of at least `10s` (`15m`, `90s`) |
| `image_runtimes` | none | `MONOAGENT_API_IMAGE_RUNTIMES` | `codex,antigravity` | comma-separated runtime ids (`[a-z0-9][a-z0-9-]{0,31}`, case and spaces ignored, `agy` is `antigravity`), or `none` alone |
| `tool_runtimes` | none | `MONOAGENT_API_TOOL_RUNTIMES` | `claude,codex` | the same |

Keys are written with underscores. `unset` also takes the dashed spelling of a flag (`v1-addr`). An empty or blank value is not a value: use `unset`.

## Saved document

One row of the existing `settings` table (`key TEXT PRIMARY KEY, value TEXT NOT NULL`, migration 001): key `api_gateway_config`, value a JSON object `{"v":1, "<setting key>": ...}`. Every field is optional, absent means not saved. All values are JSON strings except `max_concurrent`, a JSON number (a string holding a whole number is read too). Machine-wide like the daemon, not per profile. A row with no field but `v` is deleted. Written by one `BEGIN IMMEDIATE` read-modify-write, so two writers never lose each other's change; fields this binary does not know are kept as they were.

## Precedence and sources

Per setting: **flag, then environment variable, then saved, then default.** A source is one of `flag`, `env`, `saved`, `default`. `httpapi` and `daemon` have flags for `v1_addr`, `confinement`, `context_confinement`, `auto_confinement`, `max_concurrent`; for the other five the order is environment, saved, default. The TLS pair is resolved as a pair (see P7). The flags and the environment keep their present checks and messages; the saved layer is checked by `Validate` first and a server does not start on an invalid one.

## Heartbeat (`~/.monoagent/daemon-heartbeat.json`, `internal/daemonhb`)

One new optional field, written by the daemon once, with all ten settings; a heartbeat of an older daemon has none:

```json
"api_settings": {
  "v1_addr":             {"value": "0.0.0.0:9443",      "source": "saved"},
  "tls_cert_file":       {"value": "",                   "source": "default"},
  "tls_key_file":        {"value": "",                   "source": "default"},
  "confinement":         {"value": "",                   "source": "default"},
  "context_confinement": {"value": "chat-only",          "source": "default"},
  "auto_confinement":    {"value": "chat-only",          "source": "default"},
  "max_concurrent":      {"value": "8",                  "source": "flag"},
  "turn_timeout":        {"value": "10m",                "source": "default"},
  "image_runtimes":      {"value": "codex,antigravity",  "source": "default"},
  "tool_runtimes":       {"value": "claude,codex",       "source": "env"}
}
```

```go
// internal/daemonhb
type Heartbeat struct {
	// ... existing fields unchanged ...
	APISettings map[string]APISetting `json:"api_settings,omitempty"` // key -> effective value and where it came from
}
type APISetting struct {
	Value  string `json:"value"`
	Source string `json:"source"` // flag | env | saved | default
}
```

`value` is the canonical text of the effective value (`""` where a default has no value, such as `confinement`, `v1_addr`, the TLS files); `source` is one of the four. It is what the daemon started with: the address configured, not the bound port. Nothing else of the heartbeat changes.

## States

Per setting, from the saved layer and the live daemon's heartbeat (`apiconfig.State(saved, key, hb)`, where `hb` is nil when there is no live daemon):

| state | when |
|---|---|
| `not_running` | no live daemon (no heartbeat, stale, or its pid is gone) |
| `unknown` | a live daemon whose heartbeat has no `api_settings`, or none for this key |
| `overridden` | the daemon's source for it is `flag` or `env`: a saved value has no effect until that is removed (P4) |
| `applied` | the daemon's source is `saved` or `default` and its value equals what a restart would resolve (the saved value, else the default; compared in canonical spelling) |
| `pending_restart` | the same sources, a different value |

`restart_needed` is true when at least one setting is `pending_restart`. Conservative on one point: for `confinement`, whose default has no value, saving a value that spells the default (`any` on loopback) is `pending_restart` until the daemon restarts, though the policy does not change.

## Document: `api config show --json` (and `Show`)

```json
{
  "v": 1,
  "environment": "shell",
  "settings": [
    {
      "key": "v1_addr",
      "server_flag": "--v1-addr",
      "env": "MONOAGENT_API_V1_ADDR",
      "saved": "0.0.0.0:9443",
      "default": "",
      "effective": "0.0.0.0:9443",
      "source": "saved",
      "running": "",
      "running_source": "default",
      "state": "pending_restart"
    }
  ],
  "daemon": {"running": true, "reports_settings": true, "autostart": true},
  "restart_needed": true,
  "problems": []
}
```

| field | type | presence |
|---|---|---|
| `v` | integer | always `1` |
| `environment` | string | always: `shell` (the CLI) or `mcp` (the MCP server), whose environment `effective` and a `source` of `env` refer to |
| `settings` | array of 10 | always, in the order of the table above |
| `settings[].key` | string | always |
| `settings[].server_flag` | string | always; `""` for the five without a flag on `httpapi` and `daemon` |
| `settings[].env` | string | always |
| `settings[].saved` | string | omitted when nothing is saved for it (canonical spelling; an invalid hand-edited value is shown as stored) |
| `settings[].default` | string | always, may be `""` |
| `settings[].effective` | string | always, may be `""`: what a server started from this process would use (flag excluded: environment, saved, default) |
| `settings[].source` | string | always: `env`, `saved` or `default` |
| `settings[].running` | string | present only when a live daemon reports the setting; `""` means it reports an empty value |
| `settings[].running_source` | string | present exactly when `running` is: `flag`, `env`, `saved` or `default` |
| `settings[].state` | string | always: `applied`, `pending_restart`, `overridden`, `not_running`, `unknown` |
| `daemon.running` | boolean | a live heartbeat exists |
| `daemon.reports_settings` | boolean | the live heartbeat carries `api_settings`; false for an older daemon and when not running |
| `daemon.autostart` | boolean | the daemon is registered with the OS service manager (`launchd`, `systemd --user`, Scheduled Task), so `daemon restart` can restart it |
| `restart_needed` | boolean | some setting is `pending_restart` |
| `problems` | array | always, `[]` when fine; `{"key": string, "message": string}`, `key` is `""` for a problem of the document; a saved value that fails the rule of its setting, a TLS file without the other |

Exit codes: 0; 3 when the saved row is damaged (not JSON, an empty value, a version that is not a whole number from 1, a known field of the wrong type: `apiconfig.ErrDamaged`; the message starts `the saved settings are damaged` and names `monoagentcli api config unset --all --yes`: P10); 1 when the database cannot be read, and when the row is in a newer format than this binary knows (`apiconfig.ErrTooNew`: the message names the version to use and the SQL that removes the row by hand, and it is never offered a reset). Stdout is one document; notes go to stderr.

Text: a table (`SETTING`, `SAVED`, `EFFECTIVE (this shell)`, `RUNNING (daemon)`, `STATE`, where `effective` and `running` carry their source in brackets unless it is the default, and the state reads `restart needed` for `pending_restart` and `daemon not running` for `not_running`) followed by sentences: whether a daemon runs, whether `daemon restart` can restart it, which settings need a restart, which are overridden and by what, which the shell's environment overrides, and the problems. The text is for people: a consumer reads `--json`.

## Document: `api config set|unset --json` (and `Apply`)

The `show` document of the state **after** the change (the same fields and order), plus three, and a fourth that is there only when it is true:

```json
{
  "v": 1, "environment": "shell", "settings": [ ... ], "daemon": { ... }, "restart_needed": true, "problems": [],
  "applied": true,
  "changed": ["v1_addr", "confinement"],
  "widening": [
    {"key": "v1_addr", "reason": "The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine, and serve runtimes up to chat-only; it did not listen beyond this machine before."}
  ]
}
```

| field | type | meaning |
|---|---|---|
| `applied` | boolean | the saved settings are now as asked (also when nothing needed changing); `false` for `--dry-run` |
| `changed` | array of keys | the settings whose saved value is different after the change (newly saved, changed, removed), in table order; `[]` when nothing changed |
| `widening` | array | `{"key", "reason"}` for each way the change makes the server reach further (see below); `[]` when none. Without confirmation a real run refuses (exit 3) and prints nothing here, so a caller that needs the reasons first uses `--dry-run` |
| `removed_unreadable_row` | boolean, present only when `true` (added 2026-10-05, P10) | `unset --all`, confirmed, found a saved row that cannot be decoded and removed it; with `--dry-run`, would remove it. `widening` then holds the one for it (`saved_settings`, below), and `changed` is `[]`, since nothing in such a row could be read. Never set for a row in a newer format, which is an error |

With `--dry-run` the document is the state the change would give, nothing is written, and `widening` lists the reasons whether or not `--yes` was given. `settings[].state` and `restart_needed` are computed against the running daemon, so a change shows `pending_restart` there until the daemon restarts, and `restart_needed` is false when no daemon runs.

## Widening (`apiconfig.Widens(before, after Settings) []Widening`)

`Widening` is `{"key": string, "reason": string}`; `reason` is one sentence in plain words, to be shown as it is. It compares the *effective* policy the two saved documents would give (P1), for **both kinds of listener in every document** (P1b): the loopback kind (the main listener always has it) and the network kind (a dedicated listener bound beyond this machine, which the saved document names with `v1_addr` or the daemon's environment or flags may supply). A host is loopback when it is `localhost`, an IP in 127.0.0.0/8 or `::1`; an empty host binds every interface and is network. Effective classes: `confinement` unset is `any` on the loopback kind and `chat-only` on the network kind; `context_confinement` and `auto_confinement` unset are `chat-only`; neither is ever above `confinement`.

| key | when |
|---|---|
| `v1_addr` | the dedicated listener's bind reaches further than before (P9): beyond this machine where there was none or a loopback one, another host beyond it, or every interface where it was one host. The port alone, the same host, a narrower bind and unsetting are not |
| `confinement.loopback`, `confinement.network` | `confinement` of that kind is higher after than before |
| `context_confinement.loopback`, `context_confinement.network` | the class a `--context` key may use on that kind is higher |
| `auto_confinement.loopback`, `auto_confinement.network` | the class `auto` may pick on that kind is higher |
| `image_runtimes`, `tool_runtimes` | the list gains a runtime that is not in the built-in default list and was not in the old list, or it leaves `none` for a list or the default |
| `saved_settings` | not from `Widens`, which judges two documents, and a row that cannot be read is none: `Apply` adds it to the change that removes a saved row that cannot be read (`unset --all`, P10), because what the row limited cannot be told and returning every setting to its default may reach further than anything. Its reason: "The saved settings cannot be read, so what they limited cannot be told: removing them returns every setting to its default, which may reach further." (added 2026-10-05; `apiconfig.WideningKeySavedSettings`; the one key that is not a setting or a kind of listener) |

So `set --confinement sandboxed|any` is always `confinement.network` (the reason says it applies to a listener beyond this machine, wherever its address comes from), `set --confinement chat-only` never widens, and `set --context-confinement sandboxed` is `context_confinement.loopback`. Never widening: any narrowing; unsetting something that was at or above its default; `max_concurrent`, `turn_timeout`, the TLS files; saving a value that equals the default of the other nine (`context_confinement: chat-only`, `max_concurrent: 4`); a bind that reaches the same or less (the port alone, the same host, every interface to one host: P9).

A `v1_addr` reason has three forms, none to be parsed: "The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine, and serve runtimes up to chat-only; it did not listen beyond this machine before." (from nothing or loopback); "The dedicated /v1 listener would move from 192.168.1.10:9443 to 10.0.0.5:9443, another address beyond this machine, and serve runtimes up to chat-only." (one host to another); "The dedicated /v1 listener would listen on every interface (0.0.0.0:9443), where it listened only on 192.168.1.10:9443 before, and serve runtimes up to chat-only." (one host to every interface).

## Commands

All take the global `--json`. Errors go to stderr as one line each; with `--json` stdout stays empty on failure. Exit codes follow the other `api` commands: 0 ok, 1 general, 3 invalid input (2 is not used here). A saved row that cannot be read is invalid input too, exit 3 (P10).

### `monoagentcli api config show`

No flags. See above.

### `monoagentcli api config set`

```
api config set [--v1-addr A] [--tls-cert-file P] [--tls-key-file P]
               [--confinement C] [--context-confinement C] [--auto-confinement C]
               [--max-concurrent N] [--turn-timeout D] [--image-runtimes L] [--tool-runtimes L]
               [--yes] [--dry-run]
```

Changes only the settings given. At least one is needed. Exit 3 for: no setting given; an empty or blank value; a value that fails its rule (the message names the setting and the rule, e.g. `max_concurrent must be a whole number from 1 to 64`; it echoes the value only for an address or a path); a result with one TLS file and not the other; a widening change without `--yes` (the message lists the reasons and says to pass `--yes`; `--dry-run` never needs it); a saved row that cannot be read (P10: the message starts `the saved settings are damaged` and names `monoagentcli api config unset --all --yes`). `--yes` is the only confirmation: there is no prompt.

### `monoagentcli api config unset`

```
api config unset <key>... [--all] [--yes] [--dry-run]
```

Removes the saved value of each key (a key with none saved is fine), or of every setting with `--all`. Exit 3 for: neither keys nor `--all`, or both; an unknown key; a result with one TLS file and not the other; a widening change without `--yes`. The prompt-free `--yes` and `--dry-run` are as for `set`. With `--all` it also removes a saved row that cannot be decoded and says so, but only with `--yes`: that removal is the widening `saved_settings`, so without `--yes` it is exit 3 with that reason (P10); with a damaged row `unset <key>` is exit 3 and names `unset --all --yes`; a row in a newer format is exit 1 and is not removed.

### `monoagentcli daemon restart`

```
daemon restart
```

Restarts the daemon through the OS service it is registered as: `launchctl kickstart -k gui/<uid>/com.monoagent.daemon`, `systemctl --user restart monoagent-daemon.service`, or on Windows `schtasks /end` on `MonoAgentDaemon`, a wait of up to 10 seconds for the daemon to release its lock (the task has a logon trigger and no keep-alive, so a `/run` that raced the old process would leave no daemon), then `schtasks /run`. It interrupts whatever the daemon is running (workflows, org runs): the command says so on stderr first. `--json` stdout:

```json
{"restarted": true, "via": "launchd"}
```

`restarted` is a boolean (the service manager accepted the restart); `via` is one token: `launchd`, `systemd` or `schtasks`. Exit 3 when no service is registered (`Status` says not installed): the message tells to stop the daemon and start it again, or run `daemon install`; exit 1 when the service manager fails or, on Windows, the daemon is still running 10 seconds after the task was ended (the service manager's output is in the message). The command opens no database. How the daemon ends is the service manager's: this stage claims nothing about a graceful stop.

## Go entry points

`internal/apiconfig` (imports `openaiapi`, `daemonhb`, `autostart`, `apikeys`, `tlsserve`, `httpapi`; none of them imports it):

```go
// Settings are the saved values, all text in the syntax of the environment variable ("" = not saved).
type Settings struct {
	V1Addr, TLSCertFile, TLSKeyFile, Confinement, ContextConfinement, AutoConfinement,
	MaxConcurrent, TurnTimeout, ImageRuntimes, ToolRuntimes string
	// unexported: fields of the stored document this binary does not know
}
func Defaults() Settings
func Keys() []string                              // the ten keys, in document order
func LookupKey(name string) (key string, ok bool) // accepts v1_addr and v1-addr
func (s Settings) Get(key string) string
func (s *Settings) Set(key, text string) error    // unknown key only; the value is checked by Validate
func (s *Settings) Unset(key string)
func Validate(s Settings) []Problem               // the flags' and the environment's parsers on the text as typed; empty fields are skipped; the TLS pair is checked
func Canonical(key, text string) (string, error)  // for text Validate accepts: the stored spelling; otherwise why it fails its rule

type Problem struct{ Key, Message string }        // json: key, message

// Store.
const Row = "api_gateway_config"
func Load(ctx context.Context, db *sql.DB) (Settings, error)
func Update(ctx context.Context, db *sql.DB, fn func(*Settings) error) error // one BEGIN IMMEDIATE; fn error rolls back

// Resolution.
type Flags struct {
	V1Addr, Confinement, ContextConfinement, AutoConfinement string
	MaxConcurrent int // 0 = not given
}
type Resolved struct{ Key, Text, Source string }
func ResolveKey(key, flag string, getenv func(string) string, saved Settings) Resolved
func ResolveAll(f Flags, getenv func(string) string, saved Settings) []Resolved // ten, in order
func Overlay(saved Settings, getenv func(string) string) func(string) string
func EnvWithSaved(ctx context.Context, db *sql.DB, getenv func(string) string) (func(string) string, error) // Load + Validate + Overlay; the error names the setting

// Exposure gate.
type Widening struct{ Key, Reason string }       // json: key, reason
func Widens(before, after Settings) []Widening

// Documents. Show and Apply ask Installer.Status, which runs the service manager (launchctl print
// and systemctl when a registration exists under the home, schtasks /query always on Windows):
// every test sets Installer to a fake, and Heartbeat too, or they depend on the machine.
type Env struct {
	Getenv      func(string) string               // nil: os.Getenv. The process environment, without the saved layer
	Environment string                            // openaiapi.ReportSourceShell (default) or openaiapi.ReportSourceMCP
	Heartbeat   func() (daemonhb.Heartbeat, bool) // nil: daemonhb.Read
	Installer   autostart.Installer               // nil: autostart.New()
	Probe       ProbeFunc                         // BuildStatus only; nil: real HTTP probes
}
func Show(ctx context.Context, db *sql.DB, env Env) (ConfigReport, error)

type Change struct {
	Set     map[string]string // key -> value text
	Unset   []string          // keys; All unsets every key (and Unset must be empty)
	All     bool
	Confirm bool // the widening gate is open: the CLI's --yes, the MCP server's --allow-api-exposure
	DryRun  bool
}
// Apply reads the saved document, applies the change, checks it and runs Widens inside one
// BEGIN IMMEDIATE transaction, so the gate judges the row that is really replaced; a refusal
// rolls back. The document it returns is built after the commit.
func Apply(ctx context.Context, db *sql.DB, env Env, ch Change) (ChangeResult, error)
type ChangeResult struct {
	ConfigReport
	Applied  bool       `json:"applied"`
	Changed  []string   `json:"changed"`
	Widening []Widening `json:"widening"`
}

// Errors of Apply. Both implement error with a message that names the settings and the rules,
// and echo no value but an address or a path.
type ValidationError struct{ Problems []Problem } // unknown key, bad value, nothing to change, a half TLS pair, a key in both Set and Unset
type WideningError struct{ Widening []Widening}   // the change widens and Confirm is false

// State of one setting against the live daemon. hb is nil when no daemon is live (not_running);
// a heartbeat without api_settings, or without this key, is unknown.
func State(saved Settings, key string, hb *daemonhb.Heartbeat) string

// api status (moved from cmd; the document is unchanged).
func BuildStatus(ctx context.Context, db *sql.DB, env Env, profileID string) (StatusReport, error)
```

`ConfigReport`, `SettingReport`, `DaemonReport` have the fields and JSON names of the tables above (`SettingReport.Running` is a `*string`). A load failure of the saved document is an ordinary error, not a `ValidationError`: `errors.Is(err, apiconfig.ErrTooNew)` for a higher format version, `errors.Is(err, apiconfig.ErrDamaged)` for a row that cannot be decoded (P10), and anything else is the database's.

`internal/autostart`:

```go
type Installer interface { /* Install, Uninstall, Status, Start unchanged */ Restart(ctx context.Context) error }
type RestartResult struct { Restarted bool `json:"restarted"`; Via string `json:"via"` }
func ServiceManager() string                                         // "launchd", "systemd" or "schtasks"
func RestartRegistered(ctx context.Context, in Installer) (RestartResult, error)
type NotRegisteredError struct{ Detail string }                      // errors.As; Detail is Status's explanation, may be ""
```

`RestartRegistered` asks `Status` first and returns `*NotRegisteredError` without calling `Restart` when the service is not registered. The caller maps it: the CLI to exit 3, MCP to a tool error.

`internal/daemonhb`: `Heartbeat.APISettings`, `APISetting`, as above.

### As built, in addition to the lists above

Additions only: nothing above changed a name, a key, a field or a code. (The changes of 2026-10-05 are P9, which makes more `v1_addr` changes a widening, and P10, which adds the field `removed_unreadable_row`, `ErrDamaged`, `RemoveRowSQL`, `DamagedMessage` and the widening key `saved_settings`, and moves the exit code of a damaged row from 1 to 3.) What the stages after this one may also use:

```go
// internal/apiconfig
const FormatVersion = 1                  // the highest "v" this binary reads
const Row = "api_gateway_config"         // the settings-table key
var ErrTooNew error                      // a row with a higher "v": errors.Is; such a row is never rewritten, and never removed
var ErrDamaged error                     // a row that cannot be decoded (P10): errors.Is; CLI exit 3; `unset --all --yes` (Apply with All and Confirm) removes it
const DamagedMessage = "the saved settings are damaged" // how the message of every such error starts, whatever is wrong with the row
const WideningKeySavedSettings = "saved_settings"        // the Widening of removing a row that cannot be read; Apply adds it, Widens never returns it
const RemoveRowSQL = "delete from settings where key = 'api_gateway_config'" // by hand, for a row in a newer format
// ChangeResult gained RemovedUnreadableRow bool `json:"removed_unreadable_row,omitempty"` (P10), after Widening
const KeyV1Addr, KeyTLSCertFile, KeyTLSKeyFile, KeyConfinement, KeyContextConfinement, KeyAutoConfinement,
	KeyMaxConcurrent, KeyTurnTimeout, KeyImageRuntimes, KeyToolRuntimes = "v1_addr", ... // the ten keys
const SourceFlag, SourceEnv, SourceSaved, SourceDefault = "flag", "env", "saved", "default"
const StateApplied, StatePendingRestart, StateOverridden, StateNotRunning, StateUnknown = "applied", ...
type Spec struct{ Key, ServerFlag, Env, Default string }
func Specs() []Spec                      // the ten, in document order
func LoadValid(ctx context.Context, db *sql.DB) (Settings, error) // Load, then Validate: what a server may start on; the error wraps a *ValidationError that names each setting and how to fix it
func ValidListenAddr(addr string) error  // host:port, numeric port, host may be empty
type InputError struct{ Err error }      // a flag or an environment value that fails its rule: the caller's input (the CLI: exit 3); its message names the setting and the rule
func EffectivePolicy(addr, explicit string, getenv func(string) string) (openaiapi.Policy, error) // the policy of a listener bound to addr: explicit (a flag), else getenv, else the default of that kind of bind; a bad value is an *InputError
func EffectiveContextMax(explicit string, getenv func(string) string) (openaiapi.Class, error)
func EffectiveAutoMax(explicit string, getenv func(string) string) (openaiapi.Class, error)
type ProbeFunc func(base string, wantV1 bool) (reachable, v1Answers bool) // Env.Probe: GET /health, and /v1/models without a key (401: mounted)
func ProbeListener(base string, wantV1 bool) (reachable, v1Answers bool) // the real one, Env.Probe's default
func ProbeSchemes(loopback bool) []string
func ProbeAddr(addr string, loopback, wantV1 bool, probe ProbeFunc) (scheme string, reachable, v1Answers bool)
type StatusReport struct{ ... }          // the document of api status --json, moved from cmd unchanged
type ListenerReport struct{ ... }

// internal/openaiapi
func ParseMaxConcurrent(v string) (int, error)
func ParseTurnTimeout(v string) (time.Duration, error)
const DefaultMaxConcurrent = 4; DefaultTurnTimeout = 10 * time.Minute; MinTurnTimeout = 10 * time.Second

// internal/tlsserve
type Config struct { /* ... */ CertFile, KeyFile string } // an explicit pair, used when the environment names none
```

The MCP tool `api_models_list` (`internal/mcp/apikeys_tools.go`) still passes `os.Getenv` to the `openaiapi.Effective*` functions: it reads the environment and not the saved settings until stage 2 passes `apiconfig.EnvWithSaved` (or `Overlay`) there. The CLI's `api models` and `api status` already do.

## Mapping of errors per surface

CLI: `*ValidationError`, `*WideningError`, `*NotRegisteredError` and `apiconfig.ErrDamaged` (a saved row that cannot be read: P10) are exit 3, every other error exit 1 (`apiconfig.ErrTooNew` and a failing database included, neither of which is damage), message to stderr; the exit codes of the errors of `apiconfig` are given in one place, `asCLIError`, which every command that reads the row goes through. MCP (stage 2): the same are tool errors with the message as it is; `*WideningError` is refused unless the MCP server was started with `--allow-api-exposure`, which is what sets `Change.Confirm`; the model's arguments never do; that holds for the removal of a row that cannot be read too (`saved_settings`). Desktop (stage 3): a call keeps its exit code and last stderr line (`runAPICLI`: exit 3 reaches the app as `invalid_input: …`), so the dialog of a widening change comes from `--dry-run`, then `--yes`, and a damaged saved row is recognised by exit 3 and a message that starts `the saved settings are damaged` (`apiconfig.DamagedMessage`): the app offers the repair as `api config unset --all --dry-run` for the reason, then `--yes`. A row in a newer format is exit 1 and gets no reset.

## Risks

- A daemon started by hand next to a registered service makes `daemon restart` start a second daemon under the service, which cannot take the home's lock and exits; the service manager's keep-alive then retries. The help of the command says to stop a hand-started daemon first. `daemon install` over a running daemon has always had this.
- The saved row is read from the database the process opens. A daemon started by the login service uses the default database, so `--db-path` on `api config` edits a database that daemon does not read.
- `launchctl kickstart -k` and `systemctl --user restart` end the daemon the way their service manager does; the Windows task is ended with `schtasks /end`, and whether that stops the process the `cmd` wrapper started was not verified on a Windows machine (hence the wait for the lock). What the daemon does in its own shutdown is not changed here, and no document of this stage claims a graceful stop.
