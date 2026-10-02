// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
// The real i18n setup, as main.jsx loads it, so the dialog's words come from src/locales/*.json.
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'

// A stand-in for the key `api key create` prints once, built at run time so no
// key-shaped literal sits in the source.
const KEY = 'sk-ma-' + 'X'.repeat(43)

const App = {}
beforeEach(async () => {
  await i18n.changeLanguage('en')
  App.APIKeyCreate = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime; vi.restoreAllMocks() })

const created = (over = {}) => ({
  id: 'key_abcdefghijkl', profile_id: 'default', name: 'my-app', prefix: 'sk-ma-XXXXXX', context: false,
  created_at: '2026-10-02T09:00:00Z', last_used_at: '', revoked_at: '', key: KEY, ...over,
})

async function mount(props = {}) {
  const { default: ApiKeyDialog } = await import('./ApiKeyDialog.jsx')
  const onClose = vi.fn(); const onCreated = vi.fn()
  const ui = (p = {}) => <ApiKeyDialog open onClose={onClose} onCreated={onCreated} {...props} {...p} />
  const view = render(ui())
  return { onClose, onCreated, rerender: (p) => view.rerender(ui(p)) }
}
const nameInput = () => screen.getByLabelText('Name')
const submit = () => screen.getByRole('button', { name: 'Create key' })
async function createKey(name = 'my-app') {
  fireEvent.change(nameInput(), { target: { value: name } })
  fireEvent.click(submit())
  return screen.findByTestId('api-key-secret')
}

