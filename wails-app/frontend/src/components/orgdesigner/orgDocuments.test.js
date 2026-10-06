import { describe, it, expect } from 'vitest'
import busRaw from './__fixtures__/documents-bus.ndjson?raw'
import { isDocsEvent, normalizeView, statusMeta, versionLabel } from './orgDocuments.js'

const bus = busRaw.trim().split('\n').map(l => JSON.parse(l))

describe('isDocsEvent', () => {
  it('flags the document events of the recorded bus shapes', () => {
    expect(bus.map(isDocsEvent)).toEqual([true, true, true, true, false, false])
  })
  it('flags org_doc_* tools and rework audits, nothing else', () => {
    expect(isDocsEvent({ type: 'tool', tool: 'org_doc_decide' })).toBe(true)
    expect(isDocsEvent({ type: 'tool', tool: 'Bash' })).toBe(false)
    expect(isDocsEvent({ type: 'audit', reason: 'rework-exhausted' })).toBe(true)
    expect(isDocsEvent({ type: 'message', from: 'lead' })).toBe(false)
  })
  it('never throws on unknown kinds, old loops events or junk', () => {
    for (const ev of [{ type: 'loops', loop: 'x' }, { type: 'loop', round: 2 }, { type: 'section_status' }, { type: 7 }, {}, null, undefined, 'str', 4, []]) {
      expect(() => isDocsEvent(ev)).not.toThrow()
      expect(isDocsEvent(ev)).toBe(false)
    }
  })
})

describe('normalizeView', () => {
  it('is null for errors and non-views, and fills missing lists', () => {
    expect(normalizeView(null)).toBeNull()
    expect(normalizeView({ error: 'nope' })).toBeNull()
    const v = normalizeView({ v: 1, docs: [null, { id: 'a' }] })
    expect(v.docs).toEqual([{ id: 'a' }])
    expect(v.sections).toEqual([])
    expect(v.summary).toEqual({})
  })
})

describe('labels', () => {
  it('names the four statuses and tolerates an unknown one', () => {
    expect(statusMeta('pending').label).toBe('published')
    expect(statusMeta('accepted').label).toBe('accepted')
    expect(statusMeta('from-the-future').label).toBe('from-the-future')
    expect(versionLabel({ status: 'rejected', superseded_by: 2 })).toBe('rejected, superseded by v2')
    expect(versionLabel({ status: 'rejected' })).toBe('rejected')
  })
})
