import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { APIKeyRename, APIKeySetContext, APIKeyRevoke } from '../../../wailsjs/go/main/App'
import { confirm } from '../../ConfirmDialog.jsx'
import { Switch } from '../JevSection.jsx'
import ApiKeyDialog from './ApiKeyDialog.jsx'
import KeyNameEditor from './KeyNameEditor.jsx'
import { apiError, classify, describeApiError } from './apiError.js'
import { formatDate, relativeTime } from './apiModel.js'
import { block, label, hint, errText, mono } from './ui.jsx'

// The keys of the active profile: a table with a context switch, rename and revoke, and
// the create dialog. The CLI does the work (APIKeySetContext, APIKeyRename, APIKeyRevoke,
// APIKeyCreate); this block only asks, and tells its parent to reload the list.

const th = { fontSize: 9.5, padding: '6px 10px' }
const td = { fontSize: 11.5, padding: '8px 10px', whiteSpace: 'nowrap' }

/**
 * @param {Array|null} keys The profile's keys, or null while they load.
 * @param {string} err Why they could not be read.
 * @param {string[]} contextClasses The strongest class a key with context may use, on each listener that serves /v1
 *   (without repeats). One class is named; several are not, since each listener applies its own.
 * @param {() => (void|Promise<void>)} onChanged Called after a key was created, changed or revoked, or a call failed
 *   (the list may have changed anyway); the controls stay busy until what it returns settles.
 * @param {() => void} onRetry
 */
