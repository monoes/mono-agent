import { useCallback, useEffect, useState } from 'react'
import { api } from '../../services/api.js'

// Coder mode (#203) state for the chat panel: the CLI's `coder status`
// (enabled / ready / defaults) and the recent workspaces. Everything comes
// from monoagentcli through the api wrappers; nothing is decided here
// beyond what to show.

export const CODER_RUNTIME = 'claude'

// folderName is the last path segment ("…/20260927-brisk-otter").
export function folderName(path) {
  if (!path) return ''
  const parts = String(path).replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || path
}

export function isCoderConversation(c) {
  return c?.mode === 'coder'
}

// missingText explains a not-ready status: which monomind capabilities
// coder mode still needs.
export function missingText(status) {
  const missing = status?.missingCapabilities || []
  return missing.length ? `needs monomind update (missing: ${missing.join(', ')})` : 'needs monomind update'
}

// useCoderStatus loads `coder status` once `active` (the panel is open and
// can offer coder mode), and again on refresh(). A failed call (e.g. an
// older monoagentcli without `coder`, or no Wails bridge at all) reads as
// "not enabled" — started inside a promise so a synchronous throw lands
// in the same catch.
export function useCoderStatus(active) {
  const [status, setStatus] = useState(null)
  const refresh = useCallback(() => {
    return Promise.resolve().then(() => api.coderStatus()).then(setStatus).catch(() => setStatus(null))
  }, [])
  useEffect(() => {
    if (active) refresh()
  }, [active, refresh])
  return { status, refresh }
}

// useRecentWorkspaces loads `coder workspace list` while `active`.
export function useRecentWorkspaces(active) {
  const [recent, setRecent] = useState([])
  useEffect(() => {
    if (!active) return
    let current = true
    Promise.resolve().then(() => api.coderWorkspaceList())
      .then(list => { if (current) setRecent((Array.isArray(list) ? list : []).filter(w => w.exists !== false)) })
      .catch(() => { if (current) setRecent([]) })
    return () => { current = false }
  }, [active])
  return recent
}
