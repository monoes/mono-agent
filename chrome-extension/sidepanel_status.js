/**
 * MonoAgent Bridge — what the connection state MEANS
 *
 * (Written for the popup; the side panel draws exactly the same states.)
 *
 * The popup used to print one word per status: "Disconnected", in red, with
 * nothing to do about it. That word was usually correct and never useful —
 * the common cause is simply that no bridge is running, which is not an
 * error and has a one-line fix.
 *
 * This file is the whole translation, kept pure and away from the DOM so it
 * can be tested: raw worker status in, a sentence and an action out. Two
 * judgements live here and nowhere else.
 *
 * 1. A dropped socket is not news. An MV3 service worker is killed when it
 *    goes idle and respawns on the next event, so the socket drops and comes
 *    back constantly through an ordinary day. Flashing a red alarm each time
 *    would train the user to ignore the one time it matters, so a
 *    disconnection that is young, or that follows a connection this popup
 *    saw with its own eyes, is reported as reconnecting: amber, calm, no
 *    block of explanatory text. Only once it has failed to come back does it
 *    become something the user is asked to read.
 *
 * 2. "Not connected" is a question, not a verdict. When the worker cannot
 *    say why, the overwhelmingly likely answer for a loopback socket is that
 *    nothing is listening — so the copy leads with that and hedges honestly
 *    ("if the bridge isn't running"), rather than either asserting a cause we
 *    don't know or shrugging. When the worker CAN say why (`reason`), the
 *    copy drops the hedge and states it.
 *
 * Every field beyond `status` is optional. The four statuses background.js
 * broadcasts today (connected / connecting / unpaired / disconnected) map to
 * useful, actionable states with no other input at all; a richer contract
 * from the bridge daemon sharpens the wording without being required for any
 * of it to work.
 */