export default function ApiKeysBlock({ keys, err, contextClasses = [], onChanged, onRetry }) {
  const { t, i18n } = useTranslation()
  const lng = i18n.resolvedLanguage || i18n.language
  const [busy, setBusy] = useState('') // the id of the key a call is running for
  const [actionErr, setActionErr] = useState('')
  const [creating, setCreating] = useState(false)
  const [renaming, setRenaming] = useState('') // the id of the key whose name is being edited
  const [draft, setDraft] = useState('')
  const [renameErr, setRenameErr] = useState(null) // what the store said of the new name: {text, verbatim}
  const inputRef = useRef(null)
  const renameBtns = useRef({}) // key id → its Rename button
  const renameRunning = useRef(false) // a rename call is out: a second one cannot start, not even in the same tick
  const focusAfter = useRef(null) // where the keyboard goes when a rename is over: 'input' or {rename: id}

  // A control that was disabled while a call ran has lost the focus: it goes back to where the person was.
  useEffect(() => {
    if (busy || !focusAfter.current) return
    const f = focusAfter.current
    focusAfter.current = null
    if (f === 'input') inputRef.current?.focus()
    else renameBtns.current[f.rename]?.focus()
  })

  // One call at a time, and the list is read again, whatever came of it (a failed call may have been done anyway, or
  // the key may be gone), before anything else can be asked of it: the list is stale until then, and a switch on it
  // would start from a value that is no longer true. What the CLI says when it fails is shown.
  const run = async (id, fn) => {
    setBusy(id); setActionErr('')
    try { await fn() } catch (e) { setActionErr(apiError(e, t)) }
    try { await onChanged?.() } finally { setBusy('') }
  }
  // Turning context on sends excerpts of this profile's documents to the model's provider: it asks, in the words of
  // the create dialog. Turning it off only takes them away, and asks nothing.
  const setContext = async (k, on) => {
    if (on) {
      const ok = await confirm(t('settings.api.create.contextHint'), {
        title: t('settings.api.keys.contextOnTitle', { name: k.name }), confirmLabel: t('settings.api.keys.contextOnConfirm'), danger: false,
      })
      if (!ok) return
    }
    await run(k.id, () => APIKeySetContext(k.id, on))
  }
  const revoke = async (k) => {
    const ok = await confirm(t('settings.api.keys.revokeBody', { name: k.name }), {
      title: t('settings.api.keys.revokeTitle'), confirmLabel: t('settings.api.keys.revokeConfirm'), danger: true,
    })
    if (ok) await run(k.id, () => APIKeyRevoke(k.id))
  }

  // Renaming changes the name and nothing else: not the context switch, and the key is neither needed nor shown. One key
  // is edited at a time. The store judges the name: its rule and a clash with another active key are said next to the
  // field and the edit stays open, to correct; a key that is gone is said at the top, like the other key failures.
  const startRename = (k) => { if (busy) return; setRenaming(k.id); setDraft(k.name); setRenameErr(null) }
  const cancelRename = (k) => { setRenaming(''); setRenameErr(null); focusAfter.current = { rename: k.id } }
  const saveRename = async (k) => {
    const name = draft.trim()
    if (busy || renameRunning.current || !name) return
    if (name === k.name) { cancelRename(k); return } // nothing to change
    renameRunning.current = true
    setBusy(k.id); setRenameErr(null); setActionErr('')
    let done = false
    try {
      await APIKeyRename(k.id, name)
      done = true
    } catch (e) {
      if (classify(e).cls === 'not_found') { setActionErr(apiError(e, t)); done = true } else setRenameErr(describeApiError(e, t))
    }
    try { await onChanged?.() } finally { setBusy(''); renameRunning.current = false }
    if (done) { setRenaming(''); focusAfter.current = { rename: k.id } } else focusAfter.current = 'input'
  }

  return (
    <div style={block}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <span style={label}>{t('settings.api.keys.title')}</span>
        <span style={{ ...hint, flex: 1, minWidth: 220 }}>{t('settings.api.keys.hint')}</span>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => setCreating(true)}>
          <Plus size={12} /> {t('settings.api.keys.create')}
        </button>
      </div>

      {actionErr && <div role="alert" style={errText}>{actionErr}</div>}

      {err ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <div style={{ ...errText, flex: 1 }}>{t('settings.api.keys.loadError', { error: err })}</div>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>{t('settings.api.retry')}</button>
        </div>
      ) : keys === null || keys === undefined ? (
        <div style={hint}>{t('settings.api.loading')}</div>
      ) : keys.length === 0 ? (
        <div style={hint}>{t('settings.api.keys.empty')}</div>
      ) : (
        <>
          <div style={{ overflowX: 'auto', position: 'relative' }}>
            <table className="data-table" aria-label={t('settings.api.keys.title')}>
              <thead>
                <tr>
                  <th style={th}>{t('settings.api.keys.colName')}</th>
                  <th style={th}>{t('settings.api.keys.colKey')}</th>
                  <th style={th}>{t('settings.api.keys.colContext')}</th>
                  <th style={th}>{t('settings.api.keys.colCreated')}</th>
                  <th style={th}>{t('settings.api.keys.colLastUsed')}</th>
                  <th style={th}><span style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>{t('settings.api.keys.revoke')}</span></th>
                </tr>
              </thead>
              <tbody>
                {keys.map(k => (
                  <tr key={k.id}>
                    <td style={{ ...td, fontFamily: mono, color: 'var(--text)', maxWidth: renaming === k.id ? undefined : 270 }}>
                      {renaming === k.id ? (
                        <KeyNameEditor
                          name={k.name} value={draft} busy={!!busy} err={renameErr} inputRef={inputRef}
                          onChange={setDraft} onSave={() => saveRename(k)} onCancel={() => cancelRename(k)}
                        />
                      ) : (
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, maxWidth: '100%' }}>
                          <span title={k.name} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{k.name}</span>
                          <button
                            ref={el => { renameBtns.current[k.id] = el }} type="button" className="btn btn-ghost btn-sm" style={{ padding: '2px 5px', flexShrink: 0 }}
                            disabled={!!busy} aria-label={t('settings.api.keys.renameLabel', { name: k.name })} onClick={() => startRename(k)}
                          >
                            <Pencil size={11} />
                          </button>
                        </span>
                      )}
                    </td>
                    <td style={{ ...td, fontFamily: mono, color: 'var(--text-muted)' }}>{k.prefix}…</td>
                    <td style={td}>
                      <Switch
                        on={!!k.context} disabled={!!busy} label={t('settings.api.keys.contextLabel', { name: k.name })}
                        onChange={on => setContext(k, on)}
                      />
                    </td>
                    <td style={td} title={k.created_at}>{formatDate(k.created_at, lng)}</td>
                    <td style={td} title={k.last_used_at || undefined}>
                      {k.last_used_at ? relativeTime(k.last_used_at, lng) : t('settings.api.keys.never')}
                    </td>
                    <td style={{ ...td, textAlign: 'right' }}>
                      <button
                        type="button" className="btn btn-danger btn-sm" disabled={!!busy}
                        aria-label={t('settings.api.keys.revokeLabel', { name: k.name })} onClick={() => revoke(k)}
                      >
                        <Trash2 size={12} /> {t('settings.api.keys.revoke')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div style={hint}>
            {contextClasses.length > 1 ? t('settings.api.keys.contextHintMany') : t('settings.api.keys.contextHint', { class: contextClasses[0] || 'chat-only' })}
          </div>
        </>
      )}

      <ApiKeyDialog open={creating} onClose={() => setCreating(false)} onCreated={() => onChanged?.()} />
    </div>
  )
}
