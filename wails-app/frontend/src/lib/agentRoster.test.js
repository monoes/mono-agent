import { describe, it, expect } from 'vitest'
import { emptyRun, applyValidateLine, withLiveResults, chipFor, rowKey, ageParts, formatLatency, formatCost, planSummary } from './agentRoster.js'

describe('applyValidateLine', () => {
  it('tracks a run from plan to done', () => {
    let run = applyValidateLine(emptyRun, { type: 'validate.plan', run_id: 'r', plan: { calls: 2 } })
    expect(run.running).toBe(true)
    run = applyValidateLine(run, { type: 'validate.started', target: { runtime: 'codex', model: 'm' } })
    expect(run.active[rowKey('codex', 'm')]).toBe(true)
    run = applyValidateLine(run, { type: 'validate.result', result: { runtime: 'codex', model: 'm', status: 'ok' } })
    expect(run.active[rowKey('codex', 'm')]).toBeUndefined()
    expect(run.results[rowKey('codex', 'm')].status).toBe('ok')
    run = applyValidateLine(run, { type: 'validate.done', summary: { ok: 1 } })
    expect(run.running).toBe(false)
    expect(run.summary.ok).toBe(1)
  })
  it('ignores junk', () => {
    expect(applyValidateLine(emptyRun, null)).toBe(emptyRun)
    expect(applyValidateLine(emptyRun, { type: 'other' })).toBe(emptyRun)
  })
})

describe('withLiveResults', () => {
  const roster = [{ runtime: 'claude', installed: true, ready: 0, models: [{ model: 'haiku', state: 'failed', status: 'auth' }] }]
  it('replaces a row and recounts ready', () => {
    const out = withLiveResults(roster, { k: { runtime: 'claude', model: 'haiku', status: 'ok', latency_ms: 900 } })
    expect(out[0].models[0].state).toBe('ready')
    expect(out[0].ready).toBe(1)
    expect(roster[0].models[0].state).toBe('failed') // input untouched
  })
  it('adds rows and runtimes it has not seen', () => {
    const out = withLiveResults(roster, { k: { runtime: 'codex', model: 'x', status: 'quota' } })
    expect(out.find(r => r.runtime === 'codex').models[0].state).toBe('failed')
  })
  it('shows a live rate_limited result as stale, not failed', () => {
    const out = withLiveResults(roster, { k: { runtime: 'claude', model: 'haiku', status: 'rate_limited' } })
    const m = out[0].models[0]
    expect(m.state).toBe('stale')
    expect(chipFor(m)).toEqual({ key: 'rateLimited', tone: 'warn' })
    expect(out[0].ready).toBe(0)
  })
})

describe('chipFor', () => {
  it.each([
    [{ state: 'ready', status: 'ok' }, 'ok', 'ok'],
    [{ state: 'ready', status: 'ok_unexpected' }, 'okUnexpected', 'warn'],
    [{ state: 'stale', stale_reason: 'age' }, 'stale', 'warn'],
    [{ state: 'stale', stale_reason: 'version' }, 'staleVersion', 'warn'],
    [{ state: 'stale', stale_reason: 'rate_limited', status: 'rate_limited' }, 'rateLimited', 'warn'],
    [{ state: 'untested' }, 'untested', 'muted'],
    [{ state: 'failed', status: 'auth' }, 'auth', 'bad'],
    [{ state: 'failed', status: 'weird' }, 'error', 'bad'],
  ])('%o', (entry, key, tone) => {
    expect(chipFor(entry)).toEqual({ key, tone })
  })
})

describe('formatting', () => {
  it('formats latency, cost, age and plan', () => {
    expect(formatLatency(0)).toBe('—')
    expect(formatLatency(420)).toBe('420 ms')
    expect(formatLatency(1530)).toBe('1.5 s')
    expect(formatCost({ has_cost: false })).toBe('')
    expect(formatCost({ has_cost: true, cost_usd: 0.00231 })).toBe('$0.0023')
    expect(formatCost({ has_cost: true, cost_usd: 0.00231, cost_estimated: true })).toBe('≈$0.0023')
    const now = Date.parse('2026-09-29T12:00:00Z')
    expect(ageParts('0001-01-01T00:00:00Z', now)).toBeNull()
    expect(ageParts('2026-09-29T11:00:00Z', now)).toEqual({ n: 1, unit: 'h' })
    expect(ageParts('2026-09-20T12:00:00Z', now)).toEqual({ n: 9, unit: 'd' })
    expect(planSummary({ calls: 3, est_cost_usd: 0.01, unknown_cost: 1, targets: [{ runtime: 'a' }, { runtime: 'a' }, { runtime: 'b' }] }))
      .toEqual({ calls: 3, cost: 0.01, unknown: 1, runtimes: 2 })
  })
})
