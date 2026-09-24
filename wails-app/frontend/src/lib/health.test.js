import { describe, it, expect } from 'vitest'
import { groupReport, summarize, fixPlan, versionSkew } from './health.js'

const report = {
  v: 1,
  results: [
    { id: 'core.db', group: 'core', title: 'Database', status: 'fail', required: true,
      fix: { id: 'core.db.migrate', label: 'Migrate', safety: 'auto' } },
    { id: 'core.disk', group: 'core', title: 'Disk', status: 'ok' },
    { id: 'monomind.node', group: 'monomind', title: 'Node.js', status: 'fail',
      fix: { id: 'monomind.node.install', label: 'Install Node', safety: 'confirm' } },
    { id: 'runtimes.agents', group: 'runtimes', title: 'Runtimes', status: 'ok' },
    { id: 'runtimes.codex', group: 'runtimes', parent: 'runtimes.agents', title: 'codex', status: 'info',
      fix: { id: 'runtimes.install:codex', label: 'Install codex', safety: 'confirm', optional: true } },
    { id: 'browser.extension', group: 'browser', title: 'Extension', status: 'warn',
      fix: { id: 'browser.extension.install', label: 'Load it', safety: 'manual', command: 'chrome://extensions' } },
    { id: 'monomind.doctor', group: 'monomind', title: 'monomind checks', status: 'warn' },
    { id: 'monomind.doctor.helpers', group: 'monomind', parent: 'monomind.doctor', title: 'Helper Files', status: 'warn',
      fix: { id: 'monomind.doctor.fix:helpers', label: 'Fix helpers', safety: 'auto' } },
  ],
}

describe('groupReport', () => {
  it('keeps group order and nests children under their parent', () => {
    const groups = groupReport(report)
    expect(groups.map(g => g.group)).toEqual(['core', 'monomind', 'runtimes', 'browser'])
    const runtimes = groups.find(g => g.group === 'runtimes')
    expect(runtimes.rows).toHaveLength(1)
    expect(runtimes.rows[0].children.map(c => c.id)).toEqual(['runtimes.codex'])
    const monomind = groups.find(g => g.group === 'monomind')
    expect(monomind.problems).toBe(3) // node + doctor parent + helpers child
  })
})

describe('summarize', () => {
  it('reports a required failure as broken', () => {
    const s = summarize(report)
    expect(s.level).toBe('broken')
    expect(s.required).toBe(1)
    expect(s.issues).toBe(5)
  })
  it('is ok with no problems', () => {
    expect(summarize({ results: [{ id: 'a', status: 'ok' }] }).level).toBe('ok')
    expect(summarize(null).level).toBe('ok')
  })
})

describe('fixPlan', () => {
  it('lists auto and confirm fixes of problem rows, never manual or optional ones', () => {
    expect(fixPlan(report).map(f => f.id)).toEqual(['core.db.migrate', 'monomind.node.install', 'monomind.doctor.fix:helpers'])
  })
})

describe('versionSkew', () => {
  it('only compares release builds', () => {
    expect(versionSkew('v1.2.0', 'v1.3.0')).toBe(true)
    expect(versionSkew('v1.2.0', '1.2.0')).toBe(false)
    expect(versionSkew('dev', 'v1.3.0')).toBe(false)
    expect(versionSkew('v0.54.0-7-ged37834', 'v0.54.0')).toBe(false)
  })
})
