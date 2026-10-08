// FLIP motion for the board: after each render, a card whose box moved slides
// from its old place to the new one. Positions are layout (offset) coordinates
// in scroll-content space, so column scrolling and an in-flight transform never
// read as movement. Nothing animates after a resize or while the page was
// hidden. prefers-reduced-motion is followed live; also off where the Web
// Animations API is missing.
import { useEffect, useLayoutEffect, useRef } from 'react'

const MS = 220
const QUERY = '(prefers-reduced-motion: reduce)'

function mediaQuery() {
  try { return window.matchMedia?.(QUERY) ?? null } catch { return null }
}

function layoutPos(el, root) {
  let x = 0
  let y = 0
  for (let n = el; n && n !== root; n = n.offsetParent) {
    x += n.offsetLeft
    y += n.offsetTop
  }
  return { x, y }
}

// useReducedMotion returns a ref that follows prefers-reduced-motion live
// (true where the preference cannot be read).
export function useReducedMotion() {
  const reduced = useRef(null)
  if (reduced.current === null) reduced.current = mediaQuery()?.matches ?? true

  useEffect(() => {
    const mq = mediaQuery()
    if (!mq) return undefined
    const onChange = e => { reduced.current = !!e.matches }
    reduced.current = !!mq.matches
    if (mq.addEventListener) mq.addEventListener('change', onChange)
    else mq.addListener?.(onChange)
    return () => {
      if (mq.removeEventListener) mq.removeEventListener('change', onChange)
      else mq.removeListener?.(onChange)
    }
  }, [])
  return reduced
}

export function useFlip(rootRef, enabled = true) {
  const prev = useRef(new Map())
  const prevSize = useRef(null)
  const reduced = useReducedMotion()

  // No dependency list: any render may have moved a card.
  useLayoutEffect(() => {
    const root = rootRef.current
    if (!root) return
    if (root.getClientRects().length === 0) { // hidden: forget, re-baseline on return
      prev.current = new Map()
      prevSize.current = null
      return
    }
    const size = `${root.clientWidth}x${root.clientHeight}`
    const animate = enabled && !reduced.current && prevSize.current === size
    prevSize.current = size
    const next = new Map()
    root.querySelectorAll('[data-task-id]').forEach(el => {
      const pos = layoutPos(el, root)
      next.set(el.dataset.taskId, pos)
      const was = prev.current.get(el.dataset.taskId)
      if (!animate || !was || typeof el.animate !== 'function') return
      const dx = was.x - pos.x
      const dy = was.y - pos.y
      if (!dx && !dy) return
      el.animate(
        [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }],
        { duration: MS, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
      )
    })
    prev.current = next
  })
}
