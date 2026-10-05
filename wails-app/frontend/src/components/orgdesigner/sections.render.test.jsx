// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
import OrgDesigner from './OrgDesigner.jsx'
import { api } from '../../services/api.js'

vi.mock('../../services/api.js', () => ({
  api: new Proxy({}, { get: (t, k) => (t[k] ??= vi.fn()) }),
  notify: vi.fn(),
  onOrgEvent: vi.fn(() => () => {}),
  onOrgDesignUpdated: vi.fn(() => () => {}),
}))
vi.mock('./roleIcons.js', () => ({
  loadIconManifest: vi.fn(() => Promise.resolve([])),
  suggestIcon: vi.fn(() => Promise.resolve('coder')),
  iconUrl: vi.fn(() => ''),
  CATEGORY_TYPE: {},
  CAT_COLOR: {},
  getRecentIcons: vi.fn(() => []),
  pushRecentIcon: vi.fn(),
}))
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }))

class RO { observe() {} disconnect() {} }

const role = (id, parent, x, y, extra = {}) => ({ id, title: id.toUpperCase(), type: 'specialist', reports_to: parent, responsibilities: [], ui: { x, y, icon: 'coder' }, ...extra })
const ORG = {
  v: 1, rev: 'r1',
  org: {
    name: 'duo', goal: 'g', status: 'stopped',
    roles: [role('boss', null, 200, 0, { type: 'boss' }), role('a1', 'boss', 0, 200), role('a2', 'a1', 0, 340), role('b1', 'boss', 600, 200)],
    sections: {
      alpha: { lead: 'a1', members: ['a1', 'a2'], publishes: ['spec'], budget: { usd: 5 } },
      beta: { members: ['b1'], consumes: ['spec'] },
    },
    documents: { spec: { schema: { type: 'object' } } },
  },
}

function stubApi() {
  api.getOrgDesign.mockResolvedValue(ORG)
  api.listOrgAutomations.mockResolvedValue({ automations: [] })
  api.listOrgGrants.mockResolvedValue({ grants: [] })
  api.getOrgAutonomy.mockResolvedValue({ level: 'mid', tiers: {} })
  api.getDaemonStatus.mockResolvedValue({ daemon: { running: false }, org_serve: { running: true } })
  api.getOrgLogs.mockResolvedValue({ items: [] })
  api.getOrgReport.mockResolvedValue({ items: [] })
  api.saveOrgLayout.mockResolvedValue({ ok: true, rev: 'r2' })
  api.getEffectiveTools.mockResolvedValue({ tools: [] })
  api.validateOrgReport.mockResolvedValue({ errors: [], warnings: [] })
  api.orgSignatureStatus.mockResolvedValue({ supported: false })
  api.getOrgRolesAccess?.mockResolvedValue?.({ roles: [] })
}

async function open() {
  render(<OrgDesigner orgName="duo" />)
  await waitFor(() => expect(screen.getByTestId('section-alpha')).toBeInTheDocument())
}

function drag(id, dx, dy) {
  const card = document.querySelector(`[data-od-node-id="${id}"]`)
  fireEvent.mouseDown(within(card).getByText(id.toUpperCase()), { button: 0, clientX: 100, clientY: 100 })
  fireEvent.mouseMove(document, { clientX: 100 + dx, clientY: 100 + dy })
  fireEvent.mouseUp(document, { clientX: 100 + dx, clientY: 100 + dy })
}

