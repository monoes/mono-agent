/**
 * MonoAgent Bridge — asking the bridge directly whether it is there
 *
 * The popup used to infer the bridge's existence from its own WebSocket:
 * the socket is down, therefore something is wrong, therefore red. That
 * inference is wrong in the common case, because the socket is also down
 * every time Chrome suspends the service worker, and it can say nothing at
 * all about *why* — a browser WebSocket deliberately exposes no reason for
 * a failed handshake.
 *
 * The bridge answers an unauthenticated loopback endpoint instead, so the
 * popup can just ask. `GET /monoagent/health` returns the document in
 * internal/extension/status.go.
 *
 * Two things about this endpoint are easy to get wrong, and both are the
 * whole reason this file exists rather than a bare fetch at the call site.
 *
 * 1. Something answering is not the bridge answering. Chrome run with
 *    --remote-debugging-port=9222 serves HTTP on the very port the bridge
 *    prefers, and will happily return a 200 with JSON that means something
 *    else entirely. So identity is checked on the `service` field, never on
 *    the status code: a document that does not say "monoagent-bridge" is
 *    treated exactly like no answer at all.
 *
 * 2. The failure has to be as fast as the success. "No bridge running" is
 *    the most common state this whole redesign exists to serve, and it is
 *    reached by a fetch that fails — so the probe runs every candidate at
 *    once behind a short deadline rather than walking them in turn. On
 *    loopback a refused connection returns in about a millisecond; the
 *    deadline is there for the pathological case of a port that accepts and
 *    then says nothing, which must not leave the popup reading "Checking…".
 */

