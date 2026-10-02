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
  const ui = (p = {}) => <ApiSection onNavigate={onNavigate} {...props} {...p} />
  const view = render(ui())
  return { onNavigate, rerender: (p) => view.rerender(ui(p)) }
}
const toggle = () => screen.getByTestId('api-fold-toggle')
// What a person does: the section is there, its header has what the status said, and they unfold it.
async function mountExpanded(props = {}) {
  const m = await mount(props)
  await waitFor(() => expect(screen.getByTestId('api-fold-state')).not.toHaveTextContent('Loading…'))
  fireEvent.click(toggle())
  return m
}
// Lets what a render scheduled (effects, promises) run, so that a "was not called" holds.
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })
const down = { reachable: false, v1_answers: false }

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
    App.APIStatus.mockResolvedValue(statusOf([mainListener(down)], { keys: { active: 1 } }))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running'))
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('1 key')
  })

  it('says a server that does not answer while the daemon runs as not answering', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener(down)], { daemon: { running: true } }))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not answering'))
  })

  it('has no exposure chip when /v1 is bound to loopback only', async () => {
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    expect(screen.queryByTestId('api-fold-exposure')).not.toBeInTheDocument()
  })

  it('says Network in the header when any listener that serves /v1 is bound beyond loopback, whichever one answers', async () => {
    // The loopback one answers and is the one the header's state describes; the network one must not vanish behind it.
    App.APIStatus.mockResolvedValue(statusOf([mainListener(), dedicatedListener(down)]))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    expect(screen.getByTestId('api-fold-exposure')).toHaveTextContent('Network')
    expect(toggle()).toHaveAccessibleName(/OpenAI-compatible API\s+Running\s+Network\s+2 keys/)
  })

  it('describes in the header the listener that answers /v1, not the first one that is listed', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener(down), dedicatedListener()]))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
  })

  it('says nothing of exposure once the status could not be read again: the header has an error, not stale news', async () => {
    App.APIStatus.mockResolvedValue(statusOf([dedicatedListener()]))
    await mountExpanded()
    await screen.findByTestId('api-fold-exposure')
    App.APIStatus.mockRejectedValueOnce(new Error('boom'))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Error reading status'))
    expect(screen.queryByTestId('api-fold-exposure')).not.toBeInTheDocument()
  })

  it('has no exposure chip for a listener that does not serve /v1, however it is bound', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ addr: '0.0.0.0:9322', loopback: false, v1: false })]))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not serving /v1'))
    expect(screen.queryByTestId('api-fold-exposure')).not.toBeInTheDocument()
  })
})

