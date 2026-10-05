import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { CLASSES } from './apiModel.js'
import { describeConfigError } from './configError.js'
import { canSave, hasSaved, overriddenInfo, rowProblems, rowState, runningInfo, stateInfo, textOf } from './configModel.js'
import { Badge, Said, errText, hint, mono, okText } from './ui.jsx'

// One setting of the server (or, for the TLS files, the two that are saved together): what it is for, a control that
// fits it holding what is saved (or what is being typed), where it stands against the running daemon and what the
// daemon runs, what is wrong with the saved value, if anything, and the two things that can be done with it: save
// what was typed, and go back to the default. The words about a state and a running value are the CLI's facts worded
// for the page; nothing here decides what a value means.

const field = { padding: '6px 10px', fontFamily: mono, fontSize: 12, minWidth: 0 }
const title = { fontFamily: mono, fontSize: 12, fontWeight: 600, color: 'var(--text)' }

// What a call did, in the words of the page.
const NOTES = {
  saved: 'settings.api.config.note.saved',
  removed: 'settings.api.config.note.removed',
  unchanged: 'settings.api.config.note.unchanged',
}

function sourceText(t, { kind, name }) {
  switch (kind) {
    case 'flag': return t('settings.api.config.source.flag', { name })
    case 'env': return t('settings.api.config.source.env', { name })
    case 'saved': return t('settings.api.config.source.saved')
    case 'default': return t('settings.api.config.source.default')
    default: return kind // a source this page does not know, as the CLI named it
  }
}

// What the daemon runs, for a setting it reports: an empty value is a value (it runs the setting with none).
function RunningLine({ setting }) {
  const { t } = useTranslation()
  const running = runningInfo(setting)
  if (!running) return null
  return (
    <div data-testid={`api-config-running-${setting.key}`} style={{ ...hint, fontFamily: mono, wordBreak: 'break-all' }}>
      {t('settings.api.config.runningLine', { value: running.value === '' ? t('settings.api.config.valueNotSet') : running.value, source: sourceText(t, running) })}
    </div>
  )
}

// Why a saved value has no effect, for a setting the daemon was given a flag or a variable for.
function OverriddenNote({ setting }) {
  const { t } = useTranslation()
  const o = overriddenInfo(setting)
  if (!o) return null
  const text = o.kind === 'flag' ? t('settings.api.config.overriddenFlag', { name: o.name })
    : o.kind === 'env' ? t('settings.api.config.overriddenEnv', { name: o.name })
      : t('settings.api.config.stateHint.overridden')
  return <div style={{ ...hint, color: 'var(--orange)' }}>{text}</div>
}

// The control: a select for a class, a number for a count, a text for the rest. While nothing is saved it says what
// the default is, in the field, and a class that is not saved is a choice of its own that says it. Enter in a field
// saves, when there is something to save (not while a composition is being confirmed).
function Control({ row, keyName, setting, settings, drafts, onChange, onEnter, disabled, id, describedBy, focusMark }) {
  const { t } = useTranslation()
  const value = textOf(keyName, drafts, settings)
  const saved = setting?.saved ?? ''
  const common = { id, disabled, 'aria-describedby': describedBy, value, className: 'form-input', 'data-focus-row': focusMark ? row.id : undefined }
  const placeholder = row.empty ? t(row.empty) : setting?.default ? t('settings.api.config.placeholderDefault', { value: setting.default }) : ''

  if (row.kind === 'class') {
    // An empty choice only while nothing is saved (going back to the default is "Use the default"), and a saved class
    // the select does not know (an edit by hand) stays in it as stored.
    const options = [...(saved === '' ? [['', placeholder]] : []), ...CLASSES.map(c => [c, c]), ...(saved !== '' && !CLASSES.includes(saved) ? [[saved, saved]] : [])]
    return (
      <select {...common} className="form-select" style={field} onChange={e => onChange(keyName, e.target.value)}>
        {options.map(([v, text]) => <option key={v} value={v}>{text}</option>)}
      </select>
    )
  }
  return (
    <input
      {...common} type={row.kind === 'number' ? 'number' : 'text'} inputMode={row.kind === 'number' ? 'numeric' : undefined}
      placeholder={placeholder} autoComplete="off" spellCheck={false} style={field} onChange={e => onChange(keyName, e.target.value)}
      onKeyDown={e => { if (e.key === 'Enter' && !e.nativeEvent?.isComposing) { e.preventDefault(); onEnter() } }}
    />
  )
}

