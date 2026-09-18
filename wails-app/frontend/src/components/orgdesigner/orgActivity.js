// Pure, framework-free reducer over monomind org bus events (plan U15).
//
// The live canvas and run replay both fold events through applyEvent(): the
// live view feeds it the `org:event` stream, replay feeds it `org logs`.
// Nothing here touches React, the DOM, or the clock — every time value comes
// from the events themselves, so a replay produces exactly the state the live
// view had at the end of that run.
//
// Event shapes handled (monomind Org Runtime v2 bus, see __fixtures__/):
//   status   data.{from,to} state change (reason 'state-change'); 'org started',
//            'org stopped', 'session starting', 'Approval granted|denied for X',
//            'question answered' (data.questionId)
//   message  from → to within the org
//   xorg     'org:role' → 'org:role'; lands on BOTH buses with different ids
//            (C-43) — deduped by data.messageId, else by (from, to, subject,
//            body hash) within XORG_DEDUPE_MS
//   tool     tool, decision, data.chain_id/hop (M1)
//   asset    path
//   question data.action (tool approval) | data.questionId (ask_human)
//   gate     data.gateId + name (raised); reason gate-approved|gate-rejected
//   usage    data.tokens, data.cost_usd
//   audit    reason decision-resolved {kind, ref, resolver, verdict}, idle-nudge,
//            idle-stop, session-result-error, …

export const ROLE_STATUSES = ['offline', 'idle', 'working', 'blocked', 'completed']
export const XORG_DEDUPE_MS = 5000
export const RECENT_EDGE_MS = 8000

const MAX_MESSAGES = 50
const MAX_DECISIONS = 100
const MAX_ASSETS_PER_ROLE = 3
const MAX_SEEN_IDS = 500
const MAX_CHAINS = 50

function roleEntry(id, extra = {}) {
  return {
    id,
    status: 'offline',
    statusSince: 0,
    lastActiveAt: 0,
    lastTool: null,
    assets: [],
    assetCount: 0,
    tokens: 0,
    costUsd: 0,
    endpoint: false,
    ...extra,
  }
}

function roleIdOf(r) {
  if (typeof r === 'string') return { id: r, endpoint: false }
  if (!r || !r.id) return null
  const kind = r.kind ?? r.rest?.kind
  return { id: r.id, endpoint: kind === 'endpoint' }
}

/**
 * Fresh state for an org. `roles` may be ids, wire roles, or canvas nodes
 * (orgGraph.hydrate shape). opts.org names the org whose bus this is, so
 * `org:role` addresses can be resolved to local roles.
 */
export function initialState(roles = [], opts = {}) {
  const map = {}
  for (const r of roles || []) {
    const info = roleIdOf(r)
    if (info) map[info.id] = roleEntry(info.id, { endpoint: info.endpoint })
  }
  return {
    org: opts.org || null,
    run: null,
    orgStatus: 'unknown',
    lastTs: 0,
    roles: map,
    gates: {},
    approvals: [],
    questions: [],
    edges: {},
    messages: [],
    usage: { tokens: 0, costUsd: 0 },
    decisions: [],
    chains: {},
    counters: { idleNudges: 0, idleStops: 0, errors: 0, denied: 0, duplicatesDropped: 0 },
    seenIds: [],
    xorgSeen: {},
  }
}

/** Split an address into {org, role}; a bare id belongs to `defaultOrg`. */
export function parseAddress(addr, defaultOrg = null) {
  if (typeof addr !== 'string' || !addr) return { org: defaultOrg, role: null }
  const i = addr.indexOf(':')
  if (i === -1) return { org: defaultOrg, role: addr }
  return { org: addr.slice(0, i), role: addr.slice(i + 1) }
}

// Small stable string hash (FNV-1a) — enough to tell message bodies apart.
function hashString(s) {
  let h = 0x811c9dc5
  const str = String(s ?? '')
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(36)
}

/** Dedupe key for a cross-org message copy (C-43). */
export function xorgKey(event) {
  const id = event?.data?.messageId
  if (id) return `id:${id}`
  return `c:${event.from}|${event.to}|${event.subject ?? ''}|${hashString(event.msg)}`
}

