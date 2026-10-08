// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render } from '@testing-library/react'
import { applyMove, normalizeBoard } from './taskModel.js'
import { useMarks, DONE_MS, CLAIM_MS } from './useMarks.js'

const card = (id, status, extra = {}) => ({ id, title: `t${id}`, status, position: id, claim: null, ...extra })
const board = (tasks) => normalizeBoard({ profile: { id: 'p', name: 'P' }, tasks })
const start = () => board({ review: [card(1, 'review')], in_progress: [card(2, 'in_progress')] })

let seen
function Probe({ b }) { seen = useMarks(b); return null }

function mq(matches) {
  const listeners = new Set()
  const q = { matches, addEventListener: (_, f) => listeners.add(f), removeEventListener: (_, f) => listeners.delete(f), fire(m) { q.matches = m; listeners.forEach(f => f({ matches: m })) } }
  window.matchMedia = () => q
  return q
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers(); delete window.matchMedia })

describe('useMarks', () => {
  it('marks Done for its own time, whatever boards follow', () => {
    mq(false)
    const { rerender } = render(<Probe b={start()} />)
    expect(seen.done.size).toBe(0)
    const done = applyMove(start(), 1, 'done', {})
    rerender(<Probe b={done} />)
    expect([...seen.done]).toEqual([1])
    act(() => { vi.advanceTimersByTime(DONE_MS - 100) })
    rerender(<Probe b={{ ...done, rev: 9 }} />) // the confirming read
    expect([...seen.done]).toEqual([1])
    act(() => { vi.advanceTimersByTime(100) })
    expect(seen.done.size).toBe(0)
  })

  it('marks a claim, which lasts longer than the Done glow', () => {
    mq(false)
    const { rerender } = render(<Probe b={start()} />)
    rerender(<Probe b={board({ review: [card(1, 'review')], in_progress: [card(2, 'in_progress', { claim: { by: 'bot', until: 'x' } })] })} />)
    expect([...seen.claimed]).toEqual([2])
    act(() => { vi.advanceTimersByTime(DONE_MS) })
    expect(seen.claimed.size).toBe(1)
    act(() => { vi.advanceTimersByTime(CLAIM_MS) })
    expect(seen.claimed.size).toBe(0)
  })

  it('makes no marks under reduced motion, and follows the preference live', () => {
    const q = mq(true)
    const { rerender } = render(<Probe b={start()} />)
    rerender(<Probe b={applyMove(start(), 1, 'done', {})} />)
    expect(seen.done.size).toBe(0)
    q.fire(false)
    rerender(<Probe b={start()} />)
    rerender(<Probe b={applyMove(start(), 1, 'done', {})} />)
    expect([...seen.done]).toEqual([1])
  })

  it('clears its timers on unmount', () => {
    mq(false)
    const { rerender, unmount } = render(<Probe b={start()} />)
    rerender(<Probe b={applyMove(start(), 1, 'done', {})} />)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
