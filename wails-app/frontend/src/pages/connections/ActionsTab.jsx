// Actions tab: each action with its side effects, inputs and outputs, and
// Run test / Use in workflow / Export action.
import { useState } from 'react'
import { Play, Workflow, Download, FlaskConical, FileCode } from 'lucide-react'
import { api } from '../../services/api.js'
import { confirm } from '../../components/ConfirmDialog.jsx'
import { Chip, EffectChip, ErrorBox, OkBox, Busy, body, label, mono, muted, panel, copyText } from './ui.jsx'

const RISKY = new Set(['write', 'message', 'destructive'])

function Inputs({ inputs }) {
  if (!inputs || !inputs.length) return <span style={muted}>no inputs</span>
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse', tableLayout: 'fixed' }}>
      <colgroup><col style={{ width: '32%' }} /><col style={{ width: '14%' }} /><col /></colgroup>
      <tbody>
        {inputs.map(i => (
          <tr key={i.name} style={{ borderTop: '1px solid var(--border-dim)' }}>
            <td style={{ ...mono, fontSize: 10.5, color: 'var(--text)', padding: '4px 8px 4px 0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'top' }}>
              {i.name}{i.required && <span style={{ color: 'var(--red)' }}> *</span>}
            </td>
            <td style={{ ...mono, fontSize: 10, color: 'var(--cyan)', padding: '4px 8px 4px 0', verticalAlign: 'top' }}>{i.type || 'string'}</td>
            <td style={{ ...body, fontSize: 10.5, padding: '4px 0', verticalAlign: 'top' }}>
              {i.description}
              {i.default !== undefined && i.default !== null && i.default !== '' && <span style={{ ...muted, fontSize: 10 }}> (default {JSON.stringify(i.default)})</span>}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function TestResults({ res }) {
  if (!res) return null
  if (res.error) return <ErrorBox>{res.error}</ErrorBox>
  const results = res.results || []
  if (!results.length) return <span style={muted}>No fixtures to run for this action.</span>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      {results.map((r, i) => (
        <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'baseline' }}>
          <Chip color={r.ok ? 'var(--green-neon)' : 'var(--red)'}>{r.ok ? 'pass' : 'fail'}</Chip>
          <span style={{ ...mono, fontSize: 10, color: 'var(--text-secondary)' }}>{r.fixture ? `fixture ${r.fixture}` : 'validation'}{r.message ? ` — ${r.message}` : ''}</span>
        </div>
      ))}
    </div>
  )
}

function ActionRow({ automationId, a }) {
  const [busy, setBusy] = useState('')
  const [testRes, setTestRes] = useState(null)
  const [note, setNote] = useState(null) // {ok, text}
  const nodeType = a.nodeType || `${automationId}.${a.name}`

  const runTest = async (live) => {
    if (live && RISKY.has(a.sideEffects) && !(await confirm(`Run ${a.name} live? It has "${a.sideEffects}" side effects and acts on the real site with your login.`))) return
    setBusy(live ? 'live' : 'test'); setTestRes(null)
    try { setTestRes(await api.testAutomation(automationId, a.name, live)) } finally { setBusy('') }
  }
  const useInWorkflow = async () => {
    await copyText(nodeType)
    setNote({ ok: true, text: `Copied ${nodeType} — add it from the workflow editor's node palette (search for it).` })
  }
  const exportAction = async () => {
    const path = await api.chooseAutomationExportPath(`${nodeType}.mpkg`)
    if (!path) return
    setBusy('export')
    try {
      const res = await api.exportAction(nodeType, path)
      setNote(res?.error ? { ok: false, text: res.error } : { ok: true, text: `Exported to ${res?.file || path}` })
    } finally { setBusy('') }
  }

  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
        <span style={{ ...mono, fontSize: 12, fontWeight: 700, color: 'var(--text)' }}>{a.name}</span>
        <EffectChip effect={a.sideEffects} />
        {a.containsScript && <Chip color="var(--orange)"><FileCode size={8} /> script</Chip>}
        <span style={{ ...muted, fontSize: 9.5, marginLeft: 'auto' }}>{nodeType}</span>
      </div>
      {a.description && <div style={{ ...body, fontSize: 11 }}>{a.description}</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={label}>Inputs</span>
        <Inputs inputs={a.inputs} />
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, alignItems: 'center' }}>
        <span style={{ ...label, marginRight: 4 }}>Outputs</span>
        {(a.outputs || []).length ? a.outputs.map(o => <Chip key={o} color="var(--teal)">{o}</Chip>) : <span style={muted}>none</span>}
      </div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        <button className="btn btn-secondary btn-sm" onClick={() => runTest(false)} disabled={!!busy} style={{ gap: 5 }}><FlaskConical size={11} /> Run test</button>
        <button className="btn btn-ghost btn-sm" onClick={() => runTest(true)} disabled={!!busy} style={{ gap: 5 }}><Play size={11} /> Run live</button>
        <button className="btn btn-ghost btn-sm" onClick={useInWorkflow} style={{ gap: 5 }}><Workflow size={11} /> Use in workflow</button>
        <button className="btn btn-ghost btn-sm" onClick={exportAction} disabled={!!busy} style={{ gap: 5 }}><Download size={11} /> Export action</button>
      </div>
      {busy === 'test' && <Busy text="Running fixture tests…" />}
      {busy === 'live' && <Busy text="Running live in your browser…" />}
      <TestResults res={testRes} />
      {note && (note.ok ? <OkBox>{note.text}</OkBox> : <ErrorBox>{note.text}</ErrorBox>)}
    </div>
  )
}

export default function ActionsTab({ automationId, actions }) {
  if (!actions.length) return <div style={body}>This automation has no actions yet. Record one from the extension side panel, then save it here from the Recordings tab.</div>
  return actions.map(a => <ActionRow key={a.name} automationId={automationId} a={a} />)
}
