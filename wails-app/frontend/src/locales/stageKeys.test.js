// Every stage.* key the org stage (#228) uses exists in English and
// Spanish, including the ones built from a status, tool kind or event type.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import en from './en.json'
import es from './es.json'

const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
const has = (keys, k) => keys.includes(k) || (keys.includes(`${k}_one`) && keys.includes(`${k}_other`))

const dirs = ['stage', 'bubbles'].map(d => join(__dirname, '..', 'components', d))
const sources = dirs.flatMap(dir => readdirSync(dir).filter(f => /\.jsx?$/.test(f) && !f.includes('.test.')).map(f => join(dir, f)))
const literal = [...new Set(sources.flatMap(f => [...readFileSync(f, 'utf8').matchAll(/['"`]((?:stage|bubbles)\.[a-zA-Z0-9_.]+)['"`]/g)].map(m => m[1])))]
const dynamic = [
  ...['idle', 'queued', 'starting', 'working', 'waiting_lease', 'done', 'failed', 'cancelled'].map(s => `stage.status.${s}`),
  ...['edit', 'write', 'patch', 'read', 'shell', 'web', 'search', 'task', 'mcp', 'todo', 'org', 'other'].map(k => `stage.doing.${k}`),
  ...['brief', 'result', 'followup', 'question'].map(d => `stage.direction.${d}`),
  ...['spawned', 'brief', 'followup', 'result', 'question', 'status', 'reassigned', 'finished', 'message'].map(k => `stage.feed.${k}`),
  'stage.lease.pen', 'stage.lease.browser',
  ...['org_roster', 'org_spawn', 'org_wait', 'org_message', 'org_stop'].map(k => `stage.orgTool.${k}`),
]

describe('stage i18n', () => {
  const enKeys = flat(en)
  const esKeys = flat(es)
  it('finds the keys the stage code uses', () => {
    expect(literal.filter(k => k.startsWith('stage.')).length).toBeGreaterThan(40)
  })
  it('has every used key in English and Spanish', () => {
    const used = [...literal, ...dynamic]
    expect(used.filter(k => !has(enKeys, k))).toEqual([])
    expect(used.filter(k => !has(esKeys, k))).toEqual([])
  })
  it('carries the same stage keys in both locales', () => {
    const s = keys => keys.filter(k => k.startsWith('stage.')).sort()
    expect(s(esKeys)).toEqual(s(enKeys))
  })
})
