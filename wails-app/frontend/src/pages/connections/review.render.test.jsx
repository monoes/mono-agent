// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const api = vi.hoisted(() => ({
  installAutomationDryRun: vi.fn(),
  installAutomation: vi.fn(),
  chooseAutomationPackage: vi.fn(),
  analyzeRecording: vi.fn(),
  verifyDraft: vi.fn(),
  saveDraft: vi.fn(),
}))
vi.mock('../../services/api.js', () => ({ api, notify: vi.fn() }))

import ConfirmHost from '../../components/ConfirmDialog.jsx'
import ImportDialog from './ImportDialog.jsx'
import RecordingReview from './RecordingReview.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

const dryRun = {
  id: 'hackernews', name: 'HN (fork)', version: '2.0.0', dryRun: true, sha256: 'abc123',
  review: {
    source: 'imported', trust: 'imported', publisher: 'Someone',
    replaces: { id: 'hackernews', source: 'builtin', trust: 'builtin', version: '1.1.0' },
    capabilities: ['can run scripts in the page that can read site data and send it anywhere'],
    scriptSources: { 'grab.js': 'return document.cookie' },
    domains: ['news.ycombinator.com'], steps: ['page_script'], scripts: ['grab.js'],
    actionEffects: { reply: 'message' }, files: [], tier: 'standard', computedTier: 'standard', callActions: [],
  },
  issues: [],
}

