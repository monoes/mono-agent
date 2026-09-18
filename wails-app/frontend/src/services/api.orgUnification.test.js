import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../wailsjs/go/main/App', () => ({
  ListOrgAutomations: vi.fn(),
  ListUnassignedAutomations: vi.fn(),
  AddOrgAutomation: vi.fn(),
  RemoveOrgAutomation: vi.fn(),
  ListOrgGrants: vi.fn(),
  SetOrgGrant: vi.fn(),
  RemoveOrgGrant: vi.fn(),
  AddAutomationRole: vi.fn(),
  RemoveAutomationRole: vi.fn(),
  GetEffectiveTools: vi.fn(),
  GetOrgAutonomy: vi.fn(),
  SetOrgAutonomy: vi.fn(),
  PauseOrgAutonomy: vi.fn(),
  ResumeOrgAutonomy: vi.fn(),
  ListOrgDecisionLog: vi.fn(),
  ListNeedsYou: vi.fn(),
  StartOrgGroup: vi.fn(),
  StopOrgGroup: vi.fn(),
  OrgGroupStatus: vi.fn(),
  SendOrgMessage: vi.fn(),
  GetDaemonStatus: vi.fn(),
}))
vi.mock('../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn(() => () => {}), EventsOff: vi.fn() }))

import * as GoApp from '../wailsjs/go/main/App'
import { api, onApiError } from './api.js'

beforeEach(() => { vi.clearAllMocks() })

describe('org unification api wrappers', () => {
  it('parses a read and passes arguments through', async () => {
    GoApp.ListOrgGrants.mockResolvedValueOnce('{"v":1,"org":"growth","grants":[]}')
    await expect(api.listOrgGrants('growth')).resolves.toEqual({ v: 1, org: 'growth', grants: [] })
    expect(GoApp.ListOrgGrants).toHaveBeenCalledWith('growth')
  })

  it('stringifies spec objects for SetOrgGrant, AddAutomationRole, SetOrgAutonomy and SendOrgMessage', async () => {
    GoApp.SetOrgGrant.mockResolvedValueOnce('{"v":1}')
    GoApp.AddAutomationRole.mockResolvedValueOnce('{"v":1}')
    GoApp.SetOrgAutonomy.mockResolvedValueOnce('{"v":1}')
    GoApp.SendOrgMessage.mockResolvedValueOnce('{"v":1}')
    const grant = { role: 'lead', automation: 'publish', approval: 'required' }
    await api.setOrgGrant('growth', grant)
    await api.addAutomationRole('growth', { alias: 'bot', reports_to: 'lead' })
    await api.setOrgAutonomy('growth', { level: 'mid' })
    await api.sendOrgMessage('growth', { to: 'lead', subject: 's', body: 'b' })
    expect(GoApp.SetOrgGrant).toHaveBeenCalledWith('growth', JSON.stringify(grant))
    expect(JSON.parse(GoApp.AddAutomationRole.mock.calls[0][1])).toEqual({ alias: 'bot', reports_to: 'lead' })
    expect(JSON.parse(GoApp.SetOrgAutonomy.mock.calls[0][1])).toEqual({ level: 'mid' })
    expect(JSON.parse(GoApp.SendOrgMessage.mock.calls[0][1]).to).toBe('lead')
  })

  it('returns the CLI {error} payload from a mutation unchanged', async () => {
    GoApp.RemoveOrgGrant.mockResolvedValueOnce('{"error":"no such grant"}')
    await expect(api.removeOrgGrant('growth', 'lead', 'publish')).resolves.toEqual({ error: 'no such grant' })
  })

  it('turns a thrown mutation into {error} instead of rejecting', async () => {
    GoApp.PauseOrgAutonomy.mockRejectedValueOnce(new Error('binary missing'))
    await expect(api.pauseOrgAutonomy('growth', '30m')).resolves.toEqual({ error: 'binary missing' })
    expect(GoApp.PauseOrgAutonomy).toHaveBeenCalledWith('growth', '30m')
  })

  it('defaults pause to "until resumed" and decisions to all runs', async () => {
    GoApp.PauseOrgAutonomy.mockResolvedValueOnce('{"v":1}')
    GoApp.ListOrgDecisionLog.mockResolvedValueOnce('{"v":1,"decisions":[]}')
    await api.pauseOrgAutonomy('growth')
    await api.listOrgDecisionLog('growth')
    expect(GoApp.PauseOrgAutonomy).toHaveBeenCalledWith('growth', '')
    expect(GoApp.ListOrgDecisionLog).toHaveBeenCalledWith('growth', '')
  })

  it('degrades a failed read to null and reports it', async () => {
    const seen = []
    const off = onApiError(d => seen.push(d))
    GoApp.ListNeedsYou.mockRejectedValueOnce(new Error('timeout'))
    await expect(api.listNeedsYou('growth')).resolves.toBeNull()
    expect(seen[0].op).toBe('needs you')
    off()
  })

  it('wires every group and status binding', async () => {
    for (const fn of ['StartOrgGroup', 'StopOrgGroup', 'OrgGroupStatus', 'GetDaemonStatus', 'GetOrgAutonomy',
      'ListOrgAutomations', 'ListUnassignedAutomations', 'AddOrgAutomation', 'RemoveOrgAutomation',
      'RemoveAutomationRole', 'GetEffectiveTools', 'ResumeOrgAutonomy']) {
      GoApp[fn].mockResolvedValueOnce('{"v":1}')
    }
    await api.startOrgGroup('hq')
    await api.stopOrgGroup('hq')
    await api.orgGroupStatus('hq')
    await api.getDaemonStatus()
    await api.getOrgAutonomy('g')
    await api.listOrgAutomations('g')
    await api.listUnassignedAutomations()
    await api.addOrgAutomation('g', 'wf1', 'pub')
    await api.removeOrgAutomation('g', 'pub')
    await api.removeAutomationRole('g', 'bot')
    await api.getEffectiveTools('g', 'lead')
    await api.resumeOrgAutonomy('g')
    expect(GoApp.AddOrgAutomation).toHaveBeenCalledWith('g', 'wf1', 'pub')
    expect(GoApp.GetEffectiveTools).toHaveBeenCalledWith('g', 'lead')
    expect(GoApp.StopOrgGroup).toHaveBeenCalledWith('hq')
  })
})
