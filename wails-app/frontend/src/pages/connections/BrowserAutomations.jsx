// "Browser Automations" section of the Connections page (spec §7.1): one
// card per installed package from `automation list --json`, plus the
// Create card. Also the "Record new" explainer dialog.
import { X, Library } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import AutomationCard, { CreateAutomationCard } from './AutomationCard.jsx'
import { SectionHeader, ErrorBox, body, mono, muted, panel, useDialog } from './ui.jsx'
import LogInToMonoesButton from '../../components/library/LogInToMonoesButton.jsx'

export default function BrowserAutomations({ automations, error, onOpen, onRecord, onImport, onLibrary }) {
  const { t } = useTranslation()
  const all = automations || []
  const list = all.filter(a => !a.removed)
  const removed = all.filter(a => a.removed)
  const loggedIn = list.filter(a => a.session?.loggedIn).length
  return (
    <section aria-labelledby="browser-automations-title">
      <h2 id="browser-automations-title" style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>Browser automations</h2>
      <SectionHeader title="Browser Automations" hint="actions & crawl — run in your browser" count={`${loggedIn} / ${list.length} logged in`} />
      <ErrorBox>{error}</ErrorBox>
      {onLibrary && (list.length === 0 && !error ? (
        <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 10 }}>
          <span style={{ ...mono, fontSize: 11, fontWeight: 700, color: 'var(--text)' }}>{t('library.automationsEmpty.title')}</span>
          <span style={body}>{t('library.automationsEmpty.body')}</span>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <LogInToMonoesButton compact />
            <button className="btn btn-secondary btn-sm" onClick={onLibrary} style={{ gap: 5 }}><Library size={11} /> {t('library.browse')}</button>
          </div>
        </div>
      ) : (
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, alignItems: 'center', marginBottom: 10, flexWrap: 'wrap' }}>
          <LogInToMonoesButton compact />
          <button className="btn btn-secondary btn-sm" onClick={onLibrary} style={{ gap: 5 }}><Library size={11} /> {t('library.browse')}</button>
        </div>
      ))}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: 10, marginTop: error ? 10 : 0 }}>
        {list.map(a => <AutomationCard key={a.id} automation={a} onOpen={() => onOpen(a)} />)}
        <CreateAutomationCard onRecord={onRecord} onImport={onImport} />
        {removed.map(a => <AutomationCard key={a.id} automation={a} onOpen={() => onOpen(a)} />)}
      </div>
    </section>
  )
}

const STEPS = [
  'Open the MonoAgent Bridge side panel in your browser (the extension icon → Open side panel) on the site you want to automate.',
  'Choose Record, describe the goal in a sentence ("add a contact to Acme CRM"), and start.',
  'Do the task once, normally. Passwords and card numbers are never recorded.',
  'Optionally mark values that should become inputs, and data you want extracted.',
  'Stop. The recording appears in an automation\'s Recordings tab here (or in the side panel) to analyze, verify and save as an action.',
]

export function RecordHelpDialog({ onClose }) {
  const dialog = useDialog(onClose)
  return (
    <div className="modal-overlay" onClick={e => e.target === e.currentTarget && onClose()}>
      <div {...dialog} className="modal" role="dialog" aria-modal="true" aria-labelledby="record-help-title" style={{ width: 500 }}>
        <div className="modal-title">
          <span id="record-help-title">Record a new automation</span>
          <button className="btn btn-ghost btn-icon" onClick={onClose} aria-label="Close"><X size={15} /></button>
        </div>
        <div style={{ ...body, marginBottom: 14 }}>
          Recording happens in the browser extension, so it captures what you really do on the site with your real login.
        </div>
        <ol style={{ listStyle: 'none', padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {STEPS.map((text, i) => (
            <li key={i} style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
              <span style={{ ...mono, fontSize: 10, fontWeight: 700, color: 'var(--cyan)', minWidth: 16, lineHeight: '18px', textAlign: 'right' }}>{i + 1}</span>
              <span style={body}>{text}</span>
            </li>
          ))}
        </ol>
        <div style={{ ...muted, marginTop: 14 }}>
          The bridge extension must be connected (Settings → System health shows its state).
        </div>
        <div style={{ ...muted, marginTop: 8 }}>
          Prefer writing one by hand? Start from a template: <code style={{ color: 'var(--cyan)' }}>monoagentcli automation new &lt;id&gt; --template &lt;name&gt;</code>
        </div>
        <div className="modal-actions">
          <button className="btn btn-primary btn-sm" onClick={onClose}>Got it</button>
        </div>
      </div>
    </div>
  )
}
