import { AlertTriangle, Info, AlertCircle, Loader, FolderOpen } from 'lucide-react'
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

function NoticeBanner({ notice, testId }) {
  const Icon = NOTICE_ICON[notice.severity] || Info
  const color = NOTICE_COLOR[notice.severity] || '#00b4d8'
  return (
    <div data-testid={testId} style={{ display: 'flex', alignItems: 'flex-start', gap: 6, padding: '6px 8px', marginTop: 6, borderRadius: 6, background: `${color}14`, border: `1px solid ${color}40` }}>
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
//
// turnId/isLive are passed straight through to each ToolActivityCard — see
// its own doc comment. Defaults (isLive=true) match the pre-existing
// behavior for callers that only ever render a still-streaming turn.
//
// Coder turns (#202) add two things. A call with a parentCallId was made
// inside that subagent call: it renders nested in the parent's card, not
// at the top level. And three notice codes get their own treatment:
// coder.status (startup progress) is a transient line shown only while the
// live turn has produced nothing yet, coder.workspace a compact folder
// line, and coder.background a warning banner.
export function ChatTimeline({ state, turnId = '', isLive = true }) {
  const { parts, calls } = state
  const notices = state.notices || []
  if (parts.length === 0 && notices.length === 0) return null

  const childrenOf = {}
  for (const part of parts) {
    const call = part.kind === 'tool' ? calls[part.callId] : null
    if (call?.parentCallId && calls[call.parentCallId]) {
      (childrenOf[call.parentCallId] ||= []).push(call)
    }
  }
  const renderCall = (call) => (
    <ToolActivityCard
      key={`tool-${call.callId}`}
      call={call}
      turnId={turnId}
      isLive={isLive}
      childCalls={childrenOf[call.callId] || []}
      renderChild={renderCall}
    />
  )

  const statusNotices = notices.filter(n => n.code === 'coder.status')
  const showStatus = isLive && !state.terminal && parts.length === 0 && statusNotices.length > 0

  return (
    <div data-testid="chat-timeline">
      {notices.filter(n => n.code === 'coder.workspace').slice(-1).map((notice, i) => (
        <div key={`ws-${i}`} data-testid="coder-workspace-line" style={{ display: 'flex', alignItems: 'center', gap: 5, marginTop: 4, fontFamily: 'var(--font-mono)', fontSize: 9.5, color: 'var(--text-muted)', wordBreak: 'break-all' }}>
          <FolderOpen size={10} style={{ flexShrink: 0 }} />
          {notice.message}
        </div>
      ))}
      {parts.map((part, i) => {
        if (part.kind === 'text') {
          return part.text ? <ChatMarkdown key={`text-${part.partId}-${i}`} content={part.text} /> : null
        }
        const call = calls[part.callId]
        if (!call || childrenOf[call.parentCallId]) return null
        return renderCall(call)
      })}
      {showStatus && (
        <div data-testid="coder-status-line" style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 6, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
          <Loader size={10} className="chat-spin" style={{ color: '#00b4d8' }} />
          {statusNotices[statusNotices.length - 1].message}
        </div>
      )}
      {notices.map((notice, i) => {
        if (notice.code === 'coder.status' || notice.code === 'coder.workspace') return null
        const background = notice.code === 'coder.background'
        return <NoticeBanner key={i} notice={background ? { ...notice, severity: 'warning' } : notice} testId={background ? 'coder-background-banner' : undefined} />
      })}
    </div>
  )
}
