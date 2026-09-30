import { describe, it, expect } from 'vitest'
import { stageReducer, replayStage, isIdleVeteran } from './orgStage.js'

describe('spawn trees and veterans (#230)', () => {
  const ev = (seq, type, payload) => ({ seq, at: `2026-09-30T10:00:${String(seq).padStart(2, '0')}.000Z`, type, payload })
  const events = [
    ev(1, 'agent.spawned', { agentId: 'w1', role: 'Researcher', runtime: 'claude', model: 'opus', veteran: true }),
    ev(2, 'agent.status', { agentId: 'w1', to: 'idle', detail: 'veteran' }),
    ev(3, 'agent.spawned', { agentId: 'w2', parentId: 'w1', role: 'Tester', veteran: true }),
    ev(4, 'agent.status', { agentId: 'w2', to: 'idle', detail: 'veteran' }),
    ev(5, 'turn.started', { runtime: 'claude' }),
    ev(6, 'agent.spawned', { agentId: 'w3', role: 'Coder', allowSpawn: true }),
    ev(7, 'agent.spawned', { agentId: 'w4', parentId: 'w3', role: 'Reviewer' }),
    ev(8, 'agent.message', { agentId: 'w4', direction: 'brief', from: 'w3', to: 'w4', text: 'review it' }),
  ]

  it('draws a sub-worker under the worker that spawned it', () => {
    const s = replayStage(events)
    expect(s.nodes.w4.parentId).toBe('w3')
    expect(s.edges.map(e => e.id)).toContain('w3->w4')
    expect(s.flights.map(f => `${f.from}>${f.to}`)).toContain('w3>w4')
  })

  it('keeps veterans idle until the lead messages one, and leaves idle ones out of the scoreboard', () => {
    let s = replayStage(events)
    expect(isIdleVeteran(s.nodes.w1)).toBe(true)
    expect(isIdleVeteran(s.nodes.w2)).toBe(true)
    expect(s.edges.map(e => e.id)).toContain('w1->w2')
    expect(s.feed.filter(f => f.type === 'veteran').map(f => f.agentId)).toEqual(['w1', 'w2'])
    s = [
      ev(9, 'agent.status', { agentId: 'w1', from: 'idle', to: 'queued' }),
      ev(10, 'agent.status', { agentId: 'w1', from: 'queued', to: 'working' }),
      ev(11, 'agent.finished', { agentId: 'w1', outcome: 'done', summary: 'evicted in lru.go' }),
      ev(12, 'turn.finished', { status: 'completed' }),
    ].reduce(stageReducer, s)
    expect(s.nodes.w1.veteran).toBe(true)
    expect(isIdleVeteran(s.nodes.w1)).toBe(false)
    expect(isIdleVeteran(s.nodes.w2)).toBe(true)
    // w1 (messaged), w3 and w4 count; w2 stayed idle.
    expect(s.scoreboard.agents).toBe(3)
  })
})
