import { useState } from 'react'
import { AlertTriangle, Square } from 'lucide-react'
import { api } from '../../services/api.js'

// The warning a coder turn leaves when it ends with background processes
// still running (notice coder.background, #203), with "Stop all": `coder
// stop-background` stops only the pids that turn reported, and only while
// they still belong to it, then says what happened to each.

const mono = 'var(--font-mono)'
const AMBER = '#fbbf24'

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 'es'}`

// stopSummary turns {stopped, gone, refused} into one line.
export function stopSummary(res) {
  const stopped = res?.stopped?.length || 0
  const gone = res?.gone?.length || 0
  const refused = res?.refused?.length || 0
  const parts = []
  if (stopped) parts.push(`Stopped ${plural(stopped, 'process')}`)
  if (gone) parts.push(`${gone} had already exited`)
  if (refused) parts.push(`${refused} not stopped (no longer this turn's: ${res.refused.join(', ')})`)
  return parts.length ? parts.join(' · ') : 'Nothing left to stop'
}

export function CoderBackgroundBanner({ notice }) {
  const [state, setState] = useState(null) // { busy } | { result } | { error }
  const canStop = !!notice.conversationId && !!notice.turnId
  const stopAll = async () => {
    setState({ busy: true })
    try {
      setState({ result: await api.coderStopBackground(notice.conversationId, notice.turnId) })
    } catch (e) {
      setState({ error: String(e?.message || e) })
    }
  }
  const done = !!state?.result
  return (
    <div data-testid="coder-background-banner" style={{
      display: 'flex', flexDirection: 'column', gap: 5, padding: '6px 8px', marginTop: 6, borderRadius: 6,
      background: done ? 'rgba(148,163,184,0.06)' : `${AMBER}14`, border: `1px solid ${done ? 'rgba(148,163,184,0.25)' : `${AMBER}40`}`,
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}>
        <AlertTriangle size={11} color={done ? '#94a3b8' : AMBER} style={{ marginTop: 1, flexShrink: 0 }} />
        <span style={{ flex: 1, fontFamily: mono, fontSize: 10, color: done ? '#94a3b8' : AMBER }}>{notice.message}</span>
        {canStop && !done && (
          <button type="button" onClick={stopAll} disabled={!!state?.busy} style={{
            display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0, cursor: state?.busy ? 'default' : 'pointer',
            background: 'rgba(239,68,68,0.12)', border: '1px solid rgba(239,68,68,0.35)', borderRadius: 5,
            padding: '2px 7px', color: '#ef4444', fontFamily: mono, fontSize: 9.5,
          }}>
            <Square size={8} fill="#ef4444" /> {state?.busy ? 'Stopping…' : 'Stop all'}
          </button>
        )}
      </div>
      {done && <div data-testid="coder-background-result" role="status" style={{ fontFamily: mono, fontSize: 9.5, color: '#e2e8f0', paddingLeft: 17 }}>{stopSummary(state.result)}</div>}
      {state?.error && <div role="alert" style={{ fontFamily: mono, fontSize: 9.5, color: '#fca5a5', paddingLeft: 17 }}>Could not stop them: {state.error}</div>}
    </div>
  )
}
