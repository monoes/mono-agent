// @vitest-environment jsdom
// Round-3 GUI fixes: Health table layout, replace label and CLI hint filter,
// trust-reset note, no "unavailable" on removed packages, outputs by key.
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const api = vi.hoisted(() => ({
  doctorAutomations: vi.fn(),
  rerecordSelector: vi.fn(),
  installAutomationDryRun: vi.fn(),
  installAutomation: vi.fn(),
  chooseAutomationPackage: vi.fn(),
  showAutomation: vi.fn(),
  testAutomation: vi.fn(),
  chooseAutomationExportPath: vi.fn(),
  getSessions: vi.fn(() => Promise.resolve([])),
  listRecordings: vi.fn(() => Promise.resolve({ recordings: [] })),
  openURL: vi.fn(),
}))
vi.mock('../../services/api.js', () => ({
  api, notify: vi.fn(),
  onConnectionProgress: () => () => {}, onConnectionDone: () => () => {}, onConnectionOpened: () => () => {},
}))

import HealthTab from './HealthTab.jsx'
import ImportDialog from './ImportDialog.jsx'
import AutomationDrawer from './AutomationDrawer.jsx'
import ActionsTab from './ActionsTab.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('HealthTab layout', () => {
  it('uses a fixed five-column table with no horizontal scroll and wrapping cells', async () => {
    api.doctorAutomations.mockResolvedValue({ automations: [{ id: 'a', issues: [], selectors: [
      { key: 'a.very.long.selector.key.that.must.wrap.inside.the.drawer', ok: 2, fail: 5, healed: 1, status: 'broken', lastOk: '2026-09-24T10:00:00Z', lastFail: '2026-09-25T10:00:00Z' },
      { key: 'old.key', ok: 1, fail: 0, status: 'stale', stale: true },
    ] }] })
    const { container } = render(<div style={{ width: 620 }}><HealthTab automationId="a" /></div>)
    await screen.findByText('old.key')
    const table = container.querySelector('table')
    expect(table.style.tableLayout).toBe('fixed')
    expect(container.querySelectorAll('thead th')).toHaveLength(5)
    expect(table.parentElement.style.overflowX).toBe('')
    expect(screen.getByText('1 healed')).toBeInTheDocument()
    expect(screen.getByText('no longer in this package').style.whiteSpace).toBe('')
    expect(screen.getByText(/a\.very\.long/).style.wordBreak).toBe('break-all')
  })
})

const replaceDry = {
  id: 'gemini', name: 'Gemini (fork)', version: '9.0.0', previousVersion: '1.0.0', sha256: 'f0',
  review: { replaces: { id: 'gemini', source: 'builtin', trust: 'builtin', version: '1.0.0' }, domains: ['x.example.com'], steps: [], scripts: [], actionEffects: {}, files: [], capabilities: [] },
  warnings: ['REPLACES the installed BUILTIN package "gemini" 1.0.0 with imported content', 'replacing it requires confirmation (--replace-builtin on the command line)'],
  issues: [],
}

async function openReview(dry, installed = []) {
  api.chooseAutomationPackage.mockResolvedValue('/tmp/p.mpkg')
  api.installAutomationDryRun.mockResolvedValue(dry)
  render(<ImportDialog installedPackages={installed} onClose={() => {}} />)
  fireEvent.click(screen.getByText('Browse'))
  await screen.findByText(/sha256/)
}

describe('ImportDialog round 3', () => {
  it('labels a built-in replacement and drops the CLI-only flag hint, keeping other warnings', async () => {
    await openReview(replaceDry)
    expect(screen.getByText('Replace built-in gemini')).toBeInTheDocument()
    expect(screen.queryByText(/--replace-builtin on the command line/)).not.toBeInTheDocument()
    expect(screen.getByText(/REPLACES the installed BUILTIN package/)).toBeInTheDocument()
  })

  it('says "Replace local" for a local package', async () => {
    await openReview({ ...replaceDry, review: { ...replaceDry.review, replaces: { id: 'gemini', source: 'local', trust: 'local', version: '1.0.0' } } })
    expect(screen.getByText('Replace local gemini')).toBeInTheDocument()
  })

  it('warns that an update resets the trust opt-ins when either is on', async () => {
    const update = { id: 'books', name: 'Books', version: '0.2.0', previousVersion: '0.1.0', sha256: 'ab', review: { domains: [], steps: [], scripts: [], actionEffects: {}, files: [], capabilities: [], replaces: { id: 'books', source: 'imported', trust: 'imported', version: '0.1.0' } }, issues: [] }
    await openReview(update, [{ id: 'books', trust: 'imported', scriptsAllowed: true, liveRunConfirmed: false }])
    expect(screen.getByText(/Updating resets 'Allow scripts' and 'Allow live runs'/)).toBeInTheDocument()
    expect(screen.getByText('Update to 0.2.0')).toBeInTheDocument()
    cleanup()
    await openReview(update, [{ id: 'books', trust: 'imported', scriptsAllowed: false, liveRunConfirmed: false }])
    expect(screen.queryByText(/Updating resets/)).not.toBeInTheDocument()
  })
})

describe('AutomationDrawer removed', () => {
  it('shows no "unavailable" chip for an uninstalled built-in', () => {
    render(<AutomationDrawer automation={{ id: 'hn', name: 'HN', source: 'builtin', version: '1.1.0', removed: true, available: false }} onClose={() => {}} />)
    expect(screen.getByText(/This built-in is uninstalled/)).toBeInTheDocument()
    expect(screen.queryByText('unavailable')).not.toBeInTheDocument()
  })
})

describe('ActionsTab outputs', () => {
  it('shows success outputs as chips and the other keys apart', () => {
    render(<ActionsTab automationId="b" actions={[{ name: 'list_items', sideEffects: 'read', nodeType: 'b.list_items', inputs: [], outputs: ['items', 'error'], outputsByKey: { success: ['items'], error: ['error'] } }]} />)
    expect(screen.getByText('items')).toBeInTheDocument()
    expect(screen.getByText('on error: error')).toBeInTheDocument()
    expect(screen.queryByText('error', { selector: 'span[title]' })).not.toBeInTheDocument()
  })

  it('falls back to the flat list when outputsByKey is absent', () => {
    render(<ActionsTab automationId="b" actions={[{ name: 'x', sideEffects: 'read', nodeType: 'b.x', inputs: [], outputs: ['a', 'b'] }]} />)
    expect(screen.getByText('a')).toBeInTheDocument()
    expect(screen.getByText('b')).toBeInTheDocument()
  })
})
