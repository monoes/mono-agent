import { describe, it, expect } from 'vitest'
import {
  STATUS, emptySummary, applyChatEvent, summaryFromTurns, totalCost, layoutBubbles, moveBubble,
  loadState, saveState, STORAGE_KEY, shouldCollapseOnBackdrop, monogram, newDraftKey,
} from './coderBubbles.js'

const ev = (type, turnId, payload = {}, at = '2026-09-29T10:00:00Z') => ({ type, turnId, payload, at, conversationId: 'c1' })

describe('applyChatEvent', () => {
  it('runs a turn from start to done and counts it unread while collapsed', () => {
    let s = applyChatEvent(emptySummary(), ev('turn.started', 't1'), false)
    expect(s.status).toBe(STATUS.working)
    expect(s.activeTurnId).toBe('t1')
    s = applyChatEvent(s, ev('usage.updated', 't1', { costUsd: 0.01 }), false)
    s = applyChatEvent(s, ev('usage.updated', 't1', { costUsd: 0.03 }), false)
    expect(totalCost(s)).toBeCloseTo(0.03) // a snapshot per turn, not summed
    s = applyChatEvent(s, ev('turn.finished', 't1', { status: 'completed' }), false)
    expect(s.status).toBe(STATUS.done)
    expect(s.unread).toBe(1)
    expect(s.activeTurnId).toBe('')
  })

  it('does not count unread while expanded, and maps failure and stop', () => {
    let s = applyChatEvent(emptySummary(), ev('turn.started', 't1'), true)
    s = applyChatEvent(s, ev('turn.finished', 't1', { status: 'failed' }), true)
    expect(s.status).toBe(STATUS.error)
    expect(s.unread).toBe(0)
    s = applyChatEvent(s, ev('turn.started', 't2'), true)
    s = applyChatEvent(s, ev('turn.finished', 't2', { status: 'cancelled' }), true)
    expect(s.status).toBe(STATUS.idle)
    s = applyChatEvent(s, ev('turn.started', 't3'), true)
    s = applyChatEvent(s, ev('turn.finished', 't3', { status: 'interrupted' }), true)
    expect(s.status).toBe(STATUS.error)
  })

  it('ignores a late finish for an older turn while a newer one runs', () => {
    let s = applyChatEvent(emptySummary(), ev('turn.started', 't2'), false)
    s = applyChatEvent(s, ev('turn.finished', 't1', { status: 'completed' }), false)
    expect(s.status).toBe(STATUS.working)
    expect(s.activeTurnId).toBe('t2')
  })

  it('sums costs across turns and ignores unrelated events', () => {
    let s = applyChatEvent(emptySummary(), ev('usage.updated', 't1', { costUsd: 0.02 }), false)
    s = applyChatEvent(s, ev('usage.updated', 't2', { costUsd: 0.05 }), false)
    expect(totalCost(s)).toBeCloseTo(0.07)
    expect(applyChatEvent(s, ev('assistant.delta', 't2'), false)).toBe(s)
    expect(applyChatEvent(s, ev('usage.updated', 't2', {}), false)).toBe(s)
  })
})

describe('summaryFromTurns', () => {
  it('re-attaches to a running turn and shows a failed last turn', () => {
    expect(summaryFromTurns([{ id: 't9', status: 'active', createdAt: 'x' }]).status).toBe(STATUS.working)
    expect(summaryFromTurns([{ id: 't9', status: 'active', ownedByThisInstance: false }]).status).toBe(STATUS.idle)
    expect(summaryFromTurns([{ id: 't9', status: 'failed' }]).status).toBe(STATUS.error)
    expect(summaryFromTurns([{ id: 't9', status: 'completed' }]).status).toBe(STATUS.idle)
    expect(summaryFromTurns([]).status).toBe(STATUS.idle)
  })
})

describe('layoutBubbles and moveBubble', () => {
  const b = n => Array.from({ length: n }, (_, i) => ({ key: `k${i}` }))
  it('keeps everything visible up to the max', () => {
    expect(layoutBubbles(b(6), '').overflow).toHaveLength(0)
  })
  it('overflows past the max and keeps the expanded bubble visible', () => {
    const list = b(9)
    const { visible, overflow } = layoutBubbles(list, '')
    expect(visible).toHaveLength(5)
    expect(overflow).toHaveLength(4)
    const withExp = layoutBubbles(list, 'k8')
    expect(withExp.visible.map(x => x.key)).toContain('k8')
    expect(withExp.visible).toHaveLength(5)
    expect(withExp.visible.length + withExp.overflow.length).toBe(9)
  })
  it('moves a bubble', () => {
    expect(moveBubble(b(3), 'k2', 'k0').map(x => x.key)).toEqual(['k2', 'k0', 'k1'])
    expect(moveBubble(b(3), 'nope', 'k0').map(x => x.key)).toEqual(['k0', 'k1', 'k2'])
  })
})

