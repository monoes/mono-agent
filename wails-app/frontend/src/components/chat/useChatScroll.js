import { useCallback, useEffect, useRef, useState } from 'react'

// Auto-follow only while the reader is within 80px of the bottom (plan
// §"Layout and interaction") — otherwise new content must never yank the
// viewport out from under someone reading back through history.
const FOLLOW_THRESHOLD_PX = 80

// Smooth-scrolling via Element.scrollTo's `behavior` option is a separate
// browser feature from CSS animations/transitions — index.css's global
// prefers-reduced-motion rule (zeroing animation/transition durations) has
// no effect on it at all, so it must be checked here directly (plan
// gate: "reduced motion"). Checked fresh each call rather than cached: the
// OS setting can change while the panel is open.
function prefersReducedMotion() {
  try {
    return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}

// useChatScroll tracks whether the reader is currently "following" the
// bottom of a scrollable container, auto-scrolling on new content only
// while they are, and counting unread activity instead once they've
// scrolled away. `signal` is any value that changes when new content
// arrives (e.g. a running total of messages + live event count) — the hook
// reacts to it changing, not to what it actually contains.
export function useChatScroll(signal) {
  const containerRef = useRef(null)
  const [isFollowing, setIsFollowing] = useState(true)
  const [unreadCount, setUnreadCount] = useState(0)
  // Mirrors isFollowing for the signal-driven effect below, which must
  // read the CURRENT value without depending on it directly — depending on
  // isFollowing there would fire the effect on every scroll, not just on
  // new content.
  const isFollowingRef = useRef(true)
  isFollowingRef.current = isFollowing

  const checkFollowing = useCallback(() => {
    const el = containerRef.current
    if (!el) return true
    return el.scrollHeight - el.scrollTop - el.clientHeight <= FOLLOW_THRESHOLD_PX
  }, [])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const onScroll = () => setIsFollowing(checkFollowing())
    el.addEventListener('scroll', onScroll)
    return () => el.removeEventListener('scroll', onScroll)
  }, [checkFollowing])

  // Skips its first run: the initial render isn't "new content arriving",
  // it's just the starting state, so mount must never force-scroll a
  // container whose actual position hasn't been read yet.
  const mountedRef = useRef(false)
  useEffect(() => {
    if (!mountedRef.current) { mountedRef.current = true; return }
    const el = containerRef.current
    if (!el) return
    if (isFollowingRef.current) {
      el.scrollTo({ top: el.scrollHeight, behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
      setUnreadCount(0)
    } else {
      setUnreadCount(c => c + 1)
    }
    // Deliberately signal-only: isFollowingRef is read fresh via the ref,
    // not a dependency, so a plain scroll (which changes isFollowing but
    // not signal) never re-triggers this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signal])

  const jumpToLatest = useCallback(() => {
    const el = containerRef.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
    setIsFollowing(true)
    setUnreadCount(0)
  }, [])

  return { containerRef, isFollowing, unreadCount, jumpToLatest }
}
