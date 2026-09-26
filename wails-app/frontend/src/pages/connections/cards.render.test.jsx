// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'

const api = vi.hoisted(() => ({
  showAutomation: vi.fn(),
  uninstallAutomation: vi.fn(),
  restoreAutomation: vi.fn(),
  setAutomationTrust: vi.fn(),
  getSessions: vi.fn(() => Promise.resolve([])),
  doctorAutomations: vi.fn(() => Promise.resolve({ automations: [] })),
  listRecordings: vi.fn(() => Promise.resolve({ recordings: [] })),
  testAutomation: vi.fn(),
  chooseAutomationExportPath: vi.fn(),
  openURL: vi.fn(),
}))
vi.mock('../../services/api.js', () => ({
  api,
  notify: vi.fn(),
  onConnectionProgress: () => () => {},
  onConnectionDone: () => () => {},
  onConnectionOpened: () => () => {},
}))

import ConfirmHost from '../../components/ConfirmDialog.jsx'
import AutomationCard from './AutomationCard.jsx'
import BrowserAutomations from './BrowserAutomations.jsx'
import AutomationDrawer from './AutomationDrawer.jsx'
import { isRisky } from './ui.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

const base = { id: 'acme', name: 'Acme CRM', version: '1.2.0', source: 'imported', trust: 'imported', enabled: true, available: true, actions: 2, session: { loggedIn: false, username: '', status: 'logged_out' } }

describe('isRisky', () => {
  it('treats only none and read as safe; missing is dangerous', () => {
    expect(isRisky('none')).toBe(false)
    expect(isRisky('read')).toBe(false)
    for (const e of ['write', 'message', 'destructive', '', undefined, 'weird']) expect(isRisky(e)).toBe(true)
  })
})

describe('AutomationCard', () => {
  it('shows the badges the CLI reports', () => {
    render(<AutomationCard automation={{ ...base, modified: true, containsScripts: true, pendingUpdate: '1.3.0' }} onOpen={() => {}} />)
    expect(screen.getByText('imported 1.2.0')).toBeInTheDocument()
    expect(screen.getByText('modified')).toBeInTheDocument()
    expect(screen.getByText('contains scripts')).toBeInTheDocument()
    expect(screen.getByText('pending update')).toBeInTheDocument()
  })

  it('reads session expiry from session.status, not from a leftover username', () => {
    const { rerender } = render(<AutomationCard automation={{ ...base, session: { loggedIn: false, username: 'jane', status: 'logged_out' } }} onOpen={() => {}} />)
    expect(screen.getByText('logged out')).toBeInTheDocument()
    rerender(<AutomationCard automation={{ ...base, session: { loggedIn: false, username: 'jane', status: 'expired' } }} onOpen={() => {}} />)
    expect(screen.getByText('session expired')).toBeInTheDocument()
  })

  it('marks an unavailable package with its reason', () => {
    render(<AutomationCard automation={{ ...base, available: false, unavailableReason: 'needs -tags social' }} onOpen={() => {}} />)
    expect(screen.getByTitle('needs -tags social')).toHaveTextContent('unavailable')
  })
})

describe('BrowserAutomations', () => {
  it('lists removed built-ins greyed after the rest and opens them', () => {
    const onOpen = vi.fn()
    render(<BrowserAutomations automations={[base, { ...base, id: 'gemini', name: 'Gemini', source: 'builtin', removed: true }]} onOpen={onOpen} onRecord={() => {}} onImport={() => {}} />)
    expect(screen.getByText('0 / 1 logged in')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Gemini, uninstalled'))
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'gemini', removed: true }))
  })
})

const shown = {
  info: { ...base, scriptsAllowed: false, liveRunConfirmed: false },
  manifest: { description: 'Acme via the web UI', site: { domains: ['app.acme.com'] }, permissions: { steps: ['click'], scripts: [] } },
  actions: [{ name: 'create_contact', sideEffects: 'write', nodeType: 'acme.create_contact', inputs: [], outputs: [] }],
  fragments: [],
  issues: [],
}

