import { useTranslation } from 'react-i18next'
import { Shield, ChevronRight } from 'lucide-react'
import { PLATFORM_COLORS } from '../../services/api.js'
import { untilTime } from './format.js'
import { TONE } from './SystemCard.jsx'

const STATUS_TONE = { active: TONE.ok, expiring: TONE.warn, expired: TONE.bad }

export default function AccountsCard({ summary, onNavigate }) {
  const { t } = useTranslation()
  const ac = summary?.accounts
  const sessions = ac?.sessions || []
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Shield size={12} /> {t('dashboard.accounts.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('connections')} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.accounts.manage')} <ChevronRight size={11} />
        </button>
      </div>
      {!ac ? <div className="dash-empty">…</div> : sessions.length === 0 ? (
        <div className="dash-empty">{t('dashboard.accounts.empty')}</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          {sessions.map((s, i) => {
            const color = PLATFORM_COLORS[s.platform?.toUpperCase()] || 'var(--cyan)'
            const state = s.status === 'expiring'
              ? t('dashboard.accounts.expiresIn', { when: untilTime(s.expiry, t) })
              : t(`dashboard.accounts.${s.status === 'expired' ? 'expired' : 'active'}`)
            return (
              <div key={`${s.platform}/${s.username}/${i}`} className="dash-account" style={{ opacity: s.status === 'expired' ? 0.6 : 1 }}>
                <span className="dash-dot" style={{ background: STATUS_TONE[s.status] || TONE.off }} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="dash-ellipsis" style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text)' }}>{s.username || '—'}</div>
                  <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: s.status === 'active' ? 'var(--text-muted)' : STATUS_TONE[s.status] }}>{state}</div>
                </div>
                <span className="badge" style={{ background: color + '20', color, borderColor: color + '40' }}>{s.platform || '—'}</span>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