describe('persistence', () => {
  const memory = () => {
    const m = new Map()
    return { getItem: k => m.get(k) ?? null, setItem: (k, v) => m.set(k, v), m }
  }
  it('keeps real conversations and the dock side, drops drafts', () => {
    const s = memory()
    saveState({ side: 'left', bubbles: [{ key: 'draft-1', conversationId: '' }, { key: 'c1', conversationId: 'c1', cwd: '/w', model: 'opus', runtime: 'codex' }] }, s)
    expect(loadState(s)).toEqual({ side: 'left', bubbles: [{ key: 'c1', conversationId: 'c1', cwd: '/w', model: 'opus', runtime: 'codex' }] })
  })
  it('survives throwing or corrupt storage', () => {
    const throwing = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } }
    expect(loadState(throwing)).toEqual({ bubbles: [], side: 'right' })
    expect(() => saveState({ bubbles: [], side: 'right' }, throwing)).not.toThrow()
    const s = memory()
    s.setItem(STORAGE_KEY, '{nope')
    expect(loadState(s)).toEqual({ bubbles: [], side: 'right' })
  })
})

describe('shouldCollapseOnBackdrop', () => {
  it.each([
    [{ pressStartedOnBackdrop: true, selectionText: '', modalOpen: false }, true],
    [{ pressStartedOnBackdrop: false, selectionText: '', modalOpen: false }, false], // drag that ended outside
    [{ pressStartedOnBackdrop: true, selectionText: 'copied text', modalOpen: false }, false],
    [{ pressStartedOnBackdrop: true, selectionText: '  ', modalOpen: false }, true],
    [{ pressStartedOnBackdrop: true, selectionText: '', modalOpen: true }, false],
  ])('%o → %s', (input, want) => {
    expect(shouldCollapseOnBackdrop(input)).toBe(want)
  })
})

describe('monogram and draft keys', () => {
  it('makes two-letter labels', () => {
    expect(monogram('20260927-brisk-otter')).toBe('BO')
    expect(monogram('mono-agent')).toBe('MA')
    expect(monogram('api')).toBe('AP')
    expect(monogram('')).toBe('·')
  })
  it('makes unique draft keys', () => {
    expect(newDraftKey()).not.toBe(newDraftKey())
  })
})

describe('nowDoing', () => {
  it('names the latest running call and its target', async () => {
    const { nowDoing } = await import('./coderBubbles.js')
    expect(nowDoing({ calls: {} })).toBe('')
    expect(nowDoing({ calls: { a: { name: 'Read', status: 'completed', arguments: { file_path: 'x' } } } })).toBe('')
    expect(nowDoing({ calls: { a: { name: 'Edit', status: 'started', arguments: { file_path: 'src/app.go' } } } })).toBe('Edit · src/app.go')
    expect(nowDoing({ calls: { a: { name: 'Bash', status: 'started', arguments: '{"command":"go   test ./..."}' } } })).toBe('Bash · go test ./...')
    expect(nowDoing({ calls: { a: { name: 'Think', status: 'started', arguments: null } } })).toBe('Think')
  })
})

describe('applyChatEvent: a dynamic-org agent asking the user (#228)', () => {
  it('pulses the bubble until that agent moves on', () => {
    let s = applyChatEvent(emptySummary(), ev('turn.started', 't1'), false)
    s = applyChatEvent(s, ev('agent.message', 't1', { agentId: 'w1', direction: 'result', text: 'x' }), false)
    expect(s.status).toBe(STATUS.working)
    s = applyChatEvent(s, ev('agent.message', 't1', { agentId: 'w1', direction: 'question', text: 'Which cache?' }), false)
    expect(s.status).toBe(STATUS.needs)
    s = applyChatEvent(s, ev('agent.status', 't1', { agentId: 'w2', to: 'done' }), false)
    expect(s.status).toBe(STATUS.needs)
    s = applyChatEvent(s, ev('agent.status', 't1', { agentId: 'w1', to: 'working' }), false)
    expect(s.status).toBe(STATUS.working)
  })

  it('ignores a question from another turn', () => {
    const s = applyChatEvent(applyChatEvent(emptySummary(), ev('turn.started', 't2'), false), ev('agent.message', 't1', { agentId: 'w1', direction: 'question' }), false)
    expect(s.status).toBe(STATUS.working)
  })
})

describe('applyChatEvent: worker usage (#257)', () => {
  it('adds a worker\'s cost to the lead\'s instead of replacing it', () => {
    let s = applyChatEvent(emptySummary(), ev('turn.started', 't1'), false)
    s = applyChatEvent(s, ev('usage.updated', 't1', { costUsd: 0.1 }), false)
    s = applyChatEvent(s, ev('usage.updated', 't1', { agentId: 'w1', costUsd: 0.02 }), false)
    s = applyChatEvent(s, ev('usage.updated', 't1', { agentId: 'w1', costUsd: 0.03 }), false)
    s = applyChatEvent(s, ev('usage.updated', 't1', { costUsd: 0.12 }), false)
    expect(totalCost(s)).toBeCloseTo(0.15)
  })
})