describe('Org Designer sections', () => {
  beforeEach(() => { globalThis.ResizeObserver = RO; stubApi() })
  afterEach(() => { cleanup(); vi.clearAllMocks() })

  it('draws each section as a container with its lead marked and the document edge between them', async () => {
    await open()
    expect(screen.getByTestId('section-beta')).toBeInTheDocument()
    expect(screen.getByTestId('lead-badge-a1')).toBeInTheDocument()
    expect(screen.queryByTestId('lead-badge-a2')).toBeNull()
    const edge = screen.getByTestId('doc-edge')
    expect(edge).toHaveAttribute('data-from', 'alpha')
    expect(edge).toHaveAttribute('data-to', 'beta')
    expect(edge).toHaveAttribute('data-type', 'spec')
  })

  it('moves a role into the container it is dropped in, through the backend', async () => {
    api.assignOrgRole.mockResolvedValue({ ok: true, rev: 'r3', org: ORG.org })
    await open()
    drag('a2', 600, -120)
    await waitFor(() => expect(api.assignOrgRole).toHaveBeenCalledWith('duo', 'a2', 'beta'))
  })

  it('refuses a role dropped outside every section: reason inline, nothing saved to membership, role snaps back', async () => {
    await open()
    drag('a2', 2900, 2700)
    expect(await screen.findByText(/a role outside every section can only be the root/)).toBeInTheDocument()
    expect(api.assignOrgRole).not.toHaveBeenCalled()
    await waitFor(() => expect(document.querySelector('[data-od-node-id="a2"]').style.left).toBe('0px'))
  })

  it('edits a section through its inspector', async () => {
    api.updateOrgSection.mockResolvedValue({ ok: true, rev: 'r3', org: ORG.org })
    await open()
    fireEvent.mouseDown(screen.getByTestId('section-header-alpha'), { button: 0 })
    const budget = await screen.findByTestId('section-budget')
    expect(budget).toHaveValue(5)
    fireEvent.change(budget, { target: { value: '8' } })
    fireEvent.blur(budget)
    await waitFor(() => expect(api.updateOrgSection).toHaveBeenCalledWith('duo', 'alpha', { budget_usd: 8 }))
    fireEvent.change(screen.getByTestId('section-rework'), { target: { value: '3' } })
    fireEvent.blur(screen.getByTestId('section-rework'))
    await waitFor(() => expect(api.updateOrgSection).toHaveBeenCalledWith('duo', 'alpha', { max_rework_rounds: 3 }))
    fireEvent.change(screen.getByTestId('section-writes'), { target: { value: 'src/**, docs/**' } })
    fireEvent.blur(screen.getByTestId('section-writes'))
    await waitFor(() => expect(api.updateOrgSection).toHaveBeenCalledWith('duo', 'alpha', { writes: ['src/**', 'docs/**'] }))
    fireEvent.change(screen.getByTestId('section-lead'), { target: { value: 'a2' } })
    await waitFor(() => expect(api.updateOrgSection).toHaveBeenCalledWith('duo', 'alpha', { lead: 'a2' }))
  })

  it('marks a lead from the role panel', async () => {
    api.updateOrgSection.mockResolvedValue({ ok: true, rev: 'r3', org: ORG.org })
    await open()
    fireEvent.mouseDown(within(document.querySelector('[data-od-node-id="a2"]')).getByText('A2'), { button: 0 })
    fireEvent.mouseUp(document)
    fireEvent.click(await screen.findByTestId('make-lead'))
    await waitFor(() => expect(api.updateOrgSection).toHaveBeenCalledWith('duo', 'alpha', { lead: 'a2' }))
  })

  it('shows the refusal for a new section inline in the prompt and keeps it open', async () => {
    api.addOrgSection.mockResolvedValue({ error: 'section "x" needs at least one non-root role: select a role to put in it' })
    await open()
    fireEvent.click(screen.getByRole('button', { name: '+ Section' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create section' }))
    expect(await screen.findByTestId('name-prompt-error')).toHaveTextContent('needs at least one non-root role')
    expect(screen.getByTestId('name-prompt')).toBeInTheDocument()
  })

  it('refuses a direct cross-section message link with the backend reason', async () => {
    const reason = 'sections "alpha" and "beta" cannot message each other directly: work crosses sections only as a typed document'
    api.setOrgRoleReportsTo.mockResolvedValue({ error: reason })
    await open()
    // draw a reports_to edge from a2's top handle onto b1
    const card = document.querySelector('[data-od-node-id="a2"]')
    const handle = card.querySelector('[title="Drag to set reports-to"]')
    expect(handle).toBeTruthy()
    fireEvent.mouseDown(handle, { button: 0, clientX: 90, clientY: 340 })
    document.elementFromPoint = vi.fn(() => document.querySelector('[data-od-node-id="b1"]'))
    fireEvent.mouseMove(document, { clientX: 690, clientY: 230 })
    fireEvent.mouseUp(document, { clientX: 690, clientY: 230 })
    expect(await screen.findByText(/cannot message each other directly/)).toBeInTheDocument()
    expect(api.setOrgRoleReportsTo).toHaveBeenCalledWith('duo', 'a2', 'b1')
  })
})
