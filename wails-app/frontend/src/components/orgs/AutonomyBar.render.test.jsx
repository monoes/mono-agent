// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import AutonomyBar from './AutonomyBar.jsx'
import { api, notify } from '../../services/api.js'
// The real i18n setup, as main.jsx loads it, so the bar's labels come from
// src/locales/*.json and the English names below stay findable.
import i18n from '../../i18n.js'
import en from '../../locales/en.json'
import es from '../../locales/es.json'

vi.mock('../../services/api.js', () => ({
  api: {
    getOrgAutonomy: vi.fn(),
    setOrgAutonomy: vi.fn(),
    pauseOrgAutonomy: vi.fn(),
    resumeOrgAutonomy: vi.fn(),
    listOrgGrants: vi.fn(),
    getOrgGates: vi.fn(),
  },
  notify: vi.fn(),
}))

const AUTONOMY = {
  v: 1, org: 'growth', level: 'mid', effective_level: 'mid', paused_until: null,
  decider: { kind: 'model', runtime: 'claude', model: 'm', fallback: 'model', timeout_seconds: 120 },
  tiers: {}, default_tiers: { gate: 'irreversible' }, policy: '', on_decider_failure: 'deny',
  limits: {}, daemon_running: true,
}

beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  api.getOrgAutonomy.mockResolvedValue(AUTONOMY)
  api.setOrgAutonomy.mockResolvedValue({ v: 1 })
  api.pauseOrgAutonomy.mockResolvedValue({ v: 1 })
  api.resumeOrgAutonomy.mockResolvedValue({ v: 1 })
  api.listOrgGrants.mockResolvedValue({
    v: 1, org: 'growth', grants: [
      { role: 'lead', alias: 'publish_post', workflow_name: 'Publish post', tier: 'irreversible', approval: 'required' },
      { role: 'lead', alias: 'summarize', workflow_name: 'Summarize', tier: 'consequential', approval: 'required' },
    ],
  })
  api.getOrgGates.mockResolvedValue({ gates: [{ id: 'gate-1', name: 'publish-launch-post', roleId: 'judge', status: 'pending' }] })
})
afterEach(() => cleanup())

async function renderBar() {
  render(<React.StrictMode><AutonomyBar orgName="growth" /></React.StrictMode>)
  await waitFor(() => expect(screen.getByRole('radiogroup', { name: 'Autonomy level' })).toBeInTheDocument())
}