describe('ApiSection: expanding', () => {
  it('shows every listener that serves /v1 once expanded, the network one too', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener(), dedicatedListener()], { daemon: { running: true } }))
    await mountExpanded()
    expect(await screen.findByTestId('api-listener-main')).toBeInTheDocument()
    expect(await screen.findByTestId('api-listener-v1')).toBeInTheDocument()
    expect(screen.getAllByTestId('api-base-url').map(e => e.textContent)).toEqual(['http://127.0.0.1:9322/v1', 'https://localhost:9443/v1'])
    expect(screen.getAllByTestId('api-exposure').map(e => e.textContent)).toEqual(['Bound to loopback', 'Network'])
    // The models are those of the listener the header describes: the first that answers /v1, the main one.
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledWith('loopback', 'any', 'chat-only', 'chat-only'))
  })

  it('reads the models for the listener that answers when the first one listed does not', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener(down), dedicatedListener()]))
    App.APIModels.mockResolvedValue(modelsDoc({ confinement: 'chat-only', forListener: 'network' }))
    await mountExpanded()
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(1))
    expect(App.APIModels).toHaveBeenCalledWith('network', 'chat-only', 'chat-only', 'chat-only')
  })

  it('reads the keys and the models on the first expand, for the listener the header describes', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
    App.APIModels.mockResolvedValue(modelsDoc({ confinement: 'chat-only', forListener: 'network' }))
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
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

  it('expanded while the status is still loading, waits for it before reading the models for its policy', async () => {
    let resolve
    App.APIStatus.mockReturnValue(new Promise(r => { resolve = r }))
    await mount()
    fireEvent.click(toggle())
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    await settle()
    expect(App.APIModels).not.toHaveBeenCalled() // there is no policy to evaluate yet
    expect(App.APIKeyList).not.toHaveBeenCalled()
    await act(async () => { resolve(statusOf([mainListener()])) })
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(1))
    expect(App.APIModels).toHaveBeenCalledWith('loopback', 'any', 'chat-only', 'chat-only')
    expect(App.APIStatus.mock.invocationCallOrder[0]).toBeLessThan(App.APIModels.mock.invocationCallOrder[0])
    expect(await screen.findByText('notes bot')).toBeInTheDocument()
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('2 keys') // from the list now
  })

  it('reads the models with the CLI\'s own defaults when no listener serves /v1, and when the status failed', async () => {
    App.APIStatus.mockResolvedValue(statusOf([]))
    await mountExpanded()
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledWith('', '', '', ''))
    expect(await screen.findByText(/No listener serves \/v1 right now/)).toBeInTheDocument()
    cleanup(); vi.clearAllMocks()
    App.APIStatus.mockRejectedValue(new Error('boom'))
    App.APIKeyList.mockResolvedValue([])
    App.APIModels.mockResolvedValue(modelsDoc())
    await mountExpanded()
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledWith('', '', '', ''))
    expect(await screen.findByText(/Couldn't read the API status: boom/)).toBeInTheDocument()
    // The keys and the models are still shown: one failure does not blank the rest.
    expect(await screen.findByText(/No API keys yet/)).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    // ... but the models do not claim that no listener serves /v1: they do not know.
    expect(screen.getByText(/Couldn't read which listeners serve \/v1/)).toBeInTheDocument()
    expect(screen.queryByText(/No listener serves \/v1 right now/)).not.toBeInTheDocument()
  })

  it('shows what each part could not read, with its own retry', async () => {
    App.APIKeyList.mockRejectedValueOnce(new Error('invalid_input: no such table: api_keys')) // the class is not shown
    App.APIModels.mockRejectedValueOnce(new Error('invalid_input: monomind not found'))
    await mountExpanded()
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
    await mountExpanded()
    await screen.findByText('notes bot')
    await screen.findByRole('table', { name: 'Models' })
    App.APIStatus.mockResolvedValue(statusOf([mainListener(down)]))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled())
    expect(screen.getByTestId('api-state')).toHaveTextContent('Not running')
  })

  it('words a failure in the language chosen after the section was shown', async () => {
    await mountExpanded()
    await screen.findByText('notes bot')
    await act(() => i18n.changeLanguage('es'))
    App.APIStatus.mockRejectedValueOnce(undefined) // nothing to say: the page words it
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.refresh }))
    expect(await screen.findByText(`${es.settings.api.loadError.replace('{{error}}', es.settings.api.errors.unknown)}`)).toBeInTheDocument()
  })

  it('retries the status from its error line', async () => {
    App.APIStatus.mockRejectedValueOnce(new Error('not_found: boom')) // the class is not shown
    await mountExpanded()
    expect(await screen.findByText("Couldn't read the API status: boom")).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('api-base-url')).toBeInTheDocument()
    expect(App.APIStatus).toHaveBeenCalledTimes(2)
  })
})

