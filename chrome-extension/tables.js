/**
 * MonoAgent Bridge — tables (CLIP-11)
 *
 * Every data table on a captured page, expanded into a rectangle and written
 * out as RFC4180 CSV. A saved article keeps its prose in readable.md; the
 * numbers that were in a <table> only stay usable if they come out as data,
 * so each qualifying table lands as its own artifact — table-1.csv,
 * table-2.csv, …
 *
 * Pure functions over the same node tree readable.js and capture_meta.js
 * use, so every rule here is exercised from an HTML string (tables.test.mjs)
 * with no browser.
 *
 * Two judgements do the real work:
 *
 *   - which tables are data. The web is still full of tables used for
 *     layout, and a CSV of a navigation bar is noise. A table earns a CSV
 *     only if it has a <th>, more than one row, at least two columns, and
 *     does not wrap another table.
 *   - what a cell says. Inline markup is stripped to its text, because a
 *     spreadsheet cell holds a value, not a link.
 *
 * The caption deliberately does NOT go into the CSV. A leading "# …"
 * comment line would make the file stop being a CSV for most things that
 * read one; the caption is recorded in meta.tables instead.
 */

(function (root) {
  "use strict";

  const HEADINGS = new Set(["h1", "h2", "h3", "h4", "h5", "h6"]);

  // Bounds. A page can carry a pathological table — colspan="100000" is a
  // real thing seen in the wild — and a capture must not become a gigabyte
  // of commas.
  const MAX_TABLES = 25;
  const MAX_ROWS = 5000;
  const MAX_COLS = 256;
  const MAX_SPAN = 512;
  const MAX_CELL_CHARS = 4000;

  const isText = (n) => n && n.tag === "#text";
  const kids = (n) => (n && n.children) || [];
  const attr = (n, name) => (n && n.attrs && n.attrs[name]) || "";
  const clean = (s) => String(s || "").replace(/\s+/g, " ").trim();

  function textOf(node) {
    if (isText(node)) return node.text || "";
    if (node.tag === "br") return " ";
    return kids(node).map(textOf).join("");
  }

  function cellText(node) {
    const text = clean(textOf(node));
    return text.length > MAX_CELL_CHARS ? text.slice(0, MAX_CELL_CHARS) : text;
  }

  function spanOf(node, name) {
    const n = parseInt(attr(node, name), 10);
    if (!Number.isFinite(n) || n < 1) return 1;
    return Math.min(n, MAX_SPAN);
  }

  /**
   * descend walks a subtree, stopping at any nested <table>: a nested
   * table's rows belong to it, not to its host. `visit` returns false for a
   * node it has consumed and does not want descended into.
   */
  function descend(node, visit) {
    for (const child of kids(node)) {
      if (isText(child)) continue;
      const keepGoing = visit(child);
      if (keepGoing === false) continue;
      if (child.tag === "table") continue;
      descend(child, visit);
    }
  }

  function rowsOf(table) {
    const rows = [];
    descend(table, (node) => {
      if (node.tag !== "tr") return true;
      rows.push(node);
      return false;
    });
    return rows;
  }

  function cellsOf(row) {
    const cells = [];
    descend(row, (node) => {
      if (node.tag !== "td" && node.tag !== "th") return true;
      cells.push(node);
      return false;
    });
    return cells;
  }

  function hasTag(table, tag) {
    let found = false;
    descend(table, (node) => {
      if (node.tag === tag) found = true;
      return true;
    });
    return found;
  }

  /**
   * gridOf expands a table into a rectangle. colspan and rowspan repeat
   * their cell's text into every position they cover, which is what a
   * spreadsheet wants: a merged header spanning three columns should label
   * all three, not one value and two blanks.
   */
  function gridOf(table) {
    const grid = [];
    const taken = new Set();
    const rows = rowsOf(table).slice(0, MAX_ROWS);
    let width = 0;

    rows.forEach((tr, r) => {
      if (!grid[r]) grid[r] = [];
      let c = 0;
      for (const cell of cellsOf(tr)) {
        while (taken.has(`${r}:${c}`)) c++;
        if (c >= MAX_COLS) break;
        const cols = Math.min(spanOf(cell, "colspan"), MAX_COLS - c);
        const spanRows = Math.min(spanOf(cell, "rowspan"), MAX_ROWS - r);
        const text = cellText(cell);
        for (let dr = 0; dr < spanRows; dr++) {
          for (let dc = 0; dc < cols; dc++) {
            const rr = r + dr;
            const cc = c + dc;
            if (!grid[rr]) grid[rr] = [];
            grid[rr][cc] = text;
            taken.add(`${rr}:${cc}`);
          }
        }
        c += cols;
        width = Math.max(width, c);
      }
    });

    // Rectangle: a short row is padded, and a hole left by a rowspan that
    // overshot the last row becomes an empty cell rather than a JS hole.
    for (let r = 0; r < grid.length; r++) {
      const row = grid[r] || (grid[r] = []);
      for (let c = 0; c < width; c++) if (row[c] === undefined) row[c] = "";
      row.length = width;
    }
    return grid;
  }

  /**
   * layoutReason names why a table is not data, or returns "" for one that
   * is. A reason rather than a boolean, so a skipped table is reported
   * instead of silently vanishing.
   */
  function layoutReason(table, grid) {
    if (/(^|\s)(presentation|none)(\s|$)/i.test(attr(table, "role"))) return "role=presentation";
    if (hasTag(table, "table")) return "wraps another table";
    if (!hasTag(table, "th")) return "no header cell";
    if (grid.length < 2) return "single row";
    if (!grid[0] || grid[0].length < 2) return "single column";
    return "";
  }

  /** captionOf prefers the table's own caption, then its label, then the heading above it. */
  function captionOf(table, heading) {
    let caption = "";
    descend(table, (node) => {
      if (node.tag === "caption" && !caption) caption = clean(textOf(node));
      return true;
    });
    return caption || clean(attr(table, "aria-label")) || clean(attr(table, "summary")) || heading || "";
  }

  /** collect finds every table in document order, with the heading in force above it. */
  function collect(tree) {
    const found = [];
    let heading = "";
    (function walk(node) {
      for (const child of kids(node)) {
        if (isText(child)) continue;
        if (HEADINGS.has(child.tag)) heading = clean(textOf(child));
        if (child.tag === "table") found.push({ node: child, heading });
        walk(child);
      }
    })(tree);
    return found;
  }

  // --- RFC4180 -------------------------------------------------------------

  const NEEDS_QUOTE = /[",\r\n]/;

  function csvField(value) {
    const s = value === undefined || value === null ? "" : String(value);
    if (!NEEDS_QUOTE.test(s)) return s;
    return `"${s.replace(/"/g, '""')}"`;
  }

  /** toCsv renders a grid as RFC4180: CRLF line breaks, doubled quotes. */
  function toCsv(grid) {
    if (!grid.length) return "";
    return grid.map((row) => row.map(csvField).join(",")).join("\r\n") + "\r\n";
  }

  // --- the extraction ------------------------------------------------------

  /**
   * extract returns { tables, skipped }. Each kept table carries its
   * artifact name, its CSV text, and the shape and caption that go into
   * meta.tables. Numbering runs over kept tables only, so the names are
   * always table-1 … table-N with no gaps.
   */
  function extract(tree, opts) {
    const o = Object.assign({ maxTables: MAX_TABLES }, opts || {});
    const tables = [];
    const skipped = [];

    for (const { node, heading } of collect(tree)) {
      const grid = gridOf(node);
      const reason = layoutReason(node, grid);
      if (reason) {
        skipped.push({ caption: captionOf(node, heading) || null, reason });
        continue;
      }
      if (tables.length >= o.maxTables) {
        skipped.push({ caption: captionOf(node, heading) || null, reason: `over the ${o.maxTables}-table limit` });
        continue;
      }
      tables.push({
        name: `table-${tables.length + 1}.csv`,
        caption: captionOf(node, heading) || null,
        rows: grid.length,
        cols: grid[0].length,
        csv: toCsv(grid),
      });
    }

    return { tables, skipped };
  }

  /** metaEntries is what meta.tables holds: the shape and the caption, never the data. */
  function metaEntries(tables) {
    return (tables || []).map((t) => ({ name: t.name, caption: t.caption, rows: t.rows, cols: t.cols }));
  }

  root.MonoTables = {
    extract, metaEntries, toCsv, csvField, gridOf, layoutReason, captionOf,
    MAX_TABLES, MAX_ROWS, MAX_COLS,
  };
})(globalThis);
