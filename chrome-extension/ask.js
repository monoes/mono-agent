/**
 * MonoAgent Bridge — asking the backend a question (the request channel)
 *
 * Commands have always flowed one way: Go sends, the extension answers. This
 * is the other direction — the extension originates a message and waits for
 * a reply. RCL-02 ("is this URL already saved?") and RCL-05 ("what does my
 * brain say about this page?") both ride it, and so does anything later that
 * needs to ask rather than be told.
 *
 * The wire is symmetrical with what the Go side does (internal/extension/
 * request.go). Out:
 *
 *   {kind: "request", id: "req-…", method: "doc.lookup", params: {…}}
 *
 * Back, zero or more progress frames and then exactly one settling frame:
 *
 *   {kind: "reply", id: "req-…", progress: {stage, detail}}
 *   {kind: "reply", id: "req-…", ok: true, data: {…}}
 *   {kind: "reply", id: "req-…", ok: false, error: "…", code: "unavailable"}
 *
 * `kind` is the only reason this is safe to add to a live socket: no command
 * and no response has ever carried one, so an older peer at either end sees
 * a frame it does not recognise and ignores it. The requester then times out
 * and falls back, which is the correct behaviour for a backend that predates
 * the channel.
 *
 * THE RULE THIS FILE EXISTS TO ENFORCE: a caller's promise always settles.
 * The service worker is suspended without warning, the socket dies mid-flight,
 * the backend never answers — in every one of those a pending request is
 * rejected with a code, never left hanging. A page load must never be waiting
 * on this.
 */

