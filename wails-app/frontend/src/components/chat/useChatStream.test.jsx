// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'

// Mock at the services/api.js boundary (one layer above the wailsjs mocking
// api.test.js already does) — useChatStream's own tests should not have to
// re-verify api.js's JSON-parsing behavior, only that the hook calls it
// correctly and reacts to what it returns.
const eventListeners = new Map()
function fakeSubscribe(name, cb) {
  const list = eventListeners.get(name) ?? []
  list.push(cb)
  eventListeners.set(name, list)
  return () => {
    eventListeners.set(name, (eventListeners.get(name) ?? []).filter((f) => f !== cb))
  }
}
function emit(name, payload) {
  for (const cb of eventListeners.get(name) ?? []) cb(payload)
}

const getChatEvents = vi.fn()
vi.mock('../../services/api.js', () => ({
  api: { getChatEvents: (...args) => getChatEvents(...args) },
  onChatEvent: (cb) => fakeSubscribe('chat:event', cb),
}))

import { useChatStream } from './useChatStream.js'

function page(items, hasMore = false) {
  return { items, hasMore, lastCommittedSeq: items.length ? items[items.length - 1].seq : 0 }
}

function ev(type, payload, seq, over = {}) {
  return { version: 1, profileId: 'default', conversationId: 'conv-1', turnId: 'turn-1', seq, at: 't', type, payload, ...over }
}

beforeEach(() => {
  eventListeners.clear()
  getChatEvents.mockReset()
})