(function (root) {
  "use strict";

  /** The bridge's self-description. Anything else on the port is not it. */
  const SERVICE = "monoagent-bridge";

  /**
   * How long the whole probe gets. Generous next to a loopback round trip
   * (~1ms) and short enough that a hung port is indistinguishable, to the
   * person holding the mouse, from a refused one.
   */
  const PROBE_TIMEOUT_MS = 600;

  /** The two ports the server binds, in the order it tries them. */
  const PORTS = [9222, 9323];

  const HOST = "127.0.0.1";

  /**
   * healthUrl turns a WebSocket URL into the health URL on the same origin.
   * Returns "" for anything it cannot parse, so a junk stored value skips a
   * candidate instead of throwing mid-probe.
   */
  function healthUrl(wsUrl) {
    const raw = String(wsUrl || "").trim();
    if (!raw) return "";
    const match = /^(wss?|https?):\/\/([^/?#]+)/i.exec(raw);
    if (!match) return "";
    const scheme = match[1].toLowerCase();
    const secure = scheme === "wss" || scheme === "https";
    return `${secure ? "https" : "http"}://${match[2]}/monoagent/health`;
  }

  /**
   * candidates lists every health URL worth trying, best first and without
   * repeats. What the user configured outranks what we learned, which
   * outranks the defaults — the same precedence background.js uses to pick
   * a socket, so the popup cannot end up describing a different bridge from
   * the one the worker is dialling.
   */
  function candidates(known) {
    const seen = new Set();
    const out = [];
    const add = (url) => {
      const health = healthUrl(url);
      if (health && !seen.has(health)) {
        seen.add(health);
        out.push(health);
      }
    };
    const from = known || {};
    add(from.wsUrl);
    add(from.pairedWsUrl);
    add(from.workingWsUrl);
    for (const port of PORTS) add(`ws://${HOST}:${port}/monoagent`);
    return out;
  }

  /**
   * isBridge is the identity check, and it is deliberately the only thing
   * that decides whether a reply counts. See the note at the top: a 200
   * from the wrong program is worse than no reply, because it looks like
   * success.
   */
  function isBridge(doc) {
    return !!doc && typeof doc === "object" && doc.service === SERVICE;
  }

  /**
   * read normalises a health document into the fields the popup uses, and
   * nothing else. An unrecognised `status` is passed through rather than
   * coerced: a newer bridge saying something we have not heard of should
   * surface as itself, not as a wrong guess.
   */
  function read(doc) {
    if (!isBridge(doc)) return null;
    return {
      status: String(doc.status || ""),
      connected: !!doc.connected,
      wsUrl: String(doc.wsUrl || ""),
      addr: String(doc.addr || ""),
      uptimeSec: Number(doc.uptimeSec) || 0,
      version: String(doc.version || ""),
    };
  }

  /**
   * toStatus maps a health reading onto the status/reason pair the popup's
   * describe() already understands, so the endpoint changes where the
   * answer comes from without adding a second vocabulary of states.
   *
   * `null` — the fetch found nothing anywhere — is the one that matters:
   * that is a bridge that is not running, stated rather than inferred.
   */
  function toStatus(health) {
    if (!health) {
      return { status: "disconnected", reason: "no_bridge" };
    }
    if (health.status === "connected") {
      return { status: "connected", reason: "", wsUrl: health.wsUrl };
    }
    if (health.status === "unpaired") {
      return { status: "unpaired", reason: "", wsUrl: health.wsUrl };
    }
    if (health.status === "waiting") {
      // The bridge is up and willing; nothing is attached to it this
      // instant. That is the resting state of a suspended service worker,
      // not a fault, and it resolves itself the moment anything is
      // captured — so it gets its own quiet status rather than borrowing
      // "disconnected", which would be true and useless.
      return { status: "waiting", reason: "", wsUrl: health.wsUrl };
    }
    return { status: health.status || "waiting", reason: "", wsUrl: health.wsUrl };
  }

  /**
   * probe asks every candidate at once and resolves with the first real
   * bridge document, preferring earlier candidates when more than one
   * answers. Resolves null when nothing does.
   *
   * `fetchImpl` and `timeoutMs` are injected so this is testable without a
   * network or a clock.
   */
  async function probe(options) {
    const opts = options || {};
    const fetchImpl = opts.fetch || (typeof fetch === "function" ? fetch : null);
    if (!fetchImpl) return null;
    const urls = opts.urls || candidates(opts.known);
    if (!urls.length) return null;
    const timeoutMs = Number(opts.timeoutMs) > 0 ? Number(opts.timeoutMs) : PROBE_TIMEOUT_MS;

    const results = await Promise.all(urls.map((url) => ask(fetchImpl, url, timeoutMs)));
    // Ranked, not raced: whichever answered first, the configured port is
    // still the one the user meant.
    for (const result of results) {
      if (result) return result;
    }
    return null;
  }

  /**
   * ask fetches one candidate. Every failure is the same failure: null.
   *
   * The deadline is enforced twice, on purpose. The abort signal is the
   * polite way and the one a real fetch honours; the race is the guarantee.
   * A transport that ignores its signal would otherwise leave the popup
   * reading "Checking…" for as long as it stayed open, and the whole point
   * of probing is that the answer arrives promptly or not at all.
   */
  function ask(fetchImpl, url, timeoutMs) {
    const controller = typeof AbortController === "function" ? new AbortController() : null;
    let timer = null;
    const deadline = new Promise((resolve) => {
      timer = setTimeout(() => {
        if (controller) controller.abort();
        resolve(null);
      }, timeoutMs);
    });

    const attempt = (async () => {
      try {
        const response = await fetchImpl(url, {
          method: "GET",
          signal: controller ? controller.signal : undefined,
          // Never reuse a cached answer for a liveness check.
          cache: "no-store",
        });
        if (!response || !response.ok) return null;
        return read(await response.json());
      } catch {
        // Refused, aborted, unparseable, or not JSON at all. A probe has no
        // errors worth reporting — either the bridge said hello or it did not.
        return null;
      }
    })();

    return Promise.race([attempt, deadline]).finally(() => clearTimeout(timer));
  }

  root.MonoBridgeHealth = {
    probe,
    candidates,
    healthUrl,
    isBridge,
    read,
    toStatus,
    SERVICE,
    PORTS,
    PROBE_TIMEOUT_MS,
  };
})(globalThis);
