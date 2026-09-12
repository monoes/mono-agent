import { describe, it, expect } from 'vitest'
import { chatReducer, initialChatState } from './chatReducer.js'

// Builds a minimal chat:event envelope — tests only need type/payload/seq
// for the reducer; conversationId/turnId matching the current scope is
// exercised separately.
function ev(type, payload, seq, extra = {}) {
  return {
    version: 1,
    profileId: 'default',
    conversationId: 'conv-1',
    turnId: 'turn-1',
    seq,
    at: `2026-09-12T00:00:0${seq}.000Z`,
    type,
    payload,
    ...extra,
  }
}

function scoped() {
  return chatReducer(initialChatState(), { type: 'scope', scope: { conversationId: 'conv-1', turnId: 'turn-1' } })
}

function apply(state, ...events) {
  return events.reduce((s, e) => chatReducer(s, { type: 'event', event: e }), state)
}

describe('chatReducer', () => {
  it('starts idle with no scope', () => {
    const state = initialChatState()
    expect(state.scope).toBeNull()
    expect(state.parts).toEqual([])
    expect(state.terminal).toBeNull()
  })

  it('rebinding scope resets prior turn state', () => {
    let state = scoped()
    state = apply(state, ev('assistant.delta', { partId: 'p1', text: 'hello' }, 1))
    expect(state.parts).toHaveLength(1)

    state = chatReducer(state, { type: 'scope', scope: { conversationId: 'conv-2', turnId: 'turn-2' } })
    expect(state.parts).toEqual([])
    expect(state.scope).toEqual({ conversationId: 'conv-2', turnId: 'turn-2' })
  })

  it('ignores events scoped to a different conversation/turn', () => {
    const state = scoped()
    const next = apply(state, ev('assistant.delta', { partId: 'p1', text: 'hi' }, 1, { turnId: 'someone-elses-turn' }))
    expect(next).toBe(state) // unchanged reference — no-op
  })

  it('interleaves text and tool steps in actual arrival order', () => {
    const state = apply(scoped(),
      ev('assistant.delta', { partId: 'p1', text: 'Let me check.' }, 1),
      ev('tool.started', { callId: 'c1', name: 'get_workflow', arguments: { id: 'wf1' } }, 2),
      ev('tool.completed', { callId: 'c1', ok: true, result: 'found it' }, 3),
      ev('assistant.delta', { partId: 'p2', text: 'Done.' }, 4),
    )
    expect(state.parts.map(p => p.kind)).toEqual(['text', 'tool', 'text'])
    expect(state.parts[0].text).toBe('Let me check.')
    expect(state.parts[1].callId).toBe('c1')
    expect(state.calls['c1']).toMatchObject({ name: 'get_workflow', status: 'completed', ok: true, result: 'found it' })
    expect(state.parts[2].text).toBe('Done.')
  })

  it('joins adjacent assistant.delta events sharing a partId into one growing block', () => {
    const state = apply(scoped(),
      ev('assistant.delta', { partId: 'p1', text: 'Hel' }, 1),
      ev('assistant.delta', { partId: 'p1', text: 'lo ' }, 2),
      ev('assistant.delta', { partId: 'p1', text: 'world' }, 3),
    )
    expect(state.parts).toHaveLength(1)
    expect(state.parts[0].text).toBe('Hello world')
  })

  it('tracks same-name parallel tool calls independently by callId, never by name', () => {
    const state = apply(scoped(),
      ev('tool.started', { callId: 'c1', name: 'search', arguments: { q: 'a' } }, 1),
      ev('tool.started', { callId: 'c2', name: 'search', arguments: { q: 'b' } }, 2),
      ev('tool.completed', { callId: 'c2', ok: true, result: 'result-b' }, 3),
      ev('tool.completed', { callId: 'c1', ok: true, result: 'result-a' }, 4),
    )
    expect(state.parts.filter(p => p.kind === 'tool')).toHaveLength(2)
    expect(state.calls['c1'].result).toBe('result-a')
    expect(state.calls['c2'].result).toBe('result-b')
  })

  it('retains an unmatched tool.completed (no prior tool.started) as its own step', () => {
    const state = apply(scoped(),
      ev('tool.completed', { callId: 'orphan', ok: false, result: '' }, 1),
    )
    expect(state.parts).toHaveLength(1)
    expect(state.parts[0].callId).toBe('orphan')
    // Empty string / false are valid results, not "missing" — must not be
    // coerced to null/undefined.
    expect(state.calls['orphan'].result).toBe('')
    expect(state.calls['orphan'].ok).toBe(false)
  })

  it('captures start/finish timestamps on a call for elapsed-time display', () => {
    const state = apply(scoped(),
      ev('tool.started', { callId: 'c1', name: 'search', arguments: {} }, 1),
      ev('tool.completed', { callId: 'c1', ok: true, result: 'done' }, 2),
    )
    expect(state.calls['c1'].startedAt).toBe('2026-09-12T00:00:01.000Z')
    expect(state.calls['c1'].finishedAt).toBe('2026-09-12T00:00:02.000Z')
  })

  it('a still-running call has a startedAt but no finishedAt yet', () => {
    const state = apply(scoped(),
      ev('tool.started', { callId: 'c1', name: 'slow_tool', arguments: {} }, 1),
    )
    expect(state.calls['c1'].startedAt).toBe('2026-09-12T00:00:01.000Z')
    expect(state.calls['c1'].finishedAt).toBeUndefined()
  })

  it('leaves both timestamps unset on an unmatched tool.completed — no start was ever seen, so no duration can be shown', () => {
    const state = apply(scoped(),
      ev('tool.completed', { callId: 'orphan', ok: false, result: '' }, 1),
    )
    expect(state.calls['orphan'].startedAt).toBeUndefined()
    expect(state.calls['orphan'].finishedAt).toBeUndefined()
  })

  it('treats a null ok and empty result as valid, distinct from unset', () => {
    const state = apply(scoped(),
      ev('tool.started', { callId: 'c1', name: 'noop', arguments: null }, 1),
      ev('tool.completed', { callId: 'c1', ok: null, result: '' }, 2),
    )
    expect(state.calls['c1'].ok).toBeNull()
    expect(state.calls['c1'].result).toBe('')
    expect(state.calls['c1'].status).toBe('completed')
  })

  it('produces a tool-only response with no text parts at all', () => {
    const state = apply(scoped(),
      ev('tool.started', { callId: 'c1', name: 'create_workflow', arguments: {} }, 1),
      ev('tool.completed', { callId: 'c1', ok: true, result: '{"workflow_id":"wf1"}' }, 2),
      ev('turn.finished', { status: 'completed', reason: 'end_turn', exitCode: 0, historySaved: true }, 3),
    )
    expect(state.parts.every(p => p.kind === 'tool')).toBe(true)
    expect(state.terminal.status).toBe('completed')
  })

  it('ignores a duplicate/replayed event at or below the highest applied sequence', () => {
    let state = apply(scoped(), ev('assistant.delta', { partId: 'p1', text: 'hello' }, 1))
    const replayed = apply(state, ev('assistant.delta', { partId: 'p1', text: 'hello' }, 1))
    expect(replayed).toBe(state) // no-op, not double-appended
    expect(replayed.parts[0].text).toBe('hello')
  })

  it('a nonfatal notice does not erase already-observed parts', () => {
    const state = apply(scoped(),
      ev('assistant.delta', { partId: 'p1', text: 'partial work' }, 1),
      ev('notice', { code: 'stale-stop', message: 'stop arrived after completion', severity: 'warning' }, 2),
    )
    expect(state.parts[0].text).toBe('partial work')
    expect(state.notices).toHaveLength(1)
    expect(state.notices[0].severity).toBe('warning')
  })

  it('preserves partial content when a turn is stopped/cancelled', () => {
    const state = apply(scoped(),
      ev('assistant.delta', { partId: 'p1', text: 'unfinished' }, 1),
      ev('tool.started', { callId: 'c1', name: 'slow_tool', arguments: {} }, 2),
      ev('turn.finished', { status: 'cancelled', reason: 'stopped', exitCode: null, historySaved: true }, 3),
    )
    expect(state.parts[0].text).toBe('unfinished')
    // The interrupted tool call stays visible rather than being dropped —
    // its terminal "no result arrived" state is left to the UI layer.
    expect(state.calls['c1'].status).toBe('started')
    expect(state.terminal).toEqual({ status: 'cancelled', reason: 'stopped', exitCode: null, historySaved: true })
  })

  it('records session binding from session.bound', () => {
    const state = apply(scoped(), ev('session.bound', { runtime: 'claude', sessionId: 'th_123' }, 1))
    expect(state.session).toEqual({ runtime: 'claude', sessionId: 'th_123' })
  })

  it('records the latest usage snapshot without summing across events', () => {
    const state = apply(scoped(),
      ev('usage.updated', { inputTokens: 100, outputTokens: 20, costUsd: null, source: 'assistant' }, 1),
      ev('usage.updated', { inputTokens: 150, outputTokens: 45, costUsd: 0.002, source: 'result' }, 2),
    )
    expect(state.usage).toEqual({ inputTokens: 150, outputTokens: 45, costUsd: 0.002, source: 'result' })
  })

  it('tracks the timestamp of the most recently applied event, any type', () => {
    let state = apply(scoped(), ev('turn.started', { backend: 'agent', text: 'hi' }, 1))
    expect(state.lastEventAt).toBe('2026-09-12T00:00:01.000Z')
    state = apply(state, ev('tool.started', { callId: 'c1', name: 'search', arguments: {} }, 2))
    expect(state.lastEventAt).toBe('2026-09-12T00:00:02.000Z')
  })

  it('distinguishes an explicitly-absent usage metric from zero', () => {
    const state = apply(scoped(), ev('usage.updated', { inputTokens: 0, outputTokens: null, costUsd: null, source: 'result' }, 1))
    expect(state.usage.inputTokens).toBe(0)
    expect(state.usage.outputTokens).toBeNull()
  })
})
