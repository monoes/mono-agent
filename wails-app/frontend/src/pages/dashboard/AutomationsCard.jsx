import { useTranslation } from 'react-i18next'
import { Bot, ChevronRight } from 'lucide-react'

const SEGMENTS = [
  ['ok', 'var(--green-neon)'], ['decaying', '#fbbf24'], ['broken', '#ef4444'], ['stale', 'var(--text-dim)'],
]

function SelectorBar({ selectors }) {
  const { t } = useTranslation()
  const total = SEGMENTS.reduce((n, [k]) => n + (selectors?.[k] || 0), 0)
  if (!total) return <div className="dash-muted">{t('dashboard.automations.noSelectorData')}</div>
  return (
    <>
      <div className="dash-bar" role="img" aria-label={SEGMENTS.map(([k]) => `${t(`dashboard.automations.${k}`)} ${selectors[k] || 0}`).join(', ')}>
        {SEGMENTS.map(([k, c]) => selectors[k] > 0 && <span key={k} style={{ flex: selectors[k], background: c }} />)}
      </div>
      <div className="dash-legend">
        {SEGMENTS.map(([k, c]) => (
          <span key={k}><span className="dash-dot" style={{ background: c }} /> {selectors[k] || 0} {t(`dashboard.automations.${k}`)}</span>
        ))}
      </div>
    </>
  )
}

export default function AutomationsCard({ summary, onNavigate }) {
  const { t } = useTranslation()
  const a = summary?.automations
  const r = summary?.recordings
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Bot size={12} /> {t('dashboard.automations.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('connections')} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.automations.manage')} <ChevronRight size={11} />
        </button>
      </div>
      {!a ? <div className="dash-empty">…</div> : a.error ? <div className="dash-empty" title={a.error}>{t('dashboard.unavailable')}</div> : (
        <>
          <div className="dash-line">
            {t('dashboard.automations.installed', { count: a.installed })} · {t('dashboard.automations.enabled', { count: a.enabled })}
            {a.unavailable > 0 && <span className="dash-chip dash-chip-bad">{t('dashboard.automations.unavailable', { count: a.unavailable })}</span>}
            {a.pending_update > 0 && <span className="dash-chip">{t('dashboard.automations.pendingUpdate', { count: a.pending_update })}</span>}
            {a.scripts_blocked > 0 && <span className="dash-chip dash-chip-warn">{t('dashboard.automations.scriptsBlocked', { count: a.scripts_blocked })}</span>}
          </div>
          <div className="dash-subhead">{t('dashboard.automations.selectors')}</div>
          <SelectorBar selectors={a.selectors} />
          {(a.broken || []).slice(0, 3).map(b => (
            <button key={`${b.automation_id}/${b.selector_key}`} className="dash-link dash-bad-text"
              onClick={() => onNavigate('connections', { automationId: b.automation_id, tab: 'health' })}>
              {b.automation_id} › {b.selector_key}
            </button>
          ))}
        </>
      )}
      {r && !r.error && (
        <button className="dash-link" style={{ marginTop: 8 }} onClick={() => onNavigate('connections', { tab: 'recordings' })}>
          {t('dashboard.automations.recordings', { total: r.total, unsaved: r.unsaved, incomplete: r.incomplete })}
        </button>
      )}
    </div>
  )
}
