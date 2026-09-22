/**
 * MonoAgent Bridge — what the side panel shows about the page and the profile
 *
 * The popup never needed this file. It was built fresh on every click, so
 * "this page" was always the tab that had just been clicked on and nothing
 * could go stale. A side panel is the opposite: one document per window,
 * open for as long as the person likes, while they move from tab to tab
 * underneath it. Everything it says about "this page" has to follow the
 * active tab, and the worst bug it can have is a confident sentence about
 * the tab that was left behind.
 *
 * So the judgement is kept here, pure and away from the DOM, next to
 * sidepanel_status.js: a tab in, what to draw out; a profile list in, what
 * the header says out. sidepanel.js only paints the answers.
 */

(function (root) {
  "use strict";

  /** Schemes the browser keeps closed to extensions, whatever the page is. */
  const CLOSED = /^(chrome|edge|about|chrome-extension|extension|devtools|view-source|chrome-search|edge-extension|brave|opera|vivaldi):/i;

  /** The part of a URL that says which page it is — the fragment is not. */
  function identityUrl(raw) {
    const s = String(raw || "").trim();
    const hash = s.indexOf("#");
    return hash === -1 ? s : s.slice(0, hash);
  }

  /** hostOf is the site as a person would say it: no scheme, no "www.". */
  function hostOf(url) {
    const match = /^[a-z]+:\/\/([^/?#:]+)/i.exec(String(url || ""));
    return match ? match[1].replace(/^www\./i, "").toLowerCase() : "";
  }

  /** fileName is the last path segment of a file:// URL, decoded. */
  function fileName(url) {
    const path = String(url || "").replace(/[?#].*$/, "");
    const last = path.slice(path.lastIndexOf("/") + 1);
    try {
      return decodeURIComponent(last);
    } catch {
      return last;
    }
  }

  /**
   * describeTab turns a chrome.tabs.Tab into the capture card's head: what
   * the page is called, where it is from, and whether it can be saved at
   * all. `key` identifies the page (tab and URL, fragment dropped), so an
   * answer that arrives after the person has moved on can be recognised as
   * being about somewhere else and thrown away.
   */
  function describeTab(tab) {
    if (!tab || !tab.id) {
      return {
        key: "",
        tabId: 0,
        url: "",
        title: "No page in this window",
        host: "",
        favicon: "",
        capturable: false,
        loading: false,
        groupId: -1,
        why: "Open a web page and it will show up here.",
      };
    }

    const url = String(tab.pendingUrl || tab.url || "");
    const loading = tab.status === "loading";
    const favicon = /^(https?:|data:image\/)/i.test(tab.favIconUrl || "") ? tab.favIconUrl : "";
    const groupId = typeof tab.groupId === "number" ? tab.groupId : -1;
    const base = {
      key: `${tab.id}|${identityUrl(url)}`,
      tabId: tab.id,
      url,
      favicon,
      loading,
      groupId,
    };

    if (/^https?:\/\//i.test(url)) {
      const host = hostOf(url);
      return Object.assign(base, {
        title: String(tab.title || "").trim() || host || url,
        host,
        capturable: true,
        why: "",
      });
    }

    if (/^file:\/\//i.test(url)) {
      return Object.assign(base, {
        title: String(tab.title || "").trim() || fileName(url) || url,
        host: "File on this computer",
        capturable: true,
        why: "",
      });
    }

    // New-tab pages, settings, the extension store, this panel opened as a
    // tab. Saying so beats a button that fails when pressed.
    const closed = !url || CLOSED.test(url);
    return Object.assign(base, {
      title: String(tab.title || "").trim() || (url ? url : "New tab"),
      host: closed ? "Browser page" : hostOf(url),
      capturable: false,
      why: "Browser pages are closed to extensions. Switch to a web page to save it.",
    });
  }

  /**
   * tabChange says what a chrome.tabs.onUpdated change means for the panel.
   * `redraw`: something on the card changed (title, icon, loading state).
   * `recheck`: the page itself may be a different one, so anything the
   * panel knows about it — "already saved", highlights — has to be asked
   * again. A title settling after load is not a new page; a URL is, and so
   * is a load finishing (the URL a redirect chain ends on).
   */
  function tabChange(change) {
    const c = change || {};
    const recheck = !!c.url || c.status === "complete";
    const redraw =
      recheck || !!c.title || c.favIconUrl !== undefined || !!c.status || c.groupId !== undefined;
    return { redraw, recheck };
  }

  /**
   * avatarHue gives each profile a steady colour of its own, so "which
   * brain is this going into" is answered at a glance and the same
   * profile looks the same in every window. Twelve hues, a stable hash:
   * the colour is an identity, not a decoration, and must not reshuffle.
   */
  function avatarHue(id) {
    const s = String(id || "");
    if (!s) return -1;
    let h = 0;
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return (h % 12) * 30;
  }

  /** initialOf is the letter drawn in a profile's avatar. */
  function initialOf(name) {
    const m = /[\p{L}\p{N}]/u.exec(String(name || ""));
    return m ? m[0].toUpperCase() : "?";
  }

  const SHARED_INBOX = "Shared inbox";
  const LAST_PROFILE = "your last profile";

  /**
   * describeProfiles turns capture_form_state's profile fields into the
   * header's switcher. The picker is the one control the panel shows on
   * every page, so each edge case gets a sentence rather than an empty or
   * silently-wrong control:
   *
   *   - profiles to choose from (live, or the cached list while the bridge
   *     is down): a switcher, with a note when the list is the old one;
   *   - the remembered profile was deleted: switched to the fallback the
   *     worker picked, and said so — saving into a different brain than the
   *     one last chosen is the one outcome worth interrupting for;
   *   - the bridge answered and there are no profiles: not a picker at all,
   *     the shared inbox, and the command that makes one;
   *   - the bridge has never answered: the remembered choice if there is
   *     one, otherwise the shared inbox, and a note saying the list comes
   *     with the bridge.
   */
  function describeProfiles(state) {
    const s = state || {};
    const command = (root.MonoPanelStatus && root.MonoPanelStatus.PROFILE_COMMAND) || "";
    const profiles = Array.isArray(s.profiles) ? s.profiles : [];
    const chosen = String(s.profile || "");
    const offline = !!s.profilesOffline;

    const person = (p) => ({
      id: p.id,
      name: p.name || p.id,
      isDefault: !!p.default,
      initial: initialOf(p.name || p.id),
      hue: avatarHue(p.id),
    });
    const shared = {
      id: "",
      name: SHARED_INBOX,
      isDefault: false,
      initial: "",
      hue: -1,
      hint: "No profile — as captures were before profiles",
    };

    if (profiles.length) {
      const options = distinctHues(profiles.map(person)).concat([shared]);
      const current = options.find((o) => o.id === chosen) || shared;
      let note = null;
      if (s.profileChanged && s.profileReason) {
        note = { tone: "warn", text: sentence(s.profileReason) };
      } else if (offline) {
        note = { tone: "idle", text: "Not connected to the bridge, so this is the last list it gave." };
      }
      return { interactive: true, current, options, note, saveLabel: saveLabel(current) };
    }

    if (!offline) {
      return {
        interactive: false,
        current: shared,
        options: [shared],
        note: { tone: "idle", text: "No profiles yet, so captures go to the shared inbox. To add one, run:", command },
        saveLabel: saveLabel(shared),
      };
    }

    if (chosen) {
      // With no list there may be no name either, only the stored id — and
      // profile ids are usually UUIDs, which are not something to put in a
      // header or on a button. A readable id (a hand-made "work") is shown
      // as itself.
      const named = s.profileName && s.profileName !== chosen ? s.profileName : "";
      const machine = !named && /^[0-9a-f]{8}-[0-9a-f-]{8,}$/i.test(chosen);
      const current = person({ id: chosen, name: named || (machine ? LAST_PROFILE : chosen) });
      if (machine) current.initial = "";
      return {
        interactive: false,
        current,
        options: [current],
        note: { tone: "idle", text: "Saving into the profile you chose last. The full list comes back with the bridge." },
        saveLabel: saveLabel(current),
      };
    }

    return {
      interactive: false,
      current: shared,
      options: [shared],
      note: { tone: "idle", text: "Profiles load once the bridge is running. Until then, captures go to the shared inbox." },
      saveLabel: saveLabel(shared),
    };
  }

  /**
   * distinctHues keeps two profiles in one list from sharing a colour,
   * which would defeat the point of colouring them. Each keeps its own
   * hashed hue unless an earlier row already has it, then steps round the
   * wheel to the next free one — so the colours depend only on the list,
   * and every window showing the same list shows the same colours.
   */
  function distinctHues(options) {
    const taken = new Set();
    for (const option of options) {
      let hue = option.hue;
      for (let step = 0; step < 12 && taken.has(hue); step++) hue = (hue + 150) % 360;
      option.hue = hue;
      taken.add(hue);
    }
    return options;
  }

  function saveLabel(current) {
    return current && current.id ? `Save to ${current.name}` : "Save this page";
  }

  /** A name drawn as a heading starts with a capital; in a sentence it may not. */
  function displayName(name) {
    const n = String(name || "");
    return n === LAST_PROFILE ? n.charAt(0).toUpperCase() + n.slice(1) : n;
  }

  /** sentence capitalises a worker reason and ends it, for a note line. */
  function sentence(text) {
    const t = String(text || "").trim();
    if (!t) return "";
    const first = t.charAt(0).toUpperCase() + t.slice(1);
    return /[.!?]$/.test(first) ? first : `${first}.`;
  }

  root.MonoPanelView = {
    describeTab,
    tabChange,
    describeProfiles,
    avatarHue,
    initialOf,
    identityUrl,
    hostOf,
    displayName,
    SHARED_INBOX,
    LAST_PROFILE,
  };
})(globalThis);
