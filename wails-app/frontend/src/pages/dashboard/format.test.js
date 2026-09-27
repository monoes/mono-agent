import { describe, it, expect } from 'vitest'
import { relTime, duration, untilTime } from './format.js'

const t = (k, o) => (o ? `${k}:${JSON.stringify(o)}` : k)
const now = Date.parse('2026-09-26T12:00:00Z')

describe('format', () => {
  it('relTime', () => {
    expect(relTime(null, t, now)).toBe('—')
    expect(relTime('nonsense', t, now)).toBe('—')
    expect(relTime('2026-09-26T11:59:30Z', t, now)).toBe('dashboard.time.justNow')
    expect(relTime('2026-09-26T11:50:00Z', t, now)).toBe('dashboard.time.minutesAgo:{"count":10}')
    expect(relTime('2026-09-26T09:00:00Z', t, now)).toBe('dashboard.time.hoursAgo:{"count":3}')
  })
  it('duration', () => {
    expect(duration('2026-09-26T11:59:48Z', '2026-09-26T12:00:00Z', now)).toBe('12s')
    expect(duration('2026-09-26T11:57:55Z', null, now)).toBe('2m 5s')
    expect(duration(null, null, now)).toBeNull()
  })
  it('untilTime', () => {
    expect(untilTime('2026-09-26T12:00:30Z', t, now)).toBe('dashboard.time.now')
    expect(untilTime('2026-09-26T12:20:00Z', t, now)).toBe('dashboard.time.inMinutes:{"count":20}')
    expect(untilTime('2026-09-26T15:00:00Z', t, now)).toBe('dashboard.time.inHours:{"count":3}')
  })
})