describe('AutonomyBar', () => {
  it('shows the current level and decider', async () => {
    await renderBar()
    expect(screen.getByRole('radio', { name: 'Mid' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: 'Manual' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByLabelText('Decider')).toHaveValue('model')
  })

  it('writes a lower level through org autonomy set', async () => {
    await renderBar()
    fireEvent.click(screen.getByRole('radio', { name: 'Manual' }))
    await waitFor(() => expect(api.setOrgAutonomy).toHaveBeenCalledWith('growth', { level: 'manual' }))
    await waitFor(() => expect(api.getOrgAutonomy.mock.calls.length).toBeGreaterThanOrEqual(3))
  })

  it('asks before full auto and lists gates and irreversible grants', async () => {
    await renderBar()
    fireEvent.click(screen.getByRole('radio', { name: 'Full auto' }))
    const dialog = await screen.findByRole('dialog', { name: 'Turn on full auto' })
    await waitFor(() => expect(within(dialog).getByText(/Every gate a role raises/)).toBeInTheDocument())
    expect(within(dialog).getByText(/publish-launch-post \(judge\)/)).toBeInTheDocument()
    expect(within(dialog).getByText('Publish post')).toBeInTheDocument()
    expect(within(dialog).queryByText('Summarize')).not.toBeInTheDocument()
    expect(api.setOrgAutonomy).not.toHaveBeenCalled()

    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn on full auto' }))
    await waitFor(() => expect(api.setOrgAutonomy).toHaveBeenCalledWith('growth', { level: 'full' }))
  })

  it('cancelling the full auto confirmation changes nothing', async () => {
    await renderBar()
    fireEvent.click(screen.getByRole('radio', { name: 'Full auto' }))
    const dialog = await screen.findByRole('dialog', { name: 'Turn on full auto' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(api.setOrgAutonomy).not.toHaveBeenCalled()
  })

  it('changes the decider', async () => {
    await renderBar()
    fireEvent.change(screen.getByLabelText('Decider'), { target: { value: 'boss' } })
    await waitFor(() => expect(api.setOrgAutonomy).toHaveBeenCalledWith('growth', { decider: { kind: 'boss' } }))
  })

  it.each([
    ['For 30 min', '30m'],
    ['For 2 hours', '2h'],
    ['Until resumed', ''],
  ])('pauses autonomy %s', async (label, duration) => {
    await renderBar()
    fireEvent.click(screen.getByRole('button', { name: /Pause autonomy/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: label }))
    await waitFor(() => expect(api.pauseOrgAutonomy).toHaveBeenCalledWith('growth', duration))
  })

  it('shows a paused org with Resume', async () => {
    api.getOrgAutonomy.mockResolvedValue({ ...AUTONOMY, paused_until: '2999-01-01T00:00:00Z', effective_level: 'manual' })
    await renderBar()
    expect(screen.getByText('Paused')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Resume/ }))
    await waitFor(() => expect(api.resumeOrgAutonomy).toHaveBeenCalledWith('growth'))
  })

  it('warns when the daemon is not running', async () => {
    api.getOrgAutonomy.mockResolvedValue({ ...AUTONOMY, daemon_running: false })
    await renderBar()
    expect(screen.getByText('Daemon off: acts as manual')).toBeInTheDocument()
  })

  it('toasts a failed change', async () => {
    api.setOrgAutonomy.mockResolvedValue({ error: 'boss decider needs monomind with capability "org-tool-providers"' })
    await renderBar()
    fireEvent.change(screen.getByLabelText('Decider'), { target: { value: 'boss' } })
    await waitFor(() => expect(notify).toHaveBeenCalledWith('set decider', expect.stringContaining('org-tool-providers')))
  })

  it('shows an unavailable state when the CLI cannot read autonomy', async () => {
    api.getOrgAutonomy.mockResolvedValue({ error: 'unknown command "autonomy"' })
    render(<AutonomyBar orgName="growth" />)
    expect(await screen.findByText('Autonomy unavailable')).toBeInTheDocument()
  })

  it('has every string in both locales', () => {
    for (const ns of ['autonomy', 'fullAuto']) {
      const walk = (a, b, path) => {
        for (const [k, v] of Object.entries(a)) {
          if (typeof v === 'object') walk(v, b?.[k], `${path}.${k}`)
          else expect(typeof b?.[k], `es is missing ${path}.${k}`).toBe('string')
        }
      }
      walk(en.orgs[ns], es.orgs[ns], `orgs.${ns}`)
      walk(es.orgs[ns], en.orgs[ns], `orgs.${ns}`)
    }
  })

  it('speaks the chosen language, including the Full auto confirmation', async () => {
    await act(() => i18n.changeLanguage('es'))
    render(<AutonomyBar orgName="growth" />)
    await waitFor(() => expect(screen.getByRole('radiogroup', { name: es.orgs.autonomy.levelGroup })).toBeInTheDocument())
    expect(screen.getByRole('radio', { name: es.orgs.autonomy.levels.mid })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByLabelText(es.orgs.autonomy.decider)).toHaveValue('model')
    expect(screen.getByRole('button', { name: new RegExp(es.orgs.autonomy.pause) })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: es.orgs.autonomy.levels.full }))
    const dialog = await screen.findByRole('dialog', { name: es.orgs.fullAuto.dialog })
    await waitFor(() => expect(within(dialog).getByText(new RegExp(es.orgs.fullAuto.everyGate))).toBeInTheDocument())
    expect(within(dialog).getByRole('button', { name: es.orgs.fullAuto.confirm })).toBeInTheDocument()
  })
})
