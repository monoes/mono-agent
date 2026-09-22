/**
 * MonoAgent Bridge — YouTube video facts (the "Save video summary" capture)
 *
 * adapters/youtube.js turns a watch page into readable.md from what the DOM
 * rendered, and only has a transcript when the viewer happened to open the
 * transcript panel. This file is the other half: the video's own record —
 * title, channel, publish date, duration, description, chapters — read from
 * the player response the page already holds, plus the transcript fetched
 * from the page's own caption tracks (youtube_transcript.js).
 *
 * Everything here is pure except readPageState, which is serialized into the
 * page's MAIN world by chrome.scripting.executeScript (the isolated world
 * cannot see window.ytInitialPlayerResponse), and so must not close over
 * anything in this file.
 *
 * The SPA problem: YouTube navigates between videos without a page load, so
 * window.ytInitialPlayerResponse is whatever the FIRST video of the tab was.
 * Every candidate is therefore checked against the video id in the address
 * bar, and when none matches the watch page is fetched again and parsed
 * (extractJsonAssignment) — a stale record is worse than none.
 */

(function (root) {
  "use strict";

  const VIDEO_ID = /^[A-Za-z0-9_-]{6,32}$/;
  const HOSTS = /^(www\.|m\.|music\.)?youtube\.com$/;

  /** videoIdOf returns the video id of a watch/shorts/live/youtu.be URL, or "". */
  function videoIdOf(url) {
    let u;
    try {
      u = new URL(url);
    } catch {
      return "";
    }
    const host = u.hostname.toLowerCase();
    let id = "";
    if (host === "youtu.be") id = u.pathname.slice(1).split("/")[0];
    else if (HOSTS.test(host)) {
      id = u.pathname === "/watch" ? u.searchParams.get("v") || "" : (u.pathname.match(/^\/(?:shorts|live)\/([^/?#]+)/) || [])[1] || "";
    }
    return VIDEO_ID.test(id || "") ? id : "";
  }

  const isVideoUrl = (url) => !!videoIdOf(url);

  /** The patterns a context menu's documentUrlPatterns takes for "is a video". */
  const MENU_PATTERNS = [
    "*://*.youtube.com/watch*",
    "*://*.youtube.com/shorts/*",
    "*://*.youtube.com/live/*",
    "*://youtu.be/*",
  ];

  /**
   * readPageState runs in the page's MAIN world and returns the small slice
   * of YouTube's globals this file reads. Self-contained on purpose — see the
   * file comment. ytInitialData is megabytes, so only the chapter-bearing
   * parts and the id it describes come back.
   */
  function readPageState() {
    const trimPlayer = (pr) => {
      if (!pr || typeof pr !== "object") return null;
      return { videoDetails: pr.videoDetails || null, microformat: pr.microformat || null, captions: pr.captions || null };
    };
    const w = window;
    const players = [];
    try {
      const mp = document.querySelector("#movie_player");
      if (mp && typeof mp.getPlayerResponse === "function") players.push(trimPlayer(mp.getPlayerResponse()));
    } catch {
      // The player API is a courtesy; the globals below are the fallback.
    }
    players.push(trimPlayer(w.ytInitialPlayerResponse));
    const d = w.ytInitialData || null;
    let data = null;
    if (d) {
      const panels = (d.engagementPanels || []).filter((p) => {
        const r = p && p.engagementPanelSectionListRenderer;
        return r && /macro-markers|chapters/i.test(String(r.panelIdentifier || r.targetId || ""));
      });
      let markersMap = null;
      try {
        markersMap =
          d.playerOverlays.playerOverlayRenderer.decoratedPlayerBarRenderer.decoratedPlayerBarRenderer.playerBar
            .multiMarkersPlayerBarRenderer.markersMap || null;
      } catch {
        // No chapter markers on the player bar.
      }
      data = {
        currentVideoEndpoint: d.currentVideoEndpoint || null,
        // Re-nested in the shape chaptersFromData reads, so a record parsed
        // from a refetched page (the whole thing) and this trimmed one are
        // read by the same code.
        playerOverlays: markersMap
          ? { playerOverlayRenderer: { decoratedPlayerBarRenderer: { decoratedPlayerBarRenderer: { playerBar: { multiMarkersPlayerBarRenderer: { markersMap } } } } } }
          : null,
        engagementPanels: panels,
      };
    }
    return { href: location.href, players: players.filter(Boolean), data };
  }

  /**
   * extractJsonAssignment pulls `name = {...};` out of a watch page's HTML by
   * matching braces, string-aware, rather than with a regex that a `};`
   * inside a description would cut short.
   */
  function extractJsonAssignment(html, name) {
    const text = String(html || "");
    const re = new RegExp(`(?:var\\s+|window\\[["']|window\\.)?${name}(?:["']\\])?\\s*=\\s*\\{`, "g");
    const m = re.exec(text);
    if (!m) return null;
    const start = m.index + m[0].length - 1;
    let depth = 0;
    let inString = false;
    let quote = "";
    for (let i = start; i < text.length; i++) {
      const c = text[i];
      if (inString) {
        if (c === "\\") i++;
        else if (c === quote) inString = false;
        continue;
      }
      if (c === '"' || c === "'") {
        inString = true;
        quote = c;
      } else if (c === "{") depth++;
      else if (c === "}") {
        depth--;
        if (depth === 0) {
          try {
            return JSON.parse(text.slice(start, i + 1));
          } catch {
            return null;
          }
        }
      }
    }
    return null;
  }

  const playerVideoId = (p) => (p && p.videoDetails && p.videoDetails.videoId) || "";
  const dataVideoId = (d) =>
    (d && d.currentVideoEndpoint && d.currentVideoEndpoint.watchEndpoint && d.currentVideoEndpoint.watchEndpoint.videoId) || "";

  /**
   * resolveState picks the player response and initial data that describe
   * `id`. Returns { player, data, fresh } where fresh=false means nothing on
   * the page matched and the caller must refetch.
   */
  function resolveState(state, id) {
    const s = state || {};
    const player = (s.players || []).find((p) => playerVideoId(p) === id) || null;
    const data = s.data && dataVideoId(s.data) === id ? s.data : null;
    return { player, data, fresh: !!player };
  }

  const txt = (v) => {
    if (!v) return "";
    if (typeof v === "string") return v;
    if (v.simpleText) return v.simpleText;
    if (Array.isArray(v.runs)) return v.runs.map((r) => r.text || "").join("");
    return "";
  };

  /** clock renders seconds as m:ss or h:mm:ss. */
  function clock(seconds) {
    const s = Math.max(0, Math.floor(Number(seconds) || 0));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const ss = String(s % 60).padStart(2, "0");
    return h ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
  }

  /** parseClock turns "1:02:30" into 3750, or null. */
  function parseClock(stamp) {
    const parts = String(stamp).trim().split(":");
    if (parts.length < 2 || parts.length > 3 || parts.some((p) => !/^\d{1,2}$/.test(p))) return null;
    return parts.map(Number).reduce((t, p) => t * 60 + p, 0);
  }

  function chaptersFromData(data) {
    if (!data) return [];
    const out = [];
    try {
      const bar = data.playerOverlays.playerOverlayRenderer.decoratedPlayerBarRenderer.decoratedPlayerBarRenderer.playerBar;
      for (const entry of (bar.multiMarkersPlayerBarRenderer && bar.multiMarkersPlayerBarRenderer.markersMap) || []) {
        for (const ch of (entry.value && entry.value.chapters) || []) {
          const r = ch.chapterRenderer;
          if (r) out.push({ start: Math.round((r.timeRangeStartMillis || 0) / 1000), title: txt(r.title) });
        }
        if (out.length) return out;
      }
    } catch {
      // No player bar markers; try the chapters panel.
    }
    const walk = (node) => {
      if (!node || typeof node !== "object") return;
      if (node.macroMarkersListItemRenderer) {
        const r = node.macroMarkersListItemRenderer;
        const at =
          (r.onTap && r.onTap.watchEndpoint && r.onTap.watchEndpoint.startTimeSeconds) ?? parseClock(txt(r.timeDescription));
        if (at !== null && at !== undefined) out.push({ start: Number(at), title: txt(r.title) });
        return;
      }
      for (const v of Array.isArray(node) ? node : Object.values(node)) walk(v);
    };
    walk(data.engagementPanels || []);
    return out;
  }

  /**
   * chaptersFromDescription reads "0:00 Intro" lines, the convention YouTube
   * itself turns into chapters: at least two, and the first at 0:00.
   */
  function chaptersFromDescription(description) {
    const out = [];
    for (const line of String(description || "").split("\n")) {
      const m = line.trim().match(/^[([]?((?:\d{1,2}:)?\d{1,2}:\d{2})[)\]]?\s*[-–—:|]?\s*(.+)$/);
      if (!m) continue;
      const at = parseClock(m[1]);
      if (at !== null && m[2].trim()) out.push({ start: at, title: m[2].trim() });
    }
    return out.length >= 2 && out[0].start === 0 ? out : [];
  }

  /** videoFields is the video's record, from a player response and initial data. */
  function videoFields(player, data) {
    const vd = (player && player.videoDetails) || {};
    const mf = (player && player.microformat && player.microformat.playerMicroformatRenderer) || {};
    const durationSeconds = Number(vd.lengthSeconds || mf.lengthSeconds || 0) || null;
    const description = vd.shortDescription || txt(mf.description) || "";
    let chapters = chaptersFromData(data);
    let chapterSource = chapters.length ? "youtube" : null;
    if (!chapters.length) {
      chapters = chaptersFromDescription(description);
      if (chapters.length) chapterSource = "description";
    }
    const channelUrl = mf.ownerProfileUrl || (vd.channelId ? `https://www.youtube.com/channel/${vd.channelId}` : null);
    return {
      videoId: vd.videoId || null,
      title: vd.title || txt(mf.title) || null,
      channel: vd.author || mf.ownerChannelName || null,
      channelId: vd.channelId || mf.externalChannelId || null,
      channelUrl: channelUrl || null,
      publishDate: mf.publishDate || mf.uploadDate || null,
      durationSeconds,
      duration: durationSeconds ? clock(durationSeconds) : null,
      viewCount: vd.viewCount ? Number(vd.viewCount) : null,
      category: mf.category || null,
      isLive: !!(vd.isLiveContent || vd.isLive),
      keywords: Array.isArray(vd.keywords) ? vd.keywords.slice(0, 30) : [],
      description,
      chapters,
      chapterSource,
    };
  }

  root.MonoYouTubeVideo = {
    videoIdOf, isVideoUrl, MENU_PATTERNS, readPageState, extractJsonAssignment, resolveState,
    videoFields, chaptersFromData, chaptersFromDescription, clock, parseClock, txt,
  };
})(globalThis);
