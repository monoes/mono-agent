# Mandatory monoes.me Account — B4b: the extension side panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Chrome extension's side panel says "Sign in to MonoAgent", and what to run, while the MonoAgent bridge refuses requests for want of a monoes.me sign-in, and says nothing before this build enforces one.

**Architecture:** A pure `MonoAccount` module reads what the bridge says about the account (a refused request, or the account object of `ping`) into one fact: the bridge is refusing. The service worker feeds it from every frame the bridge sends, asks `ping` every 5 seconds while the bridge refuses (the one request a locked bridge answers), and puts the fact in the status payload the side panel draws; `MonoPanelStatus.describe` turns it into an `account_locked` state. Nothing is held back: the bridge keeps accepting pushed captures and recordings while locked.

**Tech Stack:** the MV3 extension's plain scripts (`importScripts` in the service worker, `<script>` tags in the panel), tested with `node:test`.

**Depends on:** B3b only: the bridge's `account_locked` reply and the account object in `ping`. It needs neither `internal/account` (B1a, B1b), nor the CLI gate (B2), nor the desktop gate (B4, a separate plan).

**Spec:** `docs/mastermind/specs/2026-10-05-monoes-account-gate-design.md` (D7, the extension bridge door of §6.3, with amendments A3 and A4); plan index `docs/mastermind/plans/2026-10-05-monoes-account-gate-index.md` (§3.4 item 7). Where this plan differs from the index (§2, §3.6) or from spec §13, the index and spec §13 win.

**Proof.** Every code block below was run in a scratch export of `f4441a2a`: `node --check` on every extension script, and `node --test` on the 45 non-browser extension test files (661 tests, none failing). The red outputs quoted in the steps were captured by hiding the implementation. The bridge's frames were checked against the Go source that sends them (`internal/extension/request.go`, `Reply`) and against the B3b plan, not against a running bridge, which does not exist before B3b merges: Task 3 step 4 does that.

## Global Constraints

Copied from index §2, the lines that bind this plan; everything else in §2 binds implicitly.

- States are `ok`, `grace`, `locked` (§4.3). A refusal is only `invalid_grant` answered to a refresh-token grant (D27); every other failure is `unreachable` or `server_error` and keeps the grace.
- Dormant (D22): while `account.EnforceDate()` is the zero time nothing locks, nothing warns, and nothing is called implicitly (no adoption, no refresh, no background refresher). Only an explicit `account` or `library` command talks to monoes.me. The one visible trace of a dormant build is the additive `account` object in `GET /health` and the bridge `ping`.
- A gated command that is refused exits 4 with `login_required` (§6.1). The first line of its message is exactly `Log in to monoes.me first: monoagentcli account login`.
- Serving commands (spec §6.4) start even when locked, because launchd's `KeepAlive` and Docker's `restart: unless-stopped` would respawn a refused daemon in a loop and MCP hosts must see a clear error. The CLI gate's third class `serve` is `daemon`, `httpapi`, `mcp` (with `--grant`) and `extension serve` (also `bridge serve`); layers 2 and 3 do the refusing. `org serve` (a launcher) and `daemon install`, `restart` and `uninstall` stay gated.
- Never print, log or put in a test's output a token, a refresh token or a key. Test fixtures use throwaway keys generated in the test.
- Files stay under 500 lines; split by responsibility. Conventional commit subjects, `type(scope): subject`. Never commit secrets or `.env` files.
- Only B5b edits `README.md`, `AGENTS.md`, `SECURITY.md`, `SUPPORT.md`, `docs/COMPARISON.md`, `CONTRIBUTING.md`, `CHANGELOG.md` and the claim strings in `internal/i18n/locales`, so parallel phases do not conflict. The new desktop strings under `account.*` in `wails-app/frontend/src/locales/{en,es}.json` belong to B4. Other phases add `ref` text, and a minimal `AGENTS.md` line, only where a test requires it.

