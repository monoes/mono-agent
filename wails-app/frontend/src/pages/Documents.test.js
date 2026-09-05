import { describe, it, expect } from 'vitest'
import { formatBytes } from './Documents.jsx'

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
