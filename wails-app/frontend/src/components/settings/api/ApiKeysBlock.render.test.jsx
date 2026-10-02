// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
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
  const onChanged = vi.fn(); const onRetry = vi.fn()
  render(<ApiKeysBlock keys={keys()} err="" contextClass="chat-only" onChanged={onChanged} onRetry={onRetry} {...props} />)
  return { onChanged, onRetry }
}
const row = (name) => screen.getByRole('row', { name: new RegExp(name) })

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
    await mount({ contextClass: 'sandboxed' })
    expect(screen.getByText(/Context adds excerpts of this profile's documents and captures/)).toHaveTextContent('up to sandboxed')
    cleanup()
    await mount({ contextClass: undefined })
    expect(screen.getByText(/Context adds excerpts/)).toHaveTextContent('up to chat-only')
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
    let resolve
    App.APIKeySetContext.mockReturnValue(new Promise(r => { resolve = r }))
    const { onChanged } = await mount()
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    expect(App.APIKeySetContext).toHaveBeenCalledWith('key_abcdefghijkl', true)
    // While it runs, nothing else on the list can be started.
    await waitFor(() => expect(within(row('notes bot')).getByRole('switch')).toBeDisabled())
    expect(within(row('my-app')).getByRole('button', { name: 'Revoke my-app' })).toBeDisabled()
    expect(onChanged).not.toHaveBeenCalled()
    await act(async () => { resolve({ id: 'key_abcdefghijkl', context: true }) })
    expect(onChanged).toHaveBeenCalledTimes(1)
    expect(within(row('notes bot')).getByRole('switch')).toBeEnabled()

    // Switching off sends false.
    App.APIKeySetContext.mockResolvedValue({})
    fireEvent.click(within(row('notes bot')).getByRole('switch'))
    expect(App.APIKeySetContext).toHaveBeenLastCalledWith('key_mnopqrstuvwx', false)
  })

  it('shows what the CLI says when a switch fails, and does not reload', async () => {
    App.APIKeySetContext.mockRejectedValue(new Error('api key not found'))
    const { onChanged } = await mount()
    fireEvent.click(within(row('my-app')).getByRole('switch'))
    expect(await screen.findByRole('alert')).toHaveTextContent('api key not found')
    expect(onChanged).not.toHaveBeenCalled()
    expect(within(row('my-app')).getByRole('switch')).toBeEnabled()
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

  it('shows what the CLI says when a revoke fails', async () => {
    mockConfirm.mockResolvedValue(true)
    App.APIKeyRevoke.mockRejectedValue(new Error('api key not found'))
    const { onChanged } = await mount()
    fireEvent.click(within(row('notes bot')).getByRole('button', { name: 'Revoke notes bot' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('api key not found')
    expect(onChanged).not.toHaveBeenCalled()
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
