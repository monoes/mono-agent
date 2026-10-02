import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check, RefreshCw } from 'lucide-react'
import { copyText } from '../../../pages/connections/ui.jsx'
import { baseURL, listenerState, pickListener, servingListeners } from './apiModel.js'
import { Badge, ClassBadge, label, hint, errText, okText, mono } from './ui.jsx'

// Where /v1 listens, whether it answers, and what it serves: for every listener
// that serves /v1, its base URL with a copy button, its running state and its
// exposure and confinement badges. A network-exposed listener is never left out
// for a loopback one that answers. What it says of a listener is what
// `api status` says (its listenerNote), worded for the page; the policy shown is
// the running daemon's when it reported one.

export const STATE = {
  serving: { tone: 'ok', text: 'settings.api.status.stateServing' },
  down: { tone: 'muted', text: 'settings.api.status.stateDown', note: 'settings.api.status.noteDown' },
  'down-daemon': { tone: 'warn', text: 'settings.api.status.stateDownDaemon', note: 'settings.api.status.noteDownDaemon' },
  stale: { tone: 'warn', text: 'settings.api.status.stateStale', note: 'settings.api.status.noteStale' },
  'no-v1-daemon': { tone: 'warn', text: 'settings.api.status.stateNoV1', note: 'settings.api.status.noteNoV1Daemon' },
  'no-v1-offloopback': { tone: 'warn', text: 'settings.api.status.stateNoV1', note: 'settings.api.status.noteNoV1Offloopback' },
  none: { tone: 'muted', text: 'settings.api.status.stateNone' },
}

const urlBox = {
  fontFamily: mono, fontSize: 12, color: 'var(--text)', background: 'var(--elevated)', border: '1px solid var(--border)',
  borderRadius: 'var(--radius)', padding: '4px 10px', userSelect: 'all', wordBreak: 'break-all',
}
const line = { display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }

/**
 * @param {object|null} status `api status --json`, or null while it loads.
 * @param {string} err Why it could not be read.
 * @param {boolean} refreshing
 * @param {() => void} onRefresh
 * @param {() => void} onRetry
 */
export default function ApiStatusBlock({ status, err, refreshing, onRefresh, onRetry }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState({ name: '', result: '' }) // result: '' | 'ok' | 'failed', of the row named
  useEffect(() => {
    if (copied.result !== 'ok') return undefined
    const id = setTimeout(() => setCopied({ name: '', result: '' }), 2000)
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

  // Every listener that serves /v1; with none, the one that says why (null: no listener at all).
  const serving = servingListeners(status)
  const shown = serving.length ? serving : [pickListener(status)]
  const refresh = (
    <button type="button" className="btn btn-ghost btn-sm" disabled={refreshing} onClick={onRefresh}>
      <RefreshCw size={12} className={refreshing ? 'spin' : undefined} /> {t('settings.api.refresh')}
    </button>
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {shown.map((l, i) => (
        <ListenerRow
          key={l?.name || 'none'} l={l} daemonRunning={!!status.daemon?.running} many={shown.length > 1}
          copied={l && copied.name === l.name ? copied.result : ''}
          onCopy={async (url) => setCopied({ name: l.name, result: (await copyText(url)) ? 'ok' : 'failed' })}
          trailing={i === 0 ? refresh : null}
        />
      ))}
    </div>
  )
}

// ListenerRow is one listener (or, with none, the line that says there is none).
// `trailing` (the Refresh button) goes at the end of the row's first line.
function ListenerRow({ l, daemonRunning, many, copied, onCopy, trailing }) {
  const { t } = useTranslation()
  const state = listenerState(l, daemonRunning)
  const s = STATE[state]
  const url = baseURL(l)
  const serves = !!l?.v1 // it is meant to serve /v1: its policy means something
  const name = l?.name === 'main' ? t('settings.api.status.listenerMain') : l?.name === 'v1' ? t('settings.api.status.listenerDedicated') : l?.name || ''
  const caps = serves ? [
    l.context_confinement && t('settings.api.status.capsContext', { class: l.context_confinement }),
    l.auto_confinement && t('settings.api.status.capsAuto', { class: l.auto_confinement }),
  ].filter(Boolean).join(' · ') : ''
  const notes = [
    state === 'none' ? t(daemonRunning ? 'settings.api.status.noteNoneDaemon' : 'settings.api.status.noteNone') : s.note && t(s.note, { addr: l.addr }),
    serves && !url && l.addr && t('settings.api.status.noteBadAddr', { addr: l.addr }),
    url?.tls && t('settings.api.status.noteTls'),
    url?.wildcard && t('settings.api.status.noteWildcard'),
    serves && l.confinement_source !== 'daemon' && t('settings.api.status.noteAssumed'),
  ].filter(Boolean)

  // The first line that exists carries the Refresh button: the title of a row among several, else the URL, else the badges.
  const trailingOn = many ? 'title' : url ? 'url' : 'badges'
  return (
    <div
      data-testid={`api-listener-${l ? l.name : 'none'}`}
      style={{ display: 'flex', flexDirection: 'column', gap: 10, ...(many ? { border: '1px solid var(--border-dim)', borderRadius: 'var(--radius)', padding: '10px 12px' } : null) }}
    >
      {many && (
        <div style={line}>
          <span style={label}>{name}</span>
          <span style={{ flex: 1 }} />
          {trailingOn === 'title' && trailing}
        </div>
      )}

      {url && (
        <div style={line}>
          <span style={label}>{t('settings.api.status.baseUrl')}</span>
          <code data-testid="api-base-url" style={urlBox}>{url.url}</code>
          <button
            type="button" className="btn btn-secondary btn-sm" onClick={() => onCopy(url.url)}
            aria-label={many ? t('settings.api.status.copyUrlOf', { name }) : t('settings.api.status.copyUrl')}
          >
            <Copy size={12} /> {t('settings.api.status.copy')}
          </button>
          <span role="status" aria-live="polite">
            {copied === 'ok' && <span style={okText}><Check size={11} /> {t('settings.api.status.copied')}</span>}
            {copied === 'failed' && <span style={errText}>{t('settings.api.status.copyFailed')}</span>}
          </span>
          <span style={{ flex: 1 }} />
          {trailingOn === 'url' && trailing}
        </div>
      )}

      <div style={{ ...line, gap: 8 }}>
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
        {trailingOn === 'badges' && <><span style={{ flex: 1 }} />{trailing}</>}
      </div>

      {notes.map((n, i) => <div key={i} style={hint}>{n}</div>)}
    </div>
  )
}
