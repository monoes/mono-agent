import { useEffect, useId, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, X } from 'lucide-react'
import { Said, errText, hint, mono } from './ui.jsx'

// The name of a key, being renamed in its cell of the keys table: a field with Save and Cancel, Enter to save and
// Escape to cancel, and what the store says of the name (its rule, a clash with another active key) beneath it, in the
// language of the page. The CLI judges the name; this only asks, and cannot show a key (none is needed to rename one).
// While the CLI works the field is read-only and not disabled, so that the keyboard stays in it.

/**
 * @param {string} name The name the key has now (it names the controls).
 * @param {string} value What is typed.
 * @param {boolean} busy A call is running.
 * @param {{text: string, verbatim: boolean}|null} err What the store said of the name.
 * @param {(value: string) => void} onChange
 * @param {() => void} onSave
 * @param {() => void} onCancel
 * @param {React.Ref<HTMLInputElement>} inputRef
 */
export default function KeyNameEditor({ name, value, busy, err, onChange, onSave, onCancel, inputRef }) {
  const { t } = useTranslation()
  const hintId = `${useId()}-rule`
  const own = useRef(null)
  const ref = inputRef || own
  useEffect(() => { ref.current?.focus(); ref.current?.select() }, [ref])

  const keyDown = (e) => {
    if (e.key === 'Enter' && !e.nativeEvent?.isComposing) { e.preventDefault(); onSave() }
    // Escape cancels the edit, and only that: the chat panel stops a running turn on an Escape that reaches the window.
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); if (!busy) onCancel() }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, whiteSpace: 'normal' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <input
          ref={ref} className="form-input" value={value} maxLength={64} readOnly={busy} autoComplete="off" spellCheck={false}
          aria-label={t('settings.api.keys.renameInput', { name })} aria-describedby={hintId}
          style={{ padding: '4px 8px', fontFamily: mono, fontSize: 11.5, minWidth: 150 }}
          onChange={e => onChange(e.target.value)} onKeyDown={keyDown}
        />
        <button
          type="button" className="btn btn-primary btn-sm" disabled={busy || !value.trim()}
          aria-label={t('settings.api.keys.renameSaveLabel', { name })} onClick={onSave}
        >
          <Check size={12} />
        </button>
        <button type="button" className="btn btn-secondary btn-sm" disabled={busy} aria-label={t('settings.api.keys.renameCancelLabel', { name })} onClick={onCancel}>
          <X size={12} />
        </button>
      </div>
      <div id={hintId} style={{ ...hint, maxWidth: 280 }}>{t('settings.api.create.nameHint')}</div>
      {err && <div role="alert" style={{ ...errText, maxWidth: 280 }}><Said said={err} /></div>}
    </div>
  )
}
