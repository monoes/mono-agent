import { useState, useEffect, useRef, useCallback } from 'react'
import { X, Trash2, Plus, History, Maximize2, Minimize2, ArrowDown } from 'lucide-react'
import { api, notify } from '../services/api.js'
import { useChatStream } from './chat/useChatStream.js'
import { chatReducer, initialChatState } from './chat/chatReducer.js'
import { ChatTimeline } from './chat/ChatTimeline.jsx'
import { ChatMarkdown } from './chat/ChatMarkdown.jsx'
import { TurnStatus } from './chat/TurnStatus.jsx'
import { ChatComposer } from './chat/ChatComposer.jsx'
import { useChatScroll } from './chat/useChatScroll.js'
import { ChatArtifactCard } from './chat/ChatArtifactCard.jsx'
import { detectArtifactCandidate, resolveArtifact } from './chat/chatArtifacts.js'
import './chat/chat.css'
import { cachedAgentScan } from '../lib/agentRuntimes.js'
import { getAssistantTools, getAssistantAllowRuns } from '../lib/assistantTools.js'

// Replays one turn's already-fetched events through chatReducer to
// reconstruct its final state — used for history (a past turn's events,
// fetched once) rather than live streaming (useChatStream.js owns that).
// No scope is set: the caller already fetched precisely one turn's events
// via getChatEvents(conversationId, turnId, ...), so every event
// necessarily belongs here. The result is fed straight into ChatTimeline/
// TurnStatus — the same components a live turn uses — so a reopened past
// turn renders identically to how it looked while it was still running.
function reduceTurnEvents(events) {
  return events.reduce((state, event) => chatReducer(state, { type: 'event', event }), initialChatState())
}

// Fetches one turn's full event backlog (paginating past a single page,
// same as useChatStream's own hydration) and returns its reduced state, or
// null for a turn that produced nothing at all and never reached a
// terminal status (defensively skipped rather than shown as a blank turn).
async function loadTurnState(conversationId, turn) {
  let afterSeq = 0
  let events = []
  for (;;) {
    const page = await api.getChatEvents(conversationId, turn.id, afterSeq, 200)
    const items = Array.isArray(page?.items) ? page.items : []
    events = events.concat(items)
    if (items.length === 0 || !page?.hasMore) break
    afterSeq = items[items.length - 1].seq
  }
  const state = reduceTurnEvents(events)
  if (state.parts.length === 0 && turn.status === 'active') return null
  return state
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

// Client-generated turn id (plan: "Client-created turn ID: registered
// locally before Start"). crypto.randomUUID() is gated on a secure
// context — Wails' custom-scheme webview origin isn't guaranteed to
// qualify on every platform the way http://wails.localhost does on
// Windows, so relying on it directly would make every send() throw on
// whichever platforms don't. crypto.getRandomValues has no such
// restriction, so build an RFC 4122 v4 string from it whenever
// randomUUID isn't available instead of failing outright. Exported for
// direct testing (jsdom/Node's crypto always has randomUUID, so the
// fallback path needs to be exercised explicitly).
export function newTurnId() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = [...bytes].map(b => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

// Relative time for the past-conversations list ("5m ago", "3d ago").
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

// ── Message bubble ─────────────────────────────────────────────────────────────
// Exported (like FileViewerModal's fileViewerKind) so the markdown-vs-plain
// rendering choice is directly unit-testable without driving the whole
// panel's streaming/session machinery. Reachable today only for 'user' and
// 'error' roles — the reducer/ChatTimeline path (role:'turn') fully
// replaced the old assistant-bubble-plus-inline-tool-cards rendering this
// component used to also handle; a prior version's now-dead `toolCalls`
// prop and its ToolCallCard were removed for exactly that reason (nothing
// ever constructed a message carrying one).
export function MessageBubble({ role, content, isError }) {
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
        {isUser ? (
          // Raw pre-wrap text — never reinterpreted as markdown syntax the
          // user didn't intend (e.g. a typed "1. foo" becoming a list).
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.55, wordBreak: 'break-word', whiteSpace: 'pre-wrap' }}>
            {content}
          </div>
        ) : (
          // Error text renders through the same ChatMarkdown ChatTimeline
          // uses for assistant text — scheme-gated links, no raw HTML, no
          // auto-loaded images — rather than a second, less-careful
          // markdown config (an error string ultimately traces back to a
          // subprocess/provider failure message, not first-party copy).
          <ChatMarkdown content={content} />
        )}
      </div>
    </div>
  )
}

