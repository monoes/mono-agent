// Every account.* key the gate and its banners use exists in English and
// Spanish, both locales carry the same account keys with the same placeholders,
// none is dead, and the Spanish is not a copy of the English. Keys are written
// out in full in the sources (no template literals), which is what lets this
// scan see them.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [[`${p}${k}`, v]]))
const keysOf = (locale) => flat(locale.account, 'account.').map(([k]) => k).sort()
const valueOf = (locale, key) => Object.fromEntries(flat(locale))[key]
// i18next plurals: "x_one"/"x_other" satisfy t('x', { count }).
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))
const base = (k) => k.replace(/_(one|other)$/, '')
const placeholders = (s) => [...String(s).matchAll(/\{\{(\w+)\}\}/g)].map(m => m[1]).sort()

const dir = join(__dirname, '..', 'components', 'account')
const sources = readdirSync(dir).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(dir, f))
const used = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](account\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]

describe('account i18n', () => {
  it('en and es have the same account keys', () => {
    expect(keysOf(en).length).toBeGreaterThan(30)
    expect(keysOf(es)).toEqual(keysOf(en))
  })

  it('every key the gate uses exists in en and es', () => {
    expect(used.length).toBeGreaterThan(25)
    expect(used.filter(k => !has(keysOf(en), k))).toEqual([])
    expect(used.filter(k => !has(keysOf(es), k))).toEqual([])
  })

  it('no account key is left unused', () => {
    expect(keysOf(en).filter(k => !used.includes(base(k)))).toEqual([])
  })

  it('both languages use the same placeholders in each string', () => {
    for (const key of keysOf(en)) {
      expect(placeholders(valueOf(es, key)), key).toEqual(placeholders(valueOf(en, key)))
    }
  })

  it('the Spanish is translated, not copied', () => {
    expect(keysOf(en).filter(k => valueOf(es, k) === valueOf(en, k))).toEqual([])
  })
})
