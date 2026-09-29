// @vitest-environment jsdom
import React, { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, act, within } from '@testing-library/react'
import '../../i18n.js'

const { api, listeners, mockConfirm } = vi.hoisted(() => ({
  listeners: { chat: new Set() },
  mockConfirm: vi.fn(),
  api: {
    getChatTurns: vi.fn(),
    getChatEvents: vi.fn(),
    startChatTurn: vi.fn(),
    stopChatTurn: vi.fn(),
    coderStatus: vi.fn(),
    coderWorkspaceList: vi.fn(),
    coderWorkspaceRoot: vi.fn(),
    createCoderConversation: vi.fn(),
    getAgentRuntimeModels: vi.fn(),
    pickCoderFolder: vi.fn(),
    openPathWithOS: vi.fn(),
  },
}))
vi.mock('../../services/api.js', () => ({
  api,
  notify: vi.fn(),
  onChatEvent: cb => { listeners.chat.add(cb); return () => listeners.chat.delete(cb) },
}))
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))

import CoderBubbles from './CoderBubbles.jsx'
import { useCoderBubbles } from './useCoderBubbles.js'

const emit = ev => act(() => { for (const cb of [...listeners.chat]) cb(ev) })

let store
function Harness({ open }) {
  store = useCoderBubbles()
  useEffect(() => { if (open) store.openConversation(open) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  return <CoderBubbles store={store} onNavigate={() => {}} />
}

const conv = { id: 'c1', cwd: '/home/u/monoagent-coder/20260929-brisk-otter', model: 'opus' }

beforeEach(() => {
  vi.clearAllMocks()
  // jsdom has no scrolling; the transcript follows new output with it.
  Element.prototype.scrollTo = function scrollTo() {}
  listeners.chat.clear()
  localStorage.clear()
  api.getChatTurns.mockResolvedValue({ items: [] })
  api.getChatEvents.mockResolvedValue({ items: [], hasMore: false })
  api.coderStatus.mockResolvedValue({ enabled: true, ready: true, workspaceRoot: '/home/u/monoagent-coder' })
  api.coderWorkspaceList.mockResolvedValue([])
  api.getAgentRuntimeModels.mockResolvedValue([{ id: 'opus', label: 'Opus 5.5', effort_levels: ['low', 'high'] }])
  api.startChatTurn.mockResolvedValue({ ok: true })
  api.stopChatTurn.mockResolvedValue({ ok: true })
})
afterEach(cleanup)

const overlay = () => screen.queryByTestId('bubble-overlay')
const backdrop = () => screen.getByTestId('bubble-backdrop')
const bubble = key => document.querySelector(`[data-bubble="${key}"]`)

describe('coder bubbles', () => {
  it('opens a conversation expanded, and a backdrop click collapses it back into its bubble', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(overlay()).toBeInTheDocument())
    expect(bubble('c1')).toHaveTextContent('BO')
    fireEvent.pointerDown(backdrop())
    fireEvent.click(backdrop())
    await waitFor(() => expect(overlay()).toBeNull())
    expect(bubble('c1')).toBeInTheDocument()
    expect(api.stopChatTurn).not.toHaveBeenCalled()
  })

  it('does not collapse for a drag that started inside, a text selection, or an open dialog', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(overlay()).toBeInTheDocument())

    // A press inside the chat that ends on the backdrop (drag / select).
    fireEvent.pointerDown(overlay())
    fireEvent.click(backdrop())
    expect(overlay()).toBeInTheDocument()

    // Text selected somewhere.
    const sel = vi.spyOn(window, 'getSelection').mockReturnValue({ toString: () => 'some text' })
    fireEvent.pointerDown(backdrop())
    fireEvent.click(backdrop())
    expect(overlay()).toBeInTheDocument()
    sel.mockRestore()

    // A modal dialog on top.
    const modal = document.createElement('div')
    modal.setAttribute('aria-modal', 'true')
    document.body.appendChild(modal)
    fireEvent.pointerDown(backdrop())
    fireEvent.click(backdrop())
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(overlay()).toBeInTheDocument()
    modal.remove()

    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(overlay()).toBeNull())
  })

  it('clicking the bubble toggles, and another bubble switches without closing', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(overlay()).toBeInTheDocument())
    act(() => { store.openConversation({ id: 'c2', cwd: '/w/mono-agent', model: 'sonnet' }) })
    await waitFor(() => expect(screen.getByTestId('bubble-overlay')).toHaveAttribute('aria-label', 'Coder chat: mono-agent'))
    fireEvent.click(bubble('c1'))
    await waitFor(() => expect(screen.getByTestId('bubble-overlay')).toHaveAttribute('aria-label', 'Coder chat: 20260929-brisk-otter'))
    fireEvent.click(bubble('c1'))
    await waitFor(() => expect(overlay()).toBeNull())
  })

  it('re-attaches to a running turn, keeps it running when collapsed, and counts the finish as unread', async () => {
    api.getChatTurns.mockResolvedValue({ items: [{ id: 't1', prompt: 'fix the build', status: 'active', ownedByThisInstance: true }] })
    render(<Harness open={conv} />)
    await waitFor(() => expect(screen.getByText('fix the build')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByTestId('stage-lead')).toHaveAttribute('data-working', 'true'))
    expect(bubble('c1')).toHaveAttribute('data-status', 'working')

    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(overlay()).toBeNull())
    expect(api.stopChatTurn).not.toHaveBeenCalled()

    emit({ conversationId: 'c1', turnId: 't1', seq: 5, type: 'turn.finished', payload: { status: 'completed' } })
    expect(bubble('c1')).toHaveAttribute('data-status', 'done')
    expect(bubble('c1').querySelector('.bubble-badge')).toHaveTextContent('1')
    fireEvent.click(bubble('c1'))
    await waitFor(() => expect(overlay()).toBeInTheDocument())
    expect(bubble('c1').querySelector('.bubble-badge')).toBeNull()
  })

  it('asks before closing a working chat, and stops its turn', async () => {
    api.getChatTurns.mockResolvedValue({ items: [{ id: 't1', prompt: 'go', status: 'active' }] })
    render(<Harness open={conv} />)
    await waitFor(() => expect(bubble('c1')).toHaveAttribute('data-status', 'working'))

    mockConfirm.mockResolvedValueOnce(false)
    fireEvent.click(screen.getByLabelText('Close 20260929-brisk-otter'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
    expect(bubble('c1')).toBeInTheDocument()
    expect(api.stopChatTurn).not.toHaveBeenCalled()

    mockConfirm.mockResolvedValueOnce(true)
    fireEvent.click(screen.getByLabelText('Close 20260929-brisk-otter'))
    await waitFor(() => expect(bubble('c1')).toBeNull())
    expect(api.stopChatTurn).toHaveBeenCalledWith('c1', 't1')
  })

  it('closes an idle chat without asking', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(bubble('c1')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Close 20260929-brisk-otter'))
    await waitFor(() => expect(bubble('c1')).toBeNull())
    expect(mockConfirm).not.toHaveBeenCalled()
  })

  it('starts a new coder chat from +, creates the conversation on the first message and keeps the bubble', async () => {
    api.coderWorkspaceRoot.mockResolvedValue({ path: '/home/u/monoagent-coder', created: false })
    api.createCoderConversation.mockResolvedValue({ id: 'c9', cwd: '/home/u/monoagent-coder', model: 'opus' })
    render(<Harness />)
    fireEvent.click(await screen.findByLabelText('New coder chat'))
    await waitFor(() => expect(screen.getByTestId('coder-setup')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByLabelText('Model')).toHaveValue('opus'))
    const box = within(overlay()).getByPlaceholderText('Type a message...')
    fireEvent.change(box, { target: { value: 'add a README' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.createCoderConversation).toHaveBeenCalledWith('claude', 'opus', '/home/u/monoagent-coder', false))
    await waitFor(() => expect(api.startChatTurn).toHaveBeenCalledWith('c9', expect.any(String), 'add a README', false, false))
    await waitFor(() => expect(store.bubbles[0].conversationId).toBe('c9'))
    expect(JSON.parse(localStorage.getItem('monoagent:coderBubbles:v1')).bubbles[0].conversationId).toBe('c9')
  })

  it('keeps the unsent draft across a collapse', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(overlay()).toBeInTheDocument())
    await waitFor(() => expect(within(overlay()).getByPlaceholderText('Type a message...')).not.toBeDisabled())
    fireEvent.change(within(overlay()).getByPlaceholderText('Type a message...'), { target: { value: 'half a thought' } })
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(overlay()).toBeNull())
    fireEvent.click(bubble('c1'))
    await waitFor(() => expect(within(overlay()).getByPlaceholderText('Type a message...')).toHaveValue('half a thought'))
  })

  it('hides + when coder mode is off and nothing is open', async () => {
    api.coderStatus.mockResolvedValue({ enabled: false })
    render(<Harness />)
    await waitFor(() => expect(api.coderStatus).toHaveBeenCalled())
    expect(screen.queryByLabelText('New coder chat')).toBeNull()
    expect(screen.queryByTestId('bubble-dock')).toBeNull()
  })

  it('restores bubbles from a previous session', async () => {
    localStorage.setItem('monoagent:coderBubbles:v1', JSON.stringify({ side: 'left', bubbles: [{ conversationId: 'c1', cwd: '/w/api', model: 'opus' }] }))
    api.getChatTurns.mockResolvedValue({ items: [{ id: 't1', status: 'failed' }] })
    render(<Harness />)
    await waitFor(() => expect(bubble('c1')).toHaveAttribute('data-status', 'error'))
    expect(screen.getByTestId('bubble-dock')).toHaveClass('left')
    expect(overlay()).toBeNull()
  })
})

describe('empty new chats', () => {
  it('discards a new chat collapsed with nothing typed, and keeps one with a draft', async () => {
    render(<Harness />)
    fireEvent.click(await screen.findByLabelText('New coder chat'))
    await waitFor(() => expect(overlay()).toBeInTheDocument())
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(overlay()).toBeNull())
    expect(store.bubbles).toHaveLength(0)

    fireEvent.click(screen.getByLabelText('New coder chat'))
    await waitFor(() => expect(screen.getByLabelText('Model')).toHaveValue('opus'))
    fireEvent.change(within(overlay()).getByPlaceholderText('Type a message...'), { target: { value: 'later' } })
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(overlay()).toBeNull())
    expect(store.bubbles).toHaveLength(1)
  })
})
