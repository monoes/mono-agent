// @vitest-environment jsdom
// "Set up AI agents": wherever an error says the AI agent is not set up
// (code or marker agent_not_setup, from the CLI), the user gets a button
// that opens the AI agents page — the chat panel (failed turn, refused
// start, no runtime), toasts, and a run's error on the dashboard.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, act, waitFor } from '@testing-library/react'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k }),
}))

Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const scanAgentRuntimes = vi.fn()
const listChatConversations = vi.fn()
const getChatTurns = vi.fn()
const getChatEvents = vi.fn()

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    api: {
      ...actual.api,
      scanAgentRuntimes: (...a) => scanAgentRuntimes(...a),
      getAgentRuntimeModels: vi.fn().mockResolvedValue([{ id: 'sonnet' }]),
      listChatConversations: (...a) => listChatConversations(...a),
      getChatTurns: (...a) => getChatTurns(...a),
      getChatEvents: (...a) => getChatEvents(...a),
    },
  }
})

import { notify } from '../services/api.js'
import { invalidateAgentScan } from '../lib/agentRuntimes.js'
import AIChatPanel, { MessageBubble } from './AIChatPanel.jsx'
import Toasts from './Toasts.jsx'
import RecentRunsCard from '../pages/dashboard/RecentRunsCard.jsx'

const ACTION = 'agentSetup.action'
const MARKED = 'node n1 (Ask): agent.ask (claude) turn failed: runner-error: Not logged in · Please run /login [agent_not_setup]'

beforeEach(() => {
  vi.clearAllMocks()
  invalidateAgentScan()
  listChatConversations.mockResolvedValue({ items: [] })
  getChatEvents.mockResolvedValue({ items: [], hasMore: false })
})

afterEach(() => {
  cleanup()
})

describe('MessageBubble', () => {
  it('links a refused start with code agent_not_setup to the AI agents page', () => {
    const onNavigate = vi.fn()
    render(<MessageBubble role="error" isError content="Error: monomind not found (AI engine)" code="agent_not_setup" onNavigate={onNavigate} />)
    fireEvent.click(screen.getByRole('button', { name: ACTION }))
    expect(onNavigate).toHaveBeenCalledWith('ai')
  })

  it('has no link for any other error', () => {
    render(<MessageBubble role="error" isError content="Error: usage limit reached" onNavigate={vi.fn()} />)
    expect(screen.queryByRole('button', { name: ACTION })).not.toBeInTheDocument()
  })
})

describe('Toasts', () => {
  it('adds the link to an agent_not_setup toast, hides the marker, and dismisses on click', () => {
    const onNavigate = vi.fn()
    render(<Toasts onNavigate={onNavigate} />)
    act(() => notify('run workflow', MARKED, 'agent_not_setup'))
    expect(screen.getByText(/Not logged in/)).not.toHaveTextContent('[agent_not_setup]')
    fireEvent.click(screen.getByRole('button', { name: ACTION }))
    expect(onNavigate).toHaveBeenCalledWith('ai')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('recognises the marker without a code, and leaves other toasts alone', () => {
    render(<Toasts onNavigate={vi.fn()} />)
    act(() => notify('org run', 'boom'))
    expect(screen.queryByRole('button', { name: ACTION })).not.toBeInTheDocument()
    act(() => notify('execution detail', new Error(MARKED)))
    expect(screen.getByRole('button', { name: ACTION })).toBeInTheDocument()
  })
})

describe('RecentRunsCard', () => {
  it('links a run that failed for want of an agent, beside its row', () => {
    const onNavigate = vi.fn()
    render(<RecentRunsCard onNavigate={onNavigate} executions={[
      { id: 'e1', workflow_id: 'w1', workflow_name: 'Digest', status: 'FAILED', error: MARKED },
      { id: 'e2', workflow_id: 'w2', workflow_name: 'Other', status: 'FAILED', error: 'node n2: timeout' },
    ]} />)
    expect(screen.getAllByRole('button', { name: ACTION })).toHaveLength(1)
    expect(screen.getByText(/Not logged in/).textContent).not.toContain('[agent_not_setup]')
    fireEvent.click(screen.getByRole('button', { name: ACTION }))
    expect(onNavigate).toHaveBeenCalledWith('ai')
    expect(onNavigate).not.toHaveBeenCalledWith('noderunner', expect.anything())
  })
})

describe('AIChatPanel', () => {
  it('offers the AI agents page when no runtime is installed', async () => {
    scanAgentRuntimes.mockResolvedValue({ agents: [{ id: 'claude', installed: false }] })
    const onNavigate = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen onClose={() => {}} onNavigate={onNavigate} />)
    const buttons = await screen.findAllByRole('button', { name: ACTION })
    expect(screen.getAllByText('agentSetup.noRuntime').length).toBeGreaterThan(0)
    fireEvent.click(buttons[0])
    expect(onNavigate).toHaveBeenCalledWith('ai')
  })

  it('offers it when monomind itself is missing', async () => {
    scanAgentRuntimes.mockResolvedValue({ error: 'monomind not found (AI engine)', code: 'agent_not_setup' })
    render(<AIChatPanel workflowID="general" isOpen onClose={() => {}} onNavigate={vi.fn()} />)
    expect((await screen.findAllByRole('button', { name: ACTION })).length).toBeGreaterThan(0)
  })

  it('links a turn that failed with code agent_not_setup, and only that turn', async () => {
    scanAgentRuntimes.mockResolvedValue({ agents: [{ id: 'claude', installed: true, binary: '' }] })
    listChatConversations.mockResolvedValue({
      items: [{ id: 'c1', backend: 'agent', workflowContext: 'general', runtimeId: 'claude', model: 'sonnet', updatedAt: '2026-09-27T00:00:00.000Z' }],
    })
    getChatTurns.mockResolvedValue({
      items: [
        { id: 't1', prompt: 'first', status: 'failed' },
        { id: 't2', prompt: 'second', status: 'failed' },
      ],
    })
    const finished = (turnId, payload) => ({ items: [
      { seq: 1, type: 'turn.started', conversationId: 'c1', turnId, payload: { backend: 'agent', text: 'hi' } },
      { seq: 2, type: 'turn.finished', conversationId: 'c1', turnId, payload },
    ], hasMore: false })
    getChatEvents.mockImplementation((_c, turnId) => Promise.resolve(turnId === 't1'
      ? finished('t1', { status: 'failed', reason: 'runner-error: Not logged in', code: 'agent_not_setup', exitCode: 1, historySaved: true })
      : finished('t2', { status: 'failed', reason: 'quota: usage limit', exitCode: 1, historySaved: true })))
    const onNavigate = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen onClose={() => {}} onNavigate={onNavigate} />)
    await screen.findByText('second')
    await waitFor(() => expect(screen.getAllByText('Failed')).toHaveLength(2))
    // Wait for the settled state: while the runtime list is still loading a
    // second, transient link can render (seen on slow CI runners).
    await waitFor(() => expect(screen.getAllByRole('button', { name: ACTION })).toHaveLength(1))
    const buttons = screen.getAllByRole('button', { name: ACTION })
    fireEvent.click(buttons[0])
    expect(onNavigate).toHaveBeenCalledWith('ai')
  })
})
