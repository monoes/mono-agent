// The running-org adapter (monoes/mono-agent#229): a running Org Runtime v2
// org's bus events become the org stage model (lib/orgStage.js), so a
// running org gets the same stage as a dynamic coder org. Each bus event is
// translated into the journal-shaped envelopes ({seq, at, type, payload})
// the stage's own stageReducer folds, and the orgActivity reducer (the one
// the Org Designer's live canvas uses) runs alongside for what the stage
// has no notion of: pending questions, approvals and gates, cost per role,
// and whether the org runs.
//
// Everything is pure and keyed by the events, never the clock: replaying a
// recorded bus.jsonl gives exactly the state the live stream built, and a
// re-delivered event changes nothing.
//
// Mapping:
//   the boss (the org's root role)  → the stage's lead node
//   any other role                  → a node under the role it reports to,
//                                     added when it first shows up (lazily
//                                     started roles pop in on their first
//                                     message); its ancestors come first
//   status state-change             → agent.status (idle, working, blocked,
//                                     done, failed)
//   message down / up / across      → a brief (first) or a message flight,
//                                     a result, a message flight
//   tool, tool_activity             → tool.started / tool.completed; a
//                                     plain `tool` call ends when the role's
//                                     next call starts or it stops working
//   usage                           → usage.updated with the role's totals
//   question from a role            → agent.message question (amber)
//   org started / org stopped       → turn.started / turn.finished

import { stageReducer, initialStage, ownerOf, LEAD_ID, MAX_CALLS } from './orgStage.js'
import {
  initialState as activityInitial, applyEvent as activityApply, parseAddress, pendingGates,
} from '../components/orgdesigner/orgActivity.js'

const MAX_SEEN = 300
// Addresses on the bus that are never roles of the org.
const NOT_ROLES = new Set(['dag', 'human', 'autonomy', 'system'])

const STAGE_STATUS = {
  idle: 'idle', offline: 'idle', working: 'working', blocked: 'blocked',
  completed: 'done', done: 'done', crashed: 'failed', error: 'failed', failed: 'failed',
}

function rolesMap(roles) {
  const out = {}
  for (const r of roles || []) {
    if (!r?.id) continue
    out[r.id] = {
      id: r.id, title: r.title || '', type: r.type || '',
      reportsTo: r.reports_to ?? r.reportsTo ?? null,
      runtime: r.runtime || '', model: r.model || '',
    }
  }
  return out
}

// initialOrgBubble is an empty adapter state for org. roles are the org's
// roles as `org chat history` lists them ({id, title, type, reports_to,
// runtime, model}); boss is the root role's id.
export function initialOrgBubble({ org, boss = '', roles = [] } = {}) {
  const byId = rolesMap(roles)
  return {
    org: org || '',
    boss,
    roles: byId,
    run: null,
    stage: initialStage(),
    activity: activityInitial(Object.keys(byId), { org }),
    seq: 0,
    seen: [],
    joined: {},     // role → true once its node exists
    briefed: {},    // role → true once it got its first message from above
    openCall: {},   // role → its plain `tool` call still in flight
    // Non-lead roles' tool calls, callId → call: the stage keeps only
    // their order, and the drawer reads them from here (as a coder
    // bubble's drawer reads chatReducer's agentCalls). Like the stage, each
    // node keeps its latest MAX_CALLS (callIds: node → their ids, oldest
    // first).
    calls: {},
    callIds: {},
  }
}

function nodeIdOf(s, role) {
  return role === s.boss ? LEAD_ID : role
}

// parentOf is the node a role hangs under: the role it reports to, the
// lead for the boss's reports and for roles the adapter can't place.
function parentOf(s, role) {
  const up = s.roles[role]?.reportsTo
  if (!up || up === s.boss || !s.roles[up]) return LEAD_ID
  return up
}

// localRole resolves an address to a role of this org, or null.
function localRole(s, addr) {
  const { org, role } = parseAddress(addr, s.org)
  if (!role || (org && s.org && org !== s.org)) return null
  if (NOT_ROLES.has(role) || org === 'human') return null
  if (Object.keys(s.roles).length && !s.roles[role]) return null
  return role
}

function iso(ts) {
  const n = Number(ts)
  return n > 0 ? new Date(n).toISOString() : null
}

function stripTrace(text) {
  return String(text ?? '').replace(/^\[trace [^\]\n]*\]\n?/, '')
}

// Mutable scratch for one bus event: the envelopes it produces go through
// stageReducer in order, each with the next seq.
function emitter(s, ev) {
  let stage = s.stage
  let seq = s.seq
  const at = iso(ev.ts)
  return {
    emit(type, payload) {
      seq += 1
      stage = stageReducer(stage, { seq, at, type, payload })
      if (type === 'tool.started' || type === 'tool.completed') recordCall(s, stage, type, payload, at)
    },
    done() { return { stage, seq } },
  }
}

