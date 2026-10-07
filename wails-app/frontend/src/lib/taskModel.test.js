import { describe, it, expect } from 'vitest'
import {
  COLUMNS, normalizeBoard, findTask, applyMove, applyRemove, applyOps, placeFor, isNoopDrop, dropIndex,
  keyMove, focusTarget,
} from './taskModel.js'

const T0 = Date.parse('2026-10-06T09:00:00Z')
const card = (id, status, extra = {}) => ({
  id, title: `t${id}`, notes: '', status, position: id * 1024,
  source: { kind: 'cli', url: '', title: '', app: '' }, claim: null,
  last_event: { actor: 'you', kind: 'created', at: '2026-10-06T08:00:00Z' },
  created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-06T08:00:00Z', ...extra,
})
const doc = (tasks, extra = {}) => ({ profile: { id: 'default', name: 'Default' }, rev: 1, counts: {}, tasks, ...extra })
const ids = (board, s) => board.columns[s].map(t => t.id)
const by = (actor, kind, at = '2026-10-06T09:10:00Z') => ({ last_event: { actor, kind, at } })

// Inbox 1 2, Ready 3 4, In progress 5 (held by bot), Review 6, Done 7 (of 120).
const base = () => normalizeBoard(doc({
  inbox: [card(2, 'inbox'), card(1, 'inbox')],
  ready: [card(3, 'ready'), card(4, 'ready')],
  in_progress: [card(5, 'in_progress', { claim: { by: 'bot', until: '2026-10-06T09:30:00Z', stale: false } })],
  review: [card(6, 'review')],
  done: [card(7, 'done')],
}, { counts: { inbox: 2, ready: 2, in_progress: 1, review: 1, done: 120, stale: 0 } }))

describe('normalizeBoard', () => {
  it('keeps the five columns, each in position order, empty ones too', () => {
    const b = normalizeBoard(doc({ inbox: [card(2, 'inbox'), card(1, 'inbox')] }))
    expect(Object.keys(b.columns)).toEqual(COLUMNS)
    expect(ids(b, 'inbox')).toEqual([1, 2])
    expect(b.columns.done).toEqual([])
    expect(b.profile.id).toBe('default')
  })
})

describe('applyMove', () => {
  it('puts a card at the default place of its new column', () => {
    expect(ids(applyMove(base(), 1, 'ready', { where: '' }), 'ready')).toEqual([3, 4, 1])
    expect(ids(applyMove(base(), 3, 'review', { where: '' }), 'review')).toEqual([3, 6])
    expect(ids(applyMove(base(), 3, 'inbox', { where: '' }), 'inbox')).toEqual([3, 1, 2])
    expect(ids(applyMove(base(), 3, 'in_progress', { where: '' }), 'in_progress')).toEqual([5, 3])
  })
  it('gives the card the status of its new column', () => {
    expect(findTask(applyMove(base(), 1, 'ready', { where: '' }), 1).task.status).toBe('ready')
    expect(findTask(applyMove(base(), 5, 'review', { where: '' }), 5).task.status).toBe('review')
  })
  it('puts it before or after a card, and falls back when that card is not there', () => {
    expect(ids(applyMove(base(), 1, 'ready', { where: 'before', ref: 4 }), 'ready')).toEqual([3, 1, 4])
    expect(ids(applyMove(base(), 1, 'ready', { where: 'after', ref: 3 }), 'ready')).toEqual([3, 1, 4])
    expect(ids(applyMove(base(), 1, 'ready', { where: 'before', ref: 99 }), 'ready')).toEqual([3, 4, 1])
  })
  it('reorders within a column', () => {
    const b = applyMove(base(), 4, 'ready', { where: 'before', ref: 3 })
    expect(ids(b, 'ready')).toEqual([4, 3])
    expect(b.counts.ready).toBe(2)
  })
  it('changes nothing for a move to the column the card is in when no place is named', () => {
    // Spec 4.6: the default end belongs to a card that is new to a column.
    const b = base()
    expect(applyMove(b, 3, 'ready', { where: '' })).toBe(b)
    expect(applyMove(b, 1, 'inbox', {})).toBe(b)
    expect(applyMove(b, 4, 'ready', undefined)).toBe(b)
  })
  it('ends the claim of a card that leaves In progress and keeps it inside', () => {
    expect(findTask(applyMove(base(), 5, 'ready', { where: '' }), 5).task.claim).toBeNull()
    expect(findTask(applyMove(base(), 5, 'in_progress', { where: 'top' }), 5).task.claim.by).toBe('bot')
  })
  it('keeps the counts in step, Done by what its column gained', () => {
    const b = applyMove(base(), 6, 'done', { where: '' })
    expect(b.counts.review).toBe(0)
    expect(b.counts.done).toBe(121)
    expect(ids(b, 'done')).toEqual([6, 7])
  })
  it('leaves the board alone for an unknown card or column', () => {
    const b = base()
    expect(applyMove(b, 99, 'ready', {})).toBe(b)
    expect(applyMove(b, 1, 'archived', {})).toBe(b)
  })
})