Other rules that bind this plan. It owns `chrome-extension/*` and nothing else: no Go file, no desktop file. `chrome-extension/background.js` is already over 500 lines, so the logic lives in the new `account_state.js` and the worker only wires it. CI runs `node --check` on every script (`.github/workflows/ci.yml:124`) and `node --test 'chrome-extension/**/*.test.mjs'` (`:141`); twelve of the tests in the six `*.browser.test.mjs` files fail on a pristine macOS tree (the service worker does not start), so the runs below name the `.test.mjs` files that are not browser tests. Line numbers in the steps refer to the files at `f4441a2a`: when a step edits one file in several places, work from the bottom up, or find each place by the quoted text.

## Review Focus

Failure modes that no task's main tests would otherwise exercise and that bite a person using the software, most likely first. Each is pinned by a named test.

1. **Before this build enforces, the panel must tell nobody to sign in.** A dormant build, and a build in the warn period, answer everything, and their `ping` still says `locked` for a machine with no sign-in (with `enforced: false`), so a `locked` state alone is not a refusal: only an `account_locked` reply, or a locked state that says `enforced: true`. Pinned in Task 1 (`a locked state with no word on enforcement proves nothing`, `locked before the enforcement date is not a refusal`).
2. **The real refusal has no `ok` key.** The bridge's `Reply.OK` is `omitempty` (`internal/extension/request.go:122`), so a refusal is `{"kind":"reply","id":…,"error":…,"code":"account_locked"}`; a reader that waits for `ok === false` never sees one and the panel stays silent forever. `ask.js` documents `ok: false`, which makes the mistake easy. Pinned in Task 1 (`a refusal has no ok key on the wire, and is read from its code alone`).
3. **The panel must clear without a reload when the person signs in, and forget a closed socket.** While refusing, the worker asks `ping` every 5 seconds and a signed-in `ping` clears the refusal; the account belongs to the socket and is dropped on close. Pinned in Task 1 for what the pure parts do (`a locked state with no word on enforcement proves nothing` ends with a signed-in `ping` clearing a refusal; `the tracker tells its listeners when the account changes` ends with `set(null)`); the worker's 5-second timer and its `set(null)` on close are wiring, looked at in Task 3 step 4.
4. **The refusal message must not borrow another connection state.** Waiting, connecting, unpaired and disconnected have their own instructions and are true whatever the account says. Pinned in Task 2 (`only a connected socket can be told it is refused`, `a refused bridge outranks nothing the arbiter decides`).
5. **A refusal must not say saving is broken.** The bridge keeps accepting pushed captures, recordings and bindings while locked, so the panel claims no queue. Pinned in Task 2 (`a connected bridge that refuses says to sign in, with the command` asserts `queues === false`).

## Decisions this plan adds

1. **A refusal is only an `account_locked` reply, or a `ping` that says locked and enforced.** A successful answer to anything but `ping` proves the bridge is not refusing; a locked `ping` that does not say whether it is enforced proves nothing (it is `null`, so it neither invents a refusal nor clears one). `refusing` itself wants `enforced === true`.
2. **The failed reply has no `ok` key** (B3b, the Go `Reply`), so the refusal is read from `code` with `!frame.ok`.
3. **The extension only tells; it holds nothing.** The B3b plan keeps accepting pushed captures, recordings and binding pushes while locked (the lead approved this deviation: they are data, not work, and what runs on them afterwards is refused where it runs). The extension counts a successful socket send as delivery and drops its queued copy (`chrome-extension/capture_bridge.js:111-117`), so **tightening that later, with a bridge that refuses pushes while locked, needs an extension release first**, one that holds captures while the panel says "Sign in", and time for it to reach installs: the bridge would otherwise lose a capture without a trace.
4. **The worker learns from the frames it already receives**, in `ws.onmessage` before the reply is handed to `ask.js`, and from the `ping` that `MonoAsk.probe()` sends on every connect (`recall_bridge.js:124`, `ask.js:223`): `ping` carries `enforced` (index §3.4 item 7), so the panel knows at connect time; a bridge that omitted it would be known at the first refused request.
5. **While the bridge refuses, the worker asks `ping` every 5 seconds** (the same 4-second timeouts as the probe), and nothing otherwise: a signed-in machine costs no extra traffic.

