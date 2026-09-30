import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  initialOrgBubble, applyOrgBusEvent, replayOrgBubble, orgStageOf, pendingByRole,
  emptyOrgSummary, applyOrgSummaryEvent, orgBubbleStatus, orgNeedsCount, touchesThread, orgsToAutoOpen,
} from './orgBubble.js'
import { LEAD_ID } from './orgStage.js'

function jsonl(url) {
  return readFileSync(fileURLToPath(url), 'utf8').split('\n').filter(Boolean).map(l => JSON.parse(l))
}

// The same bus the Go thread builder is tested on (internal/orgchat).
const ACME = jsonl(new URL('../../../../internal/orgchat/testdata/boss-thread.jsonl', import.meta.url))
const ACME_ROLES = [
  { id: 'ceo', title: 'Chief', type: 'boss', reports_to: null, runtime: 'claude', model: 'claude-sonnet-5' },
  { id: 'dev', title: 'Developer', type: 'coder', reports_to: 'ceo' },
  { id: 'qa', title: 'QA', type: 'tester', reports_to: 'dev' },
]
const acme = () => ({ org: 'acme', boss: 'ceo', roles: ACME_ROLES })

// A real Org Arena capture (scrubbed), with its roles.
const HERALD = jsonl(new URL('../components/orgdesigner/__fixtures__/herald-rehearsal.jsonl', import.meta.url))
const HERALD_ROLES = [
  { id: 'editor', title: 'Editor', reports_to: null },
  { id: 'reviewer', title: 'Reviewer', reports_to: 'editor' },
  { id: 'writer', title: 'Writer', reports_to: 'editor' },
  { id: 'judge', title: 'Judge', reports_to: 'editor' },
]

