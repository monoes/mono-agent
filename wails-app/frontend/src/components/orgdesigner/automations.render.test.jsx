// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
import AutomationsDrawer, { slugifyAlias } from './AutomationsDrawer.jsx'
import GrantDialog, { defaultGrantSpec, bashAllowed } from './GrantDialog.jsx'
import GrantsMatrix from './GrantsMatrix.jsx'
import RoleInspector from './RoleInspector.jsx'
import { api, notify } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'

vi.mock('../../services/api.js', () => ({
  api: {
    listUnassignedAutomations: vi.fn(),
    addOrgAutomation: vi.fn(),
    removeOrgAutomation: vi.fn(),
    setOrgGrant: vi.fn(),
    removeOrgGrant: vi.fn(),
    getEffectiveTools: vi.fn(),
    chooseInstructionsFile: vi.fn(),
  },
  notify: vi.fn(),
}))
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }))

const PUBLISH = {
  workflow_id: 'wf-pub', alias: 'publish_post', owned: true, workflow_name: 'Publish post',
  has_outbound_nodes: true, outbound_nodes: ['comm.slack_send'], exists: true,
}
const SUMMARIZE = {
  workflow_id: 'wf-sum', alias: 'summarize', owned: false, workflow_name: 'Summarize',
  has_outbound_nodes: false, outbound_nodes: [], exists: true,
}
const GONE = { workflow_id: 'wf-gone', alias: 'old_flow', workflow_name: '', has_outbound_nodes: false, exists: false }

const GRANT = {
  id: 'grt_1', org: 'growth', role: 'lead', alias: 'publish_post', workflow_id: 'wf-pub', workflow_name: 'Publish post',
  mode: 'run', wait: true, timeout_seconds: 600, approval: 'required', tier: 'irreversible',
  max_calls_per_run: 20, max_calls_per_day: 200, max_output_bytes: 16384, created_at: '2026-09-16T10:00:00Z',
}

const lead = { id: 'lead', title: 'Lead', type: 'boss', parentId: null, responsibilities: [], rest: {} }
const writer = { id: 'writer', title: 'Writer', type: 'specialist', parentId: 'lead', responsibilities: [], rest: { policy: { denyTools: ['Bash'] } } }
const bot = { id: 'publisher-bot', title: 'Publisher', type: 'automation', parentId: 'lead', responsibilities: [], rest: { kind: 'endpoint', automation: { workflow_id: 'wf-pub', alias: 'publish_post', reply: 'last_node' }, endpoint: { url: 'http://127.0.0.1:9322/org-endpoint/ep_SECRET', input_hint: 'text to post' } } }

beforeEach(() => {
  vi.clearAllMocks()
  api.setOrgGrant.mockResolvedValue({ v: 1, org: 'growth', grant: GRANT, tool_name: 'monoagent__automation_publish_post' })
  api.removeOrgGrant.mockResolvedValue({ v: 1, removed: true })
  api.addOrgAutomation.mockResolvedValue({ v: 1, automation: SUMMARIZE })
  api.removeOrgAutomation.mockResolvedValue({ v: 1, removed: true })
  api.listUnassignedAutomations.mockResolvedValue({ v: 1, workflows: [{ id: 'wf-new', name: 'Weekly Report!', is_active: true }] })
  api.getEffectiveTools.mockResolvedValue({
    v: 1, org: 'growth', role: 'lead', tools: [
      { name: 'org_send', source: 'org', description: 'Message a role' },
      { name: 'monoagent__automation_publish_post', source: 'grant', description: 'Publish post' },
      { name: 'Read', source: 'builtin', description: 'Read files' },
    ],
  })
})
afterEach(() => cleanup())

