import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  initialState, applyEvent, replay, parseAddress, xorgKey,
  roleActivity, recentEdges, pendingGates, pendingCounts,
} from './orgActivity.js'

// Real bus captures from the Org Arena runs (plan §14), scrubbed: chat events
// dropped, asset content removed, paths replaced, long strings truncated.
function fixture(name) {
  const path = fileURLToPath(new URL(`./__fixtures__/${name}.jsonl`, import.meta.url))
  return readFileSync(path, 'utf8').split('\n').filter(Boolean).map(l => JSON.parse(l))
}

const STUDIO_ROLES = ['cto', 'dev', 'qa']
const HERALD_ROLES = ['editor', 'reviewer', 'writer', 'judge']

describe('replaying recorded buses to final role states', () => {
  it('anvil smoke test: cto idle, dev still working, qa never started', () => {
    const s = replay(fixture('anvil-smoke'), STUDIO_ROLES, { org: 'anvil' })
    expect(s.roles.cto.status).toBe('idle')
    expect(s.roles.dev.status).toBe('working')
    expect(s.roles.qa.status).toBe('offline')
    expect(s.orgStatus).toBe('running')
    expect(s.run).toBe('run-20260916062958-rblu')
    expect(s.usage.tokens).toBe(6604)
    expect(s.usage.costUsd).toBeCloseTo(0.44117395, 6)
    expect(s.roles.dev.assetCount).toBe(9)
    expect(s.roles.dev.assets).toHaveLength(3)
    expect(s.roles.dev.assets[0].path.startsWith('<root>')).toBe(true)
    // Every Bash approval was granted by the end of the capture.
    expect(pendingCounts(s)).toEqual({ approvals: 0, questions: 0, gates: 0 })
    expect(s.edges['cto->dev'].count).toBe(1)
    expect(s.edges['cto->herald:editor']).toMatchObject({ kind: 'xorg', external: true, count: 3 })
    expect(s.edges['herald:editor->cto']).toMatchObject({ kind: 'xorg', external: true, count: 4 })
  })

  it('anvil smoke test mid-run: three queued Bash approvals for dev, cleared by one grant', () => {
    const events = fixture('anvil-smoke')
    const grantIdx = events.findIndex(e => e.type === 'status' && e.msg === 'Approval granted for Bash')
    const before = replay(events.slice(0, grantIdx), STUDIO_ROLES, { org: 'anvil' })
    expect(before.approvals.map(a => [a.role, a.action])).toEqual([['dev', 'Bash'], ['dev', 'Bash'], ['dev', 'Bash']])
    expect(roleActivity(before, 'dev').pendingApprovals).toBe(3)
    const after = applyEvent(before, events[grantIdx])
    expect(after.approvals).toEqual([])
  })

  it('forge smoke test: both roles idle, one session error counted', () => {
    const s = replay(fixture('forge-smoke'), STUDIO_ROLES, { org: 'forge' })
    expect(s.roles.cto.status).toBe('idle')
    expect(s.roles.dev.status).toBe('idle')
    expect(s.counters.errors).toBe(1)
    expect(s.usage.tokens).toBe(18826)
    expect(s.roles.dev.assetCount).toBe(6)
  })

  it('herald smoke test: editor and reviewer idle, writer and judge never started', () => {
    const s = replay(fixture('herald-smoke'), HERALD_ROLES, { org: 'herald' })
    expect(s.roles.editor.status).toBe('idle')
    expect(s.roles.reviewer.status).toBe('idle')
    expect(s.roles.writer.status).toBe('offline')
    expect(s.roles.judge.status).toBe('offline')
    expect(s.counters.idleNudges).toBe(1)
    expect(s.edges['editor->reviewer'].count).toBe(1)
    expect(s.edges['anvil:cto->editor'].count).toBe(3)
    expect(s.edges['editor->forge:cto'].count).toBe(5)
    expect(s.usage.tokens).toBe(7679)
  })

  it('herald rehearsal: judge holds the pending publish gate', () => {
    const s = replay(fixture('herald-rehearsal'), HERALD_ROLES, { org: 'herald' })
    expect(s.roles.editor.status).toBe('idle')
    expect(s.roles.judge.status).toBe('idle')
    expect(s.roles.reviewer.status).toBe('idle')
    expect(s.roles.writer.status).toBe('offline')
    const gates = pendingGates(s)
    expect(gates).toHaveLength(1)
    expect(gates[0]).toMatchObject({ gateId: 'gate-1789544435842-o5x5', role: 'judge', name: 'publish-launch-post' })
    expect(roleActivity(s, 'judge').pendingGate?.gateId).toBe('gate-1789544435842-o5x5')
    expect(roleActivity(s, 'editor').pendingGate).toBeNull()
    expect(s.edges['editor->judge'].count).toBe(10)
    expect(s.edges['forge:cto->editor'].count).toBe(11)
    expect(s.roles.reviewer.assetCount).toBe(6)
    expect(s.counters.duplicatesDropped).toBe(0)
  })

  it('replays identically when the same events are applied twice (ids dedupe)', () => {
    const events = fixture('herald-smoke')
    const once = replay(events, HERALD_ROLES, { org: 'herald' })
    let twice = once
    for (const e of events) twice = applyEvent(twice, e)
    expect(twice.edges).toEqual(once.edges)
    expect(twice.usage).toEqual(once.usage)
  })
})

