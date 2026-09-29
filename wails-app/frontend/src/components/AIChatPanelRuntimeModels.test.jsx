// @vitest-environment jsdom
// Regression tests for the model picker when the selected agent runtime
// exposes no models at all.
//
// GetAgentRuntimeModels answers "[]" (not an error) both for a runtime
// monomind's ListModels doesn't recognise and for one whose own discovery
// command failed — and api.js's guard() collapses a genuine error into []
// as well. The panel only overwrote selectedModel when the new list was
// non-empty, so switching from a runtime with models (claude) to one without
// (opencode, crush, copilot, pi, grok…) left the previous runtime's model id
// sitting in the free-text field. It read as that runtime's model and went
// straight to --model on the next send.
//
// An empty list now clears the model, shows "Not initialized" where the
// picker would be, and locks the composer so no turn can start.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'

Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const getAgentRuntimeModels = vi.fn()
const startChatTurn = vi.fn()
const createChatConversation = vi.fn().mockResolvedValue({ id: 'conv-1' })

// claude has a curated model list; opencode is unknown to ListModels and so
// comes back empty — the exact pairing that produced the stale-model bug.
const CLAUDE_MODELS = [
  { id: 'claude-opus-5', label: 'Opus 5', effort_levels: ['low', 'medium', 'high', 'max'] },
  { id: 'claude-haiku-4-5', label: 'Haiku 4.5' },
]

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    api: {
      ...actual.api,
      scanAgentRuntimes: vi.fn().mockResolvedValue({
        agents: [
          { id: 'claude', installed: true, binary: '' },
          { id: 'opencode', installed: true, binary: '' },
          { id: 'codex', installed: true, binary: '/bin/codex' },
        ],
      }),
      getAgentRuntimeModels: (...args) => getAgentRuntimeModels(...args),
      listChatConversations: vi.fn().mockResolvedValue({ items: [] }),
      createChatConversation: (...args) => createChatConversation(...args),
      startChatTurn: (...args) => startChatTurn(...args),
      getChatTurns: vi.fn().mockResolvedValue({ items: [] }),
      getChatEvents: vi.fn().mockResolvedValue({ items: [], hasMore: false }),
    },
  }
})

import AIChatPanel from './AIChatPanel.jsx'

/** Mount the panel with the runtime picker resolved to `runtime`. */
async function mountWith(runtime) {
  getAgentRuntimeModels.mockImplementation((id) =>
    Promise.resolve(id === 'claude' ? CLAUDE_MODELS : []),
  )
  render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
  const runtimeSelect = await screen.findByTitle(/Locally installed AI agent/i)
  await waitFor(() => expect(getAgentRuntimeModels).toHaveBeenCalled())
  if (runtimeSelect.value !== runtime) {
    fireEvent.change(runtimeSelect, { target: { value: runtime } })
  }
  return runtimeSelect
}

beforeEach(() => {
  vi.clearAllMocks()
  startChatTurn.mockResolvedValue({ turnId: 't1' })
})
afterEach(() => {
  cleanup()
})

describe('AIChatPanel — an agent runtime with no available models', () => {
  it('shows the model picker for a runtime that has models', async () => {
    await mountWith('claude')
    const picker = await screen.findByTitle(/Model available for the selected agent runtime/i)
    await waitFor(() => expect(picker.value).toBe('claude-opus-5'))
    expect(screen.queryByText('Not initialized')).not.toBeInTheDocument()
  })

  it('shows "Not initialized" instead of the previous runtime\'s model', async () => {
    await mountWith('claude')
    await waitFor(() =>
      expect(screen.getByTitle(/Model available for the selected agent runtime/i).value).toBe(
        'claude-opus-5',
      ),
    )

    const runtimeSelect = await screen.findByTitle(/Locally installed AI agent/i)
    fireEvent.change(runtimeSelect, { target: { value: 'opencode' } })

    await waitFor(() => expect(screen.getByText('Not initialized')).toBeInTheDocument())
    // The stale id must be gone, not merely hidden behind the label.
    expect(screen.queryByDisplayValue('claude-opus-5')).not.toBeInTheDocument()
  })

  it('locks the composer and refuses to start a turn', async () => {
    await mountWith('opencode')
    await waitFor(() => expect(screen.getByText('Not initialized')).toBeInTheDocument())

    const box = screen.getByPlaceholderText(/message|ask/i)
    expect(box).toBeDisabled()

    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(startChatTurn).not.toHaveBeenCalled()
  })

  it('re-enables everything when switching back to a runtime with models', async () => {
    await mountWith('opencode')
    await waitFor(() => expect(screen.getByText('Not initialized')).toBeInTheDocument())

    const runtimeSelect = await screen.findByTitle(/Locally installed AI agent/i)
    fireEvent.change(runtimeSelect, { target: { value: 'claude' } })

    await waitFor(() => expect(screen.queryByText('Not initialized')).not.toBeInTheDocument())
    const picker = await screen.findByTitle(/Model available for the selected agent runtime/i)
    await waitFor(() => expect(picker.value).toBe('claude-opus-5'))
    expect(screen.getByPlaceholderText(/message|ask/i)).not.toBeDisabled()
  })
})