---

## Tasks

### Task 1: `MonoAccount`, what the bridge says about the account (pure)

**Files:**
- Create: `chrome-extension/account_state.js`, `chrome-extension/account_state.test.mjs`

**Interfaces:**
- Consumes, from the B3b plan (`docs/mastermind/plans/2026-10-05-monoes-account-gate-b3b-doors.md`, Task 7, the table of the extension bridge) and index §3.4 item 7: the frames of a locked bridge. A refused request is `{"kind":"reply","id":"req-…","error":"Log in to monoes.me first: monoagentcli account login","code":"account_locked"}`, for every `kind:"request"` frame but `ping`, with **no `ok` key** (`Reply.OK` is `omitempty`, `internal/extension/request.go:122`): test `!frame.ok`, never `frame.ok === false`. `ping` is answered: `{"kind":"reply","id":"req-…","ok":true,"data":{"pong":true,"methods":[…],"account":{"state":"ok|grace|locked","reason":"…","valid_until":"<RFC 3339, absent without a session>","enforced":<bool>}}}`. `enforced` is part of the frozen contract (index §3.4 items 4, 5 and 7; amendment A4); the reader still tolerates a bridge that omits it. Pushed captures, recordings and bindings stay accepted while locked, and `GET /monoagent/health` carries no account object.
- Produces: `MonoAccount = {fromReply(frame), refusing(account), tracker(), CODE_ACCOUNT_LOCKED}`, a global in the worker (loaded by `importScripts`) and in tests (`loadExtensionScripts(["account_state.js"])` from `chrome-extension/test_helpers.mjs`). `fromReply(frame) → {state, reason, enforced} | null` (null: the frame proves nothing); `refusing(account) → boolean` (locked, and `enforced === true`); `tracker() → {get(), refusing(), set(account | null), onChange(fn(account, wasRefusing))}`.

- [ ] **Step 1: Write the failing test.** Create `chrome-extension/account_state.test.mjs`:

