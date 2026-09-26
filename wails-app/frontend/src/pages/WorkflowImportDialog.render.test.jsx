// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const api = vi.hoisted(() => ({
  importWorkflowFull: vi.fn(),
  chooseWorkflowFile: vi.fn(),
}))
vi.mock('../services/api.js', () => ({ api }))

import ConfirmHost from '../components/ConfirmDialog.jsx'
import { onAutomationsChanged } from '../lib/appEvents.js'
import WorkflowImportDialog, { copyOfExisting, splitBundle } from './WorkflowImportDialog.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

async function importFile(res, props = {}) {
  api.chooseWorkflowFile.mockResolvedValue('/w/flow.json')
  api.importWorkflowFull.mockResolvedValueOnce(res)
  render(<><ConfirmHost /><WorkflowImportDialog onClose={() => {}} onOpen={() => {}} {...props} /></>)
  fireEvent.click(screen.getByText('Browse'))
  await waitFor(() => expect(screen.getByLabelText('Workflow file path')).toHaveValue('/w/flow.json'))
  fireEvent.click(screen.getByText('Import'))
}

describe('WorkflowImportDialog', () => {
  it('shows created, updated and unchanged the way the CLI reports them', async () => {
    await importFile({ id: 'w1', name: 'Daily digest', status: 'created' })
    expect(await screen.findByText('Imported “Daily digest”.')).toBeInTheDocument()
    expect(api.importWorkflowFull).toHaveBeenCalledWith('/w/flow.json', { asNew: false })
    cleanup()
    await importFile({ id: 'w1', name: 'Daily digest', status: 'unchanged' })
    expect(await screen.findByText('Already imported — nothing changed.')).toBeInTheDocument()
    cleanup()
    await importFile({ id: 'w1', name: 'Daily digest', status: 'updated' })
    expect(await screen.findByText(/Updated “Daily digest”/)).toBeInTheDocument()
  })

  it('tells the page a workflow was imported (status bar count)', async () => {
    const onImported = vi.fn()
    await importFile({ id: 'w1', name: 'X', status: 'created' }, { onImported })
    await screen.findByText('Imported “X”.')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: 'w1' }))
  })

  it('opens the imported workflow', async () => {
    const onOpen = vi.fn()
    await importFile({ id: 'w9', name: 'X', status: 'created' }, { onOpen })
    fireEvent.click(await screen.findByText('Open'))
    expect(onOpen).toHaveBeenCalledWith('w9')
  })

  it('passes --as-new and accepts pasted JSON', async () => {
    api.importWorkflowFull.mockResolvedValue({ id: 'w2', name: 'Copy', status: 'created' })
    render(<WorkflowImportDialog onClose={() => {}} onOpen={() => {}} />)
    fireEvent.change(screen.getByLabelText('Or paste workflow JSON'), { target: { value: '{"name":"Copy","nodes":[]}' } })
    fireEvent.click(screen.getByLabelText(/Import as a new copy/))
    fireEvent.click(screen.getByText('Import'))
    await waitFor(() => expect(api.importWorkflowFull).toHaveBeenCalledWith('{"name":"Copy","nodes":[]}', { asNew: true }))
  })

  it('lists bundled automations and installs missing ones only after a confirm', async () => {
    await importFile({
      id: 'w1', name: 'Scrape', status: 'created',
      automations: [
        { id: 'books-demo', version: '0.1.0', status: 'missing', review: 'Bundled automation books-demo 0.1.0 — publisher Jane — domains books.toscrape.com — steps: navigate', reviewDetail: { id: 'books-demo', version: '0.1.0', publisher: 'Jane', domains: ['books.toscrape.com'], capabilities: ['can open and act on: books.toscrape.com'] } },
        { id: 'hackernews', version: '1.1.0', status: 'present', installedVersion: '1.1.0' },
      ],
      missingAutomations: ['books-demo'], installCommand: 'monoagentcli workflow import --file /w/flow.json --yes',
    })
    expect(await screen.findByText(/needs an automation that is not installed/)).toBeInTheDocument()
    expect(screen.getByText('monoagentcli workflow import --file /w/flow.json --yes')).toBeInTheDocument()
    expect(screen.getAllByText(/publisher Jane/)).toHaveLength(1)
    api.importWorkflowFull.mockResolvedValueOnce({
      id: 'w1', name: 'Scrape', status: 'unchanged',
      automations: [{ id: 'books-demo', version: '0.1.0', status: 'installed' }, { id: 'hackernews', version: '1.1.0', status: 'present' }],
      reviewLines: ['Bundled automation books-demo 0.1.0 — publisher Jane — domains books.toscrape.com — steps: navigate'],
    })
    fireEvent.click(screen.getByText('Install bundled automations'))
    // The confirm lists the package; cancelling installs nothing.
    expect(await screen.findByText('Install bundled automations', { selector: 'div' })).toBeInTheDocument()
    // The confirm shows the dry-run review before anything is installed.
    expect(screen.getByText('Publisher: Jane')).toBeInTheDocument()
    expect(screen.getByText('Domains: books.toscrape.com')).toBeInTheDocument()
    expect(screen.getByText('can open and act on: books.toscrape.com')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Cancel'))
    await waitFor(() => expect(api.importWorkflowFull).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByText('Install bundled automations'))
    fireEvent.click(await screen.findByText('Install'))
    await waitFor(() => expect(api.importWorkflowFull).toHaveBeenLastCalledWith('/w/flow.json', { yes: true }))
    expect(await screen.findByText('installed')).toBeInTheDocument()
    expect(screen.getByText('Installed 1 bundled automation for “Scrape”.')).toBeInTheDocument()
    expect(screen.getByText('Install review')).toBeInTheDocument()
    expect(screen.queryByText('Install bundled automations')).not.toBeInTheDocument()
  })

  it('announces installed automations so the node palette reloads', async () => {
    const onAutomationsInstalled = vi.fn(); const heard = vi.fn()
    const off = onAutomationsChanged(heard)
    await importFile({ id: 'w1', name: 'Scrape', status: 'created', automations: [{ id: 'books-demo', version: '0.1.0', status: 'missing' }], missingAutomations: ['books-demo'] }, { onAutomationsInstalled })
    api.importWorkflowFull.mockResolvedValueOnce({ id: 'w1', name: 'Scrape', status: 'unchanged', automations: [{ id: 'books-demo', version: '0.1.0', status: 'installed' }] })
    fireEvent.click(await screen.findByText('Install bundled automations'))
    fireEvent.click(await screen.findByText('Install'))
    await waitFor(() => expect(onAutomationsInstalled).toHaveBeenCalled())
    expect(heard).toHaveBeenCalledWith({ source: 'workflow-import' })
    off()
  })

  it('shows the trust drop and requires a tick when a bundled package replaces one', async () => {
    await importFile({
      id: 'w1', name: 'Scrape', status: 'created',
      automations: [{ id: 'acme', version: '2.0.0', status: 'missing', reviewDetail: {
        id: 'acme', version: '2.0.0', publisher: 'Jane', domains: ['app.acme.com'],
        capabilities: ["visits profiles — the profile's owner can see your visit (view_profile)"],
        replaces: { id: 'acme', source: 'local', trust: 'recorded', version: '1.0.0' }, replaceRequired: true,
        trustChange: { from: 'recorded', to: 'imported' }, visibility: { profile_view_visible_to_owner: ['view_profile'] } } }],
      missingAutomations: ['acme'],
    })
    expect(await screen.findByText(/Trust drops from recorded to imported/)).toBeInTheDocument()
    expect(screen.getByText(/visits profiles — the profile's owner can see your visit/)).toBeInTheDocument()
    expect(screen.getByText('Replaces local acme 1.0.0')).toBeInTheDocument()
    expect(screen.queryByText('• can open and act on: books.toscrape.com')).not.toBeInTheDocument()
    const install = screen.getByText('Install bundled automations').closest('button')
    expect(install).toBeDisabled()
    fireEvent.click(screen.getByLabelText(/I understand this replaces the local acme/))
    expect(install).not.toBeDisabled()
  })

  it('explains an import kept as a copy in plain words and replaces the existing one on request', async () => {
    await importFile({ id: 'copy-1', name: 'Daily digest', status: 'created',
      warnings: ['a workflow with this name already exists: orig-9; imported as a copy — use --replace orig-9 to replace it'] })
    expect(await screen.findByText(/A workflow named “Daily digest” already exists and was left as it is/)).toBeInTheDocument()
    expect(screen.queryByText(/--replace/)).not.toBeInTheDocument()
    api.importWorkflowFull.mockResolvedValueOnce({ id: 'orig-9', name: 'Daily digest', status: 'updated', removedCopy: 'copy-1' })
    fireEvent.click(screen.getByText('Replace the existing workflow instead'))
    fireEvent.click(await screen.findByText('Replace', { selector: 'button' }))
    await waitFor(() => expect(api.importWorkflowFull).toHaveBeenLastCalledWith('/w/flow.json', { replace: 'orig-9', removeCopy: 'copy-1' }))
    expect(await screen.findByText('Replaced the existing “Daily digest” with this file and removed the copy.')).toBeInTheDocument()
    expect(screen.queryByText('Replace the existing workflow instead')).not.toBeInTheDocument()
  })

  it('shows other warnings as they are', async () => {
    await importFile({ id: 'w1', name: 'X', status: 'created', warnings: ['node "a" uses a deprecated type'] })
    expect(await screen.findByText('⚠ node "a" uses a deprecated type')).toBeInTheDocument()
    expect(screen.queryByText('Replace the existing workflow instead')).not.toBeInTheDocument()
  })

  it('lists packages the file does not carry apart, without counting or installing them', async () => {
    await importFile({
      id: 'w1', name: 'Partial', status: 'created',
      automations: [
        { id: 'shelf-demo', version: '0.1.0', status: 'missing' },
        { id: 'ghost-pkg', version: '2.1.0', status: 'missing', notBundled: true, error: 'not in the bundle: no site domains; install ghost-pkg on this machine first' },
      ],
      missingAutomations: ['shelf-demo', 'ghost-pkg'],
    })
    expect(await screen.findByText('Not included in this file')).toBeInTheDocument()
    expect(screen.getByText('no site domains; install ghost-pkg on this machine first')).toBeInTheDocument()
    expect(screen.getByText(/needs an automation that is not installed\. Its nodes for shelf-demo will not run/)).toBeInTheDocument()
    api.importWorkflowFull.mockResolvedValueOnce({ id: 'w1', name: 'Partial', status: 'unchanged', automations: [
      { id: 'shelf-demo', version: '0.1.0', status: 'installed' },
      { id: 'ghost-pkg', version: '2.1.0', status: 'missing', notBundled: true, error: 'not in the bundle: no site domains; install ghost-pkg on this machine first' },
    ] })
    fireEvent.click(screen.getByText('Install bundled automations'))
    fireEvent.click(await screen.findByText('Install'))
    expect(await screen.findByText(/Installed 1 bundled automation/)).toBeInTheDocument()
    expect(screen.queryByText('Install bundled automations')).not.toBeInTheDocument()
    expect(screen.getByText('Not included in this file')).toBeInTheDocument()
  })

  it('has no install button when only not-bundled packages are missing', async () => {
    await importFile({ id: 'w1', name: 'P', status: 'created', automations: [{ id: 'ghost-pkg', version: '2.1.0', status: 'missing', notBundled: true, error: 'not in the bundle: x' }] })
    expect(await screen.findByText('Not included in this file')).toBeInTheDocument()
    expect(screen.queryByText('Install bundled automations')).not.toBeInTheDocument()
  })

  it('shows CLI errors inline and closes on Escape', async () => {
    const onClose = vi.fn()
    api.importWorkflowFull.mockResolvedValue({ error: 'node "x" has no type' })
    render(<WorkflowImportDialog onClose={onClose} onOpen={() => {}} />)
    fireEvent.change(screen.getByLabelText('Workflow file path'), { target: { value: '/w/bad.json' } })
    fireEvent.click(screen.getByText('Import'))
    expect(await screen.findByRole('alert')).toHaveTextContent('node "x" has no type')
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})

describe('import result helpers', () => {
  it('copyOfExisting prefers a structured field and reads the copy warning', () => {
    expect(copyOfExisting({ copyOf: 'a1', warnings: ['a workflow with this name already exists: b2; imported as a copy'] })).toBe('a1')
    expect(copyOfExisting({ warnings: ['a workflow with this name already exists: b2; imported as a copy — use --replace b2'] })).toBe('b2')
    expect(copyOfExisting({ warnings: ['something else'] })).toBe(null)
  })
  it('splitBundle keeps not-bundled packages out of the installable set', () => {
    const r = splitBundle([{ id: 'a', status: 'missing' }, { id: 'b', status: 'missing', notBundled: true }, { id: 'c', status: 'present', notBundled: true }])
    expect(r.installable.map(i => i.id)).toEqual(['a'])
    expect(r.notIncluded.map(i => i.id)).toEqual(['b'])
    expect(r.listed.map(i => i.id)).toEqual(['a', 'c'])
  })
})
