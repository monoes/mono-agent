import { describe, it, expect } from 'vitest'
import {
  deriveOutputs, deriveInputs, caseHandles, portsDependOnConfig,
  isCaseListSettled, remapSourceEdges, NODE_CONFIG_FIELDS,
} from './nodeConfigFields.js'

const ids = ports => ports.map(p => p.id)

describe('deriveOutputs', () => {
  it('core.switch follows its cases then the default handle (switch.go)', () => {
    expect(ids(deriveOutputs('core.switch', {
      cases: ['active', { value: 'x', handle: 'custom' }, { value: 'nohandle' }, 'active'],
    }))).toEqual(['active', 'custom', 'default'])
    expect(ids(deriveOutputs('core.switch', { cases: ['a'], default_handle: 'rest' }))).toEqual(['a', 'rest'])
    // default handle not duplicated when a case already uses it
    expect(ids(deriveOutputs('core.switch', { cases: ['default', 'b'] }))).toEqual(['default', 'b'])
    // JSON-array string (textarea fallback editor)
    expect(ids(deriveOutputs('core.switch', { cases: '[{"value":"active","handle":"active"},"idle"]' })))
      .toEqual(['active', 'idle', 'default'])
    expect(ids(deriveOutputs('core.switch'))).toEqual(['default'])
    expect(ids(deriveOutputs('core.switch', null))).toEqual(['default'])
  })

  it('core.filter emits main/rejected like filter.go', () => {
    expect(ids(deriveOutputs('core.filter', {}))).toEqual(['main', 'rejected'])
  })

  it('ai.choose: case handles (object handle falls back to value) + low_confidence', () => {
    expect(ids(deriveOutputs('ai.choose', {
      cases: ['billing', { value: 'tech', handle: 'support' }, { value: 'other' }, '{"value":"spam","handle":"junk"}'],
    }))).toEqual(['billing', 'support', 'other', 'junk', 'low_confidence'])
    expect(ids(deriveOutputs('ai.choose', { cases: '' }))).toEqual(['low_confidence'])
    expect(ids(deriveOutputs('ai.choose', { cases: ['a', 'low_confidence'] }))).toEqual(['a', 'low_confidence'])
  })

  it('keeps the fixed ports of other types', () => {
    expect(ids(deriveOutputs('core.if', {}))).toEqual(['true', 'false'])
    expect(ids(deriveOutputs('http.request'))).toEqual(['out', 'error'])
    expect(ids(deriveOutputs('agent.ask', { cases: ['x'] }))).toEqual(['main'])
    expect(deriveOutputs('core.stop_error')).toEqual([])
    expect(deriveInputs('trigger.manual')).toEqual([])
    expect(ids(deriveInputs('ai.choose'))).toEqual(['in'])
  })
})

describe('port re-derivation helpers', () => {
  it('portsDependOnConfig', () => {
    expect(portsDependOnConfig('core.switch', 'cases')).toBe(true)
    expect(portsDependOnConfig('core.switch', 'default_handle')).toBe(true)
    expect(portsDependOnConfig('ai.choose', 'cases')).toBe(true)
    expect(portsDependOnConfig('ai.choose', 'input')).toBe(false)
    expect(portsDependOnConfig('core.filter', 'condition')).toBe(false)
  })

  it('isCaseListSettled holds ports while JSON is mid-edit', () => {
    expect(isCaseListSettled('[{"value":')).toBe(false)
    expect(isCaseListSettled('["a"]')).toBe(true)
    expect(isCaseListSettled(['a'])).toBe(true)
    expect(isCaseListSettled('a, b')).toBe(true)
  })

  it('remapSourceEdges re-points by handle and drops vanished handles', () => {
    const edges = [
      { id: 'e1', source: 'n', sourcePortId: 'b', sourcePortIdx: 1, target: 't' },
      { id: 'e2', source: 'n', sourcePortId: 'gone', sourcePortIdx: 2, target: 't' },
      { id: 'e3', source: 'other', sourcePortId: 'main', sourcePortIdx: 0, target: 'n' },
      { id: 'e4', source: 'n', sourcePortId: 'low_confidence', sourcePortIdx: 3, target: 't' },
    ]
    const outputs = deriveOutputs('ai.choose', { cases: ['b'] })
    const got = remapSourceEdges(edges, 'n', outputs)
    expect(got.map(e => [e.id, e.sourcePortIdx])).toEqual([['e1', 0], ['e3', 0], ['e4', 1]])
    expect(got[1]).toBe(edges[2])
  })

  it('caseHandles dedupes and skips blanks', () => {
    expect(caseHandles(['a', ' ', 'a', { handle: 'b' }])).toEqual(['a', 'b'])
    expect(caseHandles('a, b ,c')).toEqual(['a', 'b', 'c'])
  })
})

describe('config field fallbacks', () => {
  it('core.switch uses the backend keys and handled cases', () => {
    const f = NODE_CONFIG_FIELDS['core.switch']
    expect(f.map(x => x.key)).toContain('field')
    expect(f.map(x => x.key)).not.toContain('expression')
    const cases = JSON.parse(f.find(x => x.key === 'cases').default)
    expect(cases.every(c => c.handle)).toBe(true)
  })

  it('ai.choose has its fields', () => {
    const keys = NODE_CONFIG_FIELDS['ai.choose'].map(x => x.key)
    for (const k of ['cases', 'input', 'fields', 'instructions', 'extra_questions', 'min_confidence', 'output_key', 'api_key', 'model', 'concurrency']) {
      expect(keys).toContain(k)
    }
    expect(NODE_CONFIG_FIELDS['ai.choose'].find(x => x.key === 'api_key').default).toBe('@secret:typesafe')
  })
})
