// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.count != null ? `${k}:${o.count}` : k) }) }))
vi.mock('../wailsjs/go/main/App', () => ({ GetVersion: () => Promise.resolve({ version: 'v1.2.3' }) }))
vi.mock('../services/api.js', () => ({ api: {}, PLATFORM_COLORS: {}, subscribeEvent: () => () => {} }))
vi.mock('../lib/health.js', () => ({
  getHealth: () => ({ report: { results: [] } }),
  subscribeHealth: () => () => {},
  summarize: () => ({ level: 'ok', issues: 0 }),
  runHealth: vi.fn(),
}))
vi.mock('./dashboard/useDashboardData.js', () => ({
  useDashboardData: () => ({
    loading: false,
    summary: {
      workflows: { total: 2, active: 1 },
      executions: { running: 0, queued: 0, waiting: 0, last_24h: { total: 3, failed: 0 }, recent: [] },
      schedules: { daemon_running: true, upcoming: [], invalid: [] },
      hil: { workflow_pending: 2, people_review: 0, drafts: 0, link_suggestions: 0, total: 2 },
      people: { total: 5, added_7d: 1, lists: 0 },
      services: { daemon: { running: true }, bridge: null, org_serve: { running: false, orgs: [] } },
      accounts: { sessions: [], active: 0, expired: 0, expiring_soon: 0 },
    },
    orgs: { orgs: [], totals: { orgs: 0, running: 0, needs_you: 0 } },
    workflows: [{ id: 'w1', name: 'Scraper', is_active: true }],
    executions: [],
    refresh: vi.fn(), setExecutions: vi.fn(), reloadLists: vi.fn(),
  }),
}))
import Dashboard from './Dashboard.jsx'

afterEach(cleanup)

describe('Dashboard', () => {
  it('composes every card and opens the HIL drawer from the strip', () => {
    const onOpenHil = vi.fn()
    render(<Dashboard onNavigate={vi.fn()} onOpenHil={onOpenHil} onRefresh={vi.fn()} />)
    for (const k of ['dashboard.stat.workflows', 'dashboard.stat.running', 'dashboard.stat.failed24h', 'dashboard.stat.orgs',
      'dashboard.stat.people', 'dashboard.workflowsSection.title', 'dashboard.recentRuns.title', 'dashboard.activity.title',
      'dashboard.system.title', 'dashboard.orgs.title', 'dashboard.automations.title', 'dashboard.accounts.title']) {
      expect(screen.getByText(k)).toBeInTheDocument()
    }
    expect(screen.getByText('Scraper')).toBeInTheDocument()
    fireEvent.click(screen.getByText('dashboard.attention.hilApprovals:2'))
    expect(onOpenHil).toHaveBeenCalledTimes(1)
  })
})