describe('useChatStream', () => {
  it('is idle with no conversation/turn selected', () => {
    const { result } = renderHook(() => useChatStream({ conversationId: '', turnId: '' }))
    expect(result.current.scope).toBeNull()
    expect(getChatEvents).not.toHaveBeenCalled()
  })

  it('hydrates from GetChatEvents and applies the fetched history in order', async () => {
    getChatEvents.mockResolvedValueOnce(page([
      ev('assistant.delta', { partId: 'p1', text: 'hel' }, 1),
      ev('assistant.delta', { partId: 'p1', text: 'lo' }, 2),
    ]))

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))

    await waitFor(() => expect(result.current.parts).toHaveLength(1))
    expect(result.current.parts[0].text).toBe('hello')
    expect(getChatEvents).toHaveBeenCalledWith('conv-1', 'turn-1', 0, 200)
  })

  it('paginates the backlog rather than assuming one page holds everything', async () => {
    getChatEvents
      .mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'a' }, 1)], true))
      .mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'b' }, 2)], false))

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))

    await waitFor(() => expect(result.current.parts[0]?.text).toBe('ab'))
    expect(getChatEvents).toHaveBeenNthCalledWith(1, 'conv-1', 'turn-1', 0, 200)
    expect(getChatEvents).toHaveBeenNthCalledWith(2, 'conv-1', 'turn-1', 1, 200)
  })

  it('applies a live event that arrives after hydration completes', async () => {
    getChatEvents.mockResolvedValueOnce(page([]))
    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))
    await waitFor(() => expect(getChatEvents).toHaveBeenCalledTimes(1))

    act(() => emit('chat:event', ev('assistant.delta', { partId: 'p1', text: 'live' }, 1)))

    await waitFor(() => expect(result.current.parts[0]?.text).toBe('live'))
  })

  it('buffers live events that arrive mid-hydration instead of dropping them', async () => {
    let resolveHydration
    getChatEvents.mockReturnValueOnce(new Promise((resolve) => { resolveHydration = resolve }))

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))

    // A live event fires while the initial fetch is still pending — must
    // not be lost, and must not apply out of order ahead of history.
    act(() => emit('chat:event', ev('assistant.delta', { partId: 'p1', text: '-live' }, 2)))
    expect(result.current.parts).toEqual([])

    await act(async () => { resolveHydration(page([ev('assistant.delta', { partId: 'p1', text: 'hist' }, 1)])) })

    await waitFor(() => expect(result.current.parts[0]?.text).toBe('hist-live'))
  })

  it('fetches a missing gap before applying a live event that jumps ahead', async () => {
    getChatEvents
      .mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'a' }, 1)])) // hydration
      .mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'b' }, 2)])) // gap fill

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))
    await waitFor(() => expect(result.current.parts[0]?.text).toBe('a'))

    // Live event at seq 4 arrives, skipping 2 and 3 — the hook must go back
    // for the gap rather than rendering seq 4 immediately. Both the gap
    // fetch and the jump-ahead event settle within the same awaited act(),
    // so the observable end state is the fully-caught-up text in the
    // correct order (never "ad", which would mean the gap was skipped).
    await act(async () => { emit('chat:event', ev('assistant.delta', { partId: 'p1', text: 'd' }, 4)) })

    await waitFor(() => expect(result.current.parts[0]?.text).toBe('abd'))
    expect(getChatEvents).toHaveBeenCalledWith('conv-1', 'turn-1', 1, 200)
  })

  it('ignores a duplicate live event already covered by hydration', async () => {
    getChatEvents.mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'hello' }, 1)]))
    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))
    await waitFor(() => expect(result.current.parts[0]?.text).toBe('hello'))

    act(() => emit('chat:event', ev('assistant.delta', { partId: 'p1', text: 'hello' }, 1)))
    expect(result.current.parts[0].text).toBe('hello') // not doubled
  })

  it('ignores events for a different conversation/turn than currently scoped', async () => {
    getChatEvents.mockResolvedValue(page([]))
    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))
    await waitFor(() => expect(getChatEvents).toHaveBeenCalled())

    act(() => emit('chat:event', ev('assistant.delta', { partId: 'p1', text: 'wrong turn' }, 1, { turnId: 'turn-other' })))
    expect(result.current.parts).toEqual([])
  })

  it('a stale in-flight fetch for a superseded turn never applies to the new turn', async () => {
    let resolveFirst
    getChatEvents.mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve }))

    const { result, rerender } = renderHook(
      ({ turnId }) => useChatStream({ conversationId: 'conv-1', turnId }),
      { initialProps: { turnId: 'turn-1' } },
    )

    getChatEvents.mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'second' }, 1, { turnId: 'turn-2' })]))
    rerender({ turnId: 'turn-2' })
    await waitFor(() => expect(result.current.parts[0]?.text).toBe('second'))

    // The first turn's fetch finally resolves — must not clobber turn-2's
    // already-applied state.
    await act(async () => { resolveFirst(page([ev('assistant.delta', { partId: 'p1', text: 'stale' }, 1)])) })
    expect(result.current.scope).toEqual({ conversationId: 'conv-1', turnId: 'turn-2' })
    expect(result.current.parts[0].text).toBe('second')
  })

  it('resets to idle when the conversation/turn is cleared', async () => {
    getChatEvents.mockResolvedValueOnce(page([ev('assistant.delta', { partId: 'p1', text: 'x' }, 1)]))
    const { result, rerender } = renderHook(
      ({ conversationId, turnId }) => useChatStream({ conversationId, turnId }),
      { initialProps: { conversationId: 'conv-1', turnId: 'turn-1' } },
    )
    await waitFor(() => expect(result.current.parts).toHaveLength(1))

    rerender({ conversationId: '', turnId: '' })
    expect(result.current.scope).toBeNull()
    expect(result.current.parts).toEqual([])
  })

  it('surfaces a notice when turn.finished reports historySaved:false', async () => {
    // historySaved:false means the live transcript just applied IS
    // authoritative and complete, but durable persistence fell behind —
    // there's nothing to re-fetch or reconcile (a refetch could only ever
    // return LESS than what's already shown). The only correct response is
    // warning the user this turn might not fully survive a reopen.
    getChatEvents.mockResolvedValueOnce(page([
      ev('turn.finished', { status: 'completed', reason: 'end_turn', exitCode: 0, historySaved: false }, 1),
    ]))

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))

    await waitFor(() => expect(result.current.terminal).not.toBeNull())
    expect(result.current.notices).toHaveLength(1)
    expect(result.current.notices[0]).toMatchObject({ severity: 'warning' })
    expect(result.current.notices[0].message).toMatch(/history|reopen|saved/i)
  })

  it('does not surface a notice when turn.finished reports historySaved:true', async () => {
    getChatEvents.mockResolvedValueOnce(page([
      ev('turn.finished', { status: 'completed', reason: 'end_turn', exitCode: 0, historySaved: true }, 1),
    ]))

    const { result } = renderHook(() => useChatStream({ conversationId: 'conv-1', turnId: 'turn-1' }))

    await waitFor(() => expect(result.current.terminal).not.toBeNull())
    expect(result.current.notices).toEqual([])
  })

  it('two independent hook instances do not cross-talk', async () => {
    getChatEvents.mockResolvedValue(page([]))
    const a = renderHook(() => useChatStream({ conversationId: 'conv-a', turnId: 'turn-a' }))
    const b = renderHook(() => useChatStream({ conversationId: 'conv-b', turnId: 'turn-b' }))
    await waitFor(() => expect(getChatEvents).toHaveBeenCalledTimes(2))

    act(() => emit('chat:event', ev('assistant.delta', { partId: 'p1', text: 'for-a' }, 1, { conversationId: 'conv-a', turnId: 'turn-a' })))

    expect(a.result.current.parts[0]?.text).toBe('for-a')
    expect(b.result.current.parts).toEqual([])
  })
})
