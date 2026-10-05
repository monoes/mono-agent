// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, within, cleanup, waitFor, act } from '@testing-library/react'
import { SectionBudgetView } from './SectionBudgetPanel.jsx'
import SectionBudgetPanel from './SectionBudgetPanel.jsx'
import { SectionBoxes } from '../orgdesigner/SectionLayer.jsx'
import { bySection } from '../orgdesigner/budgetModel.js'
import report from '../orgdesigner/__fixtures__/org-budget-synthetic.json'
import { api, onOrgEvent } from '../../services/api.js'

vi.mock('../../services/api.js', () => ({
  api: { getOrgBudget: vi.fn() },
  onOrgEvent: vi.fn(() => () => {}),
}))

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('SectionBudgetView (org status panel)', () => {
  it('shows each section against its allocation, role caps, the soft closure and totals', () => {
    render(<SectionBudgetView report={report} />)
    const d = screen.getByTestId('budget-section-drafting')
    expect(within(d).getByText('$1.00 / $1.00')).toBeInTheDocument()
    expect(within(d).getByTestId('budget-badge')).toHaveTextContent('soft-closed')
    expect(within(d).getByTestId('role-cap-writer')).toHaveTextContent('writer $0.60/$0.60')
    expect(within(d).getByTestId('soft-closure')).toHaveTextContent('closed: writer, editor · 1 task(s) held')
    const r = screen.getByTestId('budget-section-review')
    expect(within(r).getByText('$0.42 / $0.50')).toBeInTheDocument()
    expect(within(r).getByTestId('budget-badge')).toHaveTextContent('$0.08 left')
    expect(screen.getByTestId('budget-total')).toHaveTextContent('org total $1.47 / $2.00')
    expect(screen.queryByText(/cost incomplete/)).toBeNull()
  })
  it('says so when a usage event had no cost, and renders nothing without budgets', () => {
    const unknown = { ...report, cost_complete: false }
    render(<SectionBudgetView report={unknown} />)
    expect(screen.getByText(/cost incomplete: lower bound/)).toBeInTheDocument()
    cleanup()
    const none = { sections: [{ kind: 'section', name: 'a', state: 'unallocated', spent_usd: 0 }], total: { state: 'unallocated' } }
    const { container } = render(<SectionBudgetView report={none} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('SectionBudgetPanel fetch', () => {
  it('fetches the run and refetches on a usage event while live', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let emit
    onOrgEvent.mockImplementation((cb) => { emit = cb; return () => {} })
    api.getOrgBudget.mockResolvedValue(report)
    render(<SectionBudgetPanel orgName="bud" live />)
    await waitFor(() => expect(screen.getByTestId('section-budget-panel')).toBeInTheDocument())
    expect(api.getOrgBudget).toHaveBeenCalledWith('bud', '')
    act(() => emit({ orgName: 'bud', event: { type: 'usage' } }))
    act(() => emit({ orgName: 'other', event: { type: 'usage' } }))
    act(() => emit({ orgName: 'bud', event: { type: 'status' } }))
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(api.getOrgBudget).toHaveBeenCalledTimes(2)
    vi.useRealTimers()
  })
})

describe('section header on the canvas', () => {
  const rects = [{ name: 'drafting', x: 0, y: 0, w: 300, h: 200, color: '#38bdf8' }, { name: 'review', x: 400, y: 0, w: 300, h: 200, color: '#a78bfa' }]
  const sections = [{ name: 'drafting', leadId: 'writer', budgetUsd: 1 }, { name: 'review', leadId: 'checker', budgetUsd: 0.5 }]
  it('shows live spend, the soft-closed badge and the warning before the cap', () => {
    render(<SectionBoxes rects={rects} sections={sections} selectedName={null} readOnly budgets={bySection(report)} />)
    expect(screen.getByTestId('section-budget-drafting')).toHaveTextContent('$1.00 / $1.00')
    expect(screen.getByTestId('section-budget-badge-drafting')).toHaveTextContent('soft-closed')
    expect(screen.getByTestId('section-budget-review')).toHaveTextContent('$0.42 / $0.50')
    expect(screen.getByTestId('section-budget-badge-review')).toHaveTextContent('$0.08 left')
  })
  it('keeps the declared budget in the design view', () => {
    render(<SectionBoxes rects={rects} sections={sections} selectedName={null} readOnly={false} />)
    expect(screen.getByTestId('section-header-review')).toHaveTextContent('$0.5')
    expect(screen.queryByTestId('section-budget-review')).toBeNull()
  })
})
