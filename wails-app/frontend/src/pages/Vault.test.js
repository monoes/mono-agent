import { describe, it, expect } from 'vitest'
import { filterAndSortEntries } from './Vault.jsx'

const entry = (overrides) => ({
  id: overrides.name,
  name: '',
  username: '',
  kind: 'secret',
  field_count: 1,
  updated_at: '2026-01-01 00:00:00',
  ...overrides,
})

describe('filterAndSortEntries — search', () => {
  const entries = [
    entry({ name: 'AWS Prod Key', username: '', kind: 'secret' }),
    entry({ name: 'GitHub Login', username: 'octocat', kind: 'login' }),
    entry({ name: 'Stripe API', username: '', kind: 'secret' }),
  ]

  it('returns everything for an empty search', () => {
    expect(filterAndSortEntries(entries, { search: '' })).toHaveLength(3)
  })

  it('matches by name, case-insensitively', () => {
    const out = filterAndSortEntries(entries, { search: 'aws' })
    expect(out.map(e => e.name)).toEqual(['AWS Prod Key'])
  })

  it('matches by username', () => {
    const out = filterAndSortEntries(entries, { search: 'octocat' })
    expect(out.map(e => e.name)).toEqual(['GitHub Login'])
  })

  it('matches by kind and by its display label', () => {
    expect(filterAndSortEntries(entries, { search: 'login' }).map(e => e.name)).toEqual(['GitHub Login'])
    // KIND_LABELS maps kind "secret" -> the displayed label "keys"
    expect(filterAndSortEntries(entries, { search: 'keys' }).map(e => e.name).sort()).toEqual(['AWS Prod Key', 'Stripe API'])
  })

  it('returns nothing when no field matches', () => {
    expect(filterAndSortEntries(entries, { search: 'no-such-thing' })).toHaveLength(0)
  })

  it('ignores leading/trailing whitespace in the query', () => {
    expect(filterAndSortEntries(entries, { search: '  aws  ' })).toHaveLength(1)
  })
})

describe('filterAndSortEntries — sort', () => {
  const entries = [
    entry({ name: 'Charlie', updated_at: '2026-01-01 00:00:00' }),
    entry({ name: 'alice', updated_at: '2026-03-01 00:00:00' }),
    entry({ name: 'Bob', updated_at: '2026-02-01 00:00:00' }),
  ]

  it('sorts by name ascending, case-insensitively, by default', () => {
    const out = filterAndSortEntries(entries, { sortBy: 'name', sortDir: 'asc' })
    expect(out.map(e => e.name)).toEqual(['alice', 'Bob', 'Charlie'])
  })

  it('sorts by name descending', () => {
    const out = filterAndSortEntries(entries, { sortBy: 'name', sortDir: 'desc' })
    expect(out.map(e => e.name)).toEqual(['Charlie', 'Bob', 'alice'])
  })

  it('sorts by date ascending (oldest first)', () => {
    const out = filterAndSortEntries(entries, { sortBy: 'date', sortDir: 'asc' })
    expect(out.map(e => e.name)).toEqual(['Charlie', 'Bob', 'alice'])
  })

  it('sorts by date descending (newest first)', () => {
    const out = filterAndSortEntries(entries, { sortBy: 'date', sortDir: 'desc' })
    expect(out.map(e => e.name)).toEqual(['alice', 'Bob', 'Charlie'])
  })

  it('treats a missing/unparseable date as oldest, not as a crash', () => {
    const withMissing = [
      entry({ name: 'HasDate', updated_at: '2026-01-01 00:00:00' }),
      entry({ name: 'NoDate', updated_at: '' }),
      entry({ name: 'BadDate', updated_at: 'not-a-date' }),
    ]
    const out = filterAndSortEntries(withMissing, { sortBy: 'date', sortDir: 'asc' })
    expect(out[0].name === 'NoDate' || out[0].name === 'BadDate').toBe(true)
    expect(out[out.length - 1].name).toBe('HasDate')
  })

  it('does not mutate the input array', () => {
    const copy = [...entries]
    filterAndSortEntries(entries, { sortBy: 'name', sortDir: 'desc' })
    expect(entries).toEqual(copy)
  })
})

describe('filterAndSortEntries — search and sort combined', () => {
  it('filters first, then sorts the remaining subset', () => {
    const entries = [
      entry({ name: 'Zebra Secret', kind: 'secret', updated_at: '2026-01-01 00:00:00' }),
      entry({ name: 'Apple Login', kind: 'login', updated_at: '2026-02-01 00:00:00' }),
      entry({ name: 'Mango Secret', kind: 'secret', updated_at: '2026-03-01 00:00:00' }),
    ]
    const out = filterAndSortEntries(entries, { search: 'secret', sortBy: 'name', sortDir: 'asc' })
    expect(out.map(e => e.name)).toEqual(['Mango Secret', 'Zebra Secret'])
  })
})