/**
 * @param {object} row One of ROWS.
 * @param {object} doc The document of `api config show` (or of a change).
 * @param {object} settings Its settings by key.
 * @param {Object<string,string>} drafts What was typed, by key.
 * @param {null|{id: string, kind: 'set'|'unset'}} busy The change a call is running for, if any: nothing can be edited
 *   or started while it runs.
 * @param {{text: string, verbatim: boolean}|null} err What the last call for this row said when it failed.
 * @param {null|{kind: 'saved'|'removed'|'unchanged', extra: Array<{key: string, reason: string}>}} note What the last call
 *   for this row did, and the ways it made the server reach further that its dialog did not list.
 * @param {(key: string, text: string) => void} onChange
 * @param {() => void} onSave
 * @param {() => void} onUseDefault
 */
export default function ApiConfigRow({ row, doc, settings, drafts, busy, err, note, onChange, onSave, onUseDefault }) {
  const { t } = useTranslation()
  const ids = useId()
  const labelId = `${ids}-label`, hintId = `${ids}-hint`
  const state = stateInfo(rowState(row, settings))
  const problems = rowProblems(row, doc)
  const parts = row.kind === 'pair' ? row.parts : [{ key: row.keys[0], label: row.label }]
  const name = t(row.label)
  const saveable = canSave(row, drafts, settings)
  const removable = hasSaved(row, settings)
  const describedBy = (key) => [hintId, ...problems.filter(p => p.key === key).map((_, i) => `${ids}-problem-${key}-${i}`)].join(' ')
  const enter = () => { if (!busy && saveable) onSave() }
  const saving = busy?.id === row.id && busy.kind === 'set'

  return (
    <li data-testid={`api-config-row-${row.id}`} style={{ border: '1px solid var(--border-dim)', borderRadius: 'var(--radius)', padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div role={row.kind === 'pair' ? 'group' : undefined} aria-labelledby={row.kind === 'pair' ? labelId : undefined} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          {row.kind === 'pair'
            ? <span id={labelId} style={title}>{name}</span>
            : <label id={labelId} htmlFor={`${ids}-${row.keys[0]}`} style={title}>{name}</label>}
          <span style={{ flex: 1 }} />
          <Badge data-testid={`api-config-state-${row.id}`} tone={state.tone} title={t(state.hint)}>{t(state.text)}</Badge>
        </div>
        <div id={hintId} style={hint}>{t(row.hint)}</div>

        {parts.map((part, i) => {
          const setting = settings[part.key]
          return (
            <div key={part.key} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {row.kind === 'pair' && <label htmlFor={`${ids}-${part.key}`} style={{ ...hint, color: 'var(--text-secondary)' }}>{t(part.label)}</label>}
              <Control
                row={row} keyName={part.key} setting={setting} settings={settings} drafts={drafts} onChange={onChange} onEnter={enter}
                disabled={!!busy} id={`${ids}-${part.key}`} describedBy={describedBy(part.key)} focusMark={i === 0}
              />
              {setting && <RunningLine setting={setting} />}
              {setting && <OverriddenNote setting={setting} />}
              {problems.filter(p => p.key === part.key).map((p, j) => (
                <div key={j} id={`${ids}-problem-${part.key}-${j}`} style={errText}>
                  <Said said={describeConfigError(`invalid_input: ${p.message}`, t)} />
                </div>
              ))}
            </div>
          )
        })}
        {problems.length > 0 && <div style={hint}>{t('settings.api.config.problemHint')}</div>}

        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <button
            type="button" className="btn btn-primary btn-sm" disabled={!!busy || !saveable}
            aria-label={t('settings.api.config.saveLabel', { setting: name })} onClick={onSave}
          >
            {saving ? t('settings.api.config.saving') : t('settings.api.config.save')}
          </button>
          <button
            type="button" className="btn btn-secondary btn-sm" disabled={!!busy || !removable}
            aria-label={t('settings.api.config.useDefaultLabel', { setting: name })} onClick={onUseDefault}
          >
            {t('settings.api.config.useDefault')}
          </button>
          <span role="status" aria-live="polite" style={okText}>{note && t(NOTES[note.kind])}</span>
        </div>
        {note?.extra?.length > 0 && (
          <div style={{ ...hint, color: 'var(--yellow)' }}>
            {t('settings.api.config.note.also')}
            <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
              {note.extra.map((x, i) => <li key={`${x.key}-${i}`}><Said said={{ text: x.reason, verbatim: true }} /></li>)}
            </ul>
          </div>
        )}
        {err && <div role="alert" style={errText}><Said said={err} /></div>}
      </div>
    </li>
  )
}
