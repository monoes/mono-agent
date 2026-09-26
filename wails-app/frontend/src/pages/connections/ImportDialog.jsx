// Import an automation package (spec §6.3): pick a .mpkg (or paste a path /
// URL), review what `automation install --dry-run` reports, then confirm.
// The install passes the reviewed sha256 (--expect-sha256) so the bytes
// installed are the bytes reviewed, and replacing a built-in needs an
// explicit tick (--replace-builtin).
import { useState } from 'react'
import { X, FolderOpen, AlertTriangle } from 'lucide-react'
import { api } from '../../services/api.js'
import { ErrorBox, OkBox, Busy, SOURCE_LABELS, body, mono, useDialog } from './ui.jsx'
import ImportReview, { replacesProtected } from './ImportReview.jsx'

export default function ImportDialog({ installedPackages = [], onClose, onInstalled }) {
  const dialog = useDialog(onClose)
  const [path, setPath] = useState('')
  const [phase, setPhase] = useState('pick') // pick | reviewing | review | installing | done
  const [res, setRes] = useState(null)
  const [error, setError] = useState('')
  const [installed, setInstalled] = useState(null)
  const [replaceOk, setReplaceOk] = useState(false)

  const review = async (p) => {
    const src = (p ?? path).trim()
    if (!src) return
    setPhase('reviewing'); setError(''); setRes(null); setReplaceOk(false)
    const out = await api.installAutomationDryRun(src)
    if (!out || out.error) { setError(out?.error || 'Could not read the package.'); setPhase('pick'); return }
    setRes(out); setPhase('review')
  }
  const browse = async () => {
    const p = await api.chooseAutomationPackage()
    if (!p) return
    setPath(p)
    review(p)
  }
  const install = async () => {
    setPhase('installing'); setError('')
    const out = await api.installAutomation(path.trim(), { expectSha256: res?.sha256 || '', replaceBuiltin: replacesProtected(res?.review) && replaceOk })
    if (!out || out.error) { setError(out?.error || 'Install failed.'); setPhase('review'); return }
    setInstalled(out); setPhase('done')
    onInstalled?.()
  }

  const insecureURL = /^http:\/\//i.test(path.trim())
  const replaces = replacesProtected(res?.review) ? res.review.replaces : null
  // Opt-ins reset when package content changes (by design): warn on an
  // update when the installed package currently has either allowed.
  const current = installedPackages.find(a => a.id === res?.id && !a.removed)
  const resetsTrust = !!res?.previousVersion && !replaces && !!current && (current.scriptsAllowed || current.liveRunConfirmed) && ['imported', 'recorded'].includes(current.trust)
  const hasErrors = (res?.issues || []).some(i => i.severity === 'error')
  const blocked = phase !== 'review' || hasErrors || (replaces && !replaceOk)
  return (
    <div className="modal-overlay" onClick={e => e.target === e.currentTarget && onClose()} style={{ zIndex: 1100 }}>
      <div {...dialog} className="modal" role="dialog" aria-modal="true" aria-labelledby="import-title" style={{ width: 640 }}>
        <div className="modal-title">
          <span id="import-title">Import automation</span>
          <button className="btn btn-ghost btn-icon" onClick={onClose} aria-label="Close"><X size={15} /></button>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={body}>Choose a <code>.mpkg</code> file someone shared, or paste a path or https URL. You review what it can do before anything is installed.</div>
          <div style={{ display: 'flex', gap: 6 }}>
            <input className="form-input" aria-label="Package path or URL" placeholder="/path/to/package.mpkg or https://…" value={path}
              onChange={e => { setPath(e.target.value); if (phase !== 'pick') { setPhase('pick'); setRes(null) } }}
              onKeyDown={e => { if (e.key === 'Enter') review() }}
              style={{ ...mono, fontSize: 11, padding: '6px 10px' }} />
            <button className="btn btn-secondary btn-sm" onClick={browse} style={{ gap: 5 }}><FolderOpen size={11} /> Browse</button>
            {phase === 'pick' && <button className="btn btn-ghost btn-sm" onClick={() => review()} disabled={!path.trim()}>Review</button>}
          </div>
          {insecureURL && (
            <ErrorBox><AlertTriangle size={11} style={{ verticalAlign: -1 }} /> Plain http:// is not accepted: anyone on the network could swap the package. Use an https:// URL or download the file first.</ErrorBox>
          )}
          {phase === 'reviewing' && <Busy text="Reading package…" />}
          <ErrorBox>{error}</ErrorBox>
          {res && phase !== 'done' && <ImportReview res={res} hideCliConfirmHint={!!replaces} resetsTrust={resetsTrust} />}
          {replaces && phase !== 'done' && (
            <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start', ...body, fontSize: 11.5, color: 'var(--text)' }}>
              <input type="checkbox" checked={replaceOk} onChange={e => setReplaceOk(e.target.checked)} style={{ marginTop: 2 }} />
              I understand this replaces the {SOURCE_LABELS[replaces.source] || replaces.source} {replaces.id}
            </label>
          )}
          {phase === 'done' && (
            <OkBox>Installed {installed?.name || installed?.id} {installed?.version}{installed?.review?.policyBlocked ? ' — disabled by the usage policy in this build' : ''}.</OkBox>
          )}
        </div>
        <div className="modal-actions">
          {phase === 'done' ? (
            <button className="btn btn-primary btn-sm" onClick={onClose}>Done</button>
          ) : (
            <>
              <button className="btn btn-ghost btn-sm" onClick={onClose}>Cancel</button>
              <button className="btn btn-primary btn-sm" onClick={install} disabled={blocked}
                title={hasErrors ? 'The package has validation errors' : replaces && !replaceOk ? 'Tick the box to confirm the replacement' : ''}>
                {phase === 'installing' ? 'Installing…' : res?.review?.policyBlocked ? 'Install (disabled)' : replaces ? `Replace ${replaces.source === 'local' ? 'local' : 'built-in'} ${replaces.id}` : res?.previousVersion ? `Update to ${res.version}` : 'Install'}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
