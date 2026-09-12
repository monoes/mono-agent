import { useEffect, useRef, useState } from 'react'
import { api } from '../../services/api.js'
import { detectArtifactCandidate, resolveArtifact } from './chatArtifacts.js'

// Resolves chat-result artifacts (chatArtifacts.js) for every tool call a
// panel has ever rendered — finalized turns plus the live streaming turn —
// lazily and once per (turnId, callId) pair. Keyed on the PAIR, not the
// bare callId: the agent backend's callId comes straight from the external
// monomind protocol's own per-event id with no cross-turn uniqueness
// guarantee (plan: "Tool identity is (turnId,callId), never array position
// or name" — a requirement that only makes sense if callId alone CAN
// collide across turns). Caching by callId alone would let a later turn
// that happens to reuse an earlier turn's callId silently reuse its
// resolved artifact — wrong name, wrong Copy-ID value, wrong Open target.
// Callers pass `entries.push({turnId, call})` pairs rather than plain call
// objects for exactly this reason.
//
// Cached so a fast-moving live stream doesn't repeat a backend lookup on
// every re-render, and a call already resolved stays resolved when its
// turn is reopened later. A call is only ever cached once it's
// 'completed': one still 'started' is skipped (not cached as "no
// artifact") so it gets rechecked the moment it actually completes,
// instead of being judged prematurely on a result that doesn't exist yet.
//
// resolveArtifact's own null result is ambiguous — "confirmed gone" and
// "the lookup itself failed" (e.g. a transient hiccup on the same SQLite
// file the chat supervisor is concurrently writing to right as a tool call
// completes) are indistinguishable by the time api.js's guard() finishes
// swallowing a real error into the same null shape. Unlike chatArtifacts.js's
// own openArtifact click-time revalidation (which accepts that ambiguity
// for a re-check, since a click gives the user an obvious way to just try
// again), this is the *first* resolution — caching a transient failure's
// null here permanently hides the card for the rest of the session with no
// way to ever retry it. So a null gets exactly one automatic retry, after
// a short delay, before it is cached: long enough to give a same-moment
// write lock a real chance to clear, short enough the user barely notices.
// An unconditional retry-on-every-render would instead hammer a
// genuinely-nonexistent lookup forever, which is exactly what caching
// after the SECOND null still prevents. detectArtifactCandidate returning
// null is a separate, deterministic case — a pure function over
// already-known call data, not a lookup that can fail transiently — so it
// is still cached immediately with no retry.
const RESOLVE_RETRY_DELAY_MS = 300

export function useResolvedArtifacts(entries) {
  const [resolved, setResolved] = useState({}) // "turnId:callId" -> artifact | null
  const inFlightRef = useRef(new Set())
  // Pending retry timers, cancelled on unmount — without this, a panel
  // closed (or a turn's owning conversation navigated away from) mid-retry
  // would still fire its scheduled second attempt later, calling into a
  // component that no longer needs the answer.
  const retryTimersRef = useRef(new Set())
  useEffect(() => () => {
    retryTimersRef.current.forEach(id => clearTimeout(id))
    retryTimersRef.current.clear()
  }, [])

  useEffect(() => {
    entries.forEach(({ turnId, call }) => {
      if (call.status !== 'completed') return
      const key = `${turnId}:${call.callId}`
      if (key in resolved || inFlightRef.current.has(key)) return
      const candidate = detectArtifactCandidate(call)
      if (!candidate) {
        setResolved(prev => ({ ...prev, [key]: null }))
        return
      }
      inFlightRef.current.add(key)
      // Stays in inFlightRef for the whole attempt-then-retry window, not
      // just the first call — this effect has no dependency array (it
      // re-runs after every render of the owning component), so without
      // this the very next re-render during the retry delay would see
      // neither `key in resolved` nor an in-flight entry and launch a
      // second, overlapping attempt for the same key.
      //
      // .catch before .then: a rejection here (resolveArtifact's own
      // backend calls already degrade to null via api.js's guard(), so
      // this is a last-resort safety net, not the expected path) must
      // still reach the same retry/give-up logic as an ordinary null.
      const attempt = (attemptNumber) => {
        resolveArtifact(candidate, api)
          .catch(() => null)
          .then(artifact => {
            if (artifact || attemptNumber >= 2) {
              inFlightRef.current.delete(key)
              setResolved(prev => ({ ...prev, [key]: artifact }))
              return
            }
            const id = setTimeout(() => {
              retryTimersRef.current.delete(id)
              attempt(attemptNumber + 1)
            }, RESOLVE_RETRY_DELAY_MS)
            retryTimersRef.current.add(id)
          })
      }
      attempt(1)
    })
  })

  return resolved
}
