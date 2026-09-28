import { useState } from 'react'
import {
  ChevronDown, ChevronRight, Loader, Check, X, Ban, Copy, Terminal, FilePen, FilePlus,
  FileText, Search, Globe, Bot, ListChecks, Plug, Wrench,
} from 'lucide-react'
import { useTicker, formatDuration, copyToClipboard } from './toolCardUtils.js'
import { diffLines, diffStats } from './unifiedDiff.js'

// Cards for the native Claude Code tools a coder turn reports (#202/#203):
// tool.started carries {name, arguments, native: true, parentCallId?} and
// tool.completed {ok, result, truncated?, durationMs?, denied?, cancelled?}.
// Each tool gets the shape that makes it readable at a glance — a Bash
// command, an Edit's diff, a Read's path — over one shared header with
// status, duration and the truncated/denied/cancelled markers.

const mono = 'var(--font-mono)'
const CYAN = '#00b4d8'
const preStyle = {
  margin: 0, fontFamily: mono, fontSize: 10, color: '#94a3b8',
  whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 220, overflow: 'auto',
}
const sectionLabel = { fontFamily: mono, fontSize: 8, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase' }

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
  if (call.cancelled) return { key: 'cancelled', icon: <Ban size={11} color="var(--text-muted)" />, text: 'Cancelled' }
  if (call.ok === false) return { key: 'failed', icon: <X size={11} color="#ef4444" />, text: 'Failed' }
  if (call.ok === true) return { key: 'done', icon: <Check size={11} color="#10b981" />, text: 'Done' }
  return { key: 'completed', icon: <Check size={11} color="#94a3b8" />, text: 'Completed' }
}

function Tag({ children, color = 'var(--text-muted)', testId }) {
  return (
    <span data-testid={testId} style={{
      fontFamily: mono, fontSize: 8.5, color, border: `1px solid ${color}55`, borderRadius: 4,
      padding: '0 4px', flexShrink: 0, letterSpacing: 0.5,
    }}>
      {children}
    </span>
  )
}

function Section({ label, copyText, children }) {
  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 3 }}>
        <span style={sectionLabel}>{label}</span>
        {copyText != null && (
          <button type="button" onClick={() => copyToClipboard(copyText)} title={`Copy ${label.toLowerCase()}`} aria-label={`Copy ${label.toLowerCase()}`}
            style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-muted)', padding: 0, display: 'flex' }}>
            <Copy size={9} />
          </button>
        )}
      </div>
      {children}
    </div>
  )
}

function ResultSection({ call, label = 'Output' }) {
  if (call.status !== 'completed') return null
  const text = call.result === '' ? '(no output)' : call.result
  return (
    <Section label={label} copyText={call.result}>
      <pre style={{ ...preStyle, color: call.ok === false ? '#fca5a5' : preStyle.color }}>{text}</pre>
    </Section>
  )
}

export function DiffView({ oldText, newText }) {
  const lines = diffLines(oldText, newText)
  return (
    <div data-testid="edit-diff" style={{ ...preStyle, whiteSpace: 'pre', background: '#01040a', borderRadius: 4, padding: '4px 0' }}>
      {lines.map((l, i) => (
        <div key={i} data-diff={l.type === ' ' ? 'context' : l.type === '+' ? 'add' : 'del'} style={{
          padding: '0 8px',
          color: l.type === '+' ? '#86efac' : l.type === '-' ? '#fca5a5' : '#64748b',
          background: l.type === '+' ? 'rgba(16,185,129,0.08)' : l.type === '-' ? 'rgba(239,68,68,0.08)' : 'transparent',
        }}>
          {`${l.type}${l.text}`}
        </div>
      ))}
    </div>
  )
}

// fileTags labels a Write/Edit by whether its file existed when the call
// started (tool.started's fileExisted, absent when unknown): "new file",
// else existingLabel ("overwrite" for Write, "modified" for Edit).
function fileTags(call, existingLabel) {
  if (call.fileExisted === false) return [<Tag key="nf" testId="file-state" color="#86efac">new file</Tag>]
  if (call.fileExisted === true) {
    return [<Tag key="ex" testId="file-state" color={existingLabel === 'overwrite' ? '#fbbf24' : '#94a3b8'}>{existingLabel}</Tag>]
  }
  return []
}

function editPairs(name, args) {
  if (name === 'MultiEdit') return Array.isArray(args.edits) ? args.edits : []
  return [{ old_string: args.old_string, new_string: args.new_string }]
}

