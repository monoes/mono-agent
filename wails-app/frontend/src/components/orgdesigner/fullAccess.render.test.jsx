// @vitest-environment jsdom
// Full-access org roles (#205): the state badge, the role editor's grant /
// grant-again / revoke control behind the risk confirmation, the overview
// summary, and `org validate`'s taint problems in the toolbar.
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({
  api: {
    orgRoleSetAccess: vi.fn(), getOrgStatus: vi.fn(), validateOrgReport: vi.fn(), chooseInstructionsFile: vi.fn(),
    getEffectiveTools: vi.fn().mockResolvedValue(null),
  },
}))
import { api } from '../../services/api.js'
import ConfirmHost from '../ConfirmDialog.jsx'
import { FullAccessBadge, rolesAccessByRole } from './fullAccess.jsx'
import RoleFullAccessSection from './RoleFullAccessSection.jsx'
import RoleInspector from './RoleInspector.jsx'
import RoleNode from './RoleNode.jsx'
import FullAccessSummary from '../orgs/FullAccessSummary.jsx'
import { ValidationIssues } from './DesignerToolbar.jsx'
import { reportProblems, mergeValidation } from './orgValidateReport.js'

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { cleanup() })

const ACTIVE = { role: 'builder', access: 'full', access_state: 'active' }
const SUSPENDED = { role: 'builder', access: 'full', access_state: 'suspended', reason: 'config changed since the grant: prompt, reports_to' }
const BLOCKED = { role: 'ops', access: 'full', access_state: 'unattended-blocked', reason: 'run_config.allow_unattended_full_access is false' }
const REFUSAL = 'granting full access is human-only and this looks like an agent (CLAUDECODE is set); run it yourself from a terminal or the app'

function renderSection(props) {
  const onChanged = vi.fn()
  render(<><RoleFullAccessSection orgName="growth" roleID="builder" onChanged={onChanged} {...props} /><ConfirmHost /></>)
  return onChanged
}

describe('FullAccessBadge', () => {
  it('shows each state, with the reason on hover', () => {
    render(<>
      <FullAccessBadge entry={ACTIVE} />
      <FullAccessBadge entry={SUSPENDED} />
      <FullAccessBadge entry={BLOCKED} compact />
      <FullAccessBadge entry={{}} />
    </>)
    const [a, s, b, u] = screen.getAllByTestId('full-access-badge')
    expect(a).toHaveAttribute('data-state', 'active')
    expect(a).toHaveTextContent('FULL ACCESS')
    expect(s).toHaveTextContent('FULL ACCESS · SUSPENDED')
    expect(s).toHaveAttribute('title', `Full access · suspended: ${SUSPENDED.reason}`)
    expect(b).toHaveTextContent(/^\s*FULL$/)
    expect(b).toHaveAttribute('title', `Full access · unattended blocked: ${BLOCKED.reason}`)
    expect(u).toHaveAttribute('data-state', 'unknown')
  })

  it('maps roles_access by role, and an absent key to nothing', () => {
    expect(rolesAccessByRole({ roles_access: [ACTIVE, BLOCKED] })).toEqual({ builder: ACTIVE, ops: BLOCKED })
    expect(rolesAccessByRole({ status: 'stopped' })).toEqual({})
    expect(rolesAccessByRole(null)).toEqual({})
  })
})