// Settings stays mounted while another page is shown, so what the section read on mount is as old as the visit.
describe('ApiSection: coming back to the page', () => {
  it('reads the status again when the page becomes active: not on mount, not on leaving, not while it stays active', async () => {
    const { rerender } = await mount({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    await settle()
    expect(App.APIStatus).toHaveBeenCalledTimes(1)
    rerender({ isActive: false })
    await settle()
    expect(App.APIStatus).toHaveBeenCalledTimes(1)

    App.APIStatus.mockResolvedValue(statusOf([mainListener(down)])) // the server stopped meanwhile
    rerender({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running'))
    expect(App.APIStatus).toHaveBeenCalledTimes(2)
    rerender({ isActive: true })
    await settle()
    expect(App.APIStatus).toHaveBeenCalledTimes(2)
  })

  it('is always active when it is not told otherwise', async () => {
    const { rerender } = await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    rerender({}); rerender({})
    await settle()
    expect(App.APIStatus).toHaveBeenCalledTimes(1)
  })

  it('reads the status again every time it comes back', async () => {
    const { rerender } = await mount({ isActive: true })
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(1))
    for (const n of [2, 3]) {
      rerender({ isActive: false })
      rerender({ isActive: true })
      await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(n))
    }
  })

  it('reads the keys again too, once they were read, and the models only when the policy they were read for changed', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByText('notes bot')
    await screen.findByRole('table', { name: 'Models' })
    expect(App.APIKeyList).toHaveBeenCalledTimes(1)
    expect(App.APIModels).toHaveBeenCalledTimes(1)

    App.APIKeyList.mockResolvedValue(keyList().slice(0, 1)) // a key was revoked from the terminal meanwhile
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(screen.queryByText('notes bot')).not.toBeInTheDocument())
    expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('1 key')
    expect(App.APIKeyList).toHaveBeenCalledTimes(2)
    await settle()
    expect(App.APIModels).toHaveBeenCalledTimes(1) // the same listener, the same policy: a scan of the runtimes is not repeated
  })

  it('reads the models again when the listener they describe changed, and does not show the table of the old one', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByRole('table', { name: 'Models' })
    expect(screen.getByText(/Policy of the listener at 127\.0\.0\.1:9322/)).toBeInTheDocument()

    // The daemon was restarted with a dedicated network listener only.
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
    let release
    App.APIModels.mockReturnValueOnce(new Promise(r => { release = r }))
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(2))
    expect(App.APIModels).toHaveBeenLastCalledWith('network', 'chat-only', 'chat-only', 'chat-only')
    expect(screen.getByText('Loading models…')).toBeInTheDocument() // not the old table under the new listener's caption
    expect(screen.queryByRole('table', { name: 'Models' })).not.toBeInTheDocument()
    await act(async () => { release(modelsDoc({ confinement: 'chat-only', forListener: 'network' })) })
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(screen.getByText(/Policy of the listener at 0\.0\.0\.0:9443/)).toBeInTheDocument()
  })

  it('reads the models again when the last read of them failed, even for the same policy', async () => {
    App.APIModels.mockRejectedValueOnce(new Error('monomind not found'))
    const { rerender } = await mountExpanded({ isActive: true })
    expect(await screen.findByText(/Couldn't list the models: monomind not found/)).toBeInTheDocument()
    rerender({ isActive: false })
    rerender({ isActive: true })
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(App.APIModels).toHaveBeenCalledTimes(2)
  })

  it('reads only the status of a section nobody unfolded', async () => {
    const { rerender } = await mount({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await settle()
    expect(App.APIKeyList).not.toHaveBeenCalled()
    expect(App.APIModels).not.toHaveBeenCalled()
  })

  it('shows an error in the header when the status cannot be read on coming back, and the next time it recovers', async () => {
    const { rerender } = await mount({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    App.APIStatus.mockRejectedValueOnce(new Error('boom'))
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Error reading status'))
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
  })

  it('drops a slow answer from before: the newest status wins, and what followed the old one does not run', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByRole('table', { name: 'Models' })
    let slow
    App.APIStatus.mockReturnValueOnce(new Promise(r => { slow = r })) // the re-read on coming back: slow
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    App.APIStatus.mockResolvedValueOnce(statusOf([mainListener(down)])) // Refresh, started later and answered first
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running'))
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2)) // the first read and the refresh's

    await act(async () => { slow(statusOf([mainListener()])) }) // the old answer arrives: Running
    await settle()
    expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running')
    expect(screen.getByTestId('api-state')).toHaveTextContent('Not running')
    expect(App.APIKeyList).toHaveBeenCalledTimes(2) // the re-read it belonged to went no further
  })

  it('does not let a refresh that a newer read overtook go on with its older status', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByRole('table', { name: 'Models' })
    let slow
    App.APIStatus.mockReturnValueOnce(new Promise(r => { slow = r })) // Refresh: slow
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    App.APIStatus.mockResolvedValueOnce(statusOf([mainListener(down)])) // coming back to the page meanwhile: fast
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running'))
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2)) // the first read, and the re-read's
    await act(async () => { slow(statusOf([mainListener()])) })
    await settle()
    expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Not running')
    expect(App.APIKeyList).toHaveBeenCalledTimes(2) // the refresh read nothing more, and says it is done
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  })

  it('drops a slow answer about the models in favour of a newer one', async () => {
    let slow
    App.APIModels.mockReturnValueOnce(new Promise(r => { slow = r })) // the first read of the models: slow
    await mountExpanded()
    await screen.findByText('notes bot')
    App.APIModels.mockResolvedValueOnce(modelsDoc({ confinement: 'chat-only' })) // Refresh: answered first
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    const codex = () => within(screen.getByRole('row', { name: /codex\/default/ })).getAllByRole('cell')[3]
    await waitFor(() => expect(codex()).toHaveTextContent('no (policy)'))
    await act(async () => { slow(modelsDoc({ confinement: 'any' })) }) // the old one: codex is served
    await settle()
    expect(codex()).toHaveTextContent('no (policy)')
  })

  describe('a slow failure from before does not overwrite a newer answer', () => {
    const refreshed = async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
      await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled())
      await screen.findByText('notes bot')
      await screen.findByRole('table', { name: 'Models' })
    }

    it('the status', async () => {
      const { rerender } = await mountExpanded({ isActive: true })
      await screen.findByRole('table', { name: 'Models' })
      let fail
      App.APIStatus.mockReturnValueOnce(new Promise((_, r) => { fail = r })) // the re-read on coming back: slow
      rerender({ isActive: false })
      rerender({ isActive: true })
      await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
      await refreshed()
      await act(async () => { fail(new Error('boom: status')) })
      await settle()
      expect(screen.queryByText(/boom/)).not.toBeInTheDocument()
      expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running')
    })

    it('the keys', async () => {
      const { rerender } = await mountExpanded({ isActive: true })
      await screen.findByText('notes bot')
      let fail
      App.APIKeyList.mockReturnValueOnce(new Promise((_, r) => { fail = r })) // the re-read on coming back: slow
      rerender({ isActive: false })
      rerender({ isActive: true })
      await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
      await refreshed()
      await act(async () => { fail(new Error('boom: keys')) })
      await settle()
      expect(screen.queryByText(/boom/)).not.toBeInTheDocument()
      expect(screen.getByText('notes bot')).toBeInTheDocument()
    })

    it('the models', async () => {
      let fail
      App.APIModels.mockReturnValueOnce(new Promise((_, r) => { fail = r })) // the first read of them: slow
      await mountExpanded({ isActive: true })
      await screen.findByText('notes bot')
      await refreshed()
      await act(async () => { fail(new Error('boom: models')) })
      await settle()
      expect(screen.queryByText(/boom/)).not.toBeInTheDocument()
      expect(screen.getByRole('table', { name: 'Models' })).toBeInTheDocument()
    })
  })

  it('reads the models again on coming back after a failed refresh of them, though the policy did not change', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByRole('table', { name: 'Models' })
    App.APIModels.mockRejectedValueOnce(new Error('monomind not found'))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText(/Couldn't list the models: monomind not found/)).toBeInTheDocument()
    expect(App.APIModels).toHaveBeenCalledTimes(2)
    rerender({ isActive: false })
    rerender({ isActive: true })
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(App.APIModels).toHaveBeenCalledTimes(3)
  })

  it('drops a slow keys list in favour of a newer one', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await screen.findByText('notes bot')
    let slow
    App.APIKeyList.mockReturnValueOnce(new Promise(r => { slow = r }))
    rerender({ isActive: false })
    rerender({ isActive: true })
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
    App.APIKeyList.mockResolvedValueOnce(keyList().slice(0, 1)) // a retry or a refresh answered first
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.queryByText('notes bot')).not.toBeInTheDocument())
    await act(async () => { slow(keyList()) })
    await settle()
    expect(screen.queryByText('notes bot')).not.toBeInTheDocument()
  })
})

