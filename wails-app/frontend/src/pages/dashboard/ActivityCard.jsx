import { useTranslation } from 'react-i18next'
import { Activity } from 'lucide-react'

function Tile({ label, value, sub, bad, error, onClick }) {
  const { t } = useTranslation()
  if (error) { value = '—'; sub = t('dashboard.unavailable'); bad = false }
  return (
    <button className="dash-tile" onClick={onClick} title={error || undefined}>
      <span className="dash-tile-label">{label}</span>
      <span className="dash-tile-value">{value ?? '—'}</span>
      {sub && <span className={`dash-tile-sub${bad ? ' dash-bad-text' : ''}`}>{sub}</span>}
    </button>
  )
}

// Seven mini-metrics, each linking to the page that owns it. The vault tile
// is counts only — the summary never carries a secret's name or value.
export default function ActivityCard({ summary, onNavigate }) {
  const { t } = useTranslation()
  const a = summary?.activity
  const ap = summary?.applications
  const p = summary?.people
  const v = summary?.vault
  const d = a?.documents
  const docErrors = (d?.summary_errors || 0) + (d?.index_errors || 0)
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Activity size={12} /> {t('dashboard.activity.title')}</div>
      </div>
      {!summary ? <div className="dash-empty">…</div> : (
        <div className="dash-tiles">
          <Tile error={a?.error} label={t('dashboard.activity.captures')} value={a?.captures_7d}
            sub={t('dashboard.activity.capturesTotal', { count: a?.captures_total || 0 })} onClick={() => onNavigate('documents')} />
          <Tile error={a?.error} label={t('dashboard.activity.documents')} value={d?.total}
            sub={docErrors > 0 ? t('dashboard.activity.docErrors', { count: docErrors })
              : d?.summarising > 0 ? t('dashboard.activity.summarising', { count: d.summarising }) : null}
            bad={docErrors > 0} onClick={() => onNavigate('documents')} />
          <Tile error={a?.error} label={t('dashboard.activity.messages')} value={a?.messages_in_7d}
            sub={t('dashboard.activity.sent', { count: a?.messages_out_7d || 0 })} onClick={() => onNavigate('communications')} />
          <Tile error={ap?.error} label={t('dashboard.activity.applications')} value={ap?.by_status?.pending}
            sub={t('dashboard.activity.applied', { count: ap?.by_status?.applied || 0 })} onClick={() => onNavigate('applications')} />
          <Tile error={ap?.error} label={t('dashboard.activity.toEvaluate')} value={ap?.unevaluated_pending}
            sub={t('dashboard.activity.evaluated', { count: ap?.evaluated || 0 })} onClick={() => onNavigate('applications')} />
          <Tile error={p?.error} label={t('dashboard.activity.people')} value={p?.added_7d}
            sub={t('dashboard.activity.lists', { count: p?.lists || 0 })} onClick={() => onNavigate('people')} />
          <Tile error={v?.error} label={t('dashboard.activity.vault')} value={v?.secrets}
            sub={t('dashboard.activity.images', { count: v?.images || 0 })} onClick={() => onNavigate('secretsVault')} />
        </div>
      )}
    </div>
  )
}
