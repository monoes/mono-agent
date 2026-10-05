# Mandatory monoes.me Account — B5b: documentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every claim that Mono Agent is local-first, free of network calls, usable offline without limit or needs no account says what is true once a monoes.me sign-in is required; agents get the account in AGENTS.md and `ref account`; release R carries its CHANGELOG notes; and the owner reads each rewritten statement before R ships.

**Architecture:** Documentation is test-driven: each task adds a guard test for its documents (retired phrases absent, required statements present, expected strings read from `internal/account` and from the CLI gate's classification, never retyped) and watches it fail before the rewrite. The `ref` topic is a new `ref_account.go` whose open and serving command lists are tied to the gate by a drift test. A review sheet lists every changed claim, before and after, for the owner, and the last task stops for the owner's reading before anything is pushed.

**Ships with release R.** These six tasks merge in the same push as B5a (the lead's schedule), so the documents ship with the behavior they describe. The license workstream is a separate plan, `2026-10-05-monoes-account-gate-b5d-license.md`, independent of R.

**Tech Stack:** Go 1.26 (stdlib only), cobra, Markdown and JSON locale files, and one bash snippet (the next version number).

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (D26, §3 "Documentation claims", §9) and `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md`.

## Global Constraints

From the index §2, the lines that bind this plan, verbatim.

- Offline grace: 24 hours from the signed `iat` of the newest token (D3, D15). A token with `exp - iat` above 24 hours, or `iat` more than 5 minutes ahead of now, is refused (D14). Clock guard: `now < hw - 5 minutes` locks with `clock_rollback`; a freshly verified token resets `hw` to its `iat` (§4.5).
- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Refresh (§4.4): a CLI process refreshes with under 5 minutes left, or when expired and the last attempt was over 1 minute ago (the negative cache), with a 2-second connect timeout. Long-running processes refresh at half the token lifetime and retry with backoff, 30 seconds doubling to 5 minutes. Other processes start the refresher after 5 minutes of running. The guard polls `session.json`'s mtime every 5 seconds. The refresh request carries `resource=<Audience>`.
- Storage (§4.6): `~/.monoagent/account/` (directory 0700) with `session.json`, `refresh.enc` and `session.lock` (files 0600). One session per OS user, shared by all profiles, whatever `--db-path` says.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Open commands (D6): `version`, `help`, `completion`, cobra's hidden `__complete` and `__completeNoDesc`, `ref`, `update`, `doctor` (with `doctor fix`), `setup`, `account` (all of it), `library login`, `library logout`, `library status`. Everything else is gated, except the serving commands:
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- `devaccount` is a build tag, never set by `release.yml`. Test seams panic unless `testing.Testing()`. No environment variable relaxes the gate in a default build; `MONOES_BASE_URL` still redirects the library only.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

The serving commands are documented and tested here (Task 3), in AGENTS.md and in `ref account`.

## Review Focus

1. **A container or headless install that goes wrong without a visible failure.** (a) It signs in but cannot unseal its refresh token: it works for the first hour; then the daemon cannot read `refresh.enc` back (no passphrase source for the file keyring), shows `grace` with `keyring_unavailable`, and stops doing work 24 hours after its last refresh with no earlier warning. The daemon is a serving command, so it stays up and the image's `HEALTHCHECK` (`version`, always open) keeps the container looking healthy while it refuses work. `secret keyring set-passphrase` is itself gated, so the documented order puts the passphrase file first. (b) A sign-in that is copied, restored from a backup or baked into an image goes stale at the first refresh of the original, and presenting the stale refresh token makes monoes.me end every sign-in of the account on every machine (plan A, spike S2): the documents say never to copy `~/.monoagent/account/`. Pinned by `TestAgentsDocCoversTheAccount` (Task 3), `TestRefAccountDocumentsTheGate` (Task 5), `TestDockerFilesSetUpTheSignIn` (Task 4) and `TestSecurityDocStatesWhatLeavesTheMachine` (Task 2).
2. **Documentation that becomes true only after the release that needs it.** R's first users would read "no phone-home" beside a build that calls monoes.me, and R's notes would be a commit dump, because `release.yml` reads `CHANGELOG.md` at the commit it tags. Pinned by `TestChangelogNamesTheEnforcementDate` (Task 6: fails while `account.EnforceDate()` is the zero time, unless the CHANGELOG names the date, and unless a release section above `[0.100.1]` tells the account story; later releases add sections above it without breaking it) and the landing rule below.
3. **A privacy statement that omits something sent, or says "no device identifier" with nothing behind it.** The e-mail address that `--email` sends, the client id and the IP address belong in it. Pinned by `TestSecurityDocStatesWhatLeavesTheMachine` (Task 2) and the code-reading step before it; B1b's `libraryfake.Server.TokenRequests()` pins the field set of a refresh and of a code exchange.
4. **A retired claim that survives where nobody looked, or a rewritten one that overshoots** (saying again that nothing is sent, or promising what a build before R does not do). The inventory covers 25 files; `staleClaims` fails on every one of them that a test reads, and Task 4's test reads the locale strings, the issue template, the install script, the Dockerfile and the compose file. The owner's reading before the push (the last step of Task 6) is the check that no test can be. Pinned by `TestFrontDoorDocsStateTheAccount`, `TestSecurityDocStatesWhatLeavesTheMachine`, `TestAgentsDocCoversTheAccount` and `TestPositioningTextsAreTrue`.
5. **A documented list that drifts from the gate, or a new `ref` page that breaks the gate's own inventory.** The open and serving commands are spelled in AGENTS.md and in `ref account`; a command opened or closed later, or `ref account` itself (every `ref` page inherits the open class), has to be pinned. Pinned by `TestOpenListMatchesTheGate` (both directions, three classes), `TestRefAccountFlagsExistOnTheCommands` and Task 5's pin step for B2's `TestEveryCommandIsClassified`.

---

## Landing and ordering (read first)

- **Tasks 1 to 6 ride in the same push as B5a.** `release.yml:446-450` takes a release's notes from the `## [x.y.z]` section of `CHANGELOG.md` at the commit it tags, and spec D26 puts the documentation no later than the first phase that makes implicit calls to monoes.me, B5a. Every merge to master releases, so two merges would ship R with the old claims and a commit-dump note. Execute these tasks on a branch cut from B5a's tip and push B5a's commits and these six together as one pull request; Task 6 ends with the check and with the owner's reading.
- **Written against the other plans as they stand.** B2's three gate classes, B1b's fallback to an older library login, B5a's update (a restart through the service manager, the `services.daemon` doctor row) and B5c's `MONOAGENT_DEV_ENFORCE_FROM`, B1a's own key-store item for the sign-in, B3a's locked daemon and B3b's doors are documented as those plans specify them; B1b's `account` flags (`--email`, `--send`, `--code`, `--no-browser`, `--timeout`; `--offline` on `status`) are the ones documented here, and Task 5 re-checks them against `--help`. If an owning plan changes one of these, the guard test or the text here changes with it.
- **Line numbers.** An edit names the lines its file has at `3cc58601`, before any edit of this plan. An earlier edit in the same file moves the later ones, so find each edit by its quoted text, or apply a file's edits from the last to the first.
- **The license workstream is the separate plan B5d**, independent of R: nothing here waits for it and it waits for nothing here. It edits some of the same documents later (the license statements and the release links), after R's documents have merged.
- **Positioning (D26).** "Local-first" is retired as a description everywhere below, not only beside telemetry: after R the product needs a sign-in and stops 24 hours after it loses monoes.me. Descriptions say "runs on your machine"; claims state the facts (data stays on your machine; a monoes.me sign-in is required; what is contacted). The retired phrases live in one variable, `staleClaims` (Task 1), so the owner can reword in one pass.
- **The documents describe the three phases without a date** (dormant: a build before R; warn: from R until the date; enforced: from the date). **The enforcement date is written into no document except the CHANGELOG**, at cut time (Task 6, step 3: the owner has chosen it and B5a's `rollout.go` holds it), where a test compares it with `account.EnforceDate()`; the other documents say that `account status` shows it (`enforce_from`). `<scratchpad>` below is the session's scratchpad directory.

## Review sheet for the owner

What the rewritten documents said before and say now, in the order the tasks write them. Each "Now" quotes the text the task installs; the owner reads the full statements in the tasks, and the last step of Task 6 hands over the diff.

| Where | Before | Now | Task |
|---|---|---|---|
| README tagline, first paragraph, feature list | "Local-first n8n alternative in a single Go binary"; "is a local-first automation platform"; "no telemetry" | "n8n alternative that runs on your machine, in a single Go binary"; "that runs on your machine"; "no analytics or usage counters" and "a monoes.me sign-in is required" | 1 |
| README, new callout | none | "A monoes.me account is required": how to sign in, the warning before the date and exit code 4 from it, the commands that always work, what is contacted, 24 hours without a connection | 1 |
| README, Docker and Uninstall | Docker without a sign-in step; "`monoagent-vault` entry" | the sign-in kept in the volume, "never baked into the image"; `account logout` first; "`monoagent-vault` entries" | 1 |
| README, Personal data | "nothing is sent to us, and there's no telemetry to opt out of" | "there are no analytics or usage counters to opt out of"; the sign-in and update checks listed in SECURITY.md | 1 |
| SECURITY.md, telemetry section | "Default: no telemetry"; "phone-home"; "no outbound calls on its own behalf" | "Network use, telemetry and crash reporting": "No analytics, no usage counters, no device identifier", then the complete list of what is contacted and sent | 2 |
| SECURITY.md, new subsections | none | "The sign-in on disk"; "One sign-in per machine, never copied"; "When a sign-in is locked"; "The sign-in requirement is a product check" | 2 |
| AGENTS.md, opening and "No telemetry" bullet | "local-first workflow automation engine"; "No telemetry: no analytics, phone-home checks, or usage counters" | "runs on your machine"; "A monoes.me account is required"; what can leave the machine | 3 |
| AGENTS.md, new section | none | "monoes.me account": commands, states and reasons, the three phases, what is open and what serves, a refusal, "Headless and Docker", never copy a sign-in | 3 |
| docs/COMPARISON.md | "works fully offline"; "Fully local data, no telemetry by default" | "needs a monoes.me sign-in and a connection to monoes.me at least once every 24 hours"; "Your data on your own machine, no analytics" | 4 |
| CLI help (`en.json`, `es.json`) | "Local-first workflow automation agent" | "Workflow automation agent that runs on your machine" and "A monoes.me account is required" | 4 |
| CHANGELOG.md, release R | none | "A monoes.me account is required." with the enforcement date, written at cut time | 6 |

---

## Inventory of claims (grep of 2026-10-05 at `3cc58601`; line numbers are at that commit, which differs from the master `f4441a2a` the other plans cite only by the spec and the index)

Searched: `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `CONTRIBUTING.md`, `CLAUDE.md`, `docs/`, the locale files, `wails.json`, `Dockerfile`, `docker-compose.yml`, `install.sh`, the desktop frontend, and every tracked file for `local-first`, `telemetry`, `phone[- ]home`, `offline`, `no account`, `self-hosted`, `never leaves`, `nothing is sent`, `entirely`, `no analytics`. Tick a line when its task is done.

- [ ] **README.md** (Task 1): 5 "Local-first n8n alternative"; 20 "is a local-first automation platform"; 25 "no telemetry"; 29 "monoagentcli library login"; 30 "log in with"; 56 "./monoagentcli version" (Quick Start); 627 "### Docker"; 641 "All state lives under" (Uninstall); 652 "`monoagent-vault` entry"; 659 "nothing is sent to us"; 725 "telemetry & crash-reporting statement"
- [ ] **SUPPORT.md** (Task 1): 50 "entirely local-first and does not"; **install.sh**: 148 "Verify with:"
- [ ] **SECURITY.md** (Task 2): 3 "local-first workflow automation tool"; 30 "runs entirely on your"; 181 "Default: no telemetry" and the section to line 208 ("phone-home", "no outbound calls on its own behalf")
- [ ] **AGENTS.md** (Task 3): 10 "local-first workflow automation engine"; 21 "No telemetry: no analytics"; 39 "monoagentcli library login"; 64 "`ref api`" (add `ref account`); 78 "Groups: `core`"; 84 "`services` (daemon, start at login)"; 86 "`accounts` (platform login expiry"; 119 "oriented toward the social platforms"; 184 "replaces this binary with the latest release"; 227 "All reads need a login"; 237 "## monoes.me library" (the account section goes before it); 262 "without a login exit 4"; 270 "The login is stored per profile"; 2161 "auth or connection failure"; 2187 "the CLI is fully usable without it"; 2206 "MONOAGENT_ALLOW_FILE_KEYRING"; 2212 "MONOES_BASE_URL"
- [ ] **CONTRIBUTING.md** (Task 3): 34 "CI runs both modes"
- [ ] **docs/COMPARISON.md** (Task 4): 9 "fully local data"; 18 "works fully offline"; 43 "Rely on a rich JS/TS ecosystem"; 49 "A single binary on a laptop"; 53 "Fully local data, no telemetry by default"
- [ ] **Other texts** (Task 4): `docs/USAGE_POLICY.md:11` "local-first workflow engine"; `docs/plans/workflow-marketplace-curation-policy.md:135` "no-phone-home design"; `CLAUDE.md:5` "local-first workflow"; `.agents/shared_instructions.md:7` "local-first workflow automation engine"; `.github/ISSUE_TEMPLATE/feature_request.yml:9` "local-first, single binary"; `scripts/install-linux-desktop.sh:81` "Local-first workflow automation dashboard"; `wails-app/wails.json:18` "Local-first workflow automation dashboard"; `internal/i18n/locales/en.json:2` and `internal/i18n/locales/es.json:2` "local-first", and `root.long` on line 3 of both; `Dockerfile:59` "ENTRYPOINT"; `docker-compose.yml:46` "environment:" and line 62 "Usage:"
- [x] **Kept, nothing to change**: `README.md:32` "honest, self-hosted n8n" (true: the engine runs on your machine); `README.md:57`, `AGENTS.md:47` and `:51` "offline" (the `ref` manual is offline); `AGENTS.md:2282` the monomind-managed blocks (not ours); `docs/security/threat-model.md:101` "no local-first tool defends against that" (about a class of tools, not this one); `docs/plans/2026-08-28-trust-hygiene-record.md:34` (a dated implementation record; lines 60 and 114 likewise); `docs/planning/FEATURE_n8n.md:1752` (n8n's own telemetry; 1766 and 5595 likewise); `chrome-extension/adapters/github.test.mjs:55` (a fixture quoting this repository's GitHub description); `wails-app/frontend/src/locales/en.json:56` "daemonOffline_one" (the daemon's status; `es.json:56` likewise; no other hit in the frontend locales); `wails-app/frontend/src/components/ImageEditorFullscreen.jsx:142` "runs locally" (true of that image model); `wails-app/frontend/src/components/settings/JevSection.jsx:456` "nothing is sent to TypeSafe" (true); `internal/orggrant/outbound.go:87`, `cmd/monoagentcli/automation_fixture_site.go:303` and `cmd/monoagentcli/automation_fixtures.go:85` "nothing leaves the machine" (local bookkeeping and in-process fixtures: true). There is no "About" screen (`git grep -n "About" -- wails-app/frontend/src` finds only `missingIsAboutJev`); the license swap of plan B5d re-checks, and updates an About screen's license text if one exists by then. Searched for advice or features that copy, back up, restore or snapshot `~/.monoagent` or the Docker volume (`back ?up|restore|migrat|rsync|new machine|volume`, and every `export`, `import` and archive command): none. `export`, `secret export`, `capture export`, `workflow export` and `action export` write people, vault entries, captures or workflows, never `account/`, and `README.md:494` (`secret import`, "restore on another machine") is the vault only. Outside this repository and not changed by this plan: the monoes.me site, any extension-store listing and package descriptions, which the owner checks for the same claims (the repository's GitHub description reads "Mono-agent engine — workflow automation with social platforms, AI services, and browser automation" as of 2026-10-05 and needs nothing).

---

## Tasks (documentation, D26)

### Task 1: The front-door documents and the guard helpers

**Files:**
- Create: `cmd/monoagentcli/docs_claims_test.go`
- Modify: `README.md`, `SUPPORT.md` (46–53), `install.sh` (148)
- Test: `cmd/monoagentcli/docs_claims_test.go`

**Interfaces:**
- Consumes (index §3.2): `account.GraceWindow` (`time.Duration`), `account.ClientID` (`string`), `account.LoginRequiredError{Status Status}` with `Error() string`, `account.Status{State, Reason}`, the `account.State*` and `account.Reason*` constants.
- Produces, for Tasks 2 to 6 (same package, `_test` files): `docText(t *testing.T, rel string) string`, `flat(s string) string`, `staleClaims []string`, `staleHits(text string) []string`, `assertNoStaleClaims(t, rel)`, `assertSays(t, rel string, wants ...string)`, `graceText() string`, `refusalLine() string`, `spelled(wrap string) []string`.

- [ ] **Step 1: Confirm the contract the tests read.** `go doc ./internal/account GraceWindow`, `go doc ./internal/account ClientID` and `go doc ./internal/account LoginRequiredError`. Expected: `const GraceWindow = 24 * time.Hour`, `const ClientID = "monoagent"` and `type LoginRequiredError struct{ Status Status }` with an `Error` method. Anything else means B1a changed the contract: stop and tell the lead.
- [ ] **Step 2: Write the failing test.** `cmd/monoagentcli/docs_claims_test.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// docText reads a file of the repository whose claims these tests keep true.
func docText(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// flat makes a text searchable: lower case, one line, single spaces.
func flat(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// staleClaims stopped being true once a monoes.me sign-in became required (spec D26).
// A bare "offline" is not one: the `ref` manual is offline, two commands have an
// --offline flag, and the grace message says "works offline until".
var staleClaims = []string{"local-first", "no telemetry", "phone-home", "phone home", "fully offline", "runs entirely on your machine",
	"nothing is sent to us", "no outbound calls on its own behalf", "does not depend on any hosted backend"}

func staleHits(text string) (hits []string) {
	for _, c := range staleClaims {
		if strings.Contains(flat(text), c) {
			hits = append(hits, c)
		}
	}
	return hits
}

func assertNoStaleClaims(t *testing.T, rel string) {
	t.Helper()
	for _, c := range staleHits(docText(t, rel)) {
		t.Errorf("%s still says %q: rewrite it to what is true (spec D26)", rel, c)
	}
}

func assertSays(t *testing.T, rel string, wants ...string) {
	t.Helper()
	text := flat(docText(t, rel))
	for _, w := range wants {
		if !strings.Contains(text, flat(w)) {
			t.Errorf("%s does not say %q", rel, w)
		}
	}
}

// What the documents quote from the contract, so that nothing is retyped.
func graceText() string { return fmt.Sprintf("%d hours", int(account.GraceWindow.Hours())) }

func refusalLine() string {
	err := &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}}
	return strings.SplitN(err.Error(), "\n", 2)[0]
}

// spelled is every state and reason, each wrapped in wrap (a backtick, or nothing).
func spelled(wrap string) (out []string) {
	for _, s := range []account.State{account.StateOK, account.StateGrace, account.StateLocked} {
		out = append(out, wrap+string(s)+wrap)
	}
	for _, r := range []account.Reason{account.ReasonNotLoggedIn, account.ReasonExpired, account.ReasonRefused, account.ReasonClockRollback,
		account.ReasonClockSkew, account.ReasonKeyUnknown, account.ReasonInvalid, account.ReasonUnreachable, account.ReasonServerError,
		account.ReasonKeyringUnavailable} {
		out = append(out, wrap+string(r)+wrap)
	}
	return out
}

func TestStaleClaimsLeaveTheOfflineManualAndFlagsAlone(t *testing.T) {
	for _, ok := range []string{"the offline manual", "`library status [--offline]`", "all offline, always current", "this login works offline until <time>"} {
		if hits := staleHits(ok); len(hits) != 0 {
			t.Errorf("%q is true and must not be flagged: %v", ok, hits)
		}
	}
	for _, bad := range []string{"Local-first n8n alternative", "No telemetry: no phone-home checks", "it works fully offline", "nothing is sent to us"} {
		if len(staleHits(bad)) == 0 {
			t.Errorf("%q is stale and must be flagged", bad)
		}
	}
}

func TestFrontDoorDocsStateTheAccount(t *testing.T) {
	assertNoStaleClaims(t, "README.md")
	assertNoStaleClaims(t, "SUPPORT.md")
	assertSays(t, "README.md", "A monoes.me account is required", "monoagentcli account login", "account login --email", "account logout",
		"login_required", graceText(), "SECURITY.md#network-use-telemetry-and-crash-reporting", "AGENTS.md#headless-and-docker",
		"MONOAGENT_ALLOW_FILE_KEYRING", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "SECURITY.md#one-sign-in-per-machine-never-copied")
	assertSays(t, "SUPPORT.md", "account status --json", "doctor --check core.monoes_account")
	assertSays(t, "install.sh", "account login")
}
```

- [ ] **Step 3: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run 'TestStaleClaims|TestFrontDoorDocsStateTheAccount' -count=1`. Expected: `TestStaleClaimsLeaveTheOfflineManualAndFlagsAlone` passes; `TestFrontDoorDocsStateTheAccount` fails with `README.md still says "local-first": rewrite it to what is true (spec D26)`, `README.md does not say "A monoes.me account is required"` and `install.sh does not say "account login"`.
- [ ] **Step 4: Edit `README.md`.**

- `README.md` line 5: replace `<h3 align="center">Local-first n8n alternative in a single Go binary<br/>— visual workflows, CLI, human-in-the-loop.</h3>` with `<h3 align="center">n8n alternative that runs on your machine, in a single Go binary<br/>— visual workflows, CLI, human-in-the-loop.</h3>`.
- `README.md` line 20: replace `**Mono Agent** is a local-first automation platform for humans **and** AI agents:` with `**Mono Agent** is an automation platform for humans **and** AI agents that runs on your machine:`.
- `README.md` line 25: replace `no telemetry (AI steps hand off to the separate monomind runner — see [How AI works](#how-ai-works-in-mono-agent)). All data stays on your machine (crash reports default to local files — see [SECURITY.md](SECURITY.md))` with `no analytics or usage counters (AI steps hand off to the separate monomind runner — see [How AI works](#how-ai-works-in-mono-agent)). All data stays on your machine; a monoes.me sign-in is required (see [A monoes.me account is required](#a-monoesme-account-is-required) and [SECURITY.md](SECURITY.md#network-use-telemetry-and-crash-reporting))`.
- `README.md` line 29: replace `` added with `monoagentcli library login` and `monoagentcli library install automation <id>` `` with `` added with `monoagentcli account login` (`library login` is the same sign-in) and `monoagentcli library install automation <id>` ``.
- `README.md` line 30: replace ``📚 **monoes.me library** — log in with `monoagentcli library login` (browsing needs a login, official items included), then browse, install and publish workflows`` with ``📚 **monoes.me library** — browsing needs your monoes.me sign-in (`monoagentcli account login`; `library login` is the same sign-in), official items included; then browse, install and publish workflows``.
- Replace `README.md` lines 43-45, which read:

```
> Mono Agent is an independent, unofficial, MIT-licensed project. It is not affiliated with, endorsed by, or connected to any of the platforms it can talk to.

---
```

with:

```
> Mono Agent is an independent, unofficial, MIT-licensed project. It is not affiliated with, endorsed by, or connected to any of the platforms it can talk to.

> ### A monoes.me account is required
>
> Sign in with `monoagentcli account login` (it opens your browser; on a machine without one, `account login --email you@example.com` mails you a code). Until the enforcement date a build that is not signed in runs and prints a warning; from that date it refuses to run (exit code 4, `login_required`). `monoagentcli account status` shows where you stand and the date (`enforce_from`). `version`, `help`, `ref`, `update`, `doctor`, `setup` and the `account` commands always work. From that date a daemon or server (`daemon`, `httpapi`, `mcp`) started without a sign-in stays up and refuses its work until you sign in.
>
> Your workflows, run history, people, credentials and files stay on your machine. The app contacts monoes.me to sign you in and keep the sign-in fresh (about every half hour while a long-running process such as the daemon runs) and GitHub to check for updates; [SECURITY.md](SECURITY.md#network-use-telemetry-and-crash-reporting) lists exactly what is sent. After it last reached monoes.me it keeps working for 24 hours without a connection.

---
```

- Replace `README.md` lines 56-57, which read:

```
./monoagentcli version
./monoagentcli ref                       # built-in offline docs: commands, nodes, expressions
```

with:

```
./monoagentcli version
./monoagentcli account login             # sign in to monoes.me (required, see above)
./monoagentcli ref                       # built-in offline docs: commands, nodes, expressions
```

- In `README.md`, after line 637 (it ends `…ompose.yml) and the env-var table in [AGENTS.md](AGENTS.md).`), insert:

````
A container needs a monoes.me sign-in kept in its volume (never baked into the image: a copied sign-in signs the account out everywhere, see [SECURITY.md](SECURITY.md#one-sign-in-per-machine-never-copied)). The daemon starts without one and stays up, logging the command to run, but from the enforcement date it refuses work until you sign in. A container has no OS keyring, so the sign-in's refresh token is sealed under the file keyring, whose passphrase the daemon must be able to read without a terminal. The compose file sets `MONOAGENT_ALLOW_FILE_KEYRING=1` and `MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=/data/keyring-pass`; once, before `up`:

```bash
printf '%s\n' "$PASSPHRASE" | docker compose run --rm -T --entrypoint sh monoagent -c 'umask 077; cat > /data/keyring-pass'
docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --send
docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --code <the code>
docker compose up -d --build
```

Without the passphrase file the daemon cannot read the sign-in back: `account status` then says `keyring_unavailable`, and the daemon stops doing work 24 hours after its last refresh. See [AGENTS.md](AGENTS.md#headless-and-docker).
````

- `README.md` line 642: replace `browser sessions, and crash reports) and, on macOS/Linux,` with ``browser sessions, crash reports, and the monoes.me sign-in in `account/`) and, on macOS/Linux,``.
- Replace `README.md` lines 647-648, which read:

```
rm /usr/local/bin/monoagentcli        # or wherever you installed it
rm -rf ~/.monoagent                   # workflows, vault, sessions, crash reports
```

with:

```
monoagentcli account logout           # revoke the monoes.me sign-in (best effort) and delete it here
rm /usr/local/bin/monoagentcli        # or wherever you installed it
rm -rf ~/.monoagent                   # workflows, vault, sessions, crash reports, the sign-in
```

- Replace `README.md` lines 651-655, which read:

```
Then remove the Chrome extension from `chrome://extensions`, and delete the
`monoagent-vault` entry from Keychain Access / Secret Service / Windows
Credential Manager manually — that's where the vault's OS-keychain-backed
encryption key lives, and the CLI never deletes it on uninstall since there's
no install hook to run it from.
```

with:

```
Then remove the Chrome extension from `chrome://extensions`, and delete the
`monoagent-vault` entries from Keychain Access / Secret Service / Windows
Credential Manager manually — that's where the vault's OS-keychain-backed
encryption key lives, and the key that seals your monoes.me sign-in. The CLI
never deletes them on uninstall since there's no install hook to run it from.
```

- `README.md` line 659: replace `machine — nothing is sent to us, and there's no telemetry to opt out of.` with `machine; there are no analytics or usage counters to opt out of. The app's own network traffic is the monoes.me sign-in and update checks, listed in [SECURITY.md](SECURITY.md#network-use-telemetry-and-crash-reporting).`.
- `README.md` line 725: replace `| [SECURITY.md](SECURITY.md) | Reporting, supported versions, telemetry & crash-reporting statement |` with `| [SECURITY.md](SECURITY.md) | Reporting, supported versions, what the app contacts (monoes.me sign-in, updates), telemetry & crash-reporting statement |`.

- [ ] **Step 5: Edit `SUPPORT.md` and `install.sh`.**

- Replace `SUPPORT.md` lines 46-53, which read:

```
## The `monoes_apis` backend

Out of scope for this repo's support channels. Mono Agent's core (the
`monoagentcli` binary, the workflow engine, the CLI/MCP surface, the Wails
GUI, the Chrome extension bridge) is entirely local-first and does not
depend on any hosted backend — see SECURITY.md's "Telemetry and crash
reporting" section for the complete, honest list of the only situations in
which it makes network requests.
```

with:

```
## Signing in to monoes.me

When signing in, or staying signed in, fails, run `monoagentcli account status --json`
and `monoagentcli doctor --check core.monoes_account`, and put the `state` and the
`reason` they print (never a token) in your Discussion or issue: the reason says whether
monoes.me refused the sign-in, could not be reached, or the machine's clock or key store
is the problem. `monoagentcli account login` signs in again.

## The `monoes_apis` backend

Out of scope for this repo's support channels. Mono Agent's core (the
`monoagentcli` binary, the workflow engine, the CLI/MCP surface, the Wails
GUI, the Chrome extension bridge) runs on your machine and depends on a
hosted service only for the monoes.me sign-in and the library — see
SECURITY.md's "Network use, telemetry and crash reporting" section for the
complete list of what it contacts and when.
```
- In `install.sh`, after line 148 (it ends `echo "Verify with:  ${INSTALL_DIR}/${BIN} version"`), insert:

```
echo "Sign in to monoes.me (required):  ${INSTALL_DIR}/${BIN} account login"
```

- [ ] **Step 6: Check which key store entries the sign-in creates.** `git grep -nE 'accountKEKID|keyringService|kekAccount' -- internal/secrets internal/account`. Expected (B1a): the sign-in's key is an item of its own, account `kek-monoes..account`, under the vault's service name `monoagent-vault` (or a record in the file keyring). The Uninstall paragraph above says "entries" for that reason; if the code names another service, name it there.
- [ ] **Step 7: Run the tests and the shell check.** `go test ./cmd/monoagentcli/ -run 'TestStaleClaims|TestFrontDoorDocsStateTheAccount' -count=1` (expected: `ok`) and `sh -n install.sh` (no output).
- [ ] **Step 8: Commit.**
  - `git add cmd/monoagentcli/docs_claims_test.go README.md SUPPORT.md install.sh`
  - `git commit -m "docs(account): say what is true in the README, SUPPORT and install.sh" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 2: SECURITY.md says exactly what leaves the machine

**Files:**
- Create: `cmd/monoagentcli/docs_security_test.go`
- Modify: `SECURITY.md` (3, 30–31, 39, 46, 179–208)
- Test: `cmd/monoagentcli/docs_security_test.go`

**Interfaces:** Consumes `account.ClientID` and Task 1's helpers. Produces nothing.

- [ ] **Step 1: Read what the sign-in code sends before writing the list.** `git grep -nE 'url\.Values|\.Set\(|\.Add\(|http\.Header|User-Agent' -- internal/account internal/library ':!*_test.go' ':!internal/library/libraryfake'`. The form keys must be exactly (B1b pins the refresh and the code exchange with `libraryfake.Server.TokenRequests()`; the older-login exchange of item 1 goes through the same refresher): authorization request `client_id`, `redirect_uri`, `response_type`, `scope`, `state`, `code_challenge`, `code_challenge_method`, `resource`; token exchange `grant_type`, `code`, `redirect_uri`, `client_id`, `code_verifier`, `resource`; refresh `grant_type`, `refresh_token`, `client_id`, `resource`; revoke `token` (and `client_id`); e-mail flow `email`, then `email` and `code`. Headers: `Content-Type`, `Accept`, and `Authorization: Bearer` on library calls. A device or host name, an install id, the app version or a `User-Agent` of ours would make "Nothing else is sent" below false: stop and ask the lead; do not write that sentence. For item 3 (the update check) run `git grep -nE 'User-Agent|Authorization|GITHUB_TOKEN|GH_TOKEN|Header\.(Set|Add)' -- cmd/monoagentcli/update.go cmd/monoagentcli/update_app.go internal/appupdate`: at plan time the only hit is `Accept: application/vnd.github+json`, so Go's default `User-Agent` goes out and nothing else. A token header or an identifier of ours there makes "no account or device identifier is sent" false: stop and ask the lead.
- [ ] **Step 2: Write the failing test.** `cmd/monoagentcli/docs_security_test.go`:

```go
package main

import (
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

func TestSecurityDocStatesWhatLeavesTheMachine(t *testing.T) {
	assertNoStaleClaims(t, "SECURITY.md")
	assertSays(t, "SECURITY.md", "## Network use, telemetry and crash reporting", "No analytics, no usage counters, no device identifier",
		"A monoes.me account is required", "the email address you type", "IP address", "client id `"+account.ClientID+"`", "refresh token",
		"GitHub, to check for updates", graceText(), "~/.monoagent/account/", "session.json", "refresh.enc", "session.lock",
		"The sign-in requirement is a product check", "not a security boundary", "The monoes.me session", "monoes..account",
		"### One sign-in per machine, never copied", "never copy or restore", "signs the account out everywhere",
		"### When a sign-in is locked", "does not sign out an install")
}
```

- [ ] **Step 3: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run TestSecurityDocStatesWhatLeavesTheMachine -count=1`. Expected: `SECURITY.md still says "local-first"` (and `"no telemetry"`, `"phone-home"`, `"runs entirely on your machine"`, `"no outbound calls on its own behalf"`) and `does not say "## Network use, telemetry and crash reporting"`.
- [ ] **Step 4: Edit `SECURITY.md`.**

- `SECURITY.md` line 3: replace `Mono Agent is a local-first workflow automation tool. This document explains` with `Mono Agent is a workflow automation tool that runs on your machine. This document explains`.
- Replace `SECURITY.md` lines 30-31, which read:

```
Mono Agent is a desktop/CLI automation platform that runs entirely on your
machine. In scope:
```

with:

```
Mono Agent is a desktop/CLI automation platform that runs on your machine
and signs in to monoes.me. In scope:
```

- In `SECURITY.md`, after line 39 (it ends `… The browser-extension bridge and bundled deployment scripts`), insert:

```
- The monoes.me session (`internal/account`: token verification, the sign-in on
  disk, the gate) and what it sends ([Network use](#network-use-telemetry-and-crash-reporting))
```

- In `SECURITY.md`, after line 46 (it ends `… leaks caused by how a user configured their own environment`), insert:

```
- Removing the sign-in requirement from a binary you built or patched yourself
  ([a product check](#the-sign-in-requirement-is-a-product-check))
```

- Replace `SECURITY.md` lines 179-208, which read:

```
## Telemetry and crash reporting

**Default: no telemetry.** There are no analytics, phone-home checks, or
usage counters, and Mono Agent makes no outbound calls on its own behalf.

**Crash reporting is local by default.** If the CLI crashes, it writes a
crash report to a file under `~/.monoagent/crashes/` on your machine —
nothing is transmitted. Filing a crash report to GitHub happens only when
**both** of the following are true:

- the environment variable `MONOAGENT_CRASH_REPORT=1` is set, and
- the `monomind` CLI is installed and on `PATH`.

Without either condition, crash data stays in the local file. There is no
automatic network fallback for crash reporting.

**Complete list of exceptions** — the only situations in which Mono Agent
makes network requests:

1. API calls made by workflows you run, against services you configured
   (HTTP nodes, service nodes, browser nodes, etc.)
2. Commands you explicitly invoke that talk to an external service — for
   example `login` (OAuth flows), `update` (release check/download) or
   `library` (the monoes.me library: sign-in, browsing, downloads you ask
   for, and uploads you publish; the host is `MONOES_BASE_URL` or
   https://monoes.me)
3. Opt-in crash reporting as described above

Everything else — workflow definitions, execution history, the secrets
vault, CRM data, and crash reports — stays on your machine.
```

with:

```
## Network use, telemetry and crash reporting

**No analytics, no usage counters, no device identifier.** Mono Agent does not
report what you run, which workflows you have or how often you use it. A
monoes.me account is required, so it does contact monoes.me, and it checks for
updates at GitHub. This is the complete list of the situations in which it makes
network requests:

1. **monoes.me, to sign in and stay signed in.** `account login` (and `library
   login`, the same sign-in) runs the OAuth 2.1 authorization-code exchange with
   PKCE: the client id `monoagent`, the scopes, a loopback redirect address and
   the code with its verifier; with `--email` instead, the email address you type
   and then the code mailed to it. A refresh follows while a `monoagentcli`
   process runs: a long-running one (the daemon, a server, the desktop app) about
   every half hour, a short command only when the token has under five minutes
   left. It sends the refresh token, the client id and a `resource` naming Mono
   Agent. `account logout` revokes the refresh token. On the first run of the
   release that requires the account, a monoes.me library login left by an older
   release is exchanged once for this sign-in (its refresh token, the client id
   and the `resource`); if monoes.me does not allow that, nothing changes and you
   sign in once more. monoes.me learns which account, when, and the IP address
   the request comes from. Nothing else is sent: no device identifier, no usage
   data, no workflow content, no file path.
2. **monoes.me, for the library** (`library list`, `show`, `install`, `publish`,
   `update`), only when you run those commands, with your signed-in token; the
   host is `MONOES_BASE_URL` or https://monoes.me.
3. **GitHub, to check for updates and download them:** `update`, `update --app`,
   `doctor --deep` and the desktop app (at start and every 24 hours). GitHub sees
   the IP address and Go's default `User-Agent`; no account or device identifier
   is sent. Every download is verified against the release's `SHA256SUMS.txt`.
4. API calls made by workflows you run, against services you configured (HTTP
   nodes, service nodes, browser nodes, etc.), and commands you explicitly invoke
   that talk to an external service, for example `login` (OAuth flows for
   third-party platforms).
5. Opt-in crash reporting, below.

**Without a connection**, Mono Agent keeps working for 24 hours after the last
successful contact with monoes.me. Then every gated command refuses with exit
code 4 (`login_required`) until it can reach monoes.me again; `version`, `help`,
`ref`, `update`, `doctor`, `setup` and the `account` commands always work, and a
daemon or server (`daemon`, `httpapi`, `mcp`, `extension serve`) stays up and
refuses its work. A blocked or revoked account is locked at the next refresh,
within about an hour.

**Crash reporting is local by default.** If the CLI crashes, it writes a
crash report to a file under `~/.monoagent/crashes/` on your machine —
nothing is transmitted. Filing a crash report to GitHub happens only when
**both** of the following are true:

- the environment variable `MONOAGENT_CRASH_REPORT=1` is set, and
- the `monomind` CLI is installed and on `PATH`.

Without either condition, crash data stays in the local file. There is no
automatic network fallback for crash reporting.

Everything else — workflow definitions, execution history, the secrets
vault, CRM data, and crash reports — stays on your machine.

### The sign-in on disk

One sign-in per OS user, shared by every profile, in `~/.monoagent/account/`
(directory 0700, whatever `--db-path` says), created when you sign in:
`session.json` (0600), the signed access token, valid for about an hour, and its
state, readable without the keychain so that a check never triggers a keychain
prompt; `refresh.enc` (0600), the refresh token sealed with AES-256-GCM under a
key from the OS keyring or, where there is none and `MONOAGENT_ALLOW_FILE_KEYRING=1`
is set, the file keyring (see above), read only by a refresh; and `session.lock`
(0600), which keeps two processes from refreshing at once. `account logout`
deletes the token and the refresh token. The token is verified on your machine
against public keys built into the binary: there is no network call per command.

The key that seals the refresh token is an item of its own in that key store (in
the OS keychain, under the vault's service name `monoagent-vault`). In a file
keyring it is listed by `secret keyring status` as the id `monoes..account` and
follows the vault keyrings' passphrase rules: `secret keyring set-passphrase` also
checks that the new passphrase unlocks it. It exists only after an explicit
`account login` creates it.

### One sign-in per machine, never copied

Sign in once on each machine. Never copy or restore `~/.monoagent/account/`
between machines or from an old backup or disk image, and never bake a sign-in
into a Docker image or a container snapshot: keep it in the container's volume.
monoes.me rotates the refresh token at every refresh and treats one that was
already used as stolen: it ends every Mono Agent sign-in of that account, on every
machine. A stale copy, once presented, therefore signs the account out everywhere,
and each machine then has to sign in again.

### When a sign-in is locked

The reason `refused` means that monoes.me answered `invalid_grant` to a refresh:
the account was blocked, the sign-in was revoked, or a refresh token that was
already used was presented (a copied sign-in, above). The machine locks at its next
refresh (within about an hour of a block), work in flight is cancelled, and `account
login` is the way back in; a blocked account stays refused until monoes.me unblocks
it. Every other failure, such as no network, a server error or any other OAuth
error, is not a refusal: the machine keeps working until it has gone 24 hours
without reaching monoes.me (`expired`).

Signing out of the monoes.me website does not sign out an install. An install keeps
its own sign-in until you run `account logout` on it, monoes.me blocks the account
or revokes the sign-in, or it goes 24 hours without reaching monoes.me.

### The sign-in requirement is a product check

Mono Agent checks for a valid sign-in before it runs a command, and again while
a daemon, an API, an MCP server or the extension bridge runs. This is a check in
the official builds, not a security boundary: a binary you build or patch
yourself can remove it, a copy of the session files works on another machine for
a while (it counts accounts, not devices) until one of the two copies presents a
refresh token the other has already used, which ends every sign-in of the account
(see above), and releases before the enforcement release do not have it. A bypass
of an official build (a token that verifies when it should not, a gated door that
stays open) is a vulnerability: report it as described above. Removing the check
from a build of your own is not.
```

- [ ] **Step 5: Run the test.** `go test ./cmd/monoagentcli/ -run TestSecurityDocStatesWhatLeavesTheMachine -count=1`. Expected: `ok`.
- [ ] **Step 6: Commit.**
  - `git add cmd/monoagentcli/docs_security_test.go SECURITY.md`
  - `git commit -m "docs(security): list exactly what leaves the machine and what the sign-in is" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 3: AGENTS.md and CONTRIBUTING.md for agents and developers

**Files:**
- Create: `cmd/monoagentcli/docs_agents_test.go`
- Modify: `AGENTS.md`, `CONTRIBUTING.md` (35)
- Test: `cmd/monoagentcli/docs_agents_test.go`

**Interfaces:**
- Consumes: `newRootCmd() *cobra.Command` (root.go:36); B2 Task 1's `applyClassification(root *cobra.Command)`, `commandClass(c *cobra.Command) string`, `commandKey(c *cobra.Command) string` and the constants `classOpen` and `classServe` (the annotation `monoagent.account` has the values `open`, `gated` and `serve`: B2's approved Contract change request 1; index §3.5, spec §6.1 and §6.4); Task 1's helpers.
- Produces, for Task 5: `openCommands` and `servingCommands []string`, `assertListText(t *testing.T, where, text, marker string, paths []string)`.

- [ ] **Step 1: Confirm B2's names and B5a's script.** `grep -nE 'func (applyClassification|commandClass|commandKey)\(|classServe' cmd/monoagentcli/account_gate.go` (expected: the three functions and the `serve` class) `ls scripts/check-release-tags.sh`, `grep -rn 'MONOAGENT_DEV_ENFORCE_FROM' internal/account` (a file with the `devaccount` tag that reads the variable) and `ls internal/accountsmoke` (the real-binary smoke of B5c, which also owns the CI job `account-smoke`). If B5c dropped the variable or the package, delete their sentences from the AGENTS.md, CONTRIBUTING.md, CLAUDE.md and `ref account` texts below, and their strings from the tests. If a function is spelled differently, change its calls in `TestOpenListMatchesTheGate` (the only ones); if the script is named otherwise, use that name in `TestContributingExplainsTheDevelopmentBuild` and in the CONTRIBUTING.md edit below.
- [ ] **Step 2: Write the failing test.** `cmd/monoagentcli/docs_agents_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The documents spell two lists that the CLI gate's table decides: the commands
// that never need a monoes.me sign-in (spec D6) and the serving commands, which
// start even when locked and refuse their work instead (spec §6.4).
// TestOpenListMatchesTheGate ties both lists to the table in both directions, so
// opening or closing a command takes a deliberate edit of the table, these lists
// and the documents.
var (
	openCommands    = []string{"version", "help", "completion", "ref", "update", "doctor", "setup", "account", "library login", "library logout", "library status"}
	servingCommands = []string{"daemon", "httpapi", "mcp", "extension serve"}
)

// listed reports whether key is one of paths or below one (a command inherits its parent's class).
func listed(paths []string, key string) bool {
	for _, p := range paths {
		if key == p || strings.HasPrefix(key, p+" ") {
			return true
		}
	}
	return false
}

func TestOpenListMatchesTheGate(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)
	for class, paths := range map[string][]string{classOpen: openCommands, classServe: servingCommands} {
		for _, p := range paths {
			// cobra adds help and completion only while it executes
			if c, _, err := root.Find(strings.Fields(p)); err == nil && c != root && commandClass(c) != class {
				t.Errorf("%q is documented as %s but the gate classifies it %s", p, class, commandClass(c))
			}
		}
	}
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, c := range parent.Commands() {
			switch key := commandKey(c); commandClass(c) {
			case classOpen:
				if !listed(openCommands, key) {
					t.Errorf("%q is open in the gate but not in the documented open list: update the documents and openCommands", key)
				}
			case classServe:
				if !listed(servingCommands, key) {
					t.Errorf("%q is a serving command in the gate but not in the documented list: update the documents and servingCommands", key)
				}
			}
			walk(c)
		}
	}
	walk(root)
}

// assertListText checks that the paragraph of text that starts at marker names every command of paths.
func assertListText(t *testing.T, where, text, marker string, paths []string) {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		t.Errorf("%s has no paragraph starting %q", where, marker)
		return
	}
	para := text[i:]
	if j := strings.Index(para, "\n\n"); j > 0 {
		para = para[:j]
	}
	for _, p := range paths {
		if !strings.Contains(flat(para), p) {
			t.Errorf("the paragraph of %s that starts %q does not name %q", where, marker, p)
		}
	}
}

func TestAgentsDocCoversTheAccount(t *testing.T) {
	assertNoStaleClaims(t, "AGENTS.md")
	assertSays(t, "AGENTS.md", append(spelled("`"),
		"## monoes.me account", "### Headless and Docker", refusalLine(), "login_required", "account login --email", "account status --json",
		"account logout", "MONOAGENT_ALLOW_FILE_KEYRING", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "-tags devaccount", "core.monoes_account",
		"`ref account`", "No credential comes from an environment variable", "daemon restart", graceText(),
		"never copy or restore", "signs the account out everywhere", "services.daemon.restart", "MONOAGENT_DEV_ENFORCE_FROM", "monoes..account")...)
	if strings.Contains(flat(docText(t, "AGENTS.md")), "stored per profile in the encrypted vault") {
		t.Error("AGENTS.md still says the library login is stored per profile in the vault: it uses the machine session")
	}
	agents := docText(t, "AGENTS.md")
	assertListText(t, "AGENTS.md", agents, "**What is open**", openCommands)
	assertListText(t, "AGENTS.md", agents, "**Serving commands**", servingCommands)
}

func TestContributingExplainsTheDevelopmentBuild(t *testing.T) {
	assertSays(t, "CONTRIBUTING.md", "-tags devaccount", "scripts/check-release-tags.sh", "accounttest.Install", "MONOAGENT_DEV_ENFORCE_FROM", "internal/accountsmoke")
}
```

- [ ] **Step 3: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run 'TestOpenListMatchesTheGate|TestAgentsDocCoversTheAccount|TestContributingExplainsTheDevelopmentBuild' -count=1`. Expected: `TestOpenListMatchesTheGate` passes (the gate already matches the list); the others fail with `AGENTS.md still says "local-first"`, `does not say "## monoes.me account"`, `has no paragraph starting "**What is open**"` (and the same for `"**Serving commands**"`) and `CONTRIBUTING.md does not say "-tags devaccount"`.
- [ ] **Step 4: Edit `AGENTS.md`.** The account section goes in as one block, before `## monoes.me library`.

- Replace `AGENTS.md` lines 10-11, which read:

```
**mono-agent** is a local-first workflow automation engine — an n8n
alternative packed into a single Go binary (`monoagentcli`). Build workflows
```

with:

```
**mono-agent** is a workflow automation engine that runs on your machine — an n8n
alternative packed into a single Go binary (`monoagentcli`). Build workflows
```

- Replace `AGENTS.md` lines 21-26, which read:

```
- No telemetry: no analytics, phone-home checks, or usage counters. What
  can leave the machine (see [SECURITY.md](SECURITY.md) for the full
  statement): the API calls your workflows make, commands you explicitly
  invoke that talk to an external service (e.g. `login`, `update`,
  `library`), and
  opt-in crash reporting. Crash reports are written to local files under
```

with:

```
- A monoes.me account is required: sign in with `monoagentcli account login`
  (see [monoes.me account](#monoesme-account)). Your data stays on your
  machine, and there are no analytics or usage counters. What can leave the
  machine (see [SECURITY.md](SECURITY.md) for the full statement): the
  monoes.me sign-in and its refresh (the account, the time and the IP address;
  with `--email`, the address you type), update checks to GitHub, the API
  calls your workflows make, commands you explicitly invoke that talk to an
  external service (e.g. `login`, `library`), and
  opt-in crash reporting. Crash reports are written to local files under
```

- Replace `AGENTS.md` lines 39-40, which read:

```
  installed: `monoagentcli library login`, then
  `monoagentcli library install automation <id>` (see
```

with:

```
  installed: `monoagentcli account login` (`library login` is the same
  sign-in), then `monoagentcli library install automation <id>` (see
```

- In `AGENTS.md`, after line 64 (it ends ``…, status-code mapping, and the OpenAI-compatible `/v1` API |``), insert:

```
| `ref account` | The monoes.me account — commands, states and reasons, what stays open, headless and Docker sign-in |
```

- In `AGENTS.md` line 78, replace `` Groups: `core` (data folder, database, profile, vault, PATH, disk), `monomind` `` with:

```
Groups: `core` (data folder, database, profile, vault, PATH, disk, the monoes.me
sign-in as `core.monoes_account` — ok; warn in the grace window or before the
enforcement date; fail when locked; fix `account login`, or `update` for
`key_unknown`), `monomind`
```

- In `AGENTS.md` line 84, replace `` `services` (daemon, start at login) and `integrations` `` with:

```
`services` (daemon, start at login; the daemon row warns when the running daemon's
version differs from the binary's, manual fix `services.daemon.restart`:
`daemon restart`) and `integrations`
```

- `AGENTS.md` line 86: replace ``on demand with `--deep`), `accounts` (platform login expiry;`` with ``on demand with `--deep`), `accounts` (third-party platform login expiry, not the monoes.me sign-in;``.
- In `AGENTS.md` line 119, replace `oriented toward the social platforms and the CRM features. The` with:

```
oriented toward the social platforms and the CRM features. `login` and `logout`
there capture social-platform sessions; they have nothing to do with the monoes.me
account (`account login`). The
```

- In `AGENTS.md`, after line 184 (it ends ``…entcli update` replaces this binary with the latest release.``), insert:

```
- After a successful `update` or `update --app`, a daemon registered for auto-start
  that runs a different version is restarted through the service manager, in the
  update's own process (the new binary's gated `daemon restart` could refuse);
  `update` does the same when the binary was already current. Nothing is restarted
  while a workflow execution is in flight: an update never interrupts a run.
  `update --app --json` carries an optional `daemon` object (`action`: `restarted`,
  `busy`, `settings`, `unknown`, `not_registered` or `failed`; `running_version`,
  `in_flight`, `via`, `message`), and the message is also one progress line. When
  it does not restart, it says why and leaves that to `daemon restart`, and the
  `services.daemon` row of `doctor` warns about the version mismatch until then.
  The first update to the release that requires the account is made by the
  previous binary, which cannot restart anything: the same row flags the old
  daemon until the next `update`, a restart or a reboot.
```

- `AGENTS.md` line 227: replace ``All reads need a login: without one they exit 4 with `"login_required": true`. `library login` streams`` with ``All reads need the monoes.me sign-in: without one they exit 4 with `"login_required": true`. `library login|logout|status` are aliases of `account login|logout|status`; `library login` streams``.
- In `AGENTS.md`, before line 236 (it starts `## monoes.me library`), insert:

````
## monoes.me account

Mono Agent needs a monoes.me account signed in on the machine: once the enforcement
date has passed, the CLI, the daemon, the desktop app, the HTTP and `/v1` APIs, the
MCP server and the extension bridge refuse to work without one. `monoagentcli ref
account` is this section, offline and always current.

```bash
monoagentcli account login                       # browser sign-in (OAuth 2.1 + PKCE)
monoagentcli account login --email you@x.com     # headless: emails a code, then asks for it (--send / --code split the steps)
monoagentcli account status [--offline] [--json] # refreshes first if due, unless --offline; exit 0 for ok and grace, 4 for locked
monoagentcli account logout                      # revokes the refresh token (best effort), deletes the local sign-in
```

`library login|logout|status` are aliases. One session per OS user, shared by every
profile, in `~/.monoagent/account/`. No credential comes from an environment variable
and there are no unattended machine tokens. An agent that gets exit 4 with
`login_required` asks the user to run `monoagentcli account login`; it never looks
for, pastes or sets a token. `account status --json` prints
`{"v":1,"state","reason","user","plan","issued_at","valid_until","grace_until","enforce_from","enforced"}`;
`valid_until` is the access token's expiry (about an hour), `grace_until` its issue
time plus 24 hours.

**Sign in once per machine; never copy or restore `~/.monoagent/account/` between
machines or from an old backup, and never bake a sign-in into a Docker image.**
monoes.me rotates the refresh token at every refresh and treats one that was already
used as stolen: it ends every Mono Agent sign-in of that account, on every machine. A
stale copy, once presented, signs the account out everywhere, and each machine then
has to sign in again. Signing out of the monoes.me website does not sign out an
install.

| `state` | Meaning | `reason` |
|---|---|---|
| `ok` | verified and not expired | empty |
| `grace` | verified and expired, within 24 hours of its issue time: work continues | why it was not refreshed: `unreachable`, `server_error`, `keyring_unavailable` |
| `locked` | work is refused | `not_logged_in`, `expired` (24 hours without a refresh), `refused` (monoes.me answered `invalid_grant`: the account is blocked, the sign-in was revoked, or an already-used refresh token was presented, as from a copied session; work in flight is cancelled), `clock_rollback`, `clock_skew`, `key_unknown` (run `update`), `invalid` |

Only an `invalid_grant` answer to a refresh is a refusal; every other failure counts
as unreachable, so the 24-hour grace applies. A blocked account is locked at the next
refresh, within about an hour. A build before the enforcement release is dormant
(nothing locks, warns or calls monoes.me); from the release that sets the date until
`enforce_from`, gated commands run and print `A monoes.me login will be required from
<date>: monoagentcli account login` on stderr; from the date they refuse (`enforced`
says which).

**What is open** (never needs a sign-in): `version`, `help`, `completion`, `ref`,
`update`, `doctor` (with `doctor fix`), `setup`, the `account` commands, and
`library login`, `library logout` and `library status`. Every other command is
gated, local-only ones (`people`, `secret`, `image`, `profile`, `config`) included.

**Serving commands** start even when locked and, from the enforcement date, refuse
their work instead, so a service manager or a container never respawns them in a
loop: `daemon`, `httpapi`, `mcp` and `extension serve` (also `bridge serve`).
`daemon install`, `daemon restart`, `daemon uninstall` and `org serve` are gated.

**A refusal** does nothing else (no first-run writes, no database open) and exits 4.
The first line on stderr is `Log in to monoes.me first: monoagentcli account login`;
with `--json` anywhere in the arguments stdout also gets
`{"error":"…","code":"auth_or_connection","login_required":true,"account":{"state","reason"}}`.
In grace the command runs and prints one stderr line, `monoes.me is unreachable; this
login works offline until <time>`. Branch on exit 4 and `login_required`, not on the
text. A running process checks again, and a serving command started while locked
answers the same way: the HTTP API and `/v1` answer 401 `login_required` (`GET
/health` stays open and reports the account state), the webhook server 503, the
extension bridge `account_locked`, MCP a tool error. A refused workflow run is
recorded FAILED with an error starting `login_required:`; paused and queued runs
wait. A locked daemon stays up, logs the command to run, starts nothing new, stops
its org services and resumes by itself when a sign-in appears.

### Headless and Docker

Sign in once with `account login --email`; the sign-in lives in
`~/.monoagent/account/` of the OS user that runs the daemon (another user's daemon
does not see it; in Docker the image's `HOME=/data` puts it on the volume). The
refresh token is sealed under the vault's key store: the OS keychain or, where
there is none (a container, a server), the file keyring, enabled with
`MONOAGENT_ALLOW_FILE_KEYRING=1`. Its passphrase comes from
`MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE`, `~/.monoagent/keyring-passphrase`, stdin or
the terminal, in that order (see [Secrets](#secrets)). A daemon has no terminal, so it
needs the passphrase in a file; without one it cannot read the refresh token back,
`account status` shows `grace` with the reason `keyring_unavailable`, and the daemon
stops doing work 24 hours after its last refresh. Make the file **before** the first
sign-in, because `secret keyring set-passphrase` is a gated command:

```bash
# Docker: docker-compose.yml sets both variables; the passphrase file is /data/keyring-pass
printf '%s\n' "$PASSPHRASE" | docker compose run --rm -T --entrypoint sh monoagent -c 'umask 077; cat > /data/keyring-pass'
docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --send
docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --code <code>
docker compose up -d
# A server: make the passphrase file as in SECURITY.md ("File-based keyring fallback"), then
MONOAGENT_ALLOW_FILE_KEYRING=1 MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=~/.config/monoagent-keyring-pass monoagentcli account login --email you@example.com
```

The daemon is a serving command: started with no sign-in it stays up, logs the command
to run and, from the enforcement date, refuses work, so `docker compose up` before
signing in is safe. Then make
the passphrase file and sign in with `docker compose exec monoagent monoagentcli …`
instead of `docker compose run --rm …`; the daemon resumes within seconds. A healthy
container is not a signed-in one: `docker exec monoagent monoagentcli account status
--offline`; `doctor` has a row for it (`core.monoes_account`, fix `account login`).

Keep the sign-in in the volume only: never bake one into an image or restore a copy of
another machine's (the warning above). In a file keyring, `secret keyring status` lists
the account's key as `monoes..account` once an explicit `account login` has created it.
````

- Replace `AGENTS.md` lines 262-264, which read:

```
  without a login exit 4 with `Log in to monoes.me first: monoagentcli
  library login` before any network call (`--json`: `{"error", "code":
  "auth_or_connection", "login_required": true}`). A 401 on a call
```

with:

```
  without a sign-in exit 4 with `Log in to monoes.me first: monoagentcli
  account login` before any network call (`--json`: `{"error", "code":
  "auth_or_connection", "login_required": true, "account": {…}}`). A 401 on a call
```

- Replace `AGENTS.md` lines 270-272, which read:

```
  loopback dev server). The login is stored per profile in the encrypted
  vault (entry `monoes-library`, one per host) and refreshed
  automatically; `logout` revokes and forgets it.
```

with:

```
  loopback dev server). The library uses the machine session of
  `account login` (one per OS user, refreshed automatically). A login an
  older release left in a profile's vault (entry `monoes-library`) is read as
  a fallback and, on the first run of the release that requires the account,
  moved into the session when monoes.me allows it; `library login` writes only
  the session, and `library logout` ends it and forgets the older login too.
```

- `AGENTS.md` line 2161: replace `| 4 | auth or connection failure |` with ``| 4 | auth or connection failure — including `login_required`: no valid monoes.me sign-in (see [monoes.me account](#monoesme-account)) |``.
- In `AGENTS.md`, after line 2188 (it ends `regardless of where the binary runs from.`), insert:

```
A build you make runs the real gate. For development and CI, build the real
binary with `-tags devaccount` (`go build -tags devaccount -o /tmp/monoagentcli
./cmd/monoagentcli`): it also trusts a development signing key, lets the gate
honor `MONOES_BASE_URL`, so a fake monoes.me can sign you in, and reads
`MONOAGENT_DEV_ENFORCE_FROM` (an RFC 3339 time), so that a test can force the warn
period (`2999-01-01T00:00:00Z`: nothing locks) or enforcement (a past date); it
never relaxes anything, and a default build does not read it. Releases never carry
the tag, and the release workflow fails if a shipped binary lists it. Unit tests
need no tag: `accounttest.Install` and `account.SetTrustedKeysForTest` swap the
keys and the clock, and panic outside a test binary.
```

- `AGENTS.md` line 2206: replace ``Default: unset — `secret add` fails closed on machines without a keyring. |`` with ``Default: unset — `secret add` and `account login` fail closed on machines without a keyring. |``.
- In `AGENTS.md` line 2212, replace ``| `MONOES_BASE_URL` | monoes.me library host for the `library` commands. Default: unset — `https://monoes.me`. Plain `http` is accepted only for a loopback host (a local dev server). |`` with:

```
| `MONOES_BASE_URL` | monoes.me host for the `library` commands. Default: unset — `https://monoes.me`. Plain `http` is accepted only for a loopback host (a local dev server). The sign-in gate honors it only in a `-tags devaccount` build; in any other build it redirects the library alone: the session token is never sent to another host, `library login` refuses to sign in there, and the sign-in is verified against the keys built into the binary. |
| `MONOAGENT_DEV_ENFORCE_FROM` | Forces the warn period (a future date such as `2999-01-01T00:00:00Z`) or enforcement (a past date) for a test: an RFC 3339 time. Read only by a `-tags devaccount` build; a default build does not read it, so it never relaxes anything. Default: unset — the date compiled into the binary. |
```

- [ ] **Step 5: Edit `CONTRIBUTING.md`.**

- In `CONTRIBUTING.md`, after line 35 (it ends ``default (social included) and `-tags nosocial` modes.``), insert:

````
### The monoes.me sign-in while you develop

A build from source runs the same gate as a release: gated commands refuse without
a monoes.me sign-in. Unit tests need nothing (`accounttest.Install` and
`account.SetTrustedKeysForTest` swap the keys and the clock, and panic outside a
test binary). To run the real binary, build it with the `devaccount` tag, which also trusts a
development signing key and lets the gate honor `MONOES_BASE_URL`:

```bash
go build -tags devaccount -o /tmp/monoagentcli ./cmd/monoagentcli
```

`MONOAGENT_DEV_ENFORCE_FROM` (an RFC 3339 time) is read only by a `devaccount`
build. A test uses it to force the warn period (a future date such as
`2999-01-01T00:00:00Z`: nothing locks) or enforcement (a past date), and it never
relaxes anything: a default build does not read it. Scripts and CI jobs that run a
gated command but do not test the gate build with the tag and set it to a date that
never comes; only `internal/accountsmoke` (CI job `account-smoke`) signs in:
`go test -tags devaccount ./internal/accountsmoke/ -count=1 -timeout 25m`.

Never ship that binary: `release.yml` never sets the tag and fails the release if a
shipped binary lists it (`scripts/check-release-tags.sh`).
````

- [ ] **Step 6: Check the older library login.** `git grep -n 'VaultSecretName\|monoes-library' -- internal/library cmd/monoagentcli ':!*_test.go'`. Expected (B1b): the profile's vault login is only read as a fallback, adopted into the session, or forgotten at `library logout`, and `library login` writes only the session. If B1b still writes it, say so in the `AGENTS.md:270` bullet and drop the `stored per profile` check from `TestAgentsDocCoversTheAccount`.
- [ ] **Step 7: Check the library message.** `git grep -n 'Log in to monoes.me first' -- '*.go'`. Expected: the definition in `internal/account` and no `library login` variant. If the library path still prints one, quote that text in the `AGENTS.md:262` bullet instead of the account message.
- [ ] **Step 8: Run the tests.** `go test ./cmd/monoagentcli/ -run 'TestStaleClaims|TestOpenListMatchesTheGate|TestAgentsDocCoversTheAccount|TestContributingExplainsTheDevelopmentBuild' -count=1`. Expected: `ok`.
- [ ] **Step 9: Commit.**
  - `git add cmd/monoagentcli/docs_agents_test.go AGENTS.md CONTRIBUTING.md`
  - `git commit -m "docs(agents): the account, what stays open, headless and Docker sign-in" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 4: Positioning text, locale strings and the Docker files

**Files:**
- Create: `cmd/monoagentcli/docs_positioning_test.go`
- Modify: `docs/COMPARISON.md`, `docs/USAGE_POLICY.md`, `docs/plans/workflow-marketplace-curation-policy.md`, `CLAUDE.md`, `.agents/shared_instructions.md`, `.github/ISSUE_TEMPLATE/feature_request.yml`, `scripts/install-linux-desktop.sh`, `wails-app/wails.json`, `internal/i18n/locales/en.json` and `es.json`, `Dockerfile`, `docker-compose.yml`
- Test: `cmd/monoagentcli/docs_positioning_test.go`; `internal/i18n/i18n_test.go` (unchanged, must stay green)

**Interfaces:** Consumes Task 1's helpers. Produces nothing. The frontend locale files are not touched (no claim in them); the desktop's account strings are B4's.

- [ ] **Step 1: Write the failing test.** `cmd/monoagentcli/docs_positioning_test.go`:

```go
package main

import "testing"

func TestPositioningTextsAreTrue(t *testing.T) {
	for _, rel := range []string{
		"docs/COMPARISON.md", "docs/USAGE_POLICY.md", "docs/plans/workflow-marketplace-curation-policy.md",
		"CLAUDE.md", ".agents/shared_instructions.md", ".github/ISSUE_TEMPLATE/feature_request.yml",
		"scripts/install-linux-desktop.sh", "wails-app/wails.json",
		"internal/i18n/locales/en.json", "internal/i18n/locales/es.json",
	} {
		assertNoStaleClaims(t, rel)
	}
	assertSays(t, "internal/i18n/locales/en.json", "account login", "ref account")
	assertSays(t, "internal/i18n/locales/es.json", "account login", "ref account")
	assertSays(t, "docs/COMPARISON.md", "SECURITY.md#network-use-telemetry-and-crash-reporting", "monoes.me sign-in", "once a day")
	assertSays(t, "CLAUDE.md", "-tags devaccount")
}

func TestDockerFilesSetUpTheSignIn(t *testing.T) {
	assertSays(t, "docker-compose.yml",
		"MONOAGENT_ALLOW_FILE_KEYRING", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "account login --email", "account status")
	assertSays(t, "Dockerfile", "monoes.me sign-in")
}
```

- [ ] **Step 2: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run 'TestPositioningTextsAreTrue|TestDockerFilesSetUpTheSignIn' -count=1`. Expected: `docs/COMPARISON.md still says "local-first"` (and `"no telemetry"`, `"fully offline"`), the same for the other files, and `docker-compose.yml does not say "MONOAGENT_ALLOW_FILE_KEYRING"`.
- [ ] **Step 3: Edit `docs/COMPARISON.md`.**

- Replace `docs/COMPARISON.md` lines 9-10, which read:

```
shape: one static binary, fully local data, a CLI an AI agent can actually
drive, and human approval built into the engine.**
```

with:

```
shape: one static binary, your data on your own machine, a CLI an AI agent can
actually drive, and human approval built into the engine.**
```

- `docs/COMPARISON.md` line 18: replace ``| **Local-first data** | SQLite + files under `~/.monoagent/`, works fully offline, no analytics/telemetry (opt-in crash reporting aside — see [SECURITY.md](../SECURITY.md)) |`` with ``| **Where your data lives** | SQLite + files under `~/.monoagent/`; needs a monoes.me sign-in and a connection to monoes.me at least once every 24 hours; no analytics or usage counters (what it contacts is listed in [SECURITY.md](../SECURITY.md#network-use-telemetry-and-crash-reporting)) |``.
- In `docs/COMPARISON.md`, after line 43 (it ends `- **Rely on a rich JS/TS ecosystem** for custom nodes.`), insert:

```
- **Need an air-gapped or fully disconnected install.** Mono Agent needs a
  monoes.me sign-in and a connection to monoes.me at least once every 24 hours.
```

- Replace `docs/COMPARISON.md` lines 49-57, which read:

```
- **A single binary on a laptop or any random box.** No Node, no Docker, no
  Postgres, no Redis — copy one static file, run it. That machine in the
  closet with nothing installed? It works there. (The AI/agent features are
  the exception: they hand off to the monomind engine, which needs Node.js.)
- **Fully local data, no telemetry by default.** Everything in SQLite and
  files under `~/.monoagent/`. Nothing phones home on its own; the only
  network traffic is the API calls your own workflows make, commands you
  explicitly invoke that talk to an external service, and opt-in crash
  reporting (see [SECURITY.md](../SECURITY.md)).
```

with:

```
- **A single binary on a laptop or any random box.** No Node, no Docker, no
  Postgres, no Redis — copy one static file, sign in to monoes.me, run it.
  That machine in the closet with nothing installed? It works there, as long
  as it can reach monoes.me once a day. (The AI/agent features are the
  exception: they hand off to the monomind engine, which needs Node.js.)
- **Your data on your own machine, no analytics.** Everything in SQLite and
  files under `~/.monoagent/`. The one thing it needs from a hosted service is
  the monoes.me sign-in: it contacts monoes.me to sign you in and keep the
  sign-in fresh, and GitHub to check for updates. Beyond that the network
  traffic is the API calls your own workflows make, commands you explicitly
  invoke that talk to an external service, and opt-in crash reporting (see
  [SECURITY.md](../SECURITY.md#network-use-telemetry-and-crash-reporting)).
```

- [ ] **Step 4: Edit the other positioning texts.**

- Replace `docs/USAGE_POLICY.md` lines 11-12, which read:

```
It's a local-first workflow engine: you build automations that run on your
machine, against services and accounts that are yours, at a pace you control.
```

with:

```
It's a workflow engine that runs on your machine: you build automations
against services and accounts that are yours, at a pace you control.
```
- Replace `docs/plans/workflow-marketplace-curation-policy.md` lines 135-136, which read:

```
  only, consistent with the local-first, no-phone-home design documented
  in SECURITY.md.
```

with:

```
  only: an imported workflow is a local copy that never calls back
  (SECURITY.md lists what the app contacts).
```
- Replace `CLAUDE.md` lines 5-6, which read:

```
mono-agent (`github.com/monoes/mono-agent`) — local-first workflow
automation engine (n8n alternative) in a single Go binary: `monoagentcli`.
```

with:

```
mono-agent (`github.com/monoes/mono-agent`) — workflow automation engine
that runs on your machine (n8n alternative) in a single Go binary: `monoagentcli`.
```

- In `CLAUDE.md`, after line 26 (it ends ``- CLI state lives in `~/.monoagent/` (global, not per-repo).``), insert:

```
- Gated commands need a monoes.me sign-in. For development build the real binary
  with `-tags devaccount` and run it with `MONOAGENT_DEV_ENFORCE_FROM=2999-01-01T00:00:00Z`
  so that nothing locks (see CONTRIBUTING.md); never ship that binary.
```
- `.agents/shared_instructions.md` line 7: replace `local-first workflow automation engine (n8n alternative)` with `workflow automation engine that runs on your machine (n8n alternative)`.
- `.github/ISSUE_TEMPLATE/feature_request.yml` line 9: replace `fits the project's shape (local-first, single binary, CLI-drivable)` with `fits the project's shape (runs on your machine, single binary, CLI-drivable)`.
- `scripts/install-linux-desktop.sh` line 81: replace `Comment=Local-first workflow automation dashboard` with `Comment=Workflow automation dashboard`.
- `wails-app/wails.json` line 18: replace `"comments": "Local-first workflow automation dashboard"` with `"comments": "Workflow automation dashboard"`.

- [ ] **Step 5: Edit the CLI help strings** (`\n` below is a backslash and an `n`, as in the JSON).

- `internal/i18n/locales/en.json` line 2: replace `"root.short": "Local-first workflow automation agent (n8n alternative)",` with `"root.short": "Workflow automation agent that runs on your machine (n8n alternative)",`.
- `internal/i18n/locales/en.json` line 3: replace `"root.long": "Mono Agent — local-first workflow automation (n8n alternative) in a single Go binary.` with `"root.long": "Mono Agent — workflow automation that runs on your machine (n8n alternative) in a single Go binary.`.
- `internal/i18n/locales/en.json` line 3: replace `from CLI, GUI, or MCP.\n\nSTART HERE — what can this already do?` with `from CLI, GUI, or MCP.\n\nA monoes.me account is required: run 'monoagentcli account login' (see 'monoagentcli ref account').\n\nSTART HERE — what can this already do?`.
- `internal/i18n/locales/es.json` line 2: replace `"root.short": "Agente de automatización de flujos de trabajo local-first (alternativa a n8n)",` with `"root.short": "Agente de automatización de flujos de trabajo que se ejecuta en tu equipo (alternativa a n8n)",`.
- `internal/i18n/locales/es.json` line 3: replace `"root.long": "Mono Agent — automatización de flujos de trabajo local-first (alternativa a n8n) en un único binario Go.` with `"root.long": "Mono Agent — automatización de flujos de trabajo que se ejecuta en tu equipo (alternativa a n8n) en un único binario Go.`.
- `internal/i18n/locales/es.json` line 3: replace `desde la CLI, la GUI o MCP.\n\nEMPIEZA AQUÍ — ¿qué puede hacer esto ya?` with `desde la CLI, la GUI o MCP.\n\nSe necesita una cuenta de monoes.me: ejecuta 'monoagentcli account login' (consulta 'monoagentcli ref account').\n\nEMPIEZA AQUÍ — ¿qué puede hacer esto ya?`.

- [ ] **Step 6: Edit the Docker files.** First confirm what the commands rely on: `grep -nE 'ENV HOME|VOLUME|container_name' Dockerfile docker-compose.yml`. Expected: `ENV HOME=/data`, `VOLUME ["/data"]` and `container_name: monoagent`, so `~/.monoagent/account/` (the sign-in) and the passphrase file are on the volume and `docker exec monoagent` finds the container. If any differs, fix the commands below before writing them.

- In `Dockerfile`, before line 59 (it starts `ENTRYPOINT ["/usr/local/bin/monoagentcli", "daemon"]`), insert:

```
# The daemon starts without a monoes.me sign-in but, from the enforcement date,
# refuses work until one is kept in /data (README.md, "Docker", and
# docker-compose.yml say how). The HEALTHCHECK above runs `version`, which is
# always open, so a healthy container is not a signed-in one:
# `docker exec <container> monoagentcli account status --offline`.
```
- In `docker-compose.yml`, after line 47 (it ends `MONOAGENT_WEBHOOK_ADDR: "0.0.0.0:9321"`), insert:

```
      # monoes.me sign-in (see "Signing in" under Usage): a container has no OS
      # keyring, so the sign-in's refresh token is sealed under the file keyring,
      # unlocked with a passphrase file kept on the volume.
      MONOAGENT_ALLOW_FILE_KEYRING: "1"
      MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE: "/data/keyring-pass"
```

- In `docker-compose.yml`, before line 62 (it starts `# Usage:`), insert:

```
    # Signing in (once, before the first `up`). The daemon starts without a monoes.me
    # sign-in and stays up, logging the command to run, but from the enforcement date
    # it refuses work until one exists; without the passphrase file it cannot read
    # the sign-in back.
    #   printf '%s\n' "$PASSPHRASE" | docker compose run --rm -T --entrypoint sh monoagent -c 'umask 077; cat > /data/keyring-pass'
    #   docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --send
    #   docker compose run --rm --entrypoint monoagentcli monoagent account login --email you@example.com --code <code>
    #   docker exec monoagent monoagentcli account status --offline   # healthy is not signed in
    #
```

- [ ] **Step 7: Run the tests and the format checks.** `go test ./cmd/monoagentcli/ -run 'TestPositioningTextsAreTrue|TestDockerFilesSetUpTheSignIn' -count=1` and `go test ./internal/i18n/ -count=1` (expected: `ok` for both), `jq . internal/i18n/locales/en.json internal/i18n/locales/es.json wails-app/wails.json > /dev/null` (no output) and, if Docker is installed, `docker compose config -q` (no output).
- [ ] **Step 8: Commit.**
  - `git add cmd/monoagentcli/docs_positioning_test.go docs CLAUDE.md .agents .github scripts/install-linux-desktop.sh wails-app/wails.json internal/i18n/locales Dockerfile docker-compose.yml`
  - `git commit -m "docs(account): retire the local-first label and set up Docker for the sign-in" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 5: `ref account` and `ref commands`

**Files:**
- Create: `cmd/monoagentcli/ref_account.go`, `cmd/monoagentcli/ref_account_test.go`
- Modify: `cmd/monoagentcli/ref.go` (1976, 1992, 1998, 2015–2016), `cmd/monoagentcli/account_gate_test.go` (B2's `pinnedOpen`: one entry)
- Test: `cmd/monoagentcli/ref_account_test.go`; `TestRefDocsCoverEveryRegisteredNodeType` (unchanged, must stay green)

**Interfaces:**
- Consumes: `cliDocs []cmdDoc` with `cmdDoc{Name, Short, Usage, Flags string; Examples []string}` (ref.go:1612; `ref_library.go` appends to it the same way), `newRefCmd()` (ref.go:1959), `captureStdout(t, fn) string` (people_status_test.go:49), `newRootCmd()`, B2's `pinnedOpen` and `TestEveryCommandIsClassified` (`account_gate_test.go`), B1b's `account` tree (flags `email`, `send`, `code`, `no-browser`, `timeout` on `account login`; `offline` on `account status`), Tasks 1 and 3's helpers.
- Produces: `refAccountCmd() *cobra.Command` registered as `ref account`, and three `cliDocs` entries (`account login`, `account status`, `account logout`).

- [ ] **Step 1: Read the real flags.** `go run ./cmd/monoagentcli account login --help` and `go run ./cmd/monoagentcli account status --help` (`--help` is open; `go run` writes no binary). The texts below document `--no-browser`, `--timeout`, `--email`, `--send` and `--code` for `login` and `--offline` for `status`, as `library login` and `library status` have them today; if B1b spelled any differently, change the text and the flag list of `TestRefAccountFlagsExistOnTheCommands` to what `--help` prints.
- [ ] **Step 2: Check that no earlier phase wrote `ref` text for the account.** `git grep -n 'account' -- cmd/monoagentcli/ref.go cmd/monoagentcli/ref_org.go cmd/monoagentcli/ref_library.go`. Expected: no line about the monoes.me account. If an earlier phase added entries (the index lets a phase add `ref` text where a test requires it), delete them here: this task owns all of it, and `TestRefListsTheAccountTopicAndCommands` fails on a duplicate.
- [ ] **Step 3: Write the failing test.** `cmd/monoagentcli/ref_account_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestRefAccountDocumentsTheGate(t *testing.T) {
	out := captureStdout(t, func() { c := refAccountCmd(); c.Run(c, nil) })
	for _, w := range append(spelled(""), refusalLine(), "login_required", "exit 4", graceText(), "account login --email", "--send", "--code",
		"MONOAGENT_ALLOW_FILE_KEYRING", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "-tags devaccount", "no credential comes from an environment variable",
		"never copy or restore", "signs the account out everywhere", "MONOAGENT_DEV_ENFORCE_FROM", "monoes..account") {
		if !strings.Contains(flat(out), flat(w)) {
			t.Errorf("`ref account` does not say %q", w)
		}
	}
	assertListText(t, "`ref account`", out, "Open (never need a sign-in):", openCommands)
	assertListText(t, "`ref account`", out, "Serving (start even when locked):", servingCommands)
}

func TestRefListsTheAccountTopicAndCommands(t *testing.T) {
	ref := newRefCmd()
	found := false
	for _, c := range ref.Commands() {
		found = found || c.Name() == "account"
	}
	listing := captureStdout(t, func() { _ = ref.RunE(ref, nil) })
	if !found || !strings.Contains(listing, "account") || !strings.Contains(ref.Long, "account") {
		t.Error("`ref` must have an `account` topic, listed by `ref` and named in its help")
	}
	seen := map[string]int{}
	for _, d := range cliDocs {
		seen[d.Name]++
	}
	for _, name := range []string{"account login", "account status", "account logout"} {
		if seen[name] != 1 {
			t.Errorf("`ref commands` must document %q exactly once, has %d entries", name, seen[name])
		}
	}
}

// Every flag `ref commands` documents for the account commands exists on the real command.
func TestRefAccountFlagsExistOnTheCommands(t *testing.T) {
	root := newRootCmd()
	for path, flags := range map[string][]string{"account login": {"email", "send", "code", "no-browser", "timeout"}, "account status": {"offline"}} {
		c, _, err := root.Find(strings.Fields(path))
		if err != nil || c == root {
			t.Fatalf("no command %q", path)
		}
		for _, d := range cliDocs {
			for _, f := range flags {
				if d.Name == path && !strings.Contains(d.Flags+d.Usage, "--"+f) {
					t.Errorf("`ref commands` does not document --%s of %q", f, path)
				}
			}
		}
		for _, f := range flags {
			if c.Flag(f) == nil {
				t.Errorf("`ref commands` documents --%s for %q, which has no such flag", f, path)
			}
		}
	}
}
```

- [ ] **Step 4: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run 'TestRefAccount|TestRefLists' -count=1`. Expected: a build failure, `undefined: refAccountCmd`.
- [ ] **Step 5: Create the topic.** `cmd/monoagentcli/ref_account.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The `account` entries of `ref commands` (kept apart from ref.go, which is
// long enough).
func init() {
	cliDocs = append(cliDocs,
		cmdDoc{
			Name:  "account login",
			Short: "Sign in to monoes.me (browser with PKCE, or a code by email); every gated command needs it",
			Usage: "monoagentcli account login [--no-browser] [--timeout 5m] | --email <addr> [--send | --code <code>]",
			Flags: `  --no-browser        Print the sign-in URL instead of opening the browser
  --timeout duration  How long to wait for the browser (default 5m)
  --email string      Sign in with a code sent to this address (a machine with no browser)
  --send              With --email: only send the code
  --code string       With --email: the code from the email`,
			Examples: []string{
				"monoagentcli account login",
				"monoagentcli account login --email you@example.com --send",
				"monoagentcli account login --email you@example.com --code 123456",
			},
		},
		cmdDoc{
			Name:     "account status",
			Short:    "Show the monoes.me sign-in: state, reason, who, and when it stops working (exit 0 for ok and grace, 4 for locked)",
			Usage:    "monoagentcli account status [--offline] [--json]",
			Flags:    `  --offline           Do not refresh first; report what is stored`,
			Examples: []string{"monoagentcli account status", "monoagentcli --json account status"},
		},
		cmdDoc{
			Name:     "account logout",
			Short:    "Revoke the refresh token at monoes.me (best effort) and delete the local sign-in",
			Usage:    "monoagentcli account logout",
			Examples: []string{"monoagentcli account logout"},
		},
	)
}

func refAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "account",
		Short: "The monoes.me account: sign-in, states, what stays open, headless and Docker",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(`
╔══════════════════════════════════════════════════════════════╗
║        monoagentcli — the monoes.me account (account …)       ║
╚══════════════════════════════════════════════════════════════╝

Mono Agent needs a monoes.me account signed in on the machine: one sign-in per OS
user, shared by every profile, in ~/.monoagent/account/ (whatever --db-path says).
library login, logout and status are aliases of the account commands.

COMMANDS
  monoagentcli account login                    browser sign-in (OAuth 2.1 + PKCE)
  monoagentcli account login --email you@x.com  headless: mails a code, then asks for
                                                it (--send mails it, --code <code> finishes)
  monoagentcli account status [--offline] [--json]
                                                refreshes first if due, unless --offline
  monoagentcli account logout                   revokes the refresh token, deletes the sign-in
  account status --json: {"v":1,"state","reason","user":{"id","email","username"},
    "plan","issued_at","valid_until","grace_until","enforce_from","enforced"}.
  Exit 0 for ok and grace, 4 for locked. valid_until is the access token's expiry
  (about an hour); grace_until is its issue time plus 24 hours.

ONE SIGN-IN PER MACHINE
  Sign in once per machine: never copy or restore ~/.monoagent/account/ between machines
  or from an old backup, and never bake a sign-in into a Docker image. monoes.me rotates
  the refresh token at every refresh and treats one that was already used as stolen: it
  ends every Mono Agent sign-in of that account, on every machine. A stale copy, once
  presented, signs the account out everywhere, and each machine then has to sign in
  again. Signing out of the monoes.me website does not sign out an install.

STATES AND REASONS
  ok       a verified sign-in that has not expired
  grace    verified and expired, but inside 24 hours of its issue time: work continues.
           reason says why it was not refreshed: unreachable (no network, a timeout),
           server_error (monoes.me answered with an error) or keyring_unavailable (the
           key store holding the refresh token could not be opened)
  locked   work is refused. reason: not_logged_in; expired (24 hours without a refresh);
           refused (monoes.me answered invalid_grant: the account is blocked, the sign-in
           was revoked, or an already-used refresh token was presented, as from a copied
           session; work in flight is cancelled); clock_rollback (the clock went back; a fresh sign-in resets it);
           clock_skew (this clock is over 5 minutes behind monoes.me's); key_unknown (run
           'monoagentcli update'); invalid (the stored sign-in does not verify)
  Only an invalid_grant answer to a refresh is a refusal. Everything else (no network,
  a timeout, any 4xx or 5xx, any other OAuth error) counts as unreachable, so the
  24-hour grace applies. A blocked account is locked at the next refresh, within an hour.

PHASES
  dormant   a build before the enforcement release: nothing locks, warns or contacts
            monoes.me by itself
  warn      before the enforcement date (enforce_from in account status): a gated command
            runs and prints on stderr "A monoes.me login will be required from <date>:
            monoagentcli account login"
  enforced  from that date: a gated command with no valid sign-in refuses

WHAT STAYS OPEN
  Open (never need a sign-in): version, help, completion, ref, update, doctor (with
  doctor fix), setup, account, library login, library logout, library status. Every
  other command is gated, local-only ones (people, secret, image, profile, config) too.
  Serving (start even when locked): daemon, httpapi, mcp, extension serve (also bridge
  serve). They stay up and refuse their work, so a service manager or a container never
  respawns them in a loop. daemon install, daemon restart, daemon uninstall and org
  serve are gated.

A REFUSAL
  A gated command with no valid sign-in does nothing else (no first-run writes, no
  database open) and exits 4. The first line on stderr is "Log in to monoes.me first:
  monoagentcli account login". With --json anywhere in the arguments, stdout also gets
    {"error":"…","code":"auth_or_connection","login_required":true,
     "account":{"state":"locked","reason":"…"}}
  In grace the command runs and prints one stderr line: "monoes.me is unreachable; this
  login works offline until <time>". Branch on exit 4 and login_required, not on the
  text; an agent asks the user to run 'monoagentcli account login' and never looks for,
  pastes or sets a token. A running process checks again, and a serving command started
  while locked answers the same way: the HTTP API and /v1 answer 401 login_required
  (GET /health stays open and reports the account state), the webhook server 503 (no
  execution), the extension bridge account_locked, MCP an error result. A refused
  workflow run is recorded FAILED with an error starting login_required:; paused and
  queued runs wait. A locked daemon stays up, logs the command to run, starts nothing
  new, stops its org services and resumes by itself when a sign-in appears.

HEADLESS AND DOCKER
  Sign in once with 'account login --email you@x.com'; the sign-in lives in
  ~/.monoagent/account/ of the OS user that runs the daemon (another user's daemon does
  not see it; in Docker the image's HOME=/data puts it on the volume). No credential
  comes from an environment variable and there are no unattended machine tokens. The
  refresh token is sealed under the vault's key store: the OS keychain or, where there
  is none (a container, a server), the file keyring, enabled with
  MONOAGENT_ALLOW_FILE_KEYRING=1. A daemon has no terminal, so it reads the
  keyring's passphrase from the file named by MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE (or
  ~/.monoagent/keyring-passphrase). Without one it cannot read the refresh token back:
  account status shows grace with reason keyring_unavailable, and the daemon stops doing
  work 24 hours after its last refresh. Make that file BEFORE the first sign-in:
  'secret keyring set-passphrase' is itself a gated command. A daemon started with no
  sign-in stays up and waits: sign in, and it resumes within seconds. SECURITY.md
  ("File-based keyring fallback") lists the passphrase sources; AGENTS.md ("Headless
  and Docker") has the Docker commands. Keep the sign-in in the volume only. In a file
  keyring, 'secret keyring status' lists the account's key as monoes..account once an
  explicit sign-in has created it.

DEVELOPERS
  A build from source runs the real gate. For development and CI build the real binary
  with  go build -tags devaccount -o <path> ./cmd/monoagentcli : it also trusts a
  development signing key and lets the gate honor MONOES_BASE_URL. MONOAGENT_DEV_ENFORCE_FROM
  (an RFC 3339 time) is read only by such a build: a test uses it to force the warn
  period (a future date such as 2999-01-01: nothing locks) or enforcement (a past
  date), and it never relaxes anything. Releases never carry the tag.
`)
		},
	}
}
```

- [ ] **Step 6: Register it in `ref.go`.**

- In `cmd/monoagentcli/ref.go` line 1976, replace ``  org                   Orgs, automations, grants, automation roles, autonomy, holding orgs`,`` with:

```
  org                   Orgs, automations, grants, automation roles, autonomy, holding orgs
  account               The monoes.me account: sign-in, states, what stays open, headless and Docker`,
```

- In `cmd/monoagentcli/ref.go`, after line 1992 (it ends `…omations, grants, automation roles, autonomy, holding orgs")`), insert:

```
			fmt.Fprintln(w, "  account\tThe monoes.me account: sign-in, states, what stays open, headless and Docker")
```

- In `cmd/monoagentcli/ref.go`, after line 1998 (it ends `fmt.Println("Example:  monoagentcli ref api")`), insert:

```
			fmt.Println("Example:  monoagentcli ref account")
```

- Replace `cmd/monoagentcli/ref.go` lines 2015-2016, which read:

```
		refOrgCmd(),
	)
```

with:

```
		refOrgCmd(),
		refAccountCmd(),
	)
```

- [ ] **Step 7: Pin the new page in B2's inventory.** B2's `TestEveryCommandIsClassified` compares the commands the gate calls open with `pinnedOpen`, and every `ref` page inherits `ref`'s class, so a new page fails it until a person adds it. `go test ./cmd/monoagentcli/ -run '^TestEveryCommandIsClassified$' -count=1`. Expected: FAIL with `the open commands differ from the pinned list (D6)`, the `got` list holding `ref account` and the `want` list not. In `cmd/monoagentcli/account_gate_test.go`, in `pinnedOpen`, change `"ref", "ref api",` to `"ref", "ref account", "ref api",` and run it again: `ok`.

- [ ] **Step 8: Run the tests.** `go test ./cmd/monoagentcli/ -run '^TestRef(Account|Lists|Docs)' -count=1` and `go test ./cmd/monoagentcli/ -run '^(TestEveryCommandIsClassified|TestOpenListMatchesTheGate)$' -count=1` (expected: `ok` for both; the first run's tests are the three new ones and the node-coverage tests `TestRefDocsCoverEveryRegisteredNodeType` and `TestRefDocsFlagDeprecatedAINodes`; its pattern is anchored so that a refresher test named `TestRefresh…` is not picked up) and `go run ./cmd/monoagentcli ref account` (expected: the page; `ref` is open, and `go run` writes no binary into the repository).
- [ ] **Step 9: Commit.**
  - `git add cmd/monoagentcli/ref_account.go cmd/monoagentcli/ref_account_test.go cmd/monoagentcli/ref.go cmd/monoagentcli/account_gate_test.go`
  - `git commit -m "docs(ref): the account topic and its commands" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`

### Task 6: The CHANGELOG entry for R, and the landing check

**Files:**
- Create: `cmd/monoagentcli/docs_changelog_test.go`
- Modify: `CHANGELOG.md` (before line 8)
- Test: `cmd/monoagentcli/docs_changelog_test.go`

**Interfaces:** Consumes `account.EnforceDate() time.Time` (index §3.2; B5a sets it in `internal/account/rollout.go`) and Task 1's helpers. Produces nothing.

- [ ] **Step 1: Write the failing test.** `cmd/monoagentcli/docs_changelog_test.go`:

```go
package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// release.yml takes a release's notes from its own `## [x.y.z]` section, so R needs
// one that tells the account story. Later releases add sections above R's and may
// move the date, so this looks for such a section above the one that was newest
// before R ([0.100.1]) rather than at the top, and for the date anywhere above it.
func TestChangelogNamesTheEnforcementDate(t *testing.T) {
	date := account.EnforceDate()
	if date.IsZero() {
		t.Fatal("account.EnforceDate() is the zero time: this documentation lands with release R, on top of B5a, which sets the date (plan B5b, Landing)")
	}
	text := docText(t, "CHANGELOG.md")
	if i := strings.Index(text, "\n## [0.100.1]"); i >= 0 {
		text = text[:i]
	}
	if iso := date.UTC().Format("2006-01-02"); !strings.Contains(flat(text), iso) {
		t.Errorf("no release section above [0.100.1] names the enforcement date %s", iso)
	}
	heading := regexp.MustCompile(`^## \[\d+\.\d+\.\d+\] - \d{4}-\d{2}-\d{2}$`)
	wants := []string{"a monoes.me account is required", "account login", "login_required", graceText(), "daemon restart", "-tags devaccount", "SECURITY.md"}
	var closest []string
	for _, section := range strings.Split("\n"+text, "\n## [")[1:] {
		section = "## [" + section
		if !heading.MatchString(strings.SplitN(section, "\n", 2)[0]) {
			continue // [Unreleased], or a heading release.yml would not read
		}
		var missing []string
		for _, w := range wants {
			if !strings.Contains(flat(section), flat(w)) {
				missing = append(missing, w)
			}
		}
		if len(missing) == 0 {
			return
		}
		if closest == nil || len(missing) < len(closest) {
			closest = missing
		}
	}
	if closest == nil {
		t.Fatal("CHANGELOG.md has no '## [x.y.z] - date' section above [0.100.1]: release.yml uses that section as the release notes")
	}
	t.Errorf("no section above [0.100.1] tells the account story (release.yml uses it as the release notes); the closest lacks %q", closest)
}
```

- [ ] **Step 2: Run it and watch it fail.** `go test ./cmd/monoagentcli/ -run TestChangelogNamesTheEnforcementDate -count=1`. Expected: `no release section above [0.100.1] names the enforcement date` and `CHANGELOG.md has no '## [x.y.z] - date' section above [0.100.1]`. If it says `account.EnforceDate() is the zero time`, this branch is not on top of B5a: stop and rebase (see Landing).
- [ ] **Step 3: Read the three values.** ENFORCE_DATE: `grep -n '^var enforceFrom' internal/account/rollout.go` prints the one line `var enforceFrom = time.Date(...)` (a plain `grep -n enforceFrom` also prints the accessor's `return`); write that date as `YYYY-MM-DD` (UTC). Do this at cut time, after B5a's Task 1 holds the owner's date; if the owner moves the date later (B5a's merge-day check), change the CHANGELOG with it: the test fails until they agree. TODAY: `date +%Y-%m-%d`. VERSION: what `release.yml:89-101` will compute for this push; write `<scratchpad>/next-version.sh` with the Write tool and run `bash <scratchpad>/next-version.sh`:

```bash
git fetch origin --tags
LAST=$(git tag --sort=-v:refname | head -1)
MAJOR=$(echo "$LAST" | sed 's/v//' | cut -d. -f1)
MINOR=$(echo "$LAST" | sed 's/v//' | cut -d. -f2)
PATCH=$(echo "$LAST" | sed 's/v//' | cut -d. -f3)
if git log --oneline "${LAST}..HEAD" | grep -qE '^[0-9a-f]+ feat(\(.+\))?:'; then MINOR=$((MINOR + 1)); PATCH=0; else PATCH=$((PATCH + 1)); fi
echo "${MAJOR}.${MINOR}.${PATCH}"
```

  Expected: the minor version after the newest tag, with patch 0, while B5a's `feat(...)` commits are on the branch (several releases will have shipped from B1a to B5c by then, so the number is not `0.107.0`); use whatever it prints.
- [ ] **Step 4: Insert the release's section** above the file's other `## [` headings, writing the three values where `<VERSION>`, `<TODAY>` and `<ENFORCE_DATE>` stand. Leave the misplaced `## [Unreleased]` block where it is.

- In `CHANGELOG.md`, before line 8 (it starts `## [0.100.1] - 2026-10-01`), insert:

```
## [<VERSION>] - <TODAY>

### Added
- **A monoes.me account is required.** `monoagentcli account login` (browser) or `account login --email you@example.com` (a code by email, for a machine with no browser), `account status [--json]` and `account logout`; `library login|logout|status` are aliases of the same machine session, kept in `~/.monoagent/account/`. From <ENFORCE_DATE> a gated command with no valid sign-in exits 4 with `login_required` and does nothing else; until then it runs and prints a warning on stderr. `version`, `help`, `completion`, `ref`, `update`, `doctor`, `setup`, the `account` commands and `library login|logout|status` always work. The serving commands (`daemon`, `httpapi`, `mcp`, `extension serve`) start even when locked and refuse their work.
  - **Where it is checked:** before any command, inside every workflow execution, agent turn and browser action, and at every door of a running process: the HTTP API and `/v1` (401 `login_required`), the webhook server (503), the extension bridge (`account_locked`), MCP (a tool error) and the daemon, which stays up, starts nothing new and resumes when you sign in. The desktop app shows a sign-in screen, and a banner in the grace period and before the date; the Chrome extension's side panel says "Sign in to MonoAgent", and what to run, while the bridge refuses.
  - **Older library logins:** the first run exchanges a monoes.me library login left by an older release for the machine sign-in when monoes.me allows it; otherwise `account status` says to sign in.
  - **Offline:** a sign-in keeps working for 24 hours after the last contact with monoes.me. A blocked account is locked at the next refresh, within about an hour; only `invalid_grant` answered to a refresh counts as a refusal, every other failure keeps the grace.
  - **What is sent:** to monoes.me, the sign-in and, while a long-running process runs, a refresh about every half hour (monoes.me sees the account, the time and the IP address; with `--email`, the address you type); once, on the first run, an older library login's refresh token, to exchange it; to GitHub, update checks. No device id, no usage data. SECURITY.md lists everything.
- **`doctor` checks the sign-in and the daemon's version:** a `core.monoes_account` row (ok; warn in the grace window or before the date; fail when locked; fix `account login`, or `update` for `key_unknown`) and a warning from the `services.daemon` row when the running daemon's version differs from the binary's (manual fix `services.daemon.restart`: `daemon restart`). `ref account` documents all of it offline.

### Changed
- **`update` and `update --app` restart a registered daemon** through the service manager so that it runs the new code, also when the binary was already current. It restarts nothing while an execution is in flight, says so, and leaves the restart to `daemon restart`: an update never interrupts a run. `update --app --json` reports the outcome in an optional `daemon` object. The first update to this release is made by the previous binary, which cannot restart anything, so `doctor` flags the old daemon until the next `update`, a restart or a reboot.
- **The documentation says what is true.** Mono Agent is no longer described as local-first, usable offline without limit or free of network calls: your data stays on your machine, a monoes.me sign-in is required, and README, AGENTS.md, SECURITY.md, SUPPORT.md and the comparison page list what it contacts. Headless and Docker: sign in once with `account login --email`; a container needs `MONOAGENT_ALLOW_FILE_KEYRING=1` and a passphrase file (README, "Docker"). There are no environment-variable credentials.

### Notes
- Sign in once per machine. Never copy or restore `~/.monoagent/account/` between machines or from an old backup, and never bake a sign-in into a Docker image: monoes.me treats a refresh token that was already used as stolen and ends every sign-in of the account, on every machine (SECURITY.md).
- Versions before this one are not gated; only the monoes.me library, which monoes.me already gates, stops for them. A build from source is gated like a release: developers build with `-tags devaccount` (CONTRIBUTING.md), and releases never carry the tag.
```

- [ ] **Step 5: Run the test.** `go test ./cmd/monoagentcli/ -run TestChangelogNamesTheEnforcementDate -count=1`. Expected: `ok`.
- [ ] **Step 6: Commit.**
  - `git add cmd/monoagentcli/docs_changelog_test.go CHANGELOG.md`
  - `git commit -m "docs(changelog): release notes for the monoes.me account" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"`
- [ ] **Step 7: The landing check.** `git fetch origin`, then `git log --oneline origin/master..HEAD` (expected: B5a's commits, then the six `docs(...)` commits of Tasks 1 to 6: B1a to B4b and B5c are already on master by then, because each of them merged, and released, before R). Rebase onto `origin/master` if it moved and rerun `next-version.sh` immediately before the push: any release in between shifts the number, so edit the heading if it changed. Then `go build ./...`, `go vet ./...`, `gofmt -l .` (no output), `go build -tags nosocial -o /dev/null ./cmd/monoagentcli`, `go test ./internal/i18n/ -count=1` and `go test ./cmd/monoagentcli/ -run 'TestStaleClaims|TestFrontDoor|TestSecurityDoc|TestAgentsDoc|TestOpenList|TestContributing|TestPositioning|TestDockerFiles|TestRefAccount|TestRefLists|TestRefDocs|TestChangelog|TestEveryCommandIsClassified' -count=1` (expected: `ok`). R is not cut until B5a's readiness checklist is done (plan A deployed, the production signing key pinned in `internal/account/keys_default.go`, the server's reuse window live; index §1): look there first.
- [ ] **Step 8: The owner reads the claims.** Hand the owner the review sheet at the top of this plan and `git diff origin/master -- README.md SECURITY.md AGENTS.md SUPPORT.md CONTRIBUTING.md docs/COMPARISON.md CHANGELOG.md internal/i18n/locales`. The owner reads every rewritten statement, says whether each is true of the build that will ship and whether the date in the CHANGELOG is theirs. A statement they change goes through the guard tests again (they pin phrases, not paragraphs, so rewording is free: rerun the tests of Tasks 1 to 6). Push only after that answer, and tell the lead: push B5a and Tasks 1 to 6 together, never B5a alone.

## Contract change requests

None. This plan first asked for `libraryfake.Server.TokenRequests()`; B1b's plan adds it (its Contract change request 2) and pins the field sets of a refresh and of a code exchange with it, which is what the privacy statement of Task 2 rests on.
