// @vitest-environment jsdom
// Focused mount tests for AIChatPanel's conversation-bucket bookkeeping —
// not a general-purpose harness for the whole panel (there isn't one yet),
// just enough mocked surface to prove one specific fix: switching backends
// (agents <-> providers) must never leave a stale conversationId from the
// OTHER backend bound, since StartChatTurn dispatches on the *stored*
// conversation's own Backend field, not on whatever this panel's current
// mode is.
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

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    api: {
      ...actual.api,
      scanAgentRuntimes: vi.fn().mockResolvedValue({ agents: [{ id: 'claude', installed: true, binary: '' }] }),
      getAgentRuntimeModels: vi.fn().mockResolvedValue([]),
      listAIProviders: vi.fn().mockResolvedValue([{ id: 1, name: 'openai', status: 'active', default_model: 'gpt' }]),
      listChatConversations: (...args) => listChatConversations(...args),
      createChatConversation: (...args) => createChatConversation(...args),
      startChatTurn: (...args) => startChatTurn(...args),
      getChatTurns: vi.fn().mockResolvedValue({ items: [] }),
      getChatEvents: vi.fn().mockResolvedValue({ items: [], hasMore: false }),
    },
  }
})

import AIChatPanel from './AIChatPanel.jsx'

// deferred() gives the test explicit control over exactly when each
// listChatConversations call resolves, instead of racing incidental
// microtask ordering between the runtime-scan effect (which flips
// useAgents true asynchronously) and the bucket effect it triggers.
function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

describe('AIChatPanel conversation bucket switching', () => {
  it('clears a stale conversationId from the other backend when the new bucket is empty', async () => {
    const providerBucket = deferred() // the initial mount's "general:provider" bucket (useAgents defaults false)
    const agentBucket = deferred()    // "general:agent", once the runtime scan flips useAgents true
    const providerBucketAgain = deferred() // back to "general:provider" after the user switches

    let call = 0
    listChatConversations.mockImplementation(() => {
      call += 1
      if (call === 1) return providerBucket.promise
      if (call === 2) return agentBucket.promise
      return providerBucketAgain.promise
    })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)

    providerBucket.resolve({ items: [] })
    await waitFor(() => expect(listChatConversations).toHaveBeenCalledTimes(2)) // agent bucket fired once the scan resolved

    // Agent bucket already has a conversation from a prior send() — auto-
    // continued on mount, matching today's "resume the most recent one"
    // behavior.
    agentBucket.resolve({
      items: [{ id: 'agent-conv-1', backend: 'agent', workflowContext: 'general', runtimeId: 'claude', model: '', updatedAt: '2026-09-12T00:00:00.000Z' }],
    })
    const backendSelect = await screen.findByTitle('Chat backend')
    await waitFor(() => expect(backendSelect.value).toBe('agents'))

    // Flip to providers — that bucket resolves empty.
    fireEvent.change(backendSelect, { target: { value: 'providers' } })
    await waitFor(() => expect(listChatConversations).toHaveBeenCalledTimes(3))
    providerBucketAgain.resolve({ items: [] })

    // Send a message under "providers" — it must create a NEW provider
    // conversation, never reuse the stale agent-backend id left over from
    // step 2 above.
    createChatConversation.mockResolvedValueOnce({ id: 'provider-conv-1', backend: 'provider' })
    startChatTurn.mockResolvedValueOnce({ ok: true, turnId: 'ignored-by-mock', status: 'active' })

    const textarea = await screen.findByPlaceholderText('Type a message...')
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }))

    await waitFor(() => expect(createChatConversation).toHaveBeenCalledWith('provider', 'general', '', '1', 'gpt'))
    expect(startChatTurn).toHaveBeenCalledWith('provider-conv-1', expect.any(String), 'hello', false, false)
  })
})
