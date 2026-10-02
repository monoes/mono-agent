import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check, ShieldAlert } from 'lucide-react'
import { APIKeyCreate } from '../../../wailsjs/go/main/App'
import { copyText, useDialog } from '../../../pages/connections/ui.jsx'
import { apiError } from './apiError.js'
import { mono, hint, errText, okText } from './ui.jsx'

// The create-key dialog: a name and the context switch, then the show-once
// panel with the new key. The key lives in this component's state and nowhere
// else: not in storage, the console, a URL, an error text or what onCreated
// hands the parent, and it is cleared when the dialog closes, however it closes.
//
// Escape and a click outside close the form, but not while the CLI works (the key
// it creates would be shown to nobody) and never over the panel that holds the
// key, whose only way out is Done: the key is not shown again. Nothing the focus
// can be on is disabled while the CLI works (a disabled control drops the focus
// to the page behind, and Escape and Tab with it): it is read-only or
// aria-disabled instead.

const dim = { opacity: 0.4, cursor: 'not-allowed' } // what .btn:disabled looks like, for a button that holds the focus

/**
 * @param {boolean} open
 * @param {() => void} onClose
 * @param {(key: object) => void} onCreated Called with the new key's metadata,
 *   never with the key itself.
 */
export default function ApiKeyDialog({ open, onClose, onCreated }) {
  // Closed, it is not there: each opening has its own state, focus and opener.
  return open ? <Dialog onClose={onClose} onCreated={onCreated} /> : null
}

function Dialog({ onClose, onCreated }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [context, setContext] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [secret, setSecret] = useState('') // the new key
  const [copied, setCopied] = useState('') // '' | 'ok' | 'failed'
  const nameRef = useRef(null)
  const copyRef = useRef(null)
  const ids = useId()
  const titleId = `${ids}-title`, nameId = `${ids}-name`, nameHintId = `${ids}-name-hint`, keyId = `${ids}-key`

  // Done closes whatever it holds; the key is cleared even if the parent keeps the dialog open.
  const finish = () => { setName(''); setContext(false); setBusy(false); setErr(''); setSecret(''); setCopied(''); onClose?.() }
  const askClose = () => { if (!busy && !secret) finish() }
  const dialog = useDialog(askClose) // focus in on open and back to the opener on close, Escape, and Tab kept inside

  useEffect(() => { if (secret) copyRef.current?.focus() }, [secret])
  useEffect(() => {
    if (copied !== 'ok') return undefined
    const id = setTimeout(() => setCopied(''), 2000)
    return () => clearTimeout(id)
  }, [copied])

  const create = async () => {
    const n = name.trim()
    if (!n || busy) return
    setBusy(true); setErr('')
    try {
      const { key, ...meta } = (await APIKeyCreate(n, context)) || {}
      onCreated?.(meta)
      if (key) setSecret(key)
      else setErr(t('settings.api.create.noKey'))
    } catch (e) {
      setErr(apiError(e, t))
      nameRef.current?.focus() // to correct the name; it is read-only until this settles, but focusable
    } finally {
      setBusy(false)
    }
  }

  const copy = async () => setCopied((await copyText(secret)) ? 'ok' : 'failed')

  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) askClose() }}>
      <div {...dialog} role="dialog" aria-modal="true" aria-labelledby={titleId} className="modal" style={{ width: 460 }}>
        <div id={titleId} className="modal-title">
          {secret ? t('settings.api.create.doneTitle') : t('settings.api.create.title')}
        </div>

        {secret ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{
              display: 'flex', alignItems: 'flex-start', gap: 8, fontFamily: mono, fontSize: 11, lineHeight: 1.5, color: 'var(--yellow)',
              background: 'rgba(234,179,8,.05)', border: '1px solid rgba(234,179,8,.22)', borderRadius: 'var(--radius)', padding: '10px 12px',
            }}>
              <ShieldAlert size={14} style={{ flexShrink: 0, marginTop: 1 }} />
              <span>{t('settings.api.create.warning')}</span>
            </div>
            <div>
              <label className="form-label" htmlFor={keyId}>{t('settings.api.create.keyLabel')}</label>
              <input
                id={keyId} data-testid="api-key-secret" className="form-input" readOnly value={secret}
                spellCheck={false} autoComplete="off" onFocus={e => e.target.select()}
                style={{ fontFamily: mono, fontSize: 12 }}
              />
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 8, minHeight: 30 }}>
                <button ref={copyRef} type="button" className="btn btn-secondary btn-sm" onClick={copy}>
                  <Copy size={12} /> {t('settings.api.create.copy')}
                </button>
                <div role="status" aria-live="polite">
                  {copied === 'ok' && <span style={okText}><Check size={11} /> {t('settings.api.create.copied')}</span>}
                  {copied === 'failed' && <span style={errText}>{t('settings.api.create.copyFailed')}</span>}
                </div>
              </div>
            </div>
            <div style={hint}>{t('settings.api.create.usage')}</div>
            <div className="modal-actions">
              <button type="button" className="btn btn-primary" onClick={finish}>{t('settings.api.create.done')}</button>
            </div>
          </div>
        ) : (
          <form onSubmit={e => { e.preventDefault(); create() }}>
            <div className="form-group">
              <label className="form-label" htmlFor={nameId}>{t('settings.api.create.name')}</label>
              <input
                id={nameId} ref={nameRef} className="form-input" value={name} maxLength={64} readOnly={busy}
                placeholder={t('settings.api.create.namePlaceholder')} autoComplete="off" spellCheck={false}
                aria-describedby={nameHintId} onChange={e => setName(e.target.value)}
              />
              <div id={nameHintId} style={{ ...hint, marginTop: 4 }}>{t('settings.api.create.nameHint')}</div>
            </div>
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: busy ? 'default' : 'pointer' }}>
              <input
                type="checkbox" checked={context} aria-disabled={busy || undefined} onChange={e => { if (!busy) setContext(e.target.checked) }}
                style={{ marginTop: 2, accentColor: '#00b4d8', flexShrink: 0 }}
              />
              <span style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                <span style={{ fontFamily: mono, fontSize: 12, color: 'var(--text)' }}>{t('settings.api.create.context')}</span>
                <span style={hint}>{t('settings.api.create.contextHint')}</span>
              </span>
            </label>
            {err && <div role="alert" style={{ ...errText, marginTop: 12 }}>{err}</div>}
            <div className="modal-actions">
              <button type="button" className="btn btn-secondary" aria-disabled={busy || undefined} style={busy ? dim : undefined} onClick={askClose}>
                {t('settings.api.create.cancel')}
              </button>
              <button type="submit" className="btn btn-primary" disabled={!name.trim()} aria-disabled={busy || undefined} style={busy ? dim : undefined}>
                {busy ? t('settings.api.create.creating') : t('settings.api.create.submit')}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