// Resolves chat-result artifacts (chat/chatArtifacts.js) for every tool
// call this panel has ever rendered — finalized turns in `messages` plus
// the live streaming turn — lazily and once per (turnId, callId) pair.
// Keyed on the PAIR, not the bare callId: the agent backend's callId comes
// straight from the external monomind protocol's own per-event id with no
// cross-turn uniqueness guarantee (plan: "Tool identity is (turnId,callId),
// never array position or name" — a requirement that only makes sense if
// callId alone CAN collide across turns). Caching by callId alone would
// let a later turn that happens to reuse an earlier turn's callId silently
// reuse its resolved artifact — wrong name, wrong Copy-ID value, wrong
// Open target. Entries carry `entries.push({turnId, call})` pairs rather
// than plain call objects for exactly this reason.
//
// Cached so a fast-moving live stream doesn't repeat a backend lookup on
// every re-render, and a call already resolved stays resolved when its
// turn is reopened later. A call is only ever cached once it's
// 'completed': one still 'started' is skipped (not cached as "no
// artifact") so it gets rechecked the moment it actually completes,
// instead of being judged prematurely on a result that doesn't exist yet.
function useResolvedArtifacts(entries) {
  const [resolved, setResolved] = useState({}) // "turnId:callId" -> artifact | null
  const inFlightRef = useRef(new Set())

  useEffect(() => {
    entries.forEach(({ turnId, call }) => {
      if (call.status !== 'completed') return
      const key = `${turnId}:${call.callId}`
      if (key in resolved || inFlightRef.current.has(key)) return
      const candidate = detectArtifactCandidate(call)
      if (!candidate) {
        setResolved(prev => ({ ...prev, [key]: null }))
        return
      }
      inFlightRef.current.add(key)
      // .catch before .then: a rejection here (resolveArtifact's own
      // backend calls already degrade to null via api.js's guard(), so
      // this is a last-resort safety net, not the expected path) must
      // still clear inFlightRef, or this key is wedged unresolved forever.
      resolveArtifact(candidate, api)
        .catch(() => null)
        .then(artifact => {
          inFlightRef.current.delete(key)
          setResolved(prev => ({ ...prev, [key]: artifact }))
        })
    })
  })

  return resolved
}

