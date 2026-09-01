import { useEffect, useRef, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { api, onMonomindInitEvent, notify } from '../services/api.js'

const MAX_LOG_LINES = 10

/**
 * MonomindInitPrompt — shown in place of the normal empty state on the Orgs
 * and Agents tabs when the active profile's folder has never been set up
 * with `monomind init` (see api.isMonomindInitialized / App.IsMonomindInitialized
 * in wails-app/app_monomind_init.go). One button, streamed progress, calls
 * onInitialized() when done so the parent can re-fetch its data.
 *
 * @param {() => void} onInitialized
 */
export default function MonomindInitPrompt({ onInitialized }) {
  const [status, setStatus] = useState('idle') // idle | running | error
  const [lines, setLines] = useState([])
  const runningRef = useRef(false)

  useEffect(() => onMonomindInitEvent((payload) => {
    if (!runningRef.current) return
    if (payload.kind === 'line') {
      setLines(prev => [...prev, payload.message].slice(-MAX_LOG_LINES))
    } else if (payload.kind === 'error') {
      runningRef.current = false
      setStatus('error')
      notify('initialize monomind', payload.message || 'failed')
    } else if (payload.kind === 'done') {
      runningRef.current = false
      setStatus('idle')
      onInitialized?.()
    }
  }), [onInitialized])

  const start = async () => {
    runningRef.current = true
    setStatus('running')
    setLines([])
    await api.initializeMonomindProfile()
  }

  return (
    <div className="empty-state" style={{ flex: 1 }}>
      <div className="empty-state-icon"><Sparkles size={30} /></div>
      <div className="empty-state-title">
        {status === 'running' ? 'Setting up monomind…' : 'This profile isn’t set up with monomind yet'}
      </div>
      <div className="empty-state-desc">
        {status === 'running'
          ? 'This can take a minute or two.'
          : 'Orgs and agents need monomind initialized in this profile’s folder first.'}
      </div>
      {status === 'running' ? (
        <>
          <div style={{ display: 'flex', justifyContent: 'center', padding: 8 }}><div className="spinner" /></div>
          {lines.length > 0 && (
            <div style={{
              fontFamily: 'var(--font-mono)', fontSize: 10.5, color: 'var(--text-muted)',
              textAlign: 'left', maxWidth: 440, margin: '8px auto 0', maxHeight: 140, overflowY: 'auto',
            }}>
              {lines.map((l, i) => <div key={i} style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{l}</div>)}
            </div>
          )}
        </>
      ) : (
        <button className="btn btn-primary btn-sm" onClick={start} style={{ marginTop: 8 }}>
          Initiate monomind on this profile
        </button>
      )}
    </div>
  )
}
