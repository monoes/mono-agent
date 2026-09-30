import { useCallback, useEffect, useRef } from 'react'
import { api, onOrgEventsClosed } from '../../services/api.js'
import { orgsToAutoOpen } from '../../lib/orgBubble.js'
import { restartOrgEvents } from '../../lib/orgEventStreams.js'
import { isOrgBubble } from '../../lib/coderBubbles.js'
import { onOpenOrgBubble } from '../../lib/appEvents.js'

export const WATCH_MS = 30_000
// At most this many running orgs are asked for their pending items per poll.
const MAX_NEEDS_CHECKS = 6

function orgRows(res) {
  return Array.isArray(res?.orgs) ? res.orgs : []
}

// useOrgBubbleWatch keeps running orgs and their bubbles in step (#229):
//   - a running org that waits on the person gets a bubble (collapsed,
//     amber), unless the person closed it for those same items;
//   - an open org bubble's event tail is restarted when its org starts a
//     new run (or starts at all), since a tail follows the run it found;
//   - "Open as bubble" anywhere (appEvents) opens one.
// It returns onCloseOrg for CoderBubbles.
export function useOrgBubbleWatch(store) {
  const dismissed = useRef({})
  const runs = useRef({})
  const closedTails = useRef({})
  const storeRef = useRef(store)
  storeRef.current = store

  useEffect(() => onOpenOrgBubble(({ org }) => { if (org) storeRef.current.openOrg(org) }), [])

  useEffect(() => onOrgEventsClosed(payload => {
    if (payload?.orgName) closedTails.current[payload.orgName] = true
  }), [])

  useEffect(() => {
    let cancelled = false
    let inFlight = false
    const poll = async () => {
      if (inFlight || cancelled || (typeof document !== 'undefined' && document.hidden)) return
      inFlight = true
      try {
        const summary = await api.getOrgSummary(true)
        if (cancelled) return
        const rows = orgRows(summary)
        const open = new Set(storeRef.current.bubbles.filter(isOrgBubble).map(b => b.orgName))

        // Open bubbles: restart a tail whose org started a new run.
        for (const name of open) {
          const row = rows.find(r => r.name === name)
          if (!row?.running) continue
          const st = await api.getOrgStatus(name)
          if (cancelled) return
          const run = st?.run || ''
          if ((runs.current[name] !== undefined && runs.current[name] !== run) || closedTails.current[name]) {
            closedTails.current[name] = false
            restartOrgEvents(name)
          }
          runs.current[name] = run
        }

        // Running orgs without a bubble: does anything wait on the person?
        const checked = []
        for (const r of rows.filter(r => r.running && !open.has(r.name)).slice(0, MAX_NEEDS_CHECKS)) {
          const res = await api.listNeedsYou(r.name)
          if (cancelled) return
          checked.push({ name: r.name, running: true, needs: Array.isArray(res?.items) ? res.items.length : 0 })
        }
        const { open: toOpen, dismissed: next } = orgsToAutoOpen(checked, open, dismissed.current)
        dismissed.current = { ...Object.fromEntries(Object.entries(dismissed.current).filter(([k]) => !checked.some(c => c.name === k))), ...next }
        for (const name of toOpen) storeRef.current.openOrg(name, { expand: false })
      } catch {
        // The next poll tries again.
      } finally {
        inFlight = false
      }
    }
    const first = setTimeout(poll, 5_000)
    const iv = setInterval(poll, WATCH_MS)
    return () => { cancelled = true; clearTimeout(first); clearInterval(iv) }
  }, [])

  return useCallback((org, needs) => {
    if (org && needs > 0) dismissed.current = { ...dismissed.current, [org]: needs }
  }, [])
}
