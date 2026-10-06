// Scheduled ticks of an org that did not start a run (#342), from monomind's
// schedule-audit.jsonl via `org schedule-audit`: a refused start (host
// preflight, eval gate, daemon lock), a tick that yielded to a live run, and
// ticks held for one catch-up run. Renders nothing when there are none. An
// event this build does not know is shown under its own name, never dropped.
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, onOrgEvent } from '../../services/api.js'
import { Chip, mutedText, sectionLabel } from '../orgs/ui.jsx'

const KINDS = {
  refused: { label: 'refused', color: 'var(--red, #ef4444)', hint: 'The tick could not start the run: the reason is the start refusal.' },
  skipped: { label: 'skipped', color: '#eab308', hint: 'A run was already live, so this tick yielded to it.' },
  coalesced: { label: 'coalesced', color: 'var(--text-muted)', hint: 'The tick landed mid-run and was held for one catch-up run.' },
}

export function kindMeta(kind, event) {
  return KINDS[kind] || { label: event || 'other', color: 'var(--text-muted)', hint: 'An event this version does not know.' }
}

export function normalizeAudit(res) {
  if (!res || res.error || !Array.isArray(res.entries)) return null
  return res.entries.filter(e => e && typeof e === 'object')
}

export default function ScheduleAuditPanel({ orgName, live = false }) {
  const [entries, setEntries] = useState([])
  const request = useRef(0)
  const load = useCallback(async () => {
    if (!orgName) return
    const mine = ++request.current
    const list = normalizeAudit(await api.getOrgScheduleAudit(orgName))
    if (mine === request.current && list) setEntries(list)
  }, [orgName])

  useEffect(() => {
    setEntries([])
    load()
    // A refused start writes the audit file without creating a run bus.
    // Keep the visible org audit current even when no run is selected.
    const timer = setInterval(load, 5000)
    return () => { clearInterval(timer); request.current++ }
  }, [load])
  useEffect(() => {
    if (!live || !orgName) return undefined
    return onOrgEvent((p) => {
      if (p?.orgName === orgName && p.event?.type === 'audit' && String(p.event.reason || '').startsWith('scheduled-')) load()
    })
  }, [live, orgName, load])

  if (entries.length === 0) return null
  return (
    <section aria-label="Scheduled ticks" style={{ display: 'flex', flexDirection: 'column', gap: 4, marginBottom: 8 }}>
      <span style={sectionLabel}>Scheduled ticks that did not start a run</span>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
        {entries.map((e, i) => {
          const m = kindMeta(e.kind, e.event)
          return (
            <li key={`${e.ts}-${i}`} style={{ ...mutedText, display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'baseline', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '4px 8px' }}>
              <Chip color={m.color} title={m.hint}>{m.label}</Chip>
              {e.at && <span>{new Date(e.at).toLocaleString()}</span>}
              <span style={{ color: 'var(--text-secondary)', wordBreak: 'break-word' }}>{e.msg}</span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
