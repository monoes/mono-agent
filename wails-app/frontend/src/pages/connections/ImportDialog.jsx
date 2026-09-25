// Import an automation package (spec §6.3): pick a .mpkg (or paste a path /
// URL), review what `automation install --dry-run` reports — publisher,
// domains, permissions, scripts, side effects, policy, changes on update —
// then confirm, which runs the real install.
import { useState } from 'react'
import { X, FolderOpen, AlertTriangle, FileCode } from 'lucide-react'
import { api } from '../../services/api.js'
import { Chip, EffectChip, ErrorBox, OkBox, Busy, body, label, mono, muted, panel, fileSize } from './ui.jsx'
import { IssueList } from './OverviewTab.jsx'

function Row({ title, children }) {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
      <span style={{ ...label, minWidth: 110, paddingTop: 2 }}>{title}</span>
      <div style={{ flex: 1, display: 'flex', flexWrap: 'wrap', gap: 4, minWidth: 0 }}>{children}</div>
    </div>
  )
}

const none = <span style={muted}>none</span>

function Review({ res }) {
  const r = res.review || {}
  const ch = r.changes
  const effects = Object.entries(r.actionEffects || {})
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ ...mono, fontSize: 13, color: 'var(--text)', fontWeight: 700 }}>
        {res.name || res.id} <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>{res.id} · {res.previousVersion ? `${res.previousVersion} → ${res.version}` : res.version}</span>
      </div>
      {r.policyBlocked && (
        <ErrorBox>
          <AlertTriangle size={11} style={{ verticalAlign: -1 }} /> Installs disabled: {r.policyReason || 'blocked by the usage policy in this build.'}
        </ErrorBox>
      )}
      {ch && (ch.addedDomains?.length || ch.addedSteps?.length || ch.addedScripts?.length || ch.changedScripts?.length) ? (
        <div style={{ ...panel, borderColor: 'var(--yellow)', display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ ...label, color: 'var(--yellow)' }}>This update changes what it can do</span>
          {ch.addedDomains?.length > 0 && <Row title="New domains">{ch.addedDomains.map(d => <Chip key={d} color="var(--yellow)">+ {d}</Chip>)}</Row>}
          {ch.addedSteps?.length > 0 && <Row title="New step types">{ch.addedSteps.map(d => <Chip key={d} color="var(--yellow)">+ {d}</Chip>)}</Row>}
          {ch.addedScripts?.length > 0 && <Row title="New scripts">{ch.addedScripts.map(d => <Chip key={d} color="var(--orange)">+ {d}</Chip>)}</Row>}
          {ch.changedScripts?.length > 0 && <Row title="Changed scripts">{ch.changedScripts.map(d => <Chip key={d} color="var(--orange)">~ {d}</Chip>)}</Row>}
        </div>
      ) : null}
      <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 8 }}>
        <Row title="Publisher"><span style={{ ...mono, fontSize: 11, color: r.publisher ? 'var(--text)' : 'var(--yellow)' }}>{r.publisher || 'unknown'}</span></Row>
        <Row title="Domains">{(r.domains || []).length ? r.domains.map(d => <Chip key={d} color="var(--cyan)">{d}</Chip>) : <span style={{ ...muted, color: 'var(--yellow)' }}>unrestricted</span>}</Row>
        <Row title="Step types">{(r.steps || []).length ? r.steps.map(d => <Chip key={d}>{d}</Chip>) : none}</Row>
        <Row title="Page scripts">{(r.scripts || []).length ? r.scripts.map(d => <Chip key={d} color="var(--orange)"><FileCode size={8} /> {d}</Chip>) : none}</Row>
        <Row title="Downloads"><span style={{ ...mono, fontSize: 11, color: r.downloads ? 'var(--yellow)' : 'var(--text-muted)' }}>{r.downloads ? 'allowed' : 'no'}</span></Row>
        <Row title="Policy tier"><span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)' }}>{r.tier || 'standard'}</span></Row>
      </div>
      {effects.length > 0 && (
        <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span style={label}>Actions and their side effects</span>
          {effects.map(([a, e]) => (
            <div key={a} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <span style={{ ...mono, fontSize: 11, color: 'var(--text)', flex: 1 }}>{a}</span><EffectChip effect={e} />
            </div>
          ))}
        </div>
      )}
      <IssueList issues={res.issues} />
      {(res.warnings || []).map((w, i) => <div key={i} style={{ ...muted, color: 'var(--yellow)' }}>⚠ {w}</div>)}
      {(r.files || []).length > 0 && (
        <details style={{ ...panel }}>
          <summary style={{ ...label, cursor: 'pointer' }}>Files ({r.files.length})</summary>
          <div style={{ marginTop: 6, display: 'flex', flexDirection: 'column', gap: 2 }}>
            {r.files.map(f => (
              <div key={f.path} style={{ display: 'flex', gap: 8 }}>
                <span style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', flex: 1, wordBreak: 'break-all' }}>{f.path}</span>
                <span style={{ ...muted, fontSize: 10 }}>{fileSize(f.size)}</span>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  )
}

export default function ImportDialog({ onClose, onInstalled }) {
  const [path, setPath] = useState('')
  const [phase, setPhase] = useState('pick') // pick | reviewing | review | installing | done
  const [res, setRes] = useState(null)
  const [error, setError] = useState('')
  const [installed, setInstalled] = useState(null)

  const review = async (p) => {
    const src = (p ?? path).trim()
    if (!src) return
    setPhase('reviewing'); setError(''); setRes(null)
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
    const out = await api.installAutomation(path.trim())
    if (!out || out.error) { setError(out?.error || 'Install failed.'); setPhase('review'); return }
    setInstalled(out); setPhase('done')
    onInstalled?.()
  }

  const hasErrors = (res?.issues || []).some(i => i.severity === 'error')
  return (
    <div className="modal-overlay" onClick={e => e.target === e.currentTarget && onClose()} style={{ zIndex: 1100 }}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="import-title" style={{ width: 600 }}>
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
          {phase === 'reviewing' && <Busy text="Reading package…" />}
          <ErrorBox>{error}</ErrorBox>
          {res && phase !== 'done' && <Review res={res} />}
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
              <button className="btn btn-primary btn-sm" onClick={install} disabled={phase !== 'review' || hasErrors}
                title={hasErrors ? 'The package has validation errors' : ''}>
                {phase === 'installing' ? 'Installing…' : res?.review?.policyBlocked ? 'Install (disabled)' : res?.previousVersion ? `Update to ${res.version}` : 'Install'}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
