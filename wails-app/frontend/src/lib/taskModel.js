// Pure logic of the Tasks tab (spec §10): the board document, the moves the
// page shows before the CLI answers, where a drop or a key puts a card,
// search, what changed between two reads, and what a card shows. No React
// and no DOM: the tests drive all of it.

export const COLUMNS = ['inbox', 'ready', 'in_progress', 'review', 'done']

// A card new to these columns goes on top (newest first); Ready and In
// progress are queues (spec 4.6).
const NEWEST_FIRST = new Set(['inbox', 'review', 'done'])

export function defaultIndex(status, length) {
  return NEWEST_FIRST.has(status) ? 0 : length
}

const byPosition = (a, b) => (a.position - b.position) || (a.id - b.id)

// normalizeBoard turns the board document (as `task board --json` prints it)
// into the page's board: the five columns always present, each in position
// order.
export function normalizeBoard(doc) {
  const columns = {}
  for (const s of COLUMNS) columns[s] = [...(doc?.tasks?.[s] || [])].sort(byPosition)
  return {
    profile: doc?.profile || { id: '', name: '' },
    rev: doc?.rev ?? 0,
    counts: { ...(doc?.counts || {}) },
    columns,
  }
}

// findTask is where card id sits: {task, status, index}, or null.
export function findTask(board, id) {
  if (!board) return null
  for (const s of COLUMNS) {
    const index = board.columns[s].findIndex(t => t.id === id)
    if (index >= 0) return { task: board.columns[s][index], status: s, index }
  }
  return null
}

// insertAt is the index place gives in list (the column without the card);
// a ref that is not there falls back to the column's default.
function insertAt(list, status, place) {
  const at = list.findIndex(t => t.id === place?.ref)
  switch (place?.where) {
    case 'top': return 0
    case 'bottom': return list.length
    case 'before': return at < 0 ? defaultIndex(status, list.length) : at
    case 'after': return at < 0 ? defaultIndex(status, list.length) : at + 1
    default: return defaultIndex(status, list.length)
  }
}

// withCounts keeps the counts in step with the columns. Done is cut to its
// newest cards, so its count moves by what its column gained or lost.
function withCounts(board, columns) {
  const counts = { ...board.counts }
  for (const s of COLUMNS) {
    counts[s] = s === 'done'
      ? Math.max(0, (board.counts.done ?? board.columns.done.length) + columns.done.length - board.columns.done.length)
      : columns[s].length
  }
  return { ...board, columns, counts }
}

// applyMove is the board after moving card id to column `to` at place, as
// the store will do it: a card that leaves In progress loses its claim, and a
// move to the column the card is in with no place named changes nothing (spec
// 4.6: the default end belongs to a card new to a column).
export function applyMove(board, id, to, place) {
  const found = findTask(board, id)
  if (!found || !COLUMNS.includes(to)) return board
  if (found.status === to && !place?.where) return board
  const columns = { ...board.columns, [found.status]: board.columns[found.status].filter(t => t.id !== id) }
  const target = [...columns[to]]
  const moved = { ...found.task, status: to, claim: to === 'in_progress' ? found.task.claim : null }
  target.splice(insertAt(target, to, place), 0, moved)
  columns[to] = target
  return withCounts(board, columns)
}

// applyRemove is the board without card id (archived).
export function applyRemove(board, id) {
  const found = findTask(board, id)
  if (!found) return board
  return withCounts(board, { ...board.columns, [found.status]: board.columns[found.status].filter(t => t.id !== id) })
}

// applyOps lays the pending operations ({type: 'move', id, to, place} or
// {type: 'remove', id}) over the last board read, oldest first.
export function applyOps(board, ops) {
  if (!board) return board
  return ops.reduce((b, op) => (op.type === 'remove' ? applyRemove(b, op.id) : applyMove(b, op.id, op.to, op.place)), board)
}

// placeFor turns a drop at index among ids (the target column's cards as
// shown, without the moved card) into the CLI's place, always next to a
// shown card: a search or the Done column cut to its newest cards must not
// bury the card among hidden ones. An empty column takes its default.
export function placeFor(ids, index) {
  if (ids.length === 0) return { where: '' }
  if (index >= ids.length) return { where: 'after', ref: ids[ids.length - 1] }
  return { where: 'before', ref: ids[Math.max(0, index)] }
}

// isNoopDrop says whether a drop leaves the card where it was: same column,
// same order. fromIds is the source column as shown, the card included.
export function isNoopDrop(fromIds, from, to, ids, index, id) {
  if (from !== to) return false
  const order = [...ids.slice(0, index), id, ...ids.slice(index)]
  return order.length === fromIds.length && order.every((x, i) => x === fromIds[i])
}

// dropIndex is where a card dropped at height y lands among rects (the
// column's shown cards without the dragged one, top to bottom): before the
// first card whose middle is below y.
export function dropIndex(rects, y) {
  const i = rects.findIndex(r => y < r.top + r.height / 2)
  return i < 0 ? rects.length : i
}

// keyMove is the move a key asks of card id on the board as shown:
// 'left'/'right' (Shift+arrows) go to the next column at its default place,
// 'up'/'down' (Alt+arrows) pass the shown neighbour. {to, place}, or
// {blocked: 'first' | 'last' | 'top' | 'bottom'}, or null for no such card.
export function keyMove(shown, id, key) {
  const found = findTask(shown, id)
  if (!found) return null
  const { status, index } = found
  const col = shown.columns[status]
  if (key === 'left' || key === 'right') {
    const to = COLUMNS[COLUMNS.indexOf(status) + (key === 'left' ? -1 : 1)]
    if (!to) return { blocked: key === 'left' ? 'first' : 'last' }
    return { to, place: { where: '' } }
  }
  if (key === 'up') {
    if (index === 0) return { blocked: 'top' }
    return { to: status, place: { where: 'before', ref: col[index - 1].id } }
  }
  if (key === 'down') {
    if (index === col.length - 1) return { blocked: 'bottom' }
    return { to: status, place: { where: 'after', ref: col[index + 1].id } }
  }
  return null
}

// focusTarget is the card a plain arrow moves the focus to: above or below
// in the column, or the card at the same height (else the last) in the
// nearest column that has cards.
export function focusTarget(shown, id, key) {
  const found = findTask(shown, id)
  if (!found) return null
  const col = shown.columns[found.status]
  if (key === 'up') return col[found.index - 1]?.id ?? null
  if (key === 'down') return col[found.index + 1]?.id ?? null
  const step = key === 'left' ? -1 : 1
  for (let c = COLUMNS.indexOf(found.status) + step; c >= 0 && c < COLUMNS.length; c += step) {
    const other = shown.columns[COLUMNS[c]]
    if (other.length) return other[Math.min(found.index, other.length - 1)].id
  }
  return null
}

// searchBoard keeps the cards whose title or notes hold every word of query
// (case-insensitive); a blank query keeps them all. Counts stay the board's.
export function searchBoard(board, query) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  if (!board || words.length === 0) return board
  const hit = t => {
    const text = `${t.title || ''}\n${t.notes || ''}`.toLowerCase()
    return words.every(w => text.includes(w))
  }
  const columns = {}
  for (const s of COLUMNS) columns[s] = board.columns[s].filter(hit)
  return { ...board, columns }
}
