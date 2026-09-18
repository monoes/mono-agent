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

// claude has a curated model list; opencode is unknown to ListModels and so
// comes back empty — the exact pairing that produced the stale-model bug.
const CLAUDE_MODELS = [
  { id: 'claude-opus-5', label: 'Opus 5' },
  { id: 'claude-sonnet-5', label: 'Sonnet 5' },
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
        ],
      }),
      getAgentRuntimeModels: (...args) => getAgentRuntimeModels(...args),
      listAIProviders: vi.fn().mockResolvedValue([]),
      listChatConversations: vi.fn().mockResolvedValue({ items: [] }),
      createChatConversation: vi.fn().mockResolvedValue({ id: 'conv-1' }),
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
