// @vitest-environment jsdom
// C-35: messages queued for a stopped org are listed read-only, with a
// "Start org now" action that delegates to the org view's existing run path.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import QueuedMessagesPanel from './QueuedMessagesPanel.jsx'
import { api } from '../../services/api.js'
import en from '../../locales/en.json'
import es from '../../locales/es.json'

vi.mock('../../services/api.js', () => ({
  api: { listOrgQueuedMessages: vi.fn(), getOrgStatus: vi.fn() },
  notify: vi.fn(),
}))

// Same convention as Sidebar.render.test.jsx: assert raw keys; interpolated
// values are appended so tests can still see them.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, vars) => (vars ? `${key} ${Object.values(vars).join(' ')}` : key),
  }),
}))

const NOW = Date.now()
const QUEUED = {
  v: 1, org: 'growth', count: 2, skipped: 1, delivery: 'at next start',
  messages: [
    { messageId: 'msg-1', from: 'hq:ceo', to: 'lead', subject: 'Q3 plan', body: 'Start with the EU market.', ts: NOW - 5 * 60_000, endpoint: false, trace: { chain_id: 'chn_a', hop: 2 } },
    { messageId: 'msg-2', from: 'workflow:ex1', to: 'publisher-bot', subject: 'post', body: 'hello', ts: NOW - 60_000, endpoint: true, draining: true },
  ],
}

beforeEach(() => {
  vi.clearAllMocks()
  api.listOrgQueuedMessages.mockResolvedValue(QUEUED)
  api.getOrgStatus.mockResolvedValue({ status: 'stopped' })
})
afterEach(() => cleanup())

describe('QueuedMessagesPanel', () => {
  it('lists each queued message with sender, recipient, subject, and body', async () => {
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={vi.fn()} />)
    const items = await screen.findAllByTestId('queued-message')
    expect(items).toHaveLength(2)
    expect(api.listOrgQueuedMessages).toHaveBeenCalledWith('growth')
    expect(items[0]).toHaveTextContent('hq:ceo')
    expect(items[0]).toHaveTextContent('lead')
    expect(items[0]).toHaveTextContent('Q3 plan')
    expect(items[0]).toHaveTextContent('Start with the EU market.')
    expect(items[0]).toHaveTextContent('orgs.queued.hop 2')
    expect(items[1]).toHaveTextContent('orgs.queued.endpoint')
    expect(items[1]).toHaveTextContent('orgs.queued.draining')
    expect(screen.getByText(/orgs\.queued\.skipped 1/)).toBeInTheDocument()
    expect(screen.getByText(/orgs\.queued\.deliveredAtStart/)).toBeInTheDocument()
  })

  it('starts the org through the provided start path', async () => {
    const onStart = vi.fn()
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={onStart} />)
    const btn = await screen.findByRole('button', { name: /orgs\.queued\.startNow/ })
    expect(btn).toBeEnabled()
    fireEvent.click(btn)
    expect(onStart).toHaveBeenCalledTimes(1)
  })

  it('disables the start action while the org is running', async () => {
    api.getOrgStatus.mockResolvedValue({ status: 'running' })
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={vi.fn()} />)
    await screen.findAllByTestId('queued-message')
    await waitFor(() => expect(screen.getByRole('button', { name: /orgs\.queued\.startNow/ })).toBeDisabled())
    expect(screen.getByText('orgs.queued.runningNote')).toBeInTheDocument()
  })

  it('shows the empty state and no start action when nothing is queued', async () => {
    api.listOrgQueuedMessages.mockResolvedValue({ v: 1, org: 'growth', count: 0, skipped: 0, messages: [] })
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={vi.fn()} />)
    expect(await screen.findByText('orgs.queued.empty')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /orgs\.queued\.startNow/ })).toBeNull()
  })

  it('reports how many messages it read, for the tab badge', async () => {
    const onCountChange = vi.fn()
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={vi.fn()} onCountChange={onCountChange} />)
    await waitFor(() => expect(onCountChange).toHaveBeenCalledWith(2))
  })

  it('shows the CLI error', async () => {
    api.listOrgQueuedMessages.mockResolvedValue({ error: 'org "growth" not found' })
    render(<QueuedMessagesPanel orgName="growth" onStartOrg={vi.fn()} />)
    expect(await screen.findByText(/org "growth" not found/)).toBeInTheDocument()
  })

  it('has every string in both locales', () => {
    const keys = ['title', 'deliveredAtStart', 'startNow', 'starting', 'runningNote', 'empty', 'refresh', 'endpoint', 'draining', 'hop', 'skipped', 'waited', 'loadFailed', 'to', 'noSubject']
    for (const k of keys) {
      expect(en.orgs.queued[k], `en ${k}`).toBeTruthy()
      expect(es.orgs.queued[k], `es ${k}`).toBeTruthy()
    }
  })
})
