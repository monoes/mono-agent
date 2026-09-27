import { describe, it, expect } from 'vitest'
import { loadScope, saveScope, ownRow, currentProfileId, SCOPE_KEY } from './scope.js'

const mem = () => { const d = {}; return { d, getItem: k => d[k] ?? null, setItem: (k, v) => { d[k] = v } } }
const broken = { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('blocked') } }

describe('dashboard scope', () => {
  it('defaults to this profile, remembers a choice, ignores junk', () => {
    const s = mem()
    expect(loadScope(s)).toBe('profile')
    saveScope('global', s)
    expect(s.d[SCOPE_KEY]).toBe('global')
    expect(loadScope(s)).toBe('global')
    s.d[SCOPE_KEY] = 'everything'
    expect(loadScope(s)).toBe('profile')
  })
  it('survives blocked storage', () => {
    expect(loadScope(broken)).toBe('profile')
    expect(() => saveScope('global', broken)).not.toThrow()
  })
  it('a row is ours when untagged or tagged with the current profile', () => {
    expect(ownRow({ id: 'w1' }, 'default')).toBe(true)
    expect(ownRow({ profile_id: 'default' }, 'default')).toBe(true)
    expect(ownRow({ profile_id: 'p-work' }, 'default')).toBe(false)
  })
  it('finds the current profile in either view', () => {
    expect(currentProfileId({ profiles: [{ id: 'a' }, { id: 'b', current: true }] })).toBe('b')
    expect(currentProfileId({ profile_id: 'default' })).toBe('default')
    expect(currentProfileId(null)).toBe('')
  })
})
