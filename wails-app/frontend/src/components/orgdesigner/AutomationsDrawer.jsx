// Automations drawer (plan §7.4): the org's automations with their alias,
// an outbound badge (real-world side effects), open-in-editor, and remove;
// plus the Unassigned library to add from. Rows are drag sources: drop one
// on empty canvas for an automation role, or on an agent role to grant it.
import { useCallback, useState } from 'react'
import { Workflow, GripVertical, ExternalLink, Trash2, Plus, RefreshCw, AlertTriangle, Send } from 'lucide-react'
import { api, notify } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'
import { Chip, mono, mutedText, sectionLabel, smallBtn } from '../orgs/ui.jsx'

/** Role-id-shaped alias from a workflow name (plan §2 naming rule). */
export function slugifyAlias(name) {
  const s = String(name || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 40)
  return /^[a-z]/.test(s) ? s : `automation_${s || 'new'}`.replace(/_+$/, '').slice(0, 40)
}

function LibraryRow({ wf, onAdd, busy }) {
  const [alias, setAlias] = useState(() => slugifyAlias(wf.name))
  const valid = /^[a-z][a-z0-9_]{0,39}$/.test(alias) && !['human', 'workflow', 'status', 'output'].includes(alias)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, padding: '6px 8px', border: '1px solid var(--border)', borderRadius: 'var(--radius)' }}>
      <span style={{ ...mono, fontSize: 11, color: 'var(--text)' }}>{wf.name || wf.id}</span>
      <div style={{ display: 'flex', gap: 4 }}>
        <input
          aria-label={`Alias for ${wf.name || wf.id}`}
          value={alias}
          onChange={e => setAlias(e.target.value)}
          style={{ flex: 1, minWidth: 0, ...mono, fontSize: 10.5, padding: '3px 6px', background: 'var(--bg)', border: `1px solid ${valid ? 'var(--border)' : 'var(--red, #ef4444)'}`, borderRadius: 4, color: 'var(--text)' }}
        />
        <button style={smallBtn} disabled={busy || !valid} onClick={() => onAdd(wf, alias)}>
          <Plus size={10} /> Add
        </button>
      </div>
      {!valid && <span style={{ ...mono, fontSize: 9.5, color: '#f87171' }}>Use lowercase letters, digits, - or _.</span>}
    </div>
  )
}

export default function AutomationsDrawer({ orgName, automations, grants, loading, error, onRefresh, onDragStart, onOpenWorkflow }) {
  const [libraryOpen, setLibraryOpen] = useState(false)
  const [library, setLibrary] = useState(null)
  const [libraryError, setLibraryError] = useState('')
  const [busy, setBusy] = useState(false)

  const loadLibrary = useCallback(async () => {
    setLibraryError('')
    const res = await api.listUnassignedAutomations()
    if (!res || res.error) {
      setLibrary([])
      setLibraryError(res?.error || 'Could not load the library.')
      return
    }
    setLibrary(Array.isArray(res.workflows) ? res.workflows : [])
  }, [])

  const toggleLibrary = () => {
    const next = !libraryOpen
    setLibraryOpen(next)
    if (next) loadLibrary()
  }

  const add = async (wf, alias) => {
    setBusy(true)
    try {
      const res = await api.addOrgAutomation(orgName, wf.id, alias)
      if (!res || res.error) { notify('add automation', res?.error || 'failed to add automation'); return }
      await Promise.all([onRefresh?.(), loadLibrary()])
    } finally {
      setBusy(false)
    }
  }

  const remove = async (a) => {
    const used = (grants || []).filter(g => g.alias === a.alias).length
    const ok = await confirm(
      used ? `Remove ${a.alias}? ${used} grant${used === 1 ? '' : 's'} to it will be revoked.` : `Remove ${a.alias} from ${orgName}?`,
      { title: 'Remove automation', confirmLabel: 'Remove', danger: true },
    )
    if (!ok) return
    setBusy(true)
    try {
      const res = await api.removeOrgAutomation(orgName, a.alias)
      if (!res || res.error) { notify('remove automation', res?.error || 'failed to remove automation'); return }
      await onRefresh?.()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 8, padding: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Workflow size={12} style={{ color: 'var(--text-muted)' }} />
        <span style={sectionLabel}>Automations</span>
        <span style={{ flex: 1 }} />
        <button style={{ ...smallBtn, padding: 3 }} onClick={onRefresh} aria-label="Refresh automations"><RefreshCw size={10} /></button>
      </div>
      <div style={{ ...mutedText, fontSize: 10 }}>Drag onto the canvas to add an automation role, or onto a role to grant it.</div>

      {loading && <div style={{ display: 'flex', justifyContent: 'center', padding: 8 }}><div className="spinner" /></div>}
      {!loading && error && <div style={{ ...mono, fontSize: 10.5, color: '#f87171' }}>{error}</div>}
      {!loading && !error && automations.length === 0 && (
        <div style={{ ...mono, fontSize: 10.5, color: 'var(--text-muted)' }}>No automations yet. Add one from the library.</div>
      )}

      {automations.map(a => (
        <div
          key={a.alias}
          data-testid="org-automation"
          onMouseDown={(e) => { if (e.button === 0 && !e.target.closest('button')) { e.preventDefault(); onDragStart?.(a, e) } }}
          title="Drag onto the canvas or a role"
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '6px 6px', border: '1px solid var(--border)', borderRadius: 'var(--radius)', background: 'var(--surface)', cursor: 'grab' }}
        >
          <GripVertical size={11} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 3 }}>
            <span style={{ ...mono, fontSize: 11, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.workflow_name || a.alias}</span>
            <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
              <Chip>{a.alias}</Chip>
              {a.has_outbound_nodes && (
                <Chip color="var(--red, #ef4444)" title={`Sends outside mono-agent: ${(a.outbound_nodes || []).join(', ') || 'outbound nodes'}`}>
                  <Send size={8} /> outbound
                </Chip>
              )}
              {a.exists === false && <Chip color="#eab308" title="The workflow was deleted"><AlertTriangle size={8} /> missing</Chip>}
            </div>
          </div>
          <button style={{ ...smallBtn, border: 'none', padding: 3 }} disabled={a.exists === false} onClick={() => onOpenWorkflow?.(a.workflow_id)} aria-label={`Open workflow ${a.alias}`} title="Open workflow">
            <ExternalLink size={11} />
          </button>
          <button style={{ ...smallBtn, border: 'none', padding: 3 }} disabled={busy} onClick={() => remove(a)} aria-label={`Remove ${a.alias}`} title="Remove from org">
            <Trash2 size={11} />
          </button>
        </div>
      ))}

      <button style={{ ...smallBtn, justifyContent: 'center' }} onClick={toggleLibrary} aria-expanded={libraryOpen}>
        <Plus size={10} /> {libraryOpen ? 'Hide library' : 'Add from library'}
      </button>
      {libraryOpen && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={sectionLabel}>Unassigned</span>
          {library === null && <div style={{ display: 'flex', justifyContent: 'center', padding: 6 }}><div className="spinner" /></div>}
          {libraryError && <div style={{ ...mono, fontSize: 10.5, color: '#f87171' }}>{libraryError}</div>}
          {library && !libraryError && library.length === 0 && <div style={mutedText}>Every workflow already belongs to an org.</div>}
          {(library || []).map(wf => <LibraryRow key={wf.id} wf={wf} busy={busy} onAdd={add} />)}
        </div>
      )}
    </div>
  )
}