/** Resolve an address to a local role id on this bus, or null. */
function localRole(state, addr) {
  const { org, role } = parseAddress(addr, state.org)
  if (!role) return null
  if (state.org ? org === state.org : true) return role
  return null
}

function touchRole(roles, id, ts, patch = {}) {
  if (!id) return roles
  const prev = roles[id] || roleEntry(id)
  return { ...roles, [id]: { ...prev, lastActiveAt: Math.max(prev.lastActiveAt, ts || 0), ...patch } }
}

function knownOrStateChange(state, id) {
  return !!id && !!state.roles[id]
}

function applyStatus(state, ev, ts) {
  const msg = typeof ev.msg === 'string' ? ev.msg : ''
  const s = { ...state }
  if (ev.reason === 'state-change' && ev.from && ev.data?.to) {
    s.roles = touchRole(s.roles, ev.from, ts, { status: ev.data.to, statusSince: ts })
    return s
  }
  if (!ev.from && /^org started/.test(msg)) {
    s.orgStatus = 'running'
    return s
  }
  if (!ev.from && msg === 'org stopped') {
    s.orgStatus = 'stopped'
    const roles = {}
    for (const [id, r] of Object.entries(s.roles)) {
      roles[id] = r.status === 'working' ? { ...r, status: 'idle', statusSince: ts } : r
    }
    s.roles = roles
    return s
  }
  if (ev.from && /^session starting|^lazy-spawned/.test(msg)) {
    const prev = s.roles[ev.from]
    if (!prev || prev.status === 'offline') {
      s.roles = touchRole(s.roles, ev.from, ts, { status: 'idle', statusSince: ts })
    }
    return s
  }
  const approval = /^Approval (granted|denied) for (.+)$/.exec(msg)
  if (approval && ev.from) {
    const action = approval[2]
    s.approvals = s.approvals.filter(a => !(a.role === ev.from && a.action === action))
    return s
  }
  if (msg === 'question answered' && ev.data?.questionId) {
    s.questions = s.questions.filter(q => q.questionId !== ev.data.questionId)
    return s
  }
  if (knownOrStateChange(s, ev.from)) s.roles = touchRole(s.roles, ev.from, ts)
  return s
}

function addEdge(state, from, to, ts, kind, ev, external) {
  const key = `${from}->${to}`
  const prev = state.edges[key]
  const edge = {
    from, to, kind, external,
    count: (prev?.count || 0) + 1,
    firstTs: prev?.firstTs ?? ts,
    lastTs: ts,
    subject: ev.subject ?? prev?.subject ?? '',
  }
  const messages = [...state.messages, {
    from, to, kind, ts, subject: ev.subject ?? '', messageId: ev.data?.messageId ?? null, external,
  }]
  return {
    ...state,
    edges: { ...state.edges, [key]: edge },
    messages: messages.length > MAX_MESSAGES ? messages.slice(-MAX_MESSAGES) : messages,
  }
}

function applyMessage(state, ev, ts) {
  const from = localRole(state, ev.from) ?? ev.from
  const to = localRole(state, ev.to) ?? ev.to
  if (!from || !to) return state
  let s = addEdge(state, from, to, ts, 'message', ev, false)
  s = { ...s, roles: touchRole(touchRole(s.roles, from, ts), to, ts) }
  return s
}

function applyXorg(state, ev, ts) {
  const key = xorgKey(ev)
  const seenAt = state.xorgSeen[key]
  if (seenAt !== undefined && (key.startsWith('id:') || Math.abs(ts - seenAt) <= XORG_DEDUPE_MS)) {
    return { ...state, counters: { ...state.counters, duplicatesDropped: state.counters.duplicatesDropped + 1 } }
  }
  const xorgSeen = {}
  for (const [k, t] of Object.entries(state.xorgSeen)) {
    if (k.startsWith('id:') || Math.abs(ts - t) <= XORG_DEDUPE_MS * 12) xorgSeen[k] = t
  }
  xorgSeen[key] = ts
  const fromLocal = localRole(state, ev.from)
  const toLocal = localRole(state, ev.to)
  const from = fromLocal ?? ev.from
  const to = toLocal ?? ev.to
  if (!from || !to) return { ...state, xorgSeen }
  let s = addEdge({ ...state, xorgSeen }, from, to, ts, 'xorg', ev, !(fromLocal && toLocal))
  let roles = s.roles
  if (fromLocal && roles[fromLocal]) roles = touchRole(roles, fromLocal, ts)
  if (toLocal && roles[toLocal]) roles = touchRole(roles, toLocal, ts)
  return { ...s, roles }
}

