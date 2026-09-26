// Health tab: `automation doctor <id>` — selector health (last success and
// failure per selector key, decaying/broken status) and package issues —
// plus "Re-record" for one selector (`automation rerecord`, contracts §9).
import { useCallback, useEffect, useState } from 'react'
import { RefreshCw, Crosshair, MoreHorizontal } from 'lucide-react'
import { api } from '../../services/api.js'
import { Chip, ErrorBox, OkBox, Busy, body, label, mono, muted, panel, fmtShort } from './ui.jsx'
import { IssueList } from './OverviewTab.jsx'

const STATUS_COLORS = { ok: 'var(--green-neon)', decaying: 'var(--yellow)', broken: 'var(--red)', stale: 'var(--text-dim)' }

// isStale: health history for a key the installed package no longer has.
const isStale = (s) => s.stale === true || s.status === 'stale'
const WHERE_TEXT = { package: 'in the package', overlay: 'in your local overlay (the package itself is unchanged)' }
const cell = { ...muted, padding: '6px 8px', verticalAlign: 'top' }

// RowAction: decaying and broken selectors show "Re-record" directly; the
// others keep it behind a small menu so a healthy row stays quiet.
function RowAction({ s, busy, onRerecord }) {
  const [open, setOpen] = useState(false)
  if (isStale(s)) return <span style={{ ...muted, fontSize: 10 }}>no longer in this package</span>
  if (s.status !== 'ok') {
    return (
      <button className="btn btn-secondary btn-sm" disabled={busy} onClick={() => onRerecord(s.key)} style={{ gap: 4, padding: '2px 8px' }}>
        <Crosshair size={10} /> Re-record
      </button>
    )
  }
  return (
    <span style={{ position: 'relative' }}>
      <button className="btn btn-ghost btn-icon" aria-label={`More for ${s.key}`} aria-expanded={open} disabled={busy} onClick={() => setOpen(o => !o)}>
        <MoreHorizontal size={12} />
      </button>
      {open && (
        <div role="menu" style={{ position: 'absolute', right: 0, top: '100%', zIndex: 5, background: 'var(--elevated)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', padding: 4 }}>
          <button role="menuitem" className="btn btn-ghost btn-sm" onClick={() => { setOpen(false); onRerecord(s.key) }} style={{ gap: 4, whiteSpace: 'nowrap' }}>
            <Crosshair size={10} /> Re-record
          </button>
        </div>
      )}
    </span>
  )
}

export default function HealthTab({ automationId }) {
  const [res, setRes] = useState(null)
  const [loading, setLoading] = useState(false)
  const [picking, setPicking] = useState('') // selector key being re-recorded
  const [pickResult, setPickResult] = useState(null) // {ok, key, text}

  const load = useCallback(async () => {
    setLoading(true)
    try { setRes(await api.doctorAutomations(automationId)) } finally { setLoading(false) }
  }, [automationId])

  useEffect(() => { load() }, [load])

  const rerecord = async (key) => {
    setPicking(key); setPickResult(null)
    try {
      const out = await api.rerecordSelector(automationId, key)
      if (!out || out.error) {
        setPickResult({ ok: false, key, text: out?.error || 'Re-record failed.' })
        return
      }
      const n = (out.candidates || []).length
      setPickResult({ ok: true, key, text: `Saved ${n} selector candidate${n === 1 ? '' : 's'} for ${out.key || key} ${WHERE_TEXT[out.where] || (out.where ? `(${out.where})` : '')}.` })
      await load()
    } finally { setPicking('') }
  }

  const entry = (res?.automations || []).find(a => a.id === automationId) || (res?.automations || [])[0]
  const selectors = entry?.selectors || []
  const bad = selectors.filter(s => s.status !== 'ok' && !isStale(s)).length

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
      {picking && (
        <div role="status" style={{ ...panel, borderColor: 'var(--cyan)', ...body, fontSize: 11.5 }}>
          <Busy text={`Switch to your browser and click the element for ${picking}… (Esc in the page cancels)`} />
        </div>
      )}
      {pickResult && (pickResult.ok ? <OkBox>{pickResult.text}</OkBox> : <ErrorBox>Re-record {pickResult.key}: {pickResult.text}</ErrorBox>)}
      {entry && <IssueList issues={entry.issues} />}
      {selectors.length > 0 && (
        // Five columns and a fixed layout so the table fits the 620px drawer
        // without horizontal scroll: runs (ok / fail, healed below) and the
        // last-seen dates are stacked, and long keys and notes wrap.
        <div style={{ ...panel, padding: 0 }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', tableLayout: 'fixed' }}>
            <colgroup><col /><col style={{ width: 84 }} /><col style={{ width: 72 }} /><col style={{ width: 104 }} /><col style={{ width: 104 }} /></colgroup>
            <thead>
              <tr>
                {['Selector', 'Status', 'OK / fail', 'Last', ''].map(h => (
                  <th key={h || 'action'} scope="col" style={{ ...label, textAlign: 'left', padding: '8px 8px', borderBottom: '1px solid var(--border)' }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.key} style={{ borderTop: '1px solid var(--border-dim)', opacity: isStale(s) ? 0.5 : 1 }}>
                  <td style={{ ...mono, fontSize: 10.5, color: 'var(--text)', padding: '6px 8px', wordBreak: 'break-all', verticalAlign: 'top' }}>{s.key}</td>
                  <td style={{ padding: '6px 8px', verticalAlign: 'top' }}><Chip color={STATUS_COLORS[isStale(s) ? 'stale' : s.status] || 'var(--text-muted)'}>{isStale(s) ? 'stale' : s.status}</Chip></td>
                  <td style={cell}>
                    {s.ok ?? 0} / <span style={{ color: s.fail ? 'var(--red)' : 'var(--text-muted)' }}>{s.fail ?? 0}</span>
                    {s.healed ? <div style={{ fontSize: 9.5 }}>{s.healed} healed</div> : null}
                  </td>
                  <td style={{ ...cell, fontSize: 10 }}>
                    <div>ok {fmtShort(s.lastOk)}</div>
                    <div>fail {fmtShort(s.lastFail)}</div>
                  </td>
                  <td style={{ padding: '4px 8px', textAlign: 'right', verticalAlign: 'top' }}><RowAction s={s} busy={!!picking} onRerecord={rerecord} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {bad > 0 && !picking && (
        <div style={{ ...body, fontSize: 11 }}>
          Re-record a broken selector: the site opens in your browser and you click the element once — only that selector is replaced.
        </div>
      )}
    </>
  )
}
