import { describe, it, expect } from 'vitest'
import { filterApplications } from './Applications.jsx'

const app = (overrides) => ({
  id: overrides.id || 'a1',
  kind: 'job',
  status: 'pending',
  title: '',
  company: '',
  url: '',
  updated_at: '2026-01-01 00:00:00',
  tags: [],
  ...overrides,
})

describe('filterApplications', () => {
  const apps = [
    app({ id: '1', title: 'Backend Engineer', company: 'Acme', status: 'pending' }),
    app({ id: '2', title: 'Frontend Engineer', company: 'Beta', status: 'applied' }),
    app({ id: '3', title: 'Data Engineer', company: 'Acme', status: 'pending', tags: ['fit:strong-fit'] }),
  ]

  it('returns everything for statusTab "all" and empty search', () => {
    expect(filterApplications(apps, { statusTab: 'all', search: '' })).toHaveLength(3)
  })

  it('filters by status tab', () => {
    const out = filterApplications(apps, { statusTab: 'pending', search: '' })
    expect(out.map(a => a.id)).toEqual(['1', '3'])
  })

  it('filters by search matching title', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'backend' })
    expect(out.map(a => a.id)).toEqual(['1'])
  })

  it('filters by search matching company', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'acme' })
    expect(out.map(a => a.id).sort()).toEqual(['1', '3'])
  })

  it('filters by search matching a tag', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'strong-fit' })
    expect(out.map(a => a.id)).toEqual(['3'])
  })

  it('combines status tab and search', () => {
    const out = filterApplications(apps, { statusTab: 'pending', search: 'acme' })
    expect(out.map(a => a.id).sort()).toEqual(['1', '3'])
  })

  it('does not mutate the input array', () => {
    const copy = [...apps]
    filterApplications(apps, { statusTab: 'pending', search: '' })
    expect(apps).toEqual(copy)
  })
})
