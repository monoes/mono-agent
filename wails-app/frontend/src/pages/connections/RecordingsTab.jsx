// Recordings tab: recordings linked to this automation (provenance) and new
// recordings not saved anywhere yet. Analyze opens the review flow.
import { useCallback, useEffect, useState } from 'react'
import { RefreshCw, Sparkles, Trash2 } from 'lucide-react'
import { api, notify } from '../../services/api.js'
import { confirm } from '../../components/ConfirmDialog.jsx'
import { Chip, ErrorBox, Busy, body, label, mono, muted, panel, fmtDate } from './ui.jsx'
import RecordingReview from './RecordingReview.jsx'

function Row({ r, onAnalyze, onDelete }) {
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
        <span style={{ ...mono, fontSize: 11.5, fontWeight: 700, color: 'var(--text)', flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.title || r.url || r.id}</span>
        {!r.complete && <Chip color="var(--yellow)" title={r.stopReason || ''}>incomplete</Chip>}
        {r.automation && <Chip color="var(--teal)">saved</Chip>}
      </div>
      {r.goal && <div style={{ ...body, fontSize: 11 }}>“{r.goal}”</div>}
      <div style={{ ...muted, fontSize: 10, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {fmtDate(r.startedAt)} · {r.events} events · {r.url}
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        <button className="btn btn-primary btn-sm" onClick={onAnalyze} style={{ gap: 5 }}><Sparkles size={11} /> {r.automation ? 'Analyze again' : 'Analyze'}</button>
        <button className="btn btn-ghost btn-sm" onClick={onDelete} style={{ gap: 5 }}><Trash2 size={11} /> Delete</button>
      </div>
    </div>
  )
}

export default function RecordingsTab({ automationId, onSaved }) {
  const [res, setRes] = useState(null)
  const [loading, setLoading] = useState(false)
  const [reviewing, setReviewing] = useState(null)

  const load = useCallback(async () => {
    setLoading(true)
    try { setRes(await api.listRecordings()) } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  const del = async (r) => {
    if (!(await confirm(`Delete the recording "${r.title || r.id}"? Actions already saved from it keep working.`))) return
    const out = await api.deleteRecording(r.id)
    if (out?.error) notify('delete recording', out.error)
    await load()
  }

  if (reviewing) {
    return (
      <RecordingReview
        recording={reviewing}
        automationId={automationId}
        onBack={() => { setReviewing(null); load() }}
        onSaved={onSaved}
      />
    )
  }

  const all = res?.recordings || []
  const linked = all.filter(r => r.automation === automationId)
  const fresh = all.filter(r => !r.automation)
  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ ...body, flex: 1, fontSize: 11 }}>Record in the extension side panel; recordings show up here to turn into actions.</span>
        <button className="btn btn-ghost btn-sm" onClick={load} disabled={loading} style={{ gap: 5 }}><RefreshCw size={11} /> Refresh</button>
      </div>
      {loading && !res && <Busy text="Loading recordings…" />}
      {res?.error && <ErrorBox>{res.error}</ErrorBox>}
      <span style={label}>Saved into this automation ({linked.length})</span>
      {linked.length ? linked.map(r => <Row key={r.id} r={r} onAnalyze={() => setReviewing(r)} onDelete={() => del(r)} />) : <span style={muted}>none yet</span>}
      <span style={label}>New recordings ({fresh.length})</span>
      {fresh.length ? fresh.map(r => <Row key={r.id} r={r} onAnalyze={() => setReviewing(r)} onDelete={() => del(r)} />) : <span style={muted}>none</span>}
    </>
  )
}
