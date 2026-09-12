// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useChatScroll } from './useChatScroll.js'

// jsdom computes no real layout — scrollHeight/clientHeight/scrollTop are
// always 0 unless stubbed, so every test sets up its own fake geometry via
// defineProperty (the standard way to unit-test scroll behavior in jsdom).
function makeContainer({ scrollHeight = 1000, clientHeight = 500, scrollTop = 500 } = {}) {
  const el = document.createElement('div')
  Object.defineProperty(el, 'scrollHeight', { value: scrollHeight, configurable: true })
  Object.defineProperty(el, 'clientHeight', { value: clientHeight, configurable: true })
  Object.defineProperty(el, 'scrollTop', { value: scrollTop, writable: true, configurable: true })
  el.scrollTo = vi.fn((opts) => { el.scrollTop = typeof opts === 'object' ? opts.top : opts })
  return el
}

describe('useChatScroll', () => {
  it('starts following (at the bottom) by default', () => {
    const { result } = renderHook(() => useChatScroll(0))
    expect(result.current.isFollowing).toBe(true)
    expect(result.current.unreadCount).toBe(0)
  })

  it('stays following and auto-scrolls when new content arrives within 80px of the bottom', () => {
    const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 490 }) // 10px from bottom
    const { result, rerender } = renderHook(({ signal }) => {
      const scroll = useChatScroll(signal)
      scroll.containerRef.current = container
      return scroll
    }, { initialProps: { signal: 0 } })

    rerender({ signal: 1 })
    expect(result.current.isFollowing).toBe(true)
    expect(container.scrollTo).toHaveBeenCalled()
    expect(result.current.unreadCount).toBe(0)
  })

  it('stops following once the reader scrolls more than 80px away from the bottom', () => {
    const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 500 })
    const { result } = renderHook(() => {
      const scroll = useChatScroll(0)
      scroll.containerRef.current = container
      return scroll
    })

    container.scrollTop = 200 // now 300px from the bottom
    act(() => { container.dispatchEvent(new Event('scroll')) })

    expect(result.current.isFollowing).toBe(false)
  })

  it('counts unread activity instead of auto-scrolling once the reader is not following', () => {
    const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 200 })
    const { result, rerender } = renderHook(({ signal }) => {
      const scroll = useChatScroll(signal)
      scroll.containerRef.current = container
      return scroll
    }, { initialProps: { signal: 0 } })

    act(() => { container.dispatchEvent(new Event('scroll')) }) // 300px away -> not following
    expect(result.current.isFollowing).toBe(false)

    rerender({ signal: 1 })
    rerender({ signal: 2 })
    expect(result.current.unreadCount).toBe(2)
    expect(container.scrollTo).not.toHaveBeenCalled()
  })

  it('jumpToLatest scrolls to bottom, resumes following, and clears unread count', () => {
    const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 200 })
    const { result, rerender } = renderHook(({ signal }) => {
      const scroll = useChatScroll(signal)
      scroll.containerRef.current = container
      return scroll
    }, { initialProps: { signal: 0 } })

    act(() => { container.dispatchEvent(new Event('scroll')) })
    rerender({ signal: 1 })
    expect(result.current.unreadCount).toBeGreaterThan(0)

    act(() => { result.current.jumpToLatest() })
    expect(container.scrollTo).toHaveBeenCalled()
    expect(result.current.isFollowing).toBe(true)
    expect(result.current.unreadCount).toBe(0)
  })

  // index.css's global prefers-reduced-motion rule only zeroes CSS
  // animation/transition durations — it has no effect on the JS-driven
  // Element.scrollTo({behavior:'smooth'}) API, so this hook must check the
  // media query itself or auto-scroll always animates regardless of the
  // OS setting (plan gate: "reduced motion").
  it('auto-scroll uses an instant jump, not smooth animation, when the OS prefers reduced motion', () => {
    const matchMediaMock = vi.fn().mockReturnValue({ matches: true })
    vi.stubGlobal('matchMedia', matchMediaMock)
    try {
      const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 490 })
      const { rerender } = renderHook(({ signal }) => {
        const scroll = useChatScroll(signal)
        scroll.containerRef.current = container
        return scroll
      }, { initialProps: { signal: 0 } })

      rerender({ signal: 1 })
      expect(matchMediaMock).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)')
      expect(container.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'auto' }))
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('jumpToLatest also respects reduced motion', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: true }))
    try {
      const container = makeContainer({ scrollHeight: 1000, clientHeight: 500, scrollTop: 200 })
      const { result } = renderHook(() => {
        const scroll = useChatScroll(0)
        scroll.containerRef.current = container
        return scroll
      })

      act(() => { result.current.jumpToLatest() })
      expect(container.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'auto' }))
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
