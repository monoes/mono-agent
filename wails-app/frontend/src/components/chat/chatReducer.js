// Pure reducer over one turn's chat:event stream (see
// docs/mastermind/plans/2026-09-11-interactive-agent-chat.md "Event
// contract"). No side effects, no subscriptions — useChatStream.js owns
// hydration/live merge/gap catch-up and dispatches events here in
// ascending-sequence order. Kept pure so it is directly unit-testable
// without mocking Wails.

export function initialChatState() {
  return {
    scope: null,      // { conversationId, turnId } this state belongs to, or null
    parts: [],         // ordered [{ kind: 'text', partId, text } | { kind: 'tool', callId }]
    calls: {},          // callId -> { callId, name, arguments, status: 'started'|'completed', ok, result }
    notices: [],         // [{ code, message, severity }], append-only
    usage: null,          // latest { inputTokens, outputTokens, costUsd, source } snapshot, never summed
    session: null,          // { runtime, sessionId } once session.bound fires
    terminal: null,          // { status, reason, exitCode, historySaved } once turn.finished fires
    startedAt: null,          // turn.started's "at", for local elapsed-time display
    lastEventAt: null,          // "at" of the most recently applied event, any type — drives "no new activity for Ns"
    lastSeq: 0,
  }
}

function upsertTextPart(parts, partId, text) {
  const idx = parts.findIndex(p => p.kind === 'text' && p.partId === partId)
  if (idx === -1) return [...parts, { kind: 'text', partId, text }]
  const next = parts.slice()
  next[idx] = { ...next[idx], text: next[idx].text + text }
  return next
}

function eventPatch(state, ev) {
  const payload = ev.payload || {}
  switch (ev.type) {
    case 'turn.started':
      return { startedAt: ev.at }

    case 'session.bound':
      return { session: { runtime: payload.runtime, sessionId: payload.sessionId } }

    case 'assistant.delta':
      return { parts: upsertTextPart(state.parts, payload.partId, payload.text) }

    case 'tool.started': {
      const calls = {
        ...state.calls,
        [payload.callId]: {
          callId: payload.callId,
          name: payload.name,
          arguments: payload.arguments ?? null,
          status: 'started',
          ok: null,
          result: null,
          startedAt: ev.at,
        },
      }
      return { calls, parts: [...state.parts, { kind: 'tool', callId: payload.callId }] }
    }

    case 'tool.completed': {
      const existing = state.calls[payload.callId]
      const calls = {
        ...state.calls,
        // No startedAt/finishedAt on the unmatched-completion fallback below:
        // a completion with no observed start has no duration to show, so
        // leaving both undefined (rather than fabricating finishedAt alone)
        // keeps "both timestamps present" the one signal ToolActivityCard
        // needs to decide whether elapsed time can be shown at all.
        [payload.callId]: existing
          ? { ...existing, status: 'completed', ok: payload.ok, result: payload.result, finishedAt: ev.at }
          : {
              callId: payload.callId,
              name: 'unknown',
              arguments: null,
              status: 'completed',
              ok: payload.ok,
              result: payload.result,
            },
      }
      // Retain an unmatched result as its own step (plan: "retain unmatched
      // results") rather than silently dropping a completion whose start
      // was never seen (e.g. hydration started mid-turn).
      const hasStep = state.parts.some(p => p.kind === 'tool' && p.callId === payload.callId)
      const parts = hasStep ? state.parts : [...state.parts, { kind: 'tool', callId: payload.callId }]
      return { calls, parts }
    }

    case 'usage.updated':
      return {
        usage: {
          inputTokens: payload.inputTokens ?? null,
          outputTokens: payload.outputTokens ?? null,
          costUsd: payload.costUsd ?? null,
          source: payload.source,
        },
      }

    case 'notice':
      return { notices: [...state.notices, { code: payload.code, message: payload.message, severity: payload.severity }] }

    case 'turn.finished':
      return {
        terminal: {
          status: payload.status,
          reason: payload.reason,
          exitCode: payload.exitCode ?? null,
          historySaved: !!payload.historySaved,
        },
      }

    default:
      return {}
  }
}

function applyEvent(state, ev) {
  const lastSeq = typeof ev.seq === 'number' ? ev.seq : state.lastSeq
  const lastEventAt = ev.at || state.lastEventAt
  return { ...state, ...eventPatch(state, ev), lastSeq, lastEventAt }
}

export function chatReducer(state, action) {
  switch (action.type) {
    case 'scope':
      return { ...initialChatState(), scope: action.scope }

    case 'reset':
      return initialChatState()

    case 'event': {
      const ev = action.event
      if (state.scope && (ev.conversationId !== state.scope.conversationId || ev.turnId !== state.scope.turnId)) {
        return state
      }
      if (typeof ev.seq === 'number' && ev.seq <= state.lastSeq) return state
      return applyEvent(state, ev)
    }

    // Synthesized client-side (useChatStream.js), never a wire event — has
    // no seq of its own, so it goes through a separate action type rather
    // than being squeezed through 'event's seq-ordering guard.
    case 'localNotice':
      return { ...state, notices: [...state.notices, action.notice] }

    default:
      return state
  }
}
