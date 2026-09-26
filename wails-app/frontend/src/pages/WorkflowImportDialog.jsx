// Import a workflow (`monoagentcli workflow import --json`, via the
// ImportWorkflowFull binding). Everything shown is what the CLI reported:
// created / updated / unchanged, the bundled automations with their status,
// the missing ones and the per-package review. Installing bundled
// automations re-runs the same import with --yes (imports are idempotent),
// only after an explicit confirmation.
import { useState } from 'react'
import { X, FolderOpen, ExternalLink, Package, Download } from 'lucide-react'
import { api } from '../services/api.js'
import { emitAutomationsChanged } from '../lib/appEvents.js'
import { confirm } from '../components/ConfirmDialog.jsx'
import { Chip, ErrorBox, OkBox, Busy, body, label, mono, muted, panel, useDialog } from './connections/ui.jsx'

const STATUS_TEXT = {
  created: (n) => `Imported “${n}”.`,
  updated: (n) => `Updated “${n}” — it was imported before; this version replaced it.`,
  unchanged: () => 'Already imported — nothing changed.',
}
const ITEM_COLORS = { present: 'var(--green-neon)', installed: 'var(--green-neon)', missing: 'var(--yellow)', conflict: 'var(--red)', failed: 'var(--red)' }

// installSummary describes the --yes re-run: the workflow itself comes back
// unchanged, so the news is what happened to the bundled packages.
function installSummary(r) {
  const items = r.automations || []
  const done = items.filter(i => i.status === 'installed').length
  const failed = items.filter(i => i.status === 'failed').length
  const parts = [`Installed ${done} bundled automation${done === 1 ? '' : 's'} for “${r.name || r.id}”.`]
  if (failed) parts.push(`${failed} failed — see below.`)
  return parts.join(' ')
}

// ReviewDetail: what installing one bundled package means, from the CLI's
// dry-run review (reviewDetail), before anything is installed.
function ReviewDetail({ item }) {
  const d = item.reviewDetail
  const line = { fontFamily: 'var(--font-mono)', fontSize: 10.5 }
  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 3 }}>
      <span style={{ ...line, fontSize: 11.5, color: 'var(--text)' }}>{item.id} {item.version}</span>
      {d ? (
        <>
          <span style={line}>Publisher: {d.publisher || 'unknown'}</span>
          <span style={line}>Domains: {(d.domains || []).join(', ') || 'unrestricted'}</span>
          {(d.capabilities || []).length > 0 && (
            <ul style={{ margin: 0, paddingLeft: 16 }}>{d.capabilities.map(c => <li key={c} style={line}>{c}</li>)}</ul>
          )}
          {d.replaces && <span style={{ ...line, color: 'var(--red)' }}>Replaces {d.replaces.source} {d.replaces.id} {d.replaces.version}</span>}
        </>
      ) : item.review ? <span style={line}>{item.review}</span> : null}
      {item.error && <span style={{ ...line, color: 'var(--red)' }}>{item.error}</span>}
    </div>
  )
}

function Automations({ items }) {
  if (!items?.length) return null
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={label}>Bundled automations</span>
      {items.map(it => (
        <div key={it.id} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <Package size={11} color="var(--text-muted)" />
            <span style={{ ...mono, fontSize: 11, color: 'var(--text)', flex: 1 }}>{it.id} <span style={muted}>{it.version}</span></span>
            <Chip color={ITEM_COLORS[it.status] || 'var(--text-muted)'}>{it.status}{it.installedVersion && it.status === 'present' ? ` ${it.installedVersion}` : ''}</Chip>
          </div>
          {it.error && <span style={{ ...muted, fontSize: 10, color: 'var(--red)', paddingLeft: 19 }}>{it.error}</span>}
          {it.review && <span style={{ ...muted, fontSize: 10, paddingLeft: 19, wordBreak: 'break-word' }}>{it.review}</span>}
        </div>
      ))}
    </div>
  )
}

