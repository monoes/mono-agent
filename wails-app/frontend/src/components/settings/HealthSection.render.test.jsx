// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const mockRunHealthCheck = vi.fn()
const mockRunHealthFix = vi.fn()
const listeners = {}

vi.mock('../../wailsjs/go/main/App', () => ({
  RunHealthCheck: (...a) => mockRunHealthCheck(...a),
  RunHealthFix: (...a) => mockRunHealthFix(...a),
  GetVersion: () => Promise.resolve({ version: 'dev' }),
}))
vi.mock('../../services/api.js', () => ({
  subscribeEvent: (name, cb) => { listeners[name] = cb; return () => { delete listeners[name] } },
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key, o) => (o && o.n !== undefined ? `${key}:${o.n}` : key) }),
}))
const mockConfirm = vi.fn()
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))

const report = {
  v: 1, profile_id: 'default', monoagent_version: 'dev',
  results: [
    { id: 'core.db', group: 'core', title: 'Database', status: 'fail', required: true, summary: 'no database yet',
      fix: { id: 'core.db.migrate', label: 'Create / migrate database', safety: 'auto' } },
    { id: 'monomind.node', group: 'monomind', title: 'Node.js', status: 'fail', summary: 'not found',
      fix: { id: 'monomind.node.install', label: 'Download Node', safety: 'confirm', command: 'monoagentcli nodejs install' } },
    { id: 'runtimes.agents', group: 'runtimes', title: 'AI agent runtimes', status: 'ok', summary: '1 of 2' },
    { id: 'runtimes.codex', group: 'runtimes', parent: 'runtimes.agents', title: 'codex', status: 'info', summary: 'not installed',
      fix: { id: 'runtimes.install:codex', label: 'Install codex', safety: 'confirm', optional: true } },
  ],
}

let HealthSection
beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  mockRunHealthCheck.mockResolvedValue(JSON.stringify(report))
  HealthSection = (await import('./HealthSection.jsx')).default
})
afterEach(cleanup)

describe('HealthSection', () => {
  it('renders the CLI report: banner, groups, nested runtime rows', async () => {
    render(<HealthSection />)
    await screen.findByText('settings.health.broken:1')
    expect(mockRunHealthCheck).toHaveBeenCalledWith(false, false)
    expect(screen.getByText('Database')).toBeInTheDocument()
    // runtime children are collapsed while the parent is ok
    expect(screen.queryByText('codex')).not.toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('settings.health.showItems:1'))
    expect(screen.getByText('codex')).toBeInTheDocument()
    // "Fix issues" counts only non-optional auto/confirm fixes
    expect(screen.getByText('settings.health.fixIssues:2')).toBeInTheDocument()
  })

  it('runs an auto fix directly and re-checks when it is done', async () => {
    mockRunHealthFix.mockResolvedValue('{"ok":true}')
    render(<HealthSection />)
    fireEvent.click(await screen.findByText('Create / migrate database'))
    await waitFor(() => expect(mockRunHealthFix).toHaveBeenCalledWith('core.db.migrate'))
    expect(mockConfirm).not.toHaveBeenCalled()
    listeners['health:fixProgress']({ fix_id: 'core.db.migrate', kind: 'line', message: 'applying migrations' })
    listeners['health:fixProgress']({ fix_id: 'core.db.migrate', kind: 'done' })
    await screen.findByText('applying migrations')
    await waitFor(() => expect(mockRunHealthCheck).toHaveBeenCalledTimes(2))
  })

  it('asks before a confirm fix and does nothing when declined', async () => {
    mockConfirm.mockResolvedValue(false)
    render(<HealthSection />)
    fireEvent.click(await screen.findByText('Download Node'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled())
    expect(mockRunHealthFix).not.toHaveBeenCalled()
  })

  it('explains a missing CLI instead of showing checks', async () => {
    mockRunHealthCheck.mockResolvedValue(JSON.stringify({ error: 'monoagentcli not found', cli_missing: true }))
    render(<HealthSection />)
    await screen.findByText(/settings.health.cliMissingTitle/)
    expect(screen.getByText('monoagentcli not found')).toBeInTheDocument()
  })
})
