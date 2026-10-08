import { describe, it, expect } from 'vitest'
import {
  COLUMNS, normalizeBoard, findTask, applyMove, applyRemove, applyOps, placeFor, isNoopDrop, dropIndex,
  keyMove, focusTarget, matches, filterBoard, remoteChanges, transitions, hostOf, sourceChip, shortAge,
  claimState, splitMinutes, actorColor, isTypingTarget,
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
    // a hand-edited row with a claim outside In progress does not keep it on the way in
    const odd = base(); odd.columns.ready = [card(9, 'ready', { claim: { by: 'bot', until: '', stale: false } })]
    expect(findTask(applyMove(odd, 9, 'in_progress', { where: 'top' }), 9).task.claim).toBeNull()
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

describe('keyMove with a search', () => {
  it('passes only the cards a search shows', () => {
    const b = normalizeBoard(doc({ ready: [card(3, 'ready'), card(8, 'ready', { title: 'hidden' }), card(4, 'ready')] }))
    expect(keyMove(filterBoard(b, 't'), 4, 'up')).toEqual({ to: 'ready', place: { where: 'before', ref: 3 } })
  })
})

describe('search', () => {
  const t = card(12, 'inbox', {
    title: 'Fix the Login test', notes: 'flaky on CI',
    source: { kind: 'chrome', url: 'https://github.com/x/y', title: 'PR 41', app: '' },
    claim: { by: 'claude-code#a3f9', until: '', stale: false },
  })
  it('needs every word, in any field, in any case', () => {
    expect(matches(t, 'login FLAKY')).toBe(true)
    expect(matches(t, 'login missing')).toBe(false)
    expect(matches(t, 'github.com')).toBe(true)
    expect(matches(t, 'a3f9')).toBe(true)
    expect(matches(t, '')).toBe(true)
  })
  it('reads #12 as that task only', () => {
    expect(matches(t, '#12')).toBe(true)
    expect(matches(t, '#1')).toBe(false)
  })
  it('leaves the board as it is without a query', () => {
    const b = base()
    expect(filterBoard(b, '  ')).toBe(b)
    expect(ids(filterBoard(b, '#3'), 'ready')).toEqual([3])
    expect(ids(filterBoard(b, '#3'), 'inbox')).toEqual([])
  })
})

describe('remoteChanges', () => {
  it('names what an agent did, never what you did', () => {
    const next = normalizeBoard(doc({
      inbox: [card(1, 'inbox'), card(2, 'inbox')],
      ready: [card(4, 'ready')],
      in_progress: [card(3, 'in_progress', { ...by('bot', 'claimed'), claim: { by: 'bot', until: '', stale: false } })],
      review: [card(5, 'review', by('bot', 'result'))],
      done: [card(6, 'done', by('you', 'moved')), card(7, 'done')],
    }))
    expect(remoteChanges(base(), next).map(c => [c.id, c.actor, c.kind, c.to])).toEqual([
      [3, 'bot', 'claimed', 'in_progress'], [5, 'bot', 'result', 'review']])
  })
  it('names a card someone added and a claim taken over', () => {
    const prev = normalizeBoard(doc({ in_progress: [card(5, 'in_progress', { claim: { by: 'bot', until: '', stale: true } })] }))
    const next = normalizeBoard(doc({
      inbox: [card(9, 'inbox', by('chrome', 'created', '2026-10-06T09:11:00Z'))],
      in_progress: [card(5, 'in_progress', { ...by('other', 'reclaimed', '2026-10-06T09:12:00Z'), claim: { by: 'other', until: '', stale: false } })],
    }))
    expect(remoteChanges(prev, next).map(c => [c.id, c.actor, c.kind])).toEqual([[9, 'chrome', 'created'], [5, 'other', 'reclaimed']])
    expect(remoteChanges(null, next)).toEqual([])
  })
  it('ignores a comment and a card that only came back into view', () => {
    const held = { claim: { by: 'bot', until: '', stale: false } }
    const prev = normalizeBoard(doc({ in_progress: [card(5, 'in_progress', held)] }))
    const next = normalizeBoard(doc({
      in_progress: [card(5, 'in_progress', { ...held, ...by('bot', 'comment') })],
      done: [card(8, 'done', by('bot', 'result'))],
    }))
    expect(remoteChanges(prev, next)).toEqual([])
  })
  it('keeps the newest three, and nothing across profiles', () => {
    const prev = normalizeBoard(doc({}))
    const next = normalizeBoard(doc({ inbox: [1, 2, 3, 4].map(i => card(i, 'inbox', by('chrome', 'created', `2026-10-06T09:1${i}:00Z`))) }))
    expect(remoteChanges(prev, next).map(c => c.id)).toEqual([2, 3, 4])
    expect(remoteChanges({ ...prev, profile: { id: 'work', name: 'Work' } }, next)).toEqual([])
  })
})

describe('transitions', () => {
  it('marks new cards and cards just done, and nothing on the first read', () => {
    const moved = applyMove(base(), 6, 'done', {})
    const next = { ...moved, columns: { ...moved.columns, inbox: [card(9, 'inbox'), ...moved.columns.inbox] } }
    const t = transitions(base(), next)
    expect([...t.entered]).toEqual([9])
    expect([...t.done]).toEqual([6])
    expect(transitions(null, next).entered.size).toBe(0)
  })
  it('marks a claim taken, not one that is merely renewed', () => {
    const claim = (by) => ({ claim: { by, until: '2026-10-06T10:00:00Z', stale: false } })
    const withClaim = (c) => normalizeBoard(doc({ in_progress: [card(5, 'in_progress', c)], ready: [card(3, 'ready')] }))
    expect([...transitions(withClaim({}), withClaim(claim('bot'))).claimed]).toEqual([5])
    expect([...transitions(withClaim(claim('bot')), withClaim(claim('other'))).claimed]).toEqual([5])
    expect(transitions(withClaim(claim('bot')), withClaim(claim('bot'))).claimed.size).toBe(0)
    expect(transitions(null, withClaim(claim('bot'))).claimed.size).toBe(0)
    const moved = applyMove(withClaim({}), 3, 'in_progress', {})
    expect(transitions(withClaim({}), moved).claimed.size).toBe(0) // no claimant yet
  })
})

describe('card labels', () => {
  it('shows the domain, the app, or the kind', () => {
    expect(hostOf('https://www.github.com/x')).toBe('github.com')
    expect(hostOf('not a url')).toBe('')
    expect(sourceChip({ source: { kind: 'chrome', url: 'https://www.github.com/x', title: 'PR' } })).toEqual({ kind: 'chrome', text: 'github.com' })
    expect(sourceChip({ source: { kind: 'chrome', url: '', title: 'Saved page' } })).toEqual({ kind: 'chrome', text: 'Saved page' })
    expect(sourceChip({ source: { kind: 'os', app: 'Safari' } })).toEqual({ kind: 'os', text: 'Safari' })
    expect(sourceChip({ source: { kind: 'agent' } })).toEqual({ kind: 'agent', text: '' })
    expect(sourceChip({ source: { kind: 'app' } })).toEqual({ kind: 'app', text: '' })
    expect(sourceChip({})).toEqual({ kind: 'cli', text: '' })
  })
  it('gives an age in the largest whole unit', () => {
    const M = 60000
    const H = 60 * M
    const D = 24 * H
    const at = (ms) => shortAge('2026-10-06T09:00:00Z', T0 + ms)
    expect(at(59 * 1000)).toEqual({ unit: 'now', n: 0 })
    expect(at(M)).toEqual({ unit: 'm', n: 1 })
    expect(at(59 * M)).toEqual({ unit: 'm', n: 59 })
    expect(at(H)).toEqual({ unit: 'h', n: 1 })
    expect(at(D - 1)).toEqual({ unit: 'h', n: 23 })
    expect(at(D)).toEqual({ unit: 'd', n: 1 })
    expect(at(7 * D)).toEqual({ unit: 'w', n: 1 })
    expect(shortAge('garbage', T0)).toEqual({ unit: 'now', n: 0 })
  })
  it('shows a claim live until its lease ends, then stale', () => {
    const claim = { by: 'claude-code#a3f9', until: '2026-10-06T09:30:00Z', stale: false }
    expect(claimState(claim, T0)).toMatchObject({ by: 'claude-code#a3f9', initial: 'C', stale: false, minutes: 30 })
    expect(claimState(claim, Date.parse('2026-10-06T09:30:00Z'))).toMatchObject({ stale: true, minutes: 0 }) // ending exactly now is ended
    expect(claimState(claim, Date.parse('2026-10-06T09:34:10Z'))).toMatchObject({ stale: true, minutes: 4 })
    expect(claimState({ ...claim, stale: true }, T0).stale).toBe(true)
    expect(claimState({ by: '#42', until: '' }, T0)).toMatchObject({ initial: '4', stale: true })
    expect(claimState(null, T0)).toBeNull()
    expect(splitMinutes(65)).toEqual({ h: 1, m: 5 })
  })
  it('keeps one colour per agent', () => {
    expect(actorColor('claude-code#a3f9')).toBe(actorColor('claude-code#a3f9'))
    expect(actorColor('a')).toMatch(/^var\(--/)
  })
  it('knows a field from the board', () => {
    expect(isTypingTarget({ tagName: 'TEXTAREA' })).toBe(true)
    expect(isTypingTarget({ tagName: 'DIV', isContentEditable: true })).toBe(true)
    expect(isTypingTarget({ tagName: 'LI' })).toBe(false)
    expect(isTypingTarget(null)).toBe(false)
  })
})
