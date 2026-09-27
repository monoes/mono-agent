/**
 * MonoAgent Bridge — which monoagent profile this browser runs automations for
 *
 * Every browser profile with this extension runs its own copy of it, and the
 * bridge keeps one socket per copy. This file is what lets the bridge tell
 * them apart and route by profile:
 *
 *   INSTANCE ID. Minted once, by the service worker only (two contexts
 *   minting at once would race to two ids), kept in chrome.storage.local,
 *   which is per browser profile. It is how the bridge recognises the same
 *   browser across worker restarts.
 *
 *   BINDING. The monoagent profile whose browser actions run here, or "" for
 *   a default browser (runs profiles that have no browser of their own). Set
 *   in the side panel, or remotely with `monoagentcli extension bind`
 *   (a set_binding command). Either way it lands in storage, and the worker
 *   tells the bridge with a kind:"binding" frame. The socket is never
 *   reconnected for it, because that would drop a run's in-flight commands.
 *
 * isValidProfileId is kept identical to profiledir.ValidProfileID (Go) and
 * MonoCaptureProfile.isValidProfileId: the id becomes a directory name on
 * the other side.
 */

(function (root) {
  "use strict";

  const INSTANCE_KEY = "bridgeInstanceId";
  const PROFILE_KEY = "boundProfile";
  const LABEL_KEY = "browserLabel";
  const KEYS = [INSTANCE_KEY, PROFILE_KEY, LABEL_KEY];
  const MAX_LABEL = 60;
  const INSTANCE_RE = /^[A-Za-z0-9-]{8,64}$/;

  function isValidProfileId(id) {
    const value = typeof id === "string" ? id.trim() : "";
    if (!value) return false;
    if (value.includes("/") || value.includes("\\")) return false;
    if (value.includes("..")) return false;
    return true;
  }

  function browserName(ua) {
    const s = String(ua || "");
    if (/Edg\//.test(s)) return "Edge";
    if (/OPR\//.test(s)) return "Opera";
    if (/Chrome\//.test(s)) return "Chrome";
    return "Browser";
  }

  function cleanLabel(s) {
    const v = String(s || "").replace(/\s+/g, " ").trim();
    return v.length > MAX_LABEL ? v.slice(0, MAX_LABEL) : v;
  }

  function defaultLabel(ua, instance) {
    return `${browserName(ua)} ${String(instance || "").slice(0, 4)}`.trim();
  }

  /**
   * load reads this browser's binding. opts: { crypto, userAgent, create }.
   * Only the worker passes create:true; the side panel reads what the worker
   * made (and shows no id until it has).
   */
  async function load(storage, opts) {
    const o = opts || {};
    let stored = {};
    try {
      stored = (await storage.get(KEYS)) || {};
    } catch {
      // No storage: an unbound browser with a fresh id is still routable.
    }
    let instance = typeof stored[INSTANCE_KEY] === "string" ? stored[INSTANCE_KEY] : "";
    if (!INSTANCE_RE.test(instance)) {
      instance = "";
      if (o.create && o.crypto && typeof o.crypto.randomUUID === "function") {
        instance = o.crypto.randomUUID();
        try {
          await storage.set({ [INSTANCE_KEY]: instance });
        } catch {
          // Kept for this worker's life; the next start mints another.
        }
      }
    }
    const profile = isValidProfileId(stored[PROFILE_KEY]) ? stored[PROFILE_KEY].trim() : "";
    const label = cleanLabel(stored[LABEL_KEY]) || defaultLabel(o.userAgent, instance);
    return { instance, profile, label };
  }

  /** authFields is what the worker adds to its auth frame. */
  function authFields(state, version) {
    const out = { instance: state.instance, label: state.label, version: String(version || "") };
    if (state.profile) out.profile = state.profile;
    return out;
  }

  /** setBinding stores {profile, label?}. "" profile unbinds. */
  async function setBinding(storage, params) {
    const p = params || {};
    const raw = typeof p.profile === "string" ? p.profile : "";
    if (raw.trim() && !isValidProfileId(raw)) throw new Error("invalid profile id");
    const update = { [PROFILE_KEY]: raw.trim() };
    if (typeof p.label === "string") update[LABEL_KEY] = cleanLabel(p.label);
    await storage.set(update);
    return { profile: update[PROFILE_KEY], label: update[LABEL_KEY] };
  }

  function bindingFrame(state) {
    return { kind: "binding", profile: state.profile || "", label: state.label || "" };
  }

  function isBindingChange(changes, area) {
    return area === "local" && !!changes && (PROFILE_KEY in changes || LABEL_KEY in changes);
  }

  /** options is what the side panel's select offers. */
  function options(profiles, bound) {
    const list = [{ id: "", name: "Any profile (default browser)", selected: !bound }];
    let found = false;
    for (const p of Array.isArray(profiles) ? profiles : []) {
      if (!p || !isValidProfileId(p.id)) continue;
      const selected = p.id === bound;
      found = found || selected;
      list.push({ id: p.id, name: p.name || p.id, selected });
    }
    if (bound && !found) list.push({ id: bound, name: `${bound} (no longer exists)`, selected: true });
    return list;
  }

  root.MonoBrowserBinding = {
    INSTANCE_KEY, PROFILE_KEY, LABEL_KEY, MAX_LABEL,
    isValidProfileId, browserName, cleanLabel, defaultLabel,
    load, authFields, setBinding, bindingFrame, isBindingChange, options,
  };
})(globalThis);
