// @vitest-environment jsdom
// Coder mode in the chat panel (#203): the Assistant | Coder choice before
// a conversation starts, the workspace choice, the mode locked once the
// chat has started, the coder header, the history badge, and Stop.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, fireEvent, within } from '@testing-library/react'

Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const m = vi.hoisted(() => ({
  listChatConversations: vi.fn(),
  createChatConversation: vi.fn(),
  createCoderConversation: vi.fn(),
  startChatTurn: vi.fn(),
  stopChatTurn: vi.fn(),
  getChatTurns: vi.fn(),
  coderStatus: vi.fn(),
  coderWorkspaceNew: vi.fn(),
  coderWorkspaceList: vi.fn(),
  pickCoderFolder: vi.fn(),
  openPathWithOS: vi.fn(),
}))

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  const api = {
    ...actual.api,
    scanAgentRuntimes: vi.fn().mockResolvedValue({ agents: [{ id: 'codex', installed: true, binary: '' }, { id: 'claude', installed: true, binary: '' }] }),
    getAgentRuntimeModels: vi.fn().mockResolvedValue([{ id: 'sonnet' }]),
    getChatEvents: vi.fn().mockResolvedValue({ items: [], hasMore: false }),
  }
  for (const k of Object.keys(m)) api[k] = (...args) => m[k](...args)
  return { ...actual, api, onChatEvent: () => () => {} }
})

import AIChatPanel from './AIChatPanel.jsx'

const status = (over = {}) => ({
  enabled: true, workspaceRoot: '/home/u/monoagent-coder', maxTurns: 200, timeout: '60m', budgetUsd: 0,
  ready: true, missingCapabilities: [], runtime: 'claude', ...over,
})
const WS = '/home/u/monoagent-coder/20260927-brisk-otter'

beforeEach(() => {
  for (const f of Object.values(m)) f.mockReset()
  m.listChatConversations.mockResolvedValue({ items: [] })
  m.getChatTurns.mockResolvedValue({ items: [] })
  m.coderWorkspaceList.mockResolvedValue([{ path: '/w/recent-proj', lastUsed: '2026-09-27T10:00:00Z', conversations: 2, exists: true }])
  m.startChatTurn.mockResolvedValue({ ok: true, status: 'active' })
  m.stopChatTurn.mockResolvedValue({ ok: true })
})
afterEach(() => { cleanup() })

async function openPanel(props = {}) {
  render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} {...props} />)
  await screen.findByPlaceholderText('Type a message...')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Send message' }).title).not.toMatch(/No backend/))
}

async function sendMessage(text) {
  fireEvent.change(screen.getByPlaceholderText('Type a message...'), { target: { value: text } })
  const send = screen.getByRole('button', { name: 'Send message' })
  await waitFor(() => expect(send).not.toBeDisabled())
  fireEvent.click(send)
}

