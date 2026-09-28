import { useCallback, useEffect, useRef, useState } from 'react'
import { ShieldAlert, ShieldOff, Clock } from 'lucide-react'
import { api } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'

// Full-access org roles (#205). A role with policy.access "full" runs like a
// coder chat: any command, any file, no approval gate. Its state comes from
// `org status`'s roles_access ([{role, access, access_state, reason?}],
// absent when no role declares full access):
//   active             — the grant holds
//   suspended          — the role's config changed since the grant; it runs
//                        scoped until a human grants it again
//   unattended-blocked — scheduled/unattended runs don't use it, because
//                        the org doesn't allow unattended full access
// Granting is human-only: only after the confirm dialog below, and the CLI
// refuses a grant from an agent context.

// The canvas card shows only "FULL": its icon, color and tooltip carry the
// state.
export const ACCESS_STATES = {
  active: { label: 'Full access', color: '#f59e0b', icon: ShieldAlert },
  suspended: { label: 'Full access · suspended', color: '#ef4444', icon: ShieldOff },
  'unattended-blocked': { label: 'Full access · unattended blocked', color: '#fb923c', icon: Clock },
}
const UNKNOWN_STATE = { label: 'Full access', color: '#f59e0b', icon: ShieldAlert }

export function accessState(entry) {
  return ACCESS_STATES[entry?.access_state] || UNKNOWN_STATE
}

// rolesAccessByRole maps role id → roles_access entry.
export function rolesAccessByRole(status) {
  const out = {}
  for (const e of Array.isArray(status?.roles_access) ? status.roles_access : []) {
    if (e?.role) out[e.role] = e
  }
  return out
}

// declaresFull reports whether a role's saved policy asks for full access.
export function declaresFull(node) {
  return node?.rest?.policy?.access === 'full'
}

// useRolesAccess loads the org's roles_access and reloads it whenever
// `stamp` changes (the designer passes its role config, so an edit that
// suspends a grant shows up), debounced so a burst of edits costs one call.
export function useRolesAccess(orgName, stamp = '') {
  const [byRole, setByRole] = useState({})
  const current = useRef(orgName)
  current.current = orgName
  const refresh = useCallback(async () => {
    const name = orgName
    if (!name) { setByRole({}); return }
    const status = await Promise.resolve().then(() => api.getOrgStatus(name)).catch(() => null)
    if (current.current === name) setByRole(rolesAccessByRole(status))
  }, [orgName])
  useEffect(() => {
    const id = setTimeout(refresh, stamp ? 600 : 0)
    return () => clearTimeout(id)
  }, [refresh, stamp])
  return { byRole, refresh }
}

export function FullAccessBadge({ entry, compact = false }) {
  const st = accessState(entry)
  const Icon = st.icon
  const title = entry?.reason ? `${st.label}: ${entry.reason}` : st.label
  return (
    <span data-testid="full-access-badge" data-state={entry?.access_state || 'unknown'} title={title} style={{
      display: 'inline-flex', alignItems: 'center', gap: 3, flexShrink: 0,
      fontFamily: 'var(--font-mono)', fontSize: compact ? 7.5 : 8.5, fontWeight: 700, letterSpacing: 0.8,
      color: st.color, background: `${st.color}14`, border: `1px solid ${st.color}66`,
      borderRadius: 4, padding: compact ? '0 4px' : '1px 5px', whiteSpace: 'nowrap',
    }}>
      <Icon size={compact ? 8 : 9} /> {compact ? 'FULL' : st.label.toUpperCase()}
    </span>
  )
}

export const FULL_ACCESS_HELP = 'monomind org role set-access --help'

// The grant dialog's body: coder mode's risks plus the org specifics.
export function FullAccessRiskText({ role }) {
  return (
    <div data-testid="full-access-risk-text">
      <p style={{ margin: '0 0 8px' }}>Role <strong>{role}</strong> will work on your computer with no approval prompts (budgets still apply):</p>
      <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 6 }}>
        <li>It can run any command, read and change any file your user account can, and install software.</li>
        <li>Web pages and files it reads, and other roles&apos; messages, reach it and can contain instructions that steer it. Only grant this when everything that reaches it is trusted.</li>
        <li>Scheduled and unattended runs use full access only when the org allows unattended full access.</li>
        <li>It loads your normal Claude Code setup: CLAUDE.md, skills, hooks and MCP servers.</li>
      </ul>
      <p style={{ margin: '8px 0 0', fontSize: 11, color: 'var(--text-muted)' }}>
        Editing this role or the org&apos;s run settings suspends the grant. Unattended runs and accepted taint are set with <code>{FULL_ACCESS_HELP}</code>.
      </p>
    </div>
  )
}

// grantFullAccess asks for the confirmation, then grants. Resolves the CLI's
// result, or null when the user cancelled; rejects with the CLI's refusal.
export async function grantFullAccess(orgName, roleID, { again = false } = {}) {
  const ok = await confirm(<FullAccessRiskText role={roleID} />, {
    title: again ? `Grant ${roleID} full access again?` : `Give ${roleID} full access?`,
    confirmLabel: again ? 'Grant again' : 'Grant full access',
    danger: true,
  })
  if (!ok) return null
  return api.orgRoleSetAccess(orgName, roleID, 'full')
}

export function revokeFullAccess(orgName, roleID) {
  return api.orgRoleSetAccess(orgName, roleID, 'scoped')
}
