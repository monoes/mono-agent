// @vitest-environment jsdom
// Dashboard deep links: Settings scrolls to a section, Connections opens an
// automation's drawer on the tab the link names.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: k => k, i18n: { resolvedLanguage: 'en', language: 'en', changeLanguage: vi.fn() } }) }))
vi.mock('../services/api.js', () => ({
  api: {
    getDBPath: () => Promise.resolve('/db'), isDBConnected: () => Promise.resolve(true),
    listConnections: () => Promise.resolve([]), listPlatforms: () => Promise.resolve([]),
    listAutomations: () => Promise.resolve({ automations: [{ id: 'linkedin', name: 'LinkedIn' }] }),
  },
}))
vi.mock('../wailsjs/go/main/App', () => ({
  GetVersion: () => Promise.resolve({ version: '1' }), CheckForUpdate: () => Promise.resolve({}), AppSelfUpdate: vi.fn(),
}))
vi.mock('../components/settings/HealthSection.jsx', () => ({ default: () => <div>health-section</div> }))
vi.mock('../components/settings/JevSection.jsx', () => ({ default: () => <div>jev-section</div> }))
vi.mock('./connections/BrowserAutomations.jsx', () => ({ default: () => <div>automations</div>, RecordHelpDialog: () => null }))
vi.mock('./connections/ApiConnections.jsx', () => ({ default: () => null, resolveConn: () => null }))
vi.mock('./connections/ApiConnectionModal.jsx', () => ({ default: () => null }))
vi.mock('./connections/ImportDialog.jsx', () => ({ default: () => null }))
vi.mock('./connections/AutomationDrawer.jsx', () => ({
  default: ({ automation, initialTab }) => <div>drawer:{automation.id}:{initialTab}</div>,
}))
import Settings from './Settings.jsx'
import Connections from './Connections.jsx'

let scrolled
beforeEach(() => {
  scrolled = []
  Element.prototype.scrollIntoView = function () { scrolled.push(this.dataset.section) }
})
afterEach(cleanup)

describe('dashboard deep links', () => {
  it('Settings scrolls to the section navData names', async () => {
    render(<Settings onNavigate={vi.fn()} navData={{ section: 'jev' }} />)
    await waitFor(() => expect(scrolled).toEqual(['jev']))
  })
  it('Settings without navData does not scroll', () => {
    render(<Settings onNavigate={vi.fn()} navData={null} />)
    expect(scrolled).toEqual([])
  })
  it('Connections opens the named automation on the named tab', async () => {
    render(<Connections navData={{ automationId: 'linkedin', tab: 'health' }} />)
    expect(await screen.findByText('drawer:linkedin:Health')).toBeInTheDocument()
  })
  it('Connections ignores an unknown automation', async () => {
    render(<Connections navData={{ automationId: 'nope', tab: 'health' }} />)
    await screen.findByText('automations')
    expect(screen.queryByText(/drawer:/)).toBeNull()
  })
})
