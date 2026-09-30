// Every orgBubble.* key the org bubble (#229) uses exists in English and
// Spanish, including the ones built from a status, a resolution or a verb.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))

const sources = [
  'bubbles/OrgBubbleOverlay.jsx', 'bubbles/OrgChatView.jsx', 'bubbles/BubbleDock.jsx', 'bubbles/CoderBubbles.jsx', 'OrgsPanel.jsx',
].map(f => join(__dirname, '..', 'components', f))
const literal = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`](orgBubble\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]
const dynamic = [
  ...['running', 'paused', 'stopped', 'neverRun', 'loading'].map(s => `orgBubble.status.${s}`),
  ...['answered', 'approved', 'denied', 'rejected'].map(s => `orgBubble.state.${s}`),
  ...['pause', 'resume', 'stop'].flatMap(v => [`orgBubble.${v}`, `orgBubble.confirm.${v}Title`, `orgBubble.confirm.${v}Body`]),
  'stage.status.blocked', 'stage.direction.message',
]

describe('org bubble i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('finds the keys the org bubble code uses', () => {
    expect(literal.length).toBeGreaterThan(30)
  })
  it('has every used key in English and Spanish', () => {
    const used = [...literal, ...dynamic]
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })
  it('carries the same orgBubble keys in both locales', () => {
    const s = keys => keys.filter(k => k.startsWith('orgBubble.')).sort()
    expect(s(esKeys)).toEqual(s(enKeys))
  })
})