```js
// What the bridge says about the monoes.me account, read from its frames.
//
// The case that matters: a locked state that only LOOKS locked (before the
// enforcement date the bridge still answers everything, and `ping` says "locked"
// all the same) must not make the side panel tell anyone to sign in. These pin
// what counts as "refusing", and that a refusal is read from the frame the
// bridge really sends: a failed reply has no "ok" key (Reply.OK is omitempty in
// internal/extension/request.go), so `ok === false` never appears on the wire.
//
// `node --test 'chrome-extension/**/*.test.mjs'`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoAccount: A } = loadExtensionScripts(["account_state.js"]);

const reply = (extra) => Object.assign({ kind: "reply", id: "req-1" }, extra);
const ping = (account) => reply({ ok: true, data: { pong: true, methods: ["ping"], account } });

test("a refused request proves the account is locked and enforced", () => {
  const account = A.fromReply(reply({ code: "account_locked", error: "Log in to monoes.me first: monoagentcli account login" }));
  assert.deepEqual(account, { state: "locked", reason: "", enforced: true });
  assert.equal(A.refusing(account), true);
});

test("a refusal has no ok key on the wire, and is read from its code alone", () => {
  // Exactly what internal/extension/request.go marshals for a failed reply.
  const wire = JSON.parse('{"kind":"reply","id":"req-1","error":"Log in to monoes.me first: monoagentcli account login","code":"account_locked"}');
  assert.equal("ok" in wire, false);
  assert.deepEqual(A.fromReply(wire), { state: "locked", reason: "", enforced: true });
  assert.deepEqual(A.fromReply({ ...wire, ok: false }), { state: "locked", reason: "", enforced: true }, "ask.js documents ok:false: it reads the same");
});

test("ping carries the state, with the reason", () => {
  assert.deepEqual(A.fromReply(ping({ state: "locked", reason: "refused", enforced: true })), { state: "locked", reason: "refused", enforced: true });
  assert.deepEqual(A.fromReply(ping({ state: "ok" })), { state: "ok", reason: "", enforced: true });
  assert.deepEqual(A.fromReply(ping({ state: "grace", reason: "unreachable" })), { state: "grace", reason: "unreachable", enforced: true });
});

test("locked before the enforcement date is not a refusal", () => {
  const account = A.fromReply(ping({ state: "locked", reason: "not_logged_in", enforced: false }));
  assert.equal(account.enforced, false);
  assert.equal(A.refusing(account), false);
  assert.equal(A.refusing({ state: "locked", reason: "not_logged_in" }), false, "an account that does not say it is enforced is no refusal");
});

test("a locked state with no word on enforcement proves nothing: it is null, so it neither invents a refusal nor clears one", () => {
  // A bridge that sends only {state, reason, valid_until} cannot be told from the warn period.
  assert.equal(A.fromReply(ping({ state: "locked", reason: "not_logged_in" })), null);
  const t = A.tracker();
  const take = (frame) => {
    const said = A.fromReply(frame);
    if (said) t.set(said);
  };
  take(ping({ state: "locked" }));
  assert.equal(t.refusing(), false, "nothing was proven");
  take(reply({ code: "account_locked" }));
  assert.equal(t.refusing(), true);
  take(ping({ state: "locked" }));
  assert.equal(t.refusing(), true, "an ambiguous ping does not clear a proven refusal");
  take(ping({ state: "ok" }));
  assert.equal(t.refusing(), false, "a signed-in ping does");
});

test("a successful answer to anything but ping proves the bridge is not refusing", () => {
  assert.deepEqual(A.fromReply(reply({ ok: true, data: { saved: false } })), { state: "ok", reason: "", enforced: true });
  assert.deepEqual(A.fromReply(reply({ ok: true })), { state: "ok", reason: "", enforced: true });
  assert.equal(A.fromReply(ping(undefined)), null, "a ping that says nothing about the account proves nothing");
});

test("signed-in and in-grace bridges are not refusing", () => {
  for (const state of ["ok", "grace"]) assert.equal(A.refusing({ state, reason: "", enforced: true }), false, state);
});

test("a frame that says nothing about the account says nothing", () => {
  const silent = [
    null,
    undefined,
    "reply",
    {},
    reply({ ok: true, data: { pong: true, methods: [] } }), // a bridge that predates the gate, or a ping that does not say
    reply({ ok: true, data: { account: "locked" } }),
    reply({ ok: true, data: { account: { reason: "refused" } } }),
    reply({ code: "unavailable", error: "monomind is not installed" }), // a failed reply: no ok key
    reply({ code: "timeout" }),
    reply({ ok: false, code: "unavailable" }),
    reply({ progress: { stage: "searching" } }),
    { kind: "request", id: "x", method: "ping" },
    { id: "1", success: true, data: { account: { state: "locked" } } }, // a command response, not a reply
  ];
  for (const frame of silent) assert.equal(A.fromReply(frame), null, JSON.stringify(frame));
  assert.equal(A.refusing(null), false);
  assert.equal(A.refusing(undefined), false);
});

test("an unknown account is not refusing: a bridge that predates the gate never says", () => {
  const t = A.tracker();
  assert.equal(t.get(), null);
  assert.equal(t.refusing(), false);
});

test("the tracker tells its listeners when the account changes, and whether it was refusing", () => {
  const t = A.tracker();
  const seen = [];
  t.onChange((account, wasRefusing) => seen.push([account && account.state, wasRefusing]));

  t.set({ state: "locked", reason: "not_logged_in", enforced: true });
  assert.equal(t.refusing(), true);
  t.set({ state: "locked", reason: "not_logged_in", enforced: true }); // the same again: nothing new
  t.set({ state: "ok", reason: "", enforced: true });
  assert.equal(t.refusing(), false);
  t.set(null); // the socket closed
  assert.deepEqual(seen, [["locked", false], ["ok", true], [null, false]]);
});

test("a listener that throws cannot stop the next one", () => {
  const t = A.tracker();
  const seen = [];
  t.onChange(() => {
    throw new Error("decoration");
  });
  t.onChange((account) => seen.push(account.state));
  t.set({ state: "ok", reason: "", enforced: true });
  assert.deepEqual(seen, ["ok"]);
});
```

