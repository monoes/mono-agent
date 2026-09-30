import { describe, it, expect } from 'vitest'
import journal from './__fixtures__/orgStageJournal.json'
import {
  stageReducer, replayStage, RUNNING, initialStage, leasesOf, questProgress, structureKey, doingOf, hasTeam, LEAD_ID, MAX_CALLS, MAX_PARTS,
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

  it('keeps a worker that waits on the user running, marks it needing you, and clears it on the answer (#256)', () => {
    let s = stageReducer(null, { seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', role: 'Coder' } })
    // The conductor journals the status first, then the question.
    s = stageReducer(s, { seq: 2, type: 'agent.status', payload: { agentId: 'w1', from: 'working', to: 'waiting_user', detail: 'q1' } })
    s = stageReducer(s, { seq: 3, type: 'agent.message', payload: { agentId: 'w1', direction: 'question', questionId: 'q1', from: 'w1', to: 'user', text: 'Which DB?' } })
    expect(s.nodes.w1).toMatchObject({ status: 'waiting_user', needsYou: true })
    expect(RUNNING.has(s.nodes.w1.status)).toBe(true)
    // A lease report (same status) while it waits must not clear the question.
    s = stageReducer(s, { seq: 4, type: 'agent.status', payload: { agentId: 'w1', from: 'waiting_user', to: 'waiting_user', leases: [] } })
    expect(s.nodes.w1.needsYou).toBe(true)
    // The answer, then it moves on.
    s = stageReducer(s, { seq: 5, type: 'agent.message', payload: { agentId: 'w1', direction: 'followup', questionId: 'q1', from: 'user', to: 'w1', text: 'Postgres' } })
    s = stageReducer(s, { seq: 6, type: 'agent.status', payload: { agentId: 'w1', from: 'waiting_user', to: 'working' } })
    expect(s.nodes.w1).toMatchObject({ status: 'working', needsYou: false })
  })

  it('takes a per-agent usage.updated and the native agent.spawned when the runner sends them', () => {
    let s = stageReducer(null, { seq: 1, type: 'usage.updated', payload: { agentId: 'w1', inputTokens: 10, outputTokens: 2, costUsd: 0.01 } })
    expect(s.nodes.w1).toMatchObject({ tokensIn: 10, costUsd: 0.01 })
    expect(s.leadUsage).toBeNull()
    s = stageReducer(s, { seq: 2, type: 'agent.spawned', payload: { agentId: 'n1', parentId: 'w1', agentType: 'native', role: 'Explore' } })
    expect(s.nodes.n1).toMatchObject({ native: true, parentId: 'w1' })
  })

  it('keeps an isolated writer\'s branch from agent.spawned and agent.status (#230)', () => {
    let s = stageReducer(null, { seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', role: 'Coder', branch: 'monoagent/t1/w1' } })
    expect(s.nodes.w1.branch).toBe('monoagent/t1/w1')
    // A status without a branch keeps it.
    s = stageReducer(s, { seq: 2, type: 'agent.status', payload: { agentId: 'w1', from: 'queued', to: 'working' } })
    expect(s.nodes.w1.branch).toBe('monoagent/t1/w1')
    s = stageReducer(s, { seq: 3, type: 'agent.status', payload: { agentId: 'w2', to: 'queued', branch: 'monoagent/t1/w2' } })
    expect(s.nodes.w2.branch).toBe('monoagent/t1/w2')
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

describe('worker text, live usage and fidelity (#257, #258, #259)', () => {
  const spawn = (fidelity) => ({ seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', role: 'Coder', ...(fidelity ? { fidelity } : {}) } })

  it('interleaves a worker\'s text parts with its tool calls, and leaves the lead\'s text to the chat', () => {
    const s = replayStage([
      spawn(),
      { seq: 2, type: 'assistant.delta', payload: { agentId: 'w1', partId: 'w1:p1', text: 'Look' } },
      { seq: 3, type: 'assistant.delta', payload: { agentId: 'w1', partId: 'w1:p1', text: 'ing.' } },
      { seq: 4, type: 'tool.started', payload: { agentId: 'w1', callId: 'w1:t1', name: 'Read', native: true } },
      { seq: 5, type: 'assistant.delta', payload: { agentId: 'w1', partId: 'w1:p2', text: 'Found it.' } },
      { seq: 6, type: 'assistant.delta', payload: { partId: 'part-1', text: 'lead text' } },
    ])
    expect(s.nodes.w1.parts).toEqual([
      { kind: 'text', partId: 'w1:p1', text: 'Looking.' },
      { kind: 'tool', callId: 'w1:t1' },
      { kind: 'text', partId: 'w1:p2', text: 'Found it.' },
    ])
    expect(s.nodes.lead.parts).toEqual([])
    expect(stageReducer(s, { seq: 7, type: 'assistant.delta', payload: { text: 'more lead' } })).toBe(s)
  })

  it('caps a text part', () => {
    const big = 'x'.repeat(40 * 1024)
    const s = replayStage([spawn(), ...[2, 3, 4].map(seq => ({ seq, type: 'assistant.delta', payload: { agentId: 'w1', partId: 'w1:p1', text: big } }))])
    expect(s.nodes.w1.parts[0].text.length).toBe(64 * 1024 + 1)
  })

  it('moves a worker\'s meters live from its usage.updated', () => {
    let s = replayStage([spawn()])
    s = stageReducer(s, { seq: 2, type: 'usage.updated', payload: { agentId: 'w1', inputTokens: 100, outputTokens: 10, costUsd: 0.01 } })
    s = stageReducer(s, { seq: 3, type: 'usage.updated', payload: { agentId: 'w1', inputTokens: 150, outputTokens: 30, costUsd: 0.02 } })
    expect(s.nodes.w1).toMatchObject({ tokensIn: 150, tokensOut: 30, costUsd: 0.02, costEstimated: false })
    expect(s.nodes.lead.costUsd).toBeNull()
  })

  it('marks a worker\'s live cost estimated when its runtime reports none (#230)', () => {
    let s = replayStage([spawn()])
    s = stageReducer(s, { seq: 2, type: 'usage.updated', payload: { agentId: 'w1', inputTokens: 100000, outputTokens: 10000, costUsd: 0.35, costEstimated: true } })
    expect(s.nodes.w1).toMatchObject({ costUsd: 0.35, costEstimated: true })
  })

  it('shows limited activity from the reported fidelity, from the start', () => {
    expect(replayStage([spawn('start-only')]).nodes.w1.limited).toBe(true)
    expect(replayStage([spawn('none')]).nodes.w1.limited).toBe(true)
    // A full-fidelity runtime isn't limited even when its ends never came.
    const full = replayStage([
      spawn('full'),
      { seq: 2, type: 'tool.started', payload: { agentId: 'w1', callId: 'w1:a', name: 'Read' } },
      { seq: 3, type: 'agent.finished', payload: { agentId: 'w1', outcome: 'done' } },
    ])
    expect(full.nodes.w1.limited).toBe(false)
    // A reassignment to another runtime brings its fidelity.
    const moved = stageReducer(full, { seq: 4, type: 'agent.reassigned', payload: { agentId: 'w1', toRuntime: 'x', toModel: 'm', fidelity: 'start-only' } })
    expect(moved.nodes.w1).toMatchObject({ fidelity: 'start-only', limited: true })
  })
})

describe('lease holders come from the conductor', () => {
  it('shows what agent.status reports, whatever the access profile', () => {
    let s = replayStage([
      { seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', access: 'research' } },
      { seq: 2, type: 'agent.spawned', payload: { agentId: 'w2', access: 'qa' } },
      // An unconfined research worker takes the write lease.
      { seq: 3, type: 'agent.status', payload: { agentId: 'w1', to: 'working', leases: ['write'] } },
      // A QA worker keeps nothing while it waits for the pen.
      { seq: 4, type: 'agent.status', payload: { agentId: 'w2', to: 'waiting_lease', detail: 'write' } },
    ])
    expect(leasesOf(s)).toEqual({ pen: 'w1', browser: null, waiting: [{ id: 'w2', lease: 'write' }] })
    // Holding the pen while it waits for the browser.
    s = stageReducer(s, { seq: 5, type: 'agent.status', payload: { agentId: 'w1', to: 'done' } })
    s = stageReducer(s, { seq: 6, type: 'agent.status', payload: { agentId: 'w2', to: 'waiting_lease', detail: 'browser', leases: ['write'] } })
    expect(leasesOf(s)).toEqual({ pen: 'w2', browser: null, waiting: [{ id: 'w2', lease: 'browser' }] })
  })

  it('shows the lead holding the pen when the conductor reports it', () => {
    let s = replayStage([
      { seq: 1, type: 'turn.started', payload: {} },
      { seq: 2, type: 'agent.status', payload: { agentId: 'lead', from: 'working', to: 'working', leases: ['write'] } },
    ])
    expect(leasesOf(s).pen).toBe('lead')
    expect(s.order).toEqual(['lead'])
    s = stageReducer(s, { seq: 3, type: 'agent.status', payload: { agentId: 'lead', from: 'working', to: 'working' } })
    expect(leasesOf(s).pen).toBeNull()
    expect(s.nodes.lead.status).toBe('working')
  })

  it('takes a repeated status as a lease update: the pen shows held while the worker still waits for a slot', () => {
    let s = replayStage([
      { seq: 1, type: 'agent.spawned', payload: { agentId: 'w1', access: 'coding' } },
      { seq: 2, type: 'agent.status', payload: { agentId: 'w1', to: 'queued' } },
    ])
    const feed = s.feed.length
    s = stageReducer(s, { seq: 3, type: 'agent.status', payload: { agentId: 'w1', from: 'queued', to: 'queued', leases: ['write'] } })
    expect(leasesOf(s).pen).toBe('w1')
    expect(s.nodes.w1.status).toBe('queued')
    expect(s.feed.length).toBe(feed)
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
    // The call order kept per agent is capped at its latest calls; the
    // counters are not.
    expect(s.nodes.w1.callOrder.length).toBe(400)
    const w1Last = events.filter(e => e.type === 'tool.started' && e.payload.agentId === 'w1').at(-1).payload.callId
    expect(s.nodes.w1.callOrder.at(-1)).toBe(w1Last)
    // The stage never copies a call's content: that stays in the turn.
    expect(s.nodes.w1.calls).toBeUndefined()
  })
})

describe('a long run keeps its latest work', () => {
  it('keeps the latest calls and timeline parts together, never a tool part for a dropped call', () => {
    const events = [{ seq: 1, type: 'agent.spawned', payload: { agentId: 'w1' } }]
    let seq = 1
    for (let i = 0; i < 900; i++) {
      events.push({ seq: ++seq, type: 'tool.started', payload: { agentId: 'w1', callId: `w1:c${i}`, name: 'Read' } })
      if (i % 3 === 0) events.push({ seq: ++seq, type: 'assistant.delta', payload: { agentId: 'w1', partId: `w1:p${i}`, text: `note ${i}` } })
    }
    const n = replayStage(events).nodes.w1
    expect(n.callOrder.length).toBe(MAX_CALLS)
    expect(n.callOrder[0]).toBe('w1:c500')
    expect(n.callOrder.at(-1)).toBe('w1:c899')
    expect(n.parts.length).toBeLessThanOrEqual(MAX_PARTS)
    const kept = new Set(n.callOrder)
    const tools = n.parts.filter(p => p.kind === 'tool')
    expect(tools.every(p => kept.has(p.callId))).toBe(true)
    expect(tools.length).toBe(MAX_CALLS)
    expect(n.parts.slice(-4)).toEqual([
      { kind: 'tool', callId: 'w1:c897' }, { kind: 'text', partId: 'w1:p897', text: 'note 897' },
      { kind: 'tool', callId: 'w1:c898' }, { kind: 'tool', callId: 'w1:c899' },
    ])
  })

  it('keeps the latest text parts when a run mostly talks', () => {
    const events = [{ seq: 1, type: 'agent.spawned', payload: { agentId: 'w1' } }]
    for (let i = 0; i < 1000; i++) events.push({ seq: i + 2, type: 'assistant.delta', payload: { agentId: 'w1', partId: `w1:p${i}`, text: `t${i}` } })
    const n = replayStage(events).nodes.w1
    expect(n.parts.length).toBe(MAX_PARTS)
    expect(n.parts[0].text).toBe('t200')
    expect(n.parts.at(-1).text).toBe('t999')
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

// monomind#387 (#230): the CLI journals a native subagent's lifecycle and
// text as agent.* events for the node its Task call made.
describe('reported native subagents', () => {
  const at = (s) => `2026-09-30T10:00:${String(s).padStart(2, '0')}.000Z`
  const base = [
    { seq: 1, at: at(1), type: 'agent.spawned', payload: { agentId: 'w1', role: 'Researcher', runtime: 'claude', model: 'haiku' } },
    { seq: 2, at: at(2), type: 'tool.started', payload: { agentId: 'w1', callId: 'w1:t1', name: 'Task', native: true, kind: 'task', arguments: { subagent_type: 'Explore', prompt: 'Find the loader.' } } },
  ]
  const reported = [
    ...base,
    { seq: 3, at: at(3), type: 'agent.spawned', payload: { agentId: 'native:w1:t1', parentId: 'w1', agentType: 'native', role: 'Explore', brief: 'Find the loader.' } },
    { seq: 4, at: at(4), type: 'agent.status', payload: { agentId: 'native:w1:t1', to: 'working' } },
    { seq: 5, at: at(5), type: 'assistant.delta', payload: { agentId: 'native:w1:t1', partId: 'native:w1:t1:p1', text: 'Searching.' } },
    { seq: 6, at: at(6), type: 'agent.status', payload: { agentId: 'native:w1:t1', from: 'working', to: 'working', detail: 'Found loadConfig' } },
    { seq: 7, at: at(7), type: 'agent.message', payload: { agentId: 'native:w1:t1', direction: 'result', from: 'native:w1:t1', to: 'w1', text: 'It is loadConfig.' } },
    { seq: 8, at: at(8), type: 'agent.finished', payload: { agentId: 'native:w1:t1', outcome: 'failed', summary: 'It is loadConfig.', durationMs: 3050 } },
    { seq: 9, at: at(9), type: 'tool.completed', payload: { agentId: 'w1', callId: 'w1:t1', ok: true, result: 'the Task tool output' } },
  ]

  it('fills in the one node the Task call made, with its live text and progress', () => {
    const live = replayStage(reported.slice(0, 6))
    expect(live.order).toEqual([LEAD_ID, 'w1', 'native:w1:t1'])
    const n = live.nodes['native:w1:t1']
    expect(n).toMatchObject({ native: true, reported: true, parentId: 'w1', role: 'Explore', status: 'working', statusDetail: 'Found loadConfig', runtime: 'claude', model: 'haiku' })
    expect(n.parts).toEqual([{ kind: 'text', partId: 'native:w1:t1:p1', text: 'Searching.' }])
    expect(live.feed.filter(f => f.type === 'spawned' && f.agentId === 'native:w1:t1')).toHaveLength(1)
    expect(live.edges.filter(e => e.to === 'native:w1:t1')).toHaveLength(1)
  })

  it('keeps the node under the caller its Task call put it under', () => {
    const events = [...base, { seq: 3, type: 'agent.spawned', payload: { agentId: 'native:w1:t1', agentType: 'native', role: 'Explore' } }]
    const s = replayStage(events)
    expect(s.nodes['native:w1:t1'].parentId).toBe('w1')
    expect(s.edges.filter(e => e.to === 'native:w1:t1').map(e => e.from)).toEqual(['w1'])
  })

  it('lets the reported finish decide, not the Task call\'s end', () => {
    const n = replayStage(reported).nodes['native:w1:t1']
    expect(n).toMatchObject({ status: 'failed', outcome: 'failed', summary: 'It is loadConfig.', durationMs: 3050 })
    expect(replayStage(reported).feed.filter(f => f.type === 'finished' && f.agentId === 'native:w1:t1')).toHaveLength(1)
  })

  it('still infers the subagent from its Task call without the events (older monomind)', () => {
    const n = replayStage([...base, { seq: 3, at: at(5), type: 'tool.completed', payload: { agentId: 'w1', callId: 'w1:t1', ok: true, result: 'the Task tool output' } }]).nodes['native:w1:t1']
    expect(n).toMatchObject({ native: true, reported: false, status: 'done', summary: 'the Task tool output' })
  })

  it('closes a reported subagent from its Task call when no finish came', () => {
    const cut = [...reported.slice(0, 6), { seq: 9, at: at(9), type: 'tool.completed', payload: { agentId: 'w1', callId: 'w1:t1', cancelled: true, result: '' } }]
    expect(replayStage(cut).nodes['native:w1:t1'].status).toBe('cancelled')
  })
})
