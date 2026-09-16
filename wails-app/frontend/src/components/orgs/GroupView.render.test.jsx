// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import GroupView from './GroupView.jsx'
import { initialGroupTraffic, applyGroupEvent, recentArcs, arcPath } from './groupTraffic.js'
import { api, notify, onOrgEvent } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'

vi.mock('../../services/api.js', () => ({
  api: {
    orgGroupStatus: vi.fn(),
    startOrgGroup: vi.fn(),
    stopOrgGroup: vi.fn(),
    streamOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
    stopOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
  },
  notify: vi.fn(),
  onOrgEvent: vi.fn(() => () => {}),
}))
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }))

// jsdom rewrites import.meta.url, so resolve from the vitest root instead.
function fixture(name) {
  const path = join(process.cwd(), 'src/components/orgdesigner/__fixtures__', `${name}.jsonl`)
  return readFileSync(path, 'utf8').split('\n').filter(Boolean).map(l => JSON.parse(l))
}

const GROUP = {
  v: 1, holding: 'herald', rollup_usd: 1.14,
  children: [
    { org: 'anvil', status: 'running', run: 'run-a', start: 'on_demand', budget_share: 0.5, cost_usd: 0.44 },
    { org: 'forge', status: 'stopped', run: null, start: 'with_parent', budget_share: 0.25, cost_usd: 0.56 },
  ],
}

let emit = null
beforeEach(() => {
  vi.clearAllMocks()
  api.orgGroupStatus.mockResolvedValue(GROUP)
  api.startOrgGroup.mockResolvedValue(GROUP)
  api.stopOrgGroup.mockResolvedValue(GROUP)
  onOrgEvent.mockImplementation((cb) => { emit = cb; return () => { emit = null } })
})
afterEach(() => cleanup())

describe('groupTraffic', () => {
  it('counts each cross-org message once across all three buses', () => {
    const events = [
      ...fixture('herald-smoke'), ...fixture('anvil-smoke'), ...fixture('forge-smoke'),
    ].sort((a, b) => a.ts - b.ts)
    let s = initialGroupTraffic(['herald', 'anvil', 'forge'])
    for (const e of events) s = applyGroupEvent(s, e)
    expect(s.duplicatesDropped).toBe(14)
    expect(s.arcs['anvil->herald'].count).toBe(3)
    expect(s.arcs['herald->anvil'].count).toBe(4)
    expect(s.arcs['forge->herald'].count).toBe(2)
    expect(s.arcs['herald->forge'].count).toBe(5)
    expect(s.orgs.herald.received).toBe(5)
    expect(s.orgs.anvil.status).toBe('running')
    expect(s.orgs.anvil.costUsd).toBeCloseTo(0.44117395, 6)
    expect(recentArcs(s, s.lastTs, 1e12)).toHaveLength(4)
  })

  it('ignores traffic with orgs outside the group and foreign usage senders', () => {
    let s = initialGroupTraffic(['hq', 'sales'])
    s = applyGroupEvent(s, { id: '1', ts: 1, type: 'xorg', org: 'hq', from: 'hq:ceo', to: 'vendor:bot', subject: 'x', msg: 'y' })
    s = applyGroupEvent(s, { id: '2', ts: 2, type: 'usage', org: 'sales', from: 'hq:ceo', data: { cost_usd: 5 } })
    expect(s.arcs).toEqual({})
    expect(s.orgs.sales.costUsd).toBe(0)
    expect(arcPath(0, 0, 100, 0)).toMatch(/^M0.0,0.0 Q50.0,18.0 100.0,0.0$/)
  })
})

describe('GroupView', () => {
  it('renders child cards with status, cost, and roll-up', async () => {
    render(<GroupView holding="herald" onOpenOrg={() => {}} />)
    expect(await screen.findByText('anvil')).toBeInTheDocument()
    expect(screen.getByText('forge')).toBeInTheDocument()
    expect(screen.getByText('roll-up $1.14')).toBeInTheDocument()
    expect(screen.getByText('spent $0.440')).toBeInTheDocument()
    expect(screen.getByText('50%')).toBeInTheDocument()
    expect(screen.getByText('with parent')).toBeInTheDocument()
    await waitFor(() => expect(api.streamOrgEvents).toHaveBeenCalledWith('anvil'))
    expect(api.streamOrgEvents).toHaveBeenCalledWith('forge')
    expect(api.streamOrgEvents).not.toHaveBeenCalledWith('herald')
  })

  it('draws one deduped arc per direction from live xorg events', async () => {
    render(<GroupView holding="herald" />)
    await screen.findByText('anvil')
    await waitFor(() => expect(emit).toBeTypeOf('function'))
    const now = Date.now()
    const msg = { type: 'xorg', from: 'anvil:cto', to: 'herald:editor', subject: 'Submission', msg: 'here it is' }
    act(() => {
      emit({ orgName: 'anvil', event: { ...msg, id: 'a-1', ts: now, org: 'anvil' } })
      emit({ orgName: 'herald', event: { ...msg, id: 'h-1', ts: now + 2, org: 'herald' } })
      emit({ orgName: 'herald', event: { id: 'h-2', ts: now + 5, org: 'herald', type: 'xorg', from: 'herald:editor', to: 'forge:cto', subject: 'Ping', msg: 'status?' } })
    })
    const arcs = await screen.findAllByTestId('group-arc')
    expect(arcs).toHaveLength(2)
    const anvilArc = arcs.find(a => a.getAttribute('data-from') === 'anvil')
    expect(anvilArc.getAttribute('data-count')).toBe('1')
    expect(screen.getByText('· 2 cross-org messages')).toBeInTheDocument()
  })

  it('starts and stops the group, confirming the stop', async () => {
    render(<GroupView holding="herald" />)
    await screen.findByText('anvil')
    fireEvent.click(screen.getByRole('button', { name: /Start group/ }))
    await waitFor(() => expect(api.startOrgGroup).toHaveBeenCalledWith('herald'))
    fireEvent.click(screen.getByRole('button', { name: /Stop group/ }))
    await waitFor(() => expect(api.stopOrgGroup).toHaveBeenCalledWith('herald'))
    expect(confirm).toHaveBeenCalled()
  })

  it('toasts a failed start and shows a load error', async () => {
    api.startOrgGroup.mockResolvedValue({ error: 'budget_share ceiling reached' })
    render(<GroupView holding="herald" />)
    await screen.findByText('anvil')
    fireEvent.click(screen.getByRole('button', { name: /Start group/ }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('start group', 'budget_share ceiling reached'))
    cleanup()
    api.orgGroupStatus.mockResolvedValue({ error: 'herald is not a holding org' })
    render(<GroupView holding="herald" />)
    expect(await screen.findByText('herald is not a holding org')).toBeInTheDocument()
  })

  it('stops child tails on unmount', async () => {
    const { unmount } = render(<GroupView holding="herald" />)
    await screen.findByText('anvil')
    unmount()
    expect(api.stopOrgEvents).toHaveBeenCalledWith('anvil')
    expect(api.stopOrgEvents).toHaveBeenCalledWith('forge')
  })
})
