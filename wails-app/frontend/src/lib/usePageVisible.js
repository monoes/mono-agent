import { useEffect, useRef, useState } from 'react'

// Tracks document visibility so keep-alive pages can pause their pollers
// when the window is hidden (minimized, another Space, …). The Wails webview
// mirrors the OS window state into document.hidden. Pollers gate individual
// interval TICKS on this — the interval itself keeps running, but the tick
// body returns early while hidden, so a hidden app stops hitting the backend
// without tearing down and rebuilding timers on every visibility change.

export function usePageVisible() {
  const [visible, setVisible] = useState(() =>
    typeof document === 'undefined' ? true : !document.hidden
  )
  useEffect(() => {
    if (typeof document === 'undefined') return undefined
    const onChange = () => setVisible(!document.hidden)
    document.addEventListener('visibilitychange', onChange)
    return () => document.removeEventListener('visibilitychange', onChange)
  }, [])
  return visible
}

// Same as usePageVisible but returns a ref whose .current always holds the
// latest visibility — for gating interval tick bodies without adding
// visibility to the effect deps (which would recreate the interval).
export function usePageVisibleRef() {
  const visible = usePageVisible()
  const ref = useRef(visible)
  ref.current = visible
  return ref
}

// Runs `fn` once on each hidden → visible transition (not on mount) — the
// "catch up" half of tick gating, so a page that sat hidden comes back
// current instead of showing data one interval stale.
export function useVisibleCatchUp(fn) {
  const fnRef = useRef(fn)
  fnRef.current = fn
  const wasVisible = useRef(true)
  const visible = usePageVisible()
  useEffect(() => {
    if (visible && !wasVisible.current) fnRef.current()
    wasVisible.current = visible
  }, [visible])
}