describe('running-org adapter over a recorded bus', () => {
  it('maps the boss to the lead and the roles to nodes under whom they report to', () => {
    const s = replayOrgBubble(ACME, acme())
    const stage = orgStageOf(s)
    expect(stage.order).toEqual([LEAD_ID, 'dev', 'qa'])
    expect(stage.nodes[LEAD_ID].role).toBe('Chief')
    expect(stage.nodes[LEAD_ID].runtime).toBe('claude')
    expect(stage.nodes.dev.parentId).toBe(LEAD_ID)
    expect(stage.nodes.qa.parentId).toBe('dev')
    expect(stage.edges.map(e => e.id)).toEqual([`${LEAD_ID}->dev`, 'dev->qa'])
    expect(stage.nodes.dev.status).toBe('idle')
    expect(stage.nodes.dev.role).toBe('Developer')
  })

  it('maps messages, tools and usage', () => {
    const s = replayOrgBubble(ACME, acme())
    const { nodes, quests, feed } = s.stage
    // The boss's first message to dev is its brief; dev's reply is a result.
    expect(nodes.dev.brief).toBe('Draft the notes for v1.2')
    expect(quests.map(q => q.agentId)).toEqual(['dev'])
    expect(nodes.dev.summary).toBe('Draft ready in NOTES.md')
    expect(feed.some(f => f.type === 'result' && f.agentId === 'dev')).toBe(true)
    // A full-access role's tool_activity pair is one completed Edit.
    const edit = nodes.dev.callOrder.map(id => s.calls[id]).find(c => c?.name === 'Edit')
    expect(edit).toMatchObject({ status: 'completed', ok: true, native: true })
    expect(nodes.dev.files).toEqual(['/work/acme/NOTES.md'])
    // A plain `tool` call ends when the role stops working.
    expect(nodes[LEAD_ID].tools).toBe(1)
    expect(nodes[LEAD_ID].toolsDone).toBe(0) // ceo never left "working" in this log
    expect(nodes[LEAD_ID].costUsd).toBeCloseTo(0.02)
    expect(nodes[LEAD_ID].tokensIn).toBe(1200)
    expect(nodes.dev.costUsd).toBeCloseTo(0.01)
  })

  it('flags "needs you" exactly on the roles something is pending for', () => {
    const s = replayOrgBubble(ACME, acme())
    // dev's Bash approval was granted and ceo's question answered; ceo's
    // gate and qa's question are still open.
    expect(pendingByRole(s.activity)).toEqual({ ceo: 1, qa: 1 })
    const stage = orgStageOf(s)
    expect(stage.nodes[LEAD_ID].needsYou).toBe(true)
    expect(stage.nodes.qa.needsYou).toBe(true)
    expect(stage.nodes.dev.needsYou).toBe(false)
  })

  it('pops a lazily started role in only when it first shows up', () => {
    const upTo = (id) => ACME.slice(0, ACME.findIndex(e => e.id === id) + 1)
    expect(replayOrgBubble(upTo('run-a-1350-6'), acme()).stage.order).toEqual([LEAD_ID])
    expect(replayOrgBubble(upTo('run-a-1360-7'), acme()).stage.order).toEqual([LEAD_ID, 'dev'])
    expect(replayOrgBubble(upTo('run-a-2101-23'), acme()).stage.order).toEqual([LEAD_ID, 'dev'])
    expect(replayOrgBubble(ACME, acme()).stage.order).toEqual([LEAD_ID, 'dev', 'qa'])
  })

  it('replaying the same events gives the same state, live or recorded', () => {
    const recorded = replayOrgBubble(ACME, acme())
    let live = initialOrgBubble(acme())
    for (const ev of ACME) live = applyOrgBusEvent(live, ev)
    expect(live).toEqual(recorded)
    // A tail that restarts re-delivers what it already sent: nothing moves.
    let again = live
    for (const ev of ACME) again = applyOrgBusEvent(again, ev)
    expect(again).toBe(live)
    expect(replayOrgBubble(ACME, acme())).toEqual(recorded)
  })

  it('handles a real capture: every edge joins two nodes, no self edges, deterministic', () => {
    const a = replayOrgBubble(HERALD, { org: 'herald', boss: 'editor', roles: HERALD_ROLES })
    const b = replayOrgBubble(HERALD, { org: 'herald', boss: 'editor', roles: HERALD_ROLES })
    expect(a).toEqual(b)
    const stage = orgStageOf(a)
    expect(stage.order[0]).toBe(LEAD_ID)
    expect(stage.order.length).toBeGreaterThan(2)
    for (const e of stage.edges) {
      expect(e.from).not.toBe(e.to)
      expect(stage.nodes[e.from]).toBeTruthy()
      expect(stage.nodes[e.to]).toBeTruthy()
    }
    // Other orgs' roles (xorg) never become nodes here.
    expect(stage.order.every(id => id === LEAD_ID || HERALD_ROLES.some(r => r.id === id))).toBe(true)
    // The judge's publish gate is pending at the end of the capture.
    expect(stage.nodes.judge.needsYou).toBe(true)
    for (const f of stage.flights) {
      expect(f.from).not.toBe(f.to)
    }
  })

  it('drops a restarted tail\'s replay of a long run, cost included', () => {
    const opts = { org: 'herald', boss: 'editor', roles: HERALD_ROLES }
    const once = replayOrgBubble(HERALD, opts)
    let twice = once
    for (const ev of HERALD) twice = applyOrgBusEvent(twice, ev)
    expect(twice).toBe(once)
    let summary = emptyOrgSummary('herald')
    for (const ev of HERALD) summary = applyOrgSummaryEvent(summary, ev, { boss: 'editor' })
    const cost = summary.activity.usage.costUsd
    for (const ev of HERALD) summary = applyOrgSummaryEvent(summary, ev, { boss: 'editor' })
    expect(summary.activity.usage.costUsd).toBe(cost)
  })

  it('starts a fresh stage for a new run and ends the run on "org stopped"', () => {
    let s = replayOrgBubble(ACME, acme())
    s = applyOrgBusEvent(s, { id: 'x1', ts: 3000, org: 'acme', run: 'run-a', type: 'status', msg: 'org stopped' })
    expect(s.stage.turnStatus).toBe('completed')
    expect(s.stage.scoreboard.agents).toBe(2)
    expect(s.stage.nodes[LEAD_ID].toolsDone).toBe(1) // the open call ended with the run
    s = applyOrgBusEvent(s, { id: 'y1', ts: 4000, org: 'acme', run: 'run-b', type: 'status', msg: 'org started' })
    expect(s.stage.order).toEqual([LEAD_ID])
    expect(s.stage.turnStatus).toBe('running')
  })

  it('ignores another org\'s events and events it cannot place', () => {
    const s0 = initialOrgBubble(acme())
    const s1 = applyOrgBusEvent(s0, { id: 'o1', ts: 1, org: 'other', type: 'status', from: 'x', reason: 'state-change', data: { to: 'working' } })
    expect(s1).toBe(s0)
    const s2 = applyOrgBusEvent(s0, { id: 'o2', ts: 1, org: 'acme', type: 'status', from: 'dag', msg: 'task task-1 dispatched to dev' })
    expect(s2.stage.order).toEqual([LEAD_ID])
  })

  it('keeps each role\'s latest calls under one cap, in the stage and for the drawer', () => {
    const calls = []
    for (let i = 0; i < 450; i++) {
      calls.push({ id: `tu-dev-${i}`, ts: 5000 + i * 2, org: 'acme', run: 'run-a', type: 'tool_activity', from: 'acme:dev', phase: 'start', name: 'Read', input: { file_path: `/w/f${i}` } })
      calls.push({ id: `tu-dev-${i}`, ts: 5001 + i * 2, org: 'acme', run: 'run-a', type: 'tool_activity', from: 'acme:dev', phase: 'end', name: 'Read', ok: true })
    }
    calls.push({ id: 'tu-qa-0', ts: 9000, org: 'acme', run: 'run-a', type: 'tool_activity', from: 'acme:qa', phase: 'start', name: 'Bash', input: { command: 'go test' } })
    const s = replayOrgBubble([...ACME, ...calls], acme())
    const order = s.stage.nodes.dev.callOrder
    expect(order).toHaveLength(400)
    // The newest stay: the drawer never empties on a busy role.
    expect(order.at(-1)).toBe('ta:tu-dev-449')
    // dev's Edit from the fixture and its first 50 reads went first.
    expect(order[0]).toBe('ta:tu-dev-50')
    expect(s.calls['ta:tu-dev-49']).toBeUndefined()
    expect(s.calls['ta:toolu_01']).toBeUndefined()
    for (const id of order) expect(s.calls[id]).toBeTruthy()
    expect(s.calls['ta:tu-dev-449'].status).toBe('completed')
    expect(s.callIds.dev).toEqual(order)
    // Another role's calls are not pushed out by dev's.
    expect(s.calls['ta:tu-qa-0']).toBeTruthy()
    expect(s.stage.nodes.qa.callOrder).toEqual(['ta:tu-qa-0'])
  })

  it('shows full-access entries on their nodes', () => {
    const s = replayOrgBubble(ACME, acme())
    const entry = { role: 'dev', access: 'full', access_state: 'active' }
    const stage = orgStageOf(s, { dev: entry })
    expect(stage.nodes.dev.fullAccess).toBe(entry)
    expect(stage.nodes.qa.fullAccess).toBeUndefined()
  })
})

