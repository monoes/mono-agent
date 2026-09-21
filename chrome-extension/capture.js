/**
 * MonoAgent Bridge — page_capture (CLIP-01, CLIP-03, CLIP-08)
 *
 * The service-worker half of a capture. Drives the page half
 * (capture_page.js) and the four CDP calls that produce the fidelity
 * artifacts, then packs the result into the capture envelope the Go bridge
 * reads:
 *
 *   { id, success: true, data: { meta, artifacts: [...], warnings: [...] } }
 *
 * Two rules shape everything below. A capture never fails as a whole because
 * one artifact failed — a missing PDF is a warning, not a lost page. And a
 * capture never kills the WebSocket: artifacts over the per-artifact cap are
 * dropped with a warning, and what remains is split across frames rather than
 * sent as one oversized one (the Go server hangs up above 32 MiB).
 *
 * Chrome APIs reach this file through an injected `ctx`, so the whole flow is
 * exercised in node by capture.test.mjs with fakes.
 */

(function (root) {
  "use strict";

  const MB = 1024 * 1024;
  // Per-artifact cap, on raw (pre-base64) bytes. Media-heavy MHTML regularly
  // lands at 20-50MB (TRU-04).
  const DEFAULT_MAX_ARTIFACT_BYTES = 25 * MB;
  // Per-WebSocket-frame budget, on base64 bytes. The Go server's read limit
  // is 32 MiB and exceeding it closes the connection, so stay well under.
  const DEFAULT_MAX_MESSAGE_BYTES = 8 * MB;
  const DEFAULT_FORMATS = ["mhtml", "readable", "screenshot", "tables"];
  // CLIP-10/12: the registry loads before the adapters, which self-register
  // on load, and the order below is the order they get first refusal in —
  // pdf.js last, because it is the catch-all for any tab that turns out to
  // be a file rather than a page.
  const PAGE_SCRIPTS = [
    "markdown.js", "readable.js", "capture_meta.js", "tables.js",
    "adapters/util.js", "adapters/registry.js",
    "adapters/youtube.js", "adapters/arxiv.js", "adapters/twitter.js",
    "adapters/github.js", "adapters/chat.js", "adapters/pdf.js",
    "adapters/extract_items.js",
    "capture_page.js",
  ];
  // Chrome refuses to rasterize past this; a taller page is captured down to
  // the limit and says so in a warning rather than failing.
  const MAX_SCREENSHOT_PX = 16384;

  function utf8ToBase64(text) {
    const bytes = new TextEncoder().encode(text);
    let binary = "";
    // Chunked: String.fromCharCode(...bytes) blows the stack somewhere around
    // a hundred thousand arguments, and these strings run to tens of MB.
    for (let i = 0; i < bytes.length; i += 0x8000) {
      binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    }
    return btoa(binary);
  }

  /** base64Bytes reports the decoded size of a base64 string, without decoding it. */
  function base64Bytes(b64) {
    if (!b64) return 0;
    const padding = b64.endsWith("==") ? 2 : b64.endsWith("=") ? 1 : 0;
    return Math.floor((b64.length * 3) / 4) - padding;
  }

  const mb = (bytes) => `${(bytes / MB).toFixed(1)}MB`;

  function escapeHtml(s) {
    return String(s || "").replace(/[&<>"']/g, (c) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[c]));
  }

  /**
   * applySizeCaps drops oversized artifacts and explains each one. An
   * artifact the caller never sees is strictly better than a connection the
   * caller loses (TRU-04).
   */
  function applySizeCaps(artifacts, maxArtifactBytes) {
    const cap = maxArtifactBytes || DEFAULT_MAX_ARTIFACT_BYTES;
    const kept = [];
    const warnings = [];
    for (const artifact of artifacts) {
      if (artifact.rawBytes > cap) {
        warnings.push(`${artifact.name} skipped: ${mb(artifact.rawBytes)} exceeds the ${mb(cap)} artifact cap`);
        continue;
      }
      kept.push(artifact);
    }
    return { kept, warnings };
  }

  /**
   * planMessages turns an envelope into the exact WebSocket messages to send,
   * in order. Small captures — the overwhelming majority — are one message
   * with every artifact inline. When the frame budget cannot hold an
   * artifact, that artifact is streamed as chunks first and the envelope,
   * which is always last, references them:
   *
   *   chunk:    { id, success: true, data: { chunk: { index, total, of }, bytes } }
   *   envelope: { id, success: true, data: { meta, artifacts, warnings, final: true } }
   *
   * so the receiver buffers on `data.chunk` and completes on `data.final`.
   */
  function planMessages(id, meta, artifacts, warnings, opts) {
    const o = Object.assign({ maxMessageBytes: DEFAULT_MAX_MESSAGE_BYTES, type: null }, opts || {});
    const limit = Math.max(64 * 1024, o.maxMessageBytes);
    const messages = [];
    const listed = [];
    // Reserve room for meta and the JSON scaffolding around the payload.
    let budget = limit - JSON.stringify({ meta, warnings }).length - 512;

    for (const artifact of artifacts) {
      const entry = { name: artifact.name, encoding: "base64", rawBytes: artifact.rawBytes };
      if (artifact.bytes.length <= budget) {
        budget -= artifact.bytes.length;
        listed.push(Object.assign(entry, { bytes: artifact.bytes }));
        continue;
      }
      const total = Math.max(1, Math.ceil(artifact.bytes.length / limit));
      for (let index = 0; index < total; index++) {
        const slice = artifact.bytes.slice(index * limit, (index + 1) * limit);
        messages.push(withType(o.type, {
          id,
          success: true,
          data: { chunk: { index, total, of: artifact.name }, bytes: slice },
        }));
      }
      listed.push(Object.assign(entry, { chunked: true, chunks: total }));
    }

    messages.push(withType(o.type, {
      id,
      success: true,
      data: { meta, artifacts: listed, warnings, final: true },
    }));
    return messages;
  }

  function withType(type, message) {
    return type ? Object.assign({ type }, message) : message;
  }

  // --- CDP artifacts ------------------------------------------------------

  async function captureMhtml(ctx, tabId) {
    const result = await ctx.cdp(tabId, "Page.captureSnapshot", { format: "mhtml" });
    const text = (result && result.data) || "";
    if (!text) throw new Error("Page.captureSnapshot returned nothing");
    const bytes = utf8ToBase64(text);
    return { name: "page.mhtml", encoding: "base64", bytes, rawBytes: base64Bytes(bytes) };
  }

  async function capturePdf(ctx, tabId, meta) {
    // CLIP-03: the footer is what makes a printed archive citable — where it
    // came from and when, on every page.
    const footer =
      `<div style="font-size:8px;font-family:sans-serif;color:#555;width:100%;padding:0 10mm;` +
      `display:flex;justify-content:space-between;align-items:center;">` +
      `<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:68%;">${escapeHtml(meta.url)}</span>` +
      `<span>${escapeHtml(meta.capturedAt)} &middot; <span class="pageNumber"></span>/<span class="totalPages"></span></span>` +
      `</div>`;
    const result = await ctx.cdp(tabId, "Page.printToPDF", {
      printBackground: true,
      displayHeaderFooter: true,
      headerTemplate: "<span></span>",
      footerTemplate: footer,
      marginTop: 0.4,
      marginBottom: 0.6,
      marginLeft: 0.4,
      marginRight: 0.4,
    });
    const bytes = (result && result.data) || "";
    if (!bytes) throw new Error("Page.printToPDF returned nothing");
    return { name: "page.pdf", encoding: "base64", bytes, rawBytes: base64Bytes(bytes) };
  }

  async function captureScreenshot(ctx, tabId, warnings) {
    let clip = null;
    try {
      const metrics = await ctx.cdp(tabId, "Page.getLayoutMetrics", {});
      const size = (metrics && (metrics.cssContentSize || metrics.contentSize)) || null;
      if (size && size.width && size.height) {
        const height = Math.min(Math.ceil(size.height), MAX_SCREENSHOT_PX);
        if (Math.ceil(size.height) > MAX_SCREENSHOT_PX) {
          warnings.push(`screenshot.png truncated to ${MAX_SCREENSHOT_PX}px of ${Math.ceil(size.height)}px page height`);
        }
        clip = { x: 0, y: 0, width: Math.min(Math.ceil(size.width), MAX_SCREENSHOT_PX), height, scale: 1 };
      }
    } catch {
      // No layout metrics — fall back to the viewport-sized capture below.
    }
    const params = { format: "png", captureBeyondViewport: true };
    if (clip) params.clip = clip;
    const result = await ctx.cdp(tabId, "Page.captureScreenshot", params);
    const bytes = (result && result.data) || "";
    if (!bytes) throw new Error("Page.captureScreenshot returned nothing");
    return { name: "screenshot.png", encoding: "base64", bytes, rawBytes: base64Bytes(bytes) };
  }

  // --- the capture itself -------------------------------------------------

  /**
   * pageCapture runs one capture end to end and returns { meta, artifacts,
   * warnings }. It throws only when there is nothing to send at all — no tab,
   * or a page that cannot be read.
   */
  async function pageCapture(params, ctx) {
    const p = params || {};
    const formats = (Array.isArray(p.formats) && p.formats.length ? p.formats : DEFAULT_FORMATS).map((f) =>
      String(f).toLowerCase()
    );
    const warnings = [];
    const tabId = await ctx.resolveTabId(p);
    if (!tabId) throw new Error("no tab to capture");

    await ctx.inject(tabId, PAGE_SCRIPTS);

    let prepared = null;
    try {
      prepared = await ctx.callPage(tabId, "prepare", [p.prepare || {}]);
    } catch (err) {
      warnings.push(`page preparation skipped: ${err.message}`);
    }

    let page;
    try {
      page = await ctx.callPage(tabId, "extract", [{
        selection: !!p.selection,
        tables: formats.includes("tables"),
        note: p.note || null,
        tags: p.tags || [],
        collection: p.collection || null,
      }]);
    } catch (err) {
      await safeRestore(ctx, tabId);
      throw new Error(`readable extraction failed: ${err.message}`);
    }

    const meta = page.meta;
    meta.contentHash = `sha256:${await root.MonoMeta.sha256Hex(page.text || "")}`;
    if (typeof p.httpStatus === "number") meta.httpStatus = p.httpStatus;
    if (p.collection) meta.collection = p.collection;
    if (prepared) meta.preparation = prepared;
    if (p.selection && !page.selectionFound) warnings.push("selection requested but nothing was selected; captured the whole page");

    const artifacts = [];
    if (formats.includes("readable")) {
      const bytes = utf8ToBase64(page.markdown || "");
      artifacts.push({ name: "readable.md", encoding: "base64", bytes, rawBytes: base64Bytes(bytes) });
    }

    // CLIP-11: table-N.csv rides alongside the readable text. The names are
    // outside the plan's four, which the Go receiver allows — its
    // ValidArtifactName is a charset whitelist, not a fixed set of names.
    for (const table of (formats.includes("tables") && page.tables) || []) {
      const bytes = utf8ToBase64(table.csv || "");
      artifacts.push({ name: table.name, encoding: "base64", bytes, rawBytes: base64Bytes(bytes) });
    }

    // CLIP-10/12: whatever the per-site adapter or a point-and-extract run
    // produced (items.csv, items.json). The page side has already checked
    // each name against the Go receiver's rules.
    for (const extra of page.artifacts || []) {
      const bytes = utf8ToBase64(extra.text || "");
      artifacts.push({ name: extra.name, encoding: "base64", bytes, rawBytes: base64Bytes(bytes) });
    }
    if (page.adapter) meta.adapter = page.adapter;
    warnings.push(...(page.warnings || []));

    const needsCdp = ["mhtml", "pdf", "screenshot"].some((f) => formats.includes(f));
    if (needsCdp) {
      try {
        await ctx.attach(tabId);
        try {
          await ctx.cdp(tabId, "Page.enable", {});
        } catch {
          // Page.enable is a courtesy; the three capture commands work without it.
        }
        const jobs = [
          ["screenshot", () => captureScreenshot(ctx, tabId, warnings)],
          ["pdf", () => capturePdf(ctx, tabId, meta)],
          ["mhtml", () => captureMhtml(ctx, tabId)],
        ];
        for (const [format, run] of jobs) {
          if (!formats.includes(format)) continue;
          try {
            artifacts.push(await run());
          } catch (err) {
            warnings.push(`${format} skipped: ${err.message}`);
          }
        }
      } catch (err) {
        warnings.push(`debugger unavailable, captured readable text only: ${err.message}`);
      } finally {
        await ctx.detach(tabId).catch(() => {});
      }
    }

    await safeRestore(ctx, tabId);

    const capped = applySizeCaps(artifacts, p.maxArtifactBytes);
    return { meta, artifacts: capped.kept, warnings: warnings.concat(capped.warnings) };
  }

  async function safeRestore(ctx, tabId) {
    try {
      await ctx.callPage(tabId, "restore", []);
    } catch {
      // The tab may have navigated away mid-capture; nothing left to restore.
    }
  }

  // --- CLIP-08: nothing captured is ever silently lost ---------------------

  const QUEUE_KEY = "captureQueue";
  const QUEUE_MAX_ENTRIES = 20;
  // chrome.storage.local is a ~10MB bucket; a big MHTML does not belong in it.
  const QUEUE_MAX_ENTRY_BYTES = 4 * MB;

  async function queueCapture(storage, envelope) {
    const size = JSON.stringify(envelope).length;
    if (size > QUEUE_MAX_ENTRY_BYTES) {
      return { queued: false, reason: `capture too large to queue offline (${mb(size)})` };
    }
    const current = (await storage.get(QUEUE_KEY))[QUEUE_KEY] || [];
    const next = current.concat([{ queuedAt: new Date().toISOString(), envelope }]).slice(-QUEUE_MAX_ENTRIES);
    await storage.set({ [QUEUE_KEY]: next });
    return { queued: true, depth: next.length };
  }

  async function flushQueue(storage, send) {
    const queued = (await storage.get(QUEUE_KEY))[QUEUE_KEY] || [];
    if (!queued.length) return { flushed: 0 };
    await storage.set({ [QUEUE_KEY]: [] });
    let flushed = 0;
    for (const entry of queued) {
      try {
        send(entry.envelope);
        flushed++;
      } catch {
        // Connection died mid-flush — put the rest back and try again later.
        const left = queued.slice(flushed);
        await storage.set({ [QUEUE_KEY]: left });
        break;
      }
    }
    return { flushed };
  }

  root.MonoCapture = {
    pageCapture, planMessages, applySizeCaps, queueCapture, flushQueue,
    utf8ToBase64, base64Bytes, escapeHtml,
    DEFAULT_MAX_ARTIFACT_BYTES, DEFAULT_MAX_MESSAGE_BYTES, DEFAULT_FORMATS, PAGE_SCRIPTS,
  };
})(globalThis);
