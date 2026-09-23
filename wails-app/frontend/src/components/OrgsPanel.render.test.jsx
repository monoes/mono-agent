// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, waitFor, cleanup, act, fireEvent } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import OrgsPanel from './OrgsPanel.jsx'
import { api } from '../services/api.js'
// The real i18n setup, as main.jsx loads it, so the panel's tab labels come
// from src/locales/*.json and the English names below stay findable.
import i18n from '../i18n.js'
import en from '../locales/en.json'
import es from '../locales/es.json'

vi.mock('../services/api.js', () => ({
  api: {
    isReady: vi.fn(() => Promise.resolve(true)),
    isMonomindInitialized: vi.fn(() => Promise.resolve(true)),
    listOrgDesigns: vi.fn(() => Promise.resolve({
      items: [{ name: 'test-org', goal: 'a goal', status: 'active', roleCount: 1 }],
    })),
    listNeedsYou: vi.fn(() => Promise.resolve({ v: 1, items: [] })),
    getOrgQuestions: vi.fn(() => Promise.resolve({ questions: [] })),
    getOrgGates: vi.fn(() => Promise.resolve({ gates: [] })),
    getOrgApprovals: vi.fn(() => Promise.resolve({ approvals: [] })),
    getOrgAutonomy: vi.fn(() => Promise.resolve({ v: 1, org: 'test-org', level: 'manual', decider: { kind: 'model' }, daemon_running: true })),
    getOrgStatus: vi.fn(() => Promise.resolve({ status: 'stopped' })),
    listOrgQueuedMessages: vi.fn(() => Promise.resolve({ v: 1, org: 'test-org', count: 0, messages: [] })),
    getOrgReport: vi.fn(() => Promise.resolve({ items: [] })),
    streamOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
    stopOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
    orgGroupStatus: vi.fn(() => Promise.resolve({ v: 1, holding: 'hq', children: [], rollup_usd: 0 })),
    listOrgDecisionLog: vi.fn(() => Promise.resolve({ v: 1, decisions: [] })),
  },
  onOrgEvent: vi.fn(() => () => {}),
  onOrgEventsClosed: vi.fn(() => () => {}),
  onOrgRunStatus: vi.fn(() => () => {}),
  onOrgDesignUpdated: vi.fn(() => () => {}),
  notify: vi.fn(),
}))

beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
})

afterEach(() => {
  cleanup()
})

// Regression test for the exact bug reported live: opening the Orgs page
// shows a spinner that never clears, even though the org list actually
// loaded successfully underneath. Root cause: OrgsPanel's first-activation
// effect tracks "is this the first load" with a plain ref (firstLoadRef)
// that it flips to false as soon as the effect body runs -- but the ref
// persists across React StrictMode's dev-only mount->cleanup->remount
// double-invoke of every effect on initial mount, while the *cancellation*
// of the first invocation's in-flight isReady-polling correctly stops it
// from ever reaching its loadOrgs(false) call. The second (surviving)
// invocation then sees firstLoadRef.current already false and takes the
// "silent re-activation" branch (loadOrgs(true)), which populates the org
// list but deliberately never touches the loadingOrgs state -- so the
// spinner shown from the initial useState(true) is never cleared, even
// though `orgs` itself is correctly populated underneath it.
//
// This only reproduces under React.StrictMode (development), which is
// exactly the `wails dev` environment the bug was reported in -- a
// production build never double-invokes effects and would not show this.
it('clears the loading spinner and shows the org list after StrictMode double-invokes the first-load effect', async () => {
  render(
    <React.StrictMode>
      <OrgsPanel />
    </React.StrictMode>,
  )

  await waitFor(() => {
    expect(screen.getByText('test-org')).toBeInTheDocument()
  })
  expect(screen.queryByText('No orgs found.')).not.toBeInTheDocument()
})

// Guards the other half of this effect's contract, which the fix above
// must not regress: a genuine re-activation (navigating away from Orgs and
// back, without the panel ever unmounting -- this app keeps visited pages
// mounted and just toggles pageActive) should silently refresh in the
// background, never flashing the spinner a second time.
it('does not re-show the spinner on a later re-activation after the first load already completed', async () => {
  const { rerender } = render(
    <React.StrictMode>
      <OrgsPanel pageActive={true} />
    </React.StrictMode>,
  )
  await waitFor(() => {
    expect(screen.getByText('test-org')).toBeInTheDocument()
  })

  await act(async () => {
    rerender(
      <React.StrictMode>
        <OrgsPanel pageActive={false} />
      </React.StrictMode>,
    )
  })
  await act(async () => {
    rerender(
      <React.StrictMode>
        <OrgsPanel pageActive={true} />
      </React.StrictMode>,
    )
  })

  // Still showing the org list immediately, never a spinner flash.
  expect(screen.getByText('test-org')).toBeInTheDocument()
})

