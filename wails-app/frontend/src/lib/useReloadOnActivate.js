import { useEffect, useRef } from 'react'

// Tab pages stay mounted (App.jsx toggles CSS visibility) so local state and
// scroll position survive navigation. This re-runs `reload` each time the
// page becomes active again — not on first mount, which loads on its own.
export function useReloadOnActivate(isActive, reload) {
  const wasActive = useRef(isActive)
  const latest = useRef(reload)
  latest.current = reload
  useEffect(() => {
    if (isActive && !wasActive.current) latest.current()
    wasActive.current = isActive
  }, [isActive])
}
