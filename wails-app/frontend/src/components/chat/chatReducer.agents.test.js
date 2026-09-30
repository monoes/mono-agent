import { describe, it, expect } from 'vitest'
import { chatReducer, initialChatState } from './chatReducer.js'

let seq = 0
const ev = (type, payload) => ({ type: 'event', event: { type, payload, seq: ++seq, at: '2026-09-29T10:00:00Z', conversationId: 'c', turnId: 't' } })
const run = (...actions) => actions.reduce(chatReducer, initialChatState())

describe('dynamic org events (#226)', () => {
  it('keeps worker tool calls out of the lead timeline and tracks each worker', () => {
    const s = run(
      ev('tool.started', { callId: 'lead1', name: 'org_spawn' }),
      ev('agent.spawned', { agentId: 'w1', role: 'Researcher', runtime: 'claude', model: 'haiku', access: 'research', brief: 'find it', why: 'role by rule' }),
      ev('agent.status', { agentId: 'w1', from: 'queued', to: 'working' }),
      ev('tool.started', { agentId: 'w1', callId: 'w1:r1', name: 'Read' }),
      ev('tool.completed', { agentId: 'w1', callId: 'w1:r1', ok: true }),
      ev('agent.reassigned', { agentId: 'w1', fromRuntime: 'claude', fromModel: 'opus', toRuntime: 'claude', toModel: 'haiku', reason: 'auth: no login' }),
      ev('agent.message', { agentId: 'w1', direction: 'brief', text: 'find it' }),
      ev('agent.message', { agentId: 'w1', direction: 'result', text: 'It is in cache.go.' }),
      ev('agent.finished', { agentId: 'w1', outcome: 'done', summary: 'It is in cache.go.', costUsd: 0.002, filesChanged: [] }),
    )
    expect(Object.keys(s.calls)).toEqual(['lead1'])
    expect(s.parts.map(p => p.kind)).toEqual(['tool', 'agent'])
    const w = s.agents.w1
    expect(w).toMatchObject({ role: 'Researcher', status: 'done', tools: 1, lastTool: 'Read', report: 'It is in cache.go.', costUsd: 0.002, model: 'haiku' })
    expect(w.reassigned).toContain('claude/opus')
  })

  it('adds one agent part per worker even if spawned is replayed', () => {
    const spawn = { agentId: 'w1', role: 'Coder' }
    let s = run(ev('agent.spawned', spawn))
    s = chatReducer(s, { type: 'event', event: { type: 'agent.spawned', payload: spawn, seq: ++seq, conversationId: 'c', turnId: 't' } })
    expect(s.parts.filter(p => p.kind === 'agent')).toHaveLength(1)
  })
})

describe('worker questions (#256)', () => {
  it('opens a question and clears it on the user answer', () => {
    let s = run(
      ev('agent.spawned', { agentId: 'w2', role: 'Coder' }),
      ev('agent.status', { agentId: 'w2', to: 'waiting_user', detail: 'q1' }),
      ev('agent.message', { agentId: 'w2', direction: 'question', questionId: 'q1', from: 'w2', to: 'user', text: 'Which DB?' }),
    )
    expect(s.agents.w2.question).toEqual({ id: 'q1', text: 'Which DB?' })
    s = chatReducer(s, ev('agent.message', { agentId: 'w2', direction: 'followup', questionId: 'q1', from: 'user', to: 'w2', text: 'Postgres' }))
    expect(s.agents.w2.question).toBeNull()
  })
})
