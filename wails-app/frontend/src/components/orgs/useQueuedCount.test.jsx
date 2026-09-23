// @vitest-environment jsdom
// C-35: the Queued tab counts the selected org's waiting messages.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, cleanup } from '@testing-library/react'
import useQueuedCount, { queuedCount } from './useQueuedCount.js'
import { api } from '../../services/api.js'

vi.mock('../../services/api.js', () => ({
  api: { listOrgQueuedMessages: vi.fn() },
}))

const answer = (n) => ({ v: 1, org: 'growth', count: n, messages: Array.from({ length: n }, (_, i) => ({ messageId: `m${i}` })) })

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
  cleanup()
})

describe('queuedCount', () => {
  it('counts messages, and an error or nothing as none', () => {
    expect(queuedCount(answer(3))).toBe(3)
    expect(queuedCount({ error: 'no such org' })).toBe(0)
    expect(queuedCount(null)).toBe(0)
    expect(queuedCount({ v: 1 })).toBe(0)
  })
})

describe('useQueuedCount', () => {
  it('reads the selected org, and follows it as it drains', async () => {
    api.listOrgQueuedMessages.mockResolvedValue(answer(2))
    const { result } = renderHook(() => useQueuedCount('growth', true, 20_000))
    await act(async () => {})
    expect(api.listOrgQueuedMessages).toHaveBeenCalledWith('growth')
    expect(result.current[0]).toBe(2)

    api.listOrgQueuedMessages.mockResolvedValue(answer(0))
    await act(async () => { vi.advanceTimersByTime(20_000) })
    expect(result.current[0]).toBe(0)
  })

  it('never starts a second read while one is still out', async () => {
    const resolvers = []
    api.listOrgQueuedMessages.mockImplementation(() => new Promise(r => { resolvers.push(r) }))
    renderHook(() => useQueuedCount('growth', true, 20_000))
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(api.listOrgQueuedMessages).toHaveBeenCalledTimes(1)
    await act(async () => { resolvers[0](answer(1)) })
    await act(async () => { vi.advanceTimersByTime(20_000) })
    expect(api.listOrgQueuedMessages).toHaveBeenCalledTimes(2)
  })

  it('keeps each org its own count when the selection changes', async () => {
    api.listOrgQueuedMessages.mockImplementation(async (org) => answer(org === 'growth' ? 4 : 1))
    const { result, rerender } = renderHook(({ org }) => useQueuedCount(org, true, 20_000), { initialProps: { org: 'growth' } })
    await act(async () => {})
    expect(result.current[0]).toBe(4)
    rerender({ org: 'ops' })
    await act(async () => {})
    expect(result.current[0]).toBe(1)
  })

  it('does nothing when disabled or with no org', async () => {
    renderHook(() => useQueuedCount('growth', false))
    renderHook(() => useQueuedCount('', true))
    await act(async () => {})
    expect(api.listOrgQueuedMessages).not.toHaveBeenCalled()
  })

  it('takes a fresher count from the open tab', async () => {
    api.listOrgQueuedMessages.mockResolvedValue(answer(2))
    const { result } = renderHook(() => useQueuedCount('growth', true, 20_000))
    await act(async () => {})
    act(() => { result.current[1](5) })
    expect(result.current[0]).toBe(5)
  })
})
