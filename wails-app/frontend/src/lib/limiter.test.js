import { describe, it, expect } from 'vitest'
import { limiter } from './limiter.js'

describe('limiter', () => {
  it('never runs more than n tasks at once, and runs them all', async () => {
    const run = limiter(2)
    let active = 0, peak = 0
    const task = () => new Promise(r => { active++; peak = Math.max(peak, active); setTimeout(() => { active--; r(1) }, 5) })
    const results = await Promise.all(Array.from({ length: 7 }, () => run(task)))
    expect(peak).toBe(2)
    expect(results).toEqual([1, 1, 1, 1, 1, 1, 1])
  })
  it('a failing task does not stall the queue', async () => {
    const run = limiter(1)
    await expect(run(() => Promise.reject(new Error('x')))).rejects.toThrow('x')
    expect(await run(() => 5)).toBe(5)
  })
})
