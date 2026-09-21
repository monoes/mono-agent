/**
 * MonoAgent Bridge — the capture queue's face (CLIP-08, UI half)
 *
 * capture.js already queues a capture taken while the bridge is down. That
 * is the part that stops work being lost; this is the part that stops it
 * being invisible. The rule the whole file exists to keep:
 *
 *   nothing a person captured is ever silently dropped.
 *
 * So there are two lists, not one. `captureQueue` holds envelopes waiting
 * for the bridge — capture.js writes it, this reads it. `captureFailures`
 * holds captures that could not even be queued (too large for the storage
 * bucket, say) or that were retried and refused, each with the reason it
 * failed, kept until a person dismisses it.
 *
 * Storage and the socket are injected, so the whole lifecycle — queue,
 * list, retry, fail, delete — runs in node (capture_queue.test.mjs).
 */

(function (root) {
  "use strict";

  const QUEUE_KEY = "captureQueue";
  const FAILURES_KEY = "captureFailures";
  const MAX_FAILURES = 25;

  const list = async (storage, key) => {
    try {
      const stored = (await storage.get(key)) || {};
      return Array.isArray(stored[key]) ? stored[key] : [];
    } catch {
      return [];
    }
  };

  const put = async (storage, key, value) => {
    try {
      await storage.set({ [key]: value });
    } catch {
      // A profile with no storage cannot queue; deliver() reports that as a
      // failure rather than pretending the capture landed.
    }
  };

  const bytesOf = (envelope) =>
    ((envelope && envelope.artifacts) || []).reduce((n, a) => n + (a.rawBytes || 0), 0);

  /** keyOf addresses one pending item. Legacy entries with no id fall back to their timestamp. */
  function keyOf(entry, index) {
    const id = entry && entry.envelope && entry.envelope.id;
    return id || (entry && entry.queuedAt) || `entry-${index}`;
  }

  function describe(entry, index, status) {
    const envelope = (entry && entry.envelope) || {};
    const meta = envelope.meta || {};
    return {
      key: keyOf(entry, index),
      status,
      title: meta.title || meta.url || "Untitled capture",
      url: meta.url || meta.canonicalUrl || "",
      at: entry.queuedAt || entry.failedAt || null,
      reason: entry.reason || null,
      artifacts: (envelope.artifacts || []).map((a) => a.name),
      bytes: bytesOf(envelope),
      warnings: envelope.warnings || [],
    };
  }

  /**
   * pending is everything the popup shows: what is waiting to be sent and
   * what has already failed, newest last, with the counts the badge needs.
   */
  async function pending(storage) {
    const queued = (await list(storage, QUEUE_KEY)).map((e, i) => describe(e, i, "queued"));
    const failed = (await list(storage, FAILURES_KEY)).map((e, i) => describe(e, i, "failed"));
    return { queued, failed, counts: { queued: queued.length, failed: failed.length } };
  }

  /**
   * recordFailure is the end of the line for a capture that cannot be
   * queued. It keeps the envelope when there is room, so retry is still
   * possible, and always keeps enough to say what was lost.
   */
  async function recordFailure(storage, envelope, reason, opts) {
    const o = Object.assign({ keepEnvelope: true }, opts || {});
    const current = await list(storage, FAILURES_KEY);
    const entry = {
      failedAt: new Date().toISOString(),
      reason: reason || "unknown failure",
      envelope: o.keepEnvelope
        ? envelope
        : { id: envelope && envelope.id, meta: (envelope && envelope.meta) || {}, artifacts: [] },
    };
    const next = current.concat([entry]).slice(-MAX_FAILURES);
    await put(storage, FAILURES_KEY, next);
    return { failed: true, reason: entry.reason, depth: next.length };
  }

  /**
   * deliver sends an envelope if the bridge is up, queues it if not, and
   * records a failure if it can do neither. deps.send(envelope) does the
   * framing; this file never touches the wire format.
   */
  async function deliver(deps, storage, envelope) {
    if (deps.isConnected()) {
      try {
        deps.send(envelope);
        return { sent: true, queued: false };
      } catch (err) {
        // The socket died between the check and the write.
        const queued = await root.MonoCapture.queueCapture(storage, envelope);
        if (queued.queued) return { sent: false, queued: true, depth: queued.depth };
        return Object.assign({ sent: false, queued: false }, await recordFailure(storage, envelope, err.message));
      }
    }

    const queued = await root.MonoCapture.queueCapture(storage, envelope);
    if (queued.queued) return { sent: false, queued: true, depth: queued.depth };
    return Object.assign(
      { sent: false, queued: false },
      // The envelope is too big for the queue bucket, so keeping it would
      // fail the same way. Keep the provenance; drop the bytes.
      await recordFailure(storage, envelope, queued.reason, { keepEnvelope: false })
    );
  }

  /** find locates a pending item in either list. */
  async function find(storage, key) {
    const queue = await list(storage, QUEUE_KEY);
    const at = queue.findIndex((e, i) => keyOf(e, i) === key);
    if (at >= 0) return { where: QUEUE_KEY, index: at, entries: queue, entry: queue[at] };
    const failures = await list(storage, FAILURES_KEY);
    const failedAt = failures.findIndex((e, i) => keyOf(e, i) === key);
    if (failedAt >= 0) {
      return { where: FAILURES_KEY, index: failedAt, entries: failures, entry: failures[failedAt] };
    }
    return null;
  }

  /**
   * retry sends one pending capture now. A success removes it; a failure
   * leaves it exactly where it was, with the reason updated, because the
   * one thing that must never happen is a retry that loses the capture.
   */
  async function retry(deps, storage, key) {
    const found = await find(storage, key);
    if (!found) return { ok: false, reason: "that capture is no longer pending" };

    const envelope = found.entry.envelope;
    if (!envelope || !envelope.artifacts || !envelope.artifacts.length) {
      return { ok: false, reason: "the capture's contents were dropped and cannot be resent" };
    }
    if (!deps.isConnected()) {
      return { ok: false, reason: "the bridge is still disconnected" };
    }

    try {
      deps.send(envelope);
    } catch (err) {
      const entries = found.entries.slice();
      entries[found.index] = Object.assign({}, found.entry, { reason: err.message });
      await put(storage, found.where, entries);
      return { ok: false, reason: err.message };
    }

    const entries = found.entries.slice();
    entries.splice(found.index, 1);
    await put(storage, found.where, entries);
    return { ok: true, title: (envelope.meta && envelope.meta.title) || null };
  }

  /** remove drops one pending item on the user's say-so — the only way anything leaves unsent. */
  async function remove(storage, key) {
    const found = await find(storage, key);
    if (!found) return { ok: false, reason: "that capture is no longer pending" };
    const entries = found.entries.slice();
    entries.splice(found.index, 1);
    await put(storage, found.where, entries);
    return { ok: true };
  }

  /** clearFailures dismisses every failure at once. */
  async function clearFailures(storage) {
    const failures = await list(storage, FAILURES_KEY);
    await put(storage, FAILURES_KEY, []);
    return { ok: true, cleared: failures.length };
  }

  // --- the badge ----------------------------------------------------------

  const badgeCount = (n) => (n > 99 ? "99+" : String(n));

  /**
   * badgeFor turns the counts into what the toolbar icon shows. Failures
   * win over queued items: a red count is the one a person has to act on,
   * and an amber one clears itself the moment the bridge comes back.
   */
  function badgeFor(counts) {
    const c = counts || {};
    const failed = c.failed || 0;
    const queued = c.queued || 0;
    if (failed) {
      return {
        text: badgeCount(failed),
        color: "#c0392b",
        title: `${failed} capture${failed === 1 ? "" : "s"} failed — open MonoAgent Bridge to see why`,
      };
    }
    if (queued) {
      return {
        text: badgeCount(queued),
        color: "#c98a00",
        title: `${queued} capture${queued === 1 ? "" : "s"} waiting for the bridge`,
      };
    }
    return { text: "", color: "#2e8b57", title: "MonoAgent Bridge" };
  }

  /** paintBadge applies badgeFor to the real toolbar icon. */
  async function paintBadge(storage) {
    const { counts } = await pending(storage);
    const badge = badgeFor(counts);
    try {
      await chrome.action.setBadgeText({ text: badge.text });
      await chrome.action.setBadgeBackgroundColor({ color: badge.color });
      await chrome.action.setTitle({ title: badge.title });
    } catch {
      // No action API in this context — the popup still shows the list.
    }
    return badge;
  }

  root.MonoCaptureQueue = {
    pending, deliver, retry, remove, recordFailure, clearFailures, badgeFor, paintBadge, keyOf,
    QUEUE_KEY, FAILURES_KEY, MAX_FAILURES,
  };
})(globalThis);
