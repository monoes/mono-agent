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
