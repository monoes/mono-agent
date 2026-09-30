// @vitest-environment jsdom
// The org stage inside an expanded coder bubble (#228): a running dynamic
// org's journal draws its agents, a click opens the node drawer and narrows
// the chat to that agent, and Esc closes the drawer before the overlay.
import React, { useEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import '../../i18n.js'
import journal from '../../lib/__fixtures__/orgStageJournal.json'

const { api, listeners } = vi.hoisted(() => ({
  listeners: { chat: new Set() },
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
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: vi.fn() }))

import CoderBubbles from './CoderBubbles.jsx'
import { useCoderBubbles } from './useCoderBubbles.js'

function Harness({ open }) {
  const store = useCoderBubbles()
  useEffect(() => { store.openConversation(open) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  return <CoderBubbles store={store} onNavigate={() => {}} />
}

const conv = { id: 'conv-org', cwd: '/w/cache-service', model: 'opus', runtime: 'claude' }
const node = id => document.querySelector(`[data-testid="stage-node"][data-agent="${id}"]`)

beforeEach(() => {
  vi.clearAllMocks()
  Element.prototype.scrollTo = function scrollTo() {}
  listeners.chat.clear()
  localStorage.clear()
  api.coderStatus.mockResolvedValue({ enabled: true, ready: true, workspaceRoot: '/w' })
  api.coderWorkspaceList.mockResolvedValue([])
  api.getAgentRuntimeModels.mockResolvedValue([])
  // A finished turn whose recorded journal the chat reloads.
  api.getChatTurns.mockResolvedValue({ items: [{ id: 'turn-1', prompt: 'Fix the flaky cache test', status: 'completed' }] })
  api.getChatEvents.mockImplementation((c, t, after) => Promise.resolve({ items: journal.filter(e => e.seq > after), hasMore: false }))
})
afterEach(cleanup)

describe('the org stage in a coder bubble', () => {
  it('draws the turn\'s org, opens a node\'s drawer and narrows the chat to it', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(node('w2')).toBeInTheDocument())
    expect(screen.getAllByTestId('stage-node')).toHaveLength(6)
    expect(screen.getAllByTestId('agent-row')).toHaveLength(4)

    fireEvent.click(node('w2'))
    expect(screen.getByTestId('stage-drawer')).toHaveAttribute('data-agent', 'w2')
    expect(screen.getByTestId('chat-agent-filter')).toHaveTextContent('Showing Coder only')
    expect(screen.getAllByTestId('agent-row').map(r => r.dataset.agent)).toEqual(['w2'])
    expect(screen.queryByText('Done: the cache test is fixed.')).toBeNull()

    // A native subagent narrows the chat to the worker that called it.
    fireEvent.click(node('native:w1:t1'))
    expect(screen.getByTestId('stage-drawer')).toHaveAttribute('data-agent', 'native:w1:t1')
    expect(screen.getAllByTestId('agent-row').map(r => r.dataset.agent)).toEqual(['w1'])

    // The lead keeps its own text and none of the worker rows.
    fireEvent.click(node('lead'))
    expect(screen.queryAllByTestId('agent-row')).toHaveLength(0)
    expect(screen.getByText('Done: the cache test is fixed.')).toBeInTheDocument()

    // Esc closes the drawer first, then collapses the overlay.
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByTestId('stage-drawer')).toBeNull()
    expect(screen.queryByTestId('chat-agent-filter')).toBeNull()
    expect(screen.getByTestId('bubble-overlay')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('bubble-overlay')).toBeNull())
  })

  it('narrows only the shown turn: an older turn keeps its own w1 row and the lead\'s text', async () => {
    // Worker ids repeat per turn, so the older turn has a w1 of its own.
    const older = [
      { conversationId: 'conv-org', turnId: 'turn-0', seq: 1, at: '2026-09-30T09:00:00.000Z', type: 'turn.started', payload: {} },
      { conversationId: 'conv-org', turnId: 'turn-0', seq: 2, at: '2026-09-30T09:00:01.000Z', type: 'assistant.delta', payload: { partId: 'p1', text: 'Earlier answer.' } },
      { conversationId: 'conv-org', turnId: 'turn-0', seq: 3, at: '2026-09-30T09:00:02.000Z', type: 'agent.spawned', payload: { agentId: 'w1', role: 'Earlier worker' } },
      { conversationId: 'conv-org', turnId: 'turn-0', seq: 4, at: '2026-09-30T09:00:03.000Z', type: 'turn.finished', payload: { status: 'completed' } },
    ]
    api.getChatTurns.mockResolvedValue({ items: [
      { id: 'turn-1', prompt: 'Fix the flaky cache test', status: 'completed' },
      { id: 'turn-0', prompt: 'Look around', status: 'completed' },
    ] })
    api.getChatEvents.mockImplementation((c, t, after) => Promise.resolve({ items: (t === 'turn-0' ? older : journal).filter(e => e.seq > after), hasMore: false }))
    render(<Harness open={conv} />)
    await waitFor(() => expect(node('w1')).toBeInTheDocument())
    expect(screen.getAllByTestId('agent-row')).toHaveLength(5)
    fireEvent.click(node('w1'))
    const rows = screen.getAllByTestId('agent-row')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Earlier worker')
    expect(rows[1]).toHaveTextContent('Researcher')
    expect(screen.getByText('Earlier answer.')).toBeInTheDocument()
    expect(screen.queryByText('Done: the cache test is fixed.')).toBeNull()
  })

  it('clears the selection when a new turn starts', async () => {
    api.startChatTurn.mockResolvedValue({ ok: true })
    render(<Harness open={conv} />)
    await waitFor(() => expect(node('w2')).toBeInTheDocument())
    fireEvent.click(node('w2'))
    expect(screen.getByTestId('stage-drawer')).toBeInTheDocument()
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'now the docs' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.startChatTurn).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByTestId('stage-drawer')).toBeNull())
    expect(screen.queryByTestId('chat-agent-filter')).toBeNull()
  })

  it('clicking the selected node again closes its drawer', async () => {
    render(<Harness open={conv} />)
    await waitFor(() => expect(node('w1')).toBeInTheDocument())
    fireEvent.click(node('w1'))
    expect(screen.getByTestId('stage-drawer')).toBeInTheDocument()
    fireEvent.click(node('w1'))
    expect(screen.queryByTestId('stage-drawer')).toBeNull()
  })
})