describe('collapsed org bubble summary', () => {
  it('counts the boss speaking while collapsed as unread and pulses while items wait', () => {
    let s = emptyOrgSummary('acme')
    for (const ev of ACME) s = applyOrgSummaryEvent(s, ev, { boss: 'ceo', expanded: false })
    expect(s.unread).toBe(2)
    expect(orgNeedsCount(s)).toBe(2)
    expect(orgBubbleStatus(s)).toBe('needs')

    let open = emptyOrgSummary('acme')
    for (const ev of ACME) open = applyOrgSummaryEvent(open, ev, { boss: 'ceo', expanded: true })
    expect(open.unread).toBe(0)
  })

  it('reads working and idle', () => {
    let s = emptyOrgSummary('acme')
    s = applyOrgSummaryEvent(s, { id: 'a', ts: 1, org: 'acme', type: 'status', from: 'ceo', reason: 'state-change', data: { from: 'idle', to: 'working' } })
    expect(orgBubbleStatus(s)).toBe('working')
    s = applyOrgSummaryEvent(s, { id: 'b', ts: 2, org: 'acme', type: 'status', msg: 'org stopped' })
    expect(orgBubbleStatus(s)).toBe('idle')
  })
})

describe('touchesThread', () => {
  it('refetches the thread only for events that can change it', () => {
    const kinds = ACME.filter(ev => touchesThread(ev, 'ceo')).map(ev => ev.id)
    expect(kinds).toEqual([
      'run-a-1000-0', 'run-a-1100-2', 'run-a-1101-3', 'run-a-1300-5', 'run-a-1370-8', 'run-a-1600-13',
      'run-a-1650-14', 'run-a-1700-15', 'run-a-1800-16', 'run-a-1850-17', 'run-a-2000-20', 'run-a-2050-21',
      'run-a-2100-22', 'run-a-2101-23', 'run-a-2250-25',
    ])
    expect(touchesThread({ type: 'chat', from: 'dev' }, 'ceo')).toBe(false)
    expect(touchesThread({ type: 'usage' }, 'ceo')).toBe(false)
  })
})

describe('orgsToAutoOpen', () => {
  const rows = [
    { name: 'acme', running: true, needs: 2 },
    { name: 'idle', running: true, needs: 0 },
    { name: 'off', running: false, needs: 3 },
    { name: 'open', running: true, needs: 1 },
  ]
  it('opens running orgs that wait on the person and have no bubble', () => {
    expect(orgsToAutoOpen(rows, new Set(['open']))).toEqual({ open: ['acme'], dismissed: {} })
  })
  it('keeps a closed bubble closed until more items wait, and forgets it once none do', () => {
    let r = orgsToAutoOpen(rows, new Set(), { acme: 2 })
    expect(r.open).toEqual(['open'])
    expect(r.dismissed).toEqual({ acme: 2 })
    r = orgsToAutoOpen([{ name: 'acme', running: true, needs: 3 }], new Set(), r.dismissed)
    expect(r).toEqual({ open: ['acme'], dismissed: {} })
    r = orgsToAutoOpen([{ name: 'acme', running: true, needs: 0 }], new Set(), { acme: 2 })
    expect(r).toEqual({ open: [], dismissed: {} })
  })
})