describe('applyOps and applyRemove', () => {
  it('lays the pending operations over the board in order', () => {
    const b = applyOps(base(), [{ type: 'move', id: 1, to: 'ready', place: {} }, { type: 'remove', id: 3 }])
    expect(ids(b, 'inbox')).toEqual([2])
    expect(ids(b, 'ready')).toEqual([4, 1])
    expect(applyRemove(base(), 7).counts.done).toBe(119)
    expect(applyOps(null, [{ type: 'remove', id: 1 }])).toBeNull()
  })
  it('lays them oldest first, so a later one sees what an earlier one did', () => {
    // Card 1 dragged to Ready, then card 2 dropped right after it.
    const dragged = applyOps(base(), [
      { type: 'move', id: 1, to: 'ready', place: { where: '' } },
      { type: 'move', id: 2, to: 'ready', place: { where: 'after', ref: 1 } },
    ])
    expect(ids(dragged, 'ready')).toEqual([3, 4, 1, 2])
    // The same card sent to Ready and then to Review ends in Review.
    const twice = applyOps(base(), [
      { type: 'move', id: 1, to: 'ready', place: { where: '' } },
      { type: 'move', id: 1, to: 'review', place: { where: '' } },
    ])
    expect(findTask(twice, 1).status).toBe('review')
  })
})

describe('placeFor', () => {
  it('names a shown neighbour, never the bottom', () => {
    expect(placeFor([], 0)).toEqual({ where: '' })
    expect(placeFor([3, 4], 0)).toEqual({ where: 'before', ref: 3 })
    expect(placeFor([3, 4], 1)).toEqual({ where: 'before', ref: 4 })
    expect(placeFor([3, 4], 2)).toEqual({ where: 'after', ref: 4 })
    // Done shows the newest of many: below the last shown card means right after it.
    expect(placeFor([7], 1)).toEqual({ where: 'after', ref: 7 })
  })
})

describe('isNoopDrop', () => {
  it('is a drop that changes nothing', () => {
    expect(isNoopDrop([3, 4], 'ready', 'ready', [4], 0, 3)).toBe(true)
    expect(isNoopDrop([3, 4], 'ready', 'ready', [4], 1, 3)).toBe(false)
    expect(isNoopDrop([3, 4], 'ready', 'review', [6], 0, 3)).toBe(false)
  })
  it('never takes a drop into another column for a no-op, a lone card into an empty column included', () => {
    expect(isNoopDrop([3], 'ready', 'review', [], 0, 3)).toBe(false)
    expect(isNoopDrop([1], 'inbox', 'ready', [], 0, 1)).toBe(false)
    expect(isNoopDrop([3], 'ready', 'ready', [], 0, 3)).toBe(true) // back into its own column: nothing to do
  })
})

describe('dropIndex', () => {
  const rects = [{ top: 0, height: 40 }, { top: 50, height: 40 }]
  it('lands before the first card whose middle is below the pointer', () => {
    expect(dropIndex(rects, 5)).toBe(0)
    expect(dropIndex(rects, 19.9)).toBe(0)
    expect(dropIndex(rects, 20)).toBe(1) // exactly at a middle: after that card
    expect(dropIndex(rects, 69)).toBe(1)
    expect(dropIndex(rects, 70)).toBe(2)
    expect(dropIndex([], 10)).toBe(0)
  })
})

describe('keyMove', () => {
  it('moves a column at a time, to the default place', () => {
    expect(keyMove(base(), 1, 'right')).toEqual({ to: 'ready', place: { where: '' } })
    expect(keyMove(base(), 3, 'left')).toEqual({ to: 'inbox', place: { where: '' } })
    expect(keyMove(base(), 1, 'left')).toEqual({ blocked: 'first' })
    expect(keyMove(base(), 7, 'right')).toEqual({ blocked: 'last' })
  })
  it('reorders past the shown neighbour', () => {
    expect(keyMove(base(), 4, 'up')).toEqual({ to: 'ready', place: { where: 'before', ref: 3 } })
    expect(keyMove(base(), 3, 'down')).toEqual({ to: 'ready', place: { where: 'after', ref: 4 } })
    expect(keyMove(base(), 3, 'up')).toEqual({ blocked: 'top' })
    expect(keyMove(base(), 4, 'down')).toEqual({ blocked: 'bottom' })
    expect(keyMove(base(), 99, 'up')).toBeNull()
  })
})

describe('focusTarget', () => {
  it('moves the focus up, down, and across to the nearest column with cards', () => {
    const b = applyRemove(base(), 6) // Review is empty now
    expect(focusTarget(b, 3, 'down')).toBe(4)
    expect(focusTarget(b, 3, 'up')).toBeNull()
    expect(focusTarget(b, 2, 'right')).toBe(4)
    expect(focusTarget(b, 5, 'right')).toBe(7)
    expect(focusTarget(b, 1, 'left')).toBeNull()
  })
  it('lands on the last card when the nearest column is shorter than the card is deep', () => {
    // Card 4 is the second of Ready; In progress holds card 5 alone.
    expect(focusTarget(base(), 4, 'right')).toBe(5)
  })
})
