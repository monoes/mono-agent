// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../i18n.js'
import es from '../../locales/es.json'
import { mainListener, dedicatedListener, statusOf, modelsDoc, keyList, MISSING_SURFACE } from './api/__fixtures__/apiFixtures.js'

const mockConfirm = vi.fn()
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))

const KEY = 'sk-ma-' + 'X'.repeat(43) // built at run time: no key-shaped literal in the source

// The section imports the generated bindings, which call window.go.main.App.
const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIStatus', 'APIKeyList', 'APIModels', 'APIKeyCreate', 'APIKeySetContext', 'APIKeyRevoke']) App[k] = vi.fn()
  App.APIStatus.mockResolvedValue(statusOf([mainListener()]))
  App.APIKeyList.mockResolvedValue(keyList())
  App.APIModels.mockResolvedValue(modelsDoc())
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

async function mount(props = {}) {
  const { default: ApiSection } = await import('./ApiSection.jsx')
  const onNavigate = vi.fn()
  render(<ApiSection onNavigate={onNavigate} {...props} />)
  return { onNavigate }
}
const toggle = () => screen.getByTestId('api-fold-toggle')
// Lets what a render scheduled (effects, promises) run, so that a "was not called" holds.
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })

describe('ApiSection: folded', () => {
  it('is folded by default, reads only the status, and shows it in the header', async () => {
    await mount()
    expect(toggle()).toHaveAttribute('aria-expanded', 'false')
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    await settle()
    // keys.active, since the keys are not read yet
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('2 keys')
    expect(App.APIStatus).toHaveBeenCalledTimes(1)
    expect(App.APIKeyList).not.toHaveBeenCalled()
    expect(App.APIModels).not.toHaveBeenCalled()
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
  })

  it('shows loading and then an error in the header when the status cannot be read', async () => {
    let reject
    App.APIStatus.mockReturnValue(new Promise((_, r) => { reject = r }))
    await mount()
    expect(await screen.findByTestId('api-fold-state')).toHaveTextContent('Loading…')
    await act(async () => { reject(new Error('unknown command "api" for "monoagentcli"')) })
    expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Error reading status')
    expect(screen.queryByTestId('api-fold-keys')).not.toBeInTheDocument()
  })

  it('says a stopped server in the header, and one key in the singular', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ reachable: false, v1_answers: false })], { keys: { active: 1 } }))
    await mount()
    expect(await screen.findByTestId('api-fold-state')).toHaveTextContent('Not running')
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('1 key')
  })
})

describe('ApiSection: expanding', () => {
  it('reads the keys and the models on the first expand, for the listener the header describes', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
    App.APIModels.mockResolvedValue(modelsDoc({ confinement: 'chat-only', forListener: 'network' }))
    await mount()
    await screen.findByTestId('api-fold-state')
    fireEvent.click(toggle())
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    expect(await screen.findByTestId('api-base-url')).toHaveTextContent('https://localhost:9443/v1')
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(1))
    expect(App.APIModels).toHaveBeenCalledWith('network', 'chat-only', 'chat-only', 'chat-only')
    expect(App.APIKeyList).toHaveBeenCalledTimes(1)
    expect(await screen.findByRole('table', { name: 'API keys' })).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(screen.getByText('my-app')).toBeInTheDocument()
    // Folding and expanding again reads nothing more.
    fireEvent.click(toggle()); fireEvent.click(toggle())
    expect(App.APIStatus).toHaveBeenCalledTimes(1)
    expect(App.APIKeyList).toHaveBeenCalledTimes(1)
    expect(App.APIModels).toHaveBeenCalledTimes(1)
  })

  it('expanded from the start reads the status first, and the models for its policy', async () => {
    await mount({ defaultExpanded: true })
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(1))
    expect(App.APIModels).toHaveBeenCalledWith('loopback', 'any', 'chat-only', 'chat-only')
    expect(App.APIStatus.mock.invocationCallOrder[0]).toBeLessThan(App.APIModels.mock.invocationCallOrder[0])
    expect(await screen.findByText('notes bot')).toBeInTheDocument()
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('2 keys') // from the list now
  })

  it('reads the models with the CLI\'s own defaults when no listener serves /v1, and when the status failed', async () => {
    App.APIStatus.mockResolvedValue(statusOf([]))
    await mount({ defaultExpanded: true })
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledWith('', '', '', ''))
    cleanup(); vi.clearAllMocks()
    App.APIStatus.mockRejectedValue(new Error('boom'))
    App.APIKeyList.mockResolvedValue([])
    App.APIModels.mockResolvedValue(modelsDoc())
    await mount({ defaultExpanded: true })
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledWith('', '', '', ''))
    expect(await screen.findByText(/Couldn't read the API status: boom/)).toBeInTheDocument()
    // The keys and the models are still shown: one failure does not blank the rest.
    expect(await screen.findByText(/No API keys yet/)).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
  })

  it('shows what each part could not read, with its own retry', async () => {
    App.APIKeyList.mockRejectedValueOnce(new Error('no such table: api_keys'))
    App.APIModels.mockRejectedValueOnce(new Error('monomind not found'))
    await mount({ defaultExpanded: true })
    expect(await screen.findByText(/Couldn't read the keys: no such table: api_keys/)).toBeInTheDocument()
    expect(await screen.findByText("Couldn't list the models: monomind not found")).toBeInTheDocument()
    expect(screen.getByTestId('api-base-url')).toBeInTheDocument() // the status is fine

    const keysBlock = screen.getByText(/Couldn't read the keys/).parentElement
    fireEvent.click(within(keysBlock).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('notes bot')).toBeInTheDocument()
    expect(App.APIKeyList).toHaveBeenCalledTimes(2)
    expect(App.APIModels).toHaveBeenCalledTimes(1)

    const modelsBlock = screen.getByText(/Couldn't list the models/).parentElement
    fireEvent.click(within(modelsBlock).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(App.APIModels).toHaveBeenCalledTimes(2)
    expect(App.APIModels).toHaveBeenLastCalledWith('loopback', 'any', 'chat-only', 'chat-only')
  })

  it('refreshes the status, the keys and the models together, once', async () => {
    await mount({ defaultExpanded: true })
    await screen.findByText('notes bot')
    await screen.findByRole('table', { name: 'Models' })
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ reachable: false, v1_answers: false })]))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled())
    expect(screen.getByTestId('api-state')).toHaveTextContent('Not running')
  })

  it('retries the status from its error line', async () => {
    App.APIStatus.mockRejectedValueOnce(new Error('boom'))
    await mount({ defaultExpanded: true })
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('api-base-url')).toBeInTheDocument()
    expect(App.APIStatus).toHaveBeenCalledTimes(2)
  })
})