describe('AIChatPanel coder mode', () => {
  it('shows no mode choice while coder mode is off in Settings', async () => {
    m.coderStatus.mockResolvedValue(status({ enabled: false }))
    await openPanel()
    await waitFor(() => expect(m.coderStatus).toHaveBeenCalled())
    expect(screen.queryByTestId('coder-mode-picker')).not.toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: /Coder/ })).not.toBeInTheDocument()
  })

  it('never offers coder mode in a workflow chat', async () => {
    m.coderStatus.mockResolvedValue(status())
    await openPanel({ workflowID: 'wf-1' })
    expect(screen.queryByTestId('coder-mode-picker')).not.toBeInTheDocument()
    expect(m.coderStatus).not.toHaveBeenCalled()
  })

  it('disables Coder with a monomind-update hint when not ready', async () => {
    m.coderStatus.mockResolvedValue(status({ ready: false, missingCapabilities: ['agent-exec-full-access', 'init-json'] }))
    await openPanel()
    const coder = await screen.findByRole('radio', { name: /Coder/ })
    expect(coder).toBeDisabled()
    expect(screen.getByTestId('coder-not-ready')).toHaveTextContent('needs monomind update (missing: agent-exec-full-access, init-json)')
  })

  it('starts a coder chat in a new test folder, without tools, and then locks the mode', async () => {
    m.coderStatus.mockResolvedValue(status())
    m.coderWorkspaceNew.mockResolvedValue({ path: WS, created: true, git: true, init: { created: ['CLAUDE.md', '.claude/settings.json'], skipped: [] } })
    m.createCoderConversation.mockResolvedValue({ id: 'coder-1', backend: 'agent', mode: 'coder', cwd: WS })
    await openPanel()

    fireEvent.click(await screen.findByRole('radio', { name: /Coder/ }))
    const workspaces = screen.getByRole('radiogroup', { name: 'Coder workspace' })
    expect(within(workspaces).getByRole('radio', { name: /New test folder/ })).toHaveAttribute('aria-checked', 'true')
    expect(await within(workspaces).findByRole('radio', { name: /recent-proj/ })).toBeInTheDocument()

    await sendMessage('write hello.py and run it')
    await waitFor(() => expect(m.createCoderConversation).toHaveBeenCalledWith('claude', 'sonnet', WS, false))
    expect(m.coderWorkspaceNew).toHaveBeenCalledTimes(1)
    expect(m.createChatConversation).not.toHaveBeenCalled()
    await waitFor(() => expect(m.startChatTurn).toHaveBeenCalledWith('coder-1', expect.any(String), 'write hello.py and run it', false, false))

    // Mode locked: the picker is gone, the coder header shows the folder.
    expect(screen.queryByTestId('coder-mode-picker')).not.toBeInTheDocument()
    expect(screen.getByTestId('coder-cwd')).toHaveTextContent(WS)
    expect(screen.getByTestId('coder-init-note')).toHaveTextContent(`Created test folder ${WS} · created CLAUDE.md, .claude/settings.json · git repository`)
    fireEvent.click(screen.getByRole('button', { name: /Open folder/ }))
    expect(m.openPathWithOS).toHaveBeenCalledWith(WS)

    // Stop (button, then Esc) stops the running coder turn.
    fireEvent.click(screen.getByRole('button', { name: 'Stop generating' }))
    await waitFor(() => expect(m.stopChatTurn).toHaveBeenCalledWith('coder-1', expect.any(String)))
  })

  it('Escape stops a running coder turn instead of closing the panel', async () => {
    m.coderStatus.mockResolvedValue(status())
    m.createCoderConversation.mockResolvedValue({ id: 'coder-2', backend: 'agent', mode: 'coder', cwd: '/w/recent-proj' })
    const onClose = vi.fn()
    await openPanel({ onClose })
    fireEvent.click(await screen.findByRole('radio', { name: /Coder/ }))
    fireEvent.click(await screen.findByRole('radio', { name: /recent-proj/ }))
    await sendMessage('go')
    await waitFor(() => expect(m.createCoderConversation).toHaveBeenCalledWith('claude', 'sonnet', '/w/recent-proj', false))
    expect(m.coderWorkspaceNew).not.toHaveBeenCalled()
    await screen.findByRole('button', { name: 'Stop generating' })
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(m.stopChatTurn).toHaveBeenCalledWith('coder-2', expect.any(String)))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('uses a folder chosen with the native picker', async () => {
    m.coderStatus.mockResolvedValue(status())
    m.pickCoderFolder.mockResolvedValue('/home/u/src/my-repo')
    m.createCoderConversation.mockResolvedValue({ id: 'coder-3', backend: 'agent', mode: 'coder', cwd: '/home/u/src/my-repo' })
    await openPanel()
    fireEvent.click(await screen.findByRole('radio', { name: /Coder/ }))
    fireEvent.click(screen.getByRole('radio', { name: /Choose folder/ }))
    expect(await screen.findByText('/home/u/src/my-repo')).toBeInTheDocument()
    await sendMessage('hi')
    await waitFor(() => expect(m.createCoderConversation).toHaveBeenCalledWith('claude', 'sonnet', '/home/u/src/my-repo', false))
  })

  it('an assistant chat still creates an assistant conversation with tools', async () => {
    m.coderStatus.mockResolvedValue(status())
    m.createChatConversation.mockResolvedValue({ id: 'a-1', backend: 'agent', mode: 'assistant' })
    await openPanel()
    await screen.findByRole('radio', { name: /Assistant/ })
    await sendMessage('hello')
    await waitFor(() => expect(m.createChatConversation).toHaveBeenCalled())
    expect(m.createCoderConversation).not.toHaveBeenCalled()
    await waitFor(() => expect(m.startChatTurn).toHaveBeenCalledWith('a-1', expect.any(String), 'hello', true, true))
    expect(screen.queryByTestId('coder-header')).not.toBeInTheDocument()
  })

  it('a reopened coder conversation shows its folder, and history rows carry a coder badge', async () => {
    m.coderStatus.mockResolvedValue(status())
    m.listChatConversations.mockResolvedValue({
      items: [
        { id: 'coder-9', backend: 'agent', workflowContext: 'general', runtimeId: 'claude', model: 'sonnet', mode: 'coder', cwd: WS, updatedAt: '2026-09-27T10:00:00Z' },
        { id: 'a-9', backend: 'agent', workflowContext: 'general', runtimeId: 'claude', model: 'sonnet', mode: 'assistant', cwd: '', updatedAt: '2026-09-26T10:00:00Z' },
      ],
    })
    m.getChatTurns.mockResolvedValue({ items: [{ id: 't1', prompt: 'earlier prompt', status: 'completed' }] })
    await openPanel()
    await screen.findByText('earlier prompt')
    expect(screen.getByTestId('coder-cwd')).toHaveTextContent(WS)
    expect(screen.queryByTestId('coder-mode-picker')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Past sessions'))
    const rows = await screen.findAllByRole('option')
    expect(within(rows[0]).getByTestId('coder-badge')).toBeInTheDocument()
    expect(within(rows[0]).getByText('20260927-brisk-otter')).toBeInTheDocument()
    expect(within(rows[1]).queryByTestId('coder-badge')).not.toBeInTheDocument()
  })
})