// describe returns how one tool's card reads: header icon + title (+ an
// optional subtitle and tags), whether it starts open, and its body.
function describe(call, args, childCalls, renderChild) {
  const name = call.name || ''
  switch (name) {
    case 'Bash':
      return {
        icon: Terminal, title: `$ ${args.command ?? ''}`, titleMono: true, subtitle: args.description,
        tags: [
          ...(args.run_in_background ? [<Tag key="bg" color="#fbbf24">background</Tag>] : []),
          ...(typeof call.exitCode === 'number'
            ? [<Tag key="exit" testId="exit-code" color={call.exitCode === 0 ? '#94a3b8' : '#ef4444'}>{`exit ${call.exitCode}`}</Tag>]
            : []),
        ],
        body: <ResultSection call={call} />,
      }
    case 'Edit':
    case 'MultiEdit': {
      const pairs = editPairs(name, args)
      const stats = pairs.reduce((acc, p) => {
        const s = diffStats(diffLines(p.old_string, p.new_string))
        return { added: acc.added + s.added, removed: acc.removed + s.removed }
      }, { added: 0, removed: 0 })
      return {
        icon: FilePen, title: args.file_path, titleMono: true, defaultOpen: true,
        tags: [
          <Tag key="st" color="#94a3b8">{`+${stats.added} −${stats.removed}`}</Tag>,
          ...fileTags(call, 'modified'),
          ...(args.replace_all ? [<Tag key="ra">replace all</Tag>] : []),
        ],
        body: (
          <>
            {pairs.map((p, i) => <DiffView key={i} oldText={p.old_string} newText={p.new_string} />)}
            {call.ok === false && <ResultSection call={call} label="Error" />}
          </>
        ),
      }
    }
    case 'Write': {
      const content = args.content ?? ''
      const lineCount = content ? content.split('\n').length : 0
      return {
        icon: FilePlus, title: args.file_path, titleMono: true,
        tags: [
          <Tag key="lines" color="#94a3b8">{`${lineCount} line${lineCount === 1 ? '' : 's'}`}</Tag>,
          ...fileTags(call, 'overwrite'),
        ],
        body: (
          <>
            <Section label="Content" copyText={content}><pre style={preStyle}>{content}</pre></Section>
            {call.ok === false && <ResultSection call={call} label="Error" />}
          </>
        ),
      }
    }
    case 'Read': {
      const range = args.offset != null || args.limit != null
        ? ` (lines ${args.offset ?? 1}${args.limit != null ? `–${(args.offset ?? 1) + args.limit - 1}` : '+'})`
        : ''
      return { icon: FileText, compact: true, title: `Read ${args.file_path ?? ''}${range}`, body: <ResultSection call={call} /> }
    }
    case 'Glob':
    case 'Grep':
      return {
        icon: Search, compact: true,
        title: `${name} ${args.pattern ?? ''}${args.path ? ` in ${args.path}` : ''}`,
        body: <ResultSection call={call} label="Matches" />,
      }
    case 'WebFetch':
    case 'WebSearch':
      return {
        icon: Globe, title: name === 'WebFetch' ? args.url : args.query, titleMono: true, subtitle: name === 'WebFetch' ? args.prompt : undefined,
        tags: [<Tag key="ext" color="#fbbf24" testId="external-content">external content</Tag>],
        body: <ResultSection call={call} label="Result" />,
      }
    case 'Task':
    case 'Agent':
      return {
        icon: Bot, title: args.description || 'Subagent', subtitle: args.subagent_type,
        defaultOpen: true,
        tags: [<Tag key="n" color="#94a3b8">{`${childCalls.length} call${childCalls.length === 1 ? '' : 's'}`}</Tag>],
        body: (
          <>
            {args.prompt && (
              <details>
                <summary style={{ ...sectionLabel, cursor: 'pointer' }}>Prompt</summary>
                <pre style={preStyle}>{args.prompt}</pre>
              </details>
            )}
            {childCalls.length > 0 && (
              <div data-testid="subagent-calls" style={{ borderLeft: '2px solid rgba(0,180,216,0.25)', paddingLeft: 8 }}>
                {childCalls.map(c => renderChild(c))}
              </div>
            )}
            <ResultSection call={call} label="Result" />
          </>
        ),
      }
    case 'TodoWrite': {
      const todos = Array.isArray(args.todos) ? args.todos : []
      return {
        icon: ListChecks, compact: true, title: `Todos (${todos.filter(t => t.status === 'completed').length}/${todos.length} done)`,
        body: (
          <ul style={{ margin: 0, paddingLeft: 16, fontFamily: mono, fontSize: 10, color: '#94a3b8' }}>
            {todos.map((t, i) => (
              <li key={i} style={{ textDecoration: t.status === 'completed' ? 'line-through' : 'none', color: t.status === 'in_progress' ? CYAN : undefined }}>
                {t.content}
              </li>
            ))}
          </ul>
        ),
      }
    }
    default: {
      const mcp = name.startsWith('mcp__') ? name.split('__') : null
      return {
        icon: mcp ? Plug : Wrench, title: mcp ? `${mcp[1]} · ${mcp.slice(2).join('__')}` : name, titleMono: true,
        body: (
          <>
            {Object.keys(args).length > 0 && (
              <Section label="Args" copyText={JSON.stringify(args, null, 2)}><pre style={preStyle}>{JSON.stringify(args, null, 2)}</pre></Section>
            )}
            <ResultSection call={call} label="Result" />
          </>
        ),
      }
    }
  }
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
    <div data-testid="native-tool-card" data-tool={call.name} data-status={status.key} style={{
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
