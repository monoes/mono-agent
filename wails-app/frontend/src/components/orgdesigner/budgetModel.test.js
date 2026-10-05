import { describe, it, expect } from 'vitest'
import { bySection, budgetLine, budgetBadge, budgetTitle, usd } from './budgetModel.js'
import report from './__fixtures__/org-budget-synthetic.json'

// org-budget-synthetic.json is the Go SectionBudgets output for the SYNTHETIC
// fixture internal/monomind/testdata/monomind-2.24.1/synthetic-*.
describe('budgetModel', () => {
  it('indexes sections and tolerates errors', () => {
    expect(Object.keys(bySection(report))).toEqual(['drafting', 'review'])
    expect(bySection({ error: 'x' })).toEqual({})
    expect(bySection(null)).toEqual({})
  })
  it('formats spend against allocation', () => {
    const b = bySection(report)
    expect(budgetLine(b.drafting)).toBe('$1.00 / $1.00')
    expect(budgetLine({ spent_usd: 0.5 })).toBe('$0.50 spent')
    expect(usd(undefined)).toBe('—')
  })
  it('prefers the soft-closure event, then warns with what is left', () => {
    const b = bySection(report)
    expect(budgetBadge(b.drafting).label).toBe('soft-closed')
    expect(budgetBadge(b.review)).toMatchObject({ key: 'warn', label: '$0.08 left' })
    expect(budgetBadge({ state: 'closed' }).label).toBe('at allocation')
    expect(budgetBadge({ state: 'ok' })).toBeNull()
    expect(budgetTitle(b.drafting)).toContain('closed: writer, editor')
    expect(budgetTitle(b.review)).toContain('will soft-close at 100%')
  })
})
