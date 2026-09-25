// Review a recording (spec §8.6): `record analyze` drafts an action with
// AI-proposed names, `record verify` replays it (safe mode stops before the
// first side-effecting step), `record save` stores it as an action,
// fragment or workflow. Names are editable before saving.
import { useEffect, useState } from 'react'
import { ArrowLeft, ShieldCheck, Play, Save, Sparkles } from 'lucide-react'
import { api } from '../../services/api.js'
import { confirm } from '../../components/ConfirmDialog.jsx'
import { Chip, EffectChip, ErrorBox, OkBox, Busy, body, label, mono, muted, panel } from './ui.jsx'
import { IssueList } from './OverviewTab.jsx'

const VERIFY_COLORS = { pass: 'var(--green-neon)', healed: 'var(--cyan)', fail: 'var(--red)', stopped_before_side_effect: 'var(--yellow)', skipped: 'var(--text-muted)' }
const VERIFY_LABELS = { stopped_before_side_effect: 'stopped (side effect)' }
const RISKY = new Set(['write', 'message', 'destructive'])
const inputStyle = { ...mono, fontSize: 11, background: 'var(--surface)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', color: 'var(--text)', padding: '5px 8px', minWidth: 0 }

function Field({ label: text, value, onChange, id }) {
  return (
    <label htmlFor={id} style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 160 }}>
      <span style={label}>{text}</span>
      <input id={id} value={value} onChange={e => onChange(e.target.value)} style={inputStyle} />
    </label>
  )
}

