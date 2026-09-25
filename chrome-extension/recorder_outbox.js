/**
 * MonoAgent Bridge -- the recording outbox, in the worker (spec section 8.2)
 *
 * Every kind:"recording" frame goes through here on its way to the bridge.
 * The promise is the capture queue's: nothing recorded is silently dropped.
 * Concretely:
 *
 *   - a frame leaves the outbox only when Go ACKS it ({id, success,
 *     type:"recording"}). Written-to-the-socket is not delivered: a socket
 *     that dies mid-flight loses what was in it, so on every reconnect
 *     every unacked frame is sent again, in order. Go drops duplicate
 *     events by id, so resending is safe;
 *   - a refused frame (success:false) is removed too -- sending it again
 *     would be refused again -- and the refusal is reported;
 *   - it is persisted in chrome.storage.local in chunks of CHUNK frames, and
 *     only the chunks that changed are rewritten, so a long recording is
 *     not one ever-growing blob rewritten on every event;
 *   - it is bounded: MAX_FRAMES frames and MAX_BYTES of JSON. Over a bound,
 *     DOM snapshots are evicted oldest first; start/event/stop frames never
 *     are. When only those are left, push() reports `overflow` and the
 *     session stops the recording with a visible reason.
 *
 * Why a byte cap and not the unlimitedStorage permission: storage.local's
 * 10MB quota is shared with the capture queue, and a recording that has
 * buffered 6MB offline is one the person should hear about, not one that
 * silently grows. storage.set failures (quota) are reported through
 * onError, which the session shows in the side panel.
 */

