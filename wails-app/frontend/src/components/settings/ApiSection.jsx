import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronUp, KeyRound, Loader2, Plug } from 'lucide-react'
import { APIStatus, APIKeyList, APIModels } from '../../wailsjs/go/main/App'
import ApiStatusBlock, { STATE } from './api/ApiStatusBlock.jsx'
import ApiKeysBlock from './api/ApiKeysBlock.jsx'
import ApiModelsBlock from './api/ApiModelsBlock.jsx'
import { apiError } from './api/apiError.js'
import { listenerState, modelsArgs, pickListener, servingListeners } from './api/apiModel.js'
import { Badge, hint, mono } from './api/ui.jsx'

// Settings › OpenAI-compatible API (spec §8.4): where /v1 listens and whether it
// runs, the active profile's API keys, and the models it serves. Everything goes
// through `monoagentcli api …` (Go side: app_api.go), which is also what a
// terminal user runs. Folded by default like the Jev section: the status, which
// is cheap, is read when Settings opens; the keys and the models, which start
// monomind to scan the runtimes, are read when the section is first opened.
// Settings stays mounted while another page is shown, so what was read is as
// old as the visit: coming back to the page reads the status again, and the keys
// and the models too once they had been read (the models only when the listener
// they describe changed: a scan of the runtimes is not repeated for nothing).

const card = {
  background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)',
  padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 14, marginBottom: 16,
}

// What a loader says when a call that started after it has taken its place: there is nothing to go on with.
const SUPERSEDED = Symbol('superseded')

/**
 * @param {(page: string, data?: object) => void} onNavigate Settings' own: the jump to the Jev settings is
 *   `onNavigate('settings', { section: 'jev' })`, the dashboard's deep link.
 * @param {boolean} isActive Whether Settings is the page on show (the app keeps visited pages mounted, hidden).
 *   Without it, always.
 */
