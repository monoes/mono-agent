// Finds, on the page Hacker News redirected to after a reply, the reply
// just posted: a comment by args.user whose text contains the start of
// args.text and whose id is greater than args.parent (ids grow over time).
// The newest match wins.
//
// Why a script: the match compares the typed text with what HN rendered —
// HN turns *x* into italics and blank lines into <p>, so both sides are
// normalised (asterisks and all whitespace dropped, lower-cased) before
// comparing. No declarative step lower-cases or strips text, and putting
// user text into an XPath literal would break on quotes.
//
// args: { user, text, parent }   returns: { id: "<comment id>" | "" }
const norm = s => String(s || '').replace(/[*\s]+/g, '').toLowerCase();
const needle = norm(args.text).slice(0, 80);
const user = String(args.user || '');
const parent = /^[0-9]+$/.test(String(args.parent || '')) ? BigInt(args.parent) : null;
let best = null;
if (needle && user) {
  for (const row of document.querySelectorAll('tr.athing.comtr')) {
    const a = row.querySelector('a.hnuser');
    const body = row.querySelector('.commtext');
    if (!a || !body || a.textContent.trim() !== user) continue;
    if (!/^[0-9]+$/.test(row.id) || (parent !== null && BigInt(row.id) <= parent)) continue;
    const c = body.cloneNode(true);
    c.querySelectorAll('.reply').forEach(e => e.remove());
    if (!norm(c.textContent).includes(needle)) continue;
    if (best === null || BigInt(row.id) > BigInt(best)) best = row.id;
  }
}
return { id: best || '' };
