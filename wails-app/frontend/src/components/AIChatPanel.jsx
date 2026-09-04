import { useState, useEffect, useRef, useCallback } from 'react'
import { X, Send, Trash2, ChevronDown, ChevronRight, Loader, Square, Plus, History } from 'lucide-react'
import { api, onAIChunk, onAITool, onAIError, onAgentSession, notify } from '../services/api.js'
import { cachedAgentScan } from '../lib/agentRuntimes.js'
import { getAssistantTools, getAssistantAllowRuns } from '../lib/assistantTools.js'

// Renders a chat message list into the panel's display shape (role/content/toolCalls),
// dropping internal tool-result rows and parsing tool_calls JSON — shared by the
// session-history loader and (previously) the flat-history loader.
function toDisplayMessages(history) {
  if (!Array.isArray(history)) return []
  return history
    .filter(m => m.role !== 'tool')
    .map(m => {
      let toolCalls = null
      if (m.tool_calls) {
        try {
          const parsed = JSON.parse(m.tool_calls)
          if (Array.isArray(parsed) && parsed.length > 0) {
            toolCalls = parsed.map(tc => ({
              tool: tc.function?.name || tc.id || 'unknown',
              args: tc.function?.arguments || '',
              result: null,
            }))
          }
        } catch { /* ignore malformed */ }
      }
      return { role: m.role, content: m.content || '', toolCalls }
    })
}

// Shared style for the three backend/runtime/provider <select>s in the
// selector row. Without `appearance: none`, WebKitGTK draws the closed box
// with native GTK combo-box chrome — light background, dark text — ignoring
// the inline background/color below entirely; the custom chevron replaces
// the native dropdown arrow that appearance:none also removes. Same SVG
// arrow index.css already uses for .filter-select/.form-select.
const selectStyle = {
  background: '#020509',
  border: '1px solid rgba(0,180,216,0.15)',
  borderRadius: 6,
  padding: '4px 20px 4px 8px',
  color: '#e2e8f0',
  fontFamily: 'var(--font-mono)', fontSize: 10,
  outline: 'none',
  appearance: 'none',
  backgroundImage: "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%2300b4d8' stroke-width='2'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E\")",
  backgroundRepeat: 'no-repeat',
  backgroundPosition: 'right 6px center',
}

