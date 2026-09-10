import { describe, it, expect } from 'vitest'
import { formatBytes, isMonomindMissing, documentBadgeState } from './Documents.jsx'

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

describe('documentBadgeState', () => {
  it('returns indexed for an indexed, non-stale doc', () => {
    expect(documentBadgeState({ indexed: true, stale: false }, false)).toBe('indexed')
  })
  it('returns stale for an indexed doc whose content changed', () => {
    expect(documentBadgeState({ indexed: true, stale: true }, false)).toBe('stale')
  })
  it('returns not_indexed for a never-indexed doc when monomind is set up', () => {
    expect(documentBadgeState({ indexed: false, stale: false }, false)).toBe('not_indexed')
  })
  it('returns monomind_not_set_up for a never-indexed doc when monomind is not set up', () => {
    expect(documentBadgeState({ indexed: false, stale: false }, true)).toBe('monomind_not_set_up')
  })
  it('precedence: an already-indexed doc stays indexed/stale even if monomind later becomes uninitialized', () => {
    expect(documentBadgeState({ indexed: true, stale: false }, true)).toBe('indexed')
    expect(documentBadgeState({ indexed: true, stale: true }, true)).toBe('stale')
  })
})
