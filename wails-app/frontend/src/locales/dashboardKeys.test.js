// Every dashboard.* key the dashboard code uses exists in English and
// Spanish, and both locales carry the same dashboard keys.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
// i18next plurals: "x_one"/"x_other" satisfy t('x', { count }).
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))

const pages = join(__dirname, '..', 'pages')
const sources = [join(pages, 'Dashboard.jsx'),
  ...readdirSync(join(pages, 'dashboard')).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(pages, 'dashboard', f))]
const literal = sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](dashboard\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1]))

// Keys built at runtime (template literals).
const attentionIds = [...readFileSync(join(pages, 'dashboard', 'attention.js'), 'utf8').matchAll(/add\('([a-zA-Z]+)'/g)].map(m => m[1])
const dynamic = [
  ...attentionIds.map(id => `dashboard.attention.${id}`),
  ...['connected', 'waiting', 'unpaired', 'off'].map(s => `dashboard.system.bridgeState.${s}`),
  ...['ok', 'decaying', 'broken', 'stale'].map(s => `dashboard.automations.${s}`),
  'dashboard.accounts.active', 'dashboard.accounts.expired',
]

describe('dashboard i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('finds the runtime-built attention keys', () => {
    expect(attentionIds.length).toBeGreaterThan(10)
  })
  it('every dashboard key used in code exists in en and es', () => {
    const used = [...new Set([...literal, ...dynamic])]
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })
  it('en and es have the same dashboard keys', () => {
    const e = enKeys.filter(k => k.startsWith('dashboard.')).sort()
    const s = esKeys.filter(k => k.startsWith('dashboard.')).sort()
    expect(s).toEqual(e)
  })
})
