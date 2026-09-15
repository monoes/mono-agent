// @vitest-environment jsdom
//
// Focused integration tests for the two remaining live-region gaps recorded
// in docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md
// under "Turn-status live region mount-coupled": mid-turn notices, and
// ChatComposer's disabledReason banner. Not a general-purpose harness for
// the whole panel — AIChatPanel.mount.test.jsx already documents the same
// "focused, not general-purpose" scoping for exactly this reason.
//
// This lives in its own file (rather than folding into the much larger
// ChatInteraction.render.test.jsx) for two concrete reasons:
//
//   1. A live mid-turn event needs a working onChatEvent pub/sub. The
//      shared api.js mock in ChatInteraction.render.test.jsx spreads
//      `...actual` for onChatEvent, i.e. the REAL implementation, which
//      no-ops in jsdom (no window.runtime/window.go) — none of that file's
//      28 existing tests need live push, they only drive state through
//      polled getChatEvents. Overriding onChatEvent there for two new tests
//      risked nothing for the other 28, but a dedicated file makes the
//      wiring obvious rather than bolted onto an unrelated shared mock.
//   2. The disabledReason "changes text" test needs `cachedAgentScan`
//      (wails-app/frontend/src/lib/agentRuntimes.js) to resolve on a
//      timeline THIS test controls. That module keeps a 5-minute
//      module-level TTL cache shared by every consumer in the process —
//      once any earlier test's mount warms it, api.scanAgentRuntimes() is
//      never called again for the rest of that file's run, and a
//      mockReturnValueOnce/mockResolvedValueOnce override on it would
//      silently never fire. Mocking agentRuntimes.js directly (below)
//      sidesteps that cache entirely instead of relying on file/test
//      ordering to keep a shared cache cold.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, fireEvent, act } from '@testing-library/react'

// jsdom implements neither — AIChatPanel's scroll handling touches both.
Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

// ── Fake chat:event pub/sub, same shape as useChatStream.test.jsx's own ────
const chatEventListeners = new Set()
function emitChatEvent(payload) {
  for (const cb of chatEventListeners) cb(payload)
}

const createChatConversation = vi.fn()
const startChatTurn = vi.fn()
const listChatConversations = vi.fn().mockResolvedValue({ items: [] })
const getChatTurns = vi.fn().mockResolvedValue({ items: [] })
const getChatEvents = vi.fn().mockResolvedValue({ items: [], hasMore: false })
const listAIProviders = vi.fn().mockResolvedValue([{ id: 1, name: 'openai', status: 'active', default_model: 'gpt' }])
const cachedAgentScan = vi.fn().mockResolvedValue({ agents: [] })

// Bypasses agentRuntimes.js's own module-level TTL cache entirely — see the
// file-level comment above for why that cache makes per-test control of a
// scan's resolution impossible once any earlier test has warmed it.
vi.mock('../lib/agentRuntimes.js', () => ({
  cachedAgentScan: (...args) => cachedAgentScan(...args),
}))

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    onChatEvent: (cb) => {
      chatEventListeners.add(cb)
      return () => chatEventListeners.delete(cb)
    },
    api: {
      ...actual.api,
      listAIProviders: (...args) => listAIProviders(...args),
      listChatConversations: (...args) => listChatConversations(...args),
      createChatConversation: (...args) => createChatConversation(...args),
      startChatTurn: (...args) => startChatTurn(...args),
      getChatTurns: (...args) => getChatTurns(...args),
      getChatEvents: (...args) => getChatEvents(...args),
    },
  }
})

import AIChatPanel from './AIChatPanel.jsx'

beforeEach(() => {
  vi.clearAllMocks()
  chatEventListeners.clear()
})

afterEach(() => {
  cleanup()
})

// Starts a turn in "providers" mode (the default/only backend these tests'
// mocks expose) and returns the live turn's actual client-generated turnId
// — captured from startChatTurn's own call args, since AIChatPanel binds
// activeTurnId to newTurnId()'s return value, not to anything the mocked
// startChatTurn resolves.
async function sendAndCaptureTurnId(conversationId) {
  createChatConversation.mockResolvedValue({ id: conversationId, backend: 'provider' })
  startChatTurn.mockResolvedValue({ ok: true, turnId: 'ignored-by-app', status: 'active' })

  render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
  const textarea = await screen.findByPlaceholderText('Type a message...')
  fireEvent.change(textarea, { target: { value: 'hello' } })
  const sendButton = screen.getByRole('button', { name: 'Send message' })
  await waitFor(() => expect(sendButton).not.toBeDisabled())
  fireEvent.click(sendButton)

  await waitFor(() => expect(startChatTurn).toHaveBeenCalled())
  const [, turnId] = startChatTurn.mock.calls[0]
  return turnId
}

function liveRegion() {
  return screen.getByRole('status', { name: /chat turn announcements/i })
}

