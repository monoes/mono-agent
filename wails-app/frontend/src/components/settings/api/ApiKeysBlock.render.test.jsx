// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'

const mockConfirm = vi.fn()
vi.mock('../../ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))

const KEY = 'sk-ma-' + 'X'.repeat(43) // built at run time: no key-shaped literal in the source

const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  App.APIKeySetContext = vi.fn()
  App.APIKeyRevoke = vi.fn()
  App.APIKeyCreate = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

const ago = (ms) => new Date(Date.now() - ms).toISOString()
// What `api key list --json` gives, through APIKeyList: never-used is "" and not null.
const keys = () => [
  { id: 'key_abcdefghijkl', profile_id: 'default', name: 'my-app', prefix: 'sk-ma-AbCdEf', context: false,
    created_at: '2026-09-20T12:00:00Z', last_used_at: ago(3 * 60 * 1000), revoked_at: '' },
  { id: 'key_mnopqrstuvwx', profile_id: 'default', name: 'notes bot', prefix: 'sk-ma-GhIjKl', context: true,
    created_at: '2026-10-01T12:00:00Z', last_used_at: '', revoked_at: '' },
]

async function mount(props = {}) {
  const { default: ApiKeysBlock } = await import('./ApiKeysBlock.jsx')
  const onChanged = props.onChanged || vi.fn(); const onRetry = vi.fn()
  render(<ApiKeysBlock keys={keys()} err="" contextClasses={['chat-only']} onRetry={onRetry} {...props} onChanged={onChanged} />)
  return { onChanged, onRetry }
}
const row = (name) => screen.getByRole('row', { name: new RegExp(name) })
// Lets what a click scheduled (a confirmation, a call) run, so that a "was not called" holds.
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })

