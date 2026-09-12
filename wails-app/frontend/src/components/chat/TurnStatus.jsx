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
  const { terminal, stopRequested, startedAt, lastEventAt, parts, calls, ownedByThisInstance } = state
  if (terminal) {
    let label = TERMINAL_LABELS[terminal.status] || terminal.status
    if (terminal.status === 'completed' && terminal.reason && terminal.reason.startsWith('limit reached')) {
      label = 'Limit reached'
    }
    return { label, isIdleWarning: false }
  }
  // Cross-instance ownership (GetChatTurns' ownedByThisInstance — see
  // docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md,
  // "OwnerInstanceID is written and read back but never compared to
  // anything"): an active turn genuinely owned by a DIFFERENT live app
  // instance sharing this database is real and ongoing, but this instance
  // has no event stream for it — a spinner or ticking elapsed/idle time
  // here would imply liveness we cannot actually observe (plan §"Honest
  // progress": "Present observed activity ... Do not invent hidden
  // reasoning ... or successful results"). The terminal check just above
  // always wins over this one: if the event log already proves the turn
  // finished, that is more concrete truth than a turn-list snapshot taken
  // moments earlier. ownedByThisInstance is only ever `false` when
  // GetChatTurns says so explicitly — absent/undefined (older data, a
  // mock, or the backend half of this contract not deployed yet)
  // defensively falls through to normal handling below.
  if (ownedByThisInstance === false) return { label: 'Running in another window', isIdleWarning: false }
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
// ownedByThisInstance (default true — see this component's own doc comment
// on `stopRequested` just below for why a value like this is threaded in as
// a separate prop rather than living inside chatReducer's state) reflects
// GetChatTurns' cross-instance-ownership field. Defaulting to true means an
// absent/undefined value (older data, a mock, or the backend half of this
// contract not yet deployed) renders exactly like today — only an explicit
// `false` changes anything.
export function TurnStatus({ state, stopRequested, ownedByThisInstance = true }) {
  // "Active" is whatever chatReducer state itself considers active — no
  // terminal event yet observed for this turn.
  const isForeignActive = !state.terminal && ownedByThisInstance === false

  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    // Nothing to tick towards: a foreign-owned turn's displayed status can
    // only change by reloading the conversation (this instance has no live
    // event stream for someone else's turn) — a running clock here would
    // just churn re-renders for a label that never changes on its own.
    if (state.terminal || isForeignActive) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [state.terminal, isForeignActive])

  // computeStatusLabel reads stopRequested/ownedByThisInstance off the
  // object it's given, not as separate arguments — but chatReducer's own
  // state shape (see initialChatState()) has neither field; AIChatPanel.jsx
  // tracks stopRequested as its own local UI state, and ownedByThisInstance
  // comes from the raw GetChatTurns turn record, not from event replay.
  // Without merging them in here, both are always undefined in the real
  // app, and their respective labels could never actually appear — only
  // the icon swap below would, silently.
  const { label, isIdleWarning } = computeStatusLabel({ ...state, stopRequested, ownedByThisInstance }, now)
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
      {!isTerminal && !isForeignActive && (stopRequested
        ? <Square size={11} color="#ef4444" />
        : <Loader size={11} className="chat-spin" style={{ color: '#00b4d8' }} />
      )}
      <span>{label}</span>
      {isIdleWarning && <span style={{ color: '#fbbf24' }}>· No new activity for 15s</span>}
    </div>
  )
}