function applyTool(state, ev, ts) {
  let s = { ...state }
  if (ev.from) s.roles = touchRole(s.roles, ev.from, ts, { lastTool: ev.tool ?? null })
  if (ev.decision === 'deny') s.counters = { ...s.counters, denied: s.counters.denied + 1 }
  const chainId = ev.data?.chain_id
  if (chainId) {
    const hops = [...(s.chains[chainId] || []), {
      role: ev.from ?? null, tool: ev.tool ?? null, ts, decision: ev.decision ?? null, hop: ev.data?.hop ?? null,
    }]
    let chains = { ...s.chains, [chainId]: hops }
    const ids = Object.keys(chains)
    if (ids.length > MAX_CHAINS) {
      ids.sort((a, b) => (chains[a].at(-1).ts) - (chains[b].at(-1).ts))
      chains = Object.fromEntries(ids.slice(-MAX_CHAINS).map(id => [id, chains[id]]))
    }
    s.chains = chains
  }
  return s
}

function applyAsset(state, ev, ts) {
  const path = ev.path ?? ev.data?.path
  if (!ev.from || !path) return state
  const prev = state.roles[ev.from] || roleEntry(ev.from)
  const assets = [...prev.assets, { path, ts }].slice(-MAX_ASSETS_PER_ROLE)
  return { ...state, roles: touchRole(state.roles, ev.from, ts, { assets, assetCount: prev.assetCount + 1 }) }
}

function applyQuestion(state, ev, ts) {
  const d = ev.data || {}
  if (d.questionId) {
    if (state.questions.some(q => q.questionId === d.questionId)) return state
    return { ...state, questions: [...state.questions, { questionId: d.questionId, role: ev.from ?? null, since: ts }] }
  }
  if (d.action) {
    return {
      ...state,
      approvals: [...state.approvals, { role: ev.from ?? null, action: d.action, since: ts, requestId: d.requestId ?? null }],
    }
  }
  return state
}

function resolveGate(state, gateId, status, ts) {
  const g = state.gates[gateId]
  if (!g) return state
  return { ...state, gates: { ...state.gates, [gateId]: { ...g, status, resolvedAt: ts } } }
}

function applyGate(state, ev, ts) {
  const d = ev.data || {}
  if (!d.gateId) return state
  if (ev.reason === 'gate-approved' || ev.reason === 'gate-rejected') {
    return resolveGate(state, d.gateId, ev.reason === 'gate-approved' ? 'approved' : 'rejected', ts)
  }
  if (state.gates[d.gateId]) return state
  const gate = { gateId: d.gateId, role: ev.from ?? null, name: d.name ?? '', since: ts, status: 'pending', resolvedAt: null }
  return { ...state, gates: { ...state.gates, [d.gateId]: gate }, roles: touchRole(state.roles, ev.from, ts) }
}

function applyUsage(state, ev, ts) {
  const tokens = Number(ev.data?.tokens) || 0
  const cost = Number(ev.data?.cost_usd) || 0
  let s = { ...state, usage: { tokens: state.usage.tokens + tokens, costUsd: state.usage.costUsd + cost } }
  if (ev.from && state.roles[ev.from]) {
    const r = state.roles[ev.from]
    s.roles = touchRole(s.roles, ev.from, ts, { tokens: r.tokens + tokens, costUsd: r.costUsd + cost })
  }
  return s
}

