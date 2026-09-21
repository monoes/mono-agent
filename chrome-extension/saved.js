/**
 * MonoAgent Bridge — "you've already saved this" (RCL-02)
 *
 * When a tab lands on a page that is already in the brain, the toolbar icon
 * says so, and the popup shows what is stored: when it was captured, how many
 * versions there are, the note and tags that were written at save time, and
 * a way to open the archived copy.
 *
 * The whole difficulty is that this runs on EVERY navigation, and the answer
 * costs a Node process on the other end. Three rules keep that honest:
 *
 *  - DEBOUNCE. A navigation fires several times (committed, then title, then
 *    favicon); a redirect chain fires several more. Only the URL a tab has
 *    settled on is worth asking about.
 *  - CACHE, per URL and not per tab, because the common case is the same page
 *    open twice or reopened from history. A tab switch to a page that was
 *    looked up a minute ago costs nothing.
 *  - FAIL SILENT-BUT-VISIBLE. If the bridge is down or monomind is not
 *    installed, the badge is CLEARED rather than showing a question mark. An
 *    absent badge already means "not saved, as far as I know", which is the
 *    truth. The popup is where the reason gets said out loud.
 *
 * The badge is painted per tab (`chrome.action.setBadgeText({tabId})`) so it
 * follows the page it describes. A capture's own transient badge is global,
 * so this module stands out of its way: `capturedNow` clears the tab badge
 * and re-asks a moment later, by which time the answer is "saved" anyway.
 */

