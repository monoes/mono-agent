import { useState } from 'react'
import { ChevronDown, ChevronRight, Loader, Check, X, Ban, Square } from 'lucide-react'
import { useTicker, formatDuration } from './toolCardUtils.js'
import { Tag, Section, preStyle, mono, CYAN } from './nativeToolParts.jsx'
import { describe, kindOf } from './nativeToolDescribe.jsx'

export { DiffView } from './nativeToolParts.jsx'

// Cards for the native tools a coder turn reports (#202/#203), from any
// coding runtime: tool.started carries {name, kind?, arguments, native:
// true, parentCallId?} and tool.completed {ok, result, truncated?,
// durationMs?, denied?, cancelled?, exitCode?}. Each kind gets the shape
// that makes it readable at a glance — a shell command, an edit's diff, a
// patch's per-file diffs — over one shared header with status, duration
// and the truncated/denied/cancelled markers. nativeToolDescribe.jsx picks
// the shape.

// The CLI bounds each argument field; an input too large even for that
// arrives as one (truncated) JSON string instead of an object.
function parseArgs(args) {
  if (args && typeof args === 'object') return { args, raw: null }
  if (typeof args === 'string') {
    try {
      const v = JSON.parse(args)
      if (v && typeof v === 'object') return { args: v, raw: null }
    } catch { /* not JSON — shown raw */ }
    return { args: {}, raw: args }
  }
  return { args: {}, raw: null }
}

function callStatus(call, isLive) {
  if (call.status === 'started') {
    return isLive
      ? { key: 'running', icon: <Loader size={11} className="chat-spin" style={{ color: CYAN }} />, text: 'Running' }
      : { key: 'interrupted', icon: <X size={11} color="var(--text-muted)" />, text: 'Interrupted' }
  }
  if (call.denied) return { key: 'denied', icon: <Ban size={11} color="#f59e0b" />, text: 'Denied' }
  // A stopped turn closes its still-open calls as cancelled.
  if (call.cancelled) return { key: 'cancelled', icon: <Square size={10} color="var(--text-muted)" />, text: 'Stopped' }
  if (call.ok === false) return { key: 'failed', icon: <X size={11} color="#ef4444" />, text: 'Failed' }
  if (call.ok === true) return { key: 'done', icon: <Check size={11} color="#10b981" />, text: 'Done' }
  return { key: 'completed', icon: <Check size={11} color="#94a3b8" />, text: 'Completed' }
}

// NativeToolCard renders one native call. childCalls are the calls made
// inside it (a Task/Agent subagent), rendered through renderChild so they
// nest recursively; see ChatTimeline.
export function NativeToolCard({ call, turnId = '', isLive = true, childCalls = [], renderChild = () => null }) {
  const { args, raw } = parseArgs(call.arguments)
  const d = describe(call, args, childCalls, renderChild)
  const status = callStatus(call, isLive)
  const failed = status.key === 'failed'
  const [open, setOpen] = useState(!!d.defaultOpen || failed)
  const panelId = `tool-card-${turnId}-${call.callId}`

  const running = status.key === 'running'
  const now = useTicker(running)
  let durationText = null
  if (call.durationMs) durationText = call.durationMs < 1000 ? `${call.durationMs}ms` : formatDuration(call.durationMs)
  else if (call.startedAt && call.finishedAt) durationText = formatDuration(new Date(call.finishedAt) - new Date(call.startedAt))
  else if (running && call.startedAt) durationText = formatDuration(now - new Date(call.startedAt).getTime())

  const Icon = d.icon
  return (
    <div data-testid="native-tool-card" data-tool={call.name} data-kind={kindOf(call)} data-status={status.key} style={{
      background: '#020509',
      border: `1px solid ${failed ? 'rgba(239,68,68,0.3)' : status.key === 'denied' ? 'rgba(245,158,11,0.3)' : 'rgba(0,180,216,0.12)'}`,
      borderRadius: 8, marginTop: 6, overflow: 'hidden',
      // Fill the turn's width (a long command or path then ellipsizes)
      // instead of growing past the panel edge.
      alignSelf: 'stretch', minWidth: 0,
    }}>
      <button
        type="button"
        className="chat-tool-card-header"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'flex-start', gap: 6, width: '100%', textAlign: 'left',
          background: 'transparent', border: 'none', cursor: 'pointer', padding: d.compact ? '4px 10px' : '6px 10px', font: 'inherit',
        }}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 5, paddingTop: 1, flexShrink: 0 }}>
          {open ? <ChevronDown size={10} color={CYAN} /> : <ChevronRight size={10} color={CYAN} />}
          {status.icon}
          <Icon size={11} color={CYAN} />
        </span>
        {/* width 0 + flex 1: a long nowrap title must not raise the card's
            min-content width, or the turn grows past the panel edge. */}
        <span style={{ flex: 1, width: 0, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span style={{
            fontFamily: mono, fontSize: 10, color: d.titleMono ? '#e2e8f0' : CYAN, fontWeight: d.titleMono ? 500 : 600,
            whiteSpace: open && !d.compact ? 'pre-wrap' : 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', wordBreak: 'break-all',
          }}>
            {d.title}
          </span>
          {d.subtitle && <span style={{ fontFamily: 'var(--font-body)', fontSize: 10, color: 'var(--text-muted)' }}>{d.subtitle}</span>}
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 5, flexShrink: 0, paddingTop: 1 }}>
          {d.tags}
          {call.truncated && <Tag color="#fbbf24" testId="truncated">output truncated</Tag>}
          <span style={{ fontFamily: mono, fontSize: 9, color: failed ? '#fca5a5' : status.key === 'denied' ? '#fbbf24' : 'var(--text-muted)' }}>{status.text}</span>
          {durationText && <span style={{ fontFamily: mono, fontSize: 9, color: 'var(--text-muted)' }}>{`· ${durationText}`}</span>}
        </span>
      </button>
      <div id={panelId} hidden={!open} style={{ padding: '0 10px 8px', display: open ? 'flex' : 'none', flexDirection: 'column', gap: 6 }}>
        {raw != null && <Section label="Input (truncated)" copyText={raw}><pre style={preStyle}>{raw}</pre></Section>}
        {d.body}
      </div>
    </div>
  )
}
