// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.count != null ? `${k}:${o.count}` : k) }) }))
import OrgsCard from './OrgsCard.jsx'

afterEach(cleanup)

describe('OrgsCard', () => {
  it('one row per org with level, needs-you and queued', () => {
    const onNavigate = vi.fn()
    render(<OrgsCard onNavigate={onNavigate} orgs={{ orgs: [
      { name: 'acme', running: true, level: 'mid', paused: false, queued: 1, needs_you: 2 },
      { name: 'ops', running: false, level: 'manual', paused: true, queued: 0, needs_you: null, needs_you_error: 'monomind down' },
    ], totals: { orgs: 2 } }} />)
    expect(screen.getByText('orgs.autonomy.levels.mid')).toBeInTheDocument()
    expect(screen.getByText('dashboard.orgs.paused')).toBeInTheDocument()
    expect(screen.getByTitle('monomind down')).toBeInTheDocument()
    fireEvent.click(screen.getByText('acme'))
    expect(onNavigate).toHaveBeenCalledWith('orgs', { org: 'acme' })
  })
  it('empty and more', () => {
    const { rerender } = render(<OrgsCard onNavigate={vi.fn()} orgs={{ orgs: [], totals: { orgs: 0 } }} />)
    expect(screen.getByText('dashboard.orgs.empty')).toBeInTheDocument()
    const many = Array.from({ length: 8 }, (_, i) => ({ name: `o${i}`, level: 'manual', queued: 0, needs_you: 0 }))
    rerender(<OrgsCard onNavigate={vi.fn()} orgs={{ orgs: many, totals: { orgs: 8 } }} />)
    expect(screen.getByText('dashboard.orgs.more:2')).toBeInTheDocument()
  })
})