- [ ] **Step 2: Run it and see it fail.** From the repo root: `node --test chrome-extension/account_state.test.mjs`. Expected: `ℹ tests 1`, `ℹ pass 0`, `ℹ fail 1`: the file cannot load, `Error: ENOENT: no such file or directory, open '…/chrome-extension/account_state.js'`.

- [ ] **Step 3: Create `chrome-extension/account_state.js`:**

```js
/**
 * MonoAgent Bridge — what the bridge says about the monoes.me account
 *
 * While MonoAgent has no valid sign-in the bridge answers every request but
 * `ping` with {kind: "reply", id, error, code: "account_locked"} (spec §6.3),
 * and `ping` says where the account stands in `data.account`. This reads those
 * frames into one fact the worker and the side panel share: is the bridge
 * refusing? Pure, so it runs in node (account_state.test.mjs).
 *
 * A failed reply has no "ok" key on the wire (internal/extension/request.go,
 * Reply.OK is omitempty), so a refusal is read from its code, never from
 * `ok === false`.
 */

(function (root) {
  "use strict";

  /** The code a refused request carries. */
  const CODE_ACCOUNT_LOCKED = "account_locked";
  const LOCKED = "locked";

  /**
   * fromReply reads one frame from the bridge and returns the account it
   * proves, or null when it proves nothing:
   *   - a failed reply with the account_locked code proves a locked, enforced
   *     account;
   *   - a successful answer to anything but ping proves the bridge is not
   *     refusing (a refusing bridge answers every other request with the code);
   *   - `ping` carries the state itself, but a locked state with no word on
   *     enforcement could be the warn period, when the bridge still answers
   *     everything: that proves nothing, so it is null and can neither clear a
   *     refusal that was proven nor invent one.
   */
  function fromReply(frame) {
    if (!frame || frame.kind !== "reply" || frame.progress) return null;
    if (!frame.ok && frame.code === CODE_ACCOUNT_LOCKED) {
      return { state: LOCKED, reason: "", enforced: true };
    }
    if (!frame.ok) return null;
    const data = frame.data || {};
    if (data.account === undefined) {
      return data.pong === true ? null : { state: "ok", reason: "", enforced: true };
    }
    const account = data.account;
    if (!account || typeof account !== "object" || typeof account.state !== "string") return null;
    if (account.state === LOCKED && typeof account.enforced !== "boolean") return null;
    return {
      state: account.state,
      reason: typeof account.reason === "string" ? account.reason : "",
      enforced: account.enforced !== false,
    };
  }

  /**
   * refusing is true when the bridge is turning requests away: locked, and
   * said to be enforced. An account that does not say is not a refusal.
   */
  function refusing(account) {
    return !!account && account.state === LOCKED && account.enforced === true;
  }

  /**
   * tracker holds the last account the bridge reported (null: unknown, which
   * counts as not refusing: a bridge that predates the gate never says). A
   * listener is called with (account, wasRefusing) whenever it changes.
   */
  function tracker() {
    let current = null;
    const listeners = [];
    return {
      get: () => current,
      refusing: () => refusing(current),
      set(next) {
        const before = current;
        const after = next || null;
        if (JSON.stringify(before) === JSON.stringify(after)) return;
        current = after;
        for (const fn of listeners) {
          try {
            fn(after, refusing(before));
          } catch {
            // A listener is decoration; it cannot stop the others.
          }
        }
      },
      onChange: (fn) => listeners.push(fn),
    };
  }

  root.MonoAccount = { fromReply, refusing, tracker, CODE_ACCOUNT_LOCKED };
})(globalThis);
```

- [ ] **Step 4: Run it again.** Same command. Expected: `ℹ tests 11`, `ℹ pass 11`, `ℹ fail 0`.

- [ ] **Step 5: Commit:**

