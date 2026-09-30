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
    // Kept once, for the org stage's drawer.
    expect(s.agentCalls['w1:r1']).toMatchObject({ agentId: 'w1', name: 'Read', status: 'completed', ok: true })
    expect(s.parts.map(p => p.kind)).toEqual(['tool', 'agent'])
    const w = s.agents.w1
    expect(w).toMatchObject({ role: 'Researcher', status: 'done', tools: 1, lastTool: 'Read', report: 'It is in cache.go.', costUsd: 0.002, model: 'haiku' })
    expect(w.reassigned).toContain('claude/opus')
  })

  it('leaves the lead\'s lease reports to the stage', () => {
    const s = run(ev('agent.status', { agentId: 'lead', from: 'working', to: 'working', leases: ['write'] }))
    expect(s.agents).toEqual({})
    expect(s.stage.nodes.lead.leases).toEqual(['write'])
  })

  it('adds one agent part per worker even if spawned is replayed', () => {
    const spawn = { agentId: 'w1', role: 'Coder' }
    let s = run(ev('agent.spawned', spawn))
    s = chatReducer(s, { type: 'event', event: { type: 'agent.spawned', payload: spawn, seq: ++seq, conversationId: 'c', turnId: 't' } })
    expect(s.parts.filter(p => p.kind === 'agent')).toHaveLength(1)
  })

  it('keeps a worker\'s text and usage out of the lead\'s timeline and usage (#257, #258)', () => {
    const s = run(
      ev('agent.spawned', { agentId: 'w1', role: 'Coder' }),
      ev('assistant.delta', { agentId: 'w1', partId: 'w1:p1', text: 'worker text' }),
      ev('usage.updated', { agentId: 'w1', inputTokens: 5, outputTokens: 1, costUsd: 0.03 }),
      ev('assistant.delta', { partId: 'part-1', text: 'lead text' }),
      ev('usage.updated', { inputTokens: 50, outputTokens: 9, costUsd: 0.1 }),
    )
    expect(s.parts.filter(p => p.kind === 'text').map(p => p.text)).toEqual(['lead text'])
    expect(s.usage.costUsd).toBe(0.1)
    expect(s.agents.w1.costUsd).toBe(0.03)
    expect(s.stage.nodes.w1.parts).toEqual([{ kind: 'text', partId: 'w1:p1', text: 'worker text' }])
  })
})
