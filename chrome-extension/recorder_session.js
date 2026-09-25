/**
 * MonoAgent Bridge — the recording session, in the worker (§8.2, contracts §6)
 *
 * recorder.js reports what happened in the page; this decides what it means
 * for the recording and puts it on the wire as kind:"recording" frames
 * (internal/recording/types.go Frame). It owns the three things a page
 * cannot know:
 *
 *   - order: every event gets its id ("e1", "e2", …), seq and t (ms since
 *     start) here, so events from several frames and several page loads form
 *     one strictly increasing sequence;
 *   - scope: one tab. Events from any other tab are ignored; a tab opened
 *     FROM the recorded tab stops the recording ("new_tab"), closing it stops
 *     it ("tab_closed"), and so does closing the side panel ("panel_closed");
 *   - delivery: frames go out in order through an outbox that survives the
 *     bridge being down (and the worker being restarted), and is flushed in
 *     the same order when it comes back — the capture_queue.js promise,
 *     nothing recorded is silently dropped.
 *
 * Navigation comes from chrome.webNavigation, not the page: a typed URL,
 * reload or back/forward is `navigate` (the person did it); a link, form or
 * script navigation is `navigated` (the previous event did it).
 *
 * Everything Chrome-facing is injected (deps), so the state machine runs
 * under node --test. recorder_wiring.js supplies the real deps.
 */