```bash
git add chrome-extension/account_state.js chrome-extension/account_state.test.mjs
git commit -m "feat(extension): read the monoes.me account state from the bridge's frames" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 2: The side panel says it

**Files:**
- Modify: `chrome-extension/sidepanel_status.js` (lines 58, 122, 350), `chrome-extension/sidepanel_status.test.mjs` (append after line 342), `chrome-extension/sidepanel.js` (lines 113-114, 149, 168-169), `chrome-extension/background.js` (lines 28, 70, 443, 458-459, 531-536, 1332)

**Interfaces:**
- Consumes: `MonoAccount` (Task 1); the worker's existing `broadcastStatus()` and `statusPayload()` (`background.js:540`, `:531`), `MonoAsk.request(method, params, {timeoutMs, idleTimeoutMs})` (`ask.js`), the panel's `connection` object and `drawStatus` (`sidepanel.js`).
- Produces: `MonoPanelStatus.ACCOUNT_COMMAND` (`"monoagentcli account login"`); the `describe` state `account_locked` for the input `{status: "connected", account: {refusing: true, state, reason}}` (tone `warn`, label "Sign in to MonoAgent", the command, no queue); the worker's status payload gains `account: {refusing, state, reason}`; the worker asks `ping` every 5 seconds while the bridge refuses.

- [ ] **Step 1: Write the failing tests.** Append to `chrome-extension/sidepanel_status.test.mjs` (after its last line, 342 today):

```js
// ── a bridge that refuses because MonoAgent has no valid monoes.me sign-in ──────

const refusing = (reason = "not_logged_in") => ({ account: { refusing: true, state: "locked", reason } });

test("a connected bridge that refuses says to sign in, with the command", () => {
  const view = S.describe({ status: "connected", ...refusing() });
  assert.equal(view.key, "account_locked");
  assert.equal(view.tone, "warn");
  assert.equal(view.label, "Sign in to MonoAgent");
  assert.match(view.title, /signed out/);
  assert.match(view.body, /needs a monoes.me sign-in/);
  assert.equal(view.command, S.ACCOUNT_COMMAND);
  assert.equal(S.ACCOUNT_COMMAND, "monoagentcli account login");
  assert.equal(view.queues, false, "saving pages still works: the bridge accepts pushes while locked");
  assert.equal(view.busy, false);
});

test("a sign-in monoes.me ended says so, and points at another account", () => {
  const view = S.describe({ status: "connected", ...refusing("refused") });
  assert.equal(view.key, "account_locked");
  assert.match(view.title, /ended this sign-in/);
  assert.match(view.body, /another account/);
});

test("a bridge that is not refusing, or does not say, is still just connected", () => {
  for (const account of [null, undefined, { refusing: false, state: "ok", reason: "" }, { refusing: false, state: "locked", reason: "not_logged_in" }, {}]) {
    const view = S.describe({ status: "connected", account });
    assert.equal(view.key, "connected", JSON.stringify(account));
    assert.equal(view.tone, "ok");
    assert.equal(view.title, "");
  }
});

test("only a connected socket can be told it is refused: no other state borrows the message", () => {
  for (const status of ["waiting", "connecting", "unpaired", "disconnected", ""]) {
    const view = S.describe({ status, ...refusing() });
    assert.notEqual(view.key, "account_locked", status);
  }
});

test("a refused bridge outranks nothing the arbiter decides: the worker's account survives arbitration", () => {
  const worker = { status: "connected", ...refusing() };
  const arbitrated = S.arbitrate(worker, { status: "waiting", reason: "" });
  assert.equal(S.describe(arbitrated).key, "account_locked");
});
```

- [ ] **Step 2: Run them and see them fail.** `node --test chrome-extension/sidepanel_status.test.mjs`. Expected: `ℹ tests 36`, `ℹ pass 33`, `ℹ fail 3`: `a connected bridge that refuses says to sign in, with the command`, `a sign-in monoes.me ended says so, and points at another account` and `a refused bridge outranks nothing the arbiter decides: the worker's account survives arbitration`, each with `AssertionError [ERR_ASSERTION]: Expected values to be strictly equal` (the other two new tests pass against the old code, which never refuses).