(function (root) {
  "use strict";

  /**
   * How long a disconnection is treated as a blip rather than a fact.
   * Sized for the thing it exists to absorb: a service-worker respawn and
   * reconnect is normally under two seconds, so six is forgiving without
   * leaving someone watching a spinner when the bridge is genuinely gone.
   */
  const RECONNECT_GRACE_MS = 6000;

  /**
   * The commands the popup tells people to run. These MUST match the Go
   * CLI — `extension serve` and `extension pair` in cmd/monoagentcli — and
   * they are defined here, once, precisely so that keeping them in step is
   * a one-line change rather than a hunt through the markup. Nothing else
   * in the extension may spell a command out.
   */
  const START_COMMAND = "monoagentcli extension serve";
  const PAIR_COMMAND = "monoagentcli extension pair";
  const PROFILE_COMMAND = "monoagentcli profile create <name>";

  const DEFAULT_WS_URL = "ws://127.0.0.1:9222/monoagent";

  /** Action ids the popup knows how to perform. */
  const ACTIONS = {
    PAIR: "pair",
    SETTINGS: "settings",
    RETRY: "retry",
  };

  /**
   * hostOf reduces a WebSocket URL to the part worth showing a human. The
   * full URL is noise in a sentence; "127.0.0.1:9222" is the bit that
   * answers "where was it looking?".
   */
  function hostOf(wsUrl) {
    const raw = String(wsUrl || "").trim() || DEFAULT_WS_URL;
    const match = /^[a-z]+:\/\/([^/?#]+)/i.exec(raw);
    return match ? match[1] : raw;
  }

  /**
   * age is how long the current state has been the current state. An absent
   * or nonsensical `since` reads as brand new, which is the safe direction:
   * it buys a state the benefit of the grace period rather than declaring a
   * problem the instant the popup opens.
   */
  function age(since, now) {
    const start = Number(since);
    if (!Number.isFinite(start) || start <= 0) return 0;
    const elapsed = Number(now) - start;
    return Number.isFinite(elapsed) && elapsed > 0 ? elapsed : 0;
  }

  /**
   * describe turns the worker's status into everything the popup draws: the
   * pill, and the block of explanation underneath it when there is something
   * worth saying. `title` empty means there is nothing to explain and no
   * block should be drawn at all — which is the case whenever things are
   * working or merely in motion.
   */
  function describe(input) {
    const state = input || {};
    const status = String(state.status || "").trim();
    const reason = String(state.reason || "").trim();
    const now = Number.isFinite(Number(state.now)) ? Number(state.now) : Date.now();
    const elapsed = age(state.since, now);
    const where = hostOf(state.wsUrl);

    // A sentence from the worker always wins over ours: it knows things we
    // are guessing at. Ours is the fallback, not the default.
    const said = String(state.detail || "").trim();

    if (!status || status === "checking") {
      return frame({
        key: "checking",
        tone: "busy",
        label: "Checking…",
        busy: true,
        queues: false,
      });
    }

    if (status === "connected") {
      return frame({
        key: "connected",
        tone: "ok",
        label: "Connected",
        queues: false,
      });
    }

    if (status === "waiting") {
      // The health endpoint says the bridge is up and nothing is attached
      // to it. That is what a suspended service worker looks like from the
      // outside — the normal resting state between captures, not a fault —
      // so it is reported as readiness, quietly, with no block and nothing
      // to do. A capture made right now wakes the worker and goes through,
      // which is why it does not warn about queueing.
      return frame({
        key: "waiting",
        tone: "ok",
        label: "Bridge ready",
        queues: false,
      });
    }

    if (status === "connecting") {
      return frame({
        key: state.everConnected ? "reconnecting" : "connecting",
        tone: "busy",
        label: state.everConnected ? "Reconnecting…" : "Connecting…",
        busy: true,
        queues: true,
      });
    }

    if (status === "unpaired" || reason === "auth_rejected") {
      // Two different problems wearing one status. Never paired is a setup
      // step nobody has done yet; rejected means a token exists and the
      // server refused it — usually because the bridge was re-paired
      // elsewhere. The fix is the same command, but the sentence that gets
      // someone to run it is not.
      const rejected = reason === "auth_rejected";
      return frame({
        key: rejected ? "rejected" : "unpaired",
        tone: "warn",
        label: rejected ? "Pairing rejected" : "Not paired",
        title: rejected ? "The bridge refused this browser's token" : "This browser isn't paired yet",
        body:
          said ||
          (rejected
            ? "The saved token is no longer the bridge's. Generate a new one and paste it below."
            : "The bridge only accepts a browser it has been introduced to. Run this, then paste what it prints."),
        command: PAIR_COMMAND,
        action: { id: ACTIONS.PAIR, label: "Paste a pairing token" },
        queues: true,
      });
    }

    if (status === "disconnected" || status === "offline") {
      if (reason === "bad_url") {
        return frame({
          key: "bad_url",
          tone: "error",
          label: "Bad address",
          title: "That bridge address was refused",
          body: said || `The extension will only talk to a bridge on this machine, and ${where} isn't one.`,
          action: { id: ACTIONS.SETTINGS, label: "Open connection settings" },
          queues: true,
        });
      }

      if (reason === "no_bridge") {
        return frame({
          key: "no_bridge",
          // Deliberately not an error tone. Nothing is broken; a program
          // that was never started is not a fault, and colouring it red
          // makes people think they have to fix something they broke.
          tone: "idle",
          label: "Bridge not running",
          title: "No bridge is running",
          body:
            said ||
            "Captures are held on this machine and sent the moment it starts. To start it now, run:",
          command: START_COMMAND,
          queues: true,
        });
      }

      // A drop we have reason to think is momentary: either it just
      // happened, or this popup watched the connection work a moment ago.
      // See the note at the top — this is the state that must not shout.
      if (elapsed < RECONNECT_GRACE_MS || state.everConnected) {
        return frame({
          key: "reconnecting",
          tone: "busy",
          label: "Reconnecting…",
          busy: true,
          queues: true,
        });
      }

      return frame({
        key: "offline",
        tone: "idle",
        label: "Not connected",
        title: "Nothing is answering at " + where,
        body:
          said ||
          "Captures are held on this machine and sent when the bridge is back. If it isn't running, start it with:",
        command: START_COMMAND,
        action: { id: ACTIONS.RETRY, label: "Try again" },
        queues: true,
      });
    }

    // An unknown status from a newer worker. Say the word it gave us rather
    // than inventing a state, and keep the tone neutral: we do not know that
    // anything is wrong.
    return frame({
      key: "unknown",
      tone: "idle",
      label: status,
      title: "",
      body: said,
      queues: true,
    });
  }

  /** frame fills in the fields a caller left out, so the popup can draw any result without guarding each field. */
  function frame(partial) {
    return {
      key: partial.key,
      tone: partial.tone,
      label: partial.label,
      title: partial.title || "",
      body: partial.body || "",
      command: partial.command || "",
      action: partial.action || null,
      busy: !!partial.busy,
      queues: !!partial.queues,
    };
  }

  /**
   * describeQueue says what is waiting, in the one sentence a person would
   * say out loud. It is separate from the connection state on purpose: a
   * queue can be full while the bridge is perfectly healthy (a capture that
   * failed for its own reasons), and empty while the bridge is down.
   *
   * Returns null when there is nothing waiting, which is the signal to draw
   * nothing at all rather than a box reporting zero.
   */
  function describeQueue(counts) {
    const queued = Math.max(0, Number(counts && counts.queued) || 0);
    const failed = Math.max(0, Number(counts && counts.failed) || 0);
    if (!queued && !failed) return null;

    const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

    if (failed && !queued) {
      return {
        tone: "error",
        text: `${plural(failed, "capture", "captures")} couldn't be sent`,
        failed,
        queued,
      };
    }
    if (queued && !failed) {
      return {
        tone: "warn",
        text: `${plural(queued, "capture is", "captures are")} waiting to send`,
        failed,
        queued,
      };
    }
    return {
      tone: "error",
      text: `${plural(queued, "capture", "captures")} waiting, ${failed} failed`,
      failed,
      queued,
    };
  }

  /**
   * arbitrate decides which of the popup's two sources answers, given the
   * worker's last broadcast and the health probe's last reading (either may
   * be null). It exists because they disagree in the most common case.
   *
   * The probe answers "is a bridge process running?" and knows. The worker
   * reports only what its own socket just did — and with no bridge running
   * it retries constantly, broadcasting "connecting" with no reason, then a
   * result, then "connecting" again. Letting every broadcast repaint the
   * popup made it flicker between "Bridge not running" and "Not connected"
   * for as long as it stayed open.
   *
   * So each source answers only what it can know:
   * - the socket alone knows it has attached, or that a token was refused;
   * - the probe alone knows whether anything is there to attach to;
   * - until the probe has answered, the worker stands in for it.
   */
  function arbitrate(worker, health) {
    const w = worker || null;
    const h = health || null;

    if (w && w.status === "connected") return w;
    if (w && (w.status === "unpaired" || w.reason === "auth_rejected")) return w;

    if (!h) return w || { status: "checking", reason: "" };

    // The probe's "connected" means a browser is attached to the bridge —
    // not that this one is. The bridge keeps one extension connection, so
    // with the extension in two browsers (or a second bridge on the
    // default port) it can be somebody else's socket. Once this worker has
    // said it is not attached, the most the probe knows is that a bridge
    // is up, which is what "waiting" means.
    const probe = h.status === "connected" && w ? Object.assign({}, h, { status: "waiting" }) : h;

    if (probe.status === "waiting" && w && w.status === "connecting") return w;
    return probe;
  }

  root.MonoPanelStatus = {
    arbitrate,
    describe,
    describeQueue,
    hostOf,
    RECONNECT_GRACE_MS,
    START_COMMAND,
    PAIR_COMMAND,
    PROFILE_COMMAND,
    DEFAULT_WS_URL,
    ACTIONS,
  };
})(globalThis);