(function (root) {
  "use strict";

  const STATE_KEY = "recordingState";
  const OUTBOX_KEY = "recordingOutbox";
  const MAX_OUTBOX = 5000;
  const MAX_STEPS = 500;
  const STOP_SETTLE_MS = 150;

  const REASONS = ["user", "tab_closed", "new_tab", "panel_closed", "error"];

  /** navKind maps a webNavigation commit to navigate / navigated + navCause. */
  function navKind(transitionType, qualifiers, history) {
    const q = qualifiers || [];
    if (q.indexOf("forward_back") !== -1) return { type: "navigate", navCause: "back_forward" };
    if (transitionType === "reload") return { type: "navigate", navCause: "reload" };
    if (history) return { type: "navigated", navCause: "script" };
    if (q.indexOf("client_redirect") !== -1) return { type: "navigated", navCause: "script" };
    if (q.indexOf("from_address_bar") !== -1 || /^(typed|auto_bookmark|generated|keyword|keyword_generated|start_page)$/.test(transitionType || "")) {
      return { type: "navigate", navCause: "typed" };
    }
    if (transitionType === "form_submit") return { type: "navigated", navCause: "form" };
    if (transitionType === "link") return { type: "navigated", navCause: "link" };
    return { type: "navigated", navCause: "script" };
  }

  function newRecordingId(now) {
    const d = new Date(now);
    const pad = (n) => String(n).padStart(2, "0");
    const stamp = `${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}-${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}`;
    const rand = Math.random().toString(36).slice(2, 6);
    return `rec-${stamp}-${rand}`;
  }

  /** stepOf is what the side panel keeps of an event: no candidates, no snippet. */
  function stepOf(ev) {
    const s = { id: ev.id, seq: ev.seq, type: ev.type, url: ev.url };
    for (const k of ["value", "masked", "secretAs", "key", "checked", "navCause", "extract", "param", "note"]) {
      if (ev[k] !== undefined) s[k] = ev[k];
    }
    if (ev.target) {
      const t = ev.target;
      s.target = {};
      for (const k of ["tag", "ariaName", "label", "text", "placeholder", "name", "inputType", "role"]) {
        if (t[k]) s.target[k] = t[k];
      }
    }
    return s;
  }

  function createSession(deps) {
    const now = deps.now || (() => Date.now());
    const settleMs = deps.settleMs == null ? STOP_SETTLE_MS : deps.settleMs;
    let state = blank();
    let outbox = [];
    let frameSeq = 0;
    let saving = Promise.resolve();
    let stopping = null;

    function blank() {
      return { recording: false, id: "", tabId: 0, goal: "", url: "", title: "", startedAt: 0, seq: 0, lastExtract: "", pick: false, stopReason: "", steps: [] };
    }

    // Writes are coalesced: a burst of events is one storage write of the
    // state as it is when the write runs, not one write per event.
    let dirty = false;
    function persist() {
      if (dirty) return saving;
      dirty = true;
      saving = saving
        .then(() => {
          dirty = false;
          return deps.storage.set({ [STATE_KEY]: state, [OUTBOX_KEY]: outbox.slice() });
        })
        .catch(() => {});
      return saving;
    }

    function status() {
      return {
        recording: state.recording,
        id: state.id,
        tabId: state.tabId,
        goal: state.goal,
        url: state.url,
        title: state.title,
        startedAt: state.startedAt,
        pick: state.pick,
        stopReason: state.stopReason,
        steps: state.steps.slice(),
        queued: outbox.length,
        delivered: !!state.id && !outbox.some((f) => f.recordingId === state.id),
      };
    }

    function changed() {
      persist();
      if (deps.notify) {
        try {
          deps.notify(status());
        } catch {
          // no panel listening
        }
      }
    }

    /** pump sends queued frames in order until the socket refuses one. */
    function pump() {
      let sent = 0;
      while (outbox.length && deps.isConnected()) {
        let ok = false;
        try {
          ok = deps.send(outbox[0]) !== false;
        } catch {
          ok = false;
        }
        if (!ok) break;
        outbox.shift();
        sent++;
      }
      return sent;
    }

    function enqueue(frame) {
      frameSeq += 1;
      const f = Object.assign({ kind: "recording", id: `${frame.recordingId}-f${frameSeq}` }, frame);
      outbox.push(f);
      if (outbox.length > MAX_OUTBOX) {
        // Never drop an event, start or stop: the oldest DOM snippet goes first.
        const at = outbox.findIndex((x) => x.op === "snapshot");
        outbox.splice(at === -1 ? 0 : at, 1);
      }
      pump();
    }

    /** flush is called when the socket (re)opens. */
    function flush() {
      const sent = pump();
      if (sent) changed();
      return sent;
    }

    function addEvent(ev, snippet) {
      state.seq += 1;
      const event = Object.assign({ id: `e${state.seq}`, seq: state.seq }, ev);
      event.t = Math.max(0, Math.round((ev.t != null ? ev.t : now()) - state.startedAt));
      // Key order is cosmetic, but keep the types.go order for readable jsonl.
      const ordered = { id: event.id, seq: event.seq, t: event.t, type: event.type, url: event.url || "" };
      for (const k of Object.keys(event)) if (!(k in ordered)) ordered[k] = event[k];
      enqueue({ op: "event", recordingId: state.id, event: ordered });
      if (snippet) {
        enqueue({ op: "snapshot", recordingId: state.id, eventId: ordered.id, name: `dom-${ordered.id}.html`, data: snippet });
      }
      if (ordered.type === "extract") state.lastExtract = ordered.id;
      state.steps.push(stepOf(ordered));
      if (state.steps.length > MAX_STEPS) state.steps.shift();
      changed();
      return ordered;
    }

    async function start(opts) {
      if (state.recording) throw new Error("a recording is already running");
      const tabId = opts && opts.tabId;
      if (!tabId) throw new Error("no tab to record");
      const info = (deps.tabInfo && (await deps.tabInfo(tabId))) || {};
      const startedAt = now();
      state = Object.assign(blank(), {
        recording: true,
        id: newRecordingId(startedAt),
        tabId,
        goal: String((opts && opts.goal) || "").trim(),
        url: info.url || "",
        title: info.title || "",
        startedAt,
      });
      const frame = { op: "start", recordingId: state.id, tabId, url: state.url, title: state.title, startedAt };
      if (state.goal) frame.goal = state.goal;
      const profile = deps.profile ? await deps.profile() : "";
      if (profile) frame.profile = profile;
      enqueue(frame);
      changed();
      try {
        await deps.inject(tabId);
      } catch (err) {
        await stop("error");
        throw new Error(`this page cannot be recorded: ${err.message || err}`);
      }
      if (deps.badge) deps.badge(tabId, true);
      return status();
    }

    async function stop(reason) {
      if (!state.recording) return status();
      if (stopping) return stopping;
      const why = REASONS.indexOf(reason) !== -1 ? reason : "user";
      stopping = (async () => {
        const tabId = state.tabId;
        if (why !== "tab_closed" && deps.uninject) {
          // Stopping the page recorders flushes any debounced value; give
          // those last messages a moment to land before the stop frame.
          try {
            await deps.uninject(tabId);
          } catch {
            // the tab is gone or closed to scripts
          }
          if (settleMs) await new Promise((r) => setTimeout(r, settleMs));
        }
        enqueue({ op: "stop", recordingId: state.id, reason: why });
        state.recording = false;
        state.pick = false;
        state.stopReason = why;
        if (deps.badge) deps.badge(tabId, false);
        changed();
        return status();
      })();
      try {
        return await stopping;
      } finally {
        stopping = null;
      }
    }

    /** event takes one recorder_event message from a content script. */
    function event(msg, sender) {
      const tabId = sender && sender.tab && sender.tab.id;
      if (!state.recording || tabId !== state.tabId || !msg || !msg.event) return null;
      const ev = Object.assign({}, msg.event);
      const at = ev.at;
      delete ev.at;
      if (at != null) ev.t = at;
      if (msg.replacesLastExtract && state.lastExtract) ev.note = `supersedes ${state.lastExtract}`;
      return addEvent(ev, msg.snippet || "");
    }

    /** navigation takes webNavigation.onCommitted / onHistoryStateUpdated details. */
    function navigation(details, history) {
      if (!state.recording || !details || details.tabId !== state.tabId || details.frameId !== 0) return null;
      if (!/^(https?|file):/i.test(details.url || "")) return null;
      const kind = navKind(details.transitionType, details.transitionQualifiers, history);
      state.url = details.url;
      return addEvent({ type: kind.type, url: details.url, navCause: kind.navCause, t: details.timeStamp || now() });
    }

    /** documentReady re-injects the recorder into a frame that just loaded. */
    function documentReady(details) {
      if (!state.recording || !details || details.tabId !== state.tabId) return null;
      return Promise.resolve(deps.inject(state.tabId, details.frameId, state.pick)).catch(() => {});
    }

    function tabCreated(tab) {
      if (state.recording && tab && tab.openerTabId === state.tabId) return stop("new_tab");
      return null;
    }

    function navigationTarget(details) {
      if (state.recording && details && details.sourceTabId === state.tabId) return stop("new_tab");
      return null;
    }

    function tabRemoved(tabId) {
      if (state.recording && tabId === state.tabId) return stop("tab_closed");
      return null;
    }

    function panelClosed() {
      if (state.recording) return stop("panel_closed");
      return null;
    }

    async function setPick(on) {
      state.pick = !!on && state.recording;
      if (state.recording && deps.pick) await deps.pick(state.tabId, state.pick);
      changed();
      return status();
    }

    function findStep(id) {
      return state.steps.find((s) => s.id === id) || null;
    }

    /**
     * mark records a side-panel decision about an earlier event:
     *   {kind:"param", refEvent, name, on}   type value is (not) a run-time input
     *   {kind:"extract", refEvent, field}    rename an extract's field
     * Both become new events (the recording is append-only); the analyzer
     * applies the latest mark per refEvent. An un-marked param carries
     * note "unset".
     */
    function mark(m) {
      if (!state.id || !m) throw new Error("nothing is being recorded");
      const ref = findStep(m.refEvent);
      if (!ref) throw new Error(`no step ${m.refEvent}`);
      const url = ref.url || state.url;
      if (m.kind === "param") {
        if (ref.type !== "type" && ref.type !== "upload" && ref.type !== "select_option") {
          throw new Error("only typed, chosen or uploaded values can be inputs");
        }
        const ev = { type: "param", url, param: { refEvent: ref.id } };
        if (m.name) ev.param.name = String(m.name);
        if (m.on === false) ev.note = "unset";
        return addEvent(ev);
      }
      if (m.kind === "extract") {
        if (ref.type !== "extract" || !ref.extract) throw new Error(`${ref.id} is not an extract`);
        const extract = Object.assign({}, ref.extract, { field: String(m.field || "").trim() || ref.extract.field });
        return addEvent({ type: "extract", url, extract, note: `supersedes ${ref.id}` });
      }
      throw new Error(`unknown mark ${m.kind}`);
    }

    /** handleFrame consumes Go's {id, success, type:"recording"} acks. */
    function handleFrame(msg) {
      return !!(msg && msg.type === "recording" && typeof msg.success === "boolean" && !msg.kind);
    }

    /** restore reloads state after a worker restart. */
    async function restore() {
      try {
        const got = (await deps.storage.get([STATE_KEY, OUTBOX_KEY])) || {};
        if (got[STATE_KEY] && typeof got[STATE_KEY] === "object") state = Object.assign(blank(), got[STATE_KEY]);
        if (Array.isArray(got[OUTBOX_KEY])) outbox = got[OUTBOX_KEY].concat(outbox);
        frameSeq = outbox.length + state.seq * 2 + 1000;
      } catch {
        // nothing stored
      }
      if (state.recording && deps.badge) deps.badge(state.tabId, true);
      return status();
    }

    /** clear forgets a finished recording's summary once the panel is done with it. */
    function clear() {
      if (state.recording) throw new Error("stop the recording first");
      state = blank();
      changed();
      return status();
    }

    return {
      start, stop, event, navigation, documentReady, tabCreated, navigationTarget, tabRemoved,
      panelClosed, setPick, mark, flush, handleFrame, restore, clear, status,
      outbox: () => outbox.slice(),
      saved: () => saving,
    };
  }

  root.MonoRecorderSession = { createSession, navKind, stepOf, STATE_KEY, OUTBOX_KEY, MAX_OUTBOX, REASONS };
})(globalThis);