describe('ApiSection: keys', () => {
  it('reloads the keys after a create, while the new key is still shown only in its dialog', async () => {
    await mount({ defaultExpanded: true })
    await screen.findByText('notes bot')
    const created = { id: 'key_zzzzzzzzzzzz', profile_id: 'default', name: 'fresh', prefix: 'sk-ma-ZzZzZz', context: false,
      created_at: new Date().toISOString(), last_used_at: '', revoked_at: '', key: KEY }
    App.APIKeyCreate.mockResolvedValue(created)
    App.APIKeyList.mockResolvedValue([...keyList(), { ...created, key: undefined }])
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'fresh' } })
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Create key' }))
    expect(await screen.findByTestId('api-key-secret')).toHaveValue(KEY)
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
    expect(await screen.findByText('fresh')).toBeInTheDocument()
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('3 keys')
    expect(within(screen.getByRole('table', { name: 'API keys' })).queryByText(KEY)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(document.body.textContent).not.toContain(KEY)
  })

  it('reloads the keys after a revoke, and the header count follows', async () => {
    mockConfirm.mockResolvedValue(true)
    await mount({ defaultExpanded: true })
    await screen.findByText('notes bot')
    App.APIKeyRevoke.mockResolvedValue({ id: 'key_mnopqrstuvwx', revoked_at: '2026-10-02T10:00:00Z' })
    App.APIKeyList.mockResolvedValue(keyList().slice(0, 1))
    fireEvent.click(screen.getByRole('button', { name: 'Revoke notes bot' }))
    await waitFor(() => expect(App.APIKeyRevoke).toHaveBeenCalledWith('key_mnopqrstuvwx'))
    await waitFor(() => expect(screen.queryByText('notes bot')).not.toBeInTheDocument())
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('1 key')
  })

  it('names the context cap of the listener under the keys', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ context_confinement: 'sandboxed' })]))
    await mount({ defaultExpanded: true })
    expect(await screen.findByText(/Context adds excerpts/)).toHaveTextContent('up to sandboxed')
  })
})

describe('ApiSection: auto and the Jev settings', () => {
  it('jumps to the Jev settings from an auto that is off for want of Jev', async () => {
    App.APIModels.mockResolvedValue({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } })
    const { onNavigate } = await mount({ defaultExpanded: true })
    fireEvent.click(await screen.findByRole('button', { name: 'Open the Jev settings' }))
    expect(onNavigate).toHaveBeenCalledTimes(1)
    expect(onNavigate).toHaveBeenCalledWith('settings', { section: 'jev' })
  })

  it('has no jump when it is not given a way to navigate', async () => {
    App.APIModels.mockResolvedValue({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } })
    await mount({ defaultExpanded: true, onNavigate: undefined })
    await screen.findByText(/Off\. It needs/)
    expect(screen.queryByRole('button', { name: 'Open the Jev settings' })).not.toBeInTheDocument()
  })
})

describe('ApiSection: the rest', () => {
  it('is one button, named by what it shows, that unfolds the section', async () => {
    await mount()
    await screen.findByTestId('api-fold-state')
    expect(toggle().tagName).toBe('BUTTON')
    expect(toggle()).toHaveAccessibleName(/OpenAI-compatible API\s+Running\s+2 keys/)
    fireEvent.click(toggle())
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    expect(document.getElementById(toggle().getAttribute('aria-controls'))).toBeInTheDocument()
  })

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mount({ defaultExpanded: true })
    expect(await screen.findByTestId('api-fold-state')).toHaveTextContent(es.settings.api.status.stateServing)
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('2 claves')
    expect(screen.getByRole('button', { name: es.settings.api.keys.create })).toBeInTheDocument()
    expect(screen.getAllByText(es.settings.api.sectionTitle).length).toBeGreaterThan(0)
  })
})