describe('org unification in OrgsPanel', () => {
  it('shows a Needs you badge on the org in the list', async () => {
    api.listNeedsYou.mockResolvedValue({ v: 1, items: [{ kind: 'gate', ref: 'g1' }, { kind: 'question', ref: 'q1' }] })
    render(<OrgsPanel />)
    await waitFor(() => expect(screen.getByLabelText('2 waiting')).toBeInTheDocument())
  })

  it('opens an org with the autonomy header, a Needs you tab, and no auto-approve toggle (C-42)', async () => {
    api.listNeedsYou.mockResolvedValue({ v: 1, items: [] })
    render(<OrgsPanel />)
    fireEvent.click(await screen.findByText('test-org'))
    expect(await screen.findByRole('radiogroup', { name: 'Autonomy level' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Needs you/ }))
    expect(await screen.findByText('Nothing needs you.')).toBeInTheDocument()
    expect(screen.queryByText(/Auto-approve/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Group$/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Trace/ })).toBeInTheDocument()
  })

  it('counts queued messages on the Queued tab, and nothing when the queue is empty (C-35)', async () => {
    api.listOrgQueuedMessages.mockResolvedValue({ v: 1, org: 'test-org', count: 3, messages: [{ messageId: 'a' }, { messageId: 'b' }, { messageId: 'c' }] })
    render(<OrgsPanel />)
    fireEvent.click(await screen.findByText('test-org'))
    expect(await screen.findByRole('button', { name: /^Queued\s*3$/ })).toBeInTheDocument()
    expect(api.listOrgQueuedMessages).toHaveBeenCalledWith('test-org')
  })

  it('shows no count on the Queued tab when nothing waits', async () => {
    render(<OrgsPanel />)
    fireEvent.click(await screen.findByText('test-org'))
    expect(await screen.findByRole('button', { name: /^Queued$/ })).toBeInTheDocument()
  })

  it('offers the Group tab only for holding orgs', async () => {
    api.listOrgDesigns.mockResolvedValue({ items: [{ name: 'hq', status: 'active', roleCount: 1, kind: 'holding' }] })
    render(<OrgsPanel />)
    fireEvent.click(await screen.findByText('hq'))
    fireEvent.click(await screen.findByRole('button', { name: /Group/ }))
    expect(await screen.findByText('This holding org has no child orgs yet.')).toBeInTheDocument()
    expect(api.orgGroupStatus).toHaveBeenCalledWith('hq')
  })

  it('has every tab-bar string in both locales', () => {
    const tabs = ['design', 'overview', 'group', 'needs', 'queued', 'decisions', 'logs', 'costs', 'flow', 'trace']
    const bar = ['running', 'run', 'runTitle', 'fullscreen', 'exitFullscreen']
    for (const k of tabs) {
      expect(en.orgs.tabs[k], `en tabs.${k}`).toBeTruthy()
      expect(es.orgs.tabs[k], `es tabs.${k}`).toBeTruthy()
    }
    for (const k of bar) {
      expect(en.orgs.tabBar[k], `en tabBar.${k}`).toBeTruthy()
      expect(es.orgs.tabBar[k], `es tabBar.${k}`).toBeTruthy()
    }
  })

  it('labels the tabs and the Run button from the locale files', async () => {
    api.listOrgDesigns.mockResolvedValue({ items: [{ name: 'test-org', status: 'active', roleCount: 1 }] })
    api.listOrgQueuedMessages.mockResolvedValue({ v: 1, org: 'test-org', count: 0, messages: [] })
    await act(() => i18n.changeLanguage('es'))
    render(<OrgsPanel />)
    fireEvent.click(await screen.findByText('test-org'))
    for (const k of ['design', 'overview', 'needs', 'queued', 'decisions', 'logs', 'costs', 'flow', 'trace']) {
      expect(await screen.findByRole('button', { name: es.orgs.tabs[k] })).toBeInTheDocument()
    }
    expect(screen.getByRole('button', { name: es.orgs.tabBar.run })).toHaveAttribute('title', es.orgs.tabBar.runTitle)
    expect(screen.queryByRole('button', { name: /^Needs you$/ })).not.toBeInTheDocument()
  })

  it('has no auto-resolve code left in the panel source (C-42)', () => {
    const src = readFileSync(join(process.cwd(), 'src/components/OrgsPanel.jsx'), 'utf8')
    expect(src).not.toMatch(/autoApprove|Auto-approve|resolveAllPending|proceed autonomously/)
  })
})
