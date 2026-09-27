import { useEffect, useState } from 'react'

// Shared by ToolActivityCard and NativeToolCard.

// Same self-contained tick pattern as TurnStatus.jsx's own live "idle for
// Ns" clock: own `now`, tick once a second only while `active`, stop
// entirely once the call completes so a finished card never re-renders on
// a timer it no longer needs.
export function useTicker(active) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [active])
  return now
}

export function formatDuration(ms) {
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  const totalSeconds = Math.round(ms / 1000)
  return `${Math.floor(totalSeconds / 60)}m ${totalSeconds % 60}s`
}

export function copyToClipboard(text) {
  try { navigator.clipboard?.writeText(text) } catch { /* clipboard unavailable — copy is a convenience, not required */ }
}
