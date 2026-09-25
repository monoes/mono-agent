import { describe, it, expect } from 'vitest'
import {
  tierForClass, effectiveLevel, isPaused, routeFor, routeLabel, fullAutoImpact, classOfAction,
  approvalToNeedsDecision, needsDecisionToApproval, DECIDERS, isDeciderKind,
} from './autonomyModel.js'
import { toMillis, formatDuration, waitedLabel, idleStopRemainingMs, idleStopLabel } from './waiting.js'

describe('tierForClass', () => {
  it('uses plan defaults', () => {
    expect(tierForClass('tool:Bash', null)).toBe('routine')
    expect(tierForClass('org_complete', null)).toBe('consequential')
    expect(tierForClass('question', null)).toBe('consequential')
    expect(tierForClass('org_start', null)).toBe('consequential')
    expect(tierForClass('gate', null)).toBe('irreversible')
  })
  it('makes outbound grants irreversible and internal ones consequential', () => {
    expect(tierForClass('grant:publish', null, { hasOutbound: true })).toBe('irreversible')
    expect(tierForClass('grant:summarize', null, { hasOutbound: false })).toBe('consequential')
  })
  it('lets exact overrides beat wildcards and wildcards beat defaults', () => {
    const autonomy = { tiers: { 'tool:*': 'consequential', 'tool:WebFetch': 'irreversible', 'grant:*': 'routine' } }
    expect(tierForClass('tool:Bash', autonomy)).toBe('consequential')
    expect(tierForClass('tool:WebFetch', autonomy)).toBe('irreversible')
    expect(tierForClass('grant:publish', autonomy, { hasOutbound: true })).toBe('routine')
  })
  it('honours CLI default_tiers', () => {
    expect(tierForClass('gate', { default_tiers: { gate: 'consequential' } })).toBe('consequential')
  })
})

describe('levels and routing', () => {
  it('routes each tier per level', () => {
    expect(['routine', 'consequential', 'irreversible'].map(t => routeFor('manual', t))).toEqual(['you', 'you', 'you'])
    expect(['routine', 'consequential', 'irreversible'].map(t => routeFor('mid', t))).toEqual(['rule', 'decider', 'you'])
    expect(['routine', 'consequential', 'irreversible'].map(t => routeFor('full', t))).toEqual(['rule', 'decider', 'decider'])
    expect(routeLabel('decider', 'boss')).toBe('decided by boss')
    expect(routeLabel('you')).toBe('waits for you')
  })
  it('treats a paused org as manual', () => {
    const now = Date.parse('2026-09-16T12:00:00Z')
    const a = { level: 'full', paused_until: '2026-09-16T12:30:00Z' }
    expect(isPaused(a, now)).toBe(true)
    expect(effectiveLevel(a, now)).toBe('manual')
    expect(effectiveLevel({ level: 'full', paused_until: '2026-09-16T11:00:00Z' }, now)).toBe('full')
    expect(effectiveLevel({ level: 'mid', effective_level: 'manual' }, now)).toBe('manual')
    expect(effectiveLevel(null)).toBe('manual')
  })
})

describe('fullAutoImpact', () => {
  it('lists gates, irreversible grants, and irreversible overrides', () => {
    const grants = [
      { role: 'lead', alias: 'publish', workflow_name: 'Publish post', tier: 'irreversible' },
      { role: 'lead', alias: 'summarize', workflow_name: 'Summarize', tier: 'consequential' },
    ]
    const gates = [{ id: 'g1', name: 'ship-it', roleId: 'judge' }]
    const impact = fullAutoImpact({ tiers: { 'tool:WebFetch': 'irreversible', 'grant:x': 'irreversible' } }, grants, gates)
    expect(impact.gatesIncluded).toBe(true)
    expect(impact.pendingGates).toEqual([{ id: 'g1', name: 'ship-it', role: 'judge' }])
    expect(impact.grants).toEqual([{ role: 'lead', alias: 'publish', workflowName: 'Publish post' }])
    expect(impact.classes).toEqual(['tool:WebFetch'])
  })
})

describe('classOfAction and approval copy', () => {
  it('maps actions to decision classes', () => {
    expect(classOfAction('Bash')).toBe('tool:Bash')
    expect(classOfAction('org_complete')).toBe('org_complete')
    expect(classOfAction('monoagent__automation_publish_post')).toBe('grant:publish_post')
    expect(classOfAction('mcp__org__monoagent__automation_x')).toBe('grant:x')
    expect(classOfAction('monoagent__org_start')).toBe('org_start')
    expect(classOfAction('')).toBeNull()
  })
  it('round-trips approval <-> needs a decision', () => {
    expect(approvalToNeedsDecision('required')).toBe(true)
    expect(approvalToNeedsDecision('none')).toBe(false)
    expect(needsDecisionToApproval(true)).toBe('required')
    expect(needsDecisionToApproval(false)).toBe('none')
  })
})

describe('waiting helpers', () => {
  it('normalises timestamps', () => {
    expect(toMillis(1789544435842)).toBe(1789544435842)
    expect(toMillis(1789544435)).toBe(1789544435000)
    expect(toMillis('2026-09-16T12:00:00Z')).toBe(Date.parse('2026-09-16T12:00:00Z'))
    expect(toMillis('nope')).toBeNull()
    expect(toMillis(null)).toBeNull()
  })
  it('formats durations', () => {
    expect(formatDuration(45_000)).toBe('45s')
    expect(formatDuration(200_000)).toBe('3m 20s')
    expect(formatDuration(25 * 60_000)).toBe('25m')
    expect(formatDuration(80 * 60_000)).toBe('1h 20m')
    expect(formatDuration(51 * 3600_000)).toBe('2d 3h')
  })
  it('labels waits and idle-stop countdowns', () => {
    expect(waitedLabel(1789544435842, 1789544435842 + 60_000)).toBe('waiting 1m')
    expect(waitedLabel(undefined)).toBe('')
    expect(idleStopRemainingMs(300, 10_000, 70_000)).toBe(240_000)
    expect(idleStopRemainingMs(null, 0, 0)).toBeNull()
    expect(idleStopLabel(240_000)).toBe('idle stop in 4m')
    expect(idleStopLabel(0)).toBe('org may idle-stop now')
    expect(idleStopLabel(null)).toBe('')
  })
})

describe('decider kinds', () => {
  it('matches the CLI list, including jev', () => {
    expect(DECIDERS.map(d => d.id)).toEqual(['model', 'boss', 'parent', 'jev'])
    for (const k of ['model', 'boss', 'parent', 'jev']) expect(isDeciderKind(k)).toBe(true)
    expect(isDeciderKind('bogus')).toBe(false)
    expect(routeLabel('decider', 'jev')).toBe('decided by jev')
  })
})