function applyAudit(state, ev, ts) {
  const c = { ...state.counters }
  switch (ev.reason) {
    case 'idle-nudge': c.idleNudges++; return { ...state, counters: c }
    case 'idle-stop': c.idleStops++; return { ...state, counters: c }
    case 'session-result-error': c.errors++; return { ...state, counters: c }
    case 'decision-resolved': {
      const d = ev.data || {}
      let s = { ...state }
      const decisions = [...s.decisions, { kind: d.kind, ref: d.ref, resolver: d.resolver, verdict: d.verdict, role: ev.from ?? null, ts }]
      s.decisions = decisions.length > MAX_DECISIONS ? decisions.slice(-MAX_DECISIONS) : decisions
      if (d.kind === 'gate') s = resolveGate(s, d.ref, d.verdict === 'approved' ? 'approved' : 'rejected', ts)
      if (d.kind === 'question') s.questions = s.questions.filter(q => q.questionId !== d.ref)
      if (d.kind === 'approval') s.approvals = s.approvals.filter(a => !a.requestId || a.requestId !== d.ref)
      return s
    }
    default: return state
  }
}

/** Fold one bus event into state. Returns a new state; never mutates. */
export function applyEvent(state, event) {
  if (!event || typeof event !== 'object' || !event.type) return state
  let s = state
  if (event.id) {
    if (s.seenIds.includes(event.id)) return s
    const seenIds = [...s.seenIds, event.id]
    s = { ...s, seenIds: seenIds.length > MAX_SEEN_IDS ? seenIds.slice(-MAX_SEEN_IDS) : seenIds }
  }
  // A different run on the same bus starts from a clean slate (roles kept).
  if (event.run && s.run && event.run !== s.run && (!event.org || !s.org || event.org === s.org)) {
    const fresh = initialState(Object.values(s.roles).map(r => ({ id: r.id, kind: r.endpoint ? 'endpoint' : undefined })), { org: s.org })
    s = { ...fresh, seenIds: s.seenIds }
  }
  const ts = Number(event.ts) || s.lastTs
  s = { ...s, lastTs: Math.max(s.lastTs, ts), run: s.run ?? event.run ?? null }
  if (!s.org && event.org && event.type !== 'xorg') s.org = event.org
  switch (event.type) {
    case 'status': return applyStatus(s, event, ts)
    case 'message': return applyMessage(s, event, ts)
    case 'xorg': return applyXorg(s, event, ts)
    case 'tool': return applyTool(s, event, ts)
    case 'asset': return applyAsset(s, event, ts)
    case 'question': return applyQuestion(s, event, ts)
    case 'gate': return applyGate(s, event, ts)
    case 'usage': return applyUsage(s, event, ts)
    case 'audit': return applyAudit(s, event, ts)
    default:
      return event.from && s.roles[event.from] ? { ...s, roles: touchRole(s.roles, event.from, ts) } : s
  }
}

/** Replay a list of events (e.g. `org logs` items) from a fresh state. */
export function replay(events, roles = [], opts = {}) {
  let s = initialState(roles, opts)
  for (const ev of events || []) s = applyEvent(s, ev)
  return s
}

// ── Selectors ──────────────────────────────────────────────────────────────

/** Pending gates, oldest first. */
export function pendingGates(state) {
  return Object.values(state.gates).filter(g => g.status === 'pending').sort((a, b) => a.since - b.since)
}

/** Live overlay for one role card. */
export function roleActivity(state, roleId) {
  const r = state.roles[roleId]
  const gate = pendingGates(state).filter(g => g.role === roleId).at(-1) || null
  return {
    status: r?.status ?? 'offline',
    lastActiveAt: r?.lastActiveAt ?? 0,
    lastTool: r?.lastTool ?? null,
    assets: r?.assets ?? [],
    assetCount: r?.assetCount ?? 0,
    costUsd: r?.costUsd ?? 0,
    pendingGate: gate,
    pendingApprovals: state.approvals.filter(a => a.role === roleId).length,
    pendingQuestions: state.questions.filter(q => q.role === roleId).length,
  }
}

/** Edges whose last message landed within windowMs of `now` (default: the last event). */
export function recentEdges(state, now = state.lastTs, windowMs = RECENT_EDGE_MS) {
  return Object.values(state.edges).filter(e => now - e.lastTs <= windowMs)
}

/** Counts of pending questions by kind (C-45: approvals vs ask_human). */
export function pendingCounts(state) {
  return {
    approvals: state.approvals.length,
    questions: state.questions.length,
    gates: pendingGates(state).length,
  }
}