describe('AutomationDrawer', () => {
  it('renders tabs with arrow-key navigation and aria-controls', async () => {
    api.showAutomation.mockResolvedValue(shown)
    render(<AutomationDrawer automation={base} onClose={() => {}} />)
    await screen.findByText('Acme via the web UI')
    const overview = screen.getByRole('tab', { name: 'Overview' })
    expect(overview).toHaveAttribute('aria-controls', 'automation-tabpanel')
    fireEvent.keyDown(overview, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: 'Session' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Session' }), { key: 'End' })
    expect(screen.getByRole('tab', { name: 'Recordings' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(screen.getByRole('tab', { name: 'Actions' }))
    expect(await screen.findByText('create_contact')).toBeInTheDocument()
  })

  it('Escape closes the drawer, but Escape in a confirm on top only closes the confirm', async () => {
    api.showAutomation.mockResolvedValue(shown)
    const onClose = vi.fn()
    render(<><ConfirmHost /><AutomationDrawer automation={base} onClose={onClose} /></>)
    await screen.findByText('Acme via the web UI')
    fireEvent.click(screen.getByText('Uninstall'))
    const cancel = await screen.findByText('Cancel')
    await act(async () => { fireEvent.keyDown(cancel, { key: 'Escape' }); fireEvent.keyDown(window, { key: 'Escape' }) })
    await waitFor(() => expect(screen.queryByText('Cancel')).not.toBeInTheDocument())
    expect(onClose).not.toHaveBeenCalled()
    expect(api.uninstallAutomation).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('offers only Restore for an uninstalled built-in and never calls show', async () => {
    api.restoreAutomation.mockResolvedValue({ ok: true })
    const onChanged = vi.fn()
    render(<AutomationDrawer automation={{ ...base, source: 'builtin', removed: true }} onClose={() => {}} onChanged={onChanged} />)
    expect(screen.getByText(/This built-in is uninstalled/)).toBeInTheDocument()
    expect(screen.getAllByRole('tab')).toHaveLength(1)
    expect(screen.queryByText('Uninstall')).not.toBeInTheDocument()
    fireEvent.click(screen.getByText('Restore'))
    await waitFor(() => expect(api.restoreAutomation).toHaveBeenCalledWith('acme'))
    expect(api.showAutomation).not.toHaveBeenCalled()
  })

  it('toggles "Allow scripts" through automation trust after a confirmation', async () => {
    api.showAutomation.mockResolvedValue(shown)
    api.setAutomationTrust.mockResolvedValue({ ok: true })
    render(<><ConfirmHost /><AutomationDrawer automation={base} onClose={() => {}} /></>)
    fireEvent.click(await screen.findByLabelText('Allow scripts'))
    fireEvent.click(await screen.findByText('Confirm'))
    await waitFor(() => expect(api.setAutomationTrust).toHaveBeenCalledWith('acme', 'scripts'))
  })

  it('confirms Run live when sideEffects is missing', async () => {
    api.showAutomation.mockResolvedValue({ ...shown, actions: [{ name: 'mystery', nodeType: 'acme.mystery', inputs: [], outputs: [] }] })
    api.testAutomation.mockResolvedValue({ results: [] })
    render(<><ConfirmHost /><AutomationDrawer automation={base} onClose={() => {}} /></>)
    await screen.findByText('Acme via the web UI')
    fireEvent.click(screen.getByRole('tab', { name: 'Actions' }))
    fireEvent.click(await screen.findByText('Run live'))
    expect(await screen.findByText(/declares no side-effect level/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Cancel'))
    await waitFor(() => expect(screen.queryByText(/declares no side-effect level/)).not.toBeInTheDocument())
    expect(api.testAutomation).not.toHaveBeenCalled()
  })
})