export default function ApiSection({ onNavigate, isActive = true } = {}) {
  const { t } = useTranslation()
  const bodyId = useId()
  const [expanded, setExpanded] = useState(false)

  const [status, setStatus] = useState(null)
  const [statusErr, setStatusErr] = useState('')
  const [keys, setKeys] = useState(null)
  const [keysErr, setKeysErr] = useState('')
  const [models, setModels] = useState(null)
  const [modelsErr, setModelsErr] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const detailsAsked = useRef(false)
  // A failure is worded when it happens, in the language of the moment; the loaders keep one identity.
  const tRef = useRef(t)
  tRef.current = t
  // Each loader applies the answer of the call that started last: a slower one started before it (a refresh while a
  // re-read runs, a retry) is dropped, so an older state never overwrites a newer one.
  const seq = useRef({ status: 0, keys: 0, models: 0 })
  const modelsFor = useRef(null) // the arguments of the models on show (null: none, or the last read of them failed)

  // Each part is read on its own: one that fails is shown as failed, and the others stay.
  const loadStatus = useCallback(async () => {
    const mine = ++seq.current.status
    try {
      const st = await APIStatus()
      if (mine !== seq.current.status) return SUPERSEDED
      setStatus(st); setStatusErr(''); return st
    } catch (e) {
      if (mine !== seq.current.status) return SUPERSEDED
      setStatusErr(apiError(e, tRef.current)); return null
    }
  }, [])
  const loadKeys = useCallback(async () => {
    const mine = ++seq.current.keys
    try {
      const list = await APIKeyList()
      if (mine === seq.current.keys) { setKeys(list); setKeysErr('') }
    } catch (e) {
      if (mine === seq.current.keys) setKeysErr(apiError(e, tRef.current))
    }
  }, [])
  // The models are evaluated for the listener the header describes, so they wait for the status.
  const loadModels = useCallback(async (st) => {
    const mine = ++seq.current.models
    const args = modelsArgs(pickListener(st))
    const key = JSON.stringify(args)
    if (key !== modelsFor.current) setModels(null) // another policy: the table on show is not the answer to this
    try {
      const m = await APIModels(...args)
      if (mine === seq.current.models) { setModels(m); setModelsErr(''); modelsFor.current = key }
    } catch (e) {
      if (mine === seq.current.models) { setModelsErr(apiError(e, tRef.current)); modelsFor.current = null }
    }
  }, [])

  useEffect(() => { loadStatus() }, [loadStatus])

  const statusSettled = status !== null || statusErr !== ''
  useEffect(() => {
    if (!expanded || !statusSettled || detailsAsked.current) return
    detailsAsked.current = true
    loadKeys()
    loadModels(status)
    // status is read once here, when it settles: a later one goes through refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded, statusSettled, loadKeys, loadModels])

  // Coming back to the page (not the first time, which is the mount): what was read is as old as the visit.
  const wasActive = useRef(isActive)
  useEffect(() => {
    const returned = isActive && !wasActive.current
    wasActive.current = isActive
    if (!returned) return
    ;(async () => {
      const st = await loadStatus()
      if (st === SUPERSEDED || !detailsAsked.current) return
      loadKeys()
      if (JSON.stringify(modelsArgs(pickListener(st))) !== modelsFor.current) loadModels(st)
    })()
  }, [isActive, loadStatus, loadKeys, loadModels])

  const refresh = async () => {
    detailsAsked.current = true
    setRefreshing(true)
    try {
      const st = await loadStatus()
      if (st !== SUPERSEDED) await Promise.all([loadKeys(), loadModels(st)])
    } finally {
      setRefreshing(false)
    }
  }

  const listener = pickListener(status)
  const state = STATE[listenerState(listener, !!status?.daemon?.running)]
  // Any listener that serves /v1 beyond loopback is said in the header, whichever listener it describes.
  const serving = servingListeners(status)
  const exposed = serving.some(l => !l.loopback)
  const contextClasses = [...new Set(serving.map(l => l.context_confinement).filter(Boolean))]
  const count = keys ? keys.length : status?.keys?.active
  const toggle = () => setExpanded(v => !v)

  return (
    <div id="settings-api" data-testid="api-section">
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
        <span style={{ fontFamily: mono, fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
          {t('settings.api.sectionTitle')}
        </span>
        <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
      </div>

      <button
        type="button" data-testid="api-fold-toggle" aria-expanded={expanded} aria-controls={bodyId} onClick={toggle}
        style={{
          width: '100%', textAlign: 'left', font: 'inherit', color: 'inherit',
          background: 'var(--surface)', border: `1px solid ${statusErr ? 'rgba(239,68,68,.35)' : 'var(--border)'}`, borderRadius: 'var(--radius-lg)',
          padding: '12px 18px', marginBottom: expanded ? 12 : 16, display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          cursor: 'pointer', userSelect: 'none', transition: 'border-color 0.15s ease, background 0.15s ease',
        }}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Plug size={16} style={{ color: !statusErr && state?.tone === 'ok' ? 'var(--cyan)' : 'var(--text-muted)', flexShrink: 0 }} />
            <span style={{ fontFamily: mono, fontSize: 12, fontWeight: 700, color: 'var(--text)', letterSpacing: 0.5 }}>
              {t('settings.api.sectionTitle')}
            </span>
          </span>
          {statusErr ? (
            <Badge data-testid="api-fold-state" tone="bad">{t('settings.api.statusError')}</Badge>
          ) : !status ? (
            <Badge data-testid="api-fold-state" tone="info"><Loader2 size={11} className="spin" /> {t('settings.api.loading')}</Badge>
          ) : (
            <Badge data-testid="api-fold-state" tone={state.tone}>{t(state.text)}</Badge>
          )}
          {!statusErr && exposed && (
            <Badge data-testid="api-fold-exposure" tone="warn" title={t('settings.api.status.exposureNetworkHint')}>
              {t('settings.api.status.exposureNetwork')}
            </Badge>
          )}
          {typeof count === 'number' && (
            <Badge data-testid="api-fold-keys" tone={count > 0 ? 'ok' : 'muted'}>
              <KeyRound size={11} /> {t('settings.api.keysChip', { count })}
            </Badge>
          )}
        </span>
        {expanded ? <ChevronUp size={16} style={{ color: 'var(--text-muted)' }} /> : <ChevronDown size={16} style={{ color: 'var(--text-muted)' }} />}
      </button>

      {expanded && (
        <div id={bodyId} style={card}>
          <div style={hint}>{t('settings.api.intro')}</div>
          <ApiStatusBlock status={status} err={statusErr} refreshing={refreshing} onRefresh={refresh} onRetry={refresh} />
          <ApiKeysBlock
            keys={keys} err={keysErr} contextClasses={contextClasses}
            onChanged={loadKeys} onRetry={loadKeys}
          />
          <ApiModelsBlock
            models={models} err={modelsErr} status={status} statusErr={statusErr}
            onOpenJev={onNavigate ? () => onNavigate('settings', { section: 'jev' }) : undefined}
            onRetry={() => loadModels(status)}
          />
        </div>
      )}
    </div>
  )
}
