import { useEffect, useRef, useState } from 'react'

// Motion timing for the org stage (#228). Only events from the last
// FRESH_MS animate: opening a finished turn, or hydrating a long journal,
// shows the end state at once instead of replaying every flight.
export const FRESH_MS = 8000
export const FLIGHT_MS = 900
export const BUBBLE_MS = 4000
export const MAX_ACTIVE_FLIGHTS = 4

export function prefersReducedMotion() {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

// isFresh says whether something that happened at `at` is recent enough to
// animate. A time well in the future (another clock) isn't.
export function isFresh(at, now = Date.now()) {
  const t = Date.parse(at || '')
  return Number.isFinite(t) && t >= now - FRESH_MS && t <= now + FRESH_MS
}

// useStageMotion turns the stage's flights into what is on screen right
// now: the flights in the air (at most MAX_ACTIVE_FLIGHTS, the newest; the
// rest land without flying) and the speech bubbles a landed result leaves
// above its parent for BUBBLE_MS. With reduced motion nothing flies:
// results land at once.
export function useStageMotion(flights, { reducedMotion = false } = {}) {
  const [active, setActive] = useState([])
  const [bubbles, setBubbles] = useState([])
  const seen = useRef(new Set())
  const timers = useRef(new Set())
  const flying = useRef(0)

  // On unmount (or StrictMode's dev re-mount) drop every pending timer and
  // start over: nothing is left flying, and a re-mount flies what is
  // still fresh again.
  useEffect(() => {
    const pending = timers.current
    const seenIds = seen.current
    return () => {
      pending.forEach(clearTimeout)
      pending.clear()
      seenIds.clear()
      flying.current = 0
      setActive([])
      setBubbles([])
    }
  }, [])

  useEffect(() => {
    const list = flights || []
    const now = Date.now()
    const later = (fn, ms) => {
      const id = setTimeout(() => { timers.current.delete(id); fn() }, ms)
      timers.current.add(id)
    }
    const land = (f) => {
      if (f.kind !== 'result' || !f.text) return
      setBubbles(b => [...b.filter(x => x.nodeId !== f.to), { id: f.id, nodeId: f.to, text: f.text }])
      later(() => setBubbles(b => b.filter(x => x.id !== f.id)), BUBBLE_MS)
    }
    const fresh = []
    for (const f of list) {
      if (seen.current.has(f.id)) continue
      seen.current.add(f.id)
      if (isFresh(f.at, now)) fresh.push(f)
    }
    // When more arrive at once than may fly, the newest fly and the older
    // ones land straight away.
    const slots = reducedMotion ? 0 : Math.max(0, MAX_ACTIVE_FLIGHTS - flying.current)
    const firstFlying = Math.max(0, fresh.length - slots)
    fresh.forEach((f, i) => {
      if (i < firstFlying) {
        land(f)
        return
      }
      flying.current += 1
      setActive(a => [...a, f])
      later(() => {
        flying.current -= 1
        setActive(a => a.filter(x => x.id !== f.id))
        land(f)
      }, FLIGHT_MS)
    })
    // Forget flights the stage no longer keeps, so this stays small.
    const ids = new Set(list.map(f => f.id))
    for (const id of seen.current) if (!ids.has(id)) seen.current.delete(id)
  }, [flights, reducedMotion])

  return { active, bubbles }
}

// useThrottled hands back value at most once every ms: a burst of events
// (a hydrating journal, a chatty worker) re-renders the stage a few times a
// second, not once per event. The latest value always arrives.
export function useThrottled(value, ms) {
  const [shown, setShown] = useState(value)
  const last = useRef(0)
  const timer = useRef(null)
  const latest = useRef(value)
  latest.current = value
  useEffect(() => {
    if (value === shown) return undefined
    const wait = last.current + ms - Date.now()
    if (wait <= 0) {
      last.current = Date.now()
      setShown(value)
      return undefined
    }
    if (!timer.current) {
      timer.current = setTimeout(() => {
        timer.current = null
        last.current = Date.now()
        setShown(latest.current)
      }, wait)
    }
    return undefined
  }, [value, shown, ms])
  useEffect(() => () => clearTimeout(timer.current), [])
  return shown
}
