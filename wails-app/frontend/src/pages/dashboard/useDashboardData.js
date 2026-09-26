// Everything the dashboard shows, from the CLI via bindings. Polls only while
// the window is visible AND the dashboard is the page being shown (App.jsx
// keeps pages mounted behind display:none); coming back refreshes at once.
// Events trigger an immediate refresh.
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, subscribeEvent } from '../../services/api.js'
import { usePageVisibleRef, useVisibleCatchUp } from '../../lib/usePageVisible.js'
import { LIVE_STATUSES } from '../../lib/execStatus.js'

export const SUMMARY_POLL_MS = 15000
export const ORG_FAST_POLL_MS = 15000
export const ORG_FULL_POLL_MS = 60000
export const FAST_POLL_MS = 2000 // while a run is live (`workflow executions --all` ≈ 20 ms)
export const BASE_POLL_MS = 5000 // catches runs started outside this window (CLI, daemon cron)

const REFRESH_EVENTS = ['workflow:complete', 'workflow:exec-started', 'documents:changed', 'images:changed', 'org:runStatus']

// A fast org summary has needs_you null; keep the counts the last full one found.
export function mergeFast(prev, fast) {
  const byName = Object.fromEntries((prev?.orgs || []).map(o => [o.name, o]))
  const orgs = (fast.orgs || []).map(o => ({
    ...o,
    needs_you: byName[o.name]?.needs_you ?? null,
    needs_you_error: byName[o.name]?.needs_you_error,
  }))
  const needsYou = orgs.reduce((n, o) => n + (o.needs_you || 0), 0)
  return { ...fast, orgs, totals: { ...fast.totals, needs_you: needsYou } }
}

function useInterval(fn, ms) {
  const ref = useRef(fn)
  ref.current = fn
  useEffect(() => {
    const id = setInterval(() => ref.current(), ms)
    return () => clearInterval(id)
  }, [ms])
}

export function useDashboardData({ active = true } = {}) {
  const [summary, setSummary] = useState(null)
  const [orgs, setOrgs] = useState(null)
  const [workflows, setWorkflows] = useState([])
  const [executions, setExecutions] = useState([])
  const [loading, setLoading] = useState(true)
  const windowVisible = usePageVisibleRef()
  const activeRef = useRef(active)
  activeRef.current = active
  const visible = { get current() { return windowVisible.current && activeRef.current } }
  const haveFull = useRef(false)

  const loadSummary = useCallback(async () => {
    const s = await api.getSummary()
    if (s) setSummary(s)
  }, [])
  const loadOrgs = useCallback(async (fast = true) => {
    const o = await api.getOrgSummary(fast)
    if (!o) return
    if (!fast) haveFull.current = true
    setOrgs(prev => (fast && haveFull.current ? mergeFast(prev, o) : o))
  }, [])
  const loadLists = useCallback(async () => {
    const [w, e] = await Promise.all([api.listWorkflows(), api.getRecentExecutions(30)])
    setWorkflows(w || [])
    setExecutions(e || [])
  }, [])
  const refresh = useCallback(async () => {
    await Promise.all([loadSummary(), loadLists(), loadOrgs(true)])
    setLoading(false)
  }, [loadSummary, loadLists, loadOrgs])

  useEffect(() => { refresh(); loadOrgs(false) }, [refresh, loadOrgs])
  useVisibleCatchUp(() => { if (activeRef.current) refresh() })
  // Returning to the dashboard page: catch up instead of showing stale data.
  const wasActive = useRef(active)
  useEffect(() => {
    if (active && !wasActive.current) refresh()
    wasActive.current = active
  }, [active, refresh])

  useEffect(() => {
    const offs = REFRESH_EVENTS.map(n => subscribeEvent(n, () => { if (activeRef.current) { loadSummary(); loadLists() } }))
    return () => offs.forEach(off => off && off())
  }, [loadSummary, loadLists])

  useInterval(() => { if (visible.current) loadSummary() }, SUMMARY_POLL_MS)
  useInterval(() => { if (visible.current) loadOrgs(true) }, ORG_FAST_POLL_MS)
  useInterval(() => { if (visible.current) loadOrgs(false) }, ORG_FULL_POLL_MS)
  const live = executions.some(e => LIVE_STATUSES.has((e.status || '').toUpperCase()))
  useInterval(() => { if (visible.current) loadLists() }, live ? FAST_POLL_MS : BASE_POLL_MS)

  return { summary, orgs, workflows, executions, loading, refresh, setExecutions, reloadLists: loadLists }
}