// recordCall keeps a call in the shape ChatTimeline renders, under the node
// the stage files it under (its own ownerOf), dropping that node's oldest once it has more
// than MAX_CALLS, exactly as the stage caps the node's callOrder. The
// lead's own calls aren't kept (the stage keeps no order for them).
function recordCall(s, stage, type, p, at) {
  const owner = ownerOf(stage, p)
  if (owner === LEAD_ID) return
  const prev = s.calls[p.callId]
  if (type === 'tool.completed' && !prev) return // dropped already, or never seen
  let call
  if (type === 'tool.started') {
    call = {
      callId: p.callId, name: p.name, arguments: p.arguments ?? null, status: 'started', ok: null, result: null, startedAt: at,
      ...(p.native ? { native: true } : {}), ...(p.parentCallId ? { parentCallId: p.parentCallId } : {}),
    }
  } else {
    call = {
      ...prev, status: 'completed', ok: p.ok ?? null, result: p.result ?? '', finishedAt: at,
      ...(p.denied ? { denied: true } : {}), ...(p.cancelled ? { cancelled: true } : {}),
    }
  }
  const calls = { ...s.calls, [p.callId]: call }
  if (type === 'tool.started' && !prev) {
    const ids = [...(s.callIds[owner] || []), p.callId]
    while (ids.length > MAX_CALLS) delete calls[ids.shift()]
    s.callIds = { ...s.callIds, [owner]: ids }
  }
  s.calls = calls
}

function join(s, out, role) {
  if (!role || role === s.boss || s.joined[role]) return
  const parent = parentOf(s, role)
  if (parent !== LEAD_ID) join(s, out, parent)
  const r = s.roles[role] || { title: '', type: '', runtime: '', model: '' }
  s.joined = { ...s.joined, [role]: true }
  out.emit('agent.spawned', {
    agentId: role, parentId: parent, role: r.title || role, agentType: r.type || '',
    runtime: r.runtime || '', model: r.model || '',
  })
  out.emit('agent.status', { agentId: role, to: 'idle' })
}

function endCall(s, out, role, extra = {}) {
  const callId = s.openCall[role]
  if (!callId) return
  const next = { ...s.openCall }
  delete next[role]
  s.openCall = next
  const agentId = nodeIdOf(s, role)
  out.emit('tool.completed', { callId, ok: true, ...(agentId !== LEAD_ID ? { agentId } : {}), ...extra })
}