describe('AIChatPanel live region: mid-turn notices', () => {
  it('announces a notice event the moment it arrives, without waiting for the turn to finish', async () => {
    const turnId = await sendAndCaptureTurnId('conv-notice-1')

    act(() => emitChatEvent({
      version: 1, profileId: 'default', conversationId: 'conv-notice-1', turnId,
      seq: 1, at: '2026-09-12T00:00:00Z', type: 'notice',
      payload: { code: 'rate_limited', message: 'Slowing down due to rate limits.', severity: 'warning' },
    }))

    await waitFor(() => expect(liveRegion()).toHaveTextContent('Slowing down due to rate limits.'))
    // The announcement landed before finalize — the turn is still streaming.
    expect(screen.getByRole('button', { name: 'Stop generating' })).toBeInTheDocument()
  })

  it('does not repeat a mid-turn notice in the finalize-time summary once the turn completes', async () => {
    const turnId = await sendAndCaptureTurnId('conv-notice-2')

    act(() => emitChatEvent({
      version: 1, profileId: 'default', conversationId: 'conv-notice-2', turnId,
      seq: 1, at: '2026-09-12T00:00:00Z', type: 'notice',
      payload: { code: 'rate_limited', message: 'Slowing down due to rate limits.', severity: 'warning' },
    }))
    await waitFor(() => expect(liveRegion()).toHaveTextContent('Slowing down due to rate limits.'))

    await act(async () => {
      emitChatEvent({
        version: 1, profileId: 'default', conversationId: 'conv-notice-2', turnId,
        seq: 2, at: '2026-09-12T00:00:01Z', type: 'turn.finished',
        payload: { status: 'completed', reason: 'end_turn', exitCode: 0, historySaved: true },
      })
      await Promise.resolve()
    })

    // The finalize summary must land as exactly "Response completed." — not
    // the notice message repeated a second time alongside it.
    await waitFor(() => expect(liveRegion().textContent).toBe('Response completed.'))
    expect(screen.getByText('Completed')).toBeInTheDocument()
  })

  it('announces two distinct mid-turn notices independently, and excludes both from the finalize summary', async () => {
    const turnId = await sendAndCaptureTurnId('conv-notice-3')

    act(() => emitChatEvent({
      version: 1, profileId: 'default', conversationId: 'conv-notice-3', turnId,
      seq: 1, at: '2026-09-12T00:00:00Z', type: 'notice',
      payload: { code: 'a', message: 'First warning.', severity: 'warning' },
    }))
    await waitFor(() => expect(liveRegion()).toHaveTextContent('First warning.'))

    act(() => emitChatEvent({
      version: 1, profileId: 'default', conversationId: 'conv-notice-3', turnId,
      seq: 2, at: '2026-09-12T00:00:01Z', type: 'notice',
      payload: { code: 'b', message: 'Second warning.', severity: 'warning' },
    }))
    // Exact equality, not toHaveTextContent's substring match: proves the
    // region was updated to just the NEW notice (slice(announcedCount) in
    // the mid-turn branch), not "First warning. Second warning." — which a
    // substring check on "Second warning." alone wouldn't distinguish.
    await waitFor(() => expect(liveRegion().textContent).toBe('Second warning.'))

    await act(async () => {
      emitChatEvent({
        version: 1, profileId: 'default', conversationId: 'conv-notice-3', turnId,
        seq: 3, at: '2026-09-12T00:00:02Z', type: 'turn.finished',
        payload: { status: 'completed', reason: 'end_turn', exitCode: 0, historySaved: true },
      })
      await Promise.resolve()
    })

    await waitFor(() => expect(liveRegion().textContent).toBe('Response completed.'))
  })
})

describe('AIChatPanel live region: composer disabledReason banner', () => {
  it('announces the disabledReason banner once its text changes (e.g. once a runtime scan resolves monomind missing)', async () => {
    // No providers this test, so hasBackend stays false throughout.
    // cachedAgentScan is held pending for the whole first half — runtimesLoading
    // (seeded from isOpen=true) makes "Loading available AI systems…" the
    // panel's actual first-committed state, so that's the resting-state
    // baseline; the scan settling to monomindMissing is the first real change.
    listAIProviders.mockResolvedValue([])
    let resolveScan
    cachedAgentScan.mockReturnValueOnce(new Promise(r => { resolveScan = r }))

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await screen.findByPlaceholderText('Type a message...')

    expect(screen.getByText('Loading available AI systems…')).toBeInTheDocument()
    const region = liveRegion()
    // Nothing changed yet relative to the panel's first render — opening
    // the panel in its resting (already-loading) state must not itself
    // announce anything.
    expect(region).toBeEmptyDOMElement()

    await act(async () => {
      resolveScan({ error: 'monomind not found' })
      await new Promise(r => setTimeout(r, 0))
    })

    await waitFor(() => expect(screen.queryByText('Loading available AI systems…')).not.toBeInTheDocument())
    await waitFor(() => expect(region).toHaveTextContent(/monomind not found/i))
  })

  it('announces that Send became available once a provider finishes loading', async () => {
    // cachedAgentScan uses the file-level default (resolves quickly, finds
    // no agents) — that settling to "Select an AI provider..." is itself a
    // real, legitimate announcement now (Loading… -> nothing installed),
    // distinct from and prior to the providers-resolving announcement this
    // test is actually about.
    let resolveProviders
    listAIProviders.mockReturnValueOnce(new Promise(r => { resolveProviders = r }))

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await screen.findByPlaceholderText('Type a message...')
    await waitFor(() => expect(screen.queryByText('Loading available AI systems…')).not.toBeInTheDocument())

    // "Select an AI provider..." now legitimately renders in two places at
    // once — the composer banner and this scan-settled announcement — so a
    // bare getByText would be ambiguous; the region check below is the one
    // that matters for this test.
    const region = liveRegion()
    await waitFor(() => expect(region).toHaveTextContent('Select an AI provider above to start chatting'))

    await act(async () => {
      resolveProviders([{ id: 1, name: 'openai', status: 'active', default_model: 'gpt' }])
      await new Promise(r => setTimeout(r, 0))
    })

    await waitFor(() => expect(screen.queryByText('Select an AI provider above to start chatting')).not.toBeInTheDocument())
    expect(region).toHaveTextContent(/you can send/i)
  })
})
