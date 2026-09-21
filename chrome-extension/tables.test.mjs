// Tests for CLIP-11: which tables become CSV, and what the CSV says.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoTables, MonoDomLite } = loadExtensionScripts(["domlite.js", "tables.js"]);
const { MonoCapture } = loadExtensionScripts(["capture_meta.js", "capture.js"]);

const parse = (html) => MonoDomLite.parse(html);
const rowsOf = (csv) => csv.replace(/\r\n$/, "").split("\r\n");

test("a data table becomes RFC4180 CSV", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>Keeper</th><th>Years</th></tr>
      <tr><td>Ada Renn</td><td>12</td></tr>
      <tr><td>Bram Yule</td><td>7</td></tr>
    </table>`)
  );

  assert.equal(tables.length, 1);
  assert.equal(tables[0].name, "table-1.csv");
  assert.equal(tables[0].rows, 3);
  assert.equal(tables[0].cols, 2);
  assert.deepEqual(rowsOf(tables[0].csv), ["Keeper,Years", "Ada Renn,12", "Bram Yule,7"]);
  // RFC4180 line ending, including the last one.
  assert.ok(tables[0].csv.endsWith("\r\n"));
});

test("quotes, commas and newlines are escaped the RFC4180 way", () => {
  assert.equal(MonoTables.csvField('say "hi"'), '"say ""hi"""');
  assert.equal(MonoTables.csvField("Dunmore, Cork"), '"Dunmore, Cork"');
  assert.equal(MonoTables.csvField("two\nlines"), '"two\nlines"');
  assert.equal(MonoTables.csvField("plain"), "plain");
  assert.equal(MonoTables.csvField(null), "");
});

test("inline markup is stripped to the cell's text", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>Source</th><th>Note</th></tr>
      <tr><td><a href="/x"><b>The</b> <i>Paper</i></a></td><td>a<br>b</td></tr>
    </table>`)
  );
  assert.deepEqual(rowsOf(tables[0].csv)[1].split(","), ["The Paper", "a b"]);
});

test("colspan and rowspan are expanded into every cell they cover", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th colspan="2">Watch</th><th>Log</th></tr>
      <tr><td rowspan="2">Ada</td><td>dawn</td><td>clear</td></tr>
      <tr><td>dusk</td><td>fog</td></tr>
    </table>`)
  );

  assert.equal(tables[0].cols, 3);
  assert.deepEqual(rowsOf(tables[0].csv), [
    "Watch,Watch,Log",
    "Ada,dawn,clear",
    "Ada,dusk,fog",
  ]);
});

test("ragged rows are padded to a rectangle", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>a</th><th>b</th><th>c</th></tr>
      <tr><td>1</td></tr>
    </table>`)
  );
  assert.deepEqual(rowsOf(tables[0].csv), ["a,b,c", "1,,"]);
});

test("layout tables are skipped, each with a reason", () => {
  const html = `
    <table><tr><td>nav</td><td>logo</td></tr></table>
    <table><tr><th>only</th><th>one</th></tr></table>
    <table role="presentation"><tr><th>x</th><th>y</th></tr><tr><td>1</td><td>2</td></tr></table>
    <table><tr><th>outer</th><th>shell</th></tr><tr><td><table><tr><th>In</th><th>Out</th></tr><tr><td>1</td><td>2</td></tr></table></td><td>z</td></tr></table>`;
  const { tables, skipped } = MonoTables.extract(parse(html));

  assert.deepEqual(skipped.map((s) => s.reason), [
    "no header cell",
    "single row",
    "role=presentation",
    "wraps another table",
  ]);
  // The real table inside the layout wrapper is still extracted on its own.
  assert.equal(tables.length, 1);
  assert.deepEqual(rowsOf(tables[0].csv), ["In,Out", "1,2"]);
});

test("a single-column list is not a spreadsheet", () => {
  const { tables, skipped } = MonoTables.extract(
    parse(`<table><tr><th>Name</th></tr><tr><td>Ada</td></tr></table>`)
  );
  assert.equal(tables.length, 0);
  assert.equal(skipped[0].reason, "single column");
});

test("the caption is recorded in meta, never prepended to the CSV", () => {
  const { tables } = MonoTables.extract(
    parse(`<h2>Keepers of Dunmore</h2>
      <table><caption>Roster, 1881</caption>
        <tr><th>Keeper</th><th>Years</th></tr><tr><td>Ada</td><td>12</td></tr></table>
      <h2>Supplies</h2>
      <table><tr><th>Item</th><th>Count</th></tr><tr><td>Oil</td><td>40</td></tr></table>`)
  );

  assert.equal(tables[0].caption, "Roster, 1881");
  // Falls back to the heading in force above the table.
  assert.equal(tables[1].caption, "Supplies");
  assert.ok(!tables[0].csv.startsWith("#"));
  assert.equal(rowsOf(tables[0].csv)[0], "Keeper,Years");

  assert.deepEqual(MonoTables.metaEntries(tables), [
    { name: "table-1.csv", caption: "Roster, 1881", rows: 2, cols: 2 },
    { name: "table-2.csv", caption: "Supplies", rows: 2, cols: 2 },
  ]);
});

test("names are gapless over kept tables", () => {
  const { tables } = MonoTables.extract(
    parse(`<table><tr><td>layout</td><td>only</td></tr></table>
      <table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>
      <table><tr><th>c</th><th>d</th></tr><tr><td>3</td><td>4</td></tr></table>`)
  );
  assert.deepEqual(tables.map((t) => t.name), ["table-1.csv", "table-2.csv"]);
});

