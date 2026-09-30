// Pure state for the coder chat bubbles (monoes/mono-agent#227): which coder
// chats are open as bubbles, their order, and each one's collapsed summary
// (status ring, unread count, cost) folded from `chat:event`. Everything
// here is plain data in, plain data out, so the rules are unit-testable
// without React or Wails.

export const MAX_VISIBLE = 6
export const STORAGE_KEY = 'monoagent:coderBubbles:v1'

// Bubble statuses, in the order they matter to the ring's color.
export const STATUS = {
  idle: 'idle',       // nothing running, nothing new
  working: 'working', // a turn is running
  done: 'done',       // the last turn completed
  error: 'error',     // the last turn failed or was interrupted
  needs: 'needs',     // someone in the chat is waiting on the user
}

let draftSeq = 0

// newDraftKey names a bubble whose conversation doesn't exist yet (a new
// coder chat before its first message).
export function newDraftKey() {
  draftSeq += 1
  return `draft-${Date.now().toString(36)}-${draftSeq}`
}

export function emptySummary() {
  return { status: STATUS.idle, unread: 0, activeTurnId: '', costByTurn: {}, turnStartedAt: '' }
}

// totalCost sums each turn's latest cost snapshot (usage.updated reports a
// running total per turn, never a delta), and each worker's.
export function totalCost(summary) {
  return Object.values(summary?.costByTurn || {}).reduce((n, c) => n + (Number(c) || 0), 0)
}

// applyChatEvent folds one chat:event envelope into a bubble's summary.
// expanded says whether the user is looking at that chat right now: a turn
// that finishes while collapsed counts as unread.
export function applyChatEvent(summary, ev, expanded) {
  const s = summary || emptySummary()
  const p = ev?.payload || {}
  switch (ev?.type) {
    case 'turn.started':
      return { ...s, status: STATUS.working, activeTurnId: ev.turnId || '', turnStartedAt: ev.at || '' }
    case 'usage.updated': {
      if (p.costUsd == null || !ev.turnId) return s
      // A dynamic-org worker's running total (#257) is kept under its own
      // key, so the chat's cost adds it to the lead's instead of replacing it.
      const key = p.agentId ? `${ev.turnId}:${p.agentId}` : ev.turnId
      return { ...s, costByTurn: { ...s.costByTurn, [key]: p.costUsd } }
    }
    // A dynamic-org agent asking the user something (#228) makes the
    // bubble pulse until that agent moves on.
    case 'agent.message':
      if (p.direction !== 'question' || (s.activeTurnId && ev.turnId && ev.turnId !== s.activeTurnId)) return s
      return { ...s, status: STATUS.needs, needsAgent: p.agentId || '' }
    case 'agent.status':
    case 'agent.finished':
      if (s.status !== STATUS.needs || !s.needsAgent || p.agentId !== s.needsAgent) return s
      return { ...s, status: STATUS.working, needsAgent: '' }
    case 'turn.finished': {
      // A turn other than the running one finishing (a late event for an
      // older turn) must not clear the running one.
      if (s.activeTurnId && ev.turnId && ev.turnId !== s.activeTurnId) return s
      const status = p.status === 'completed' ? STATUS.done
        : p.status === 'cancelled' ? STATUS.idle
          : STATUS.error
      return { ...s, status, activeTurnId: '', turnStartedAt: '', unread: expanded ? 0 : s.unread + 1 }
    }
    default:
      return s
  }
}

// summaryFromTurns builds a restored bubble's summary from `getChatTurns`
// (newest first): the latest turn says whether it is still running.
export function summaryFromTurns(turns) {
  const s = emptySummary()
  const latest = (turns || [])[0]
  if (!latest) return s
  if (latest.status === 'active' && latest.ownedByThisInstance !== false) {
    return { ...s, status: STATUS.working, activeTurnId: latest.id, turnStartedAt: latest.createdAt || '' }
  }
  if (latest.status === 'failed' || latest.status === 'interrupted') return { ...s, status: STATUS.error }
  return s
}

// layoutBubbles splits the open bubbles into the visible ones and an
// overflow list. The expanded bubble always stays visible, so it can be
// found again after switching.
export function layoutBubbles(bubbles, expandedKey, max = MAX_VISIBLE) {
  if (bubbles.length <= max) return { visible: bubbles, overflow: [] }
  const visible = bubbles.slice(0, max - 1)
  const rest = bubbles.slice(max - 1)
  const exp = rest.find(b => b.key === expandedKey)
  if (exp) {
    return { visible: [...visible.slice(0, max - 2), exp], overflow: [...visible.slice(max - 2), ...rest.filter(b => b !== exp)] }
  }
  return { visible, overflow: rest }
}

