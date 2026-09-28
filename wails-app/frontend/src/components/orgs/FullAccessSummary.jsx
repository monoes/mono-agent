import { useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { FullAccessBadge, accessState, grantFullAccess } from '../orgdesigner/fullAccess.jsx'

// The org overview's full-access roles (#205), from `org status`'s
// roles_access: how many roles run with full access, each one's state, the
// reason when it isn't active, and "Grant again" for a suspended one.
// Granting and revoking otherwise live in the role editor (Design tab).

const mono = 'var(--font-mono)'

export default function FullAccessSummary({ orgName, status, onChanged }) {
  const roles = Array.isArray(status?.roles_access) ? status.roles_access : []
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  if (roles.length === 0) return null

  const grantAgain = async (role) => {
    setBusy(role); setErr('')
    try {
      if (await grantFullAccess(orgName, role, { again: true })) onChanged?.()
    } catch (e) {
      setErr(String(e?.message || e))
    } finally {
      setBusy('')
    }
  }

  return (
    <div data-testid="full-access-summary" style={{
      background: 'rgba(245,158,11,0.04)', border: '1px solid rgba(245,158,11,0.25)', borderRadius: 'var(--radius)',
      padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 6,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontFamily: mono, fontSize: 10, color: '#f59e0b', textTransform: 'uppercase', letterSpacing: 1 }}>
        <ShieldAlert size={11} /> {roles.length} full-access role{roles.length === 1 ? '' : 's'}
      </div>
      {roles.map(r => (
        <div key={r.role} data-testid="full-access-row" style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={{ fontFamily: mono, fontSize: 11, color: 'var(--text)' }}>{r.role}</span>
            <FullAccessBadge entry={r} />
            {r.access_state === 'suspended' && (
              <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy} onClick={() => grantAgain(r.role)}>
                {busy === r.role ? 'Granting…' : 'Grant again…'}
              </button>
            )}
          </div>
          {r.reason && r.access_state !== 'active' && (
            <div style={{ fontFamily: mono, fontSize: 10, color: accessState(r).color, lineHeight: 1.5, wordBreak: 'break-word' }}>{r.reason}</div>
          )}
        </div>
      ))}
      {err && <div role="alert" style={{ fontFamily: mono, fontSize: 10, color: 'var(--red)', lineHeight: 1.5, wordBreak: 'break-word' }}>{err}</div>}
    </div>
  )
}