describe('ApiSection: keys', () => {
  it('reloads the keys after a create, while the new key is still shown only in its dialog', async () => {
    await mountExpanded()
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
    await mountExpanded()
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
    await mountExpanded()
    expect(await screen.findByText(/Context adds excerpts/)).toHaveTextContent('up to sandboxed')
  })

  it('names the context cap when every listener that serves /v1 has the same, and none when they differ', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ context_confinement: 'sandboxed' }), dedicatedListener({ context_confinement: 'sandboxed' })]))
    await mountExpanded()
    expect(await screen.findByText(/Context adds excerpts/)).toHaveTextContent('up to sandboxed')
    cleanup()
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ context_confinement: 'sandboxed' }), dedicatedListener({ context_confinement: 'chat-only' })]))
    await mountExpanded()
    expect(await screen.findByText(/Context adds excerpts/)).toHaveTextContent('up to the class each listener allows')
    cleanup()
    // A listener that does not serve /v1 has no say.
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ v1: false, context_confinement: 'chat-only' }), dedicatedListener({ context_confinement: 'sandboxed' })]))
    await mountExpanded()
    expect(await screen.findByText(/Context adds excerpts/)).toHaveTextContent('up to sandboxed')
  })

  it('keeps the keys busy while the list is read again after a context change, and then shows the new value', async () => {
    mockConfirm.mockResolvedValue(true)
    await mountExpanded()
    await screen.findByText('notes bot')
    let release
    App.APIKeySetContext.mockResolvedValue({})
    App.APIKeyList.mockReturnValueOnce(new Promise(r => { release = r }))
    fireEvent.click(within(screen.getByRole('row', { name: /my-app/ })).getByRole('switch'))
    await waitFor(() => expect(App.APIKeyList).toHaveBeenCalledTimes(2))
    // The list is being read: the old rows are still there, and cannot be acted on.
    expect(within(screen.getByRole('row', { name: /notes bot/ })).getByRole('switch')).toBeDisabled()
    await act(async () => { release(keyList().map(k => (k.id === 'key_abcdefghijkl' ? { ...k, context: true } : k))) })
    await waitFor(() => expect(within(screen.getByRole('row', { name: /my-app/ })).getByRole('switch')).toHaveAttribute('aria-checked', 'true'))
    expect(within(screen.getByRole('row', { name: /my-app/ })).getByRole('switch')).toBeEnabled()
  })
})

