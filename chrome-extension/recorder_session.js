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
 *   - delivery: frames go out through recorder_outbox.js, which keeps each
 *     one until Go acks it and resends the unacked ones in order on every
 *     reconnect. A recording is `delivered` only once its stop frame is
 *     acked -- Go has written the envelope -- and then every frame of it,
 *     typed values included, is purged from storage.
 *
 * The live recording is chrome.storage.session state: it survives a worker
 * restart but not a browser restart, so a recording can never "resume" in
 * whatever new tab reuses an old tab id. On restore the tab is checked too.
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

  /** clean applies the privacy module's URL rules when it is loaded (always, in the worker). */
  const clean = (u) => (root.MonoRecorderPrivacy ? root.MonoRecorderPrivacy.sanitizeUrl(u) : u);

  const OVERFLOW =
    "the bridge was unreachable for too long and the recording buffer is full, so recording stopped. " +
    "Start the bridge; what was recorded so far is kept and will be sent.";

  function createSession(deps) {
    const now = deps.now || (() => Date.now());
    const settleMs = deps.settleMs == null ? STOP_SETTLE_MS : deps.settleMs;
    const stateStore = deps.sessionStorage || deps.storage;
    let state = blank();
    let stopping = null;
    let saving = Promise.resolve();
    let dirty = false;

    const outbox = root.MonoRecorderOutbox.createOutbox({
      storage: deps.storage,
      send: (f) => deps.send(f),
      isConnected: () => deps.isConnected(),
      maxFrames: deps.maxFrames,
      maxBytes: deps.maxBytes,
      onError: (msg) => {
        state.error = msg;
        changed();
      },
    });

    function blank() {
      return {
        recording: false, id: "", tabId: 0, goal: "", url: "", title: "", profile: "", startedAt: 0,
        seq: 0, frameSeq: 0, lastExtract: "", pick: false, stopReason: "", steps: [],
        finalized: false, discarded: false, envelopeId: "", error: "", warning: "",
      };
    }

    // Writes are coalesced: a burst of events is one write of the state as
    // it is when the write runs.
    function persist() {
      if (dirty) return saving;
      dirty = true;
      saving = saving
        .then(() => {
          dirty = false;
          return stateStore.set({ [STATE_KEY]: state });
        })
        .catch(() => {});
      return saving;
    }

    function status() {
      return {
        recording: state.recording,
        stopping: !!stopping,
        id: state.id,
        tabId: state.tabId,
        goal: state.goal,
        url: state.url,
        title: state.title,
        profile: state.profile,
        startedAt: state.startedAt,
        pick: state.pick,
        stopReason: state.stopReason,
        steps: state.steps.slice(),
        queued: outbox.size(),
        delivered: !!state.id && state.finalized && !state.discarded,
        discarded: state.discarded,
        envelopeId: state.envelopeId,
        error: state.error,
        warning: state.warning,
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

    function enqueue(frame, force) {
      state.frameSeq += 1;
      const f = Object.assign({ kind: "recording", id: `${frame.recordingId}-f${state.frameSeq}` }, frame);
      // On every frame, not just start: a restarted bridge re-adopts the
      // spool by recordingId inside this profile's inbox.
      if (state.profile && !f.profile) f.profile = state.profile;
      const res = outbox.push(f, force);
      if (res.overflow && state.recording && !stopping) {
        state.error = OVERFLOW;
        Promise.resolve().then(() => stop("error"));
      }
      return !res.overflow;
    }

    /** flush resends every unacked frame, in order: called when the socket (re)opens. */
    function flush() {
      return outbox.resend();
    }

    function addEvent(ev, snippet) {
      state.seq += 1;
      const event = Object.assign({ id: `e${state.seq}`, seq: state.seq }, ev);
      event.t = Math.max(0, Math.round((ev.t != null ? ev.t : now()) - state.startedAt));
      // Key order is cosmetic, but keep the types.go order for readable jsonl.
      const ordered = { id: event.id, seq: event.seq, t: event.t, type: event.type, url: event.url || "" };
      for (const k of Object.keys(event)) if (!(k in ordered)) ordered[k] = event[k];
      if (!enqueue({ op: "event", recordingId: state.id, event: ordered })) {
        state.seq -= 1; // never sent, so the number is free again
        changed();
        return null;
      }
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
      if (state.recording || stopping) throw new Error("a recording is already running");
      const tabId = opts && opts.tabId;
      if (!tabId) throw new Error("no tab to record");
      const info = (deps.tabInfo && (await deps.tabInfo(tabId))) || {};
      const startedAt = now();
      state = Object.assign(blank(), {
        recording: true,
        id: newRecordingId(startedAt),
        tabId,
        goal: String((opts && opts.goal) || "").trim(),
        url: clean(info.url || ""),
        title: String(info.title || "").slice(0, 300),
        startedAt,
      });
      state.profile = deps.profile ? await deps.profile() : "";
      const frame = { op: "start", recordingId: state.id, tabId, url: state.url, title: state.title, startedAt };
      if (state.goal) frame.goal = state.goal;
      enqueue(frame, true);
      if (deps.listen) deps.listen(true);
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
        if (why !== "tab_closed") {
          try {
            if (state.pick && deps.pick) await deps.pick(tabId, false);
            // Stopping the page recorders flushes any debounced value; give
            // those last messages a moment to land before the stop frame.
            if (deps.uninject) await deps.uninject(tabId);
          } catch {
            // the tab is gone or closed to scripts
          }
          if (settleMs) await new Promise((r) => setTimeout(r, settleMs));
        }
        enqueue({ op: "stop", recordingId: state.id, reason: why }, true);
        state.recording = false;
        state.pick = false;
        state.stopReason = why;
        if (deps.listen) deps.listen(false);
        if (deps.badge) deps.badge(tabId, false);
        return status();
      })();
      try {
        return await stopping;
      } finally {
        stopping = null;
        changed();
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

    /** navigation takes webNavigation onCommitted / onHistoryStateUpdated / onReferenceFragmentUpdated. */
    function navigation(details, history) {
      if (!state.recording || stopping || !details || details.tabId !== state.tabId || details.frameId !== 0) return null;
      if (!/^(https?|file):/i.test(details.url || "")) return null;
      const kind = navKind(details.transitionType, details.transitionQualifiers, history);
      const url = clean(details.url);
      state.url = url;
      return addEvent({ type: kind.type, url, navCause: kind.navCause, t: details.timeStamp || now() });
    }

    /** documentReady re-injects the recorder into a frame that just loaded. */
    function documentReady(details) {
      if (!state.recording || stopping || !details || details.tabId !== state.tabId) return null;
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
      state.pick = !!on && state.recording && !stopping;
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
     * note "unset". Marks need the recording still open on the Go side.
     */
    function mark(m) {
      if (!state.id || !m) throw new Error("nothing is being recorded");
      if (!state.recording) throw new Error("the recording has stopped; mark inputs while recording");
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

    /**
     * handleFrame consumes Go's acks: {id, success, type:"recording"}. The
     * acked frame leaves the outbox. The stop frame's ack means the envelope
     * is written (or, "already finished", was written before): the recording
     * is delivered and its frames are purged from storage.
     */
    function handleFrame(msg) {
      if (!(msg && msg.type === "recording" && typeof msg.success === "boolean" && !msg.kind)) return false;
      const frame = outbox.ack(msg.id);
      if (!frame) return true;
      const err = String(msg.error || "");
      if (frame.op === "stop" && (msg.success || /already finished/i.test(err))) {
        if (frame.recordingId === state.id) {
          state.finalized = true;
          if (msg.data && msg.data.id) state.envelopeId = String(msg.data.id);
          // Go writes nothing for a recording with no events.
          if (msg.data && msg.data.discarded) state.discarded = true;
        }
        outbox.purge(frame.recordingId);
        changed();
      } else if (!msg.success) {
        state.warning = `the bridge refused a ${frame.op} frame: ${err || "no reason given"}`;
        changed();
      } else if (msg.data && msg.data.dropped) {
        state.warning = `the bridge dropped a ${frame.op} frame: ${msg.data.dropped}`;
        changed();
      } else {
        // A plain ack: the queued count changed.
        if (deps.notify) changed();
      }
      return true;
    }

    /** closeOrphans ends recordings left in the outbox with no stop frame. */
    function closeOrphans() {
      const open = new Map();
      for (const f of outbox.frames()) {
        if (f.op === "stop") open.set(f.recordingId, false);
        else if (!open.has(f.recordingId)) open.set(f.recordingId, f.profile || "");
      }
      for (const [id, profile] of open) {
        if (profile === false || (state.recording && id === state.id)) continue;
        const f = { kind: "recording", id: `${id}-orphan-stop`, op: "stop", recordingId: id, reason: "error" };
        if (profile) f.profile = profile;
        outbox.push(f, true);
      }
    }

    /**
     * restore reloads after a worker restart. `startup` (runtime.onStartup,
     * a new browser session) starts from nothing: any recording left in the
     * outbox from before is closed with reason "error".
     */
    async function restore(opts) {
      await outbox.load();
      try {
        const got = opts && opts.startup ? {} : (await stateStore.get(STATE_KEY)) || {};
        state = got[STATE_KEY] && typeof got[STATE_KEY] === "object" ? Object.assign(blank(), got[STATE_KEY]) : blank();
      } catch {
        state = blank();
      }
      if (state.recording) {
        const alive = deps.tabExists ? await deps.tabExists(state.tabId) : true;
        if (!alive) {
          state.error = "the recorded tab is gone";
          await stop("error");
        } else {
          if (deps.listen) deps.listen(true);
          if (deps.badge) deps.badge(state.tabId, true);
        }
      }
      closeOrphans();
      changed();
      return status();
    }

    /** clear forgets a finished recording's summary once the panel is done with it. */
    function clear() {
      if (state.recording || stopping) throw new Error("stop the recording first");
      state = blank();
      changed();
      return status();
    }

    return {
      start, stop, event, navigation, documentReady, tabCreated, navigationTarget, tabRemoved,
      panelClosed, setPick, mark, flush, handleFrame, restore, clear, status,
      outbox: () => outbox.frames(),
      saved: () => Promise.all([saving, outbox.saved()]),
    };
  }

  root.MonoRecorderSession = { createSession, navKind, stepOf, STATE_KEY, REASONS, OVERFLOW };
})(globalThis);
