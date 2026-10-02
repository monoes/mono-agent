import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2 } from 'lucide-react'
import { APIKeySetContext, APIKeyRevoke } from '../../../wailsjs/go/main/App'
import { confirm } from '../../ConfirmDialog.jsx'
import { Switch } from '../JevSection.jsx'
import ApiKeyDialog from './ApiKeyDialog.jsx'
import { formatDate, relativeTime } from './apiModel.js'
import { block, label, hint, errText, mono, errMsg } from './ui.jsx'

// The keys of the active profile: a table with a context switch and revoke, and
// the create dialog. The CLI does the work (APIKeySetContext, APIKeyRevoke,
// APIKeyCreate); this block only asks, and tells its parent to reload the list.

const th = { fontSize: 9.5, padding: '6px 10px' }
const td = { fontSize: 11.5, padding: '8px 10px', whiteSpace: 'nowrap' }

/**
 * @param {Array|null} keys The profile's keys, or null while they load.
 * @param {string} err Why they could not be read.
 * @param {string} contextClass The strongest class a key with context may use on the listener.
 * @param {() => void} onChanged Called after a key was created, changed or revoked.
 * @param {() => void} onRetry
 */
export default function ApiKeysBlock({ keys, err, contextClass, onChanged, onRetry }) {
  const { t, i18n } = useTranslation()
  const lng = i18n.resolvedLanguage || i18n.language
  const [busy, setBusy] = useState('') // the id of the key a call is running for
  const [actionErr, setActionErr] = useState('')
  const [creating, setCreating] = useState(false)

  // One call at a time: what the CLI says when it fails is shown, and nothing is reloaded.
  const run = async (id, fn) => {
    setBusy(id); setActionErr('')
    try { await fn(); onChanged?.() } catch (e) { setActionErr(errMsg(e)) } finally { setBusy('') }
  }
  const setContext = (k, on) => run(k.id, () => APIKeySetContext(k.id, on))
  const revoke = async (k) => {
    const ok = await confirm(t('settings.api.keys.revokeBody', { name: k.name }), {
      title: t('settings.api.keys.revokeTitle'), confirmLabel: t('settings.api.keys.revokeConfirm'), danger: true,
    })
    if (ok) await run(k.id, () => APIKeyRevoke(k.id))
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
          <div style={{ overflowX: 'auto' }}>
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
                    <td style={{ ...td, fontFamily: mono, color: 'var(--text)', maxWidth: 240, overflow: 'hidden', textOverflow: 'ellipsis' }} title={k.name}>
                      {k.name}
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
          <div style={hint}>{t('settings.api.keys.contextHint', { class: contextClass || 'chat-only' })}</div>
        </>
      )}

      <ApiKeyDialog open={creating} onClose={() => setCreating(false)} onCreated={() => onChanged?.()} />
    </div>
  )
}
