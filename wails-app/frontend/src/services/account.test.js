import { describe, it, expect, vi, beforeEach } from 'vitest'

const go = vi.hoisted(() => ({
  AccountStatus: vi.fn(), AccountLogin: vi.fn(), AccountLoginCancel: vi.fn(),
  AccountLoginEmailSend: vi.fn(), AccountLoginEmailVerify: vi.fn(), AccountLogout: vi.fn(),
}))
vi.mock('../wailsjs/go/main/App', () => go)

import { account, answerOf } from './account.js'
import { viewOf } from '../lib/accountGate.js'

const OK = { v: 1, state: 'ok', reason: '', plan: 'free', enforced: true }
const failure = (cause, message = '', over = {}) => ({ failure: { cause, message, enforced: true, ...over } })

describe('answerOf: the gate fails closed', () => {
  it('takes a status document of schema 1 in each known state', () => {
    for (const state of ['ok', 'grace', 'locked']) expect(answerOf(JSON.stringify({ ...OK, state }))).toEqual({ status: { ...OK, state } })
  })

  it('takes the Go side\'s coded failures by their cause, with what it says of enforcement', () => {
    for (const code of ['cli_not_found', 'cli_too_old', 'cli_failed']) {
      expect(answerOf(JSON.stringify({ error: 'it said', code, enforced: true }))).toEqual(failure(code, 'it said', { enforce_from: undefined }))
    }
    const early = { error: 'x', code: 'cli_too_old', enforced: false, enforce_from: '2026-10-26T00:00:00Z' }
    expect(answerOf(JSON.stringify(early)).failure).toMatchObject({ enforced: false, enforce_from: '2026-10-26T00:00:00Z' })
    expect(answerOf(JSON.stringify({ error: 'x', code: 'cli_failed' })).failure.enforced).toBe(true) // silent: enforced
  })

  it('reads anything else as a CLI this app cannot talk to, never as a status', () => {
    for (const raw of [{ v: 2, state: 'ok' }, { v: 1, state: 'paused' }, { v: 1 }, { state: 'ok' }, {}, [], null, 42]) {
      expect(answerOf(JSON.stringify(raw)), JSON.stringify(raw)).toEqual(failure('cli_too_old'))
    }
    expect(answerOf(JSON.stringify({ error: 'busy', code: 'busy' })).failure.cause).toBe('cli_failed')
    expect(answerOf('Usage: monoagentcli').failure.cause).toBe('cli_failed')
  })
})

describe('account', () => {
  beforeEach(() => { Object.values(go).forEach(f => f.mockReset()) })

  it('status resolves to the status of a good answer', async () => {
    go.AccountStatus.mockResolvedValue(JSON.stringify(OK))
    expect(await account.status()).toEqual({ status: OK })
  })

  it('a binding that is missing, throws or answers a non-object defers to dormant: not enforced, so not locked', async () => {
    go.AccountStatus.mockRejectedValue(new Error('bridge down'))
    expect(await account.status()).toEqual(failure('cli_failed', 'bridge down', { enforced: false }))
    go.AccountStatus.mockImplementation(() => { throw new TypeError('AccountStatus is not a function') })
    expect((await account.status()).failure).toMatchObject({ enforced: false })
    for (const v of [undefined, null, 42]) {
      go.AccountStatus.mockResolvedValue(v)
      const view = viewOf({ phase: 'ready', ...(await account.status()) })
      expect(view.locked, String(v)).toBe(false)
      expect(view.warn).toBe(false)
    }
  })

  it('a real answer of enforced:true with a state it does not know stays locked (fail closed)', async () => {
    go.AccountStatus.mockResolvedValue(JSON.stringify({ ...OK, state: 'paused' }))
    const answer = await account.status()
    expect(answer.failure.enforced).toBe(true)
    expect(viewOf({ phase: 'ready', ...answer }).locked).toBe(true)
    go.AccountStatus.mockResolvedValue(JSON.stringify({ error: 'boom', code: 'cli_failed', enforced: true }))
    expect(viewOf({ phase: 'ready', ...(await account.status()) }).locked).toBe(true)
  })

  it('sign-in calls the Account bindings and returns their JSON, or {error}', async () => {
    go.AccountLogin.mockResolvedValue('{"v":1,"state":"ok"}')
    go.AccountLoginEmailVerify.mockRejectedValue(new Error('offline'))
    expect(await account.login()).toEqual({ v: 1, state: 'ok' })
    expect(await account.verifyCode('a@b.c', '123')).toEqual({ error: 'offline' })
    await account.sendCode('a@b.c'); await account.cancelLogin(); await account.logout()
    expect(go.AccountLoginEmailSend).toHaveBeenCalledWith('a@b.c')
    expect(go.AccountLoginCancel).toHaveBeenCalledTimes(1)
    expect(go.AccountLogout).toHaveBeenCalledTimes(1)
  })
})