describe('xorg dedupe (C-43)', () => {
  it('counts a message once when both buses deliver their copy', () => {
    const anvil = fixture('anvil-smoke').filter(e => e.type === 'xorg')
    const herald = fixture('herald-smoke').filter(e => e.type === 'xorg' && (e.from.startsWith('anvil') || e.to.startsWith('anvil')))
    expect(anvil.length).toBe(7)
    expect(herald.length).toBe(7)
    const merged = [...anvil, ...herald].sort((a, b) => a.ts - b.ts)
    const s = replay(merged, HERALD_ROLES, { org: 'herald' })
    expect(s.counters.duplicatesDropped).toBe(7)
    expect(s.edges['anvil:cto->editor'].count).toBe(3)
    expect(s.edges['editor->anvil:cto'].count).toBe(4)
  })

  it('prefers messageId when M3 stamps one', () => {
    const a = { id: 'a1', ts: 1000, type: 'xorg', from: 'hq:ceo', to: 'sales:lead', subject: 's', msg: 'x', data: { messageId: 'msg-1' } }
    const b = { ...a, id: 'b1', ts: 90_000, msg: 'different copy text' }
    const s = replay([a, b], ['lead'], { org: 'sales' })
    expect(s.edges['hq:ceo->lead'].count).toBe(1)
    expect(xorgKey(a)).toBe('id:msg-1')
  })

  it('keeps two identical messages sent far apart', () => {
    const a = { id: 'a1', ts: 1000, type: 'xorg', from: 'hq:ceo', to: 'sales:lead', subject: 'ping', msg: 'status?' }
    const b = { ...a, id: 'a2', ts: 1000 + 60_000 }
    const s = replay([a, b], ['lead'], { org: 'sales' })
    expect(s.edges['hq:ceo->lead'].count).toBe(2)
  })
})

