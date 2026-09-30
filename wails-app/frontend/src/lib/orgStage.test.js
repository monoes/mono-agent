import { describe, it, expect } from 'vitest'
import journal from './__fixtures__/orgStageJournal.json'
import {
  stageReducer, replayStage, initialStage, leasesOf, questProgress, structureKey, doingOf, hasTeam, LEAD_ID,
} from './orgStage.js'
import { chatReducer, initialChatState } from '../components/chat/chatReducer.js'
import { reduceTurnEvents } from '../components/chat/useChatStream.js'

const upTo = (seq) => journal.filter(e => e.seq <= seq)
const bySeq = (seq) => journal.find(e => e.seq === seq)
const seqOf = (pred) => journal.find(pred).seq

// live feeds the journal the way useChatStream does: scoped chatReducer,
// with re-deliveries of events it has already applied.
function live(events) {
  let s = chatReducer(initialChatState(), { type: 'scope', scope: { conversationId: 'conv-org', turnId: 'turn-1' } })
  events.forEach((event, i) => {
    s = chatReducer(s, { type: 'event', event })
    if (i % 5 === 0) s = chatReducer(s, { type: 'event', event: events[Math.max(0, i - 3)] }) // a gap-fill overlap
  })
  return s.stage
}

describe('stageReducer over a recorded 4-agent journal', () => {
  const stage = replayStage(journal)

  it('builds the lead, four workers and the native subagent under its caller', () => {
    expect(stage.order).toEqual([LEAD_ID, 'w1', 'w2', 'w3', 'w4', 'native:w1:t1'])
    expect(stage.edges.map(e => e.id)).toEqual(['lead->w1', 'lead->w2', 'lead->w3', 'lead->w4', 'w1->native:w1:t1'])
    const sub = stage.nodes['native:w1:t1']
    expect(sub).toMatchObject({ parentId: 'w1', native: true, role: 'Explore', status: 'done', tools: 1, toolsDone: 1 })
    expect(sub.summary).toBe('The test sleeps 10ms and expects expiry.')
    expect(stage.nodes.lead).toMatchObject({ runtime: 'claude', status: 'done' })
  })

  it('tracks each worker: staffing, status, reassignment, meters and XP', () => {
    expect(stage.nodes.w1).toMatchObject({ role: 'Researcher', model: 'haiku', effort: 'low', access: 'research', status: 'done', pickConfidence: 0.82, jevConfidence: 0.71, tokensIn: 5200, tokensOut: 640, costUsd: 0.0031 })
    // The Read inside the Task call belongs to the subagent, not to w1.
    expect(stage.nodes.w1.tools).toBe(2)
    expect(stage.nodes.w2).toMatchObject({ runtime: 'codex', model: 'gpt-5', status: 'done', costEstimated: true, testsRun: 1, testsPassed: 1 })
    expect(stage.nodes.w2.prevModel).toEqual({ runtime: 'claude', model: 'opus', reason: 'quota: weekly limit reached' })
    expect(stage.nodes.w2.files).toEqual(['/w/internal/cache/cache_test.go'])
    expect(stage.nodes.w3).toMatchObject({ status: 'done', testsRun: 1, testsPassed: 0, limited: false })
    expect(stage.nodes.w4).toMatchObject({ status: 'failed', outcome: 'failed', tools: 0 })
  })

  it('keeps a quest per brief with the turn progress', () => {
    expect(stage.quests.map(q => [q.agentId, q.status])).toEqual([
      ['w1', 'done'], ['w2', 'done'], ['w3', 'done'], ['w4', 'failed'], ['native:w1:t1', 'done'],
    ])
    expect(questProgress(stage)).toEqual({ done: 5, total: 5, ratio: 1 })
    const mid = replayStage(upTo(seqOf(e => e.type === 'agent.status' && e.payload.to === 'waiting_lease')))
    expect(mid.quests.map(q => q.status)).toEqual(['active', 'active', 'queued'])
  })

  it('flies briefs down and results up', () => {
    const kinds = stage.flights.map(f => `${f.kind}:${f.from}>${f.to}`)
    expect(kinds).toEqual([
      'brief:lead>w1', 'brief:lead>w2', 'brief:lead>w3', 'brief:lead>w4',
      'brief:w1>native:w1:t1', 'result:native:w1:t1>w1',
      'result:w1>lead', 'result:w2>lead', 'result:w3>lead',
    ])
  })

  it('shows who holds the pen and the browser as the turn moves', () => {
    const waiting = replayStage(upTo(seqOf(e => e.type === 'agent.status' && e.payload.agentId === 'w2' && e.payload.to === 'working')))
    expect(leasesOf(waiting)).toEqual({ pen: 'w2', browser: null, waiting: [{ id: 'w3', lease: 'write' }] })
    const qa = replayStage(upTo(seqOf(e => e.type === 'agent.status' && e.payload.agentId === 'w3' && e.payload.to === 'working')))
    expect(leasesOf(qa)).toEqual({ pen: 'w3', browser: 'w3', waiting: [] })
    expect(leasesOf(stage)).toEqual({ pen: null, browser: null, waiting: [] })
  })

  it('scores the turn when it finishes', () => {
    expect(stage.scoreboard).toEqual({
      agents: 5, workers: 4, natives: 1, done: 4, failed: 1,
      durationMs: 15000, costUsd: 0.1361, costEstimated: true,
      filesChanged: ['/w/internal/cache/cache_test.go'],
      tools: 11, testsRun: 2, testsPassed: 1, status: 'completed',
    })
    expect(replayStage(upTo(40)).scoreboard).toBeNull()
  })

  it('says what each agent is doing, per tool kind', () => {
    const s = replayStage(upTo(seqOf(e => e.payload.callId === 'w2:s1')))
    expect(s.nodes.w2.doing).toMatchObject({ kind: 'shell', target: 'go test ./internal/cache/...', active: true })
    expect(doingOf('Edit', '', { file_path: '/a/b/chat.go' })).toEqual({ kind: 'edit', name: 'Edit', target: 'chat.go' })
    expect(doingOf('WebFetch', '', '{"url":"https://example.com/x"}')).toEqual({ kind: 'web', name: 'WebFetch', target: 'example.com' })
  })
})

