// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import ConfirmHost from '../ConfirmDialog.jsx'
import CoderModeSection, { CODER_RISK_TITLE } from './CoderModeSection.jsx'

const App = {}
beforeEach(() => {
  for (const k of ['CoderStatus', 'CoderEnable', 'CoderDisable', 'CoderSet', 'PickCoderFolder']) App[k] = vi.fn()
  window.go = { main: { App } }
})
afterEach(() => { cleanup(); delete window.go })

const status = (over = {}) => JSON.stringify({
  enabled: false, workspaceRoot: '/home/u/monoagent-coder', maxTurns: 200, timeout: '60m', budgetUsd: 0,
  ready: true, missingCapabilities: [], monomindVersion: '1.2.3', runtime: 'claude', ...over,
})

function mount(st) {
  App.CoderStatus.mockResolvedValue(st)
  return render(<><CoderModeSection /><ConfirmHost /></>)
}

describe('CoderModeSection', () => {
  it('is off by default and shows the saved settings', async () => {
    mount(status())
    const toggle = await screen.findByRole('checkbox', { name: 'Coder mode' })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    expect(toggle).not.toBeChecked()
    expect(screen.getByLabelText('Coder root')).toHaveValue('/home/u/monoagent-coder')
    expect(screen.getByLabelText('Max turns')).toHaveValue('200')
    expect(screen.getByLabelText('Timeout')).toHaveValue('60m')
    expect(screen.getByLabelText('Budget per turn')).toHaveValue('')
  })

  it('turning it on asks for the risk confirmation first; cancelling leaves it off', async () => {
    mount(status())
    const toggle = await screen.findByRole('checkbox', { name: 'Coder mode' })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    fireEvent.click(toggle)
    const dialog = await screen.findByRole('dialog', { name: CODER_RISK_TITLE })
    expect(dialog).toHaveTextContent('run any command, read and change any file your user account can, and install software')
    expect(dialog).toHaveTextContent('Only point it at folders and repos you trust')
    expect(dialog).toHaveTextContent('CLAUDE.md, skills, hooks and MCP servers')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(App.CoderEnable).not.toHaveBeenCalled()
    expect(toggle).not.toBeChecked()
  })

  it('confirming turns it on through the CLI', async () => {
    mount(status())
    App.CoderEnable.mockResolvedValue(status({ enabled: true }))
    const toggle = await screen.findByRole('checkbox', { name: 'Coder mode' })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    fireEvent.click(toggle)
    fireEvent.click(await screen.findByRole('button', { name: 'Turn on Coder mode' }))
    await waitFor(() => expect(App.CoderEnable).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(toggle).toBeChecked())
  })

  it('turning it off needs no confirmation', async () => {
    mount(status({ enabled: true }))
    App.CoderDisable.mockResolvedValue(status({ enabled: false }))
    const toggle = await screen.findByRole('checkbox', { name: 'Coder mode' })
    await waitFor(() => expect(toggle).toBeChecked())
    fireEvent.click(toggle)
    await waitFor(() => expect(App.CoderDisable).toHaveBeenCalled())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(toggle).not.toBeChecked())
  })

  it('shows the monomind version when ready', async () => {
    mount(status({ enabled: true, monomindVersion: '2.17.0' }))
    expect(await screen.findByTestId('coder-ready')).toHaveTextContent('Ready · monomind 2.17.0 · claude')
    expect(screen.queryByTestId('coder-needs-update')).not.toBeInTheDocument()
  })

  it('says monomind needs an update when not ready, and still allows turning it on', async () => {
    mount(status({ ready: false, missingCapabilities: ['agent-exec-tool-activity', 'init-json'] }))
    expect(await screen.findByTestId('coder-needs-update')).toHaveTextContent('needs monomind update (missing: agent-exec-tool-activity, init-json)')
    expect(screen.getByRole('checkbox', { name: 'Coder mode' })).not.toBeDisabled()
  })

  it('saves the workspace root (from the folder picker) and defaults with coder set', async () => {
    mount(status({ enabled: true }))
    App.PickCoderFolder.mockResolvedValue('/data/coder')
    App.CoderSet.mockResolvedValue(status({ enabled: true, workspaceRoot: '/data/coder', maxTurns: 50, timeout: '30m', budgetUsd: 1.5 }))
    await screen.findByLabelText('Coder root')
    fireEvent.click(screen.getByRole('button', { name: /Browse/ }))
    await waitFor(() => expect(screen.getByLabelText('Coder root')).toHaveValue('/data/coder'))
    fireEvent.change(screen.getByLabelText('Max turns'), { target: { value: '50' } })
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '30m' } })
    fireEvent.change(screen.getByLabelText('Budget per turn'), { target: { value: '1.5' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(App.CoderSet).toHaveBeenCalledWith('/data/coder', 50, '30m', 1.5))
    expect(await screen.findByText(/Saved\./)).toBeInTheDocument()
  })

  it('shows the CLI error', async () => {
    mount(status({ enabled: true }))
    App.CoderSet.mockResolvedValue(JSON.stringify({ error: 'invalid --timeout "soon"', code: 'invalid_input' }))
    fireEvent.change(await screen.findByLabelText('Timeout'), { target: { value: 'soon' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid --timeout "soon"')
  })
})