describe('ImportDialog', () => {
  it('shows capabilities, script source and the replaced built-in, and gates install on the tick', async () => {
    api.chooseAutomationPackage.mockResolvedValue('/tmp/hn.mpkg')
    api.installAutomationDryRun.mockResolvedValue(dryRun)
    api.installAutomation.mockResolvedValue({ id: 'hackernews', name: 'HN (fork)', version: '2.0.0', installed: true, review: {} })
    render(<ImportDialog onClose={() => {}} />)
    fireEvent.click(screen.getByText('Browse'))
    expect(await screen.findByText(/can run scripts in the page/)).toBeInTheDocument()
    expect(screen.getByText(/Replaces built-in hackernews 1.1.0/)).toBeInTheDocument()
    expect(screen.getByText('return document.cookie')).toBeInTheDocument()
    const install = screen.getByText('Install')
    expect(install).toBeDisabled()
    fireEvent.click(screen.getByLabelText(/I understand this replaces the built-in hackernews/))
    expect(install).not.toBeDisabled()
    fireEvent.click(install)
    await waitFor(() => expect(api.installAutomation).toHaveBeenCalledWith('/tmp/hn.mpkg', { expectSha256: 'abc123', replaceBuiltin: true }))
    expect(await screen.findByText(/Installed HN \(fork\) 2.0.0/)).toBeInTheDocument()
  })

  it('treats replacing an earlier import as a plain update (no tick, no --replace-builtin)', async () => {
    api.chooseAutomationPackage.mockResolvedValue('/tmp/b.mpkg')
    api.installAutomationDryRun.mockResolvedValue({ ...dryRun, previousVersion: '1.0.0', review: { ...dryRun.review, replaces: { id: 'hackernews', source: 'imported', trust: 'imported', version: '1.0.0' } } })
    api.installAutomation.mockResolvedValue({ id: 'hackernews', version: '2.0.0', installed: true, review: {} })
    render(<ImportDialog onClose={() => {}} />)
    fireEvent.click(screen.getByText('Browse'))
    const update = await screen.findByText('Update to 2.0.0')
    expect(screen.queryByText(/Replaces/)).not.toBeInTheDocument()
    expect(update).not.toBeDisabled()
    fireEvent.click(update)
    await waitFor(() => expect(api.installAutomation).toHaveBeenCalledWith('/tmp/b.mpkg', { expectSha256: 'abc123', replaceBuiltin: false }))
  })

  it('warns about plain http URLs and closes on Escape', () => {
    const onClose = vi.fn()
    render(<ImportDialog onClose={onClose} />)
    fireEvent.change(screen.getByLabelText('Package path or URL'), { target: { value: 'http://example.com/x.mpkg' } })
    expect(screen.getByText(/Plain http:\/\/ is not accepted/)).toBeInTheDocument()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})

const analyzed = {
  draftDir: '/d/rec1',
  draft: {
    recordingId: 'rec1', targetAutomation: 'acme', isNew: false, action: 'create_contact', saveAs: 'action',
    names: { automation: 'acme', action: 'create_contact', fragment: '' }, lint: [],
    actionDef: { description: 'Create a contact', steps: [{ id: 'open', type: 'navigate', url: 'https://app.acme.com' }, { id: 'save', type: 'click', intent: 'Save', sideEffect: true }] },
    inputs: [
      { name: 'email', type: 'string', required: true, secret: false, needsValue: false },
      { name: 'password', type: 'secret', required: true, secret: true, needsValue: true },
    ],
    scripts: { 'read.js': 'return 1' },
  },
}

describe('RecordingReview', () => {
  it('asks only for the values the CLI says it needs, masks secrets, and confirms a full verify', async () => {
    api.analyzeRecording.mockResolvedValue(analyzed)
    api.verifyDraft.mockResolvedValue({ steps: [{ id: 'open', status: 'pass' }], ok: true, stoppedAt: null })
    render(<><ConfirmHost /><RecordingReview recording={{ id: 'rec1', title: 'Rec' }} automationId="acme" onBack={() => {}} /></>)
    await screen.findByText('Create a contact')
    expect(api.analyzeRecording).toHaveBeenCalledWith('rec1', 'acme', false)
    expect(screen.queryByLabelText('Value for email during verify')).not.toBeInTheDocument()
    const pw = screen.getByLabelText('Value for password during verify')
    expect(pw).toHaveAttribute('type', 'password')
    fireEvent.change(pw, { target: { value: 'hunter2' } })
    expect(screen.getByText('return 1')).toBeInTheDocument()
    // sideEffects is missing on this draft: a full verify must confirm first.
    fireEvent.click(screen.getByText('Verify full'))
    fireEvent.click(await screen.findByText('Confirm'))
    await waitFor(() => expect(api.verifyDraft).toHaveBeenCalledWith('/d/rec1', true, { password: 'hunter2' }))
  })

  it('explains a verify step refused because scripts are off for recorded drafts', async () => {
    api.analyzeRecording.mockResolvedValue(analyzed)
    api.verifyDraft.mockResolvedValue({ ok: false, stoppedAt: null, steps: [
      { id: 'open', type: 'navigate', status: 'pass' },
      { id: 'save', type: 'page_script', status: 'fail', message: 'page_script step save: refused: scripts are not allowed for automation "acme" (allow with: monoagentcli automation trust acme --scripts)' },
    ] })
    render(<RecordingReview recording={{ id: 'rec1' }} automationId="acme" onBack={() => {}} />)
    await screen.findByText('Create a contact')
    expect(screen.queryByText(/which are off for recorded automations/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByText('Verify (safe)'))
    expect(await screen.findByText(/This draft runs page scripts, which are off for recorded automations/)).toBeInTheDocument()
  })

  it('does not show the script note for other failures', async () => {
    api.analyzeRecording.mockResolvedValue(analyzed)
    api.verifyDraft.mockResolvedValue({ ok: false, stoppedAt: null, steps: [{ id: 'save', type: 'click', status: 'fail', message: 'element not found' }] })
    render(<RecordingReview recording={{ id: 'rec1' }} automationId="acme" onBack={() => {}} />)
    await screen.findByText('Create a contact')
    fireEvent.click(screen.getByText('Verify (safe)'))
    expect(await screen.findByText('element not found')).toBeInTheDocument()
    expect(screen.queryByText(/which are off for recorded automations/)).not.toBeInTheDocument()
  })

  it('saves with renamed inputs', async () => {
    api.analyzeRecording.mockResolvedValue(analyzed)
    api.saveDraft.mockResolvedValue({ nodeType: 'acme.add_contact', version: '1.2.1' })
    render(<RecordingReview recording={{ id: 'rec1' }} automationId="acme" onBack={() => {}} />)
    await screen.findByText('Create a contact')
    fireEvent.change(screen.getByLabelText('Action name'), { target: { value: 'add_contact' } })
    fireEvent.change(screen.getByLabelText('Name for input email'), { target: { value: 'email_address' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.saveDraft).toHaveBeenCalledWith('/d/rec1', { as: 'action', name: 'add_contact', renameInputs: { email: 'email_address' }, automation: 'acme' }))
    expect(await screen.findByText(/Saved as node acme.add_contact/)).toBeInTheDocument()
  })

  it('holds a draft with lint errors until "Save anyway", then passes force', async () => {
    api.analyzeRecording.mockResolvedValue({ ...analyzed, draft: { ...analyzed.draft, lint: [{ severity: 'error', code: 'selector_ambiguous', stepId: 'save', message: 'matches 3 elements' }] } })
    api.saveDraft.mockResolvedValue({ nodeType: 'acme.create_contact', version: '1.2.1' })
    render(<RecordingReview recording={{ id: 'rec1' }} automationId="acme" onBack={() => {}} />)
    await screen.findByText('Create a contact')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('save: matches 3 elements', { exact: false })).toBeInTheDocument()
    expect(api.saveDraft).not.toHaveBeenCalled()
    fireEvent.click(screen.getByText('Save anyway'))
    await waitFor(() => expect(api.saveDraft).toHaveBeenCalledWith('/d/rec1', expect.objectContaining({ force: true })))
  })

  it('re-analyzes with advanced steps only after the warning is accepted', async () => {
    api.analyzeRecording.mockResolvedValue(analyzed)
    render(<><ConfirmHost /><RecordingReview recording={{ id: 'rec1' }} automationId="acme" onBack={() => {}} /></>)
    await screen.findByText('Create a contact')
    fireEvent.click(screen.getByText('Allow advanced steps…'))
    expect(await screen.findByText(/may then write page scripts/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Re-analyze'))
    await waitFor(() => expect(api.analyzeRecording).toHaveBeenLastCalledWith('rec1', 'acme', true))
  })
})