describe('RoleFullAccessSection', () => {
  it('grants only after the risk confirmation; cancelling calls nothing', async () => {
    const onChanged = renderSection({ entry: null, declared: false })
    fireEvent.click(screen.getByRole('button', { name: 'Grant full access…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Give builder full access?' })
    expect(dialog).toHaveTextContent('run any command, read and change any file your user account can, and install software')
    expect(dialog).toHaveTextContent("other roles' messages, reach it")
    expect(dialog).toHaveTextContent('Scheduled and unattended runs use full access only when the org allows unattended full access')
    expect(dialog).toHaveTextContent('monomind org role set-access --help')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(api.orgRoleSetAccess).not.toHaveBeenCalled()

    api.orgRoleSetAccess.mockResolvedValue({ org: 'growth', role: 'builder', access: 'full', message: 'builder now has full access' })
    fireEvent.click(screen.getByRole('button', { name: 'Grant full access…' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Grant full access' }))
    await waitFor(() => expect(api.orgRoleSetAccess).toHaveBeenCalledWith('growth', 'builder', 'full'))
    expect(await screen.findByText('builder now has full access')).toBeInTheDocument()
    expect(onChanged).toHaveBeenCalled()
  })

  it('revokes without a confirmation', async () => {
    api.orgRoleSetAccess.mockResolvedValue({ org: 'growth', role: 'builder', access: 'scoped', message: '' })
    const onChanged = renderSection({ entry: ACTIVE, declared: true })
    expect(screen.getByText('This role runs with full access.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Grant again/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(api.orgRoleSetAccess).toHaveBeenCalledWith('growth', 'builder', 'scoped'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByText('Full access revoked.')).toBeInTheDocument()
    expect(onChanged).toHaveBeenCalled()
  })

  it('a suspended role shows why and can be granted again', async () => {
    api.orgRoleSetAccess.mockResolvedValue({ org: 'growth', role: 'builder', access: 'full', message: 'granted' })
    renderSection({ entry: SUSPENDED, declared: true })
    expect(screen.getByTestId('full-access-reason')).toHaveTextContent(SUSPENDED.reason)
    fireEvent.click(screen.getByRole('button', { name: 'Grant again…' }))
    await screen.findByRole('dialog', { name: 'Grant builder full access again?' })
    fireEvent.click(screen.getByRole('button', { name: 'Grant again' }))
    await waitFor(() => expect(api.orgRoleSetAccess).toHaveBeenCalledWith('growth', 'builder', 'full'))
  })

  it('shows a refused grant verbatim', async () => {
    api.orgRoleSetAccess.mockRejectedValue(new Error(REFUSAL))
    const onChanged = renderSection({ entry: null, declared: false })
    fireEvent.click(screen.getByRole('button', { name: 'Grant full access…' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Grant full access' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(REFUSAL)
    expect(onChanged).not.toHaveBeenCalled()
  })

  it('the role editor shows it for a session role, not for an automation role', () => {
    const node = { id: 'builder', title: 'Builder', type: 'specialist', parentId: 'root', responsibilities: [], icon: '', rest: { policy: { access: 'full' } } }
    const { unmount } = render(<RoleInspector node={node} allNodes={[node]} onPatch={() => {}} orgName="growth" fullAccess={SUSPENDED} />)
    expect(screen.getByTestId('role-full-access')).toBeInTheDocument()
    expect(screen.getByTestId('full-access-badge')).toHaveAttribute('data-state', 'suspended')
    unmount()
    const auto = { id: 'hook', title: 'Hook', type: 'automation', parentId: 'root', responsibilities: [], icon: '', rest: { kind: 'endpoint', endpoint: { kind: 'workflow', ref: 'w1' } } }
    render(<RoleInspector node={auto} allNodes={[auto]} onPatch={() => {}} orgName="growth" />)
    expect(screen.queryByTestId('role-full-access')).not.toBeInTheDocument()
  })
})

describe('RoleNode', () => {
  it('carries the full-access badge on the canvas card', () => {
    const node = { id: 'builder', title: 'Builder', type: 'specialist', parentId: 'root', responsibilities: [], icon: '', x: 0, y: 0, rest: {} }
    const { rerender } = render(<RoleNode node={node} fullAccess={BLOCKED} />)
    expect(screen.getByTestId('full-access-badge')).toHaveAttribute('data-state', 'unattended-blocked')
    rerender(<RoleNode node={node} fullAccess={null} />)
    expect(screen.queryByTestId('full-access-badge')).not.toBeInTheDocument()
  })
})

describe('FullAccessSummary', () => {
  it('counts the full-access roles and offers Grant again only for a suspended one', async () => {
    api.orgRoleSetAccess.mockResolvedValue({ message: 'granted' })
    const onChanged = vi.fn()
    render(<><FullAccessSummary orgName="growth" status={{ roles_access: [SUSPENDED, BLOCKED] }} onChanged={onChanged} /><ConfirmHost /></>)
    expect(screen.getByTestId('full-access-summary')).toHaveTextContent('2 full-access roles')
    const [builder, ops] = screen.getAllByTestId('full-access-row')
    expect(builder).toHaveTextContent(SUSPENDED.reason)
    expect(ops).toHaveTextContent(BLOCKED.reason)
    expect(within(ops).queryByRole('button')).not.toBeInTheDocument()
    fireEvent.click(within(builder).getByRole('button', { name: 'Grant again…' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Grant again' }))
    await waitFor(() => expect(api.orgRoleSetAccess).toHaveBeenCalledWith('growth', 'builder', 'full'))
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
  })

  it('renders nothing without full-access roles', () => {
    const { container } = render(<FullAccessSummary orgName="growth" status={{ status: 'stopped' }} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('org validate taint problems', () => {
  const taint = 'role "builder" has full access but is reachable from untrusted input: scraper → analyst → builder'

  it('splits the report into problems and merges them with the structural checks', () => {
    const cli = reportProblems({ valid: false, error: `${taint}\n- role "x": bad`, warnings: ['w'] })
    expect(cli.errors).toEqual([taint, 'role "x": bad'])
    expect(reportProblems({ valid: true, warnings: [] }).errors).toEqual([])
    const merged = mergeValidation({ valid: true, errors: [] }, cli)
    expect(merged.valid).toBe(false)
    expect(merged.errors).toEqual([taint, 'role "x": bad'])
    expect(mergeValidation({ valid: false, errors: ['role "x": bad'] }, cli).errors).toEqual(['role "x": bad', taint])
  })

  it('the toolbar issue count lists them', () => {
    render(<ValidationIssues errors={['duplicate role id(s): a', taint]} />)
    const count = screen.getByRole('button', { name: '2 issues' })
    expect(screen.queryByTestId('validation-issues')).not.toBeInTheDocument()
    fireEvent.click(count)
    expect(screen.getByTestId('validation-issues')).toHaveTextContent('scraper → analyst → builder')
  })
})