// Relative time for the past-sessions list ("5m ago", "3d ago").
function relativeTime(iso) {
  if (!iso) return ''
  const diffMs = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diffMs / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

// ── Tool call card (collapsible) ───────────────────────────────────────────────
function ToolCallCard({ tool, args, result }) {
  const [open, setOpen] = useState(false)
  return (
    <div style={{
      background: '#020509',
      border: '1px solid rgba(0,180,216,0.12)',
      borderRadius: 8,
      marginTop: 6,
      overflow: 'hidden',
    }}>
      <div
        onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '6px 10px',
          cursor: 'pointer',
          userSelect: 'none',
        }}
      >
        {open ? <ChevronDown size={10} color="#00b4d8" /> : <ChevronRight size={10} color="#00b4d8" />}
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00b4d8', fontWeight: 600 }}>
          {tool}
        </span>
      </div>
      {open && (
        <div style={{ padding: '0 10px 8px', display: 'flex', flexDirection: 'column', gap: 6 }}>
          {args && (
            <div>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 8, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 3 }}>Args</div>
              <pre style={{
                margin: 0, fontFamily: 'var(--font-mono)', fontSize: 10,
                color: '#94a3b8', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                maxHeight: 120, overflow: 'auto',
              }}>
                {typeof args === 'string' ? args : JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}
          {result && (
            <div>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 8, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 3 }}>Result</div>
              <pre style={{
                margin: 0, fontFamily: 'var(--font-mono)', fontSize: 10,
                color: '#94a3b8', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                maxHeight: 120, overflow: 'auto',
              }}>
                {typeof result === 'string' ? result : JSON.stringify(result, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Message bubble ─────────────────────────────────────────────────────────────
function MessageBubble({ role, content, toolCalls, isError }) {
  const isUser = role === 'user'
  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: isUser ? 'flex-end' : 'flex-start',
      marginBottom: 8,
    }}>
      <div style={{
        maxWidth: '88%',
        padding: '8px 12px',
        borderRadius: isUser ? '12px 12px 4px 12px' : '12px 12px 12px 4px',
        background: isError
          ? 'rgba(239,68,68,0.1)'
          : isUser
            ? 'rgba(0,180,216,0.15)'
            : '#0d1a28',
        border: isError
          ? '1px solid rgba(239,68,68,0.25)'
          : isUser
            ? '1px solid rgba(0,180,216,0.25)'
            : '1px solid rgba(0,180,216,0.08)',
        color: isError ? '#fca5a5' : '#e2e8f0',
      }}>
        <div style={{
          fontFamily: 'var(--font-mono)', fontSize: 11,
          lineHeight: 1.55,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}>
          {content}
        </div>
        {toolCalls && toolCalls.length > 0 && (
          <div style={{ marginTop: 4 }}>
            {toolCalls.map((tc, i) => (
              <ToolCallCard key={i} tool={tc.tool} args={tc.args} result={tc.result} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// ── Main panel ─────────────────────────────────────────────────────────────────
export default function AIChatPanel({ workflowID, isOpen, onClose, onWorkflowCreated, initialRuntime, canvasMode = true }) {
  const [messages, setMessages]             = useState([])
  const [input, setInput]                   = useState('')
  const [streaming, setStreaming]           = useState(false)
  const [currentContent, setCurrentContent] = useState('')
  const [currentToolCalls, setCurrentToolCalls] = useState([])
  const [providers, setProviders]           = useState([])
  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedModel, setSelectedModel]   = useState('')
  const [runtimes, setRuntimes]             = useState([])
  const [selectedRuntime, setSelectedRuntime] = useState('')
  const [useAgents, setUseAgents]           = useState(false)
  const [monomindMissing, setMonomindMissing] = useState(false)
  // sessionId is the underlying agent runtime's resumable session id (from
  // monomind's agent:session event) — kept until the user starts a new
  // chat or changes runtime/model, so consecutive messages continue the
  // same real conversation instead of each being a stateless one-shot turn.
  // sessionRuntime is tracked alongside it: a session id is only ever valid
  // to resume under the runtime that created it (mixing them fails fast —
  // "No conversation found with session ID: ..." — rather than answering),
  // and selectedRuntime can drift from sessionRuntime via more than one
  // path (initialRuntime overriding a loaded session's own runtime, or the
  // runtime-scan mount effect racing the session-list mount effect), so
  // send() re-checks the pairing at the point of use rather than trusting
  // every sessionId setter to have kept them in sync.
  const [sessionId, setSessionId]           = useState('')
  const [sessionRuntime, setSessionRuntime] = useState('')
  const [pastSessions, setPastSessions]     = useState([])
  const [showSessions, setShowSessions]     = useState(false)

  const messagesEndRef   = useRef(null)
  const textareaRef      = useRef(null)
  const createdWfIdRef   = useRef(null)
  const modelAtFocusRef  = useRef('')
  // The global panel instance mounts hidden at app boot and every instance
  // stays mounted under keep-alive navigation — expensive loads below key
  // off these latches so a never-opened panel costs (almost) nothing:
  // hasOpenedRef latches on the first isOpen false→true transition,
  // hasScannedRef/providersLoadedRef make the runtime scan and provider
  // list one-shot per panel lifetime (FV4-3/4).
  const hasOpenedRef      = useRef(false)
  const hasScannedRef     = useRef(false)
  const providersLoadedRef = useRef(false)
  // Latest-value mirrors for guards inside callbacks/effects that must not
  // re-run (or go stale) when the underlying state changes:
  // streamingRef — mid-stream switch guards (FV4-6); activeStreamRef — the
  // { workflowID, mode } of the in-flight stream, so a workflowID change can
  // stop the right bucket with the right stop call (FV4-7).
  const streamingRef  = useRef(false)
  streamingRef.current = streaming
  const activeStreamRef = useRef(null)

  // ── Latch the first open: isOpen's first false→true transition ─────────
  useEffect(() => {
    if (isOpen) hasOpenedRef.current = true
  }, [isOpen])

  // ── First open: scan local agent runtimes (monomind delegation) ────────
  // Deferred until first open because `agent scan` spawns monomind and
  // probes every known agent CLI (~6-7s) — a cost the app used to pay on
  // boot for the hidden global panel. Served from the shared TTL cache, so
  // e.g. an Agents-page "Chat" click right after that page scanned
  // reconciles instantly instead of rescanning.
  useEffect(() => {
    if (!isOpen || hasScannedRef.current) return
    hasScannedRef.current = true
    cachedAgentScan().then(res => {
      if (!res || res.error) { setMonomindMissing(true); return }
      const installed = (res.agents || []).filter(a => a.installed)
      setRuntimes(installed)
      if (installed.length > 0) {
        const preferred = initialRuntime && installed.some(a => a.id === initialRuntime)
          ? initialRuntime
          : installed[0].id
        setSelectedRuntime(preferred)
        setUseAgents(true) // prefer local agents when any is installed
      }
    })
    // initialRuntime intentionally excluded: changes to it are reconciled
    // against the already-loaded runtime list by the effect below — no rescan.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen])

  // ── Agents-page "Chat" click targets a specific runtime: apply it
  //     against the cached runtime list without rescanning ────────────────
  useEffect(() => {
    if (!initialRuntime) return
    if (runtimes.some(r => r.id === initialRuntime)) {
      setSelectedRuntime(initialRuntime)
      setUseAgents(true)
    }
  }, [initialRuntime, runtimes])

  // ── Load providers on first open ─────────────────────────────────────────
  useEffect(() => {
    if (!isOpen || providersLoadedRef.current) return
    providersLoadedRef.current = true
    api.listAIProviders().then(list => {
      const active = (list || []).filter(p => p.status === 'active')
      setProviders(active)
      if (active.length > 0 && !selectedProvider) {
        setSelectedProvider(String(active[0].id))
        setSelectedModel(active[0].default_model || '')
      }
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen])

  // ── Load a specific past session into the panel ─────────────────────────
  const loadSession = useCallback((session) => {
    if (!workflowID || !session?.session_id) return
    // Switching the active session mid-stream would let chunks of the
    // in-flight answer land in the newly loaded transcript (crosstalk) —
    // block the switch and tell the user to stop first (FV4-6).
    if (streamingRef.current) {
      notify('chat', 'Stop the current response first')
      return
    }
    api.getChatSessionMessages(workflowID, session.session_id).then(history => {
      setMessages(toDisplayMessages(history))
      // initialRuntime (e.g. the Agents-page "Chat" button picking a specific
      // runtime) wins over a past session's own runtime; otherwise resume
      // with whatever the session was actually using. Either way, sessionId
      // and sessionRuntime are set together so send() can verify the pairing
      // still matches selectedRuntime before ever passing --resume — a
      // session id is only valid to resume under the exact runtime that
      // created it (mismatched, it fails fast: "No conversation found").
      if (session.runtime && !initialRuntime) setSelectedRuntime(session.runtime)
      if (session.model) setSelectedModel(session.model)
      setSessionId(session.session_id)
      setSessionRuntime(session.runtime || '')
      setCurrentContent('')
      setCurrentToolCalls([])
      setShowSessions(false)
    })
  }, [workflowID, initialRuntime])

  // ── Start a fresh chat: clears the visible transcript and active session,
  //     but leaves prior sessions in the history — they stay reachable via
  //     the past-sessions list. The next send() omits --resume, so the
  //     runtime allocates a brand new session. Blocked while a response is
  //     streaming (FV4-6) for the same crosstalk reason as loadSession. ────
  const startNewSession = useCallback(() => {
    if (streamingRef.current) {
      notify('chat', 'Stop the current response first')
      return
    }
    setSessionId('')
    setSessionRuntime('')
    setMessages([])
    setCurrentContent('')
    setCurrentToolCalls([])
  }, [])

  // ── Load past sessions + auto-continue the most recent one when the
  //     panel switches to a new workflowID (chat-history bucket) ──────────
  const sessionsFetchedRef = useRef(null) // workflowID bucket last fetched for
  useEffect(() => {
    if (!workflowID) return
    // Deferred until the panel has been opened at least once — a
    // mounted-but-never-opened panel (the global assistant at app boot)
    // skips the history fetch (FV4-3/4). After that, only an actual
    // workflowID change refetches: close/reopen transitions must not
    // rebind the active session out from under the user.
    if ((!isOpen && !hasOpenedRef.current) || sessionsFetchedRef.current === workflowID) return
    sessionsFetchedRef.current = workflowID
    api.listChatSessions(workflowID).then(sessions => {
      const list = Array.isArray(sessions) ? sessions : []
      setPastSessions(list)
      if (list.length > 0) loadSession(list[0])
    })
    // Intentionally excludes loadSession: this effect should only run when
    // the panel's workflowID (chat-history bucket) actually changes, not
    // every time loadSession's own deps (e.g. initialRuntime) change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowID, isOpen])

  // ── Canvas re-key / workflow switch must not orphan an in-flight stream ──
  // This panel's event filters key on workflowID, so chunks for the OLD
  // bucket would arrive into nothing — the stream would keep running with
  // no visible output and no stop button. Stop the old bucket (with the
  // stop call matching the mode that started it) and reset local stream
  // state so the new bucket starts clean (FV4-7).
  const prevWorkflowIDRef = useRef(workflowID)
  useEffect(() => {
    const prev = prevWorkflowIDRef.current
    prevWorkflowIDRef.current = workflowID
    if (!prev || prev === workflowID) return
    const active = activeStreamRef.current
    if (streamingRef.current && active?.workflowID === prev) {
      if (active.mode === 'agents') api.stopAgentChat(prev)
      else api.stopAIChat(prev)
      activeStreamRef.current = null
      setStreaming(false)
      setCurrentContent('')
      setCurrentToolCalls([])
    }
  }, [workflowID])

  // ── Subscribe to streaming events ───────────────────────────────────────
  useEffect(() => {
    const offChunk = onAIChunk((data) => {
      if (data.workflowID !== workflowID) return
      if (data.done) {
        // Streaming finished — finalize the assistant message (guard against double-fire)
        setStreaming(prev => {
          if (!prev) return false // already finalized
          setCurrentContent(content => {
            const final = content + (data.content || '')
            if (!final) return '' // nothing to add
            setCurrentToolCalls(prevTC => {
              setMessages(msgs => [
                ...msgs,
                { role: 'assistant', content: final, toolCalls: prevTC.length > 0 ? prevTC : null },
              ])
              return []
            })
            return ''
          })
          // Navigate to newly created workflow after all tool calls are done
          if (createdWfIdRef.current && onWorkflowCreated) {
            const id = createdWfIdRef.current
            createdWfIdRef.current = null
            // Small delay to let final DB writes settle
            setTimeout(() => onWorkflowCreated(id), 300)
          }
          return false
        })
      } else {
        setCurrentContent(prev => prev + (data.content || ''))
      }
    })

    const offTool = onAITool((data) => {
      if (data.workflowID !== workflowID) return
      setCurrentToolCalls(prev => [
        ...prev,
        { tool: data.tool, args: data.args, result: data.result },
      ])
      // Track newly created workflow ID for auto-navigate after stream completes
      if (data.tool === 'create_workflow' && data.result) {
        try {
          const res = JSON.parse(data.result)
          if (res.workflow_id) createdWfIdRef.current = res.workflow_id
        } catch { /* ignore */ }
      }
    })

    const offError = onAIError((data) => {
      if (data.workflowID !== workflowID) return
      setStreaming(false)
      setCurrentContent('')
      setCurrentToolCalls([])
      setMessages(msgs => [
        ...msgs,
        { role: 'error', content: data.error || 'Unknown error' },
      ])
    })

    // Captures the resumable session id monomind assigns/confirms each
    // turn — the next send() reuses it as --resume until the user resets.
    // data.runtime is the runtime that actually produced this session
    // (app_ai.go's StreamAgentChat emits its own agentRuntime param here,
    // not whatever selectedRuntime happens to be at listener-fire time).
    const offSession = onAgentSession((data) => {
      if (data.workflowID !== workflowID) return
      if (data.session_id) {
        setSessionId(data.session_id)
        setSessionRuntime(data.runtime || '')
      }
    })

    return () => { offChunk(); offTool(); offError(); offSession() }
  }, [workflowID])

  // ── Auto-scroll to bottom ───────────────────────────────────────────────
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, currentContent, currentToolCalls])

  // ── Send message ────────────────────────────────────────────────────────
  const send = useCallback(async () => {
    const text = input.trim()
    if (!text || streaming || !workflowID) return
    if (useAgents && !selectedRuntime) return
    if (!useAgents && !selectedProvider) return

    setMessages(msgs => [...msgs, { role: 'user', content: text }])
    setInput('')
    setStreaming(true)
    setCurrentContent('')
    setCurrentToolCalls([])

    try {
      if (useAgents) {
        // A session id is only valid to resume under the exact runtime that
        // created it — mismatched, monomind fails fast ("No conversation
        // found with session ID: ..."). sessionRuntime can drift out of
        // sync with selectedRuntime (initialRuntime overriding a loaded
        // past session's runtime, or a mount-order race between the
        // runtime-scan and session-list effects), so re-verify here rather
        // than trusting every earlier sessionId setter to have kept them
        // paired — silently starting fresh beats erroring out.
        const resumeID = sessionRuntime === selectedRuntime ? sessionId : ''
        // Remember which bucket + mode owns this stream so a workflowID
        // change (canvas re-key) can stop the right one (FV4-7).
        activeStreamRef.current = { workflowID, mode: 'agents' }
        // Tool flags (monoagentTools/allowRuns) are resolved from the
        // persisted Assistant tool access settings inside api.js at call
        // time — always current, never a stale closure.
        await api.streamAgentChat(workflowID, text, selectedRuntime, selectedModel, resumeID, canvasMode)
        // Refresh the past-sessions list now that this turn has been
        // persisted (new session, or another message added to the active one).
        api.listChatSessions(workflowID).then(list => setPastSessions(Array.isArray(list) ? list : []))
      } else {
        activeStreamRef.current = { workflowID, mode: 'providers' }
        await api.streamAIChat(workflowID, text, selectedProvider, selectedModel)
      }
    } catch (err) {
      setStreaming(false)
      setMessages(msgs => [
        ...msgs,
        { role: 'error', content: String(err) },
      ])
    }
  }, [input, streaming, workflowID, useAgents, selectedRuntime, selectedProvider, selectedModel, canvasMode, sessionId, sessionRuntime])

  // Whether a backend is actually selected for the current mode — gates the
  // input, matching send()'s own guard (useAgents ? selectedRuntime : selectedProvider).
  const hasBackend = useAgents ? !!selectedRuntime : !!selectedProvider

  // Assistant tool access (Settings → "Assistant tool access", GX2 contract):
  // read per render so toggling it there applies here without a remount.
  const assistantToolsOn   = getAssistantTools()
  const assistantAllowRuns = getAssistantAllowRuns()

  // ── Stop an in-flight stream ──────────────────────────────────────────────
  const stop = useCallback(async () => {
    if (!workflowID) return
    if (useAgents) {
      await api.stopAgentChat(workflowID)
    } else {
      await api.stopAIChat(workflowID)
    }
    setStreaming(false)
  }, [workflowID, useAgents])

  // ── Clear history ───────────────────────────────────────────────────────
  const clearHistory = useCallback(async () => {
    if (!workflowID) return
    await api.clearAIChatHistory(workflowID)
    setMessages([])
    setCurrentContent('')
    setCurrentToolCalls([])
  }, [workflowID])

  // ── Handle key down in textarea ─────────────────────────────────────────
  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  // ── Provider change ─────────────────────────────────────────────────────
  const handleProviderChange = (e) => {
    const id = e.target.value
    setSelectedProvider(id)
    const p = providers.find(p => String(p.id) === id)
    if (p) setSelectedModel(p.default_model || '')
  }

  if (!isOpen) return null

  return (
    <div style={{
      width: 380, flexShrink: 0,
      background: '#060b13',
      borderLeft: '1px solid rgba(0,180,216,0.1)',
      display: 'flex', flexDirection: 'column',
      overflow: 'hidden',
    }}>
      {/* ── Header ── */}
      <div style={{
        padding: '10px 12px',
        borderBottom: '1px solid rgba(0,180,216,0.1)',
        display: 'flex', alignItems: 'center', gap: 8,
        flexShrink: 0,
        position: 'relative',
      }}>
        <span style={{
          fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 700,
          color: '#e2e8f0', flex: 1, letterSpacing: 1,
        }}>
          AI ASSISTANT
        </span>
        {assistantToolsOn && (
          <span
            title={`monoagent tools enabled${assistantAllowRuns ? ' — including running workflows/actions from chat' : ''}`}
            style={{
              fontFamily: 'var(--font-mono)', fontSize: 8.5, fontWeight: 700, letterSpacing: 1,
              color: '#00b4d8', background: 'rgba(0,180,216,0.08)',
              border: '1px solid rgba(0,180,216,0.35)',
              borderRadius: 4, padding: '2px 5px', flexShrink: 0,
            }}
          >
            TOOLS{assistantAllowRuns ? '+RUN' : ''}
          </span>
        )}
        <button
          onClick={startNewSession}
          title="New chat"
          style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-muted)', padding: 2, display: 'flex', alignItems: 'center',
            transition: 'color 100ms',
          }}
          onMouseEnter={e => e.currentTarget.style.color = '#00b4d8'}
          onMouseLeave={e => e.currentTarget.style.color = 'var(--text-muted)'}
        >
          <Plus size={13} />
        </button>
        <button
          onClick={() => setShowSessions(s => !s)}
          title="Past sessions"
          style={{
            background: showSessions ? 'rgba(0,180,216,0.12)' : 'transparent',
            border: 'none', borderRadius: 4, cursor: 'pointer',
            color: showSessions ? '#00b4d8' : 'var(--text-muted)', padding: 2, display: 'flex', alignItems: 'center',
            transition: 'color 100ms',
          }}
          onMouseEnter={e => e.currentTarget.style.color = '#00b4d8'}
          onMouseLeave={e => e.currentTarget.style.color = showSessions ? '#00b4d8' : 'var(--text-muted)'}
        >
          <History size={12} />
        </button>
        {showSessions && (
          <div style={{
            position: 'absolute', top: '100%', right: 8, marginTop: 4,
            width: 280, maxHeight: 260, overflowY: 'auto',
            background: '#0a1018', border: '1px solid rgba(0,180,216,0.2)',
            borderRadius: 8, boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
            zIndex: 20, padding: 4,
          }}>
            {pastSessions.length === 0 ? (
              <div style={{ padding: 10, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                No past sessions yet
              </div>
            ) : (
              pastSessions.map(s => (
                <div
                  key={s.session_id}
                  onClick={() => loadSession(s)}
                  style={{
                    padding: '7px 9px', borderRadius: 6, cursor: 'pointer',
                    background: s.session_id === sessionId ? 'rgba(0,180,216,0.1)' : 'transparent',
                  }}
                  onMouseEnter={e => { if (s.session_id !== sessionId) e.currentTarget.style.background = 'rgba(255,255,255,0.04)' }}
                  onMouseLeave={e => { if (s.session_id !== sessionId) e.currentTarget.style.background = 'transparent' }}
                >
                  <div style={{
                    fontFamily: 'var(--font-mono)', fontSize: 10.5, color: '#e2e8f0',
                    whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                  }}>
                    {s.preview || '(no preview)'}
                  </div>
                  <div style={{ display: 'flex', gap: 6, marginTop: 2 }}>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: '#00b4d8' }}>{s.runtime}</span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>{relativeTime(s.updated_at)}</span>
                  </div>
                </div>
              ))
            )}
          </div>
        )}
        <button
          onClick={clearHistory}
          title="Clear chat history"
          style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-muted)', padding: 2, display: 'flex', alignItems: 'center',
            transition: 'color 100ms',
          }}
          onMouseEnter={e => e.currentTarget.style.color = '#ef4444'}
          onMouseLeave={e => e.currentTarget.style.color = 'var(--text-muted)'}
        >
          <Trash2 size={12} />
        </button>
        <button
          onClick={onClose}
          title="Close panel"
          style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-muted)', padding: 2, display: 'flex', alignItems: 'center',
            transition: 'color 100ms',
          }}
          onMouseEnter={e => e.currentTarget.style.color = '#fff'}
          onMouseLeave={e => e.currentTarget.style.color = 'var(--text-muted)'}
        >
          <X size={13} />
        </button>
      </div>

      {/* ── Runtime / Provider / Model selectors ── */}
      <div style={{
        padding: '8px 12px',
        borderBottom: '1px solid rgba(0,180,216,0.06)',
        display: 'flex', gap: 6,
        flexShrink: 0,
      }}>
        {/* Only a real choice when both a local runtime and a configured
            provider exist — one-option dropdowns are noise, not a control. */}
        {(runtimes.length > 0 && providers.length > 0) && (
          <select
            value={useAgents ? 'agents' : 'providers'}
            onChange={e => setUseAgents(e.target.value === 'agents')}
            title="Chat backend"
            style={selectStyle}
          >
            {runtimes.length > 0 && <option value="agents">agents</option>}
            {providers.length > 0 && <option value="providers">providers</option>}
          </select>
        )}
        {useAgents ? (
          <select
            value={selectedRuntime}
            onChange={e => {
              // Runtime change resets the session — blocked mid-stream for
              // the same crosstalk reason as loadSession (FV4-6).
              if (streamingRef.current) {
                notify('chat', 'Stop the current response first')
                return
              }
              setSelectedRuntime(e.target.value)
              startNewSession()
            }}
            title="Locally installed AI agent (via monomind)"
            style={{ ...selectStyle, flex: 1 }}
          >
            {runtimes.length === 0 && (
              <option value="">
                {monomindMissing ? 'monomind missing — npm i -g @monoes/monomindcli' : 'No agent runtimes'}
              </option>
            )}
            {runtimes.map(r => (
              <option key={r.id} value={r.id}>{r.id}</option>
            ))}
          </select>
        ) : (
          <select
            value={selectedProvider}
            onChange={handleProviderChange}
            style={{ ...selectStyle, flex: 1 }}
          >
            {providers.length === 0 && <option value="">No providers</option>}
            {providers.map(p => (
              <option key={p.id} value={String(p.id)}>{p.name}</option>
            ))}
          </select>
        )}
        <input
          type="text"
          value={selectedModel}
          onChange={e => setSelectedModel(e.target.value)}
          onFocus={e => { modelAtFocusRef.current = e.target.value }}
          onBlur={e => { if (e.target.value !== modelAtFocusRef.current) startNewSession() }}
          placeholder="Model"
          style={{
            flex: 1,
            background: '#020509',
            border: '1px solid rgba(0,180,216,0.15)',
            borderRadius: 6,
            padding: '4px 8px',
            color: '#e2e8f0',
            fontFamily: 'var(--font-mono)', fontSize: 10,
            outline: 'none',
          }}
        />
      </div>

      {/* ── Messages area ── */}
      <div style={{
        flex: 1,
        overflowY: 'auto',
        padding: '12px',
        display: 'flex', flexDirection: 'column',
      }}>
        {messages.length === 0 && !streaming && (
          <div style={{
            flex: 1,
            display: 'flex', flexDirection: 'column',
            alignItems: 'center', justifyContent: 'center', gap: 8,
            color: 'var(--text-muted)',
          }}>
            <span style={{ fontSize: 24, opacity: 0.15 }}>AI</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, textAlign: 'center', lineHeight: 1.6 }}>
              {workflowID === 'general'
                ? <>Chat with your connected AI providers.<br />Ask anything.</>
                : <>Ask the AI about your workflow,<br />request changes, or get help.</>
              }
            </span>
          </div>
        )}

        {messages.map((msg, i) => (
          <MessageBubble
            key={i}
            role={msg.role}
            content={msg.content}
            toolCalls={msg.toolCalls}
            isError={msg.role === 'error'}
          />
        ))}

        {/* Streaming assistant bubble */}
        {streaming && (currentContent || currentToolCalls.length > 0) && (
          <MessageBubble
            role="assistant"
            content={currentContent || '...'}
            toolCalls={currentToolCalls.length > 0 ? currentToolCalls : null}
          />
        )}

        {/* Streaming indicator when no content yet */}
        {streaming && !currentContent && currentToolCalls.length === 0 && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 0',
          }}>
            <Loader size={12} style={{ animation: 'spin 0.7s linear infinite', color: '#00b4d8' }} />
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
              Thinking...
            </span>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {/* ── Input area ── */}
      <div style={{
        padding: '8px 12px 10px',
        borderTop: '1px solid rgba(0,180,216,0.1)',
        display: 'flex', flexDirection: 'column', gap: 6,
        flexShrink: 0,
      }}>
        {!hasBackend && (
          <div style={{ padding: '8px 12px', background: 'rgba(251,191,36,.08)', border: '1px solid rgba(251,191,36,.2)', borderRadius: 'var(--radius)', fontFamily: 'var(--font-mono)', fontSize: 10, color: '#fbbf24' }}>
            {useAgents ? 'Select an agent runtime above to start chatting' : 'Select an AI provider above to start chatting'}
          </div>
        )}
        <div style={{ display: 'flex', gap: 6 }}>
        <textarea
          ref={textareaRef}
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Type a message..."
          rows={1}
          style={{
            flex: 1,
            background: '#020509',
            border: '1px solid rgba(0,180,216,0.15)',
            borderRadius: 8,
            padding: '8px 10px',
            color: '#e2e8f0',
            fontFamily: 'var(--font-mono)', fontSize: 11,
            outline: 'none',
            resize: 'none',
            minHeight: 36,
            maxHeight: 120,
            lineHeight: 1.4,
          }}
          onInput={e => {
            e.target.style.height = 'auto'
            e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px'
          }}
        />
        {streaming ? (
          <button
            onClick={stop}
            title="Stop generating"
            aria-label="Stop generating"
            style={{
              background: 'rgba(239,68,68,0.15)',
              border: '1px solid rgba(239,68,68,0.35)',
              borderRadius: 8,
              padding: '0 12px',
              cursor: 'pointer',
              color: '#ef4444',
              display: 'flex', alignItems: 'center',
              transition: 'all 100ms',
              flexShrink: 0,
            }}
            onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.25)' }}
            onMouseLeave={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.15)' }}
          >
            <Square size={12} fill="#ef4444" />
          </button>
        ) : (
          <button
            onClick={send}
            disabled={!input.trim() || !hasBackend}
            title={!hasBackend ? (useAgents ? 'No runtime selected' : 'No provider selected') : 'Send message'}
            aria-label="Send message"
            style={{
              background: !input.trim() || !hasBackend ? 'rgba(0,180,216,0.05)' : 'rgba(0,180,216,0.15)',
              border: `1px solid ${!input.trim() || !hasBackend ? 'rgba(0,180,216,0.08)' : 'rgba(0,180,216,0.3)'}`,
              borderRadius: 8,
              padding: '0 12px',
              cursor: !input.trim() || !hasBackend ? 'default' : 'pointer',
              color: !input.trim() || !hasBackend ? 'var(--text-muted)' : '#00b4d8',
              display: 'flex', alignItems: 'center',
              transition: 'all 100ms',
              flexShrink: 0,
            }}
            onMouseEnter={e => { if (input.trim() && hasBackend) e.currentTarget.style.background = 'rgba(0,180,216,0.25)' }}
            onMouseLeave={e => { if (input.trim() && hasBackend) e.currentTarget.style.background = 'rgba(0,180,216,0.15)' }}
          >
            <Send size={13} />
          </button>
        )}
        </div>
      </div>
    </div>
  )
}