describe('ApiSection: auto and the Jev settings', () => {
  it('jumps to the Jev settings from an auto that is off for want of Jev', async () => {
    App.APIModels.mockResolvedValue({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } })
    const { onNavigate } = await mountExpanded()
    fireEvent.click(await screen.findByRole('button', { name: 'Open the Jev settings' }))
    expect(onNavigate).toHaveBeenCalledTimes(1)
    expect(onNavigate).toHaveBeenCalledWith('settings', { section: 'jev' })
  })

  it('has no jump when it is not given a way to navigate', async () => {
    App.APIModels.mockResolvedValue({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } })
    await mountExpanded({ onNavigate: undefined })
    await screen.findByText(/Off\. It needs/)
    expect(screen.queryByRole('button', { name: 'Open the Jev settings' })).not.toBeInTheDocument()
  })
})

describe('ApiSection: the rest', () => {
  it('is one button, named by what it shows, that unfolds the section', async () => {
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    expect(toggle().tagName).toBe('BUTTON')
    expect(toggle()).toHaveAccessibleName(/OpenAI-compatible API\s+Running\s+2 keys/)
    fireEvent.click(toggle())
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    expect(document.getElementById(toggle().getAttribute('aria-controls'))).toBeInTheDocument()
  })

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mountExpanded()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent(es.settings.api.status.stateServing))
    await waitFor(() => expect(screen.getByTestId('api-fold-keys')).toHaveTextContent('2 claves'))
    expect(screen.getByRole('button', { name: es.settings.api.keys.create })).toBeInTheDocument()
    expect(screen.getAllByText(es.settings.api.sectionTitle).length).toBeGreaterThan(0)
  })
})
