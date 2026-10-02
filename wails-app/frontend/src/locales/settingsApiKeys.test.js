// Every settings.api.* key the OpenAI-compatible API section uses exists in
// English and Spanish, both locales carry the same settings.api keys, and none of
// them is dead. Keys are written out in full in the sources (no template
// literals), which is what lets this scan see them.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
// i18next plurals: "x_one"/"x_other" satisfy t('x', { count }).
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))

const settings = join(__dirname, '..', 'components', 'settings')
const sources = [
  join(settings, 'ApiSection.jsx'),
  ...readdirSync(join(settings, 'api')).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(settings, 'api', f)),
].filter(existsSync)
const used = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](settings\.api\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]

describe('settings.api i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  const mine = keys => keys.filter(k => k.startsWith('settings.api.')).sort()

  it('en and es have the same settings.api keys', () => {
    expect(mine(enKeys).length).toBeGreaterThan(50)
    expect(mine(esKeys)).toEqual(mine(enKeys))
  })

  it('every key the section uses exists in en and es', () => {
    expect(used.length).toBeGreaterThan(0)
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })

  it('no settings.api key is left unused', () => {
    const base = k => k.replace(/_(one|other)$/, '')
    expect(mine(enKeys).filter(k => !used.includes(base(k)))).toEqual([])
  })
})
