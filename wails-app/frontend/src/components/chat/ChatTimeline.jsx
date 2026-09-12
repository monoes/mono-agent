import { AlertTriangle, Info, AlertCircle } from 'lucide-react'
import { ChatMarkdown } from './ChatMarkdown.jsx'
import { ToolActivityCard } from './ToolActivityCard.jsx'

const NOTICE_ICON = {
  info: Info,
  warning: AlertTriangle,
  error: AlertCircle,
}
const NOTICE_COLOR = {
  info: '#00b4d8',
  warning: '#fbbf24',
  error: '#ef4444',
}

function NoticeBanner({ notice }) {
  const Icon = NOTICE_ICON[notice.severity] || Info
  const color = NOTICE_COLOR[notice.severity] || '#00b4d8'
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6, padding: '6px 8px', marginTop: 6, borderRadius: 6, background: `${color}14`, border: `1px solid ${color}40` }}>
      <Icon size={11} color={color} style={{ marginTop: 1, flexShrink: 0 }} />
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color }}>{notice.message}</span>
    </div>
  )
}

// ChatTimeline renders one turn's ordered parts (interleaved assistant text
// and tool steps, in actual arrival order — never re-sorted or grouped by
// kind) plus any nonfatal notices. Pure presentation over chatReducer
// state; AIChatPanel.jsx supplies the state, live (via useChatStream) or
// reconstructed from history (via reduceTurnEvents).
export function ChatTimeline({ state }) {
  const { parts, calls, notices } = state
  if (parts.length === 0 && (!notices || notices.length === 0)) return null

  return (
    <div data-testid="chat-timeline">
      {parts.map((part, i) => {
        if (part.kind === 'text') {
          return part.text ? <ChatMarkdown key={`text-${part.partId}-${i}`} content={part.text} /> : null
        }
        const call = calls[part.callId]
        if (!call) return null
        return <ToolActivityCard key={`tool-${part.callId}`} call={call} />
      })}
      {(notices || []).map((notice, i) => (
        <NoticeBanner key={i} notice={notice} />
      ))}
    </div>
  )
}
