import { useTranslation } from 'react-i18next'
import { Layers, Zap, XCircle, Network, Users } from 'lucide-react'

// error: the section behind this card failed — show "—", not a fake zero.
function StatCard({ icon: Icon, label, value, sub, color, loading, error, onClick }) {
  const { t } = useTranslation()
  const Tag = onClick ? 'button' : 'div'
  if (error) { value = '—'; sub = t('dashboard.unavailable') }
  return (
    <Tag className={`stat-card${onClick ? ' stat-card-btn' : ''}`} onClick={onClick} title={error || undefined}
      style={{ '--accent-color': color, '--icon-bg': color + '18', '--icon-color': color }}>
      <span className="stat-icon"><Icon size={16} /></span>
      <span className="stat-value">
        {loading
          ? <span style={{ display: 'block', width: 40, height: 20, background: 'var(--elevated)', borderRadius: 4, animation: 'pulse-dot 1.5s infinite' }} />
          : (value ?? '—')}
      </span>
      <span className="stat-label">{label}</span>
      {sub && !loading && <span className="stat-sub">{sub}</span>}
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
        value={wf?.total} error={wf?.error} sub={wf ? t('dashboard.stat.activeCount', { count: wf.active }) : null}
        onClick={() => onNavigate('noderunner')} />
      <StatCard icon={Zap} color="#eab308" loading={waiting} label={t('dashboard.stat.running')}
        value={ex?.running} error={ex?.error} sub={ex ? t('dashboard.stat.queuedCount', { count: (ex.queued || 0) + (ex.waiting || 0) }) : null} />
      <StatCard icon={XCircle} color="#ef4444" loading={waiting} label={t('dashboard.stat.failed24h')}
        value={ex?.last_24h?.failed} error={ex?.error} sub={ex ? t('dashboard.stat.ofRuns', { count: ex.last_24h?.total || 0 }) : null} />
      <StatCard icon={Network} color="var(--purple-light)" loading={loading || !orgs} label={t('dashboard.stat.orgs')}
        value={ot?.running} sub={ot ? t('dashboard.stat.ofOrgs', { count: ot.orgs || 0 }) : null}
        onClick={() => onNavigate('orgs')} />
      <StatCard icon={Users} color="var(--green-neon)" loading={waiting} label={t('dashboard.stat.people')}
        value={people?.total} error={people?.error} sub={people ? t('dashboard.stat.addedWeek', { count: people.added_7d || 0 }) : null}
        onClick={() => onNavigate('people')} />
    </div>
  )
}
