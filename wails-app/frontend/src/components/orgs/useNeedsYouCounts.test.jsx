// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, cleanup } from '@testing-library/react'
import useNeedsYouCounts from './useNeedsYouCounts.js'
import { api } from '../../services/api.js'

vi.mock('../../services/api.js', () => ({
  api: {
    listNeedsYou: vi.fn(),
    getOrgQuestions: vi.fn(),
    getOrgGates: vi.fn(),
    getOrgApprovals: vi.fn(),
  },
}))

// Hands out a resolver per call so a poll can be held mid-flight, the way a
// slow `org autonomy needs-you` subprocess holds one for up to the 60s CLI
// timeout.
function deferredListNeedsYou() {
  const resolvers = []
  api.listNeedsYou.mockImplementation(() => new Promise(resolve => { resolvers.push(resolve) }))
  return resolvers
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  cleanup()
})

describe('useNeedsYouCounts', () => {
  // The poll walks every org sequentially and each fetch can take up to the
  // CLI timeout, so an unguarded interval stacks subprocesses -- and because
  // every poll ends by replacing the whole map, a slower older poll lands
  // last and freezes the badges on stale counts.
  it('never starts a second poll while one is still in flight', async () => {
    const resolvers = deferredListNeedsYou()
    renderHook(() => useNeedsYouCounts(['growth', 'ops'], true, 20_000))

    // First poll is under way on the first org.
    expect(api.listNeedsYou).toHaveBeenCalledTimes(1)

    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(api.listNeedsYou).toHaveBeenCalledTimes(1)

    // Once it finishes, the next tick polls again.
    await act(async () => {
      resolvers.forEach(r => r({ v: 1, items: [] }))
    })
    expect(api.listNeedsYou).toHaveBeenCalledTimes(2)

    await act(async () => { resolvers.slice(1).forEach(r => r({ v: 1, items: [] })) })
    await act(async () => { vi.advanceTimersByTime(20_000) })
    expect(api.listNeedsYou).toHaveBeenCalledTimes(3)
  })

  it('reports each org’s count once a poll completes', async () => {
    api.listNeedsYou.mockImplementation(org => Promise.resolve({
      v: 1, items: org === 'growth' ? [{ kind: 'gate' }, { kind: 'question' }] : [],
    }))
    const { result } = renderHook(() => useNeedsYouCounts(['growth', 'ops'], true, 20_000))
    await act(async () => {})
    expect(result.current[0]).toEqual({ growth: 2, ops: 0 })
  })
})
