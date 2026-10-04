// @vitest-environment jsdom
// The API section when the page it is on is shown again: Settings stays mounted while another page is
// shown, so what the section read is as old as the visit (ApiSection.render.test.jsx has the rest).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../i18n.js'
import { mainListener, dedicatedListener, statusOf, modelsDoc, keyList } from './api/__fixtures__/apiFixtures.js'

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