function stageEvent(s, out, ev) {
  const d = ev.data || {}
  const from = localRole(s, ev.from)
  switch (ev.type) {
    case 'status': {
      const msg = typeof ev.msg === 'string' ? ev.msg : ''
      if (!ev.from && /^org started/.test(msg)) { out.emit('turn.started', {}); return }
      if (!ev.from && msg === 'org stopped') {
        for (const role of Object.keys(s.openCall)) endCall(s, out, role)
        out.emit('turn.finished', { status: 'completed' })
        return
      }
      if (!from) return
      join(s, out, from)
      if (ev.reason === 'state-change' && d.to) {
        if (d.to !== 'working') endCall(s, out, from)
        out.emit('agent.status', { agentId: nodeIdOf(s, from), to: STAGE_STATUS[d.to] || d.to })
      }
      return
    }
    case 'message': {
      const to = localRole(s, ev.to)
      if (!from || !to || from === to) return
      join(s, out, from)
      join(s, out, to)
      const text = stripTrace(ev.msg) || ev.subject || ''
      const nf = nodeIdOf(s, from)
      const nt = nodeIdOf(s, to)
      // The transcript names both ends by their titles.
      const names = { from: s.roles[from]?.title || from, to: s.roles[to]?.title || to }
      if (nt !== LEAD_ID && parentOf(s, to) === nf) {
        if (!s.briefed[to]) {
          s.briefed = { ...s.briefed, [to]: true }
          out.emit('agent.message', { agentId: nt, direction: 'brief', ...names, text })
        } else {
          out.emit('agent.message', { agentId: nt, direction: 'message', ...names, text, flightFrom: nf, flightTo: nt })
        }
      } else if (nf !== LEAD_ID && parentOf(s, from) === nt) {
        out.emit('agent.message', { agentId: nf, direction: 'result', ...names, text })
      } else {
        const agentId = nf === LEAD_ID ? nt : nf
        out.emit('agent.message', { agentId, direction: 'message', ...names, text, flightFrom: nf, flightTo: nt })
      }
      return
    }
    case 'tool': {
      if (!from) return
      join(s, out, from)
      endCall(s, out, from)
      const agentId = nodeIdOf(s, from)
      const callId = `tool:${ev.id || `${from}-${out.done().seq}`}`
      out.emit('tool.started', {
        callId, name: ev.tool || 'tool', arguments: d.input ?? null, ...(agentId !== LEAD_ID ? { agentId } : {}),
      })
      if (ev.decision === 'deny') {
        out.emit('tool.completed', { callId, ok: false, denied: true, result: ev.reason || '', ...(agentId !== LEAD_ID ? { agentId } : {}) })
      } else {
        s.openCall = { ...s.openCall, [from]: callId }
      }
      return
    }
    case 'tool_activity': {
      if (!from || !ev.id) return
      join(s, out, from)
      const agentId = nodeIdOf(s, from)
      const owner = agentId !== LEAD_ID ? { agentId } : {}
      const callId = `ta:${ev.id}`
      if (ev.phase === 'end') {
        const output = ev.output == null ? '' : typeof ev.output === 'string' ? ev.output : JSON.stringify(ev.output)
        out.emit('tool.completed', {
          callId, ok: typeof ev.ok === 'boolean' ? ev.ok : !(ev.denied || ev.cancelled), result: output,
          ...(ev.denied ? { denied: true } : {}), ...(ev.cancelled ? { cancelled: true } : {}), ...owner,
        })
      } else {
        out.emit('tool.started', {
          callId, name: ev.name || 'tool', arguments: ev.input ?? null, native: true,
          ...(ev.parent_tool_use_id ? { parentCallId: `ta:${ev.parent_tool_use_id}` } : {}), ...owner,
        })
      }
      return
    }
    case 'usage': {
      if (!from) return
      join(s, out, from)
      const r = s.activity.roles[from]
      const agentId = nodeIdOf(s, from)
      out.emit('usage.updated', {
        inputTokens: r ? r.tokens : null, outputTokens: null, costUsd: r ? r.costUsd : null,
        ...(agentId !== LEAD_ID ? { agentId } : {}),
      })
      return
    }
    case 'question':
    case 'gate': {
      if (!from) return
      join(s, out, from)
      const agentId = nodeIdOf(s, from)
      const text = d.question || d.name || d.action || ''
      if (agentId !== LEAD_ID && (ev.type === 'question' || !ev.reason)) {
        out.emit('agent.message', { agentId, direction: 'question', from: s.roles[from]?.title || from, to: '', text })
      }
      return
    }
    case 'chat':
    case 'asset':
      if (from) join(s, out, from)
      return
    default:
  }
}

function seenKey(ev) {
  if (!ev.id) return ''
  return ev.type === 'tool_activity' ? `${ev.id}:${ev.phase || 'start'}` : ev.id
}

// replayed reports whether ev was already folded. A tail that (re)starts
// replays its run's bus from the top, and the bus is append-only, so an
// event of the current run older than the newest one folded is a replay;
// among events of that same millisecond, the ids tell.
function replayed(state, ev) {
  if (ev.run && ev.run === state.run && Number(ev.ts) < state.activity.lastTs) return true
  const key = seenKey(ev)
  return !!key && state.seen.includes(key)
}

// applyOrgBusEvent folds one bus event into the adapter state. It never
// mutates; an event it already folded returns the state as is.
export function applyOrgBusEvent(state, ev) {
  if (!ev || typeof ev !== 'object' || !ev.type) return state
  if (ev.org && state.org && ev.org !== state.org && ev.type !== 'xorg') return state
  if (replayed(state, ev)) return state
  const key = seenKey(ev)
  let s = { ...state }
  if (key) {
    const seen = [...s.seen, key]
    s.seen = seen.length > MAX_SEEN ? seen.slice(-MAX_SEEN) : seen
  }
  // A new run on the same bus starts a fresh stage (the roles stay).
  if (ev.run && s.run && ev.run !== s.run) {
    s = { ...s, stage: initialStage(), seq: 0, joined: {}, briefed: {}, openCall: {}, calls: {}, callIds: {} }
  }
  if (ev.run) s.run = ev.run
  s.activity = activityApply(s.activity, ev)
  const out = emitter(s, ev)
  stageEvent(s, out, ev)
  const { stage, seq } = out.done()
  s.stage = stage
  s.seq = seq
  return s
}

// replayOrgBubble builds the adapter state from recorded bus events.
export function replayOrgBubble(events, opts) {
  let s = initialOrgBubble(opts)
  for (const ev of events || []) s = applyOrgBusEvent(s, ev)
  return s
}

// pendingByRole counts what each role is waiting on a person for.
export function pendingByRole(activity) {
  const out = {}
  const bump = (role) => { if (role) out[role] = (out[role] || 0) + 1 }
  for (const q of activity?.questions || []) bump(q.role)
  for (const a of activity?.approvals || []) bump(a.role)
  for (const g of activity ? pendingGates(activity) : []) bump(g.role)
  return out
}

