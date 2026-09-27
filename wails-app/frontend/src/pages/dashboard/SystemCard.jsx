import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, ChevronRight, Activity } from 'lucide-react'
import { getHealth, subscribeHealth, summarize, runHealth } from '../../lib/health.js'
import { subscribeEvent } from '../../services/api.js'

// One service row: coloured dot, label, state; clickable when it links somewhere.
export function DashRow({ tone, label, value, title, onClick }) {
  const Tag = onClick ? 'button' : 'div'
  return (
    <Tag className="dash-row" onClick={onClick} title={title}>
      <span className="dash-dot" style={{ background: tone, boxShadow: `0 0 4px ${tone}` }} />
      <span className="dash-row-label">{label}</span>
      <span className="dash-row-value">{value}</span>
    </Tag>
  )
}

export const TONE = { ok: 'var(--green-neon)', warn: '#fbbf24', bad: '#ef4444', off: 'var(--text-dim)' }

export default function SystemCard({ summary, onNavigate }) {
  const { t } = useTranslation()
  const [health, setHealth] = useState(getHealth())
  useEffect(() => subscribeHealth(setHealth), [])
  // The app checks for a release in the background (via `update --check`)
  // and announces one; no extra request from here.
  const [update, setUpdate] = useState(null)
  useEffect(() => subscribeEvent('update:available', info => { if (info?.update_available) setUpdate(info) }), [])
  const sv = summary?.services
  const jev = summary?.jev
  const h = summarize(health?.report)
  const bridge = sv?.bridge ? sv.bridge.status : 'off'
  const toHealth = () => onNavigate('settings', { section: 'health' })
  const healthTone = health?.cliMissing || h.level === 'broken' ? TONE.bad : h.level === 'issues' ? TONE.warn : health?.report ? TONE.ok : TONE.off
  const healthValue = health?.cliMissing ? t('dashboard.system.cliMissing')
    : !health?.report ? t('dashboard.system.healthUnknown')
      : h.level === 'ok' ? t('dashboard.system.healthOk') : t('dashboard.system.healthIssues', { count: h.issues })

  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Server size={12} /> {t('dashboard.system.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={toHealth} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.system.details')} <ChevronRight size={11} />
        </button>
      </div>
      {!summary ? <div className="dash-empty">…</div> : (
        <>
          <DashRow tone={sv?.daemon?.running ? TONE.ok : TONE.bad} label={t('dashboard.system.daemon')}
            value={sv?.daemon?.running ? t('dashboard.system.running') : t('dashboard.system.stopped')}
            title={sv?.daemon?.pid ? `pid ${sv.daemon.pid}` : undefined} onClick={toHealth} />
          <DashRow tone={bridge === 'connected' ? TONE.ok : bridge === 'off' ? TONE.off : TONE.warn} label={t('dashboard.system.bridge')}
            value={t(`dashboard.system.bridgeState.${bridge}`)} title={sv?.bridge?.addr} onClick={toHealth} />
          <DashRow tone={sv?.org_serve?.running ? TONE.ok : TONE.off} label={t('dashboard.system.orgServe')}
            value={sv?.org_serve?.running ? t('dashboard.system.orgServeRunning', { count: sv.org_serve.orgs.length }) : t('dashboard.system.stopped')}
            onClick={() => onNavigate('orgs')} />
          <DashRow tone={healthTone} label={t('dashboard.system.health')} value={healthValue} onClick={toHealth} />
          <DashRow tone={jev?.key_configured ? TONE.ok : TONE.off} label={t('dashboard.system.jev')}
            value={jev?.error ? t('dashboard.unavailable') : jev?.key_configured
              ? t('dashboard.system.jevUsage', { calls: jev.calls_24h, usd: `$${(jev.estimated_usd_24h || 0).toFixed(2)}` })
              : t('dashboard.system.jevNoKey')}
            title={jev?.error || undefined}
            onClick={() => onNavigate('settings', { section: 'jev' })} />
          {update && (
            <DashRow tone={TONE.warn} label={t('dashboard.system.update')}
              value={t('dashboard.system.updateAvailable', { version: update.latest_version })}
              title={update.release_url} onClick={() => onNavigate('settings', { section: 'version' })} />
          )}
        </>
      )}
      <button className="btn btn-ghost btn-sm" style={{ marginTop: 8, gap: 5 }} disabled={health?.loading}
        onClick={() => runHealth({ background: true })}>
        <Activity size={11} style={{ animation: health?.loading ? 'spin 1s linear infinite' : 'none' }} />
        {health?.loading ? t('dashboard.system.checking') : t('dashboard.system.runCheck')}
      </button>
    </div>
  )
}
