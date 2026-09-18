// Pure reducer for the holding-org group view (Phase 6): cross-org message
// arcs between org cards, per-org live status, and live spend. Fed by the
// `org:event` streams of every member org, so each cross-org message arrives
// twice (sender and recipient bus, C-43) and is counted once.
import { XORG_DEDUPE_MS, parseAddress, xorgKey } from '../orgdesigner/orgActivity.js'

export const ARC_RECENT_MS = 10_000

function orgEntry() {
  return { status: 'unknown', costUsd: 0, tokens: 0, lastTs: 0, sent: 0, received: 0 }
}

export function initialGroupTraffic(members = []) {
  const orgs = {}
  for (const m of members) if (m) orgs[m] = orgEntry()
  return { orgs, arcs: {}, seen: {}, duplicatesDropped: 0, lastTs: 0 }
}

/** Fold one event from any member org's bus. `busOrg` defaults to event.org. */
export function applyGroupEvent(state, event, busOrg = event?.org) {
  if (!event || !event.type) return state
  const ts = Number(event.ts) || state.lastTs
  let s = { ...state, lastTs: Math.max(state.lastTs, ts) }
  const bump = (org, patch) => {
    if (!org || !s.orgs[org]) return
    s = { ...s, orgs: { ...s.orgs, [org]: { ...s.orgs[org], lastTs: Math.max(s.orgs[org].lastTs, ts), ...patch(s.orgs[org]) } } }
  }

  if (event.type === 'xorg') {
    const key = xorgKey(event)
    const seenAt = s.seen[key]
    if (seenAt !== undefined && (key.startsWith('id:') || Math.abs(ts - seenAt) <= XORG_DEDUPE_MS)) {
      return { ...s, duplicatesDropped: s.duplicatesDropped + 1 }
    }
    const seen = {}
    for (const [k, t] of Object.entries(s.seen)) {
      if (k.startsWith('id:') || Math.abs(ts - t) <= XORG_DEDUPE_MS * 12) seen[k] = t
    }
    seen[key] = ts
    s = { ...s, seen }
    const from = parseAddress(event.from, busOrg).org
    const to = parseAddress(event.to, busOrg).org
    if (!from || !to || from === to || !s.orgs[from] || !s.orgs[to]) return s
    const arcKey = `${from}->${to}`
    const prev = s.arcs[arcKey]
    s = { ...s, arcs: { ...s.arcs, [arcKey]: { from, to, count: (prev?.count || 0) + 1, lastTs: ts, subject: event.subject ?? '' } } }
    bump(from, o => ({ sent: o.sent + 1 }))
    bump(to, o => ({ received: o.received + 1 }))
    return s
  }

  if (event.type === 'status' && !event.from && typeof event.msg === 'string') {
    if (/^org started/.test(event.msg)) bump(busOrg, () => ({ status: 'running' }))
    else if (event.msg === 'org stopped') bump(busOrg, () => ({ status: 'stopped' }))
    return s
  }

  // Spend counts only roles of the bus's own org — cost tables and buses can
  // name foreign senders (C-48), which must not be double counted.
  if (event.type === 'usage' && event.from && !String(event.from).includes(':')) {
    const cost = Number(event.data?.cost_usd) || 0
    const tokens = Number(event.data?.tokens) || 0
    bump(busOrg, o => ({ costUsd: o.costUsd + cost, tokens: o.tokens + tokens }))
    return s
  }

  bump(busOrg, () => ({}))
  return s
}

/** Arcs with traffic in the last windowMs before `now`. */
export function recentArcs(state, now, windowMs = ARC_RECENT_MS) {
  return Object.values(state.arcs).filter(a => now - a.lastTs <= windowMs)
}

/** Quadratic arc path between two points, bowed to one side so A→B and B→A don't overlap. */
export function arcPath(ax, ay, bx, by, bow = 0.18) {
  const mx = (ax + bx) / 2
  const my = (ay + by) / 2
  const dx = bx - ax
  const dy = by - ay
  const cx = mx - dy * bow
  const cy = my + dx * bow
  return `M${ax.toFixed(1)},${ay.toFixed(1)} Q${cx.toFixed(1)},${cy.toFixed(1)} ${bx.toFixed(1)},${by.toFixed(1)}`
}
