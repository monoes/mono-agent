// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.count != null ? `${k}:${o.count}` : o?.when ? `${k}:${o.when}` : k) }) }))
vi.mock('../../services/api.js', () => ({ PLATFORM_COLORS: {} }))
import AutomationsCard from './AutomationsCard.jsx'
import AccountsCard from './AccountsCard.jsx'

afterEach(cleanup)

describe('AutomationsCard', () => {
  const summary = {
    automations: { installed: 9, enabled: 8, unavailable: 0, pending_update: 1, scripts_blocked: 0,
      selectors: { ok: 10, decaying: 2, broken: 1, stale: 0 }, broken: [{ automation_id: 'linkedin', selector_key: 'post.send' }] },
    recordings: { total: 3, unsaved: 1, incomplete: 0 },
  }
  it('shows packages, selector health and deep-links a broken selector', () => {
    const onNavigate = vi.fn()
    render(<AutomationsCard summary={summary} onNavigate={onNavigate} />)
    expect(screen.getByText('dashboard.automations.pendingUpdate:1')).toBeInTheDocument()
    expect(screen.queryByText(/scriptsBlocked/)).toBeNull()
    expect(screen.queryByText(/dashboard.automations.unavailable/)).toBeNull()
    fireEvent.click(screen.getByText('linkedin › post.send'))
    expect(onNavigate).toHaveBeenCalledWith('connections', { automationId: 'linkedin', tab: 'health' })
    fireEvent.click(screen.getByText('dashboard.automations.recordings'))
    expect(onNavigate).toHaveBeenCalledWith('connections', { tab: 'recordings' })
  })
})

describe('AccountsCard', () => {
  it('shows expiring and expired logins', () => {
    render(<AccountsCard onNavigate={vi.fn()} summary={{ accounts: { sessions: [
      { platform: 'x', username: 'me', status: 'expiring', expiry: new Date(Date.now() + 20 * 3600e3).toISOString() },
      { platform: 'tiktok', username: 'me2', status: 'expired', expiry: '2026-09-01T00:00:00Z' },
    ] } }} />)
    expect(screen.getByText(/dashboard.accounts.expiresIn/)).toBeInTheDocument()
    expect(screen.getByText('dashboard.accounts.expired')).toBeInTheDocument()
  })
  it('empty', () => {
    render(<AccountsCard onNavigate={vi.fn()} summary={{ accounts: { sessions: [] } }} />)
    expect(screen.getByText('dashboard.accounts.empty')).toBeInTheDocument()
  })
})
