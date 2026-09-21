/**
 * The pages the browser tests drive, and the helpers they drive them with.
 *
 * Kept beside the tests rather than inside them: the markup is the point —
 * it is indented, wrapped across source lines and full of inline elements
 * exactly as real pages are, because that is what the highlighter has to
 * survive. Not loaded by the extension; only by the tests.
 */

/**
 * The fixture is formatted the way HTML actually is — indented, wrapped
 * across source lines — because that indentation is whitespace inside the
 * text nodes, and getting it wrong is the failure mode this suite exists
 * to catch.
 */
export const FIXTURE = `<!doctype html>
<html><head><meta charset="utf-8"><title>Highlight fixture</title>
<style>body{font:16px/1.6 serif;width:900px;margin:24px}pre{background:#eee;padding:8px}</style>
</head><body>
<h1 id="title">A page worth marking up</h1>
<p id="one">The first paragraph is ordinary prose, long enough that a
  selection can take a piece out of the middle of it without ever
  touching either end of the paragraph.</p>
<p id="two">The second paragraph carries <em>emphasis</em> and
  <a id="link" href="https://example.com/target">a link that must keep working</a>
  and a little <code>inline_code()</code> besides.</p>
<p id="three">A third paragraph follows it, so that one selection can run
  from the middle of one block into the middle of the next.</p>
<pre id="pre"><code>def indent():
    return "  spaces  matter  here  "</code></pre>
<p id="tail">The tail of the document says see below twice, and says see below again.</p>
<script>
  window.__linkClicks = 0;
  document.getElementById("link").addEventListener("click", (e) => {
    e.preventDefault();
    window.__linkClicks++;
  });
</script>
</body></html>
`;

/** Markup that is awkward in ways prose is not. */
export const AWKWARD = `<!doctype html><html><head><meta charset="utf-8">
<style>body{font:16px/1.6 serif;width:900px;margin:24px}</style></head><body>
<div id="min"><p>A minified page keeps no whitespace between its blocks.</p><p>The next block begins at once, and the two must not run together.</p></div>
<p id="broken">A line ends here<br>and the break reads as a space between them.</p>
<p id="veiled">Some of this sentence <span style="display:none">IS NOT SHOWN TO ANYONE</span>is hidden from the reader entirely.</p>
</body></html>`;

/** In-page helpers the tests drive the mouse with. */
export const HELPERS = `
  // The nth VISIBLE character inside an element: offsets that ignore the
  // source's own indentation, so a drag always starts and ends on a glyph.
  window.__at = (sel, n) => {
    const walk = document.createTreeWalker(document.querySelector(sel), NodeFilter.SHOW_TEXT);
    let seen = 0;
    for (let node = walk.nextNode(); node; node = walk.nextNode()) {
      const value = node.nodeValue;
      for (let i = 0; i < value.length; i++) {
        if (/\s/.test(value[i])) continue;
        if (seen === n) return { node, offset: i };
        seen++;
      }
    }
    throw new Error(sel + " has no visible character " + n);
  };
  window.__box = (s1, o1, s2, o2) => {
    const a = window.__at(s1, o1), b = window.__at(s2, o2);
    const first = document.createRange(); first.setStart(a.node, a.offset); first.setEnd(a.node, a.offset + 1);
    const last = document.createRange(); last.setStart(b.node, b.offset); last.setEnd(b.node, b.offset + 1);
    const f = first.getBoundingClientRect(), l = last.getBoundingClientRect();
    return { x1: f.left + 1, y1: f.top + f.height / 2, x2: l.right - 1, y2: l.top + l.height / 2 };
  };
  window.__buttons = () => [...document.querySelectorAll("#monoagent-highlight-ui button")].map((b) => {
    const r = b.getBoundingClientRect();
    return { label: b.textContent, title: b.title, x: r.left + r.width / 2, y: r.top + r.height / 2 };
  });
  window.__marks = () => [...document.querySelectorAll("mark.monoagent-highlight")].map((m) => ({
    id: m.dataset.monoagentHighlight, text: m.textContent, title: m.title,
  }));
  window.__markText = () => window.__marks().map((m) => m.text).join("").replace(/\\s+/g, " ").trim();
  // What the highlight spans, end to end: the marks plus whatever sits
  // between them (the formatting whitespace between two blocks, which is
  // deliberately not wrapped).
  window.__covered = () => {
    const marks = [...document.querySelectorAll("mark.monoagent-highlight")];
    if (!marks.length) return "";
    const range = document.createRange();
    range.setStartBefore(marks[0]);
    range.setEndAfter(marks[marks.length - 1]);
    // Read it back as a selection, not as range.toString(): the first is
    // what the reader sees, the second is raw node data.
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    const text = selection.toString();
    selection.removeAllRanges();
    return text;
  };
  // Every visible character inside that span must be inside a mark.
  window.__gapless = () => {
    const bare = (s) => s.replace(/\\s+/g, "");
    return bare(window.__marks().map((m) => m.text).join("")) === bare(window.__covered());
  };
  window.__center = (sel) => {
    const r = document.querySelector(sel).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  };
  true;
`;
