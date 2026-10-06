import { describe, it, expect } from 'vitest'
import { bySection, roleBudgets, roleBadge, budgetLine, budgetBadge, budgetTitle, usd } from './budgetModel.js'
import recorded from './__fixtures__/org-budget-recorded.json'

// org-budget-recorded.json is the Go SectionBudgets output for the recorded
// budgeted run internal/monomind/testdata/monomind-2.24.1/budget-*. The
// "warning" copy is review as it stood at its recorded section-budget-warning
// event ($0.034 of $0.04), before the closure.
const report = recorded
const warning = { ...recorded, sections: recorded.sections.map(s => (s.name === 'review'
  ? { ...s, spent_usd: 0.034, remaining_usd: 0.006, fraction: 0.85, state: 'warn', soft_closed: false, closed_at: 0, closed_roles: [] } : s)) }
describe('budgetModel', () => {
  it('indexes sections and tolerates errors', () => {
    expect(Object.keys(bySection(report))).toEqual(['drafting', 'review'])
    expect(bySection({ error: 'x' })).toEqual({})
    expect(bySection(null)).toEqual({})
  })
  it('formats spend against allocation', () => {
    const b = bySection(report)
    expect(budgetLine(b.drafting)).toBe('$0.07 / $0.05')
    expect(budgetLine({ spent_usd: 0.5 })).toBe('$0.50 spent')
    expect(usd(undefined)).toBe('—')
  })
  it('prefers the soft-closure event, then warns with what is left', () => {
    const b = bySection(report)
    expect(budgetBadge(b.drafting).label).toBe('soft-closed')
    expect(budgetBadge(bySection(warning).review)).toMatchObject({ key: 'warn', label: '$0.01 left' })
    expect(budgetBadge({ state: 'closed' }).label).toBe('at allocation')
    expect(budgetBadge({ state: 'ok' })).toBeNull()
    expect(budgetTitle(b.drafting)).toContain('closed: writer')
    expect(budgetTitle(bySection(warning).review)).toContain('will soft-close at 100%')
  })
  it('badges roles with a cap, warning near it', () => {
    const caps = roleBudgets(warning)
    expect(Object.keys(caps).sort()).toEqual(['checker', 'lead', 'writer'])
    expect(roleBadge(caps.lead)).toMatchObject({ text: '$0.02/$0.06', state: 'ok' })
    expect(roleBadge({ ...caps.checker, spent_usd: 0.034, fraction: 0.85, state: 'warn' }).title).toContain('near its cap')
    expect(roleBadge(caps.writer)).toMatchObject({ state: 'closed' })
    expect(roleBadge({ id: 'x', spent_usd: 1 })).toBeNull()
    expect(roleBudgets({ error: 'x' })).toEqual({})
  })
})
