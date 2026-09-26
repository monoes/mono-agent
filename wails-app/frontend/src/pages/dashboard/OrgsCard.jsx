import { useTranslation } from 'react-i18next'
import { Network, ChevronRight } from 'lucide-react'
import { DashRow, TONE } from './SystemCard.jsx'

const MAX_ROWS = 6

function OrgRow({ org, onNavigate }) {
  const { t } = useTranslation()
  const level = t(`orgs.autonomy.levels.${org.level}`, { defaultValue: org.level })
  const needs = org.needs_you == null
    ? <span title={org.needs_you_error || t('dashboard.orgs.counting')}>…</span>
    : org.needs_you
  return (
    <DashRow
      tone={org.running ? TONE.ok : TONE.off}
      label={<>{org.name} <span className="dash-chip">{org.paused ? t('dashboard.orgs.paused') : level}</span></>}
      value={<>
        <span className={org.needs_you ? 'dash-warn-text' : ''}>{needs} {t('dashboard.orgs.needsYou')}</span>
        {org.queued > 0 && <> · {org.queued} {t('dashboard.orgs.queued')}</>}
      </>}
      title={org.running ? t('dashboard.system.running') : t('dashboard.system.stopped')}
      onClick={() => onNavigate('orgs', { org: org.name })}
    />
  )
}

export default function OrgsCard({ orgs, onNavigate }) {
  const { t } = useTranslation()
  const list = orgs?.orgs || []
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Network size={12} /> {t('dashboard.orgs.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('orgs')} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.orgs.open')} <ChevronRight size={11} />
        </button>
      </div>
      {!orgs ? <div className="dash-empty">…</div>
        : list.length === 0 ? (
          <div className="dash-empty">
            {t('dashboard.orgs.empty')}{' '}
            <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('orgs')}>{t('dashboard.orgs.create')}</button>
          </div>
        ) : (
          <>
            {list.slice(0, MAX_ROWS).map(o => <OrgRow key={o.name} org={o} onNavigate={onNavigate} />)}
            {list.length > MAX_ROWS && (
              <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('orgs')}>
                {t('dashboard.orgs.more', { count: list.length - MAX_ROWS })}
              </button>
            )}
          </>
        )}
    </div>
  )
}
