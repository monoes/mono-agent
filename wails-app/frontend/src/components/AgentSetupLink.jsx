import { useTranslation } from 'react-i18next'
import { Bot } from 'lucide-react'

// The action next to an error that failed because the AI agent is not set
// up: it opens the AI agents page, where runtimes are installed and chosen.
export default function AgentSetupLink({ onNavigate, onDone, compact = false }) {
  const { t } = useTranslation()
  if (!onNavigate) return null
  return (
    <button
      type="button"
      onClick={(e) => { e.stopPropagation(); onNavigate('ai'); onDone?.() }}
      title={t('agentSetup.title')}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 5,
        marginTop: compact ? 0 : 6, padding: compact ? '1px 8px' : '3px 10px',
        borderRadius: 4, background: 'transparent', color: '#00b4d8',
        border: '1px solid rgba(0,180,216,.35)', cursor: 'pointer',
        fontFamily: 'var(--font-mono)', fontSize: 10, flexShrink: 0,
      }}
    >
      <Bot size={11} /> {t('agentSetup.action')}
    </button>
  )
}