describe('ApiKeysBlock', () => {
  it('lists each key with its prefix, dates and context switch', async () => {
    await mount()
    const table = screen.getByRole('table', { name: 'API keys' })
    for (const h of ['Name', 'Key', 'Context', 'Created', 'Last used']) expect(within(table).getByRole('columnheader', { name: h })).toBeInTheDocument()
    const one = row('my-app')
    // A long name is cut to the column, and its tooltip says all of it.
    expect(within(one).getByText('my-app')).toHaveAttribute('title', 'my-app')
    expect(within(one).getByText('sk-ma-AbCdEf…')).toBeInTheDocument()
    expect(within(one).getByText(/Sep 20, 2026/)).toBeInTheDocument()
    expect(within(one).getByText('3 minutes ago')).toBeInTheDocument()
    expect(within(one).getByRole('switch', { name: 'Context for my-app' })).toHaveAttribute('aria-checked', 'false')
    const two = row('notes bot')
    expect(within(two).getByText('never')).toBeInTheDocument()
    expect(within(two).getByRole('switch', { name: 'Context for notes bot' })).toHaveAttribute('aria-checked', 'true')
  })

  it('says what context adds and which models such a key may use', async () => {
    await mount({ contextClasses: ['sandboxed'] })
    expect(screen.getByText(/Context adds excerpts of this profile's documents and captures/)).toHaveTextContent('up to sandboxed')
    cleanup()
    // Nothing known of the listeners (or no listener serving): the CLI's own default.
    await mount({ contextClasses: [] })
    expect(screen.getByText(/Context adds excerpts/)).toHaveTextContent('up to chat-only')
  })

  it('does not name one class when the listeners that serve /v1 allow different ones', async () => {
    await mount({ contextClasses: ['chat-only', 'sandboxed'] })
    const text = screen.getByText(/Context adds excerpts/)
    expect(text).toHaveTextContent('up to the class each listener allows (--context-confinement, see above)')
    expect(text).not.toHaveTextContent(/up to (chat-only|sandboxed)/)
  })

  it('shows an empty state, a loading state and a load error with a retry', async () => {
    await mount({ keys: [] })
    expect(screen.getByText(/No API keys yet/)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create key' })).toBeInTheDocument()
    cleanup()
    await mount({ keys: null })
    expect(screen.getByText('Loading…')).toBeInTheDocument()
    cleanup()
    const { onRetry } = await mount({ keys: null, err: 'no such table: api_keys' })
    expect(screen.getByText(/Couldn't read the keys: no such table: api_keys/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('switches context through the CLI, one key at a time, then asks for a reload', async () => {
    mockConfirm.mockResolvedValue(true)
    let resolve
    App.APIKeySetContext.mockReturnValue(new Promise(r => { resolve = r }))
    const { onChanged } = await mount()
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    await waitFor(() => expect(App.APIKeySetContext).toHaveBeenCalledWith('key_abcdefghijkl', true))
    // While it runs, nothing else on the list can be started.
    await waitFor(() => expect(within(row('notes bot')).getByRole('switch')).toBeDisabled())
    expect(within(row('my-app')).getByRole('button', { name: 'Revoke my-app' })).toBeDisabled()
    expect(onChanged).not.toHaveBeenCalled()
    await act(async () => { resolve({ id: 'key_abcdefghijkl', context: true }) })
    expect(onChanged).toHaveBeenCalledTimes(1)
    expect(within(row('notes bot')).getByRole('switch')).toBeEnabled()

    // Switching off sends false, and asks nothing.
    App.APIKeySetContext.mockResolvedValue({})
    fireEvent.click(within(row('notes bot')).getByRole('switch'))
    expect(App.APIKeySetContext).toHaveBeenLastCalledWith('key_mnopqrstuvwx', false)
    expect(mockConfirm).toHaveBeenCalledTimes(1) // only for the one that turned it on
  })

  it('keeps every control busy until the list has been read again: no second click lands on a stale row', async () => {
    mockConfirm.mockResolvedValue(true)
    App.APIKeySetContext.mockResolvedValue({})
    let reloaded
    const onChanged = vi.fn(() => new Promise(r => { reloaded = r }))
    await mount({ onChanged })
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    // The CLI has answered and the list is not read yet: it still shows the old value, and nothing may be started on it.
    expect(within(row('my-app')).getByRole('switch')).toBeDisabled()
    expect(within(row('notes bot')).getByRole('switch')).toBeDisabled()
    expect(within(row('my-app')).getByRole('button', { name: 'Revoke my-app' })).toBeDisabled()
    await act(async () => { reloaded() })
    expect(within(row('my-app')).getByRole('switch')).toBeEnabled()
    expect(within(row('notes bot')).getByRole('button', { name: 'Revoke notes bot' })).toBeEnabled()
  })

  it('shows what the CLI says when a switch fails, and reads the list again: the key may be gone', async () => {
    mockConfirm.mockResolvedValue(true)
    App.APIKeySetContext.mockRejectedValue(new Error('not_found: api key not found'))
    const { onChanged } = await mount()
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    expect(await screen.findByRole('alert')).toHaveTextContent(en.settings.api.errors.keyNotFound)
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    expect(within(row('my-app')).getByRole('switch')).toBeEnabled()
    expect(screen.getByRole('alert')).toHaveTextContent(en.settings.api.errors.keyNotFound) // the reload does not wipe it
  })

  it('keeps the controls busy through the reload after a failure too', async () => {
    mockConfirm.mockResolvedValue(true)
    App.APIKeySetContext.mockRejectedValue(new Error('boom'))
    let reloaded
    const onChanged = vi.fn(() => new Promise(r => { reloaded = r }))
    await mount({ onChanged })
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    expect(within(row('notes bot')).getByRole('switch')).toBeDisabled()
    await act(async () => { reloaded() })
    expect(within(row('notes bot')).getByRole('switch')).toBeEnabled()
  })

  describe('turning context on', () => {
    it('asks first, in the words of the create dialog, and does nothing when declined', async () => {
      mockConfirm.mockResolvedValueOnce(false)
      const { onChanged } = await mount()
      fireEvent.click(within(row('my-app')).getByRole('switch'))
      await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
      const [message, opts] = mockConfirm.mock.calls[0]
      expect(message).toBe(en.settings.api.create.contextHint)
      expect(message).toMatch(/so they reach the model's provider/)
      expect(opts).toMatchObject({ title: 'Turn on context for my-app?', confirmLabel: 'Turn on context', danger: false })
      await settle()
      expect(App.APIKeySetContext).not.toHaveBeenCalled()
      expect(onChanged).not.toHaveBeenCalled()
      expect(within(row('my-app')).getByRole('switch')).toHaveAttribute('aria-checked', 'false')
      expect(within(row('my-app')).getByRole('switch')).toBeEnabled()
    })

    it('turns it on after a yes', async () => {
      mockConfirm.mockResolvedValueOnce(true)
      App.APIKeySetContext.mockResolvedValue({})
      const { onChanged } = await mount()
      fireEvent.click(within(row('my-app')).getByRole('switch'))
      await waitFor(() => expect(App.APIKeySetContext).toHaveBeenCalledWith('key_abcdefghijkl', true))
      await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    })

    it('does not ask when turning it off: that only takes the excerpts away', async () => {
      App.APIKeySetContext.mockResolvedValue({})
      await mount()
      fireEvent.click(within(row('notes bot')).getByRole('switch'))
      await waitFor(() => expect(App.APIKeySetContext).toHaveBeenCalledWith('key_mnopqrstuvwx', false))
      expect(mockConfirm).not.toHaveBeenCalled()
    })

    it('asks in the chosen language', async () => {
      await act(() => i18n.changeLanguage('es'))
      mockConfirm.mockResolvedValueOnce(false)
      await mount()
      fireEvent.click(within(row('my-app')).getByRole('switch'))
      await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
      const [message, opts] = mockConfirm.mock.calls[0]
      expect(message).toBe(es.settings.api.create.contextHint)
      expect(opts.title).toBe(es.settings.api.keys.contextOnTitle.replace('{{name}}', 'my-app'))
      expect(opts.confirmLabel).toBe(es.settings.api.keys.contextOnConfirm)
    })
  })

  it('revokes only after the confirmation says yes', async () => {
    mockConfirm.mockResolvedValueOnce(false)
    const { onChanged } = await mount()
    fireEvent.click(within(row('my-app')).getByRole('button', { name: 'Revoke my-app' }))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
    const [message, opts] = mockConfirm.mock.calls[0]
    expect(message).toBe('Programs that use "my-app" stop working at their next request. This can\'t be undone.')
    expect(opts).toMatchObject({ title: 'Revoke this key?', confirmLabel: 'Revoke key', danger: true })
    expect(App.APIKeyRevoke).not.toHaveBeenCalled()
    expect(onChanged).not.toHaveBeenCalled()

    mockConfirm.mockResolvedValueOnce(true)
    App.APIKeyRevoke.mockResolvedValue({ id: 'key_abcdefghijkl', revoked_at: '2026-10-02T10:00:00Z' })
    fireEvent.click(within(row('my-app')).getByRole('button', { name: 'Revoke my-app' }))
    await waitFor(() => expect(App.APIKeyRevoke).toHaveBeenCalledWith('key_abcdefghijkl'))
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
  })

  it('shows what the CLI says when a revoke fails, in the chosen language when it can', async () => {
    mockConfirm.mockResolvedValue(true)
    App.APIKeyRevoke.mockRejectedValueOnce(new Error('database is locked'))
    const { onChanged } = await mount()
    fireEvent.click(within(row('notes bot')).getByRole('button', { name: 'Revoke notes bot' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('database is locked')
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1)) // read again: a failed revoke may still have happened

    await act(() => i18n.changeLanguage('es'))
    App.APIKeyRevoke.mockRejectedValueOnce(new Error('not_found: api key not found'))
    fireEvent.click(within(row('notes bot')).getByRole('button', { name: 'Revocar notes bot' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(es.settings.api.errors.keyNotFound))
  })

  it('opens the create dialog, and reloads the list once a key is created while the key is still shown', async () => {
    App.APIKeyCreate.mockResolvedValue({
      id: 'key_zzzzzzzzzzzz', profile_id: 'default', name: 'fresh', prefix: 'sk-ma-ZzZzZz', context: false,
      created_at: ago(1000), last_used_at: '', revoked_at: '', key: KEY,
    })
    const { onChanged } = await mount()
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
    const dialog = screen.getByRole('dialog', { name: 'Create an API key' })
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'fresh' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    expect(await screen.findByTestId('api-key-secret')).toHaveValue(KEY)
    expect(onChanged).toHaveBeenCalledTimes(1)
    // The list never holds the key: only the dialog does, until Done.
    expect(within(screen.getByRole('table')).queryByText(KEY)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(KEY)
  })

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mount()
    const a = es.settings.api.keys
    expect(screen.getByRole('table', { name: a.title })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: a.colLastUsed })).toBeInTheDocument()
    expect(screen.getByText('hace 3 minutos')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: a.contextLabel.replace('{{name}}', 'my-app') })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: a.create })).toBeInTheDocument()
  })
})
