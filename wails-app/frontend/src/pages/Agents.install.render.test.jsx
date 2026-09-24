// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'

const scan = {
  v: 1,
  agents: [
    { id: 'claude', installed: true, version: '2.1.0', install_hint: 'npm install -g @anthropic-ai/claude-code',
      install: { kind: 'npm', packages: ['@anthropic-ai/claude-code'] }, login_hint: 'claude login' },
    { id: 'codex', installed: false, install_hint: 'npm install -g @openai/codex',
      install: { kind: 'npm', packages: ['@openai/codex'] } },
    { id: 'grok', installed: false, install_hint: 'install the Grok Build CLI per https://docs.x.ai/build/cli',
      install: { kind: 'manual' } },
  ],
}
const mockInstall = vi.fn()
const mockConfirm = vi.fn()
vi.mock('../lib/agentRuntimes.js', () => ({
  cachedAgentScan: () => Promise.resolve(scan),
  invalidateAgentScan: vi.fn(),
}))
vi.mock('../lib/health.js', () => ({
  installRuntime: (...a) => mockInstall(...a),
  runHealth: vi.fn(),
}))
vi.mock('../services/api.js', () => ({ api: { isMonomindInitialized: () => Promise.resolve(true) } }))
vi.mock('../components/ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))
vi.mock('../components/MonomindInitPrompt.jsx', () => ({ default: () => null }))

import Agents from './Agents.jsx'

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})
afterEach(cleanup)

const tile = id => document.querySelector(`[data-runtime="${id}"]`)

describe('Agents page install actions', () => {
  it('offers Install, Update and Copy steps by recipe', async () => {
    render(<Agents onOpenChat={() => {}} />)
    await waitFor(() => expect(tile('codex')).toBeTruthy())
    expect(within(tile('codex')).getByText('Install')).toBeInTheDocument()
    expect(within(tile('claude')).getByText('Update')).toBeInTheDocument()
    expect(within(tile('claude')).getByText('sign in: claude login')).toBeInTheDocument()
    expect(within(tile('grok')).getByText('Copy steps')).toBeInTheDocument()
    expect(within(tile('grok')).queryByText('Install')).toBeNull()
  })

  it('installs only after the user confirms, then shows the result', async () => {
    mockConfirm.mockResolvedValue(true)
    mockInstall.mockImplementation(async (id, update, onLine) => { onLine('added 12 packages'); return { ok: true, message: 'codex 1.0 installed' } })
    render(<Agents onOpenChat={() => {}} />)
    await waitFor(() => expect(tile('codex')).toBeTruthy())
    fireEvent.click(within(tile('codex')).getByText('Install'))
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith('codex', false, expect.any(Function)))
    expect(mockConfirm).toHaveBeenCalledTimes(1)
    await screen.findByText('codex 1.0 installed')
  })

  it('does nothing when the confirmation is declined', async () => {
    mockConfirm.mockResolvedValue(false)
    render(<Agents onOpenChat={() => {}} />)
    await waitFor(() => expect(tile('claude')).toBeTruthy())
    fireEvent.click(within(tile('claude')).getByText('Update'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled())
    expect(mockInstall).not.toHaveBeenCalled()
  })
})
