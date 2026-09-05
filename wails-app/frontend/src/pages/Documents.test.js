import { describe, it, expect } from 'vitest'
import { formatBytes, isMonomindMissing } from './Documents.jsx'

describe('formatBytes', () => {
  it('formats bytes under 1KB as-is', () => {
    expect(formatBytes(512)).toBe('512 B')
  })
  it('formats kilobytes with one decimal', () => {
    expect(formatBytes(2048)).toBe('2.0 KB')
  })
  it('formats megabytes with one decimal', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
  })
  it('handles zero', () => {
    expect(formatBytes(0)).toBe('0 B')
  })
})

describe('isMonomindMissing', () => {
  it('detects the ErrNotFound message from internal/monomind/find.go', () => {
    expect(isMonomindMissing('monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli`')).toBe(true)
  })
  it('is case-insensitive', () => {
    expect(isMonomindMissing('Monomind Not Found')).toBe(true)
  })
  it('returns false for an unrelated indexing error', () => {
    expect(isMonomindMissing('knowledge_ingest: exit status 1')).toBe(false)
  })
  it('returns false for empty/undefined', () => {
    expect(isMonomindMissing('')).toBe(false)
    expect(isMonomindMissing(undefined)).toBe(false)
  })
})
