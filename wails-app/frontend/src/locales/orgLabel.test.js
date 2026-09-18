// C-25: the sidebar item is labelled "Org" (singular product concept); the
// i18n key stays `orgs` so routing and saved state are untouched.
import { describe, it, expect } from 'vitest'
import en from './en.json'
import es from './es.json'

describe('sidebar org label', () => {
  it('reads "Org" in English under the unchanged key', () => {
    expect(en.sidebar.nav.orgs).toBe('Org')
  })
  it('is singular in Spanish under the unchanged key', () => {
    expect(es.sidebar.nav.orgs).toBe('Organización')
  })
})