describe('AIChatPanel — switching runtime while its models load', () => {
  it("never sends the previous runtime's model or effort", async () => {
    await mountWith('claude')
    const effortSelect = await screen.findByTitle(/Reasoning effort level for the selected model/i)
    fireEvent.change(effortSelect, { target: { value: 'high' } })

    // codex's model listing is still running when the user sends.
    getAgentRuntimeModels.mockImplementation((id) =>
      id === 'codex' ? new Promise(() => {}) : Promise.resolve(id === 'claude' ? CLAUDE_MODELS : []),
    )
    const runtimeSelect = await screen.findByTitle(/Locally installed AI agent/i)
    fireEvent.change(runtimeSelect, { target: { value: 'codex' } })
    await waitFor(() => expect(getAgentRuntimeModels).toHaveBeenCalledWith('codex', '/bin/codex'))

    const box = screen.getByPlaceholderText(/message|ask/i)
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await new Promise(r => setTimeout(r, 20))

    for (const call of createChatConversation.mock.calls) {
      expect(call[2]).not.toBe('claude-opus-5')
      expect(call[3]).not.toBe('high')
    }
    expect(screen.queryByText('Opus 5')).not.toBeInTheDocument()
  })
})

describe('AIChatPanel — model reasoning effort levels', () => {
  it('renders the effort dropdown when the selected model supports effort tiers', async () => {
    await mountWith('claude')
    const effortSelect = await screen.findByTitle(/Reasoning effort level for the selected model/i)
    expect(effortSelect).toBeInTheDocument()
    expect(effortSelect.value).toBe('')

    const options = Array.from(effortSelect.querySelectorAll('option')).map(o => o.value)
    expect(options).toEqual(['', 'low', 'medium', 'high', 'max'])
  })

  it('hides the effort dropdown when switching to a model without effort tiers', async () => {
    await mountWith('claude')
    expect(await screen.findByTitle(/Reasoning effort level for the selected model/i)).toBeInTheDocument()

    const modelSelect = screen.getByTitle(/Model available for the selected agent runtime/i)
    fireEvent.change(modelSelect, { target: { value: 'claude-haiku-4-5' } })

    await waitFor(() => {
      expect(screen.queryByTitle(/Reasoning effort level for the selected model/i)).not.toBeInTheDocument()
    })
  })

  it('forwards the selected effort to createChatConversation on message send', async () => {
    await mountWith('claude')
    const effortSelect = await screen.findByTitle(/Reasoning effort level for the selected model/i)
    fireEvent.change(effortSelect, { target: { value: 'high' } })

    const box = screen.getByPlaceholderText(/message|ask/i)
    fireEvent.change(box, { target: { value: 'explain monads' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    await waitFor(() => {
      expect(createChatConversation).toHaveBeenCalledWith('general', 'claude', 'claude-opus-5', 'high')
    })
  })

  it('forwards empty effort when sending with a model without effort tiers', async () => {
    await mountWith('claude')
    const modelSelect = screen.getByTitle(/Model available for the selected agent runtime/i)
    fireEvent.change(modelSelect, { target: { value: 'claude-haiku-4-5' } })

    const box = screen.getByPlaceholderText(/message|ask/i)
    fireEvent.change(box, { target: { value: 'fast question' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    await waitFor(() => {
      expect(createChatConversation).toHaveBeenCalledWith('general', 'claude', 'claude-haiku-4-5', '')
    })
  })
})

