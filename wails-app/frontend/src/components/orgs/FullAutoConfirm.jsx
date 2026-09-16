// Confirmation shown before an org goes to Full auto (U16): lists what the
// decider will resolve with no human — gates, and grants whose row says
// tier "irreversible" — so the operator sees the blast radius first.
import { useEffect, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { api } from '../../services/api.js'
import { fullAutoImpact } from './autonomyModel.js'
import { mono, mutedText } from './ui.jsx'

function itemsOf(payload, key) {
  if (!payload || payload.error) return []
  if (Array.isArray(payload[key])) return payload[key]
  if (Array.isArray(payload.items)) return payload.items
  return []
}

export default function FullAutoConfirm({ open, orgName, autonomy, deciderKind, onConfirm, onCancel }) {
  const [loading, setLoading] = useState(false)
  const [impact, setImpact] = useState(null)
  const [loadError, setLoadError] = useState('')

  useEffect(() => {
    if (!open || !orgName) return
    let cancelled = false
    setLoading(true)
    setLoadError('')
    Promise.all([api.listOrgGrants(orgName), api.getOrgGates(orgName)]).then(([grants, gates]) => {
      if (cancelled) return
      if (!grants || grants.error) setLoadError(grants?.error || 'Could not load grants.')
      const pending = itemsOf(gates, 'gates').filter(g => !g.status || g.status === 'pending')
      setImpact(fullAutoImpact(autonomy, itemsOf(grants, 'grants'), pending))
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [open, orgName, autonomy])

  if (!open) return null
  const decider = deciderKind || autonomy?.decider?.kind || 'model'

  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) onCancel() }}>
      <div role="dialog" aria-modal="true" aria-label="Turn on full auto" className="modal" style={{ width: 480 }}>
        <div className="modal-title" style={{ justifyContent: 'flex-start', gap: 8 }}>
          <AlertTriangle size={16} color="var(--red, #ef4444)" /> Full auto for {orgName}?
        </div>
        <div style={{ ...mono, fontSize: 11.5, color: 'var(--text-secondary)', marginBottom: 12 }}>
          The {decider} decider will resolve these with no human:
        </div>
        {loading ? (
          <div style={{ display: 'flex', justifyContent: 'center', padding: 12 }}><div className="spinner" /></div>
        ) : (
          <ul style={{ ...mono, fontSize: 11.5, color: 'var(--text)', margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 6 }}>
            {impact?.gatesIncluded && (
              <li>
                Every gate a role raises
                {impact.pendingGates.length > 0 && (
                  <span style={mutedText}> — pending now: {impact.pendingGates.map(g => `${g.name}${g.role ? ` (${g.role})` : ''}`).join(', ')}</span>
                )}
              </li>
            )}
            {impact?.grants.map(g => (
              <li key={`${g.role}:${g.alias}`}>
                <b>{g.role}</b> running <b>{g.workflowName}</b> <span style={mutedText}>(outbound, irreversible)</span>
              </li>
            ))}
            {impact?.classes.map(c => <li key={c}>{c} <span style={mutedText}>(set to irreversible)</span></li>)}
            {impact && !impact.gatesIncluded && impact.grants.length === 0 && impact.classes.length === 0 && (
              <li style={mutedText}>No irreversible decisions are configured.</li>
            )}
          </ul>
        )}
        {loadError && <div style={{ ...mono, fontSize: 10.5, color: '#f87171', marginTop: 10 }}>{loadError} The list may be incomplete.</div>}
        <div className="modal-actions">
          <button className="btn btn-ghost" onClick={onCancel}>Cancel</button>
          <button className="btn btn-danger" onClick={onConfirm} disabled={loading}>Turn on full auto</button>
        </div>
      </div>
    </div>
  )
}