(function (root) {
  "use strict";

  // Long enough to swallow a redirect chain and the onUpdated storm a single
  // navigation produces, short enough that the badge is there before anyone
  // reaches for the toolbar.
  const DEBOUNCE_MS = 400;
  // A capture made elsewhere could change the answer, but not often, and a
  // stale "not saved" corrects itself on the next visit.
  const CACHE_TTL_MS = 120000;
  // Bounded so a long browsing session cannot grow this without limit. LRU
  // by insertion order, which for this access pattern is close enough.
  const MAX_CACHE = 200;

  const BADGE_SAVED = "✓";
  const BADGE_COLOR = "#2e8b57";

  let deps = null;
  const cache = new Map(); // identity url -> { record, at }
  // tabId -> { timer, resolve, url }. The resolve is held alongside the
  // timer because a debounce that is superseded must SETTLE the call it
  // replaces, not abandon it: the same "a promise always settles" rule the
  // request channel holds itself to (ask.js), and just as easy to break.
  const timers = new Map();
  const tabUrls = new Map(); // tabId -> identity url, for the popup

  /**
   * install takes: ask(method, params, opts) -> Promise (MonoAsk.request),
   * badge({tabId, text, color}), and optionally now() for tests.
   */
  function install(d) {
    deps = d;
    cache.clear();
    for (const tabId of [...timers.keys()]) supersede(tabId);
    tabUrls.clear();
  }

  const now = () => (deps && deps.now ? deps.now() : Date.now());

  /**
   * identityUrl is the same rule the ingest side keys on
   * (captureIdentityUrl in capture-envelope.ts, identityURL in
   * internal/extension/knowledge.go): drop the fragment, rewrite nothing
   * else. A query string genuinely distinguishes pages on real sites.
   */
  function identityUrl(raw) {
    const s = String(raw || "").trim();
    const hash = s.indexOf("#");
    return (hash === -1 ? s : s.slice(0, hash)).trim();
  }

  /** capturable reports whether a URL names a page that could ever have
   *  been saved. A chrome:// or about: tab is not an unsaved page. */
  function capturable(url) {
    return /^https?:\/\/[^/\s]+/i.test(url);
  }

  function remember(url, record) {
    cache.set(url, { record, at: now() });
    while (cache.size > MAX_CACHE) {
      const oldest = cache.keys().next().value;
      cache.delete(oldest);
    }
  }

  function cached(url) {
    const hit = cache.get(url);
    if (!hit) return null;
    if (now() - hit.at > CACHE_TTL_MS) {
      cache.delete(url);
      return null;
    }
    return hit.record;
  }

  function paint(tabId, record) {
    if (!deps || !deps.badge || !tabId) return;
    const saved = !!(record && record.saved);
    try {
      deps.badge({
        tabId,
        text: saved ? BADGE_SAVED : "",
        color: BADGE_COLOR,
        title: saved ? savedTitle(record) : "",
      });
    } catch {
      // No action API (tests, a worker mid-teardown) — the badge is
      // decoration and must never fail a lookup.
    }
  }

  /** savedTitle is the toolbar tooltip: the one line worth reading without
   *  opening anything. */
  function savedTitle(record) {
    const parts = ["Saved to monomind"];
    if (record.versions > 1) parts.push(`${record.versions} versions`);
    if (record.capturedAt) parts.push(shortDate(record.capturedAt));
    return parts.join(" · ");
  }

  function shortDate(iso) {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return "";
    return new Date(t).toISOString().slice(0, 10);
  }

  /**
   * lookup asks the backend, with no debounce and no cache read. Everything
   * else goes through check(); this is the one place that actually spends a
   * request.
   */
  async function lookup(url) {
    const record = await deps.ask("doc.lookup", { url }, { timeoutMs: 10000, idleTimeoutMs: 10000 });
    // A backend that answers something unrecognisable is treated as "no
    // answer" rather than believed — a wrong badge is worse than none.
    if (!record || typeof record !== "object") return { saved: false, url };
    return record;
  }

  /**
   * check is what a navigation calls. Debounced per tab, cached per URL, and
   * never rejecting: a caller on the navigation path has nothing useful to
   * do with an error, and the return value says what happened.
   *
   * Resolves to {record, source} where source is "cache" | "backend" |
   * "skipped" | "offline".
   */
  function check(tabId, rawUrl, opts) {
    const options = opts || {};
    const url = identityUrl(rawUrl);
    if (tabId) tabUrls.set(tabId, url);

    if (!capturable(url)) {
      paint(tabId, null);
      return Promise.resolve({ record: { saved: false, url }, source: "skipped" });
    }

    if (!options.force) {
      const hit = cached(url);
      if (hit) {
        paint(tabId, hit);
        return Promise.resolve({ record: hit, source: "cache" });
      }
    }

    // One pending lookup per tab. A tab mid-redirect asks about the URL it
    // ends on, not each one it passes through — and the calls it passed
    // through are settled as superseded, never dropped.
    supersede(tabId);

    return new Promise((resolve) => {
      const timer = setTimeout(async () => {
        timers.delete(tabId);
        // The tab may have moved on while this was waiting.
        if (tabId && tabUrls.get(tabId) !== url) {
          resolve({ record: { saved: false, url }, source: "skipped" });
          return;
        }
        try {
          const record = await lookup(url);
          remember(url, record);
          if (!tabId || tabUrls.get(tabId) === url) paint(tabId, record);
          resolve({ record, source: "backend" });
        } catch (err) {
          // Silent but visible: no badge, and the reason kept for the
          // popup to explain if anyone opens it.
          paint(tabId, null);
          resolve({
            record: { saved: false, url, unavailable: true, reason: err.message || String(err) },
            source: "offline",
          });
        }
      }, options.debounceMs === undefined ? DEBOUNCE_MS : options.debounceMs);
      // NOT unref'd: this timer resolves the promise the caller awaits, so
      // it must keep the event loop alive until it fires. See ask.js.
      timers.set(tabId, { timer, resolve, url });
    });
  }

  /** get is what the popup reads: the record for a tab, if one is cached.
   *  Never triggers a lookup — the popup opening is not a navigation. */
  function get(tabId) {
    const url = tabUrls.get(tabId);
    if (!url) return null;
    return cached(url);
  }

  /** forget drops a tab's state. Called when the tab closes. */
  function forget(tabId) {
    supersede(tabId);
    tabUrls.delete(tabId);
  }

  /** supersede cancels a tab's pending debounce AND settles the call that
   *  was waiting on it, so nothing is left holding an unsettled promise. */
  function supersede(tabId) {
    const entry = timers.get(tabId);
    if (!entry) return;
    clearTimeout(entry.timer);
    timers.delete(tabId);
    entry.resolve({ record: { saved: false, url: entry.url }, source: "superseded" });
  }

  /**
   * capturedNow is called after a capture of this tab lands. The cached
   * answer is now wrong, and the badge belongs to the capture for a moment:
   * clear it, let the capture's own badge show, then ask again.
   */
  function capturedNow(tabId, rawUrl, delayMs) {
    const url = identityUrl(rawUrl);
    cache.delete(url);
    paint(tabId, null);
    return check(tabId, url, {
      force: true,
      debounceMs: delayMs === undefined ? 4000 : delayMs,
    });
  }

  /** invalidate drops a URL from the cache without asking anything — for a
   *  capture that landed in some other tab. */
  function invalidate(rawUrl) {
    cache.delete(identityUrl(rawUrl));
  }

  root.MonoSaved = {
    install,
    check,
    get,
    forget,
    capturedNow,
    invalidate,
    identityUrl,
    capturable,
    savedTitle,
    cacheSize: () => cache.size,
    DEBOUNCE_MS,
    CACHE_TTL_MS,
    MAX_CACHE,
  };
})(globalThis);
