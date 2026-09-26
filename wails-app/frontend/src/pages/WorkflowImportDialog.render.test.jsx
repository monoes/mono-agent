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
import WorkflowImportDialog from './WorkflowImportDialog.jsx'

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
        { id: 'books-demo', version: '0.1.0', status: 'missing', review: 'Bundled automation books-demo 0.1.0 — publisher Jane — domains books.toscrape.com — steps: navigate' },
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
