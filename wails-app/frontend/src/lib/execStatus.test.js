import { describe, it, expect } from 'vitest'
import { execStatus, LIVE_STATUSES } from './execStatus.js'

describe('execStatus', () => {
  it.each([
    ['SUCCESS', 'success', false], ['COMPLETED', 'success', false], ['success', 'success', false],
    ['SUCCESS_WITH_ERRORS', 'partial', false], ['RUNNING', 'running', true], ['QUEUED', 'queued', true],
    ['PENDING', 'queued', true], ['WAITING', 'waiting', true], ['FAILED', 'failed', false],
    ['CANCELLED', 'cancelled', false], ['', 'unknown', false], [undefined, 'unknown', false],
  ])('%s → %s', (raw, key, live) => {
    const s = execStatus(raw)
    expect(s.key).toBe(key)
    expect(s.live).toBe(live)
    expect(s.tone).toMatch(/^(var\(--|#)/)
  })
  it('LIVE_STATUSES matches live', () => {
    expect([...LIVE_STATUSES].sort()).toEqual(['PENDING', 'QUEUED', 'RUNNING', 'WAITING'])
    for (const s of LIVE_STATUSES) expect(execStatus(s).live).toBe(true)
  })
})
