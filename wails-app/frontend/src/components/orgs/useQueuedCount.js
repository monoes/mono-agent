// Polls how many messages wait in the selected org's offline queue (C-35),
// for the count on the Queued tab. `org queued` only reads inbox.jsonl, so
// one org every poll is cheap; a running org drains its queue, which the
// next poll shows as the count going back to zero.
import { useCallback, useEffect, useState } from 'react'
import { api } from '../../services/api.js'

export const QUEUED_POLL_MS = 20_000

/** queuedCount reads a message count from an `org queued` answer; 0 on an error. */
export function queuedCount(res) {
  if (!res || res.error) return 0
  return Array.isArray(res.messages) ? res.messages.length : 0
}

/** [count, setCount] for orgName; the Queued tab reports fresher counts through setCount. */
export default function useQueuedCount(orgName, enabled = true, intervalMs = QUEUED_POLL_MS) {
  const [counts, setCounts] = useState({})

  useEffect(() => {
    if (!enabled || !orgName) return
    let cancelled = false
    // A tick that arrives while the last read is still out is skipped, so a
    // slow CLI never stacks subprocesses (as in useNeedsYouCounts).
    let inFlight = false
    const poll = async () => {
      if (inFlight) return
      inFlight = true
      try {
        let res = null
        try {
          res = await api.listOrgQueuedMessages(orgName)
        } catch {
          // Counted as none; the tab itself shows the error.
        }
        const n = queuedCount(res)
        if (!cancelled) setCounts(prev => (prev[orgName] === n ? prev : { ...prev, [orgName]: n }))
      } finally {
        inFlight = false
      }
    }
    poll()
    const iv = setInterval(poll, intervalMs)
    return () => { cancelled = true; clearInterval(iv) }
  }, [orgName, enabled, intervalMs])

  const setCount = useCallback((n) => {
    if (!orgName) return
    setCounts(prev => (prev[orgName] === n ? prev : { ...prev, [orgName]: n }))
  }, [orgName])
  return [counts[orgName] || 0, setCount]
}
