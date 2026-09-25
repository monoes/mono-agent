// Reads the comment tree of the Hacker News item page currently open.
//
// Why a script: HN renders the tree as a flat list of tr.athing.comtr rows
// whose td.ind[indent] is the depth; a comment's parent is the closest
// previous row one level up. Finding it needs a stack carried from row to
// row (and across "More" pages, via args.stack) — a stateful scan no
// declarative step expresses. Everything else (pagination, the
// topLevelOnly filter, recording) is done by the action's own steps.
//
// args: { itemID: "<digits>", stack: [open ancestor ids] | "" }
// returns: { found, comments: [{id, author, text, depth, parentId, age,
//   deleted}], stack, nextP: "<page number of the More link>" | "" }
const itemID = String(args.itemID || '');
const found = !!document.getElementById(itemID);
const out = [];
const stack = Array.isArray(args.stack) ? args.stack.slice() : [];
for (const row of document.querySelectorAll('tr.athing.comtr')) {
  const ind = row.querySelector('td.ind');
  let depth = ind ? parseInt(ind.getAttribute('indent') || '', 10) : 0;
  if (isNaN(depth)) {
    const img = ind && ind.querySelector('img');
    depth = img ? Math.round((parseInt(img.getAttribute('width') || '0', 10) || 0) / 40) : 0;
  }
  stack.length = depth;
  const parentId = depth > 0 ? (stack[depth - 1] || '') : itemID;
  stack[depth] = row.id;
  const a = row.querySelector('a.hnuser');
  const body = row.querySelector('.commtext');
  let text = '';
  if (body) {
    const c = body.cloneNode(true);
    c.querySelectorAll('.reply').forEach(e => e.remove());
    c.querySelectorAll('p').forEach(p => p.prepend('\n\n'));
    text = c.textContent.replace(/[ \t]+\n/g, '\n').trim();
  }
  const age = row.querySelector('.age');
  out.push({ id: row.id, author: a ? a.textContent.trim() : '', text, depth, parentId,
    age: age ? (age.getAttribute('title') || '').split(' ')[0] : '', deleted: !body });
}
// Only a "More" link to another page of this same item is followed.
let nextP = '';
const more = document.querySelector('a.morelink');
if (more) {
  try {
    const u = new URL(more.href);
    if (u.host === 'news.ycombinator.com' && u.pathname === '/item' && u.searchParams.get('id') === itemID) {
      const p = u.searchParams.get('p') || '';
      if (/^[0-9]{1,4}$/.test(p)) nextP = p;
    }
  } catch (e) {}
}
return { found, comments: out, stack: stack.map(x => x || ''), nextP };
