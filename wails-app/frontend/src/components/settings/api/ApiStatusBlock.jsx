import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check, RefreshCw } from 'lucide-react'
import { copyText } from '../../../pages/connections/ui.jsx'
import { baseURL, listenerState, pickListener } from './apiModel.js'
import { Badge, ClassBadge, label, hint, errText, okText, mono } from './ui.jsx'

// Where /v1 listens, whether it answers, and what it serves: the base URL with a
// copy button, the running state and the exposure and confinement badges. What
// it says of a listener is what `api status` says (its listenerNote), worded for
// the page; the policy shown is the running daemon's when it reported one.

export const STATE = {
  serving: { tone: 'ok', text: 'settings.api.status.stateServing' },
  down: { tone: 'muted', text: 'settings.api.status.stateDown', note: 'settings.api.status.noteDown' },
  stale: { tone: 'warn', text: 'settings.api.status.stateStale', note: 'settings.api.status.noteStale' },
  'no-v1-daemon': { tone: 'warn', text: 'settings.api.status.stateNoV1', note: 'settings.api.status.noteNoV1Daemon' },
  'no-v1-offloopback': { tone: 'warn', text: 'settings.api.status.stateNoV1', note: 'settings.api.status.noteNoV1Offloopback' },
  none: { tone: 'muted', text: 'settings.api.status.stateNone' },
}

const urlBox = {
  fontFamily: mono, fontSize: 12, color: 'var(--text)', background: 'var(--elevated)', border: '1px solid var(--border)',
  borderRadius: 'var(--radius)', padding: '4px 10px', userSelect: 'all', wordBreak: 'break-all',
}

/**
 * @param {object|null} status `api status --json`, or null while it loads.
 * @param {string} err Why it could not be read.
 * @param {boolean} refreshing
 * @param {() => void} onRefresh
 * @param {() => void} onRetry
 */
export default function ApiStatusBlock({ status, err, refreshing, onRefresh, onRetry }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState('') // '' | 'ok' | 'failed'
  useEffect(() => {
    if (copied !== 'ok') return undefined
    const id = setTimeout(() => setCopied(''), 2000)
    return () => clearTimeout(id)
  }, [copied])

  if (err) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <div style={{ ...errText, flex: 1 }}>{t('settings.api.loadError', { error: err })}</div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>{t('settings.api.retry')}</button>
      </div>
    )
  }
  if (!status) return <div style={hint}>{t('settings.api.loading')}</div>

  const l = pickListener(status)
  const state = listenerState(l)
  const url = baseURL(l)
  const serves = !!l?.v1 // it is meant to serve /v1: its policy means something
  const s = STATE[state]
  const caps = [
    l?.context_confinement && t('settings.api.status.capsContext', { class: l.context_confinement }),
    l?.auto_confinement && t('settings.api.status.capsAuto', { class: l.auto_confinement }),
  ].filter(Boolean).join(' · ')
  const notes = [
    state === 'none' ? (status.daemon?.running ? t('settings.api.status.noteNoneDaemon') : t('settings.api.status.noteNone')) : s.note && t(s.note, { addr: l.addr }),
    url?.tls && t('settings.api.status.noteTls'),
    url?.wildcard && t('settings.api.status.noteWildcard'),
    serves && l.confinement_source !== 'daemon' && t('settings.api.status.noteAssumed'),
  ].filter(Boolean)

  const copy = async () => setCopied((await copyText(url.url)) ? 'ok' : 'failed')

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        {url && <span style={label}>{t('settings.api.status.baseUrl')}</span>}
        {url && <code data-testid="api-base-url" style={urlBox}>{url.url}</code>}
        {url && (
          <button type="button" className="btn btn-secondary btn-sm" aria-label={t('settings.api.status.copyUrl')} onClick={copy}>
            <Copy size={12} /> {t('settings.api.status.copy')}
          </button>
        )}
        <span role="status" aria-live="polite">
          {copied === 'ok' && <span style={okText}><Check size={11} /> {t('settings.api.status.copied')}</span>}
          {copied === 'failed' && <span style={errText}>{t('settings.api.status.copyFailed')}</span>}
        </span>
        <span style={{ flex: 1 }} />
        <button type="button" className="btn btn-ghost btn-sm" disabled={refreshing} onClick={onRefresh}>
          <RefreshCw size={12} className={refreshing ? 'spin' : undefined} /> {t('settings.api.refresh')}
        </button>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Badge data-testid="api-state" tone={s.tone}>{t(s.text)}</Badge>
        {serves && (
          <Badge
            data-testid="api-exposure" tone={l.loopback ? 'ok' : 'warn'}
            title={t(l.loopback ? 'settings.api.status.exposureLoopbackHint' : 'settings.api.status.exposureNetworkHint')}
          >
            {t(l.loopback ? 'settings.api.status.exposureLoopback' : 'settings.api.status.exposureNetwork')}
          </Badge>
        )}
        {serves && (
          <ClassBadge data-testid="api-confinement" value={l.confinement}>
            {t('settings.api.status.confinement', { class: l.confinement })}
          </ClassBadge>
        )}
        {serves && caps && <span style={hint}>{caps}</span>}
      </div>

      {notes.map((n, i) => <div key={i} style={hint}>{n}</div>)}
    </div>
  )
}