describe('ApiKeyDialog', () => {
  it('renders nothing while closed', async () => {
    await mount({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('opens on the name, which is required', async () => {
    await mount()
    const dialog = screen.getByRole('dialog', { name: 'Create an API key' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(nameInput()).toHaveFocus()
    expect(submit()).toBeDisabled()
    fireEvent.change(nameInput(), { target: { value: '   ' } })
    expect(submit()).toBeDisabled()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    expect(submit()).toBeEnabled()
    expect(App.APIKeyCreate).not.toHaveBeenCalled()
  })

  it('creates the key with the name and the context choice, and shows it once', async () => {
    App.APIKeyCreate.mockResolvedValue(created({ context: true }))
    const { onCreated } = await mount()
    fireEvent.change(nameInput(), { target: { value: ' notes bot ' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /Add this profile's knowledge/ }))
    fireEvent.click(submit())
    const secret = await screen.findByTestId('api-key-secret')
    expect(App.APIKeyCreate).toHaveBeenCalledTimes(1)
    expect(App.APIKeyCreate).toHaveBeenCalledWith('notes bot', true)
    expect(secret).toHaveValue(KEY)
    expect(secret).toHaveAttribute('readonly')
    expect(screen.getByRole('dialog', { name: 'Your new API key' })).toBeInTheDocument()
    expect(screen.getByText(/only time the key is shown/)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Copy key' })).toHaveFocus())
    // The form is gone: the name cannot be submitted again from the panel.
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument()
    // The parent learns of the new key, without its secret.
    expect(onCreated).toHaveBeenCalledTimes(1)
    expect(Object.keys(onCreated.mock.calls[0][0])).not.toContain('key')
    expect(onCreated.mock.calls[0][0]).toMatchObject({ id: 'key_abcdefghijkl', name: 'my-app', context: true })
    expect(JSON.stringify(onCreated.mock.calls)).not.toContain(KEY)
  })

  it('creates without context by default, and on Enter', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    await mount()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    fireEvent.submit(nameInput().closest('form'))
    await screen.findByTestId('api-key-secret')
    expect(App.APIKeyCreate).toHaveBeenCalledWith('my-app', false)
  })

  it('copies the key and says so, or says it could not', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    await mount()
    await createKey()
    fireEvent.click(screen.getByRole('button', { name: 'Copy key' }))
    await screen.findByText('Copied')
    expect(window.runtime.ClipboardSetText).toHaveBeenCalledWith(KEY)

    window.runtime.ClipboardSetText.mockResolvedValue(false)
    fireEvent.click(screen.getByRole('button', { name: 'Copy key' }))
    await screen.findByText(/Couldn't copy: select the key/)
    expect(screen.queryByText('Copied')).not.toBeInTheDocument()
  })

  it('Done clears the key even if the parent keeps the dialog open', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    const { onClose } = await mount()
    await createKey()
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('api-key-secret')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(KEY)
    expect(Array.from(document.querySelectorAll('input')).map(i => i.value)).not.toContain(KEY)
    // ... and the form is empty again.
    expect(nameInput()).toHaveValue('')
  })

  it('Escape closes the panel and clears the key too', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    const { onClose } = await mount()
    await createKey()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('api-key-secret')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(KEY)
  })

  it('a click outside closes the form but never the panel that holds the key', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    const { onClose } = await mount()
    const overlay = () => screen.getByRole('dialog').parentElement
    fireEvent.click(overlay())
    expect(onClose).toHaveBeenCalledTimes(1)

    await createKey()
    fireEvent.click(overlay())
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('api-key-secret')).toHaveValue(KEY)
  })

  it('holds nothing once closed from outside: reopening shows an empty form', async () => {
    App.APIKeyCreate.mockResolvedValue(created())
    const { rerender } = await mount()
    await createKey()
    rerender({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(KEY)
    rerender({ open: true })
    expect(screen.queryByTestId('api-key-secret')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(KEY)
    expect(nameInput()).toHaveValue('')
  })

  it('says in words that a name is taken, keeps the form, and tells the parent nothing', async () => {
    App.APIKeyCreate.mockRejectedValue(new Error('invalid_input: an active key with that name already exists in this profile'))
    const { onCreated } = await mount()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    fireEvent.click(submit())
    expect(await screen.findByRole('alert')).toHaveTextContent(en.settings.api.errors.nameTaken)
    expect(nameInput()).toHaveValue('my-app')
    expect(submit()).toBeEnabled()
    expect(screen.queryByTestId('api-key-secret')).not.toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
  })

  it('words the failure in the chosen language, and gives what it cannot word as the CLI said it', async () => {
    await act(() => i18n.changeLanguage('es'))
    App.APIKeyCreate.mockRejectedValueOnce(new Error('invalid_input: an active key with that name already exists in this profile'))
    await mount()
    fireEvent.change(screen.getByLabelText(es.settings.api.create.name), { target: { value: 'my-app' } })
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.create.submit }))
    expect(await screen.findByRole('alert')).toHaveTextContent(es.settings.api.errors.nameTaken)
    App.APIKeyCreate.mockRejectedValueOnce(new Error('database is locked'))
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.create.submit }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('database is locked'))
    App.APIKeyCreate.mockRejectedValueOnce(new Error('invalid_input: unknown flag: --nope'))
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.create.submit }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/^unknown flag: --nope$/))
  })

  it('says so when the CLI returns no key, and shows no empty panel', async () => {
    App.APIKeyCreate.mockResolvedValue(created({ key: undefined }))
    await mount()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    fireEvent.click(submit())
    expect(await screen.findByRole('alert')).toHaveTextContent('did not return the key')
    expect(screen.queryByTestId('api-key-secret')).not.toBeInTheDocument()
  })

  it('does not submit twice while the CLI works', async () => {
    let resolve
    App.APIKeyCreate.mockReturnValue(new Promise(r => { resolve = r }))
    await mount()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    fireEvent.click(submit())
    expect(await screen.findByRole('button', { name: 'Creating…' })).toBeDisabled()
    fireEvent.submit(nameInput().closest('form'))
    expect(App.APIKeyCreate).toHaveBeenCalledTimes(1)
    await act(async () => { resolve(created()) })
    await screen.findByTestId('api-key-secret')
  })

  it('cannot be closed while the CLI works, so a key is never created for nobody', async () => {
    let resolve
    App.APIKeyCreate.mockReturnValue(new Promise(r => { resolve = r }))
    const { onClose } = await mount()
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    fireEvent.click(submit())
    await screen.findByRole('button', { name: 'Creating…' })
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.click(screen.getByRole('dialog').parentElement)
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(onClose).not.toHaveBeenCalled()
    await act(async () => { resolve(created()) })
    expect(await screen.findByTestId('api-key-secret')).toHaveValue(KEY)
  })

  it('keeps the key out of storage, the console and the page address', async () => {
    const stored = []
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation((...a) => { stored.push(a) })
    const logged = []
    for (const m of ['log', 'info', 'warn', 'error', 'debug']) vi.spyOn(console, m).mockImplementation((...a) => { logged.push(a) })
    App.APIKeyCreate.mockResolvedValue(created())
    await mount()
    await createKey()
    fireEvent.click(screen.getByRole('button', { name: 'Copy key' }))
    await screen.findByText('Copied')
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(JSON.stringify(stored)).not.toContain(KEY)
    expect(JSON.stringify(logged)).not.toContain(KEY)
    expect(window.location.href).not.toContain(KEY)
    expect(document.title).not.toContain(KEY)
  })

  it('keeps Tab inside the dialog', async () => {
    await mount()
    const dialog = screen.getByRole('dialog')
    const buttons = within(dialog).getAllByRole('button')
    const last = buttons[buttons.length - 1]
    fireEvent.change(nameInput(), { target: { value: 'my-app' } })
    last.focus()
    fireEvent.keyDown(dialog, { key: 'Tab' })
    expect(nameInput()).toHaveFocus()
    nameInput().focus()
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true })
    expect(last).toHaveFocus()
  })

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mount()
    expect(screen.getByRole('dialog', { name: es.settings.api.create.title })).toBeInTheDocument()
    expect(screen.getByLabelText(es.settings.api.create.name)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: es.settings.api.create.submit })).toBeInTheDocument()
  })
})
