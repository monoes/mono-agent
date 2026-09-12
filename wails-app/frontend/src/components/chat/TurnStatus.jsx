import { useEffect, useState } from 'react'
import { Loader, Square } from 'lucide-react'

// Idle-activity threshold (plan §"Honest progress"): "After 15 seconds
// without activity, show 'No new activity for 15s'; silence is not proof
// of a hang."
const IDLE_WARNING_MS = 15000

const TERMINAL_LABELS = {
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Stopped',
  interrupted: 'Interrupted',
}

// computeStatusLabel derives one of the plan's exact honest-progress
// strings from reducer state — Starting agent, Waiting for response,
// Running <tool>, Responding, Stopping, Completed, Failed, Stopped — never
// a fabricated percentage or invented reasoning. Pure and independent of
// wall-clock timers so it is directly unit-testable; `now` (epoch ms) is a
// parameter rather than Date.now() for the same reason. Returns
// { label, isIdleWarning } — the component below re-renders this on a
// ticking clock to keep isIdleWarning current.
export function computeStatusLabel(state, now) {
  const { terminal, stopRequested, startedAt, lastEventAt, parts, calls } = state
  if (terminal) {
    let label = TERMINAL_LABELS[terminal.status] || terminal.status
    if (terminal.status === 'completed' && terminal.reason && terminal.reason.startsWith('limit reached')) {
      label = 'Limit reached'
    }
    return { label, isIdleWarning: false }
  }
  if (stopRequested) return { label: 'Stopping', isIdleWarning: false }

  let label
  const runningTool = Object.values(calls || {}).find(c => c.status === 'started')
  if (runningTool) label = `Running ${runningTool.name}`
  else if ((parts || []).some(p => p.kind === 'text')) label = 'Responding'
  else if (!startedAt) label = 'Starting agent'
  else label = 'Waiting for response'

  const isIdleWarning = !!lastEventAt && (now - new Date(lastEventAt).getTime()) > IDLE_WARNING_MS
  return { label, isIdleWarning }
}

// TurnStatus renders the live label in a polite ARIA status region (plan
// §Accessibility: "announce meaningful state changes in a polite status
// region, not every token") plus the idle-activity notice, ticking once a
// second only while the turn is still active.
export function TurnStatus({ state, stopRequested }) {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (state.terminal) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [state.terminal])

  // computeStatusLabel reads stopRequested off the object it's given, not
  // as a separate argument — but chatReducer's own state shape (see
  // initialChatState()) has no such field; AIChatPanel.jsx tracks it as its
  // own local UI state and passes it down as this component's own prop.
  // Without merging it in here, state.stopRequested is always undefined in
  // the real app, and the "Stopping" label can never actually appear —
  // only the icon swap below would, silently.
  const { label, isIdleWarning } = computeStatusLabel({ ...state, stopRequested }, now)
  const isTerminal = !!state.terminal
  const isFailure = state.terminal && state.terminal.status !== 'completed'

  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: '4px 0',
        fontFamily: 'var(--font-mono)', fontSize: 10,
        color: isFailure ? '#fca5a5' : 'var(--text-muted)',
      }}
    >
      {!isTerminal && (stopRequested
        ? <Square size={11} color="#ef4444" />
        : <Loader size={11} className="chat-spin" style={{ color: '#00b4d8' }} />
      )}
      <span>{label}</span>
      {isIdleWarning && <span style={{ color: '#fbbf24' }}>· No new activity for 15s</span>}
    </div>
  )
}