describe('AutomationsDrawer', () => {
  const props = (over = {}) => ({
    orgName: 'growth', automations: [PUBLISH, SUMMARIZE, GONE], grants: [GRANT], loading: false, error: '',
    onRefresh: vi.fn(() => Promise.resolve()), onDragStart: vi.fn(), onOpenWorkflow: vi.fn(), ...over,
  })

  it('lists automations with alias, outbound badge, and missing workflows', () => {
    render(<AutomationsDrawer {...props()} />)
    const rows = screen.getAllByTestId('org-automation')
    expect(rows).toHaveLength(3)
    expect(within(rows[0]).getByText('Publish post')).toBeInTheDocument()
    expect(within(rows[0]).getByText('publish_post')).toBeInTheDocument()
    expect(within(rows[0]).getByText('outbound')).toBeInTheDocument()
    expect(within(rows[1]).queryByText('outbound')).not.toBeInTheDocument()
    expect(within(rows[2]).getByText('missing')).toBeInTheDocument()
  })

  it('opens a workflow and starts a drag from a row', () => {
    const p = props()
    render(<AutomationsDrawer {...p} />)
    fireEvent.click(screen.getByRole('button', { name: 'Open workflow summarize' }))
    expect(p.onOpenWorkflow).toHaveBeenCalledWith('wf-sum')
    fireEvent.mouseDown(screen.getAllByTestId('org-automation')[0], { button: 0, clientX: 5, clientY: 6 })
    expect(p.onDragStart).toHaveBeenCalledWith(PUBLISH, expect.anything())
  })

  it('removes an automation after confirming, warning about its grants', async () => {
    const p = props()
    render(<AutomationsDrawer {...p} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove publish_post' }))
    await waitFor(() => expect(api.removeOrgAutomation).toHaveBeenCalledWith('growth', 'publish_post'))
    expect(confirm.mock.calls[0][0]).toMatch(/1 grant to it will be revoked/)
    expect(p.onRefresh).toHaveBeenCalled()
  })

  it('adds from the unassigned library with an editable alias', async () => {
    const p = props()
    render(<AutomationsDrawer {...p} />)
    fireEvent.click(screen.getByRole('button', { name: /Add from library/ }))
    const alias = await screen.findByLabelText('Alias for Weekly Report!')
    expect(alias).toHaveValue('weekly_report')
    fireEvent.change(alias, { target: { value: 'weekly' } })
    fireEvent.click(screen.getByRole('button', { name: /^Add$/ }))
    await waitFor(() => expect(api.addOrgAutomation).toHaveBeenCalledWith('growth', 'wf-new', 'weekly'))
  })

  it('blocks invalid aliases and shows empty and error states', async () => {
    render(<AutomationsDrawer {...props({ automations: [] })} />)
    expect(screen.getByText('No automations yet. Add one from the library.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Add from library/ }))
    const alias = await screen.findByLabelText('Alias for Weekly Report!')
    fireEvent.change(alias, { target: { value: 'Bad Alias' } })
    expect(screen.getByRole('button', { name: /^Add$/ })).toBeDisabled()
    cleanup()
    render(<AutomationsDrawer {...props({ automations: [], error: 'grants need monomind with capability "org-tool-providers"' })} />)
    expect(screen.getByText(/org-tool-providers/)).toBeInTheDocument()
  })

  it('toasts a failed add', async () => {
    api.addOrgAutomation.mockResolvedValue({ error: 'alias "lead" collides with a role id' })
    render(<AutomationsDrawer {...props()} />)
    fireEvent.click(screen.getByRole('button', { name: /Add from library/ }))
    await screen.findByLabelText('Alias for Weekly Report!')
    fireEvent.click(screen.getByRole('button', { name: /^Add$/ }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('add automation', 'alias "lead" collides with a role id'))
  })

  it('slugifies workflow names into aliases', () => {
    expect(slugifyAlias('Publish Post (v2)')).toBe('publish_post_v2')
    expect(slugifyAlias('__')).toBe('automation_new')
  })
})

describe('GrantDialog', () => {
  const autonomyMid = { level: 'mid', decider: { kind: 'boss' }, tiers: {}, default_tiers: {} }

  it('defaults outbound automations to needing a decision and previews tier and route', () => {
    render(<GrantDialog open orgName="growth" role={lead} automation={PUBLISH} autonomy={autonomyMid} onClose={vi.fn()} />)
    expect(screen.getByRole('radio', { name: 'yes' })).toBeChecked()
    const tier = screen.getByTestId('grant-tier')
    expect(within(tier).getByText('irreversible')).toBeInTheDocument()
    expect(tier).toHaveTextContent('at mid: waits for you')
    fireEvent.click(screen.getByRole('radio', { name: 'no' }))
    expect(screen.getByTestId('grant-tier')).toHaveTextContent('Runs without a decision.')
    expect(screen.getByText('This workflow sends outside mono-agent.')).toBeInTheDocument()
  })

  it('shows consequential routed to the decider for an internal workflow at mid', () => {
    render(<GrantDialog open orgName="growth" role={lead} automation={SUMMARIZE} autonomy={autonomyMid} onClose={vi.fn()} />)
    expect(screen.getByRole('radio', { name: 'no' })).toBeChecked()
    fireEvent.click(screen.getByRole('radio', { name: 'yes' }))
    expect(screen.getByTestId('grant-tier')).toHaveTextContent('consequentialat mid: decided by boss')
  })

  it('saves through org grant add and denies Bash when asked', async () => {
    const onSaved = vi.fn()
    const onDenyBash = vi.fn()
    render(<GrantDialog open orgName="growth" role={lead} automation={PUBLISH} autonomy={autonomyMid} onClose={vi.fn()} onSaved={onSaved} onDenyBash={onDenyBash} />)
    expect(screen.getByText('This role can run Bash, which can bypass its grants.')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/Wait for the result/))
    fireEvent.change(screen.getByLabelText('Timeout seconds'), { target: { value: '900' } })
    fireEvent.change(screen.getByLabelText('Per-run cap'), { target: { value: '5' } })
    fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'trigger' } })
    fireEvent.click(screen.getByRole('button', { name: 'Grant' }))
    await waitFor(() => expect(api.setOrgGrant).toHaveBeenCalledWith('growth', {
      role: 'lead', automation: 'publish_post', mode: 'trigger', wait: false, timeout_seconds: 900, approval: 'required', max_calls_per_run: 5,
    }))
    expect(onDenyBash).toHaveBeenCalledWith(lead)
    expect(onSaved).toHaveBeenCalled()
  })

  it('keeps Bash when unticked, hides the warning when already denied, and shows CLI errors', async () => {
    const onDenyBash = vi.fn()
    api.setOrgGrant.mockResolvedValueOnce({ error: 'grants need monomind with capability "org-tool-providers" (installed: 2.10.30) — update monomind' })
    const { rerender } = render(<GrantDialog open orgName="growth" role={lead} automation={SUMMARIZE} onClose={vi.fn()} onDenyBash={onDenyBash} />)
    fireEvent.click(screen.getByLabelText(/Deny Bash for Lead/))
    fireEvent.click(screen.getByRole('button', { name: 'Grant' }))
    expect(await screen.findByText(/update monomind/)).toBeInTheDocument()
    expect(onDenyBash).not.toHaveBeenCalled()
    rerender(<GrantDialog open orgName="growth" role={writer} automation={SUMMARIZE} onClose={vi.fn()} />)
    expect(screen.queryByText(/can bypass its grants/)).not.toBeInTheDocument()
  })

  it('edits an existing grant with its saved values', () => {
    render(<GrantDialog open orgName="growth" role={lead} automation={PUBLISH} grant={{ ...GRANT, wait: false, max_calls_per_run: 3 }} onClose={vi.fn()} />)
    expect(screen.getByRole('dialog', { name: 'Grant automation' })).toHaveTextContent('Edit grant')
    expect(screen.getByLabelText(/Wait for the result/)).not.toBeChecked()
    expect(screen.getByLabelText('Per-run cap')).toHaveValue(3)
    expect(screen.getByRole('button', { name: 'Save grant' })).toBeInTheDocument()
  })

  // `org grant add` is an upsert that rebuilds the row from its flags, so an
  // edit that does not resend the daily and output caps resets whatever the
  // operator set on the CLI back to 200 / 16384.
  it('resends an existing grant’s daily and output caps when only the timeout changes', async () => {
    render(<GrantDialog open orgName="growth" role={writer} automation={PUBLISH} grant={{ ...GRANT, role: 'writer', max_calls_per_day: 5, max_output_bytes: 2048 }} autonomy={autonomyMid} onClose={vi.fn()} />)
    fireEvent.change(screen.getByLabelText('Timeout seconds'), { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save grant' }))
    await waitFor(() => expect(api.setOrgGrant).toHaveBeenCalledWith('growth', {
      role: 'writer', automation: 'publish_post', mode: 'run', wait: true, timeout_seconds: 30, approval: 'required',
      max_calls_per_run: 20, max_calls_per_day: 5, max_output_bytes: 2048,
    }))
  })

  it('exposes pure helpers', () => {
    expect(defaultGrantSpec('lead', PUBLISH)).toEqual({ role: 'lead', automation: 'publish_post', mode: 'run', wait: true, timeout_seconds: 600, max_calls_per_run: 20, approval: 'required' })
    expect(defaultGrantSpec('lead', SUMMARIZE).approval).toBe('none')
    expect(bashAllowed(lead)).toBe(true)
    expect(bashAllowed(writer)).toBe(false)
  })
})

describe('GrantsMatrix', () => {
  const nodes = [lead, writer, bot]

  it('shows agent roles × automations and hides automation roles', () => {
    render(<GrantsMatrix orgName="growth" nodes={nodes} automations={[PUBLISH, SUMMARIZE]} grants={[GRANT]} />)
    const grid = screen.getByRole('grid', { name: 'Grants matrix' })
    expect(within(grid).getByRole('rowheader', { name: /Lead/ })).toBeInTheDocument()
    expect(within(grid).queryByRole('rowheader', { name: /Publisher/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Revoke publish_post from lead' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Grant summarize to writer' })).toHaveAttribute('aria-pressed', 'false')
    expect(within(grid).getByText('irreversible')).toBeInTheDocument()
  })

  it('grants with safe defaults, revokes after confirm, and opens the editor', async () => {
    const onChanged = vi.fn(() => Promise.resolve())
    const onEditGrant = vi.fn()
    render(<GrantsMatrix orgName="growth" nodes={nodes} automations={[PUBLISH, SUMMARIZE]} grants={[GRANT]} onChanged={onChanged} onEditGrant={onEditGrant} />)
    fireEvent.click(screen.getByRole('button', { name: 'Grant publish_post to writer' }))
    await waitFor(() => expect(api.setOrgGrant).toHaveBeenCalledWith('growth', defaultGrantSpec('writer', PUBLISH)))
    fireEvent.click(screen.getByRole('button', { name: 'Revoke publish_post from lead' }))
    await waitFor(() => expect(api.removeOrgGrant).toHaveBeenCalledWith('growth', 'lead', 'publish_post'))
    expect(confirm).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Edit grant publish_post for lead' }))
    expect(onEditGrant).toHaveBeenCalledWith(lead, PUBLISH, GRANT)
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2))
  })

  it('has empty states', () => {
    render(<GrantsMatrix orgName="growth" nodes={nodes} automations={[]} grants={[]} />)
    expect(screen.getByText(/No automations in this org yet/)).toBeInTheDocument()
    cleanup()
    render(<GrantsMatrix orgName="growth" nodes={[bot]} automations={[PUBLISH]} grants={[]} />)
    expect(screen.getByText('No agent roles to grant to.')).toBeInTheDocument()
  })
})

