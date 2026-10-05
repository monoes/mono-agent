import { describe, it, expect } from 'vitest'
import { NODE_W, NODE_H } from './orgGraph.js'
import {
  parseSections, sectionsEnabled, sectionOfRole, sectionRects, dropMembership, documentEdges,
  sectionErrors, layOutSections, sectionEdgePath, freeSectionName, placeNewRole, sectionAtPoint,
} from './sectionsGraph.js'

const wire = {
  alpha: { lead: 'a1', members: ['a1', 'a2'], publishes: ['spec'], budget: { usd: 5 }, writes: ['src/**'], max_rework_rounds: 2 },
  beta: { members: ['b1'], consumes: ['spec'] },
}
const list = parseSections(wire)
const node = (id, parentId, x, y, rest) => ({ id, parentId, x, y, rest })
const nodes = [
  node('boss', null, 200, 0),
  node('a1', 'boss', 0, 200), node('a2', 'a1', 0, 340),
  node('b1', 'boss', 600, 200),
]

describe('parseSections', () => {
  it('reads lead, members, budget, writes, rework rounds and edges', () => {
    expect(list.map(s => s.name)).toEqual(['alpha', 'beta'])
    expect(list[0]).toMatchObject({ leadId: 'a1', budgetUsd: 5, writes: ['src/**'], maxReworkRounds: 2, publishes: ['spec'] })
    expect(list[1]).toMatchObject({ leadId: 'b1', lead: '', budgetUsd: null })
  })
  it('keeps a dedicated lead outside members in the roster', () => {
    const s = parseSections({ x: { lead: 'l', members: ['m'] } })[0]
    expect(s.roster).toEqual(['m', 'l'])
  })
  it('tolerates junk', () => {
    expect(parseSections(null)).toEqual([])
    expect(parseSections({ a: 'nope', b: { members: 5 } })[0].roster).toEqual([])
  })
  it('is enabled only with a rostered section', () => {
    expect(sectionsEnabled(list)).toBe(true)
    expect(sectionsEnabled(parseSections({}))).toBe(false)
    expect(sectionsEnabled(parseSections({ a: {} }))).toBe(false)
  })
})

describe('membership by drop', () => {
  const rects = sectionRects(nodes, list)
  it('builds a container around each roster', () => {
    const a = rects.find(r => r.name === 'alpha')
    expect(a.x).toBeLessThan(0)
    expect(a.x + a.w).toBeGreaterThan(NODE_W)
    expect(a.y + a.h).toBeGreaterThan(340 + NODE_H)
  })
  it('stays when dropped inside its own container', () => {
    expect(dropMembership(rects, list, { ...nodes[2], x: 10, y: 300 })).toEqual({ kind: 'stay' })
  })
  it('moves when dropped inside another container', () => {
    expect(dropMembership(rects, list, { ...nodes[2], x: 610, y: 210 })).toEqual({ kind: 'move', to: 'beta' })
  })
  it('refuses a non-root role dropped outside every section, with the reason', () => {
    const r = dropMembership(rects, list, { ...nodes[2], x: 2000, y: 2000 })
    expect(r.kind).toBe('refuse')
    expect(r.reason).toContain('outside every section can only be the root')
  })
  it('refuses the root inside a section, lets it stay outside', () => {
    expect(dropMembership(rects, list, { ...nodes[0], x: 0, y: 200 }).kind).toBe('refuse')
    expect(dropMembership(rects, list, nodes[0])).toEqual({ kind: 'stay' })
  })
  it('exempts automation endpoints', () => {
    expect(dropMembership(rects, list, node('hook', 'boss', 3000, 3000, { kind: 'endpoint' }))).toEqual({ kind: 'stay' })
  })
  it('finds the container under a point', () => {
    expect(sectionAtPoint(rects, 10, 220).name).toBe('alpha')
    expect(sectionAtPoint(rects, 5000, 5000)).toBeNull()
  })
})

describe('document edges', () => {
  it('goes from each publisher to each consumer', () => {
    expect(documentEdges(list)).toEqual([{ type: 'spec', from: 'alpha', to: 'beta' }])
  })
})

describe('sectionErrors', () => {
  it('flags a non-root role in no section, like monomind', () => {
    const e = sectionErrors([...nodes, node('x', 'boss', 0, 0)], list)
    expect(e).toEqual(["roles.x: a role outside every section can only be the root — add it to a section's members or make it a lead"])
  })
  it('is silent for a plain org and for endpoints', () => {
    expect(sectionErrors([node('x', 'boss', 0, 0)], [])).toEqual([])
    expect(sectionErrors([...nodes, node('h', 'boss', 0, 0, { kind: 'endpoint' })], list)).toEqual([])
  })
})

describe('layOutSections', () => {
  it('places the root above and one lane per section, leaving placed nodes alone', () => {
    const bare = nodes.map(n => ({ ...n, x: undefined, y: undefined }))
    const out = layOutSections(bare, list)
    const at = (id) => out.find(n => n.id === id)
    expect(out.every(n => typeof n.x === 'number' && typeof n.y === 'number')).toBe(true)
    expect(at('boss').y).toBeLessThan(at('a1').y)
    expect(at('a1').y).toBeLessThan(at('a2').y)
    expect(at('b1').x).toBeGreaterThan(at('a2').x + NODE_W)
    const rs = sectionRects(out, list)
    const [ra, rb] = rs
    expect(ra.x + ra.w).toBeLessThan(rb.x)
  })
  it('does not move a placed node', () => {
    const half = nodes.map(n => (n.id === 'b1' ? { ...n, x: undefined, y: undefined } : n))
    const out = layOutSections(half, list)
    expect(out.find(n => n.id === 'a1')).toMatchObject({ x: 0, y: 200 })
    expect(out.find(n => n.id === 'b1').x).toBeGreaterThan(NODE_W)
  })
  it('is a no-op for a plain org', () => {
    expect(layOutSections(nodes, [])).toBe(nodes)
  })
})

describe('misc', () => {
  it('sectionOfRole', () => expect(sectionOfRole(list, 'b1')).toBe('beta'))
  it('edge path starts at the facing sides', () => {
    const [a, b] = sectionRects(nodes, list)
    const { d, mid } = sectionEdgePath(a, b)
    expect(d.startsWith(`M${a.x + a.w},`)).toBe(true)
    expect(mid.x).toBeGreaterThan(a.x + a.w)
  })
  it('freeSectionName', () => {
    expect(freeSectionName(list, 'gamma')).toBe('gamma')
    expect(freeSectionName(list, 'alpha')).toBe('alpha-2')
  })
  it('placeNewRole prefers the anchor section and reports to the anchor or the lead', () => {
    expect(placeNewRole({ list, nodes, anchorId: 'a2' })).toEqual({ section: 'alpha', parent: 'a2' })
    expect(placeNewRole({ list, nodes, anchorId: 'boss', selectedSection: 'beta' })).toEqual({ section: 'beta', parent: 'b1' })
    expect(placeNewRole({ list, nodes, anchorId: 'boss' })).toBeNull()
    expect(placeNewRole({ list: [list[0]], nodes, anchorId: 'boss' })).toEqual({ section: 'alpha', parent: 'a1' })
  })
})
