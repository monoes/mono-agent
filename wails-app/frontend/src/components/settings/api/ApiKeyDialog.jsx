import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check, ShieldAlert } from 'lucide-react'
import { APIKeyCreate } from '../../../wailsjs/go/main/App'
import { copyText } from '../../../pages/connections/ui.jsx'
import { errMsg, mono, hint, errText, okText } from './ui.jsx'

// The create-key dialog: a name and the context switch, then the show-once
// panel with the new key. The key lives in this component's state and nowhere
// else: not in storage, the console, a URL, an error text or what onCreated
// hands the parent, and it is cleared when the dialog closes, however it closes.

const FOCUSABLE = 'button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'

/**
 * @param {boolean} open
 * @param {() => void} onClose
 * @param {(key: object) => void} onCreated Called with the new key's metadata,
 *   never with the key itself.
 */
export default function ApiKeyDialog({ open, onClose, onCreated }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [context, setContext] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [secret, setSecret] = useState('') // the new key
  const [copied, setCopied] = useState('') // '' | 'ok' | 'failed'
  const nameRef = useRef(null)
  const copyRef = useRef(null)
  const dialogRef = useRef(null)
  const ids = useId()
  const titleId = `${ids}-title`, nameId = `${ids}-name`, nameHintId = `${ids}-name-hint`, keyId = `${ids}-key`

  const reset = () => { setName(''); setContext(false); setBusy(false); setErr(''); setSecret(''); setCopied('') }
  // Closing waits for the CLI: a key created while the dialog is gone would be shown to nobody.
  const close = () => { if (busy) return; reset(); onClose?.() }

  // A dialog that opens or closes holds nothing of the last one: not a name, not a key.
  useEffect(() => { reset() }, [open])
  useEffect(() => { if (open && !secret) nameRef.current?.focus() }, [open, secret])
  useEffect(() => { if (secret) copyRef.current?.focus() }, [secret])
  useEffect(() => {
    if (copied !== 'ok') return undefined
    const id = setTimeout(() => setCopied(''), 2000)
    return () => clearTimeout(id)
  }, [copied])
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => { if (e.key === 'Escape') close() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  if (!open) return null

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
      setErr(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  const copy = async () => setCopied((await copyText(secret)) ? 'ok' : 'failed')

  // Keep Tab inside the dialog: the page behind it is not reachable.
  const trap = (e) => {
    if (e.key !== 'Tab' || !dialogRef.current) return
    const items = Array.from(dialogRef.current.querySelectorAll(FOCUSABLE))
    if (!items.length) return
    const first = items[0], last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
  }

  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget && !secret) close() }}>
      <div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={titleId} className="modal" style={{ width: 460 }} onKeyDown={trap}>
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
              <button type="button" className="btn btn-primary" onClick={close}>{t('settings.api.create.done')}</button>
            </div>
          </div>
        ) : (
          <form onSubmit={e => { e.preventDefault(); create() }}>
            <div className="form-group">
              <label className="form-label" htmlFor={nameId}>{t('settings.api.create.name')}</label>
              <input
                id={nameId} ref={nameRef} className="form-input" value={name} maxLength={64} disabled={busy}
                placeholder={t('settings.api.create.namePlaceholder')} autoComplete="off" spellCheck={false}
                aria-describedby={nameHintId} onChange={e => setName(e.target.value)}
              />
              <div id={nameHintId} style={{ ...hint, marginTop: 4 }}>{t('settings.api.create.nameHint')}</div>
            </div>
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: busy ? 'default' : 'pointer' }}>
              <input
                type="checkbox" checked={context} disabled={busy} onChange={e => setContext(e.target.checked)}
                style={{ marginTop: 2, accentColor: '#00b4d8', flexShrink: 0 }}
              />
              <span style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                <span style={{ fontFamily: mono, fontSize: 12, color: 'var(--text)' }}>{t('settings.api.create.context')}</span>
                <span style={hint}>{t('settings.api.create.contextHint')}</span>
              </span>
            </label>
            {err && <div role="alert" style={{ ...errText, marginTop: 12 }}>{err}</div>}
            <div className="modal-actions">
              <button type="button" className="btn btn-secondary" disabled={busy} onClick={close}>{t('settings.api.create.cancel')}</button>
              <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
                {busy ? t('settings.api.create.creating') : t('settings.api.create.submit')}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
