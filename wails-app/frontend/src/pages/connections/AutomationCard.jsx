// One browser automation package as a card (spec §7.1), plus the
// "Create automation" card that explains how new packages are made.
import { useState } from 'react'
import { Plus, Circle, FileCode, AlertTriangle, ArrowUpCircle, PenLine, Trash2 } from 'lucide-react'
import { Chip, Dot, SOURCE_LABELS, mono, muted, body } from './ui.jsx'

// dashed: removed built-ins and the Create card. Part of the border
// shorthand — mixing in borderStyle makes React warn on every re-render.
const cardStyle = (active, hov, dashed = false) => ({
  background: active ? 'linear-gradient(145deg,var(--elevated),var(--surface))' : 'var(--surface)',
  border: `1px ${dashed ? 'dashed' : 'solid'} ${active ? 'var(--border-active)' : hov ? 'var(--border-bright)' : 'var(--border)'}`,
  borderRadius: 'var(--radius-lg)',
  padding: '12px 14px',
  cursor: 'pointer',
  display: 'flex', flexDirection: 'column', gap: 7,
  transition: 'all var(--transition)',
  boxShadow: active ? 'var(--shadow-glow-sm)' : 'none',
  minWidth: 0, textAlign: 'left',
})

// loginText reads the CLI's session block; status (active | expired |
// logged_out) is used when the CLI sends it, else only loggedIn.
export function loginText(s) {
  if (s?.loggedIn) return s.username ? `logged in · ${s.username}` : 'logged in'
  if (s?.status === 'expired') return 'session expired'
  return 'logged out'
}

export default function AutomationCard({ automation: a, onOpen }) {
  const [hov, setHov] = useState(false)
  const s = a.session || {}
  const removed = !!a.removed
  const unavailable = !removed && a.available === false
  const disabled = !removed && !unavailable && a.enabled === false
  return (
    <button
      type="button"
      onClick={onOpen}
      onMouseEnter={() => setHov(true)}
      onMouseLeave={() => setHov(false)}
      aria-label={removed ? `${a.name || a.id}, uninstalled` : `${a.name || a.id}, ${loginText(s)}, ${a.actions} actions`}
      style={{ ...cardStyle(s.loggedIn && !unavailable && !removed, hov, removed), opacity: removed ? 0.5 : unavailable || disabled ? 0.75 : 1 }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
        <span title={a.name || a.id} style={{ ...mono, fontSize: 12, fontWeight: 700, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
          {a.name || a.id}
        </span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Dot on={s.loggedIn} warn={!s.loggedIn && s.status === 'expired'} />
        <span title={loginText(s)} style={{ ...muted, fontSize: 10, color: s.loggedIn ? 'var(--green-neon)' : 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {loginText(s)}
        </span>
      </div>
      <span style={{ ...muted, fontSize: 10 }}>{a.actions} action{a.actions === 1 ? '' : 's'}</span>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
        <Chip color={a.source === 'imported' ? 'var(--purple-light)' : a.source === 'local' ? 'var(--teal)' : 'var(--cyan)'}>
          {SOURCE_LABELS[a.source] || a.source} {a.version}
        </Chip>
        {removed && <Chip title="Uninstalled built-in — open it to restore"><Trash2 size={8} /> uninstalled</Chip>}
        {unavailable && <Chip color="var(--red)" title={a.unavailableReason}><AlertTriangle size={8} /> unavailable</Chip>}
        {disabled && <Chip><Circle size={7} /> disabled</Chip>}
        {a.modified && <Chip color="var(--yellow)" title="Differs from the copy shipped with this version"><PenLine size={8} /> modified</Chip>}
        {a.containsScripts && <Chip color="var(--orange)" title="Runs its own JavaScript in the page"><FileCode size={8} /> contains scripts</Chip>}
        {a.pendingUpdate && <Chip color="var(--green-neon)" title={`Version ${a.pendingUpdate} is held back because you modified this automation`}><ArrowUpCircle size={8} /> pending update</Chip>}
      </div>
    </button>
  )
}

export function CreateAutomationCard({ onRecord, onImport }) {
  return (
    <div style={{ ...cardStyle(false, false, true), cursor: 'default', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, ...mono, fontSize: 12, fontWeight: 700, color: 'var(--text)' }}>
        <Plus size={13} /> Create automation
      </div>
      <div style={{ ...body, fontSize: 11 }}>
        Record yourself doing a task in the browser and the AI turns it into an action, or import a package someone shared.
      </div>
      <div style={{ display: 'flex', gap: 6, marginTop: 'auto' }}>
        <button className="btn btn-secondary btn-sm" onClick={onRecord}>Record new</button>
        <button className="btn btn-ghost btn-sm" onClick={onImport}>Import</button>
      </div>
    </div>
  )
}
