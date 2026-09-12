// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, fireEvent, act } from '@testing-library/react'
import { ChatComposer } from './ChatComposer.jsx'
import AIChatPanel from '../AIChatPanel.jsx'

// jsdom implements neither — AIChatPanel's scroll handling touches both.
Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const originalInnerWidth = window.innerWidth

afterEach(() => {
  cleanup()
  // The narrow-viewport Escape test mutates window.innerWidth — restore it
  // so later tests in this file don't mount AIChatPanel already believing
  // the viewport is narrow (no resize handle, no expand button).
  window.innerWidth = originalInnerWidth
})

// ── ChatComposer: IME-safe Enter ─────────────────────────────────────────────

describe('ChatComposer', () => {
  it('sends on a plain Enter', () => {
    const onSend = vi.fn()
    render(<ChatComposer value="hi" onChange={() => {}} onSend={onSend} onStop={() => {}} streaming={false} disabled={false} />)
    fireEvent.keyDown(screen.getByPlaceholderText('Type a message...'), { key: 'Enter' })
    expect(onSend).toHaveBeenCalledTimes(1)
  })

  it('never sends while an IME composition is in progress', () => {
    const onSend = vi.fn()
    render(<ChatComposer value="日本語" onChange={() => {}} onSend={onSend} onStop={() => {}} streaming={false} disabled={false} />)
    const textarea = screen.getByPlaceholderText('Type a message...')
    fireEvent.compositionStart(textarea)
    fireEvent.keyDown(textarea, { key: 'Enter', isComposing: true })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('catches the stray keyCode-229 Enter some browsers fire right as composition ends', () => {
    const onSend = vi.fn()
    render(<ChatComposer value="hi" onChange={() => {}} onSend={onSend} onStop={() => {}} streaming={false} disabled={false} />)
    const textarea = screen.getByPlaceholderText('Type a message...')
    fireEvent.compositionStart(textarea)
    fireEvent.compositionEnd(textarea)
    fireEvent.keyDown(textarea, { key: 'Enter', keyCode: 229 })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('sends normally once composition has actually ended', () => {
    const onSend = vi.fn()
    render(<ChatComposer value="hi" onChange={() => {}} onSend={onSend} onStop={() => {}} streaming={false} disabled={false} />)
    const textarea = screen.getByPlaceholderText('Type a message...')
    fireEvent.compositionStart(textarea)
    fireEvent.compositionEnd(textarea)
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(onSend).toHaveBeenCalledTimes(1)
  })

  it('never sends on Shift+Enter — inserts a newline instead', () => {
    const onSend = vi.fn()
    render(<ChatComposer value="hi" onChange={() => {}} onSend={onSend} onStop={() => {}} streaming={false} disabled={false} />)
    fireEvent.keyDown(screen.getByPlaceholderText('Type a message...'), { key: 'Enter', shiftKey: true })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('shows Stop instead of Send while streaming, and it stays clickable', () => {
    const onStop = vi.fn()
    render(<ChatComposer value="" onChange={() => {}} onSend={() => {}} onStop={onStop} streaming={true} disabled={false} />)
    fireEvent.click(screen.getByRole('button', { name: 'Stop generating' }))
    expect(onStop).toHaveBeenCalledTimes(1)
  })

  it('disables Send with an explicit reason when no backend is selected', () => {
    render(<ChatComposer value="hi" onChange={() => {}} onSend={() => {}} onStop={() => {}} streaming={false} disabled={true} disabledReason="Select an agent runtime above to start chatting" />)
    expect(screen.getByText('Select an agent runtime above to start chatting')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled()
  })
})

// ── AIChatPanel: resize, scroll-follow, close/reopen, Escape/focus ──────────
//
// Only enough mocked surface to mount the panel in "providers" mode (no
// runtime scan needed) — resize/scroll/focus/Escape are orthogonal to which
// backend is active.

const createChatConversation2 = vi.fn()
const startChatTurn2 = vi.fn()
// Base default is deliberately {items: []} — every test in this file runs
// providers-only (scanAgentRuntimes resolves no agents), so the bucket
// effect fires exactly once and each artifact test's own .mockResolvedValueOnce
// covers that one call. If a future test adds a runtime here, a second
// bucket call would fall through to this base mock and wipe `messages`
// (the Task 4 bucket-switch else-branch) — reseed both calls if so.
const listChatConversations2 = vi.fn().mockResolvedValue({ items: [] })
const getChatTurns2 = vi.fn().mockResolvedValue({ items: [] })
const getChatEvents2 = vi.fn().mockResolvedValue({ items: [], hasMore: false })
const listOrgDesigns2 = vi.fn().mockResolvedValue(null)
const listProfileDocuments2 = vi.fn().mockResolvedValue([])
const getProfileDocument2 = vi.fn().mockResolvedValue(null)
const getWorkflow2 = vi.fn().mockResolvedValue(null)
const stopChatTurn2 = vi.fn().mockResolvedValue({ ok: true })

vi.mock('../../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    api: {
      ...actual.api,
      scanAgentRuntimes: vi.fn().mockResolvedValue({ agents: [] }),
      listAIProviders: vi.fn().mockResolvedValue([{ id: 1, name: 'openai', status: 'active', default_model: 'gpt' }]),
      listChatConversations: (...args) => listChatConversations2(...args),
      createChatConversation: (...args) => createChatConversation2(...args),
      startChatTurn: (...args) => startChatTurn2(...args),
      getChatTurns: (...args) => getChatTurns2(...args),
      getChatEvents: (...args) => getChatEvents2(...args),
      stopChatTurn: (...args) => stopChatTurn2(...args),
      listOrgDesigns: (...args) => listOrgDesigns2(...args),
      listProfileDocuments: (...args) => listProfileDocuments2(...args),
      getProfileDocument: (...args) => getProfileDocument2(...args),
      getWorkflow: (...args) => getWorkflow2(...args),
    },
  }
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('AIChatPanel stop() error handling', () => {
  it('recovers from a failed stopChatTurn instead of stranding the UI on "Stopping" forever', async () => {
    createChatConversation2.mockResolvedValue({ id: 'conv-4', backend: 'provider' })
    startChatTurn2.mockResolvedValue({ ok: true, turnId: 'ignored', status: 'active' })
    // stopChatTurn goes through parseStreamResult, which throws on this
    // exact {"error":...} shape (same convention every other chat binding
    // uses for a synchronous failure).
    stopChatTurn2.mockRejectedValueOnce(new Error('chat supervisor not initialized'))

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    const textarea = await screen.findByPlaceholderText('Type a message...')
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop generating' })).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Stop generating' }))
    await waitFor(() => expect(stopChatTurn2).toHaveBeenCalled())

    // Without the fix, stopRequested is stuck true forever (activeTurnId
    // only clears via liveTurn.terminal, which a failed stop call never
    // produces) — the status line is permanently stuck on "Stopping" with
    // no way to retry. With the fix, it resets so the turn is still
    // visibly running and stoppable again.
    await waitFor(() => expect(screen.queryByText('Stopping')).not.toBeInTheDocument())
  })
})

describe('AIChatPanel resize/scroll/focus/Escape', () => {
  it('dragging the resize handle changes the panel width without touching an in-flight turn', async () => {
    createChatConversation2.mockResolvedValue({ id: 'conv-1', backend: 'provider' })
    startChatTurn2.mockResolvedValue({ ok: true, turnId: 'ignored', status: 'active' })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    const textarea = await screen.findByPlaceholderText('Type a message...')

    // Start a turn so we can prove resizing doesn't disturb it.
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop generating' })).toBeInTheDocument())

    const handle = screen.getByTitle('Drag to resize')
    const panel = handle.parentElement
    const initialWidth = panel.style.width
    fireEvent.mouseDown(handle, { clientX: 400 })
    // Body-level userSelect/cursor are suppressed for the drag's duration so
    // dragging over the transcript (or the rest of the page) doesn't fight
    // the resize with a native text selection.
    expect(document.body.style.userSelect).toBe('none')
    fireEvent.mouseMove(window, { clientX: 300 }) // dragged left -> wider
    fireEvent.mouseUp(window)

    expect(document.body.style.userSelect).not.toBe('none')
    expect(panel.style.width).not.toBe(initialWidth)
    // The turn is still running — resizing is a pure layout operation.
    expect(screen.getByRole('button', { name: 'Stop generating' })).toBeInTheDocument()
  })

  it('shows Jump to latest once new content arrives while the reader has scrolled away, and returns to following on click', async () => {
    createChatConversation2.mockResolvedValue({ id: 'conv-1', backend: 'provider' })
    startChatTurn2.mockResolvedValue({ ok: true, turnId: 'ignored', status: 'active' })

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    const textarea = await screen.findByPlaceholderText('Type a message...')

    // Scroll away from the bottom FIRST — before any content is added —
    // so the message this test sends next is genuinely "new content that
    // arrives while not following", not content added before the reader
    // ever scrolled.
    const scrollContainer = document.querySelector('[style*="overflow-y: auto"]')
    Object.defineProperty(scrollContainer, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(scrollContainer, 'clientHeight', { value: 500, configurable: true })
    Object.defineProperty(scrollContainer, 'scrollTop', { value: 100, writable: true, configurable: true }) // far from bottom
    fireEvent.scroll(scrollContainer)

    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(startChatTurn2).toHaveBeenCalled())

    expect(await screen.findByText(/Jump to latest/)).toBeInTheDocument()
    fireEvent.click(screen.getByText(/Jump to latest/))
    await waitFor(() => expect(screen.queryByText(/Jump to latest/)).not.toBeInTheDocument())
  })

  it('preserves the draft and transcript across close/reopen (no remount — same component instance)', async () => {
    const { rerender } = render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    const textarea = await screen.findByPlaceholderText('Type a message...')
    fireEvent.change(textarea, { target: { value: 'unsent draft' } })

    rerender(<AIChatPanel workflowID="general" isOpen={false} onClose={() => {}} />)
    expect(screen.queryByPlaceholderText('Type a message...')).not.toBeInTheDocument()

    rerender(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    expect(screen.getByPlaceholderText('Type a message...')).toHaveValue('unsent draft')
  })

  it('restores focus to whatever opened the panel once it closes', async () => {
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    expect(document.activeElement).toBe(opener)

    const { rerender } = render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await screen.findByPlaceholderText('Type a message...')

    // Simulate the user actually interacting inside the panel (opening it
    // alone doesn't move focus — this plan only asks for focus *return* on
    // close, not a focus trap on open).
    screen.getByPlaceholderText('Type a message...').focus()

    rerender(<AIChatPanel workflowID="general" isOpen={false} onClose={() => {}} />)
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('Escape collapses an expanded view first, and only closes when already docked', async () => {
    const onClose = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen={true} onClose={onClose} />)
    await screen.findByPlaceholderText('Type a message...')

    fireEvent.click(screen.getByTitle('Expand'))
    expect(screen.getByTitle('Collapse')).toBeInTheDocument()

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTitle('Expand')).toBeInTheDocument() // back to docked

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('Escape closes immediately when narrow, even if presentation is still "expanded" from a wider viewport', async () => {
    // The expand/collapse toggle is hidden once narrow (no room for it), so
    // a panel left "expanded" before the viewport shrank would otherwise be
    // unreachable by Escape — it would only collapse to a state with no
    // visible control to get out of, trapping the user behind the
    // full-viewport overlay with just the header's X button left.
    const onClose = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen={true} onClose={onClose} />)
    await screen.findByPlaceholderText('Type a message...')

    fireEvent.click(screen.getByTitle('Expand'))
    expect(screen.getByTitle('Collapse')).toBeInTheDocument()

    window.innerWidth = 480
    fireEvent(window, new Event('resize'))
    await waitFor(() => expect(screen.queryByTitle('Collapse')).not.toBeInTheDocument())

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

// ── Chat result artifacts (Task 6) ──────────────────────────────────────────
//
// chatArtifacts.test.js already covers detectArtifactCandidate/resolveArtifact
// exhaustively at the unit level (forged/malformed/missing/cross-profile
// inputs). What that file CANNOT prove is that AIChatPanel's own wiring
// actually calls them and renders the result — these two tests drive a
// real past turn through loadConversation (mocked getChatTurns/getChatEvents,
// same as production) to exercise that wiring end to end.

function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}

function turnEvents(callName, resultObj) {
  return [
    { seq: 1, at: '2026-09-12T00:00:00Z', type: 'turn.started', payload: {} },
    { seq: 2, at: '2026-09-12T00:00:01Z', type: 'tool.started', payload: { callId: 'call-1', name: callName, arguments: {} } },
    { seq: 3, at: '2026-09-12T00:00:02Z', type: 'tool.completed', payload: { callId: 'call-1', ok: true, result: JSON.stringify(resultObj) } },
    { seq: 4, at: '2026-09-12T00:00:03Z', type: 'turn.finished', payload: { status: 'completed', reason: '', exitCode: 0, historySaved: true } },
  ]
}

describe('AIChatPanel chat result artifacts', () => {
  it('renders an Open action for a create_org result once the name is confirmed against the real org listing, and wires it to onOpenArtifact', async () => {
    listChatConversations2.mockResolvedValueOnce({ items: [{ id: 'conv-1', backend: 'provider', workflowContext: 'general', runtimeId: '', model: '', updatedAt: '2026-09-12T00:00:00Z' }] })
    getChatTurns2.mockResolvedValueOnce({ items: [{ id: 'turn-1', prompt: 'create an org called Acme', status: 'completed' }] })
    getChatEvents2.mockResolvedValueOnce({ items: turnEvents('create_org', { org_name: 'Acme', created: true }), hasMore: false })
    // Two calls expected: the initial resolve that builds the card, and the
    // click-time re-validation that must also find it still there.
    listOrgDesigns2
      .mockResolvedValueOnce([{ name: 'Acme' }])
      .mockResolvedValueOnce([{ name: 'Acme' }])

    const onOpenArtifact = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} onOpenArtifact={onOpenArtifact} />)

    const openBtn = await screen.findByTitle('Open organization')
    fireEvent.click(openBtn)
    await waitFor(() => expect(onOpenArtifact).toHaveBeenCalledWith({ type: 'org', name: 'Acme' }))
  })

  it('renders no action for a save_document result whose vault id no longer resolves (deleted/cross-profile), leaving the generic tool card as the only output', async () => {
    listChatConversations2.mockResolvedValueOnce({ items: [{ id: 'conv-2', backend: 'provider', workflowContext: 'general', runtimeId: '', model: '', updatedAt: '2026-09-12T00:00:00Z' }] })
    getChatTurns2.mockResolvedValueOnce({ items: [{ id: 'turn-2', prompt: 'save a report', status: 'completed' }] })
    getChatEvents2.mockResolvedValueOnce({
      items: turnEvents('save_document', { filename: 'report.md', path: '/x/report.md', size_bytes: 10, vault_document_id: 'doc-999' }),
      hasMore: false,
    })
    getProfileDocument2.mockResolvedValueOnce(null) // doc-999 no longer resolves (deleted/cross-profile)

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)

    // The generic ToolActivityCard renders unconditionally, synchronously —
    // proves the turn actually loaded before asserting on the async part.
    await screen.findByText('save_document')
    await waitFor(() => expect(getProfileDocument2).toHaveBeenCalledWith('doc-999'))
    expect(screen.queryByTitle('Open document')).not.toBeInTheDocument()
  })

  it('keys the resolved-artifact cache by (turnId, callId), not callId alone — two turns reusing the same callId must not share a cached artifact', async () => {
    // The agent backend's callId is passed straight through from the
    // external monomind protocol's own per-event id with no cross-turn
    // uniqueness guarantee (plan: tool identity is (turnId,callId), never
    // callId alone) — turnEvents() below hardcodes callId 'call-1' for
    // every turn, so both turns here collide on purpose.
    listChatConversations2.mockResolvedValueOnce({ items: [{ id: 'conv-3', backend: 'provider', workflowContext: 'general', runtimeId: '', model: '', updatedAt: '2026-09-12T00:00:00Z' }] })
    getChatTurns2.mockResolvedValueOnce({
      items: [
        { id: 'turn-a', prompt: 'make a workflow', status: 'completed' },
        { id: 'turn-b', prompt: 'make an org', status: 'completed' },
      ],
    })
    // loadConversation processes turns oldest-first via .slice().reverse(),
    // so turn-b ("make an org") is fetched before turn-a ("make a
    // workflow") — these are queued in THAT order so each turn's events
    // actually match its own prompt.
    getChatEvents2
      .mockResolvedValueOnce({ items: turnEvents('create_org', { org_name: 'SecondTurnOrg', created: true }), hasMore: false })
      .mockResolvedValueOnce({ items: turnEvents('create_workflow', { workflow_id: 'wf-shared-id' }), hasMore: false })
    getWorkflow2.mockResolvedValueOnce({ id: 'wf-shared-id', name: 'First Turn Workflow' })
    listOrgDesigns2.mockResolvedValueOnce([{ name: 'SecondTurnOrg' }])

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)

    // Both artifact cards must appear, correctly attributed to their own
    // turn — not one card overwriting or hiding the other via a colliding
    // cache key.
    await screen.findByTitle('Copy workflow ID')
    await screen.findByTitle('Open organization')
    expect(screen.getByText('First Turn Workflow')).toBeInTheDocument()
    expect(screen.getByText('SecondTurnOrg')).toBeInTheDocument()
  })

  it('re-validates at click time — a card that resolved successfully but whose org was since deleted refuses to open', async () => {
    listChatConversations2.mockResolvedValueOnce({ items: [{ id: 'conv-5', backend: 'provider', workflowContext: 'general', runtimeId: '', model: '', updatedAt: '2026-09-12T00:00:00Z' }] })
    getChatTurns2.mockResolvedValueOnce({ items: [{ id: 'turn-5', prompt: 'create an org called Acme', status: 'completed' }] })
    getChatEvents2.mockResolvedValueOnce({ items: turnEvents('create_org', { org_name: 'Acme', created: true }), hasMore: false })
    // First call (initial resolve, while building the card) finds it;
    // second call (re-validation on click, sometime later) no longer does —
    // simulates the org being deleted in between.
    listOrgDesigns2
      .mockResolvedValueOnce([{ name: 'Acme' }])
      .mockResolvedValueOnce([])

    const onOpenArtifact = vi.fn()
    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} onOpenArtifact={onOpenArtifact} />)

    const openBtn = await screen.findByTitle('Open organization')
    fireEvent.click(openBtn)

    // The click must trigger a fresh lookup, not just replay the cached
    // resolution from when the card first appeared.
    await waitFor(() => expect(listOrgDesigns2).toHaveBeenCalledTimes(2))
    expect(onOpenArtifact).not.toHaveBeenCalled()
  })
})

// ── loadConversation staleness guard ────────────────────────────────────────
//
// A slow, superseded load must never clobber a newer one's transcript (plan:
// "Guard asynchronous loads with a conversation generation token"). This is
// a genuinely reachable UI race, not a synthetic one: the panel's own
// bucket-auto-continue effect calls loadConversation(items[0]) on mount, and
// a user opening the sessions dropdown and picking a DIFFERENT conversation
// before that first fetch finishes is a completely ordinary interaction.
describe('AIChatPanel loadConversation race', () => {
  it('a later click wins even if its fetch resolves before an earlier, still-in-flight one', async () => {
    const convA = { id: 'conv-a', backend: 'provider', workflowContext: 'general', runtimeId: '', model: 'model-a', updatedAt: '2026-09-12T00:00:00.000Z' }
    const convB = { id: 'conv-b', backend: 'provider', workflowContext: 'general', runtimeId: '', model: 'model-b', updatedAt: '2026-09-12T00:00:01.000Z' }
    // items[0] (convA) auto-continues on mount via the bucket effect.
    listChatConversations2.mockResolvedValueOnce({ items: [convA, convB] })

    const turnsA = deferred()
    const turnsB = deferred()
    getChatTurns2.mockImplementation((convId) => (convId === 'conv-a' ? turnsA.promise : turnsB.promise))
    getChatEvents2.mockImplementation((_convId, turnId) => Promise.resolve({
      items: turnEvents('create_workflow', { workflow_id: `wf-${turnId}` }),
      hasMore: false,
    }))

    render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)

    // Auto-continue has already called loadConversation(convA) — its
    // getChatTurns is pending on turnsA. Switch to convB (a later click)
    // before A resolves.
    fireEvent.click(await screen.findByTitle('Past sessions'))
    fireEvent.click(await screen.findByText('model-b'))

    // Resolve out of click order on purpose — B (the later click) finishes
    // first, A (the earlier, now-superseded click) finishes last. Without
    // the generation guard, A's late arrival overwrites B's transcript.
    turnsB.resolve({ items: [{ id: 'turn-b', prompt: 'from B', status: 'completed' }] })
    await screen.findByText('from B')
    turnsA.resolve({ items: [{ id: 'turn-a', prompt: 'from A', status: 'completed' }] })
    // Give A's now-superseded chain a chance to (wrongly, if unguarded) land.
    await new Promise(r => setTimeout(r, 0))

    expect(screen.getByText('from B')).toBeInTheDocument()
    expect(screen.queryByText('from A')).not.toBeInTheDocument()

    // Restore the shared mocks' plain default behavior for hygiene — this
    // test is the only one in the file using .mockImplementation on these.
    getChatTurns2.mockReset().mockResolvedValue({ items: [] })
    getChatEvents2.mockReset().mockResolvedValue({ items: [], hasMore: false })
  })
})

// ── Bucket-switch history fetch: retry after failure ────────────────────────
//
// conversationsFetchedRef marks a bucket "fetched" synchronously, before the
// request it guards even resolves — so a failure must not leave that mark in
// place, or the bucket becomes permanently stuck: same workflowID/useAgents,
// same bucket string, so a plain close/reopen of the panel (isOpen only)
// never re-triggers the effect's fetch again once the ref already matches.
describe('AIChatPanel bucket-switch history fetch', () => {
  it('retries on close/reopen after a failed fetch, instead of leaving the bucket stuck', async () => {
    listChatConversations2.mockRejectedValueOnce(new Error('backend unavailable'))
    listChatConversations2.mockResolvedValueOnce({
      items: [{ id: 'conv-retry', backend: 'provider', workflowContext: 'general', runtimeId: '', model: 'retried-model', updatedAt: '2026-09-12T00:00:00Z' }],
    })
    getChatTurns2.mockResolvedValue({ items: [] })

    const { rerender } = render(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)
    await waitFor(() => expect(listChatConversations2).toHaveBeenCalledTimes(1))

    // Close and reopen the SAME bucket (workflowID/useAgents unchanged) —
    // the only user action a stuck ref would make unrecoverable, since
    // isOpen toggling is the one thing that still re-runs this effect.
    rerender(<AIChatPanel workflowID="general" isOpen={false} onClose={() => {}} />)
    rerender(<AIChatPanel workflowID="general" isOpen={true} onClose={() => {}} />)

    await waitFor(() => expect(listChatConversations2).toHaveBeenCalledTimes(2))
    // Proves the retry's result was actually used (loadConversation ran),
    // not just that a second HTTP-ish call happened.
    await waitFor(() => expect(getChatTurns2).toHaveBeenCalledWith('conv-retry', '', 50))
  })

  // The .catch() above has always had this staleness check; the .then()
  // success path did not — a late-resolving fetch for a bucket the UI no
  // longer shows could call loadConversation (or clear state) and stomp
  // whatever the newer, already-current bucket had just loaded.
  it('a stale bucket fetch resolving after a newer bucket already loaded does not overwrite it', async () => {
    const bucketA = deferred()
    listChatConversations2.mockImplementationOnce(() => bucketA.promise) // wf-a's fetch — stays pending
    listChatConversations2.mockResolvedValueOnce({
      items: [{ id: 'conv-b', backend: 'provider', workflowContext: 'wf-b', runtimeId: '', model: 'model-b', updatedAt: '2026-09-12T00:00:01Z' }],
    })
    getChatTurns2.mockImplementation((convId) => Promise.resolve({
      items: [{ id: `turn-${convId}`, prompt: convId === 'conv-a' ? 'from-A' : 'from-B', status: 'completed' }],
    }))
    getChatEvents2.mockResolvedValue({ items: [], hasMore: false })

    const { rerender } = render(<AIChatPanel workflowID="wf-a" isOpen={true} onClose={() => {}} />)
    await waitFor(() => expect(listChatConversations2).toHaveBeenCalledTimes(1))

    // Switch buckets (a new workflowID, same shape of change a quick
    // agents<->providers toggle produces) before wf-a's fetch resolves.
    rerender(<AIChatPanel workflowID="wf-b" isOpen={true} onClose={() => {}} />)
    await waitFor(() => expect(listChatConversations2).toHaveBeenCalledTimes(2))
    await screen.findByText('from-B')

    // The stale wf-a fetch finally resolves, with a conversation for the
    // bucket the UI no longer shows.
    await act(async () => {
      bucketA.resolve({ items: [{ id: 'conv-a', backend: 'provider', workflowContext: 'wf-a', runtimeId: '', model: 'model-a', updatedAt: '2026-09-12T00:00:00Z' }] })
      await new Promise(r => setTimeout(r, 0))
    })

    expect(screen.getByText('from-B')).toBeInTheDocument()
    expect(screen.queryByText('from-A')).not.toBeInTheDocument()

    getChatTurns2.mockReset().mockResolvedValue({ items: [] })
    getChatEvents2.mockReset().mockResolvedValue({ items: [], hasMore: false })
  })
})
