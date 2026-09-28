import { parseAddress } from '../orgdesigner/orgActivity.js'

// Full-access roles' native tool calls on the org bus (#205 item 6):
// monomind 2.17.0 relays them as `tool_activity` events, the same shape as
// in agent exec — {id, phase: "start"|"end", name, input, output, ok,
// denied, cancelled, duration_ms, output_truncated, parent_tool_use_id} —
// plus the bus's own from (the role's address) and ts. buildToolActivity
// folds them into one chatReducer-shaped state per role, so the Orgs tab
// renders them with the coder chat's cards (ChatTimeline/NativeToolCard).

export const TOOL_ACTIVITY = 'tool_activity'

export function isToolActivity(e) {
  return e?.type === TOOL_ACTIVITY
}

function roleOf(e) {
  return parseAddress(e.from || e.role || '').role || e.role || 'unknown role'
}

function at(e) {
  const ts = Number(e.ts)
  if (ts > 0) return new Date(ts).toISOString()
  return typeof e.ts === 'string' ? e.ts : undefined
}

function resultText(output) {
  if (output == null) return ''
  return typeof output === 'string' ? output : JSON.stringify(output, null, 2)
}

// buildToolActivity returns [{ role, state }] in order of each role's first
// call; state is { parts, calls, notices } as ChatTimeline reads it.
export function buildToolActivity(events) {
  const byRole = new Map()
  const roleOfCall = new Map()
  for (const e of Array.isArray(events) ? events : []) {
    if (!isToolActivity(e) || !e.id) continue
    let role = roleOf(e)
    // An end without a from still belongs to the role that started it.
    if (e.phase === 'end' && roleOfCall.has(e.id)) role = roleOfCall.get(e.id)
    if (!byRole.has(role)) byRole.set(role, { parts: [], calls: {}, notices: [] })
    const st = byRole.get(role)
    const prev = st.calls[e.id]
    if (!prev) {
      st.parts.push({ kind: 'tool', callId: e.id })
      roleOfCall.set(e.id, role)
    }
    const base = prev || { callId: e.id, name: e.name || 'unknown', arguments: null, status: 'started', ok: null, result: null, native: true }
    if (e.phase === 'end') {
      st.calls[e.id] = {
        ...base,
        name: base.name === 'unknown' && e.name ? e.name : base.name,
        status: 'completed',
        ok: typeof e.ok === 'boolean' ? e.ok : (e.denied || e.cancelled ? false : null),
        result: resultText(e.output),
        finishedAt: at(e),
        ...(e.output_truncated || e.input_truncated ? { truncated: true } : {}),
        ...(e.denied ? { denied: true } : {}),
        ...(e.cancelled ? { cancelled: true } : {}),
        ...(e.duration_ms > 0 ? { durationMs: e.duration_ms } : {}),
      }
    } else {
      st.calls[e.id] = {
        ...base,
        name: e.name || base.name,
        arguments: e.input ?? base.arguments,
        startedAt: at(e),
        ...(e.parent_tool_use_id ? { parentCallId: e.parent_tool_use_id } : {}),
        ...(e.input_truncated ? { truncated: true } : {}),
      }
    }
  }
  return [...byRole.entries()].map(([role, state]) => ({ role, state }))
}
