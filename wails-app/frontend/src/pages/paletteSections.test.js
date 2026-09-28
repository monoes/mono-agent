import { describe, it, expect } from 'vitest'
import { paletteCategoryLabel, paletteSectionOf, splitPaletteSections, WEB_AUTOMATION } from './paletteSections.js'

describe('palette sections', () => {
  it('reads a category section and label from its nodes', () => {
    const acme = [{ type: 'acme-crm.list_deals', section: 'web_automation', category_label: 'Acme CRM' }]
    expect(paletteSectionOf(acme)).toBe(WEB_AUTOMATION)
    expect(paletteCategoryLabel(acme)).toBe('Acme CRM')
    expect(paletteSectionOf([{ type: 'core.if', section: 'nodes' }])).toBe('nodes')
    expect(paletteSectionOf([{ type: 'core.if' }])).toBe('nodes') // an older CLI sends no section
    expect(paletteCategoryLabel([{ type: 'core.if' }])).toBe('')
  })

  it('puts web automations first, by name, and keeps both sections', () => {
    const cats = [
      { id: 'control', label: 'CONTROL', section: 'nodes' },
      { id: 'x', label: 'X', section: WEB_AUTOMATION },
      { id: 'acme-crm', label: 'ACME CRM', section: WEB_AUTOMATION },
      { id: 'data', label: 'DATA', section: 'nodes' },
    ]
    const [web, rest] = splitPaletteSections(cats)
    expect(web.categories.map(c => c.id)).toEqual(['acme-crm', 'x'])
    expect(rest.categories.map(c => c.id)).toEqual(['control', 'data'])
    expect(splitPaletteSections([]).map(s => s.categories.length)).toEqual([0, 0])
  })
})
