import { useTranslation } from 'react-i18next'
import { Layers, Zap, XCircle, Network, Users } from 'lucide-react'

function StatCard({ icon: Icon, label, value, sub, color, loading, onClick }) {
  const Tag = onClick ? 'button' : 'div'
  return (
    <Tag className={`stat-card${onClick ? ' stat-card-btn' : ''}`} onClick={onClick}
      style={{ '--accent-color': color, '--icon-bg': color + '18', '--icon-color': color }}>
      <div className="stat-icon"><Icon size={16} /></div>
      <div className="stat-value">
        {loading
          ? <div style={{ width: 40, height: 20, background: 'var(--elevated)', borderRadius: 4, animation: 'pulse-dot 1.5s infinite' }} />
          : (value ?? '—')}
      </div>
      <div className="stat-label">{label}</div>
      {sub && !loading && <div className="stat-sub">{sub}</div>}
    </Tag>
  )
}

export default function StatRow({ summary, orgs, loading, onNavigate }) {
  const { t } = useTranslation()
  const wf = summary?.workflows
  const ex = summary?.executions
  const people = summary?.people
  const ot = orgs?.totals
  const waiting = loading || !summary
  return (
    <div className="stat-grid">
      <StatCard icon={Layers} color="var(--cyan)" loading={waiting} label={t('dashboard.stat.workflows')}
        value={wf?.total} sub={wf ? t('dashboard.stat.activeCount', { count: wf.active }) : null}
        onClick={() => onNavigate('noderunner')} />
      <StatCard icon={Zap} color="#eab308" loading={waiting} label={t('dashboard.stat.running')}
        value={ex?.running} sub={ex ? t('dashboard.stat.queuedCount', { count: (ex.queued || 0) + (ex.waiting || 0) }) : null} />
      <StatCard icon={XCircle} color="#ef4444" loading={waiting} label={t('dashboard.stat.failed24h')}
        value={ex?.last_24h?.failed} sub={ex ? t('dashboard.stat.ofRuns', { count: ex.last_24h?.total || 0 }) : null} />
      <StatCard icon={Network} color="var(--purple-light)" loading={loading || !orgs} label={t('dashboard.stat.orgs')}
        value={ot?.running} sub={ot ? t('dashboard.stat.ofOrgs', { count: ot.orgs || 0 }) : null}
        onClick={() => onNavigate('orgs')} />
      <StatCard icon={Users} color="var(--green-neon)" loading={waiting} label={t('dashboard.stat.people')}
        value={people?.total} sub={people ? t('dashboard.stat.addedWeek', { count: people.added_7d || 0 }) : null}
        onClick={() => onNavigate('people')} />
    </div>
  )
}