export default function WorkflowImportDialog({ onClose, onOpen, onImported, onAutomationsInstalled }) {
  const dialog = useDialog(onClose)
  const [path, setPath] = useState('')
  const [pasted, setPasted] = useState('')
  const [asNew, setAsNew] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [res, setRes] = useState(null)
  const [installRes, setInstallRes] = useState(null)

  const input = pasted.trim() || path.trim()

  const run = async (opts) => {
    setError('')
    const out = await api.importWorkflowFull(input, opts)
    if (!out || out.error) { setError(out?.error || 'Import failed.'); return null }
    return out
  }
  const doImport = async () => {
    setBusy('import'); setRes(null); setInstallRes(null)
    try { const out = await run({ asNew }); if (out) { setRes(out); onImported?.(out) } } finally { setBusy('') }
  }
  const browse = async () => {
    const p = await api.chooseWorkflowFile()
    if (p) { setPath(p); setPasted(''); setRes(null); setInstallRes(null); setError('') }
  }
  const shown = installRes || res
  const missing = (shown?.automations || []).filter(i => i.status === 'missing')
  const installMissing = async () => {
    const message = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <span>Install {missing.length} bundled automation{missing.length === 1 ? '' : 's'} from this workflow file?</span>
        {missing.map(i => <ReviewDetail key={i.id} item={i} />)}
        <span>They come from the file, not from a source you chose separately. Each package is checked against its sha256 and reviewed before anything is written.</span>
      </div>
    )
    if (!(await confirm(message, { title: 'Install bundled automations', confirmLabel: 'Install' }))) return
    setBusy('install')
    try {
      // Re-import with --yes: the workflow itself comes back unchanged.
      const out = await run({ yes: true })
      if (out) {
        setInstallRes(out)
        if ((out.automations || []).some(i => i.status === 'installed')) {
          onAutomationsInstalled?.(out)
          emitAutomationsChanged({ source: 'workflow-import' })
        }
      }
    } finally { setBusy('') }
  }

  const stillMissing = missing.length > 0
  const edit = (fn) => (e) => { fn(e); setRes(null); setInstallRes(null); setError('') }
  return (
    <div className="modal-overlay" onClick={e => e.target === e.currentTarget && onClose()} style={{ zIndex: 1100 }}>
      <div {...dialog} className="modal" role="dialog" aria-modal="true" aria-labelledby="wf-import-title" style={{ width: 600 }}>
        <div className="modal-title">
          <span id="wf-import-title">Import workflow</span>
          <button className="btn btn-ghost btn-icon" onClick={onClose} aria-label="Close"><X size={15} /></button>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={body}>Choose a workflow <code>.json</code> file, or paste the workflow JSON. Importing the same file again updates it instead of making a copy.</div>
          <div style={{ display: 'flex', gap: 6 }}>
            <input className="form-input" aria-label="Workflow file path" placeholder="/path/to/workflow.json" value={path}
              onChange={edit(e => setPath(e.target.value))} onKeyDown={e => { if (e.key === 'Enter' && input) doImport() }}
              style={{ ...mono, fontSize: 11, padding: '6px 10px' }} />
            <button className="btn btn-secondary btn-sm" onClick={browse} style={{ gap: 5 }}><FolderOpen size={11} /> Browse</button>
          </div>
          <textarea className="form-textarea" aria-label="Or paste workflow JSON" placeholder='…or paste workflow JSON here: {"name": …, "nodes": […]}' value={pasted}
            onChange={edit(e => setPasted(e.target.value))} rows={4} style={{ fontSize: 11 }} />
          <label style={{ display: 'flex', gap: 8, alignItems: 'center', ...body, fontSize: 11.5 }}>
            <input type="checkbox" checked={asNew} onChange={edit(e => setAsNew(e.target.checked))} />
            Import as a new copy (even if this workflow was imported before)
          </label>
          {busy === 'import' && <Busy text="Importing…" />}
          <ErrorBox>{error}</ErrorBox>

          {shown && (
            <>
              <OkBox>
                {installRes
                  ? installSummary(installRes)
                  : (STATUS_TEXT[shown.status] || STATUS_TEXT.created)(shown.name || shown.id)}
              </OkBox>
              <Automations items={shown.automations} />
              {(shown.reviewLines || []).length > 0 && (
                <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 4 }}>
                  <span style={label}>Install review</span>
                  {shown.reviewLines.map((l, i) => <span key={i} style={{ ...muted, fontSize: 10.5, wordBreak: 'break-word' }}>{l}</span>)}
                </div>
              )}
              {stillMissing && (
                <div style={{ ...panel, borderColor: 'var(--yellow)', display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <span style={{ ...body, fontSize: 11.5 }}>
                    The workflow was imported, but it needs {missing.length === 1 ? 'an automation that is' : `${missing.length} automations that are`} not installed. Its nodes for {missing.map(i => i.id).join(', ')} will not run until you install them.
                  </span>
                  {shown.installCommand && <code style={{ ...mono, fontSize: 10, color: 'var(--text-muted)', wordBreak: 'break-all' }}>{shown.installCommand}</code>}
                  <button className="btn btn-primary btn-sm" onClick={installMissing} disabled={!!busy} style={{ alignSelf: 'flex-start', gap: 5 }}>
                    <Download size={11} /> {busy === 'install' ? 'Installing…' : 'Install bundled automations'}
                  </button>
                </div>
              )}
            </>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn btn-ghost btn-sm" onClick={onClose}>{shown ? 'Close' : 'Cancel'}</button>
          {shown ? (
            <button className="btn btn-primary btn-sm" onClick={() => onOpen(shown.id)} style={{ gap: 5 }}><ExternalLink size={11} /> Open</button>
          ) : (
            <button className="btn btn-primary btn-sm" onClick={doImport} disabled={!input || !!busy}>Import</button>
          )}
        </div>
      </div>
    </div>
  )
}
