// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'

const mockRunHealthCheck = vi.fn()
const mockRunHealthFix = vi.fn()
const mockCancel = vi.fn()
const listeners = {}

vi.mock('../../wailsjs/go/main/App', () => ({
  RunHealthCheck: (...a) => mockRunHealthCheck(...a),
  RunHealthFix: (...a) => mockRunHealthFix(...a),
  CancelHealthRun: (...a) => mockCancel(...a),
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
    { id: 'monomind.binary', group: 'monomind', title: 'monomind', status: 'ok', summary: '/bin/monomind',
      actions: [{ id: 'monomind.node.update', label: 'Update managed Node.js', safety: 'confirm', optional: true, command: 'monoagentcli nodejs update' }] },
    { id: 'runtimes.codex', group: 'runtimes', parent: 'runtimes.agents', title: 'codex', status: 'info', summary: 'not installed',
      fix: { id: 'runtimes.install:codex', label: 'Install codex', safety: 'confirm', optional: true } },
  ],
}

let HealthSection
beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  localStorage.clear()
  mockRunHealthCheck.mockResolvedValue(JSON.stringify(report))
  HealthSection = (await import('./HealthSection.jsx')).default
})
afterEach(cleanup)

describe('HealthSection', () => {
  it('renders the CLI report: banner, groups, nested runtime rows', async () => {
    render(<HealthSection />)
    await screen.findByText('settings.health.broken:1')
    // Opening Settings runs the background check: no runtime scan (#146).
    expect(mockRunHealthCheck).toHaveBeenCalledWith('background')
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
    await screen.findByTestId('setup-steps')
    fireEvent.click(within(document.querySelector('[data-health-row="core.db"]')).getByText('Create / migrate database'))
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
    await screen.findByTestId('setup-steps')
    fireEvent.click(within(document.querySelector('[data-health-row="monomind.node"]')).getByText('Download Node'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled())
    expect(mockRunHealthFix).not.toHaveBeenCalled()
  })

  it('lists the setup steps and runs a row action after confirming', async () => {
    mockConfirm.mockResolvedValue(true)
    mockRunHealthFix.mockResolvedValue('{"ok":true}')
    render(<HealthSection />)
    const steps = await screen.findByTestId('setup-steps')
    expect(steps).toHaveTextContent('settings.health.finishSetup')
    expect(steps).toHaveTextContent('Create / migrate database')
    fireEvent.click(screen.getByText('Update managed Node.js'))
    await waitFor(() => expect(mockRunHealthFix).toHaveBeenCalledWith('monomind.node.update'))
    expect(mockConfirm).toHaveBeenCalled()
  })

  it('explains a missing CLI instead of showing checks', async () => {
    mockRunHealthCheck.mockResolvedValue(JSON.stringify({ error: 'monoagentcli not found', cli_missing: true }))
    render(<HealthSection />)
    await screen.findByText(/settings.health.cliMissingTitle/)
    expect(screen.getByText('monoagentcli not found')).toBeInTheDocument()
  })

  it('shows the command, runs the fix on yes, and does not re-check after a no', async () => {
    mockConfirm.mockResolvedValueOnce(false)
    render(<HealthSection />)
    await screen.findByTestId('setup-steps')
    const nodeRow = () => within(document.querySelector('[data-health-row="monomind.node"]'))
    fireEvent.click(nodeRow().getByText('Download Node'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
    const [body] = mockConfirm.mock.calls[0]
    render(body)
    expect(screen.getByText('monoagentcli nodejs install')).toBeInTheDocument()
    await new Promise(r => setTimeout(r, 20))
    expect(mockRunHealthCheck).toHaveBeenCalledTimes(1) // no re-check after a no

    mockConfirm.mockResolvedValueOnce(true)
    mockRunHealthFix.mockResolvedValue('{"ok":true}')
    fireEvent.click(nodeRow().getByText('Download Node'))
    await waitFor(() => expect(mockRunHealthFix).toHaveBeenCalledWith('monomind.node.install'))
  })

  it('"Fix issues" starts the daemon only after everything else', async () => {
    // The daemon row comes first, as in a real report on a fresh machine.
    const withDaemon = { ...report, results: [
      { id: 'services.daemon', group: 'services', title: 'Workflow daemon', status: 'warn', summary: 'not running',
        fix: { id: 'services.daemon.start', label: 'Start the workflow daemon', safety: 'confirm', command: 'monoagentcli daemon' } },
      ...report.results,
    ] }
    mockRunHealthCheck.mockResolvedValue(JSON.stringify(withDaemon))
    mockConfirm.mockResolvedValue(true)
    const order = []
    mockRunHealthFix.mockImplementation(async id => {
      order.push(id)
      setTimeout(() => listeners['health:fixProgress']({ fix_id: id, kind: 'done' }), 0)
      return '{"ok":true}'
    })
    render(<HealthSection />)
    fireEvent.click(await screen.findByText('settings.health.fixIssues:3'))
    await waitFor(() => expect(order).toContain('services.daemon.start'))
    expect(order.indexOf('services.daemon.start')).toBe(order.length - 1)
    expect(order.slice(0, 2).sort()).toEqual(['core.db.migrate', 'monomind.node.install'])
  })

  it('does not start a fix that is already running, and holds "Fix issues" meanwhile', async () => {
    mockRunHealthFix.mockResolvedValue('{"ok":true}')
    render(<HealthSection />)
    await screen.findByTestId('setup-steps')
    const button = within(document.querySelector('[data-health-row="core.db"]')).getByText('Create / migrate database')
    fireEvent.click(button)
    await waitFor(() => expect(mockRunHealthFix).toHaveBeenCalledTimes(1))
    fireEvent.click(button)
    await new Promise(r => setTimeout(r, 20))
    expect(mockRunHealthFix).toHaveBeenCalledTimes(1)
    expect(screen.getByText('settings.health.fixIssues:2').closest('button')).toBeDisabled()
  })

  // #146 item 5: "Fix issues" asks before every confirm fix, runs auto ones
  // without asking, and never runs optional ones.
  it('"Fix issues" asks before each confirm fix and skips optional ones', async () => {
    const withMore = { ...report, results: [
      ...report.results,
      { id: 'browser.bridge', group: 'browser', title: 'Bridge', status: 'warn', summary: 'down',
        fix: { id: 'browser.bridge.start', label: 'Start the bridge', safety: 'confirm', command: 'monoagentcli bridge' } },
      { id: 'integrations.mcp', group: 'integrations', title: 'MCP', status: 'warn', summary: 'not registered',
        fix: { id: 'integrations.mcp.add', label: 'Register MCP', safety: 'confirm', optional: true } },
    ] }
    mockRunHealthCheck.mockResolvedValue(JSON.stringify(withMore))
    // Yes to the first confirm fix, no to the second.
    mockConfirm.mockResolvedValueOnce(true).mockResolvedValueOnce(false)
    const ran = []
    mockRunHealthFix.mockImplementation(async id => {
      ran.push(id)
      setTimeout(() => listeners['health:fixProgress']({ fix_id: id, kind: 'done' }), 0)
      return '{"ok":true}'
    })
    render(<HealthSection />)
    fireEvent.click(await screen.findByText('settings.health.fixIssues:3'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByText('settings.health.fixIssues:3').closest('button')).not.toBeDisabled())
    expect(mockConfirm.mock.calls.map(c => c[1].title)).toEqual(['Download Node', 'Start the bridge'])
    expect(ran).toEqual(['core.db.migrate', 'monomind.node.install']) // the declined and the optional one never ran
    expect(ran).not.toContain('integrations.mcp.add')
  })

  // #146 item 2: a running fix can be cancelled.
  it('cancels a running fix', async () => {
    mockRunHealthFix.mockResolvedValue('{"ok":true}')
    mockCancel.mockResolvedValue('{"ok":true,"cancelled":true}')
    render(<HealthSection />)
    await screen.findByTestId('setup-steps')
    const dbRow = () => within(document.querySelector('[data-health-row="core.db"]'))
    fireEvent.click(dbRow().getByText('Create / migrate database'))
    await waitFor(() => expect(mockRunHealthFix).toHaveBeenCalled())
    fireEvent.click(dbRow().getByLabelText('settings.health.cancel: Create / migrate database'))
    expect(mockCancel).toHaveBeenCalledWith('core.db.migrate')
    listeners['health:fixProgress']({ fix_id: 'core.db.migrate', kind: 'error', message: 'cancelled', cancelled: true })
    await screen.findByText('settings.health.fixCancelled')
    expect(screen.queryByText('cancelled')).toBeNull() // not shown as an error
  })

  it('cancels a running check and keeps the report', async () => {
    render(<HealthSection />)
    await screen.findByTestId('setup-steps')
    let finish
    mockRunHealthCheck.mockReturnValueOnce(new Promise(r => { finish = r }))
    mockCancel.mockResolvedValue('{"ok":true,"cancelled":true}')
    fireEvent.click(screen.getByText('settings.health.deepCheck'))
    fireEvent.click(await screen.findByText('settings.health.cancelCheck'))
    expect(mockCancel).toHaveBeenCalledWith('check:deep')
    finish('{"error":"cancelled","cancelled":true}')
    await waitFor(() => expect(screen.queryByText('settings.health.cancelCheck')).toBeNull())
    expect(screen.getByText('Database')).toBeInTheDocument()
  })

  it('says the runtimes are not checked in the background', async () => {
    mockRunHealthCheck.mockResolvedValue(JSON.stringify({ ...report, results: report.results.filter(r => r.group !== 'runtimes') }))
    render(<HealthSection />)
    expect(await screen.findByTestId('runtimes-not-checked')).toHaveTextContent('settings.health.runtimesNotChecked')
  })
})
