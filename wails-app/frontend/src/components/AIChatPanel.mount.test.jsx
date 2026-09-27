// @vitest-environment jsdom
// Focused mount tests for AIChatPanel's conversation bookkeeping — not a
// general-purpose harness for the whole panel (there isn't one yet), just
// enough mocked surface to prove that chat is agent-only: a new chat
// creates an "agent" conversation, and a conversation from the removed
// in-app AI provider stays readable but read-only.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'

// jsdom doesn't implement scrollIntoView/scrollTo at all — useChatScroll
// calls scrollTo on every new-content signal once mounted.
Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const listChatConversations = vi.fn()
const createChatConversation = vi.fn()
const startChatTurn = vi.fn()
const getChatTurns = vi.fn()

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    api: {
      ...actual.api,
      scanAgentRuntimes: vi.fn().mockResolvedValue({ agents: [{ id: 'claude', installed: true, binary: '' }] }),
      getAgentRuntimeModels: vi.fn().mockResolvedValue([{ id: 'sonnet' }]),
      listChatConversations: (...args) => listChatConversations(...args),
      createChatConversation: (...args) => createChatConversation(...args),
      startChatTurn: (...args) => startChatTurn(...args),
      getChatTurns: (...args) => getChatTurns(...args),
      getChatEvents: vi.fn().mockResolvedValue({ items: [], hasMore: false }),
    },
  }
})

import AIChatPanel from './AIChatPanel.jsx'

const readOnlyNote = 'This conversation used a removed AI provider. Start a new chat to continue.'

beforeEach(() => {
  vi.clearAllMocks()
  getChatTurns.mockImplementation((convId) => Promise.resolve({
    items: [{ id: `turn-${convId}`, prompt: `prompt of ${convId}`, status: 'completed' }],
  }))
})

afterEach(() => {
  cleanup()
})

describe('AIChatPanel agent-only chat', () => {
  it('creates an agent conversation on the selected runtime for a new chat', async () => {
    listChatConversations.mockResolvedValue({ items: [] })
    createChatConversation.mockResolvedValueOnce({ id: 'agent-conv-1', backend: 'agent' })
    startChatTurn.mockResolvedValueOnce({ ok: true, turnId: 'ignored-by-mock', status: 'active' })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    const textarea = await screen.findByPlaceholderText('Type a message...')
    fireEvent.change(textarea, { target: { value: 'hello' } })
    const sendButton = screen.getByRole('button', { name: 'Send message' })
    await waitFor(() => expect(sendButton).not.toBeDisabled())
    fireEvent.click(sendButton)

    await waitFor(() => expect(createChatConversation).toHaveBeenCalledWith('general', 'claude', 'sonnet'))
    expect(startChatTurn).toHaveBeenCalledWith('agent-conv-1', expect.any(String), 'hello', expect.any(Boolean), expect.any(Boolean))
    expect(screen.queryByTitle('Chat backend')).not.toBeInTheDocument()
  })

  it('auto-continues the latest agent conversation, never a newer provider one', async () => {
    listChatConversations.mockResolvedValue({
      items: [
        { id: 'prov-conv', backend: 'provider', workflowContext: 'general', providerId: 'p1', model: 'gpt', updatedAt: '2026-09-12T00:00:02.000Z' },
        { id: 'agent-conv', backend: 'agent', workflowContext: 'general', runtimeId: 'claude', model: 'sonnet', updatedAt: '2026-09-12T00:00:01.000Z' },
      ],
    })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await screen.findByText('prompt of agent-conv')
    expect(getChatTurns).not.toHaveBeenCalledWith('prov-conv', expect.anything(), expect.anything())
    expect(screen.queryByText(readOnlyNote)).not.toBeInTheDocument()
  })

  it('shows a provider conversation read-only: history loads, the composer is disabled', async () => {
    listChatConversations.mockResolvedValue({
      items: [
        { id: 'prov-conv', backend: 'provider', workflowContext: 'general', providerId: 'p1', model: 'gpt', updatedAt: '2026-09-12T00:00:02.000Z' },
      ],
    })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await screen.findByPlaceholderText('Type a message...')

    fireEvent.click(screen.getByTitle('Past sessions'))
    const row = await screen.findByRole('option', { name: /read-only/ })
    fireEvent.click(row)

    await screen.findByText('prompt of prov-conv')
    // Shown in the composer banner (and announced in the live region).
    await waitFor(() => expect(screen.getAllByText(readOnlyNote).length).toBeGreaterThan(0))
    expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled()

    // A new chat leaves the read-only conversation and can send again.
    fireEvent.click(screen.getByTitle('New chat'))
    await waitFor(() => expect(screen.queryByText(readOnlyNote)).not.toBeInTheDocument())
    expect(createChatConversation).not.toHaveBeenCalled()
  })
})