- [ ] **Step 3: Add the panel's state.** In `chrome-extension/sidepanel_status.js`: after line 58 add

```js
  const ACCOUNT_COMMAND = "monoagentcli account login";
```

immediately before line 122 (`    if (status === "connected") {`) insert

```js
    // The bridge is up and attached, and turning every request away because
    // MonoAgent has no valid monoes.me sign-in (account_state.js decides
    // that). Nothing is wrong with the connection, and the person has one
    // thing to do, so it is a warning with the way to do it.
    if (status === "connected" && state.account && state.account.refusing) {
      const refused = String(state.account.reason || "") === "refused";
      return frame({
        key: "account_locked",
        tone: "warn",
        label: "Sign in to MonoAgent",
        title: refused ? "monoes.me ended this sign-in" : "MonoAgent is signed out",
        body:
          said ||
          (refused
            ? "This account can no longer use MonoAgent. Sign in with another account in the MonoAgent app, or run:"
            : "The bridge is running, but it needs a monoes.me sign-in before it answers questions or looks up your saved pages. Sign in in the MonoAgent app, or run:"),
        command: ACCOUNT_COMMAND,
      });
    }
```

(blank line after it), and in the `root.MonoPanelStatus = {` list add `    ACCOUNT_COMMAND,` after `    PROFILE_COMMAND,` (line 350).

- [ ] **Step 4: Run the panel tests.** Same command. Expected: `ℹ tests 36`, `ℹ pass 36`, `ℹ fail 0`.

- [ ] **Step 5: Carry it through the panel.** In `chrome-extension/sidepanel.js`: in the `connection` object, after `  wsUrl: "",` (line 114) add

```js
  // What the worker last said about the bridge's account: {refusing, state, reason}.
  account: null,
```

after line 149 (`  connection.detail = next.detail || "";`) add

```js
  connection.account = next.account || null;
```

and in `drawStatus`'s `Status.describe({…})` call, after the `wsUrl:` line (169), add

```js
    account: connection.account,
```

- [ ] **Step 6: Let the worker read it and pass it on.** In `chrome-extension/background.js`, working from the bottom up:
  - before the comment `// The activity recorder rides the same socket:` (line 1332) add, followed by a blank line:

```js
// The side panel hears of a change at once. While the bridge refuses it is asked
// again every few seconds: its answer to ping (read in ws.onmessage) is how this
// worker learns the person signed in.
account.onChange(() => broadcastStatus());
setInterval(() => {
  if (account.refusing() && ws?.readyState === WebSocket.OPEN) {
    MonoAsk.request("ping", {}, { timeoutMs: 4000, idleTimeoutMs: 4000 }).catch(() => {});
  }
}, 5000);

```

  - in `statusPayload` (line 531), after the `binding:` line (536) add:

```js
    // What the bridge said about the monoes.me account; the side panel words it.
    account: {
      refusing: account.refusing(),
      state: (account.get() || {}).state || "",
      reason: (account.get() || {}).reason || "",
    },
```

  - in `ws.onclose` (line 458), after the `stopKeepAlive();` line (459) add:

```js
    // What the bridge said about the account belonged to that socket.
    account.set(null);
```

  - in `ws.onmessage`, after the `if (cmd.type === "pong") return;` line (443) add:

```js
    // What the bridge says about the account rides on its replies (account_state.js).
    const said = MonoAccount.fromReply(cmd);
    if (said) account.set(said);
```

  - after line 70 (`let keepAliveInterval = null;`) add:

```js
// What the bridge says about the monoes.me account (account_state.js); the side
// panel says it to the person.
const account = MonoAccount.tracker();
```

  - line 28 becomes (`account_state.js` joins the list, before `saved.js`):

```js
importScripts("ask.js", "account_state.js", "saved.js", "highlights.js", "recall_bridge.js");
```