(function (root) {
  "use strict";

  const META_KEY = "recordingOutboxMeta";
  const CHUNK_PREFIX = "recordingOutbox.";
  const LEGACY_KEY = "recordingOutbox";
  const CHUNK = 25;
  const MAX_FRAMES = 5000;
  const MAX_BYTES = 6 * 1024 * 1024;

  function createOutbox(deps) {
    const maxFrames = deps.maxFrames || MAX_FRAMES;
    const maxBytes = deps.maxBytes || MAX_BYTES;
    const onError = deps.onError || (() => {});
    // chunks: [{n, frames: [{frame, bytes}]}], oldest first.
    let chunks = [];
    let next = 0;
    let bytes = 0;
    let count = 0;
    const dirty = new Set();
    const removed = new Set();
    let metaDirty = false;
    let saving = Promise.resolve();
    let scheduled = false;

    const all = () => chunks.flatMap((c) => c.frames);

    function schedule() {
      if (scheduled) return saving;
      scheduled = true;
      saving = saving.then(write, write);
      return saving;
    }

    async function write() {
      scheduled = false;
      const set = {};
      for (const n of dirty) {
        const c = chunks.find((x) => x.n === n);
        if (c) set[CHUNK_PREFIX + n] = c.frames.map((f) => f.frame);
      }
      dirty.clear();
      if (metaDirty) set[META_KEY] = { chunks: chunks.map((c) => c.n), next };
      metaDirty = false;
      const gone = Array.from(removed, (n) => CHUNK_PREFIX + n);
      removed.clear();
      try {
        if (Object.keys(set).length) await deps.storage.set(set);
        if (gone.length && deps.storage.remove) await deps.storage.remove(gone);
      } catch (err) {
        onError(`could not save the recording buffer: ${(err && err.message) || err}`);
      }
    }

    function touch(c) {
      dirty.add(c.n);
      schedule();
    }

    function dropChunkIfEmpty(c) {
      if (c.frames.length || c === chunks[chunks.length - 1]) return;
      chunks = chunks.filter((x) => x !== c);
      dirty.delete(c.n);
      removed.add(c.n);
      metaDirty = true;
    }

    function removeAt(c, i) {
      const [gone] = c.frames.splice(i, 1);
      bytes -= gone.bytes;
      count -= 1;
      touch(c);
      dropChunkIfEmpty(c);
      return gone.frame;
    }

    /** evictSnapshot removes the oldest snapshot frame; false when none is left. */
    function evictSnapshot() {
      for (const c of chunks) {
        const i = c.frames.findIndex((f) => f.frame.op === "snapshot");
        if (i !== -1) {
          removeAt(c, i);
          return true;
        }
      }
      return false;
    }

    function sendOne(frame) {
      if (!deps.isConnected()) return false;
      try {
        return deps.send(frame) !== false;
      } catch {
        return false;
      }
    }

    // While a resend is owed (a send failed), new frames wait for it, so
    // nothing ever overtakes an earlier frame on the wire.
    let behind = false;

    /**
     * push appends a frame and sends it when the socket is up. Returns
     * {overflow: true} when the bounds are hit and no snapshot is left to
     * evict; `force` (the stop frame) is always kept.
     */
    function push(frame, force) {
      const size = JSON.stringify(frame).length;
      while (!force && (count + 1 > maxFrames || bytes + size > maxBytes)) {
        if (!evictSnapshot()) return { overflow: true };
      }
      let tail = chunks[chunks.length - 1];
      if (!tail || tail.frames.length >= CHUNK) {
        tail = { n: next++, frames: [] };
        chunks.push(tail);
        metaDirty = true;
      }
      tail.frames.push({ frame, bytes: size });
      bytes += size;
      count += 1;
      touch(tail);
      if (!behind && !sendOne(frame)) behind = true;
      return { overflow: false };
    }

    /** resend sends every unacked frame again, in order (on reconnect). */
    function resend() {
      let sent = 0;
      for (const f of all()) {
        if (!sendOne(f.frame)) {
          behind = true;
          return sent;
        }
        sent++;
      }
      behind = false;
      return sent;
    }

    /** ack removes the frame Go answered; returns it, or null if unknown. */
    function ack(id) {
      for (const c of chunks) {
        const i = c.frames.findIndex((f) => f.frame.id === id);
        if (i !== -1) return removeAt(c, i);
      }
      return null;
    }

    /** purge forgets every frame of one recording (it is finalised). */
    function purge(recordingId) {
      for (const c of chunks.slice()) {
        for (let i = c.frames.length - 1; i >= 0; i--) {
          if (c.frames[i].frame.recordingId === recordingId) removeAt(c, i);
        }
      }
      if (!count) {
        for (const c of chunks) removed.add(c.n);
        chunks = [];
        metaDirty = true;
        schedule();
      }
    }

    async function load() {
      try {
        const got = (await deps.storage.get([META_KEY, LEGACY_KEY])) || {};
        const meta = got[META_KEY];
        if (meta && Array.isArray(meta.chunks)) {
          const keys = meta.chunks.map((n) => CHUNK_PREFIX + n);
          const data = (await deps.storage.get(keys)) || {};
          chunks = meta.chunks.map((n) => ({
            n,
            frames: (data[CHUNK_PREFIX + n] || []).map((frame) => ({ frame, bytes: JSON.stringify(frame).length })),
          }));
          next = meta.next || meta.chunks.length;
        }
        if (Array.isArray(got[LEGACY_KEY])) {
          // One-time move from the single-blob layout.
          for (const frame of got[LEGACY_KEY]) push(frame, true);
          if (deps.storage.remove) await deps.storage.remove(LEGACY_KEY);
        }
      } catch {
        chunks = [];
      }
      count = all().length;
      bytes = all().reduce((n, f) => n + f.bytes, 0);
      behind = count > 0;
      return count;
    }

    return {
      push, resend, ack, purge, load,
      frames: () => all().map((f) => f.frame),
      size: () => count,
      bytes: () => bytes,
      has: (recordingId) => all().some((f) => f.frame.recordingId === recordingId),
      saved: () => saving,
    };
  }

  root.MonoRecorderOutbox = { createOutbox, META_KEY, CHUNK_PREFIX, CHUNK, MAX_FRAMES, MAX_BYTES };
})(globalThis);