// orgStageOf is the stage to render: the lead named after the boss and on
// the boss's runtime, and each node flagged "needs you" exactly while its
// role has a question, approval or gate pending. access maps a role to its
// full-access entry (fullAccess.jsx), shown as a badge on its node.
export function orgStageOf(state, access = {}) {
  const stage = state.stage
  const waiting = pendingByRole(state.activity)
  const boss = state.roles[state.boss]
  const nodes = {}
  for (const id of stage.order) {
    const n = stage.nodes[id]
    const role = id === LEAD_ID ? state.boss : id
    const needsYou = n.native ? n.needsYou : !!waiting[role]
    let next = n.needsYou === needsYou ? n : { ...n, needsYou }
    if (id === LEAD_ID && boss) {
      next = { ...next, role: boss.title || boss.id, agentType: boss.type || '', runtime: next.runtime || boss.runtime, model: next.model || boss.model }
    }
    if (access[role] && next.fullAccess !== access[role]) next = { ...next, fullAccess: access[role] }
    nodes[id] = next
  }
  return { ...stage, nodes }
}

// ── Collapsed bubbles ─────────────────────────────────────────────────────
// A collapsed org bubble keeps only the orgActivity state and an unread
// count, not the stage.

export function emptyOrgSummary(org) {
  return { activity: activityInitial([], { org }), unread: 0 }
}

// applyOrgSummaryEvent folds one bus event into a collapsed org bubble's
// summary. The boss saying something while the bubble is collapsed counts
// as unread; boss is the root role's id ('' when not known yet).
export function applyOrgSummaryEvent(summary, ev, { boss = '', expanded = false } = {}) {
  const s = summary || emptyOrgSummary(ev?.org)
  const a0 = s.activity
  // A restarted tail replays the run from the top (see replayed above).
  if (ev?.run && ev.run === a0.run && Number(ev.ts) < a0.lastTs) return s
  const activity = activityApply(a0, ev)
  let unread = s.unread
  if (!expanded && ev?.type === 'chat' && boss && ev.from === boss && activity !== s.activity) unread += 1
  return activity === s.activity && unread === s.unread ? s : { ...s, activity, unread }
}

// orgBubbleStatus is the dock's ring for an org: amber while anything waits
// for the person, spinning while a role works, idle otherwise.
export function orgBubbleStatus(summary) {
  const a = summary?.activity
  if (!a) return 'idle'
  const needs = (a.questions?.length || 0) + (a.approvals?.length || 0) + pendingGates(a).length
  if (needs > 0) return 'needs'
  if (a.orgStatus === 'stopped') return 'idle'
  if (Object.values(a.roles || {}).some(r => r.status === 'working')) return 'working'
  return 'idle'
}

// orgNeedsCount is how many items wait for the person.
export function orgNeedsCount(summary) {
  const a = summary?.activity
  if (!a) return 0
  return (a.questions?.length || 0) + (a.approvals?.length || 0) + pendingGates(a).length
}

// ── Chat thread refresh ──────────────────────────────────────────────────

// touchesThread reports whether a live bus event can change the boss thread
// (`org chat history`), so the chat refetches only then.
export function touchesThread(ev, boss = '') {
  switch (ev?.type) {
    case 'chat': return !boss || ev.from === boss
    case 'xorg': case 'message': case 'question': case 'gate': return true
    case 'audit': return ev.reason === 'decision-resolved'
    case 'status': {
      const msg = typeof ev.msg === 'string' ? ev.msg : ''
      return msg === 'question answered' || /^Approval (granted|denied)/.test(msg) || /^org (started|stopped)/.test(msg)
    }
    default: return false
  }
}

// ── Auto-open ────────────────────────────────────────────────────────────

// orgsToAutoOpen picks the running orgs that get a bubble because they ask
// the person something (#229): rows are [{name, running, needs}], open the
// orgs that already have a bubble, dismissed maps an org whose bubble the
// person closed to how many items waited then. Such an org comes back only
// when more items wait than when it was closed; once nothing waits it is
// forgotten. Returns { open: [names], dismissed }.
export function orgsToAutoOpen(rows, open, dismissed = {}) {
  const next = {}
  const names = []
  for (const r of rows || []) {
    if (!r?.name) continue
    const needs = Number(r.needs) || 0
    if (dismissed[r.name] != null && r.running && needs > 0) next[r.name] = dismissed[r.name]
    if (!r.running || needs === 0 || open.has(r.name)) continue
    if (next[r.name] != null && needs <= next[r.name]) continue
    names.push(r.name)
    delete next[r.name]
  }
  return { open: names, dismissed: next }
}