// ── Main panel ─────────────────────────────────────────────────────────────────
export default function AIChatPanel({ workflowID, isOpen, onClose, onOpenArtifact, initialRuntime, canvasMode = true }) {
  const [messages, setMessages]             = useState([])
  const [input, setInput]                   = useState('')
  // conversationId is this panel's current app-conversation (new chat
  // contract) — empty until the first send() lazily creates one. activeTurnId
  // is the client-generated turn id of the in-flight turn, '' when idle;
  // useChatStream below is the sole source of live activity for it.
  const [conversationId, setConversationId] = useState('')
  const [activeTurnId, setActiveTurnId]     = useState('')
  const [stopRequested, setStopRequested]   = useState(false)
  const [providers, setProviders]           = useState([])
  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedModel, setSelectedModel]   = useState('')
  const [runtimes, setRuntimes]             = useState([])
  const [selectedRuntime, setSelectedRuntime] = useState('')
  const [runtimeModels, setRuntimeModels]   = useState([]) // models for selectedRuntime, from getAgentRuntimeModels
  const [runtimeModelsLoading, setRuntimeModelsLoading] = useState(false)
  const [useAgents, setUseAgents]           = useState(false)
  const [monomindMissing, setMonomindMissing] = useState(false)
  const [pastConversations, setPastConversations] = useState([])
  const [showSessions, setShowSessions]     = useState(false)
  // Presentation: 'docked' (resizable sidebar, 380-720px) or 'expanded' (a
  // wider ~760px reading column, same docked layout just resized — not a
  // separate modal, so nothing about the turn owner below ever remounts).
  // viewportNarrow overrides either into a full-width overlay below 640px,
  // per plan §"Layout and interaction".
  const [panelWidth, setPanelWidth]         = useState(380)
  const [presentation, setPresentation]     = useState('docked')
  const [viewportNarrow, setViewportNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth < 640)

  const liveTurn = useChatStream({ conversationId, turnId: activeTurnId })
  const streaming = !!activeTurnId
  // Changes whenever new content arrives — a finalized turn (messages
  // grows) or a live delta/tool/notice within the current turn (lastSeq
  // advances) — driving useChatScroll's follow-vs-unread decision below.
  const scrollSignal = `${messages.length}:${liveTurn.lastSeq}`
  const scroll = useChatScroll(scrollSignal)

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
  // activeTurnIdRef — mid-stream switch guards (FV4-6); activeStreamRef — the
  // { workflowID, conversationId, turnId } of the in-flight turn, so a
  // workflowID change can stop the right one (FV4-7).
  const activeTurnIdRef  = useRef('')
  activeTurnIdRef.current = activeTurnId
  const activeStreamRef = useRef(null)
  // Guards loadConversation's multi-await chain (getChatTurns, then one
  // loadTurnState per turn) against a slower, superseded call clobbering a
  // newer one's transcript — the plan's generation-token requirement (§
  // "Guard asynchronous loads... so a slow old fetch cannot replace the
  // newly selected transcript"), applied to the ONE async load path in this
  // file that didn't already have an equivalent (useChatStream.js's own
  // live-hydration path has its own `cancelled` closure for the same
  // reason). Bumped by every competitor for "what the transcript should be
  // right now": loadConversation itself, startNewSession, and the bucket-
  // switch effect's own no-conversation-found branch.
  const loadGenerationRef = useRef(0)

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

  // ── Selected runtime changed: fetch its real model list ─────────────────
  // antigravity/codex discover their own catalog by shelling out to
  // themselves (see internal/monomind/models.go); claude has no such
  // command and returns a curated static list either way — the picker
  // below doesn't need to know which. binary comes from the runtime's own
  // ScanEntry (already fetched by the scan effect above), not re-resolved
  // here. A stale response for a runtime the user has since switched away
  // from is dropped rather than applied (the `current` guard).
  useEffect(() => {
    if (!useAgents || !selectedRuntime) { setRuntimeModels([]); return }
    const runtime = runtimes.find(r => r.id === selectedRuntime)
    let current = true
    setRuntimeModelsLoading(true)
    api.getAgentRuntimeModels(selectedRuntime, runtime?.binary || '').then(models => {
      if (!current) return
      const list = Array.isArray(models) ? models : []
      setRuntimeModels(list)
      if (list.length > 0 && !list.some(m => m.id === selectedModel)) {
        setSelectedModel(list[0].id)
      }
    }).finally(() => { if (current) setRuntimeModelsLoading(false) })
    return () => { current = false }
    // selectedModel intentionally excluded: this effect only reacts to a
    // runtime change, and re-including it would refetch on every keystroke
    // if the model field is ever hand-edited.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [useAgents, selectedRuntime, runtimes])

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

  // ── Refresh the past-conversations list for the current workflowID/mode ──
  const refreshPastConversations = useCallback(() => {
    if (!workflowID) return
    const backend = useAgents ? 'agent' : 'provider'
    api.listChatConversations('', 50).then(res => {
      const items = Array.isArray(res?.items) ? res.items : []
      setPastConversations(items.filter(c => c.workflowContext === workflowID && c.backend === backend))
    }).catch(err => notify('chat', `Could not refresh session list: ${err}`))
  }, [workflowID, useAgents])

  // ── Load one past conversation's turns into the visible transcript ──────
  const loadConversation = useCallback((conv) => {
    if (!conv?.id) return
    // Switching the active conversation mid-turn would let events from the
    // in-flight one land against the newly loaded transcript (crosstalk) —
    // block the switch and tell the user to stop first (FV4-6).
    if (activeTurnIdRef.current) {
      notify('chat', 'Stop the current response first')
      return
    }
    const generation = ++loadGenerationRef.current
    api.getChatTurns(conv.id, '', 50).then(async (res) => {
      const turns = (Array.isArray(res?.items) ? res.items : []).slice().reverse() // oldest first
      const built = []
      for (const turn of turns) {
        // Bail before each further fetch once superseded — no point
        // fetching turn N+1's events for a transcript that will never be
        // shown.
        if (loadGenerationRef.current !== generation) return
        // eslint-disable-next-line no-await-in-loop
        const state = await loadTurnState(conv.id, turn)
        if (!state) continue
        built.push({ role: 'user', content: turn.prompt })
        // Same shape a live turn uses (ChatTimeline/TurnStatus read reducer
        // state directly) — a reopened past turn renders exactly as it did
        // while still running, ordering/errors/partial work included.
        // turnId is carried alongside state (not read from state.scope,
        // which reduceTurnEvents deliberately never sets) so
        // useResolvedArtifacts can key its cache by (turnId, callId), not
        // callId alone.
        built.push({ role: 'turn', turnId: turn.id, state })
      }
      // Final check right before committing: a competing load could have
      // started and even finished while the last turn's events were still
      // being fetched above.
      if (loadGenerationRef.current !== generation) return
      setMessages(built)
      if (conv.runtimeId && !initialRuntime) setSelectedRuntime(conv.runtimeId)
      if (conv.model) setSelectedModel(conv.model)
      setConversationId(conv.id)
      setActiveTurnId('')
      setShowSessions(false)
    }).catch(err => {
      if (loadGenerationRef.current !== generation) return
      notify('chat', `Could not load session: ${err}`)
    })
  }, [initialRuntime])

  // ── Start a fresh chat: clears the visible transcript and active
  //     conversation, but leaves prior conversations in history — they stay
  //     reachable via the past-conversations list. The next send() lazily
  //     creates a brand new conversation. Blocked while a turn is active
  //     (FV4-6) for the same crosstalk reason as loadConversation. ────────
  const startNewSession = useCallback(() => {
    if (activeTurnIdRef.current) {
      notify('chat', 'Stop the current response first')
      return
    }
    loadGenerationRef.current++ // invalidate any in-flight loadConversation
    setConversationId('')
    setActiveTurnId('')
    setMessages([])
  }, [])

  // ── Load past conversations + auto-continue the most recent one when the
  //     panel switches to a new workflowID (chat-history bucket) ──────────
  const conversationsFetchedRef = useRef(null) // "workflowID:mode" bucket last fetched for
  useEffect(() => {
    if (!workflowID) return
    // Deferred until the panel has been opened at least once — a
    // mounted-but-never-opened panel (the global assistant at app boot)
    // skips the history fetch (FV4-3/4). After that, only an actual
    // bucket change refetches: close/reopen transitions must not rebind
    // the active conversation out from under the user.
    const bucket = `${workflowID}:${useAgents ? 'agent' : 'provider'}`
    if ((!isOpen && !hasOpenedRef.current) || conversationsFetchedRef.current === bucket) return
    conversationsFetchedRef.current = bucket
    const backend = useAgents ? 'agent' : 'provider'
    api.listChatConversations('', 50).then(res => {
      const items = (Array.isArray(res?.items) ? res.items : [])
        .filter(c => c.workflowContext === workflowID && c.backend === backend)
      setPastConversations(items)
      if (items.length > 0) {
        loadConversation(items[0])
      } else {
        // No conversation for this bucket yet — without this, a
        // conversationId left over from the PREVIOUS bucket (e.g. the
        // agent conversation, right after flipping to providers) stays
        // bound, and the next send() would dispatch on the wrong backend
        // (StartChatTurn branches on the stored conversation's own
        // Backend, not on whatever this panel's useAgents currently is).
        loadGenerationRef.current++ // invalidate any in-flight loadConversation from the PREVIOUS bucket
        setConversationId('')
        setActiveTurnId('')
        setMessages([])
      }
    }).catch(err => {
      // The ref above was set synchronously, before this request resolved —
      // left in place, this bucket would be stuck forever: same
      // workflowID/useAgents means the same bucket string, and isOpen is
      // the only other dep, so a plain close/reopen would never retry.
      // Only clear it if it's still this bucket; a switch that already
      // moved on to a different bucket while this request was in flight
      // owns the ref now and must not be clobbered by a late failure here.
      if (conversationsFetchedRef.current === bucket) conversationsFetchedRef.current = null
      notify('chat', `Could not load chat history: ${err}`)
    })
    // Intentionally excludes loadConversation: this effect should only run
    // when the panel's bucket actually changes, not every time
    // loadConversation's own deps (e.g. initialRuntime) change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowID, useAgents, isOpen])

  // ── Canvas re-key / workflow switch must not orphan an in-flight turn ───
  // This panel's live turn is keyed by conversationId/turnId, not
  // workflowID, so a bucket switch leaves the OLD turn running with no
  // visible output and no stop button unless explicitly stopped here.
  // Reset local state so the new bucket starts clean (FV4-7).
  const prevWorkflowIDRef = useRef(workflowID)
  useEffect(() => {
    const prev = prevWorkflowIDRef.current
    prevWorkflowIDRef.current = workflowID
    if (!prev || prev === workflowID) return
    const active = activeStreamRef.current
    if (activeTurnIdRef.current && active?.workflowID === prev) {
      api.stopChatTurn(active.conversationId, active.turnId)
    }
    activeStreamRef.current = null
    setActiveTurnId('')
    setStopRequested(false)
    setConversationId('')
    setMessages([])
  }, [workflowID])

  // ── Track viewport width for the narrow-overlay breakpoint ──────────────
  useEffect(() => {
    const onResize = () => setViewportNarrow(window.innerWidth < 640)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // ── Focus return: restore focus to whatever opened the panel once it
  //     closes, without requiring the caller to manage this itself ───────
  const previousFocusRef = useRef(null)
  useEffect(() => {
    if (isOpen) {
      previousFocusRef.current = document.activeElement
    } else if (previousFocusRef.current) {
      previousFocusRef.current.focus?.()
      previousFocusRef.current = null
    }
  }, [isOpen])

  // ── Escape: collapse an expanded view first, close only when already
  //     docked — mirrors a typical "un-maximize, then dismiss" pattern so
  //     one stray Escape can't discard an expanded reading session. When
  //     narrow, the panel is a full-viewport overlay with no visible
  //     expand/collapse control, so Escape must close outright — otherwise
  //     a panel left "expanded" from a wider viewport traps the user with
  //     no way back except the header's X button ───────────────────────
  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (e) => {
      if (e.key !== 'Escape') return
      if (presentation === 'expanded' && !viewportNarrow) setPresentation('docked')
      else onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [isOpen, presentation, viewportNarrow, onClose])

  // ── Resize handle: drag the panel's left edge, clamped 380-720px ────────
  const handleResizeStart = useCallback((e) => {
    e.preventDefault()
    const startX = e.clientX
    const startWidth = panelWidth
    // preventDefault above only blocks selection starting inside the handle
    // itself — once the drag crosses into the transcript or the rest of the
    // page, native text selection kicks back in and fights the resize, so
    // it's suppressed globally for the duration of the drag and restored on
    // mouseup.
    const prevUserSelect = document.body.style.userSelect
    const prevCursor = document.body.style.cursor
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'ew-resize'
    const onMove = (ev) => {
      const dx = startX - ev.clientX // dragging left (dx>0) widens a right-docked panel
      setPanelWidth(Math.min(720, Math.max(380, startWidth + dx)))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      document.body.style.userSelect = prevUserSelect
      document.body.style.cursor = prevCursor
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }, [panelWidth])

  // ── Finalize the live turn into the transcript once it terminates ───────
  useEffect(() => {
    if (!activeTurnId || !liveTurn.terminal) return
    // Same shape a reopened past turn uses (loadConversation above) — the
    // just-finished turn renders identically to how it looked while still
    // running, via ChatTimeline/TurnStatus, ordering/errors/partial work
    // included. Pushed even when parts is empty: TurnStatus alone still
    // truthfully reports a silent failure/stop rather than hiding it.
    setMessages(msgs => [...msgs, { role: 'turn', turnId: activeTurnId, state: liveTurn }])
    setActiveTurnId('')
    setStopRequested(false)
    refreshPastConversations()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTurnId, liveTurn.terminal])

  // ── Send message ────────────────────────────────────────────────────────
  const send = useCallback(async () => {
    const text = input.trim()
    if (!text || activeTurnId || !workflowID) return
    if (useAgents && !selectedRuntime) return
    if (!useAgents && !selectedProvider) return

    setMessages(msgs => [...msgs, { role: 'user', content: text }])
    setInput('')
    setStopRequested(false)

    try {
      let convId = conversationId
      if (!convId) {
        const conv = useAgents
          ? await api.createChatConversation('agent', workflowID, selectedRuntime, '', selectedModel)
          : await api.createChatConversation('provider', workflowID, '', selectedProvider, selectedModel)
        convId = conv.id
        setConversationId(convId)
      }
      const turnId = newTurnId()
      const tools = useAgents && getAssistantTools()
      const allowRuns = useAgents && getAssistantAllowRuns()
      activeStreamRef.current = { workflowID, conversationId: convId, turnId }
      const res = await api.startChatTurn(convId, turnId, text, !!tools, !!allowRuns)
      if (res?.ok === false) {
        activeStreamRef.current = null
        setMessages(msgs => [...msgs, { role: 'error', content: `Could not start: ${res.status}` }])
        return
      }
      setActiveTurnId(turnId)
    } catch (err) {
      activeStreamRef.current = null
      setMessages(msgs => [
        ...msgs,
        { role: 'error', content: String(err) },
      ])
    }
  }, [input, activeTurnId, workflowID, useAgents, selectedRuntime, selectedProvider, selectedModel, conversationId])

  // Whether a backend is actually selected for the current mode — gates the
  // input, matching send()'s own guard (useAgents ? selectedRuntime : selectedProvider).
  const hasBackend = useAgents ? !!selectedRuntime : !!selectedProvider

  // Assistant tool access (Settings → "Assistant tool access", GX2 contract):
  // read per render so toggling it there applies here without a remount.
  const assistantToolsOn   = getAssistantTools()
  const assistantAllowRuns = getAssistantAllowRuns()

  // ── Stop an in-flight turn ──────────────────────────────────────────────
  const stop = useCallback(async () => {
    if (!conversationId || !activeTurnId) return
    setStopRequested(true)
    try {
      await api.stopChatTurn(conversationId, activeTurnId)
      // activeTurnId itself clears once turn.finished (status "cancelled")
      // arrives through useChatStream — not here — so a stale UI never
      // claims the turn stopped before the backend actually acknowledged it;
      // stopRequested only drives the "Stopping" label in the meantime.
    } catch (err) {
      // stopChatTurn goes through parseStreamResult, which throws on a
      // {"error":...} reply — without this catch, that rejection left
      // stopRequested stuck true forever (activeTurnId only clears via
      // liveTurn.terminal, which never arrives if the stop call itself
      // failed), stranding the UI on "Stopping" with Stop now a no-op and
      // no way back except closing the panel.
      setStopRequested(false)
      notify('chat', `Could not stop: ${err}`)
    }
  }, [conversationId, activeTurnId])

  // ── Clear history ───────────────────────────────────────────────────────
  const clearHistory = useCallback(async () => {
    if (!workflowID || activeTurnIdRef.current) return
    const backend = useAgents ? 'agent' : 'provider'
    const res = await api.listChatConversations('', 50).catch(() => null)
    const items = (Array.isArray(res?.items) ? res.items : [])
      .filter(c => c.workflowContext === workflowID && c.backend === backend)
    await Promise.all(items.map(c => api.deleteChatConversation(c.id).catch(() => {})))
    setPastConversations([])
    setConversationId('')
    setMessages([])
  }, [workflowID, useAgents])

  // ── Provider change ─────────────────────────────────────────────────────
  const handleProviderChange = (e) => {
    const id = e.target.value
    setSelectedProvider(id)
    const p = providers.find(p => String(p.id) === id)
    if (p) setSelectedModel(p.default_model || '')
  }

  // Flat list of every call across finalized turns + the live one, each
  // paired with the turn id it belongs to — see useResolvedArtifacts' own
  // comment for why the cache key needs both, not just callId.
  const artifactEntries = []
  messages.forEach(msg => {
    if (msg.role === 'turn') {
      Object.values(msg.state.calls).forEach(call => artifactEntries.push({ turnId: msg.turnId, call }))
    }
  })
  Object.values(liveTurn.calls).forEach(call => artifactEntries.push({ turnId: activeTurnId, call }))
  const resolvedArtifacts = useResolvedArtifacts(artifactEntries)

  // useResolvedArtifacts' cache never expires — once a card resolves it
  // stays resolved for the life of this panel, even if the underlying
  // workflow/org/document is deleted a minute later. Re-run the same
  // lookup right before actually acting on a click, rather than trusting
  // the cached snapshot; on a click reusing detectArtifactCandidate off
  // the live `call` (not the stale cached artifact) means a cross-profile
  // or genuinely-deleted target is caught here even though the card was
  // legitimately valid when it first appeared.
  const openArtifact = useCallback((call, artifact) => {
    const candidate = detectArtifactCandidate(call)
    if (!candidate) return
    resolveArtifact(candidate, api).catch(() => null).then(fresh => {
      if (!fresh) {
        // A null result here means either "genuinely gone" or "the lookup
        // itself failed" — api.js's guard() swallows real backend errors
        // into the same null/[] shape a clean not-found produces, so this
        // can't claim deletion specifically without risking a false
        // "no longer exists" on a mere transient failure (a real failure
        // also already gets its own toast from guard's reportError).
        notify('chat', `Couldn't confirm this ${artifact.type} still exists — not opening it.`)
        return
      }
      onOpenArtifact?.(fresh)
    })
  }, [onOpenArtifact])

  if (!isOpen) return null

  return (
    <div style={viewportNarrow ? {
      position: 'fixed', inset: 0, zIndex: 50,
      background: '#060b13',
      display: 'flex', flexDirection: 'column',
      overflow: 'hidden',
    } : {
      width: presentation === 'expanded' ? 760 : panelWidth,
      maxWidth: '100vw',
      flexShrink: 0,
      background: '#060b13',
      borderLeft: '1px solid rgba(0,180,216,0.1)',
      display: 'flex', flexDirection: 'column',
      overflow: 'hidden',
      position: 'relative',
    }}>
      {/* Resize handle — drags the panel's left edge; hidden in expanded
          mode (fixed width there) and on the narrow overlay (full width). */}
      {!viewportNarrow && presentation === 'docked' && (
        <div
          onMouseDown={handleResizeStart}
          title="Drag to resize"
          style={{
            position: 'absolute', left: 0, top: 0, bottom: 0, width: 6,
            cursor: 'ew-resize', zIndex: 1,
          }}
        />
      )}
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
        {!viewportNarrow && (
          <button
            onClick={() => setPresentation(p => p === 'expanded' ? 'docked' : 'expanded')}
            title={presentation === 'expanded' ? 'Collapse' : 'Expand'}
            style={{
              background: 'transparent', border: 'none', cursor: 'pointer',
              color: 'var(--text-muted)', padding: 2, display: 'flex', alignItems: 'center',
              transition: 'color 100ms',
            }}
            onMouseEnter={e => e.currentTarget.style.color = '#00b4d8'}
            onMouseLeave={e => e.currentTarget.style.color = 'var(--text-muted)'}
          >
            {presentation === 'expanded' ? <Minimize2 size={12} /> : <Maximize2 size={12} />}
          </button>
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
            {pastConversations.length === 0 ? (
              <div style={{ padding: 10, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>
                No past sessions yet
              </div>
            ) : (
              pastConversations.map(c => (
                <div
                  key={c.id}
                  onClick={() => loadConversation(c)}
                  style={{
                    padding: '7px 9px', borderRadius: 6, cursor: 'pointer',
                    background: c.id === conversationId ? 'rgba(0,180,216,0.1)' : 'transparent',
                  }}
                  onMouseEnter={e => { if (c.id !== conversationId) e.currentTarget.style.background = 'rgba(255,255,255,0.04)' }}
                  onMouseLeave={e => { if (c.id !== conversationId) e.currentTarget.style.background = 'transparent' }}
                >
                  <div style={{
                    fontFamily: 'var(--font-mono)', fontSize: 10.5, color: '#e2e8f0',
                    whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                  }}>
                    {c.model || c.runtimeId || c.providerId || '(no model)'}
                  </div>
                  <div style={{ display: 'flex', gap: 6, marginTop: 2 }}>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: '#00b4d8' }}>{c.runtimeId || c.providerId}</span>
                    <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>{relativeTime(c.updatedAt)}</span>
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
            onChange={e => {
              // Same mid-turn guard as the runtime/model selects below —
              // flipping backends while a turn is active would otherwise
              // fetch the OTHER bucket's conversations underneath a still-
              // running turn instead of blocking like every other switch.
              if (activeTurnIdRef.current) {
                notify('chat', 'Stop the current response first')
                return
              }
              setUseAgents(e.target.value === 'agents')
            }}
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
              // Runtime change resets the conversation — blocked mid-turn
              // for the same crosstalk reason as loadConversation (FV4-6).
              if (activeTurnIdRef.current) {
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
        {useAgents && (runtimeModels.length > 0 || runtimeModelsLoading) ? (
          <select
            value={selectedModel}
            onChange={e => { setSelectedModel(e.target.value); startNewSession() }}
            disabled={runtimeModelsLoading}
            title="Model available for the selected agent runtime"
            style={{ ...selectStyle, flex: 1 }}
          >
            {runtimeModelsLoading && <option value="">Loading models…</option>}
            {!runtimeModelsLoading && runtimeModels.map(m => (
              <option key={m.id} value={m.id}>{m.label || m.id}</option>
            ))}
          </select>
        ) : (
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
        )}
      </div>

      {/* ── Messages area ── */}
      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
      <div ref={scroll.containerRef} style={{
        height: '100%',
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
            padding: '0 16px',
          }}>
            <span style={{ fontSize: 24, opacity: 0.15 }}>AI</span>
            {monomindMissing && !hasBackend ? (
              // Local agents are the primary chat path (useAgents defaults
              // true once any runtime is installed) — when monomind itself
              // isn't found, staying silent here just leaves the panel
              // looking broken with an empty "No providers" dropdown, since
              // there's usually nothing in the providers list either for a
              // fresh install. Surface the same fix Agents.jsx's empty
              // state gives, right where the user is already looking.
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, textAlign: 'center', lineHeight: 1.6 }}>
                monomind (the local AI agent engine) isn't installed.<br />
                Install it with <code>npm install -g @monoes/monomindcli</code><br />
                — or add an AI provider API key in Settings instead.
              </span>
            ) : (
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, textAlign: 'center', lineHeight: 1.6 }}>
                {workflowID === 'general'
                  ? <>Chat with your connected AI providers.<br />Ask anything.</>
                  : <>Ask the AI about your workflow,<br />request changes, or get help.</>
                }
              </span>
            )}
          </div>
        )}

        {messages.map((msg, i) => (
          msg.role === 'turn' ? (
            <div key={i} className="chat-assistant-turn">
              <ChatTimeline state={msg.state} />
              {Object.values(msg.state.calls).map(call => {
                const artifact = resolvedArtifacts[`${msg.turnId}:${call.callId}`]
                return artifact
                  ? <ChatArtifactCard key={call.callId} artifact={artifact} onOpenArtifact={() => openArtifact(call, artifact)} />
                  : null
              })}
              <TurnStatus state={msg.state} stopRequested={false} />
            </div>
          ) : (
            <MessageBubble
              key={i}
              role={msg.role}
              content={msg.content}
              isError={msg.role === 'error'}
            />
          )
        ))}

        {/* Live turn: timeline of interleaved text/tool steps plus its
            truthful status line (Starting agent/Running <tool>/Responding/
            Stopping/etc) — TurnStatus always renders something, so this
            replaces the old separate "Thinking..." indicator too. */}
        {streaming && (
          <div className="chat-assistant-turn">
            <ChatTimeline state={liveTurn} />
            {Object.values(liveTurn.calls).map(call => {
              const artifact = resolvedArtifacts[`${activeTurnId}:${call.callId}`]
              return artifact
                ? <ChatArtifactCard key={call.callId} artifact={artifact} onOpenArtifact={() => openArtifact(call, artifact)} />
                : null
            })}
            <TurnStatus state={liveTurn} stopRequested={stopRequested} />
          </div>
        )}
      </div>

      {/* Auto-follow only within 80px of the bottom; otherwise this stays
          out of the way instead of yanking the viewport while reading
          back through history (plan §"Layout and interaction"). */}
      {!scroll.isFollowing && scroll.unreadCount > 0 && (
        <button
          onClick={scroll.jumpToLatest}
          style={{
            position: 'absolute', bottom: 12, left: '50%', transform: 'translateX(-50%)',
            display: 'flex', alignItems: 'center', gap: 5,
            background: '#0d1a28', border: '1px solid rgba(0,180,216,0.35)',
            borderRadius: 999, padding: '5px 12px', cursor: 'pointer',
            color: '#00b4d8', fontFamily: 'var(--font-mono)', fontSize: 10,
            boxShadow: '0 4px 12px rgba(0,0,0,0.3)',
          }}
        >
          <ArrowDown size={11} />
          Jump to latest{scroll.unreadCount > 1 ? ` (${scroll.unreadCount})` : ''}
        </button>
      )}
      </div>

      {/* ── Input area ── */}
      <ChatComposer
        value={input}
        onChange={setInput}
        onSend={send}
        onStop={stop}
        streaming={streaming}
        disabled={!hasBackend}
        disabledReason={monomindMissing
          ? <>monomind not found — install with <code>npm install -g @monoes/monomindcli</code>, or select an AI provider above</>
          : (useAgents ? 'Select an agent runtime above to start chatting' : 'Select an AI provider above to start chatting')}
      />
    </div>
  )
}