describe('RoleInspector automation sections', () => {
  const base = (node, over = {}) => ({
    node, allNodes: [lead, writer, bot], onPatch: vi.fn(), onSetReportsTo: vi.fn(), onPromoteToRoot: vi.fn(),
    onOpenIconPicker: vi.fn(), onDelete: vi.fn(),
    orgName: 'growth', grants: [GRANT], automations: [PUBLISH, SUMMARIZE], engineOffline: false, grantsVersion: 1,
    onGrantsChanged: vi.fn(() => Promise.resolve()), onOpenWorkflow: vi.fn(), onEditGrant: vi.fn(), ...over,
  })

  it('lists grants, effective tools, and warns loudly about Bash', async () => {
    const props = base(lead)
    render(<RoleInspector {...props} />)
    const grant = screen.getByTestId('role-grant')
    expect(within(grant).getByText('needs a decision')).toBeInTheDocument()
    expect(within(grant).getByText('irreversible')).toBeInTheDocument()
    const tools = await screen.findByRole('list', { name: 'Effective tools' })
    expect(within(tools).getByText('monoagent__automation_publish_post')).toBeInTheDocument()
    expect(within(tools).getByText('grant')).toBeInTheDocument()
    expect(within(tools).getByText('builtin')).toBeInTheDocument()
    expect(api.getEffectiveTools).toHaveBeenCalledWith('growth', 'lead')

    expect(screen.getByRole('alert')).toHaveTextContent('This role can bypass its grants')
    fireEvent.click(screen.getByRole('button', { name: 'Deny Bash' }))
    expect(props.onPatch).toHaveBeenCalledWith({ policy: expect.objectContaining({ denyTools: ['Bash'] }) })
    expect(screen.queryByText('This role can bypass its grants')).not.toBeInTheDocument()
  })

  it('opens and revokes a grant', async () => {
    const props = base(lead)
    render(<RoleInspector {...props} />)
    fireEvent.click(screen.getByRole('button', { name: 'Open workflow publish_post' }))
    expect(props.onOpenWorkflow).toHaveBeenCalledWith('wf-pub')
    fireEvent.click(screen.getByRole('button', { name: 'Revoke publish_post' }))
    await waitFor(() => expect(api.removeOrgGrant).toHaveBeenCalledWith('growth', 'lead', 'publish_post'))
    expect(props.onGrantsChanged).toHaveBeenCalled()
  })

  it('shows no Bash warning for a role without grants or with Bash denied', () => {
    render(<RoleInspector {...base(writer, { grants: [{ ...GRANT, role: 'writer' }] })} />)
    expect(screen.queryByText('This role can bypass its grants')).not.toBeInTheDocument()
    cleanup()
    render(<RoleInspector {...base({ ...writer, id: 'other', rest: {} })} />)
    expect(screen.getByText(/No grants\. Drag an automation onto this role/)).toBeInTheDocument()
    expect(screen.queryByText('This role can bypass its grants')).not.toBeInTheDocument()
  })

  it('summarises an automation role, hides agent-only fields, and never shows its endpoint URL', () => {
    render(<RoleInspector {...base(bot, { engineOffline: true })} />)
    expect(screen.getByText('Automation role')).toBeInTheDocument()
    expect(screen.getByText('Input: text to post')).toBeInTheDocument()
    expect(screen.getByText(/Engine offline/)).toBeInTheDocument()
    expect(screen.queryByText('Runtime')).not.toBeInTheDocument()
    expect(screen.queryByText('Tool policy')).not.toBeInTheDocument()
    expect(screen.queryByText('Effective tools')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('ep_SECRET')
  })
})
