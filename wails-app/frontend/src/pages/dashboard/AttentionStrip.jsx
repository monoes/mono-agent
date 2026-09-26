import { useTranslation } from 'react-i18next'
import { AlertTriangle, CheckCircle } from 'lucide-react'

export default function AttentionStrip({ items, loading, onNavigate, onOpenHil }) {
  const { t } = useTranslation()
  if (loading) return null
  if (!items.length) {
    return <div className="attn-strip attn-clear"><CheckCircle size={13} /> {t('dashboard.attention.allClear')}</div>
  }
  const go = target => (target.hil ? onOpenHil?.() : onNavigate(target.page, target.data))
  return (
    <div className="attn-strip" role="region" aria-label={t('dashboard.attention.title')}>
      <span className="attn-title"><AlertTriangle size={12} /> {t('dashboard.attention.title')}</span>
      {items.map(it => (
        <button key={it.id} className={`attn-chip attn-${it.severity}`} onClick={() => go(it.target)}>
          <span className="attn-count">{it.count}</span> {t(it.labelKey, { count: it.count })}
        </button>
      ))}
    </div>
  )
}
