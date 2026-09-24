// Org header autonomy controls (U16/U17): level selector, decider picker, and
// Pause autonomy. Writes only through `monoagentcli org autonomy …` bindings;
// the level is enforced from the CLI's DB copy, so the bar always re-reads
// after a change instead of trusting local state.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { PauseCircle, PlayCircle, ShieldCheck, RefreshCw } from 'lucide-react'
import { api, notify } from '../../services/api.js'
import { LEVELS, DECIDERS, isPaused, effectiveLevel } from './autonomyModel.js'
import { toMillis } from './waiting.js'
import FullAutoConfirm from './FullAutoConfirm.jsx'
import { Chip, mono, smallBtn } from './ui.jsx'

const PAUSE_OPTIONS = [
  { id: '30m', labelKey: 'orgs.autonomy.pauseFor30m' },
  { id: '2h', labelKey: 'orgs.autonomy.pauseFor2h' },
  { id: '', labelKey: 'orgs.autonomy.pauseUntilResumed' },
]

function pausedLabel(autonomy, t) {
  const until = toMillis(autonomy?.paused_until)
  if (until == null || until - Date.now() > 365 * 24 * 3600_000) return t('orgs.autonomy.paused')
  return t('orgs.autonomy.pausedUntil', { time: new Date(until).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
}

export default function AutonomyBar({ orgName, onChange }) {
  const { t } = useTranslation()
  const [autonomy, setAutonomy] = useState(null)
  const [loading, setLoading] = useState(true)
  const [unavailable, setUnavailable] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmFull, setConfirmFull] = useState(false)
  const [pauseOpen, setPauseOpen] = useState(false)
  const orgRef = useRef(orgName)
  orgRef.current = orgName
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  const load = useCallback(async () => {
    if (!orgName) return
    const res = await api.getOrgAutonomy(orgName)
    if (orgRef.current !== orgName) return
    setLoading(false)
    if (!res || res.error) {
      setAutonomy(null)
      setUnavailable(res?.error || t('orgs.autonomy.unreadable'))
      return
    }
    setUnavailable('')
    setAutonomy(res)
    onChangeRef.current?.(res)
  }, [orgName, t])

  useEffect(() => {
    setLoading(true)
    setAutonomy(null)
    setPauseOpen(false)
    load()
    const iv = setInterval(load, 30_000)
    return () => clearInterval(iv)
  }, [load])

  const apply = useCallback(async (fn, op) => {
    setBusy(true)
    try {
      const res = await fn()
      if (!res || res.error) notify(op, res?.error || `${op} failed`)
      await load()
    } finally {
      setBusy(false)
    }
  }, [load])

  const setLevel = (level) => {
    if (!autonomy || level === autonomy.level) return
    if (level === 'full') { setConfirmFull(true); return }
    apply(() => api.setOrgAutonomy(orgName, { level }), 'set autonomy level')
  }

  const setDecider = (kind) => {
    if (!autonomy || kind === autonomy.decider?.kind) return
    apply(() => api.setOrgAutonomy(orgName, { decider: { kind } }), 'set decider')
  }

  if (!orgName) return null

  if (loading) {
    return <div style={{ ...mono, fontSize: 10, color: 'var(--text-muted)' }}>{t('orgs.autonomy.loading')}</div>
  }

  if (!autonomy) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }} title={unavailable}>
        <span style={{ ...mono, fontSize: 10, color: 'var(--text-muted)' }}>{t('orgs.autonomy.unavailable')}</span>
        <button style={smallBtn} onClick={() => { setLoading(true); load() }} aria-label={t('orgs.autonomy.retry')}>
          <RefreshCw size={10} />
        </button>
      </div>
    )
  }

  const paused = isPaused(autonomy)
  const eff = effectiveLevel(autonomy)
  const deciderKind = autonomy.decider?.kind || 'model'

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <ShieldCheck size={12} style={{ color: 'var(--text-muted)' }} />
      <div role="radiogroup" aria-label={t('orgs.autonomy.levelGroup')} style={{ display: 'flex', border: '1px solid var(--border)', borderRadius: 'var(--radius)', overflow: 'hidden' }}>
        {LEVELS.map(l => {
          const active = autonomy.level === l.id
          return (
            <button
              key={l.id}
              role="radio"
              aria-checked={active}
              disabled={busy}
              title={t(`orgs.autonomy.levels.${l.id}Hint`)}
              onClick={() => setLevel(l.id)}
              style={{
                ...mono, fontSize: 10, padding: '4px 9px', border: 'none', cursor: busy ? 'default' : 'pointer',
                background: active ? (l.id === 'full' ? 'rgba(239,68,68,0.15)' : 'var(--elevated)') : 'transparent',
                color: active ? (l.id === 'full' ? 'var(--red, #ef4444)' : 'var(--text)') : 'var(--text-muted)',
              }}
            >
              {t(`orgs.autonomy.levels.${l.id}`)}
            </button>
          )
        })}
      </div>

      <label style={{ display: 'flex', alignItems: 'center', gap: 4, ...mono, fontSize: 10, color: 'var(--text-muted)' }}>
        {t('orgs.autonomy.decider')}
        <select
          aria-label={t('orgs.autonomy.decider')}
          className="filter-select"
          value={deciderKind}
          disabled={busy}
          onChange={e => setDecider(e.target.value)}
          style={{ fontSize: 10, padding: '2px 22px 2px 6px' }}
        >
          {DECIDERS.map(d => <option key={d.id} value={d.id} title={t(`orgs.autonomy.deciders.${d.id}Hint`)}>{t(`orgs.autonomy.deciders.${d.id}`)}</option>)}
        </select>
      </label>

      {paused ? (
        <>
          <Chip color="#eab308" title={t('orgs.autonomy.pausedTitle')}>{pausedLabel(autonomy, t)}</Chip>
          <button style={smallBtn} disabled={busy} onClick={() => apply(() => api.resumeOrgAutonomy(orgName), 'resume autonomy')}>
            <PlayCircle size={11} /> {t('orgs.autonomy.resume')}
          </button>
        </>
      ) : (
        <div style={{ position: 'relative' }}>
          <button
            style={smallBtn}
            disabled={busy || autonomy.level === 'manual'}
            title={autonomy.level === 'manual' ? t('orgs.autonomy.alreadyManual') : t('orgs.autonomy.pauseTitle')}
            aria-haspopup="menu"
            aria-expanded={pauseOpen}
            onClick={() => setPauseOpen(v => !v)}
          >
            <PauseCircle size={11} /> {t('orgs.autonomy.pause')}
          </button>
          {pauseOpen && (
            <div role="menu" style={{
              position: 'absolute', top: '100%', right: 0, marginTop: 4, zIndex: 20, minWidth: 140,
              background: 'var(--surface)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)',
              boxShadow: '0 8px 24px rgba(0,0,0,0.5)', display: 'flex', flexDirection: 'column',
            }}>
              {PAUSE_OPTIONS.map(o => (
                <button
                  key={o.labelKey}
                  role="menuitem"
                  onClick={() => { setPauseOpen(false); apply(() => api.pauseOrgAutonomy(orgName, o.id), 'pause autonomy') }}
                  style={{ ...mono, fontSize: 10.5, textAlign: 'left', padding: '6px 10px', background: 'transparent', border: 'none', color: 'var(--text)', cursor: 'pointer' }}
                >
                  {t(o.labelKey)}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {autonomy.daemon_running === false && autonomy.level !== 'manual' && (
        <Chip color="#eab308" title={t('orgs.autonomy.daemonOffTitle')}>{t('orgs.autonomy.daemonOff')}</Chip>
      )}
      {!paused && eff !== autonomy.level && autonomy.daemon_running !== false && (
        <Chip color="#eab308">{t('orgs.autonomy.actingAs', { level: t(`orgs.autonomy.levels.${eff}`, { defaultValue: eff }) })}</Chip>
      )}

      <FullAutoConfirm
        open={confirmFull}
        orgName={orgName}
        autonomy={autonomy}
        deciderKind={deciderKind}
        onCancel={() => setConfirmFull(false)}
        onConfirm={() => { setConfirmFull(false); apply(() => api.setOrgAutonomy(orgName, { level: 'full' }), 'set autonomy level') }}
      />
    </div>
  )
}
