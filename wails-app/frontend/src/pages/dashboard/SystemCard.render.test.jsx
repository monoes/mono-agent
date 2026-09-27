// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o ? `${k}:${JSON.stringify(o)}` : k) }) }))
const runHealth = vi.fn()
vi.mock('../../lib/health.js', () => ({
  getHealth: () => ({ report: { results: [] }, loading: false }),
  subscribeHealth: () => () => {},
  summarize: () => ({ level: 'issues', issues: 2 }),
  runHealth: (...a) => runHealth(...a),
}))
const events = {}
vi.mock('../../services/api.js', () => ({ subscribeEvent: (n, fn) => { events[n] = fn; return () => {} } }))
import { act } from '@testing-library/react'
import SystemCard from './SystemCard.jsx'
import StatRow from './StatRow.jsx'

afterEach(cleanup)
const summary = {
  services: { daemon: { running: true, pid: 1 }, bridge: { status: 'waiting', connected: false }, org_serve: { running: true, orgs: ['acme', 'ops'] } },
  jev: { key_configured: true, calls_24h: 12, estimated_usd_24h: 0.0312, surfaces_enabled: 3 },
}

describe('SystemCard', () => {
  it('renders service states, health and Jev usage', () => {
    render(<SystemCard summary={summary} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.system.daemon')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.bridgeState.waiting')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.orgServeRunning:{"count":2}')).toBeInTheDocument()
    expect(screen.getByText('dashboard.system.healthIssues:{"count":2}')).toBeInTheDocument()
    expect(screen.getByText(/\$0\.03/)).toBeInTheDocument()
  })
  it('run check calls runHealth in background mode; rows navigate', () => {
    const onNavigate = vi.fn()
    render(<SystemCard summary={summary} onNavigate={onNavigate} />)
    fireEvent.click(screen.getByText('dashboard.system.runCheck'))
    expect(runHealth).toHaveBeenCalledWith({ background: true })
    fireEvent.click(screen.getByText('dashboard.system.jev'))
    expect(onNavigate).toHaveBeenCalledWith('settings', { section: 'jev' })
  })
  it('shows an announced update and links to Settings', () => {
    const onNavigate = vi.fn()
    render(<SystemCard summary={summary} onNavigate={onNavigate} />)
    expect(screen.queryByText('dashboard.system.update')).toBeNull()
    act(() => events['update:available']({ update_available: true, latest_version: 'v9.9.9', release_url: 'https://x' }))
    fireEvent.click(screen.getByText('dashboard.system.update'))
    expect(onNavigate).toHaveBeenCalledWith('settings', { section: 'version' })
  })
  it('bridge null shows not running', () => {
    render(<SystemCard summary={{ ...summary, services: { ...summary.services, bridge: null } }} onNavigate={vi.fn()} />)
    expect(screen.getByText('dashboard.system.bridgeState.off')).toBeInTheDocument()
  })
})

describe('StatRow', () => {
  it('shows counts with sub-lines and navigates', () => {
    const onNavigate = vi.fn()
    render(<StatRow loading={false} onNavigate={onNavigate}
      summary={{ workflows: { total: 12, active: 7 }, executions: { running: 1, queued: 2, waiting: 1, last_24h: { failed: 3, total: 40 } }, people: { total: 1204, added_7d: 18 } }}
      orgs={{ totals: { orgs: 5, running: 2 } }} />)
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(screen.getByText('dashboard.stat.queuedCount:{"count":3}')).toBeInTheDocument()
    expect(screen.getByText('dashboard.stat.ofRuns:{"count":40}')).toBeInTheDocument()
    expect(screen.getByText('dashboard.stat.ofOrgs:{"count":5}')).toBeInTheDocument()
    fireEvent.click(screen.getByText('dashboard.stat.people'))
    expect(onNavigate).toHaveBeenCalledWith('people')
  })
})
