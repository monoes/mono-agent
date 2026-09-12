import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, Loader, Check, X, Copy } from 'lucide-react'

// Same self-contained tick pattern as TurnStatus.jsx's own live "idle for
// Ns" clock: own `now`, tick once a second only while `active`, stop
// entirely once the call completes so a finished card never re-renders on
// a timer it no longer needs.
function useTicker(active) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [active])
  return now
}

function formatDuration(ms) {
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  const totalSeconds = Math.round(ms / 1000)
  return `${Math.floor(totalSeconds / 60)}m ${totalSeconds % 60}s`
}

function formatArgs(args) {
  if (args === null || args === undefined) return null
  return typeof args === 'string' ? args : JSON.stringify(args, null, 2)
}

// Empty string / false / null are valid tool outputs, not "nothing arrived
// yet" — the plan is explicit about this ("Empty string, zero, false and
// null are valid results. Distinguish them from a result that has not
// arrived."). A completed call always has *some* result value (chatevents'
// ToolCompletedPayload.result is a plain, always-present string), so once
// status is "completed" this renders an explicit "(empty result)" for ''
// rather than rendering nothing, which would look identical to "not shown".
function formatResult(call) {
  if (call.status !== 'completed') return null
  if (call.result === '') return '(empty result)'
  return call.result
}

function copyToClipboard(text) {
  try { navigator.clipboard?.writeText(text) } catch { /* clipboard unavailable — copy is a convenience, not required */ }
}

// One accessible, expandable timeline step per tool call — identity is
// (turnId, callId) upstream, never array position or name, so two
// same-named parallel calls each get their own independent card. A real
// <button> header means Enter/Space activation is native; no custom key
// handling is reimplemented here.
//
// turnId scopes the controlled panel's DOM id: callId alone is only
// unique within one turn (the runtime numbers calls per-turn), and every
// past turn stays mounted simultaneously (no virtualization), so two
// tool-using turns would otherwise produce duplicate ids.
//
// isLive distinguishes "this call belongs to the turn currently
// streaming" from "this call belongs to a finalized turn being replayed."
// A call can be left at status:'started' forever (Stop mid-call, a
// crashed subprocess, an app restart) with no tool.completed ever
// arriving — without isLive, replaying that turn later would render a
// live-ticking "Running" clock counting up from its original startedAt
// indefinitely, for a call that will never run again. Defaults to true so
// existing callers that only ever render live turns are unaffected.
export function ToolActivityCard({ call, turnId = '', isLive = true }) {
  const failed = call.status === 'completed' && call.ok === false
  const orphaned = !isLive && call.status === 'started'
  // Never collapse a card the user would need to see: an error stays open
  // by default, everything else starts collapsed (plan: "errors remain
  // prominent... never collapse a card the user is actively inspecting").
  const [open, setOpen] = useState(failed)

  const argsText = formatArgs(call.arguments)
  const resultText = formatResult(call)
  const panelId = `tool-card-${turnId}-${call.callId}`

  const running = isLive && call.status === 'started'
  const now = useTicker(running)
  let durationText = null
  if (call.startedAt && call.finishedAt) {
    durationText = formatDuration(new Date(call.finishedAt).getTime() - new Date(call.startedAt).getTime())
  } else if (running && call.startedAt) {
    durationText = formatDuration(now - new Date(call.startedAt).getTime())
  }

  let statusIcon, statusText
  if (orphaned) {
    statusIcon = <X size={11} color="var(--text-muted)" />
    statusText = 'Interrupted'
  } else if (call.status === 'started') {
    statusIcon = <Loader size={11} className="chat-spin" style={{ color: '#00b4d8' }} />
    statusText = 'Running'
  } else if (failed) {
    statusIcon = <X size={11} color="#ef4444" />
    statusText = 'Failed'
  } else if (call.ok === true) {
    statusIcon = <Check size={11} color="#10b981" />
    statusText = 'Done'
  } else {
    // ok === null: some adapters never report explicit success/failure —
    // truthfully "Completed", not a fabricated pass/fail.
    statusIcon = <Check size={11} color="#94a3b8" />
    statusText = 'Completed'
  }

  return (
    <div style={{
      background: '#020509',
      border: `1px solid ${failed ? 'rgba(239,68,68,0.3)' : 'rgba(0,180,216,0.12)'}`,
      borderRadius: 8,
      marginTop: 6,
      overflow: 'hidden',
    }}>
      <button
        type="button"
        className="chat-tool-card-header"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 6,
          width: '100%', textAlign: 'left',
          background: 'transparent', border: 'none', cursor: 'pointer',
          padding: '6px 10px', font: 'inherit',
        }}
      >
        {open ? <ChevronDown size={10} color="#00b4d8" /> : <ChevronRight size={10} color="#00b4d8" />}
        {statusIcon}
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00b4d8', fontWeight: 600 }}>
          {call.name}
        </span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: failed ? '#fca5a5' : 'var(--text-muted)' }}>
          {statusText}
        </span>
        {durationText && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>
            {`· ${durationText}`}
          </span>
        )}
      </button>
      {/* Always mounted (visibility toggled via `hidden`, not conditional
          rendering) — aria-controls must resolve to a real element even
          while collapsed, which is the default state for every non-error
          card. */}
      {(argsText != null || resultText != null) && (
        <div id={panelId} hidden={!open} style={{ padding: '0 10px 8px', display: 'flex', flexDirection: 'column', gap: 6 }}>
          {argsText != null && (
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 3 }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 8, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase' }}>Args</span>
                <button type="button" onClick={() => copyToClipboard(argsText)} title="Copy arguments" aria-label="Copy arguments" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-muted)', padding: 0, display: 'flex' }}>
                  <Copy size={9} />
                </button>
              </div>
              <pre style={{
                margin: 0, fontFamily: 'var(--font-mono)', fontSize: 10,
                color: '#94a3b8', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                maxHeight: 120, overflow: 'auto',
              }}>
                {argsText}
              </pre>
            </div>
          )}
          {resultText != null && (
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 3 }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 8, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase' }}>Result</span>
                <button type="button" onClick={() => copyToClipboard(call.result)} title="Copy result" aria-label="Copy result" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-muted)', padding: 0, display: 'flex' }}>
                  <Copy size={9} />
                </button>
              </div>
              <pre style={{
                margin: 0, fontFamily: 'var(--font-mono)', fontSize: 10,
                color: failed ? '#fca5a5' : '#94a3b8', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                maxHeight: 120, overflow: 'auto',
              }}>
                {resultText}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
