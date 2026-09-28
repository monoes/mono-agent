import { useState } from 'react'
import { FullAccessBadge, accessState, grantFullAccess, revokeFullAccess } from './fullAccess.jsx'

// The role editor's "Full access" control (#205): grant (after the risk
// confirmation), grant again for a suspended role, and revoke. The grant
// state is `org status`'s roles_access entry for this role; declared says
// the saved policy asks for full access even if no state came back.

const mono = 'var(--font-mono)'
const hint = { fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--text-muted)', lineHeight: 1.5 }

const STATE_HINT = {
  active: 'This role runs with full access.',
  suspended: 'Its config changed since the grant, so it runs scoped until you grant it again.',
  'unattended-blocked': "Scheduled and unattended runs don't use full access: the org doesn't allow unattended full access.",
}

export default function RoleFullAccessSection({ orgName, roleID, entry, declared, onChanged }) {
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [note, setNote] = useState('')
  const isFull = !!entry || declared
  const state = entry?.access_state

  const run = async (what, fn) => {
    setBusy(what); setErr(''); setNote('')
    try {
      const res = await fn()
      if (res) { setNote(res.message || (what === 'revoke' ? 'Full access revoked.' : 'Full access granted.')); onChanged?.() }
    } catch (e) {
      setErr(String(e?.message || e))
    } finally {
      setBusy('')
    }
  }
  const grant = (again) => run('grant', () => grantFullAccess(orgName, roleID, { again }))
  const revoke = () => run('revoke', () => revokeFullAccess(orgName, roleID))

  return (
    <section data-testid="role-full-access">
      <div className="form-label" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        Full access {isFull && <FullAccessBadge entry={entry} />}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {isFull ? (
          <>
            <div style={hint}>{STATE_HINT[state] || 'This role declares full access.'}</div>
            {entry?.reason && state !== 'active' && (
              <div data-testid="full-access-reason" style={{ fontFamily: mono, fontSize: 10, color: accessState(entry).color, lineHeight: 1.5, wordBreak: 'break-word' }}>
                {entry.reason}
              </div>
            )}
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
              {state === 'suspended' && (
                <button type="button" className="btn btn-primary btn-sm" disabled={!!busy} onClick={() => grant(true)}>
                  {busy === 'grant' ? 'Granting…' : 'Grant again…'}
                </button>
              )}
              <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy} onClick={revoke}>
                {busy === 'revoke' ? 'Revoking…' : 'Revoke'}
              </button>
            </div>
          </>
        ) : (
          <>
            <div style={hint}>
              Lets this role run any command and change any file with no approval prompts, like a coder chat.
              Only a person can grant it.
            </div>
            <div>
              <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy || !orgName} onClick={() => grant(false)}>
                {busy === 'grant' ? 'Granting…' : 'Grant full access…'}
              </button>
            </div>
          </>
        )}
        {note && <div style={{ fontFamily: mono, fontSize: 10, color: 'var(--green-neon)', wordBreak: 'break-word' }}>{note}</div>}
        {err && <div role="alert" style={{ fontFamily: mono, fontSize: 10, color: 'var(--red)', lineHeight: 1.5, wordBreak: 'break-word' }}>{err}</div>}
      </div>
    </section>
  )
}
