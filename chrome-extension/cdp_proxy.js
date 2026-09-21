/**
 * MonoAgent Bridge — raw CDP proxy (GLU-01 / RIG-07)
 *
 * background.js has passed CDP through for a while, but only in fixed
 * shapes: eval_cdp is `Runtime.evaluate` with the expression filled in,
 * type_cdp is a scripted click-and-insert. Every new instrument meant a new
 * command here and a new implementation on the Go side.
 *
 * This generalises those two into one: any CDP method, in both directions.
 * That is what lets monobrowse — whose console, network, HAR, vitals, trace,
 * profiler, snapshot, screenshot and emulation modules are all written
 * against a CDP client — point at the browser the user is actually signed
 * into, without a line of those modules changing. See
 * internal/extension/cdp.go for the relay and
 * packages/@monoes/monobrowse/src/browser/bridge.ts for the client.
 *
 * The events are the half that did not exist before. A command reply is
 * matched by id; a debugger event is nobody's reply, so it is pushed up as
 * an unsolicited frame the way a queued capture is (CLIP-08).
 *
 * What this cannot do is Chrome's business, not ours: chrome.debugger
 * exposes a fixed set of domains, and HeapProfiler and Browser are not among
 * them. Those commands come back as errors from Chrome, and are relayed as
 * errors rather than papered over.
 */

(function (root) {
  "use strict";

  // The Go server caps every bridge frame at 32MiB (maxMessageSize in
  // internal/extension/server.go) and drops the whole connection on a bigger
  // one — so an oversized screenshot or trace chunk would not just fail, it
  // would take the extension's socket down with it. Refuse them here, under
  // that limit, leaving room for the JSON envelope around the payload.
  const MAX_FRAME_BYTES = 30 * 1024 * 1024;

  // Pushed frames carry a type instead of an id, since nothing on the Go
  // side is waiting for them (see internal/extension/cdp.go's isCdpEvent).
  const EVENT_TYPE = "cdp_event";

  // Tabs we are relaying events for. A tab is in here between cdp_attach and
  // cdp_detach; events for anything else (another feature's debugger use,
  // another window) are not ours to forward.
  const subscribed = new Set();
  let deps = null;

  /**
   * install hands over what background.js owns: the socket, its per-tab
   * debugger helpers, the pin/unpin that keeps the idle sweep off a tab we
   * are listening to, and the two chrome.debugger listener registrations.
   */
  function install(d) {
    deps = d;
    d.onDebuggerEvent((source, method, params) => relayEvent(source, method, params));
    d.onDebuggerDetach((source, reason) => relayDetach(source, reason));
  }

  async function resolveTabId(params) {
    if (params && params.tabId) return params.tabId;
    const tabId = await deps.activeTabId();
    if (!tabId) throw new Error("cdp: no tab to attach to");
    return tabId;
  }

  /**
   * handleCommand answers the three cdp_* commands. background.js's dispatch
   * calls this and sends whatever comes back; a throw becomes the error in
   * that same reply.
   */
  async function handleCommand(type, params) {
    switch (type) {
      case "cdp_attach":
        return attach(params);
      case "cdp_detach":
        return detach(params);
      case "cdp":
        return command(params);
      default:
        throw new Error(`cdp proxy: unknown command ${type}`);
    }
  }

  async function attach(params) {
    const tabId = await resolveTabId(params);
    await deps.attach(tabId);
    subscribed.add(tabId);
    // Without this the alarm-driven sweep in background.js detaches after
    // 30s of no CDP traffic — which is exactly what a capture that only
    // *listens* (console, network, trace) looks like.
    deps.pin(tabId);
    return { tabId };
  }

  async function detach(params) {
    const tabId = await resolveTabId(params);
    subscribed.delete(tabId);
    deps.unpin(tabId);
    await deps.detach(tabId);
    return { tabId };
  }

  async function command(params) {
    const method = params && params.method;
    if (!method) throw new Error("cdp: method is required");
    const tabId = await resolveTabId(params);
    // Attaching on demand keeps a client that only sends commands honest:
    // it never has to know the debugger lifecycle, only CDP.
    await deps.attach(tabId);

    const session = { tabId };
    // chrome.debugger's tab debuggee IS the page session; a sessionId here
    // means a child session (an out-of-process iframe), so it is only set
    // when the caller actually named one.
    if (params.sessionId) session.sessionId = params.sessionId;

    const result = await deps.cdp(session, method, params.params || {});
    const payload = { result: result === undefined ? {} : result };
    const size = frameSize(payload);
    if (size > MAX_FRAME_BYTES) {
      throw new Error(
        `cdp: ${method} result too large (${size} bytes; the bridge frame limit is ${MAX_FRAME_BYTES})`
      );
    }
    return payload;
  }

  function frameSize(value) {
    try {
      return JSON.stringify(value).length;
    } catch {
      // Circular or otherwise unserializable: treat as unsendable.
      return Number.POSITIVE_INFINITY;
    }
  }

  function push(data) {
    if (!deps.isConnected()) return;
    deps.send({ type: EVENT_TYPE, success: true, data });
  }

  function relayEvent(source, method, params) {
    const tabId = source && source.tabId;
    if (!subscribed.has(tabId)) return;

    const data = { tabId, method, params: params || {} };
    if (source.sessionId) data.sessionId = source.sessionId;

    const size = frameSize(data);
    if (size > MAX_FRAME_BYTES) {
      // Dropping a trace chunk silently would corrupt the trace at the far
      // end with no sign of why, so the drop is itself an event. Nothing
      // listens for this method in CDP, which is the point: it reaches a
      // reader of the stream without pretending to be page data.
      push({
        tabId,
        method: "MonoAgent.eventDropped",
        params: { method, bytes: size, limit: MAX_FRAME_BYTES },
      });
      return;
    }
    push(data);
  }

  function relayDetach(source, reason) {
    const tabId = source && source.tabId;
    if (!subscribed.has(tabId)) return;
    subscribed.delete(tabId);
    deps.unpin(tabId);
    // The user hit Cancel on Chrome's debugging banner, DevTools opened on
    // the tab, or the tab closed. A client waiting on a command should hear
    // that now rather than sit out its timeout.
    push({ tabId, method: "Inspector.detached", params: { reason: reason || "unknown" } });
  }

  function subscribedTabs() {
    return [...subscribed];
  }

  root.MonoCdpProxy = { install, handleCommand, subscribedTabs };
})(globalThis);
