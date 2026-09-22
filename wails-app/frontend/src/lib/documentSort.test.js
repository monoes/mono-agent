import { describe, it, expect } from 'vitest'
import {
  sourceLabel, documentType, viewerFilename, isCapture, sortDocuments, nextSort,
  loadSort, saveSort, DEFAULT_SORT, sourceOptions, filterBySource,
} from './documentSort.js'

const capture = {
  id: 'doc-7', filename: 'Jev Picker Plan', source: 'extension', url: 'https://claude.ai/artifact/x',
  path: '/h/.monoagent/profiles/p/.monomind/inbox/2026-09-22T10-20-19Z-claude-ai/page.mhtml',
  capture_dir: '/h/.monoagent/profiles/p/.monomind/inbox/2026-09-22T10-20-19Z-claude-ai',
  created_at: '2026-09-22 10:20:19',
}
const readableCapture = { ...capture, id: 'doc-8', path: capture.capture_dir + '/readable.md' }
const upload = { id: 'doc-1', filename: 'resume.pdf', source: 'upload', path: '/v/doc-001.pdf', created_at: '2026-01-01 09:00:00' }
const discovered = { id: 'doc-2', filename: 'notes.md', source: 'discovered', path: '/p/notes.md', created_at: '2026-03-01 09:00:00' }
const zebra = { id: 'doc-3', filename: 'Zebra.docx', source: 'upload', path: '/v/doc-003.docx', created_at: '2025-12-01 09:00:00' }

describe('sourceLabel', () => {
  it('says a capture came from the browser extension', () => {
    expect(sourceLabel('extension')).toBe('Browser extension')
  })
  it('labels the built-in sources and passes custom ones through', () => {
    expect(sourceLabel('upload')).toBe('Upload')
    expect(sourceLabel('discovered')).toBe('Profile folder')
    expect(sourceLabel('my-script')).toBe('my-script')
    expect(sourceLabel('')).toBe('Unknown')
  })
})

describe('documentType / viewerFilename', () => {
  it('takes the type from the file the row opens, not the title', () => {
    expect(documentType(capture)).toBe('mhtml')
    expect(documentType(readableCapture)).toBe('md')
    expect(documentType(upload)).toBe('pdf')
  })
  it('falls back to the filename, and to empty when there is no extension', () => {
    expect(documentType({ filename: 'a.TXT' })).toBe('txt')
    expect(documentType({ filename: 'Makefile', path: '/x/Makefile' })).toBe('')
  })
  it('gives a capture its title plus its artifact extension for the viewer', () => {
    expect(isCapture(capture)).toBe(true)
    expect(isCapture(upload)).toBe(false)
    expect(viewerFilename(readableCapture)).toBe('Jev Picker Plan.md')
    expect(viewerFilename(upload)).toBe('resume.pdf')
  })
})

describe('sortDocuments', () => {
  const docs = [upload, capture, discovered, zebra]
  const ids = (sorted) => sorted.map(d => d.id)

  it('sorts by date, newest first by default', () => {
    expect(ids(sortDocuments(docs, DEFAULT_SORT))).toEqual(['doc-7', 'doc-2', 'doc-1', 'doc-3'])
    expect(ids(sortDocuments(docs, { key: 'date', dir: 'asc' }))).toEqual(['doc-3', 'doc-1', 'doc-2', 'doc-7'])
  })
  it('sorts by name case-insensitively', () => {
    expect(ids(sortDocuments(docs, { key: 'name', dir: 'asc' }))).toEqual(['doc-7', 'doc-2', 'doc-1', 'doc-3'])
    expect(ids(sortDocuments(docs, { key: 'name', dir: 'desc' }))).toEqual(['doc-3', 'doc-1', 'doc-2', 'doc-7'])
  })
  it('sorts by type (extension), ties broken by name', () => {
    expect(ids(sortDocuments(docs, { key: 'type', dir: 'asc' }))).toEqual(['doc-3', 'doc-2', 'doc-7', 'doc-1'])
  })
  it('sorts by the displayed source label, ties broken by name', () => {
    // Browser extension < Profile folder < Upload (resume.pdf, Zebra.docx)
    expect(ids(sortDocuments(docs, { key: 'source', dir: 'asc' }))).toEqual(['doc-7', 'doc-2', 'doc-1', 'doc-3'])
  })
  it('never mutates its input', () => {
    const copy = [...docs]
    sortDocuments(docs, { key: 'name', dir: 'asc' })
    expect(docs).toEqual(copy)
  })
})

describe('nextSort', () => {
  it('flips direction on the active column', () => {
    expect(nextSort({ key: 'name', dir: 'asc' }, 'name')).toEqual({ key: 'name', dir: 'desc' })
    expect(nextSort({ key: 'name', dir: 'desc' }, 'name')).toEqual({ key: 'name', dir: 'asc' })
  })
  it('starts a new column A→Z, or newest-first for dates', () => {
    expect(nextSort({ key: 'date', dir: 'desc' }, 'source')).toEqual({ key: 'source', dir: 'asc' })
    expect(nextSort({ key: 'name', dir: 'asc' }, 'date')).toEqual({ key: 'date', dir: 'desc' })
  })
})

describe('loadSort / saveSort', () => {
  const memory = () => {
    const m = new Map()
    return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, v) }
  }
  it('round-trips the choice', () => {
    const s = memory()
    saveSort({ key: 'type', dir: 'asc' }, s)
    expect(loadSort(s)).toEqual({ key: 'type', dir: 'asc' })
  })
  it('falls back to the default for nothing stored, junk, or a throwing storage', () => {
    expect(loadSort(memory())).toEqual(DEFAULT_SORT)
    const junk = memory()
    junk.setItem('monoagent.documents.sort', '{not json')
    expect(loadSort(junk)).toEqual(DEFAULT_SORT)
    const bad = memory()
    bad.setItem('monoagent.documents.sort', JSON.stringify({ key: 'size', dir: 'up' }))
    expect(loadSort(bad)).toEqual(DEFAULT_SORT)
    const throwing = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } }
    expect(loadSort(throwing)).toEqual(DEFAULT_SORT)
    expect(() => saveSort({ key: 'name', dir: 'asc' }, throwing)).not.toThrow()
  })
})

describe('source filter', () => {
  it('lists each source once, by label', () => {
    expect(sourceOptions([upload, capture, zebra, discovered])).toEqual(['extension', 'discovered', 'upload'])
  })
  it('filters to one source, or none for the empty choice', () => {
    expect(filterBySource([upload, capture], 'extension')).toEqual([capture])
    expect(filterBySource([upload, capture], '')).toEqual([upload, capture])
  })
})