test("a pathological span cannot blow the grid up", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th colspan="100000">wide</th><th>b</th></tr>
      <tr><td>1</td><td>2</td></tr>
    </table>`)
  );
  assert.ok(tables[0].cols <= MonoTables.MAX_COLS, `cols = ${tables[0].cols}`);
  assert.equal(tables[0].rows, 2);
});

test("a rowspan cannot invent rows the table does not have", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>Keeper</th><th>Years</th></tr>
      <tr><td rowspan="500">Ada</td><td>12</td></tr>
      <tr><td>7</td></tr>
    </table>`)
  );
  // Three <tr> on the page is three rows in the CSV, whatever the span says.
  assert.equal(tables[0].rows, 3, `rows = ${tables[0].rows}`);
  assert.deepEqual(rowsOf(tables[0].csv), ["Keeper,Years", "Ada,12", "Ada,7"]);
});

test("a lone rowspan does not smuggle a layout table past the filter", () => {
  // One row, one giant rowspan: the grid used to come out 500 rows tall, so
  // the "single row" check never fired.
  const { tables, skipped } = MonoTables.extract(
    parse(`<table><tr><th rowspan="500">nav</th><th>logo</th></tr></table>`)
  );
  assert.equal(tables.length, 0);
  assert.equal(skipped[0].reason, "single row");
});

test("the table cap is checked before the grids are built", () => {
  const big = `<table>${`<tr><th>a</th><th>b</th></tr>`.repeat(4000)}</table>`;
  const html = `<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>${big.repeat(40)}`;
  const tree = parse(html);

  const started = Date.now();
  const { tables, skipped } = MonoTables.extract(tree, { maxTables: 1 });
  const elapsed = Date.now() - started;

  assert.equal(tables.length, 1);
  assert.equal(skipped.length, 40);
  assert.ok(elapsed < 250, `building grids nobody wanted took ${elapsed}ms`);
});

test("script and style source never lands in a cell", () => {
  // capture_page.js hands the *unpruned* tree here, so the stripping that
  // readable.js does for Markdown has not happened yet.
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>Keeper</th><th>Years</th></tr>
      <tr><td>Ada<script>window.tracker = 1;</script></td><td>12<style>.x{color:red}</style></td></tr>
    </table>`)
  );
  assert.deepEqual(rowsOf(tables[0].csv), ["Keeper,Years", "Ada,12"]);
});

test("a cell that reads as a spreadsheet formula is neutralised", () => {
  // The CSV is opened in a spreadsheet; a leading = + - @ is code there.
  assert.equal(MonoTables.csvField("=1+1"), "'=1+1");
  assert.equal(MonoTables.csvField('=HYPERLINK("http://evil.test","click")'), '"\'=HYPERLINK(""http://evil.test"",""click"")"');
  assert.equal(MonoTables.csvField("@SUM(A1)"), "'@SUM(A1)");
  assert.equal(MonoTables.csvField("+1+1"), "'+1+1");
  assert.equal(MonoTables.csvField("-2+3"), "'-2+3");
  // …but a number is data, not a formula, and must stay a number.
  assert.equal(MonoTables.csvField("-12"), "-12");
  assert.equal(MonoTables.csvField("+0.5"), "+0.5");
  assert.equal(MonoTables.csvField("-1.5e3"), "-1.5e3");
  assert.equal(MonoTables.csvField("12"), "12");
});

test("formula-shaped cells are neutralised through the real extraction", () => {
  const { tables } = MonoTables.extract(
    parse(`<table>
      <tr><th>Item</th><th>Change</th></tr>
      <tr><td>=cmd|'/c calc'!A1</td><td>-12</td></tr>
    </table>`)
  );
  // No comma or quote in the value, so RFC4180 quoting is not triggered —
  // the apostrophe is doing the work on its own.
  assert.deepEqual(rowsOf(tables[0].csv)[1], `'=cmd|'/c calc'!A1,-12`);
});

// --- the wiring: page tables become envelope artifacts --------------------

/** captureCtx is the smallest Chrome stand-in that reaches the artifact list. */
function captureCtx(tables) {
  const asked = {};
  return {
    asked,
    resolveTabId: async () => 1,
    inject: async () => {},
    callPage: async (tabId, fn, args) => {
      if (fn === "prepare") return {};
      if (fn === "restore") return { restored: true };
      asked.extract = args[0];
      return {
        meta: { url: "https://paper.test/roster", title: "Roster", tags: [] },
        markdown: "# Roster",
        text: "Roster",
        tables,
      };
    },
    attach: async () => {},
    cdp: async () => ({ data: "" }),
    detach: async () => {},
  };
}

test("each extracted table is sent as its own artifact", async () => {
  const tables = MonoTables.extract(
    parse(`<table><caption>Roster</caption><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>`)
  ).tables;
  const ctx = captureCtx(tables);

  const result = await MonoCapture.pageCapture({ formats: ["readable", "tables"] }, ctx);

  assert.equal(ctx.asked.extract.tables, true);
  assert.deepEqual(result.artifacts.map((a) => a.name), ["readable.md", "table-1.csv"]);
  const csv = Buffer.from(result.artifacts[1].bytes, "base64").toString("utf8");
  assert.deepEqual(rowsOf(csv), ["a,b", "1,2"]);
});

test("a capture that did not ask for tables carries none", async () => {
  const ctx = captureCtx([]);
  const result = await MonoCapture.pageCapture({ formats: ["readable"] }, ctx);
  assert.equal(ctx.asked.extract.tables, false);
  assert.deepEqual(result.artifacts.map((a) => a.name), ["readable.md"]);
});

test("the table count is bounded and the overflow says so", () => {
  const one = `<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>`;
  const { tables, skipped } = MonoTables.extract(parse(one.repeat(4)), { maxTables: 2 });
  assert.equal(tables.length, 2);
  assert.equal(skipped.length, 2);
  assert.match(skipped[0].reason, /2-table limit/);
});
