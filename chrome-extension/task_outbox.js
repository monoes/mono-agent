/**
 * MonoAgent Bridge - the task outbox (task board spec 11.3)
 *
 * Every task the browser adds goes through here before anything else. A
 * request on the bridge fails at once when MonoAgent is not running and
 * never queues (ask.js), and a task the person added must not vanish
 * because nothing was listening. So:
 *
 *   - a task is QUEUED FIRST, then sent (task_bridge.js). It leaves the
 *     outbox only when MonoAgent answers with the task (made now, or by an
 *     earlier attempt: the client id makes a resend harmless), or refuses it
 *     in a way that would repeat (invalid_input), and then it moves to a
 *     short failures list, with up to FAILED_TEXT_BYTES of its text, which
 *     the side panel shows so the person can copy it;
 *   - every read-modify-write goes through one promise chain, so two adds a
 *     moment apart cannot write over each other, and a read that FAILED is
 *     never taken for an empty list: written back, it would wipe every
 *     waiting task. Only count and failedCount, which feed the badge, answer
 *     0 instead (a badge must never fail a capture);
 *   - it is bounded, MAX_ENTRIES tasks and MAX_BYTES of JSON, and a task that
 *     does not fit is refused, never queued half-way.
 *
 * Pure: storage is handed in (chrome.storage.local in the worker, a fake in
 * task_outbox.test.mjs), and nothing here touches the socket.
 */

(function (root) {
  "use strict";

  const KEY = "taskOutbox";
  const FAILED_KEY = "taskOutboxFailed";
  const MAX_ENTRIES = 200;
  const MAX_BYTES = 1024 * 1024;
  const MAX_FAILED = 20;
  // A task's text is cut to this before it is queued (spec 4.6 and 11.4).
  const MAX_TEXT_BYTES = 64 * 1024;
  // What a refused task keeps of its text, for the person to copy.
  const FAILED_TEXT_BYTES = 2048;

  const sizeOf = (list) => new TextEncoder().encode(JSON.stringify(list)).length;

  /** capBytes cuts s to at most max bytes of UTF-8, never inside a character. */
  function capBytes(s, max) {
    const text = String(s == null ? "" : s);
    const bytes = new TextEncoder().encode(text);
    if (bytes.length <= max) return text;
    let end = max;
    // A byte 10xxxxxx continues a character: step back to where one starts.
    while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
    return new TextDecoder().decode(bytes.subarray(0, end));
  }

  /** titleOf is the line a task is listed under: its first line, short. */
  function titleOf(entry) {
    const e = entry || {};
    const first = String(e.text || "").split("\n").find((line) => line.trim()) || e.title || e.url || "";
    const line = String(first).trim().replace(/\s+/g, " ");
    return line.length > 80 ? `${line.slice(0, 77)}...` : line;
  }

  /** read is one list; a storage that cannot be read throws. */
  async function read(storage, key) {
    const got = (await storage.get(key)) || {};
    return Array.isArray(got[key]) ? got[key] : [];
  }

  async function readOrNone(storage, key) {
    try {
      return await read(storage, key);
    } catch {
      return [];
    }
  }

  /** count is how many tasks wait, for the badge (capture_queue.js); 0 when unreadable. */
  async function count(storage) {
    return (await readOrNone(storage, KEY)).length;
  }

  /** failedCount is how many refused tasks are listed; 0 when unreadable. */
  async function failedCount(storage) {
    return (await readOrNone(storage, FAILED_KEY)).length;
  }

  /**
   * create returns the outbox over one storage area. Make ONE per worker:
   * the chain that keeps the writes in order belongs to the outbox.
   */
  function create(storage) {
    let chain = Promise.resolve();

    /** serial runs fn after everything queued before it, whatever that did. */
    function serial(fn) {
      const run = chain.then(() => fn());
      chain = run.catch(() => {});
      return run;
    }

    function enqueue(entry) {
      return serial(async () => {
        let list;
        try {
          list = await read(storage, KEY);
        } catch (err) {
          return { queued: false, reason: `could not read the waiting tasks: ${(err && err.message) || err}` };
        }
        if (list.length >= MAX_ENTRIES) return { queued: false, reason: `${MAX_ENTRIES} tasks are already waiting to sync` };
        const next = list.concat([entry]);
        if (sizeOf(next) > MAX_BYTES) {
          return { queued: false, reason: "the tasks waiting to sync already fill the space kept for them" };
        }
        try {
          await storage.set({ [KEY]: next });
        } catch (err) {
          return { queued: false, reason: `could not save it: ${(err && err.message) || err}` };
        }
        return { queued: true, size: next.length };
      });
    }

    function list() {
      return serial(() => read(storage, KEY));
    }

    function remove(clientId) {
      return serial(async () => {
        const current = await read(storage, KEY);
        const next = current.filter((e) => e.client_id !== clientId);
        if (next.length === current.length) return false;
        await storage.set({ [KEY]: next });
        return true;
      });
    }

    function fail(entry, reason) {
      return serial(async () => {
        const current = await read(storage, KEY);
        const failed = await read(storage, FAILED_KEY);
        const line = {
          client_id: entry.client_id,
          title: titleOf(entry),
          text: capBytes(entry.text || entry.title || entry.url || "", FAILED_TEXT_BYTES),
          profile: entry.profile,
          reason: String(reason || "refused"),
          at: new Date().toISOString(),
        };
        await storage.set({
          [KEY]: current.filter((e) => e.client_id !== entry.client_id),
          [FAILED_KEY]: failed.concat([line]).slice(-MAX_FAILED),
        });
        return line;
      });
    }

    function failures() {
      return serial(() => read(storage, FAILED_KEY));
    }

    function dismiss() {
      return serial(async () => {
        await storage.set({ [FAILED_KEY]: [] });
      });
    }

    return { enqueue, list, remove, fail, failures, dismiss };
  }

  root.MonoTaskOutbox = {
    create, count, failedCount, capBytes, titleOf,
    KEY, FAILED_KEY, MAX_ENTRIES, MAX_BYTES, MAX_FAILED, MAX_TEXT_BYTES, FAILED_TEXT_BYTES,
  };
})(globalThis);
