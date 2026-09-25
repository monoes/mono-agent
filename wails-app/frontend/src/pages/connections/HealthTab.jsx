// Health tab: `automation doctor <id>` — selector health (last success and
// failure per selector key, decaying/broken status) and package issues.
import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '../../services/api.js'
import { Chip, ErrorBox, Busy, body, label, mono, muted, panel, fmtShort } from './ui.jsx'
import { IssueList } from './OverviewTab.jsx'

const STATUS_COLORS = { ok: 'var(--green-neon)', decaying: 'var(--yellow)', broken: 'var(--red)' }

export default function HealthTab({ automationId }) {
  const [res, setRes] = useState(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try { setRes(await api.doctorAutomations(automationId)) } finally { setLoading(false) }
  }, [automationId])

  useEffect(() => { load() }, [load])

  const entry = (res?.automations || []).find(a => a.id === automationId) || (res?.automations || [])[0]
  const selectors = entry?.selectors || []
  const bad = selectors.filter(s => s.status !== 'ok').length

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ ...body, flex: 1 }}>
          {entry ? (selectors.length ? `${selectors.length} selectors tracked, ${bad} need attention.` : 'No selector runs recorded yet — health fills in as actions run.') : ''}
        </span>
        <button className="btn btn-ghost btn-sm" onClick={load} disabled={loading} style={{ gap: 5 }}><RefreshCw size={11} /> Re-check</button>
      </div>
      {loading && <Busy text="Checking…" />}
      {res?.error && <ErrorBox>{res.error}</ErrorBox>}
      {entry && <IssueList issues={entry.issues} />}
      {selectors.length > 0 && (
        <div style={{ ...panel, padding: 0, overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                {['Selector', 'Status', 'OK', 'Fail', 'Healed', 'Last OK', 'Last fail'].map(h => (
                  <th key={h} scope="col" style={{ ...label, textAlign: 'left', padding: '8px 10px', borderBottom: '1px solid var(--border)' }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.key} style={{ borderTop: '1px solid var(--border-dim)' }}>
                  <td style={{ ...mono, fontSize: 10.5, color: 'var(--text)', padding: '6px 10px' }}>{s.key}</td>
                  <td style={{ padding: '6px 10px' }}><Chip color={STATUS_COLORS[s.status] || 'var(--text-muted)'}>{s.status}</Chip></td>
                  <td style={{ ...muted, padding: '6px 10px' }}>{s.ok ?? 0}</td>
                  <td style={{ ...muted, padding: '6px 10px', color: s.fail ? 'var(--red)' : 'var(--text-muted)' }}>{s.fail ?? 0}</td>
                  <td style={{ ...muted, padding: '6px 10px' }}>{s.healed ?? 0}</td>
                  <td style={{ ...muted, padding: '6px 10px', whiteSpace: 'nowrap' }}>{fmtShort(s.lastOk)}</td>
                  <td style={{ ...muted, padding: '6px 10px', whiteSpace: 'nowrap' }}>{fmtShort(s.lastFail)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {bad > 0 && (
        <div style={{ ...body, fontSize: 11 }}>
          Suggested fix for a broken selector: re-record that step from the extension side panel — you click the element once and only that selector is replaced.
        </div>
      )}
    </>
  )
}