(function (root) {
  "use strict";

  const KIND_REQUEST = "request";
  const KIND_REPLY = "reply";

  // A question that has not been answered in this long is not going to be.
  // Well past the Go side's own 20s handler deadline, so a backend that is
  // working normally always settles a request before this fires — which
  // means a timeout here really does mean the frame or the socket was lost.
  const DEFAULT_TIMEOUT_MS = 25000;
  // A progress frame restarts this, so a long ask that keeps reporting is
  // not killed by the overall deadline. A silent one still is.
  const DEFAULT_IDLE_MS = 12000;

  // Error codes callers branch on. `offline` is this side's own: the socket
  // is down, so nothing was even sent. Everything else comes from the reply.
  const CODE_OFFLINE = "offline";
  const CODE_TIMEOUT = "timeout";
  const CODE_UNAVAILABLE = "unavailable";

  let deps = null;
  const pending = new Map(); // id -> { resolve, reject, timer, idle, onProgress }
  // What the backend said it can answer, from the last probe. Null means
  // "not asked yet"; an array means asked, and a panel can decide whether
  // to render at all instead of timing out against a method nobody has.
  let methods = null;
  // Which connection the cached `methods` belongs to. A probe answers about
  // one socket; if that socket is gone by the time the answer (or the
  // failure) arrives, the answer is about nothing and must not be cached.
  let connection = 0;
  let seq = 0;

  function install(d) {
    deps = d;
    methods = null;
    connection += 1;
  }

  function newId() {
    const uuid = globalThis.crypto && crypto.randomUUID && crypto.randomUUID();
    seq += 1;
    return `req-${uuid || `${Date.now()}-${seq}`}`;
  }

  function askError(message, code) {
    const err = new Error(message);
    err.code = code;
    return err;
  }

  /** settle removes a request from the table and clears its timers. */
  function settle(id) {
    const entry = pending.get(id);
    if (!entry) return null;
    pending.delete(id);
    if (entry.timer) clearTimeout(entry.timer);
    if (entry.idle) clearTimeout(entry.idle);
    return entry;
  }

  function arm(entry, ms, id, message, code) {
    const timer = setTimeout(() => {
      const found = settle(id);
      if (found) found.reject(askError(message, code));
    }, ms);
    // NOT unref'd. This timer is the only thing that settles the promise
    // the caller is awaiting, so it has to hold the event loop open until
    // it fires; unref'd, node:test sees a pending promise with an idle loop
    // and cancels the run. (`unref` does not exist on a worker's timer
    // anyway, so it only ever changed what the tests could observe.)
    return timer;
  }

  /**
   * request asks the backend one question.
   *
   * Rejects immediately with `offline` when the socket is down — deliberately
   * not queued. A question is only worth asking about the page in front of
   * the user right now, and answering it ten minutes later against a tab
   * that has moved on is worse than not answering. (Captures are the
   * opposite, which is why they queue; see capture_queue.js.)
   */
  function request(method, params, opts) {
    const options = opts || {};
    if (!deps || !deps.isConnected()) {
      return Promise.reject(askError("the monoagent bridge is not connected", CODE_OFFLINE));
    }
    const id = newId();
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, onProgress: options.onProgress || null };
      pending.set(id, entry);
      const timeoutMs = options.timeoutMs || DEFAULT_TIMEOUT_MS;
      const idleMs = options.idleTimeoutMs || DEFAULT_IDLE_MS;
      entry.timer = arm(entry, timeoutMs, id, `${method} timed out`, CODE_TIMEOUT);
      entry.idleMs = idleMs;
      entry.idle = arm(entry, idleMs, id, `${method} went quiet`, CODE_TIMEOUT);

      try {
        // `false` means the socket closed between the isConnected() check
        // above and this write. Without this the question sits in the table
        // until its 25s deadline, and whatever was waiting on it — a recall
        // panel, a profile list — spins for the whole of that.
        if (deps.send({ kind: KIND_REQUEST, id, method, params: params || {} }) === false) {
          throw askError("the monoagent bridge went away mid-request", CODE_OFFLINE);
        }
      } catch (err) {
        settle(id);
        reject(askError(err.message || "could not send the request", CODE_OFFLINE));
      }
    });
  }

  /**
   * handleFrame consumes one frame from the socket. Returns true when it was
   * a reply this module owns, so background.js's dispatch can stop there
   * rather than handing a reply to the command handler.
   */
  function handleFrame(frame) {
    if (!frame || frame.kind !== KIND_REPLY || !frame.id) return false;
    const entry = pending.get(frame.id);
    // A reply to a request that has already timed out, or to one from a
    // previous life of the service worker. Consumed, not dispatched: it is
    // still not a command.
    if (!entry) return true;

    if (frame.progress) {
      // Progress means the backend is alive and working, so the silence
      // timer starts again. The overall deadline is untouched — progress
      // buys patience, not immortality.
      if (entry.idle) clearTimeout(entry.idle);
      entry.idle = arm(entry, entry.idleMs, frame.id, "the backend went quiet", CODE_TIMEOUT);
      if (entry.onProgress) {
        try {
          entry.onProgress(frame.progress);
        } catch {
          // A progress callback is decoration; it cannot fail the request.
        }
      }
      return true;
    }

    settle(frame.id);
    if (frame.ok) entry.resolve(frame.data);
    else entry.reject(askError(frame.error || "the backend refused the request", frame.code || ""));
    return true;
  }

  /**
   * disconnected settles everything in flight. Called when the socket closes:
   * those requests will never be answered, and a promise nobody ever settles
   * is a panel that spins forever.
   */
  function disconnected(reason) {
    // Both of these go first, and the counter is why clearing `methods` on
    // its own was not enough. A reject only schedules the awaiting
    // continuation, so probe()'s catch runs after this function has returned
    // — and wrote [] back over the cache that had just been cleared. An
    // empty array is truthy, so probe() then served it for the life of the
    // worker: supports() false forever, and every panel gated on it
    // rendering as permanently absent. Moving the connection on tells that
    // late catch its answer is stale.
    methods = null;
    connection += 1;
    const ids = [...pending.keys()];
    for (const id of ids) {
      const entry = settle(id);
      if (entry) entry.reject(askError(reason || "the bridge disconnected", CODE_OFFLINE));
    }
  }

  /**
   * probe asks the backend what it can answer. Cached for the life of the
   * connection — the answer only changes when the backend restarts, which
   * closes the socket and clears this.
   */
  async function probe() {
    if (methods) return methods;
    const asked = connection;
    let found;
    try {
      const data = await request("ping", {}, { timeoutMs: 4000, idleTimeoutMs: 4000 });
      found = Array.isArray(data && data.methods) ? data.methods : [];
    } catch {
      // An older backend has no ping either, so this is also how "the
      // channel is not there at all" is discovered.
      found = [];
    }
    // The caller still gets an answer — an interrupted probe resolves empty
    // rather than rejecting, because "nothing is available" is the right
    // thing for a panel to render while the bridge is down. It is only the
    // cache that must not keep it.
    if (asked !== connection) return found;
    methods = found;
    return methods;
  }

  /** supports reports whether the backend advertised a method. */
  async function supports(method) {
    const list = await probe();
    return list.indexOf(method) !== -1;
  }

  /** isOffline is true for the errors that mean "nothing was wrong, the
   *  backend just is not there" — the ones a UI renders as absence. */
  function isOffline(err) {
    const code = err && err.code;
    return code === CODE_OFFLINE || code === CODE_UNAVAILABLE || code === CODE_TIMEOUT;
  }

  root.MonoAsk = {
    install,
    request,
    handleFrame,
    disconnected,
    probe,
    supports,
    isOffline,
    inFlight: () => pending.size,
    CODE_OFFLINE,
    CODE_TIMEOUT,
    CODE_UNAVAILABLE,
    DEFAULT_TIMEOUT_MS,
  };
})(globalThis);