describe('replay equals live', () => {
  it('gives the same stage from the history path, the live path and a one-by-one fold', () => {
    const replayed = replayStage(journal)
    expect(live(journal)).toEqual(replayed)
    expect(reduceTurnEvents(journal).stage).toEqual(replayed)
    expect(journal.reduce(stageReducer, null)).toEqual(replayed)
  })

  it('is deterministic: two folds of the same journal are equal', () => {
    expect(replayStage(journal)).toEqual(replayStage(structuredClone(journal)))
  })

  it('replays a shuffled recording in seq order', () => {
    const shuffled = journal.slice().reverse()
    expect(replayStage(shuffled)).toEqual(replayStage(journal))
  })
})

describe('tolerance', () => {
  it('ignores a re-delivered or older event and keeps the same object', () => {
    const s = replayStage(upTo(20))
    expect(stageReducer(s, bySeq(20))).toBe(s)
    expect(stageReducer(s, bySeq(3))).toBe(s)
  })

  it('returns the state untouched for unknown and unrelated events', () => {
    const s = replayStage(upTo(10))
    expect(stageReducer(s, { seq: 999, type: 'something.new', payload: {} })).toBe(s)
    expect(stageReducer(s, { seq: 999, type: 'assistant.delta', payload: { text: 'x' } })).toBe(s)
    expect(stageReducer(null, { seq: 1, type: 'notice', payload: {} })).toBeNull()
    expect(stageReducer(s, null)).toBe(s)
  })

  it('makes a node for a worker whose spawn it never saw, and fills it in later', () => {
    let s = stageReducer(null, { seq: 5, type: 'agent.status', payload: { agentId: 'w9', to: 'working' } })
    expect(s.nodes.w9).toMatchObject({ status: 'working', parentId: LEAD_ID })
    s = stageReducer(s, { seq: 6, type: 'agent.spawned', payload: { agentId: 'w9', role: 'Coder', model: 'm' } })
    expect(s.nodes.w9).toMatchObject({ status: 'working', role: 'Coder', model: 'm' })
    expect(s.edges).toEqual([{ id: 'lead->w9', from: 'lead', to: 'w9' }])
  })

  it('takes a question for the user and clears it when the agent moves on', () => {
    let s = replayStage(upTo(seqOf(e => e.type === 'agent.status' && e.payload.agentId === 'w1' && e.payload.to === 'working')))
    s = stageReducer(s, { seq: 900, type: 'agent.message', payload: { agentId: 'w1', direction: 'question', from: 'w1', to: 'user', text: 'Which cache?' } })
    expect(s.nodes.w1.needsYou).toBe(true)
    expect(s.flights.at(-1)).toMatchObject({ kind: 'question', from: 'w1' })
    s = stageReducer(s, { seq: 901, type: 'agent.status', payload: { agentId: 'w1', to: 'done' } })
    expect(s.nodes.w1.needsYou).toBe(false)
  })

  it('takes a per-agent usage.updated and the native agent.spawned when the runner sends them', () => {
    let s = stageReducer(null, { seq: 1, type: 'usage.updated', payload: { agentId: 'w1', inputTokens: 10, outputTokens: 2, costUsd: 0.01 } })
    expect(s.nodes.w1).toMatchObject({ tokensIn: 10, costUsd: 0.01 })
    expect(s.leadUsage).toBeNull()
    s = stageReducer(s, { seq: 2, type: 'agent.spawned', payload: { agentId: 'n1', parentId: 'w1', agentType: 'native', role: 'Explore' } })
    expect(s.nodes.n1).toMatchObject({ native: true, parentId: 'w1' })
  })

  it('marks limited activity when a runtime reports tool starts only', () => {
    let s = stageReducer(null, { seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', role: 'R' } })
    s = stageReducer(s, { seq: 2, type: 'tool.started', payload: { agentId: 'w1', callId: 'w1:a', name: 'Read' } })
    s = stageReducer(s, { seq: 3, type: 'agent.finished', payload: { agentId: 'w1', outcome: 'done' } })
    expect(s.nodes.w1.limited).toBe(true)
  })

  it('caps the quests on a turn with many follow-ups', () => {
    const events = [{ seq: 1, type: 'agent.spawned', payload: { agentId: 'w1' } }]
    for (let i = 2; i < 400; i++) events.push({ seq: i, type: 'agent.message', payload: { agentId: 'w1', direction: 'followup', text: `f${i}` } })
    const s = replayStage(events)
    expect(s.quests.length).toBe(100)
    expect(s.quests.at(-1).text).toBe('f399')
  })

  it('caps the feed and flights on a long turn', () => {
    const events = [{ seq: 1, type: 'agent.spawned', payload: { agentId: 'w1' } }]
    for (let i = 2; i < 3000; i++) events.push({ seq: i, type: 'agent.message', payload: { agentId: 'w1', direction: 'result', text: `r${i}` } })
    const s = replayStage(events)
    expect(s.feed.length).toBe(200)
    expect(s.flights.length).toBe(24)
    expect(s.feed.at(-1).text).toBe('r2999')
  })
})

describe('performance', () => {
  it('folds a 20,000-event journal of six busy workers quickly', () => {
    const events = []
    let seq = 0
    for (let w = 1; w <= 6; w++) events.push({ seq: ++seq, type: 'agent.spawned', payload: { agentId: `w${w}`, role: 'Coder', access: 'coding' } })
    while (seq < 20000) {
      const w = `w${(seq % 6) + 1}`
      const id = `${w}:c${seq}`
      events.push({ seq: ++seq, type: 'tool.started', payload: { agentId: w, callId: id, name: 'Edit', kind: 'edit', arguments: { file_path: `/w/f${seq % 50}.go` } } })
      events.push({ seq: ++seq, type: 'tool.completed', payload: { agentId: w, callId: id, ok: true, result: 'ok' } })
    }
    const t0 = performance.now()
    const s = replayStage(events)
    expect(performance.now() - t0).toBeLessThan(3000)
    expect(s.nodes.w1.tools).toBeGreaterThan(1000)
    // The call order kept per agent is capped; the counters are not.
    expect(s.nodes.w1.callOrder.length).toBeLessThanOrEqual(400)
    // The stage never copies a call's content: that stays in the turn.
    expect(s.nodes.w1.calls).toBeUndefined()
  })
})

describe('selectors', () => {
  it('keeps structureKey stable across status changes and changes it on a spawn', () => {
    const a = replayStage(upTo(7))
    const b = stageReducer(a, { seq: 8, type: 'agent.status', payload: { agentId: 'w1', to: 'working' } })
    expect(structureKey(b)).toBe(structureKey(a))
    const c = stageReducer(b, { seq: 9, type: 'agent.spawned', payload: { agentId: 'w7' } })
    expect(structureKey(c)).not.toBe(structureKey(b))
  })

  it('has a team once the lead brings someone in', () => {
    expect(hasTeam(initialStage())).toBe(false)
    expect(hasTeam(replayStage(journal))).toBe(true)
  })
})
