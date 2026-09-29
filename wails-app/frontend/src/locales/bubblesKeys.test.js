// Every bubbles.* key the coder bubble code uses exists in English and
// Spanish, and both locales carry the same bubbles keys.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
// i18next plurals: "x_one"/"x_other" satisfy t('x', { count }).
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))

const dir = join(__dirname, '..', 'components', 'bubbles')
const sources = readdirSync(dir).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(dir, f))
const used = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](bubbles\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]

describe('bubbles i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('finds the keys the bubble code uses', () => {
    expect(used.length).toBeGreaterThan(30)
  })
  it('has every used key in English and Spanish', () => {
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })
  it('carries the same bubbles keys in both locales', () => {
    const b = keys => keys.filter(k => k.startsWith('bubbles.')).sort()
    expect(b(esKeys)).toEqual(b(enKeys))
  })
})