// moveBubble returns bubbles with the one at `from` moved to index `to`.
export function moveBubble(bubbles, fromKey, toKey) {
  const from = bubbles.findIndex(b => b.key === fromKey)
  const to = bubbles.findIndex(b => b.key === toKey)
  if (from < 0 || to < 0 || from === to) return bubbles
  const next = bubbles.slice()
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

// orgBubble is a running org opened as a bubble (#229): chatted with
// through its boss. Its key can never collide with a conversation id.
export function orgBubble(orgName) {
  return { key: `org:${orgName}`, kind: 'org', orgName, conversationId: '', cwd: '', model: '', runtime: '' }
}

export function isOrgBubble(b) {
  return b?.kind === 'org'
}

// ── Persistence ─────────────────────────────────────────────────────────────
// Only real conversations and org bubbles are kept across restarts: a
// draft (no message sent yet) has nothing to come back to. The storage
// accessors can throw (private windows, blocked storage), and the app must
// work without them.

export function loadState(storage = globalThis.localStorage) {
  try {
    const raw = storage?.getItem(STORAGE_KEY)
    const parsed = raw ? JSON.parse(raw) : null
    const bubbles = Array.isArray(parsed?.bubbles)
      ? parsed.bubbles.map(b => {
        if (b?.kind === 'org' && typeof b.orgName === 'string' && b.orgName) return orgBubble(b.orgName)
        if (b && typeof b.conversationId === 'string' && b.conversationId) {
          return { key: b.conversationId, conversationId: b.conversationId, cwd: String(b.cwd || ''), model: String(b.model || ''), runtime: String(b.runtime || '') }
        }
        return null
      }).filter(Boolean)
      : []
    const side = parsed?.side === 'left' ? 'left' : 'right'
    return { bubbles, side }
  } catch {
    return { bubbles: [], side: 'right' }
  }
}

export function saveState(state, storage = globalThis.localStorage) {
  try {
    const bubbles = state.bubbles.filter(b => b.conversationId || (b.kind === 'org' && b.orgName))
      .map(b => (b.kind === 'org'
        ? { kind: 'org', orgName: b.orgName }
        : { conversationId: b.conversationId, cwd: b.cwd || '', model: b.model || '', runtime: b.runtime || '' }))
    storage?.setItem(STORAGE_KEY, JSON.stringify({ bubbles, side: state.side === 'left' ? 'left' : 'right' }))
  } catch {
    // Storage unavailable: bubbles just won't come back after a restart.
  }
}

// ── Click-outside ───────────────────────────────────────────────────────────

// shouldCollapseOnBackdrop decides whether a click that landed on the
// overlay's backdrop collapses it. It doesn't when the press started inside
// the overlay (a drag or text selection that ended outside), when text is
// selected, or while a modal dialog is open on top.
export function shouldCollapseOnBackdrop({ pressStartedOnBackdrop, selectionText, modalOpen }) {
  if (!pressStartedOnBackdrop) return false
  if (selectionText && selectionText.trim()) return false
  if (modalOpen) return false
  return true
}

// monogram is the bubble's two-letter label from a folder name
// ("20260927-brisk-otter" → "BO", "mono-agent" → "MA", "api" → "AP").
export function monogram(name) {
  const words = String(name || '').split(/[^A-Za-z]+/).filter(Boolean)
  if (words.length === 0) return '·'
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase()
  return (words[words.length - 2][0] + words[words.length - 1][0]).toUpperCase()
}

// nowDoing names what a live turn is doing right now, from its latest
// running tool call ("Edit · src/app.go", "Bash · go test ./..."), or ''.
export function nowDoing(turn) {
  const running = Object.values(turn?.calls || {}).filter(c => c.status === 'started')
  const call = running[running.length - 1]
  if (!call) return ''
  let args = call.arguments
  if (typeof args === 'string') {
    try { args = JSON.parse(args) } catch { args = {} }
  }
  args = args && typeof args === 'object' ? args : {}
  const target = args.file_path || args.path || args.command || args.pattern || args.url || args.description || ''
  const text = String(target).replace(/\s+/g, ' ').trim()
  return text ? `${call.name} · ${text.length > 60 ? text.slice(0, 59) + '…' : text}` : call.name
}
