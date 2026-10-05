// Per-section budget report for the canvas and the status panel. The data is
// `monoagentcli org budget` (Go does the derivation); this only fetches it
// and refetches (debounced) when a usage or section-budget event arrives for
// the org, so a live canvas follows spend and the soft-closure moment.
import { useEffect, useState } from 'react'
import { api, onOrgEvent } from '../../services/api.js'

const REFRESH_MS = 800

export default function useOrgBudget({ orgName, enabled, run = '', live = false }) {
  const [report, setReport] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!enabled || !orgName) { setReport(null); setError(''); return undefined }
    let cancelled = false
    let timer = null
    const load = () => api.getOrgBudget(orgName, run).then(res => {
      if (cancelled) return
      if (!res || res.error) { setError(res?.error || ''); setReport(null) } else { setError(''); setReport(res) }
    })
    load()
    const off = live
      ? onOrgEvent((payload) => {
        const ev = payload?.event
        if (cancelled || payload?.orgName !== orgName || !ev) return
        const relevant = ev.type === 'usage' || (ev.type === 'audit' && String(ev.reason || '').startsWith('section-budget'))
        if (!relevant) return
        clearTimeout(timer)
        timer = setTimeout(load, REFRESH_MS)
      })
      : () => {}
    return () => { cancelled = true; clearTimeout(timer); off() }
  }, [orgName, enabled, run, live])

  return { report, error }
}
