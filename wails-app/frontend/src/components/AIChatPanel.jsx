import { useState, useEffect, useRef, useCallback } from 'react'
import { X, Trash2, Plus, History, Maximize2, Minimize2, ArrowDown } from 'lucide-react'
import { api, notify } from '../services/api.js'
import { useChatStream, loadTurnState } from './chat/useChatStream.js'
import { ChatTimeline } from './chat/ChatTimeline.jsx'
import { ChatMarkdown } from './chat/ChatMarkdown.jsx'
import { TurnStatus, computeStatusLabel } from './chat/TurnStatus.jsx'
import { ChatComposer } from './chat/ChatComposer.jsx'
import { useChatScroll } from './chat/useChatScroll.js'
import { ChatArtifactCard } from './chat/ChatArtifactCard.jsx'
import { openArtifact as revalidateAndOpenArtifact } from './chat/chatArtifacts.js'
import { useResolvedArtifacts } from './chat/useResolvedArtifacts.js'
import './chat/chat.css'
import { cachedAgentScan } from '../lib/agentRuntimes.js'
import { getAssistantTools, getAssistantAllowRuns } from '../lib/assistantTools.js'

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

// composeLiveAnnouncement builds the one text fired into the panel's
// persistent aria-live region when a turn finalizes. TurnStatus's own
// per-turn "role=status" region only exists while {streaming && ...} is
// mounted — at the exact moment a turn finishes, that block unmounts and a
// *new* TurnStatus instance mounts inside the finalized message with the
// terminal label already baked in. Most screen readers only announce a
// mutation inside an already-present live region, not a freshly-inserted
// node whose content is already set, so "Completed"/"Failed" would
// otherwise go unannounced despite mid-stream status changes working fine.
// Folds in any notices and tool failures already present at finalize time
// (e.g. historySaved:false) — those otherwise have no live-region coverage
// at all (NoticeBanner is plain, non-live DOM). Exported and pure/
// deterministic like TurnStatus's own computeStatusLabel, for the same
// direct-unit-testability reason.
//
// alreadyAnnouncedCount (default 0, so every existing caller/test is
// unaffected) excludes the first N of turnState.notices from this summary —
// the live-turn effect below announces mid-turn notices individually the
// moment they arrive rather than waiting for finalize, and passes its own
// running count here so a notice already spoken once is never repeated in
// this finalize-time summary too.
export function composeLiveAnnouncement(turnState, alreadyAnnouncedCount = 0) {
  const { label } = computeStatusLabel(turnState, Date.now())
  const parts = [`Response ${label.toLowerCase()}.`]
  const failedCount = Object.values(turnState.calls || {}).filter(c => c.status === 'completed' && c.ok === false).length
  if (failedCount === 1) parts.push('1 tool call failed.')
  else if (failedCount > 1) parts.push(`${failedCount} tool calls failed.`)
  for (const notice of (turnState.notices || []).slice(alreadyAnnouncedCount)) parts.push(notice.message)
  return parts.join(' ')
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
  const [liveAnnouncement, setLiveAnnouncement] = useState('')
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
        // callId alone. ownedByThisInstance is carried the same way
        // (GetChatTurns' cross-instance-ownership field — see
        // docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md,
        // "OwnerInstanceID is written and read back but never compared to
        // anything" — not part of chatReducer's own event-replay state).
        // Collapsed to a real boolean here so TurnStatus only ever sees
        // true/false, never undefined: anything but an explicit `false` is
        // treated as owned/normal, so older data or a mock lacking the
        // field can never crash or mislabel an ordinary turn as foreign.
        built.push({ role: 'turn', turnId: turn.id, state, ownedByThisInstance: turn.ownedByThisInstance !== false })
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
      // A later bucket switch (e.g. a quick agents<->providers toggle) may
      // already have moved conversationsFetchedRef on to a different
      // bucket by the time this resolves — same staleness check the
      // .catch() below already applies. Without it, a late-resolving fetch
      // for a bucket the UI no longer shows can overwrite the
      // already-loaded transcript (or wipe it via the empty-items branch).
      if (conversationsFetchedRef.current !== bucket) return
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
      // The dropdown is a transient overlay on top of the panel, not a
      // presentation state — Escape must dismiss it alone, the same way it
      // would dismiss any other popup, without also collapsing/closing the
      // panel underneath in the same keystroke.
      if (showSessions) { setShowSessions(false); return }
      if (presentation === 'expanded' && !viewportNarrow) setPresentation('docked')
      else onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [isOpen, presentation, viewportNarrow, onClose, showSessions])

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

  // How many of the live turn's notices have already been individually
  // announced mid-turn by the effect below — so composeLiveAnnouncement's
  // finalize-time summary doesn't repeat one a second time. Reset at the
  // START of every new turn in send() (not only on finalize below): a turn
  // can also end via stop(), a workflow switch, or loadConversation without
  // ever reaching the finalize branch here, so resetting only there could
  // leave a stale non-zero count that silently suppresses the next turn's
  // mid-turn announcements until enough new notices accumulate to exceed it.
  const announcedNoticeCountRef = useRef(0)

  // ── Announce mid-turn notices as they arrive, and finalize the live turn
  //     into the transcript once it terminates ────────────────────────────
  // chatReducer's state.notices used to be read only once, at finalize, by
  // composeLiveAnnouncement below — a notice that arrives while a turn is
  // still actively streaming (e.g. a nonfatal warning) had no live-region
  // coverage at all until the turn ended, sometimes much later. Each new
  // notice is now announced the moment it lands, through the same
  // persistent region.
  //
  // Both concerns live in this ONE effect, rather than a second effect
  // watching liveTurn.notices independently, because the terminal event and
  // a notice can legitimately land in the SAME render: useChatStream's own
  // dispatchEvent synthesizes the historySaved:false notice as a second,
  // synchronous dispatch right alongside the turn.finished event that
  // triggered it, and React batches both into one update — so liveTurn.
  // terminal and a newly-appended liveTurn.notices entry can both be new in
  // one pass. Two separate effects would each queue their own
  // setLiveAnnouncement call in that pass, and only the later-declared
  // effect's value would actually reach the DOM (React batches same-tick
  // state updates into a single commit) — silently dropping whichever ran
  // first, which for this exact case would mean the historySaved:false
  // warning stops reaching the live region. Deciding both cases in one
  // effect (terminal branch first) avoids that race entirely and keeps
  // today's "Response completed. This turn finished, but its history may
  // not have saved…" behavior exactly as it already is.
  useEffect(() => {
    if (!activeTurnId) return
    if (liveTurn.terminal) {
      // Same shape a reopened past turn uses (loadConversation above) — the
      // just-finished turn renders identically to how it looked while still
      // running, via ChatTimeline/TurnStatus, ordering/errors/partial work
      // included. Pushed even when parts is empty: TurnStatus alone still
      // truthfully reports a silent failure/stop rather than hiding it.
      setMessages(msgs => [...msgs, { role: 'turn', turnId: activeTurnId, state: liveTurn }])
      setLiveAnnouncement(composeLiveAnnouncement(liveTurn, announcedNoticeCountRef.current))
      announcedNoticeCountRef.current = 0
      setActiveTurnId('')
      setStopRequested(false)
      refreshPastConversations()
      return
    }
    const notices = liveTurn.notices || []
    if (notices.length > announcedNoticeCountRef.current) {
      const newOnes = notices.slice(announcedNoticeCountRef.current)
      announcedNoticeCountRef.current = notices.length
      setLiveAnnouncement(newOnes.map(n => n.message).join(' '))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTurnId, liveTurn.terminal, liveTurn.notices])

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
      // Fresh slate for the mid-turn notice announcement count (see the
      // finalize/mid-turn effect above) — not just relying on the previous
      // turn's own finalize to have reset it, since a turn can also end via
      // stop()/workflow-switch/loadConversation without ever reaching that
      // branch.
      announcedNoticeCountRef.current = 0
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

  // Plain-text mirror of the JSX `disabledReason` passed to <ChatComposer>
  // below (near the bottom of this component) — kept logically in sync with
  // that ternary by hand, since the live-region announcement effect right
  // after this needs a flat string to compare/speak, not the JSX element
  // the monomindMissing branch uses there (an inline <code> tag). If you
  // change one, change the other the same way.
  const disabledReasonText = !hasBackend
    ? (monomindMissing
        ? 'monomind not found — install with npm install -g @monoes/monomindcli, or select an AI provider above'
        : (useAgents ? 'Select an agent runtime above to start chatting' : 'Select an AI provider above to start chatting'))
    : ''

  // Assistant tool access (Settings → "Assistant tool access", GX2 contract):
  // read per render so toggling it there applies here without a remount.
  const assistantToolsOn   = getAssistantTools()
  const assistantAllowRuns = getAssistantAllowRuns()

  // ── Announce ChatComposer's disabledReason banner as it changes ─────────
  // The banner changes asynchronously as the runtime scan/provider list
  // resolve (monomindMissing, hasBackend) — a user focused on the composer
  // otherwise has no indication why Send just became enabled/disabled.
  // Announced through the same persistent live region rather than a second
  // one. prevDisabledReasonRef seeds from the first render's own value, so
  // simply opening the panel in its resting state never announces anything
  // — only an actual later change does.
  const prevDisabledReasonRef = useRef(disabledReasonText)
  useEffect(() => {
    if (disabledReasonText === prevDisabledReasonRef.current) return
    prevDisabledReasonRef.current = disabledReasonText
    setLiveAnnouncement(disabledReasonText || 'You can send a message now.')
  }, [disabledReasonText])

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

  // See chatArtifacts.js's own openArtifact doc comment for why this
  // re-validates at click time instead of trusting useResolvedArtifacts'
  // cached snapshot (which never expires).
  const openArtifact = useCallback((call, artifact) => {
    revalidateAndOpenArtifact(call, artifact, { api, notify, onOpen: onOpenArtifact })
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
      {/* Persistent, visually-hidden live region for turn-completion
          announcements — see composeLiveAnnouncement's doc comment for why
          this must stay mounted at all times rather than living inside
          the {streaming && ...} block below. */}
      <div
        role="status"
        aria-live="polite"
        aria-label="Chat turn announcements"
        style={{
          position: 'absolute', width: 1, height: 1, padding: 0, margin: -1,
          overflow: 'hidden', clip: 'rect(0,0,0,0)', whiteSpace: 'nowrap', border: 0,
        }}
      >
        {liveAnnouncement}
      </div>
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
          aria-expanded={showSessions}
          aria-haspopup="listbox"
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
          <div role="listbox" aria-label="Past sessions" style={{
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
                  role="option"
                  tabIndex={0}
                  aria-selected={c.id === conversationId}
                  onClick={() => loadConversation(c)}
                  onKeyDown={(e) => {
                    if (e.key !== 'Enter' && e.key !== ' ') return
                    e.preventDefault()
                    loadConversation(c)
                  }}
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
              <ChatTimeline state={msg.state} turnId={msg.turnId} isLive={false} />
              {Object.values(msg.state.calls).map(call => {
                const artifact = resolvedArtifacts[`${msg.turnId}:${call.callId}`]
                return artifact
                  ? <ChatArtifactCard key={call.callId} artifact={artifact} onOpenArtifact={() => openArtifact(call, artifact)} />
                  : null
              })}
              <TurnStatus state={msg.state} stopRequested={false} ownedByThisInstance={msg.ownedByThisInstance} />
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
            <ChatTimeline state={liveTurn} turnId={activeTurnId} isLive={true} />
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
        // Kept in sync by hand with the plain-text disabledReasonText above
        // (used by the live-region announcement effect) — update both the
        // same way.
        disabledReason={monomindMissing
          ? <>monomind not found — install with <code>npm install -g @monoes/monomindcli</code>, or select an AI provider above</>
          : (useAgents ? 'Select an agent runtime above to start chatting' : 'Select an AI provider above to start chatting')}
      />
    </div>
  )
}
