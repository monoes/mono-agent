import { describe, it, expect } from 'vitest'
import { isMonomindNotFound } from './agentRuntimes.js'

describe('isMonomindNotFound', () => {
  it('matches only the backend "binary not found" error', () => {
    expect(isMonomindNotFound('monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli`')).toBe(true)
    expect(isMonomindNotFound('monomind 2.1.0 is too old (need >= 2.9.0)')).toBe(false)
    expect(isMonomindNotFound('handshake with /x/monomind failed: exit status 127')).toBe(false)
    expect(isMonomindNotFound('')).toBe(false)
    expect(isMonomindNotFound(undefined)).toBe(false)
  })
})
