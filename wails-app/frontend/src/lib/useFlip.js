// FLIP motion for the board: after each render, a card whose box moved slides
// from its old place to the new one. Off under prefers-reduced-motion (read
// once when the page mounts) and where the Web Animations API is missing.
import { useLayoutEffect, useRef } from 'react'

const MS = 220

function reducedMotion() {
  try { return !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches } catch { return true }
}

export function useFlip(rootRef, enabled = true) {
  const prev = useRef(new Map())
  const reduced = useRef(null)
  if (reduced.current === null) reduced.current = reducedMotion()

  // No dependency list: any render may have moved a card.
  useLayoutEffect(() => {
    const root = rootRef.current
    if (!root) return
    const next = new Map()
    const animate = enabled && !reduced.current
    root.querySelectorAll('[data-task-id]').forEach(el => {
      const r = el.getBoundingClientRect()
      next.set(el.dataset.taskId, r)
      const was = prev.current.get(el.dataset.taskId)
      if (!animate || !was || typeof el.animate !== 'function') return
      const dx = was.left - r.left
      const dy = was.top - r.top
      if (!dx && !dy) return
      el.animate(
        [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }],
        { duration: MS, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
      )
    })
    prev.current = next
  })
}
