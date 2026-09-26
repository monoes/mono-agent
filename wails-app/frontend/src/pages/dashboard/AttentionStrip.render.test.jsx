// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: k => k }) }))
import AttentionStrip from './AttentionStrip.jsx'

afterEach(cleanup)

describe('AttentionStrip', () => {
  const items = [
    { id: 'hilApprovals', count: 2, severity: 'warn', labelKey: 'dashboard.attention.hilApprovals', target: { hil: true } },
    { id: 'orgNeedsYou', count: 1, severity: 'warn', labelKey: 'dashboard.attention.orgNeedsYou', target: { page: 'orgs' } },
    { id: 'health', count: 3, severity: 'danger', labelKey: 'dashboard.attention.health', target: { page: 'settings', data: { section: 'health' } } },
  ]
  it('opens the HIL drawer or navigates', () => {
    const onNavigate = vi.fn()
    const onOpenHil = vi.fn()
    render(<AttentionStrip items={items} onNavigate={onNavigate} onOpenHil={onOpenHil} />)
    fireEvent.click(screen.getByText('dashboard.attention.hilApprovals'))
    expect(onOpenHil).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByText('dashboard.attention.orgNeedsYou'))
    expect(onNavigate).toHaveBeenCalledWith('orgs', undefined)
    fireEvent.click(screen.getByText('dashboard.attention.health'))
    expect(onNavigate).toHaveBeenCalledWith('settings', { section: 'health' })
  })
  it('all clear, and nothing while loading', () => {
    const { rerender, container } = render(<AttentionStrip items={[]} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.attention.allClear')).toBeInTheDocument()
    rerender(<AttentionStrip items={[]} loading onNavigate={vi.fn()} />)
    expect(container).toBeEmptyDOMElement()
  })
})