describe('gates, questions, decisions, runs', () => {
  const base = () => initialState([{ id: 'boss' }, { id: 'bot', rest: { kind: 'endpoint' } }], { org: 'growth' })

  it('marks endpoint roles from canvas nodes', () => {
    expect(base().roles.bot.endpoint).toBe(true)
    expect(base().roles.boss.endpoint).toBe(false)
  })

  it('clears a gate on gate-approved and on a decision-resolved audit', () => {
    let s = applyEvent(base(), { id: '1', ts: 10, type: 'gate', from: 'boss', data: { gateId: 'g1', name: 'ship' } })
    expect(roleActivity(s, 'boss').pendingGate.name).toBe('ship')
    s = applyEvent(s, { id: '2', ts: 20, type: 'gate', from: 'boss', reason: 'gate-approved', data: { gateId: 'g1', approved: true } })
    expect(pendingGates(s)).toEqual([])
    expect(s.gates.g1.status).toBe('approved')

    s = applyEvent(s, { id: '3', ts: 30, type: 'gate', from: 'boss', data: { gateId: 'g2', name: 'spend' } })
    s = applyEvent(s, { id: '4', ts: 40, type: 'audit', from: 'boss', reason: 'decision-resolved', data: { kind: 'gate', ref: 'g2', resolver: 'model:claude', verdict: 'denied' } })
    expect(s.gates.g2.status).toBe('rejected')
    expect(s.decisions.at(-1)).toMatchObject({ kind: 'gate', resolver: 'model:claude', verdict: 'denied' })
  })

  it('separates ask_human questions from tool approvals (C-45)', () => {
    let s = applyEvent(base(), { id: '1', ts: 1, type: 'question', from: 'boss', data: { questionId: 'q1', question: 'Which market?' } })
    s = applyEvent(s, { id: '2', ts: 2, type: 'question', from: 'boss', data: { action: 'monoagent__automation_publish', requestId: 'apr-1' } })
    expect(pendingCounts(s)).toEqual({ approvals: 1, questions: 1, gates: 0 })
    s = applyEvent(s, { id: '3', ts: 3, type: 'status', from: 'boss', msg: 'question answered', data: { questionId: 'q1' } })
    s = applyEvent(s, { id: '4', ts: 4, type: 'audit', from: 'boss', reason: 'decision-resolved', data: { kind: 'approval', ref: 'apr-1', resolver: 'human', verdict: 'approved' } })
    expect(pendingCounts(s)).toEqual({ approvals: 0, questions: 0, gates: 0 })
  })

  it('records chain traces from tool events', () => {
    const s = applyEvent(base(), { id: '1', ts: 5, type: 'tool', from: 'boss', tool: 'mcp__org__monoagent__automation_publish', decision: 'allow', data: { chain_id: 'chn_a', hop: 2 } })
    expect(s.chains.chn_a).toEqual([{ role: 'boss', tool: 'mcp__org__monoagent__automation_publish', ts: 5, decision: 'allow', hop: 2 }])
    expect(s.roles.boss.lastTool).toBe('mcp__org__monoagent__automation_publish')
  })

  it('resets activity when a new run starts on the same bus', () => {
    let s = applyEvent(base(), { id: '1', ts: 1, run: 'run-a', org: 'growth', type: 'status', from: 'boss', reason: 'state-change', data: { from: 'idle', to: 'working' } })
    s = applyEvent(s, { id: '2', ts: 2, run: 'run-a', org: 'growth', type: 'usage', from: 'boss', data: { tokens: 10, cost_usd: 0.1 } })
    s = applyEvent(s, { id: '3', ts: 100, run: 'run-b', org: 'growth', type: 'status', msg: 'org started (2 agents)' })
    expect(s.run).toBe('run-b')
    expect(s.usage.tokens).toBe(0)
    expect(s.roles.boss.status).toBe('offline')
    expect(s.roles.bot.endpoint).toBe(true)
    expect(s.orgStatus).toBe('running')
  })

  it('org stopped drops working roles to idle', () => {
    let s = applyEvent(base(), { id: '1', ts: 1, type: 'status', from: 'boss', reason: 'state-change', data: { from: 'idle', to: 'working' } })
    s = applyEvent(s, { id: '2', ts: 2, type: 'status', msg: 'org stopped' })
    expect(s.orgStatus).toBe('stopped')
    expect(s.roles.boss.status).toBe('idle')
  })

  it('recentEdges uses event time, not the wall clock', () => {
    let s = applyEvent(base(), { id: '1', ts: 1000, type: 'message', from: 'boss', to: 'bot', subject: 'go' })
    s = applyEvent(s, { id: '2', ts: 20_000, type: 'message', from: 'bot', to: 'boss', subject: 're: go' })
    expect(recentEdges(s).map(e => `${e.from}->${e.to}`)).toEqual(['bot->boss'])
    expect(recentEdges(s, 1500).length).toBe(2)
  })

  it('never mutates the previous state and ignores junk', () => {
    const s0 = base()
    const frozen = JSON.stringify(s0)
    applyEvent(s0, { id: 'x', ts: 1, type: 'usage', from: 'boss', data: { tokens: 5 } })
    expect(JSON.stringify(s0)).toBe(frozen)
    expect(applyEvent(s0, null)).toBe(s0)
    expect(applyEvent(s0, { nope: true })).toBe(s0)
  })

  it('parses addresses', () => {
    expect(parseAddress('sales:lead', 'hq')).toEqual({ org: 'sales', role: 'lead' })
    expect(parseAddress('lead', 'hq')).toEqual({ org: 'hq', role: 'lead' })
    expect(parseAddress('', 'hq')).toEqual({ org: 'hq', role: null })
  })
})