function StepList({ steps, verify }) {
  const byId = Object.fromEntries((verify?.steps || []).map(s => [s.id, s]))
  return (
    <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 5 }}>
      {steps.map((s, i) => {
        const v = byId[s.id]
        return (
          <li key={s.id || i} style={{ display: 'flex', gap: 8, alignItems: 'baseline', borderTop: i ? '1px solid var(--border-dim)' : 'none', paddingTop: i ? 5 : 0 }}>
            <span style={{ ...mono, fontSize: 10, color: 'var(--text-dim)', minWidth: 18, textAlign: 'right' }}>{i + 1}</span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
                <span style={{ ...mono, fontSize: 10.5, color: 'var(--cyan)' }}>{s.type}</span>
                {s.sideEffect && <Chip color="var(--orange)">side effect</Chip>}
                {v && <Chip color={VERIFY_COLORS[v.status] || 'var(--text-muted)'}>{VERIFY_LABELS[v.status] || v.status}</Chip>}
              </div>
              <div style={{ ...body, fontSize: 11 }}>{s.intent || s.description || s.configKey || s.url || s.id}</div>
              {v?.message && <div style={{ ...muted, fontSize: 10, color: v.status === 'fail' ? 'var(--red)' : 'var(--text-muted)' }}>{v.message}</div>}
              {(v?.selector || s.configKey) && <div style={{ ...muted, fontSize: 9.5, wordBreak: 'break-all' }}>{v?.selector || s.configKey}</div>}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

export default function RecordingReview({ recording, automationId, onBack, onSaved }) {
  const [phase, setPhase] = useState('analyzing') // analyzing | ready | verifying | saving | saved
  const [error, setError] = useState('')
  const [draftDir, setDraftDir] = useState('')
  const [draft, setDraft] = useState(null)
  const [verify, setVerify] = useState(null)
  const [saved, setSaved] = useState(null)
  const [names, setNames] = useState({ action: '', automation: '', fragment: '' })
  const [inputNames, setInputNames] = useState({})
  const [saveAs, setSaveAs] = useState('action')

  useEffect(() => {
    let live = true
    ;(async () => {
      const res = await api.analyzeRecording(recording.id, automationId)
      if (!live) return
      if (!res || res.error) { setError(res?.error || 'Analysis failed.'); setPhase('ready'); return }
      const d = res.draft || {}
      setDraftDir(res.draftDir)
      setDraft(d)
      setNames({ action: d.names?.action || d.action || '', automation: d.names?.automation || d.targetAutomation || automationId, fragment: d.names?.fragment || '' })
      setInputNames(Object.fromEntries((d.actionDef?.inputs || []).map(i => [i.name, i.name])))
      setSaveAs(d.saveAs || 'action')
      setPhase('ready')
    })()
    return () => { live = false }
  }, [recording.id, automationId])

  const def = draft?.actionDef || {}
  const steps = def.steps || []

  const runVerify = async (full) => {
    if (full && RISKY.has(def.sideEffects) && !(await confirm(`Run the whole action for real? It has "${def.sideEffects}" side effects — it will act on the site with your login.`))) return
    setPhase('verifying'); setError('')
    const res = await api.verifyDraft(draftDir, full)
    if (!res || res.error) setError(res?.error || 'Verify failed.')
    else setVerify(res)
    setPhase('ready')
  }

  const save = async () => {
    setPhase('saving'); setError('')
    const renameInputs = Object.fromEntries(Object.entries(inputNames).filter(([from, to]) => to && to !== from))
    const spec = {
      as: saveAs,
      name: saveAs === 'fragment' ? names.fragment || names.action : names.action,
      renameInputs,
      ...(draft?.isNew ? { new: names.automation } : { automation: names.automation || automationId }),
    }
    const res = await api.saveDraft(draftDir, spec)
    if (!res || res.error) { setError(res?.error || 'Save failed.'); setPhase('ready'); return }
    setSaved(res); setPhase('saved')
    onSaved?.()
  }

  const busy = phase === 'analyzing' || phase === 'verifying' || phase === 'saving'
  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <button className="btn btn-ghost btn-sm" onClick={onBack} style={{ gap: 5 }}><ArrowLeft size={11} /> Recordings</button>
        <span style={{ ...mono, fontSize: 11.5, color: 'var(--text)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{recording.title || recording.id}</span>
      </div>
      {phase === 'analyzing' && <Busy text="Analyzing the recording with AI — this can take a minute…" />}
      <ErrorBox>{error}</ErrorBox>

      {draft && (
        <>
          <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <Sparkles size={12} color="var(--cyan)" />
              <span style={{ ...label, flex: 1 }}>Names proposed by the AI — edit before saving</span>
              {def.sideEffects && <EffectChip effect={def.sideEffects} />}
            </div>
            {def.description && <div style={{ ...body, fontSize: 11 }}>{def.description}</div>}
            <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
              <Field id="rr-action" label="Action name" value={names.action} onChange={v => setNames(n => ({ ...n, action: v }))} />
              {draft.isNew
                ? <Field id="rr-automation" label="New automation id" value={names.automation} onChange={v => setNames(n => ({ ...n, automation: v }))} />
                : <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 160 }}><span style={label}>Saves into</span><span style={{ ...mono, fontSize: 11, color: 'var(--text)', padding: '5px 0' }}>{names.automation}</span></div>}
              {saveAs === 'fragment' && <Field id="rr-fragment" label="Fragment name" value={names.fragment} onChange={v => setNames(n => ({ ...n, fragment: v }))} />}
            </div>
            {Object.keys(inputNames).length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <span style={label}>Inputs</span>
                {(def.inputs || []).map(i => (
                  <div key={i.name} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                    <input aria-label={`Name for input ${i.name}`} value={inputNames[i.name] ?? i.name} onChange={e => setInputNames(m => ({ ...m, [i.name]: e.target.value }))} style={{ ...inputStyle, width: 180 }} />
                    <span style={{ ...mono, fontSize: 10, color: 'var(--cyan)' }}>{i.type || 'string'}</span>
                    <span style={{ ...muted, fontSize: 10, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{i.description || (draft.recordedInputs?.[i.name] !== undefined ? `recorded: ${JSON.stringify(draft.recordedInputs[i.name])}` : i.default !== undefined ? `recorded: ${JSON.stringify(i.default)}` : '')}</span>
                  </div>
                ))}
              </div>
            )}
            {(def.outputs || []).length > 0 && (
              <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', alignItems: 'center' }}>
                <span style={{ ...label, marginRight: 4 }}>Outputs</span>
                {def.outputs.map(o => <Chip key={o} color="var(--teal)">{o}</Chip>)}
              </div>
            )}
          </div>

          <IssueList issues={draft.lint} />

          <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
              <span style={{ ...label, flex: 1 }}>Steps ({steps.length})</span>
              <button className="btn btn-secondary btn-sm" onClick={() => runVerify(false)} disabled={busy || !draftDir} style={{ gap: 5 }}><ShieldCheck size={11} /> Verify (safe)</button>
              <button className="btn btn-ghost btn-sm" onClick={() => runVerify(true)} disabled={busy || !draftDir} style={{ gap: 5 }}><Play size={11} /> Verify full</button>
            </div>
            {phase === 'verifying' && <Busy text="Replaying in your browser…" />}
            {verify && (verify.ok
              ? <OkBox>{verify.stoppedAt ? 'Verified up to the first side-effecting step — it was not executed.' : 'All steps passed.'}</OkBox>
              : <ErrorBox>Verification found failing steps — fix or re-record them before saving.</ErrorBox>)}
            {steps.length ? <StepList steps={steps} verify={verify} /> : <span style={muted}>The draft has no steps.</span>}
          </div>

          {phase === 'saved' ? (
            <OkBox>
              Saved {saved?.nodeType ? `as node ${saved.nodeType}` : ''}{saved?.version ? ` (version ${saved.version})` : ''}{saved?.workflowId ? ` — workflow ${saved.workflowId} created` : ''}.
            </OkBox>
          ) : (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <label htmlFor="rr-saveas" style={label}>Save as</label>
              <select id="rr-saveas" className="form-select" value={saveAs} onChange={e => setSaveAs(e.target.value)} style={{ width: 'auto', padding: '5px 30px 5px 10px', fontSize: 11 }}>
                <option value="action">Action (workflow node)</option>
                <option value="fragment">Fragment (part of a node)</option>
                <option value="workflow">Workflow draft</option>
              </select>
              <button className="btn btn-primary btn-sm" onClick={save} disabled={busy || !names.action || (draft.isNew && !names.automation)} style={{ gap: 5 }}>
                <Save size={11} /> {phase === 'saving' ? 'Saving…' : 'Save'}
              </button>
              {!verify && <span style={{ ...muted, fontSize: 10 }}>Tip: verify first.</span>}
            </div>
          )}
        </>
      )}
    </>
  )
}
