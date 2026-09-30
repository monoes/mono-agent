// Every agentRow.* key AgentRow uses exists in English and Spanish.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))
const src = readFileSync(join(__dirname, '..', 'components', 'chat', 'AgentRow.jsx'), 'utf8')
const used = [...new Set([...src.matchAll(/['"`](agentRow\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1]))]

describe('agentRow i18n', () => {
  it('has every key in both locales, and the same keys', () => {
    const enKeys = flat(en), esKeys = flat(es)
    expect(used.length).toBeGreaterThan(5)
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
    const a = keys => keys.filter(k => k.startsWith('agentRow.')).sort()
    expect(a(esKeys)).toEqual(a(enKeys))
    for (const s of ['queued', 'starting', 'working', 'waiting_lease', 'done', 'failed', 'cancelled', 'idle']) {
      expect(enKeys).toContain(`agentRow.status.${s}`)
    }
  })
})
