import { useTranslation } from 'react-i18next'
import { Users } from 'lucide-react'
import { switchToProfile } from './profileSwitch.js'

// One row per profile in the All profiles view: what each is doing, and a
// way to go there.
export default function ProfilesCard({ summary, onSwitch = switchToProfile }) {
  const { t } = useTranslation()
  const profiles = summary?.profiles || []
  return (
    <div className="card" data-testid="dash-profiles">
      <div className="section-header">
        <div className="section-title"><Users size={12} /> {t('dashboard.profiles.title')}</div>
      </div>
      {profiles.length === 0 ? <div className="dash-empty">…</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {profiles.map(p => (
            <div key={p.id} className="dash-account" data-profile={p.id}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="dash-ellipsis" style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--text)' }}>
                  {p.name}
                  {p.current && <span className="dash-chip" style={{ marginLeft: 6 }}>{t('dashboard.profiles.current')}</span>}
                </div>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: p.error ? '#ef4444' : 'var(--text-muted)' }} title={p.error || undefined}>
                  {p.error ? t('dashboard.unavailable') : t('dashboard.profiles.line', {
                    active: p.workflows_active || 0, running: p.running || 0, failed: p.failed_24h || 0, waiting: p.waiting_for_you || 0,
                  })}
                </div>
              </div>
              {!p.current && (
                <button className="btn btn-ghost btn-sm" onClick={() => onSwitch(p.id)} title={t('dashboard.profiles.switchTo', { name: p.name })}>
                  {t('dashboard.profiles.switch')}
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
