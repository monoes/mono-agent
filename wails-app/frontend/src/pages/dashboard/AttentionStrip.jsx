import { useTranslation } from 'react-i18next'
import { AlertTriangle, CheckCircle, HelpCircle } from 'lucide-react'

// failed: the summary could not be loaded at all. unread: sections that
// reported an error — their counts are unknown, so "All clear" is not claimed.
export default function AttentionStrip({ items, loading, failed, unread = [], onNavigate, onOpenHil }) {
  const { t } = useTranslation()
  if (loading) return null
  if (failed && !items.length) {
    return <div className="attn-strip attn-clear dash-warn-text"><HelpCircle size={13} /> {t('dashboard.attention.unavailable')}</div>
  }
  const unreadNote = unread.length > 0 && (
    <span className="attn-unread" title={unread.join(', ')}><HelpCircle size={12} /> {t('dashboard.attention.partial', { count: unread.length })}</span>
  )
  if (!items.length) {
    return unreadNote
      ? <div className="attn-strip attn-clear">{unreadNote}</div>
      : <div className="attn-strip attn-clear"><CheckCircle size={13} /> {t('dashboard.attention.allClear')}</div>
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
      {unreadNote}
    </div>
  )
}