- [ ] **Step 7: Run the checks.** `node --test chrome-extension/account_state.test.mjs chrome-extension/sidepanel_status.test.mjs`. Expected: `ℹ tests 47`, `ℹ pass 47`, `ℹ fail 0`. Then `find chrome-extension -name '*.js' -exec node --check {} +` prints nothing, and `node --test chrome-extension/ask.test.mjs chrome-extension/capture_bridge.test.mjs` still passes (neither file is touched). `background.js` and `sidepanel.js` have no unit tests: the pure parts they call are tested above and in Task 1, and Task 3 step 4 looks at the wiring against a real bridge.

- [ ] **Step 8: Commit:**

```bash
git add chrome-extension/sidepanel_status.js chrome-extension/sidepanel_status.test.mjs chrome-extension/sidepanel.js chrome-extension/background.js
git commit -m "feat(extension): the side panel says when the bridge needs a monoes.me sign-in" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

### Task 3: Whole-branch verification

No new code. Run, in order, from the worktree root, and fix what fails before offering the branch.

- [ ] **Step 1: Syntax.** `find chrome-extension -name '*.js' -exec node --check {} +` prints nothing (CI's own command, `ci.yml:124`).
- [ ] **Step 2: Tests.** `find chrome-extension -name '*.test.mjs' ! -name '*.browser.test.mjs' | xargs node --test` ends `ℹ tests 661`, `ℹ fail 0`. (CI's `node --test 'chrome-extension/**/*.test.mjs'` also runs the browser tests, which need a Chrome that starts the service worker.)
- [ ] **Step 3: Mutation checks.** Break each line below, run `node --test chrome-extension/account_state.test.mjs chrome-extension/sidepanel_status.test.mjs`, see the named test fail, restore the line (`git diff` must show nothing for the file afterwards):
  - `account_state.js` `fromReply`: `if (!frame.ok && frame.code === CODE_ACCOUNT_LOCKED) {` to `if (frame.ok === false && frame.code === CODE_ACCOUNT_LOCKED) {` fails `a refusal has no ok key on the wire, and is read from its code alone` (and the two tests that send the refusal as the bridge does).
  - `account_state.js` `fromReply`: delete the line `if (account.state === LOCKED && typeof account.enforced !== "boolean") return null;`; `a locked state with no word on enforcement proves nothing` fails.
  - `account_state.js` `refusing`: `account.enforced === true` to `account.enforced !== false`; `locked before the enforcement date is not a refusal` fails.
  - `sidepanel_status.js`: delete `status === "connected" && ` from the `account_locked` condition; `only a connected socket can be told it is refused` fails.
- [ ] **Step 4: With B3b and B5c merged, look at the real thing.** Build the CLI with the dev tag outside the repo root (`go build -tags devaccount -o <tmp>/monoagentcli ./cmd/monoagentcli`) and pair the unpacked extension with `<tmp>/monoagentcli extension serve` (it starts while signed out: B2's `serve` class). `MONOAGENT_DEV_ENFORCE_FROM`, an RFC 3339 time that only a `-tags devaccount` binary reads (B5c), moves the enforcement date. With it set to `2020-01-01T00:00:00Z` and no sign-in: the side panel shows "Sign in to MonoAgent" with `monoagentcli account login`, at once when `ping` carries `enforced`, otherwise after the first refused request (opening a page makes the extension ask `doc.lookup`, `chrome-extension/saved.js:141`); saving a page still works. Run `<tmp>/monoagentcli account login` against the stand-in monoes.me that a `devaccount` build trusts (B1b: it honors `MONOES_BASE_URL` and the development key) and sign in: the panel clears within about 5 seconds, with no reload. Restart the bridge with the variable set to a time a week ahead (the warn period) and no sign-in: the panel says nothing and pages still save. Stop the bridge: the panel falls back to its disconnected state, and starting it again with the date in the past shows "Sign in to MonoAgent" again (the account belongs to the socket). Without the variable, before B5a sets the date (dormant), the panel never says anything.

## Contract change requests

None open. The one this plan raised, `enforced` on the account object of the bridge's `ping`, was accepted and is part of the frozen contract (index §3.4 items 4, 5 and 7; spec amendment A4).
