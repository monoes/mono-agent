// @vitest-environment jsdom
// The API section and its "Server settings" block: when the settings are read, what a read that fails leaves on screen,
// and the guard that lets a newer answer win over an older one (a change that was made counts as an answer).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../i18n.js'
import en from '../../locales/en.json'
import es from '../../locales/es.json'
import { mainListener, dedicatedListener, statusOf, modelsDoc, keyList } from './api/__fixtures__/apiFixtures.js'
import { DAMAGED, NEWER, WIDENING, changeDoc, configDoc } from './api/__fixtures__/configFixtures.js'

const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIStatus', 'APIKeyList', 'APIModels', 'APIKeyCreate', 'APIKeySetContext', 'APIKeyRename', 'APIKeyRevoke', 'APIConfigShow', 'APIConfigSet', 'APIConfigUnset', 'APIConfigReset', 'DaemonRestart']) App[k] = vi.fn()
  App.APIStatus.mockResolvedValue(statusOf([mainListener()]))
  App.APIKeyList.mockResolvedValue(keyList())
  App.APIModels.mockResolvedValue(modelsDoc())
  App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '6' }, running: {}, autostart: true }))
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

const c = en.settings.api.config
async function mount(props = {}) {
  const { default: ApiSection } = await import('./ApiSection.jsx')
  const ui = (p = {}) => <ApiSection onNavigate={vi.fn()} {...props} {...p} />
  const view = render(ui())
  return { rerender: (p) => view.rerender(ui(p)) }
}
const toggle = () => screen.getByTestId('api-fold-toggle')
async function mountExpanded(props = {}) {
  const m = await mount(props)
  await waitFor(() => expect(screen.getByTestId('api-fold-state')).not.toHaveTextContent('Loading…'))
  fireEvent.click(toggle())
  return m
}
const openBlock = () => fireEvent.click(screen.getByTestId('api-config-toggle'))
const maxInput = () => screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })

describe('ApiSection: reading the server settings', () => {
  it('reads them when the section is first opened, with the keys and the models, and not before', async () => {
    await mount()
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    await settle()
    expect(App.APIConfigShow).not.toHaveBeenCalled() // folded: only the status, which is cheap
    fireEvent.click(toggle())
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(1))
    openBlock()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(6)
    fireEvent.click(toggle()); fireEvent.click(toggle()) // folding and unfolding reads nothing more
    expect(App.APIConfigShow).toHaveBeenCalledTimes(1)
  })

  it('puts the block below the status and above the keys and the models', async () => {
    await mountExpanded()
    const block = await screen.findByTestId('api-config-block')
    const status = await screen.findByTestId('api-base-url')
    const keys = await screen.findByRole('table', { name: 'API keys' })
    const models = await screen.findByRole('table', { name: 'Models' })
    const before = (a, b) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    before(status, block); before(block, keys); before(keys, models)
  })

  it('reads them with the rest when Refresh is pressed', async () => {
    await mountExpanded()
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(1))
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '9' }, running: {}, autostart: true }))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(2))
    openBlock()
    await waitFor(() => expect(maxInput()).toHaveValue(9))
  })

  it('reads them again when Settings is shown again, once the section had been opened, and not for a section nobody opened', async () => {
    const { rerender } = await mountExpanded({ isActive: true })
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(1))
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '12' }, running: {}, autostart: true })) // changed from the terminal meanwhile
    rerender({ isActive: false })
    await settle()
    expect(App.APIConfigShow).toHaveBeenCalledTimes(1)
    rerender({ isActive: true })
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(2))
    openBlock()
    await waitFor(() => expect(maxInput()).toHaveValue(12))
    cleanup()

    vi.clearAllMocks()
    App.APIStatus.mockResolvedValue(statusOf([mainListener()]))
    const folded = await mount({ isActive: true })
    await waitFor(() => expect(screen.getByTestId('api-fold-state')).toHaveTextContent('Running'))
    folded.rerender({ isActive: false }); folded.rerender({ isActive: true })
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await settle()
    expect(App.APIConfigShow).not.toHaveBeenCalled()
  })

  it('reads them although the status could not be read: each part is read on its own', async () => {
    App.APIStatus.mockRejectedValue(new Error('boom'))
    await mountExpanded()
    openBlock()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(6)
  })
})

describe('ApiSection: when reading the server settings fails', () => {
  it('says it with a retry when there is nothing to show, and the retry reads again', async () => {
    App.APIConfigShow.mockRejectedValueOnce(new Error('invalid_input: unknown command "config" for "monoagentcli api"'))
    await mountExpanded()
    openBlock()
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent('Couldn\'t read the server settings: unknown command "config" for "monoagentcli api"')
    expect(screen.getByTestId('api-config-chip-error')).toBeInTheDocument()
    fireEvent.click(within(screen.getByTestId('api-config-block')).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toBeInTheDocument()
    expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument()
    expect(App.APIConfigShow).toHaveBeenCalledTimes(2)
  })

  it('keeps what was on screen when a later read fails, and says so', async () => {
    await mountExpanded()
    openBlock()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(6)
    App.APIConfigShow.mockRejectedValueOnce(new Error('database is locked'))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent('database is locked')
    expect(maxInput()).toHaveValue(6) // what was read before is still there, and can be used
    expect(screen.getByTestId('api-config-chip-saved')).toBeInTheDocument()
    // and a read that works takes the failure away
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument())
  })

  it('takes the failure away when a change is made: what that gave is newer than the read that failed', async () => {
    await mountExpanded()
    openBlock()
    await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })
    App.APIConfigShow.mockRejectedValueOnce(new Error('database is locked'))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByTestId('api-config-load-error')).toBeInTheDocument()
    const saved = changeDoc(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? { ...saved, applied: false } : saved))
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    await waitFor(() => expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument())
    expect(maxInput()).toHaveValue(8)
  })

  it('words a failure in the language chosen after the section was shown', async () => {
    await mountExpanded()
    openBlock()
    await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })
    await act(() => i18n.changeLanguage('es'))
    App.APIConfigShow.mockRejectedValueOnce(undefined) // nothing to say: the page words it
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.refresh }))
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent(`${es.settings.api.config.loadError} ${es.settings.api.errors.unknown}`)
  })
})

describe('ApiSection: an older answer never overwrites a newer one', () => {
  const doc = (max) => configDoc({ saved: { max_concurrent: max }, running: {}, autostart: true })

  // The status is read first by each of these (as the section does), and then the settings: two reads of them are out
  // when the section was shown again and Refresh was pressed after it, the older one slow.
  async function twoReads({ slow }) {
    const m = await mountExpanded({ isActive: true })
    openBlock()
    await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })
    App.APIConfigShow.mockReturnValueOnce(slow) // the older one: coming back to the page
    App.APIConfigShow.mockResolvedValueOnce(doc('9')) // the newer one: Refresh, answered at once
    m.rerender({ isActive: false }); m.rerender({ isActive: true })
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(maxInput()).toHaveValue(9))
  }

  it('drops a slow read that a newer one overtook', async () => {
    let release
    await twoReads({ slow: new Promise(r => { release = r }) })
    await act(async () => { release(doc('1')) }) // the old answer arrives
    await settle()
    expect(maxInput()).toHaveValue(9)
  })

  it('drops the failure of a slow read that a newer one overtook', async () => {
    let fail
    await twoReads({ slow: new Promise((_, r) => { fail = r }) })
    await act(async () => { fail(new Error('boom: settings')) })
    await settle()
    expect(screen.queryByText(/boom/)).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument()
    expect(maxInput()).toHaveValue(9)
  })

  it('counts a change that was made as an answer: a read that started before it does not overwrite it', async () => {
    let release
    await mountExpanded()
    openBlock()
    await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })
    App.APIConfigShow.mockReturnValueOnce(new Promise(r => { release = r })) // a read that is out, and slow
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(2))

    const saved = changeDoc(doc('8'), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? { ...saved, applied: false } : saved))
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    await waitFor(() => expect(maxInput()).toHaveValue(8))
    expect(await screen.findByText(c.note.saved)).toBeInTheDocument()

    await act(async () => { release(doc('6')) }) // the read that began before the change comes back with what was there before it
    await settle()
    expect(maxInput()).toHaveValue(8)
  })

  it('never shows a dry run as the state of the settings', async () => {
    await mountExpanded()
    openBlock()
    await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })
    const would = changeDoc(doc('8'), { applied: false, changed: ['max_concurrent'], widening: [{ key: 'v1_addr', reason: 'It would listen beyond this machine.' }] })
    App.APIConfigSet.mockResolvedValue(would) // the dry run asks for a confirmation; nobody gives it
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    await screen.findByRole('alertdialog')
    fireEvent.click(screen.getByRole('button', { name: c.widening.cancel }))
    await settle()
    expect(screen.getByTestId('api-config-chip-saved')).toHaveTextContent('1 saved')
    expect(maxInput()).toHaveValue(8) // what was typed; nothing was saved, and what is saved is still 6
    fireEvent.change(maxInput(), { target: { value: '6' } })
    expect(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) })).toBeDisabled() // 6 is what is saved
  })
})

describe('ApiSection: a saved row that cannot be read', () => {
  // What the binding rejects with (app_api.go): exit 3 reaches the page as "invalid_input: " and the CLI's last line; exit 1 as that line.
  const damagedErr = () => new Error(`invalid_input: ${DAMAGED}`)
  const dry = () => ({ ...changeDoc(configDoc(), { applied: false, widening: [WIDENING.savedSettings] }), removed_unreadable_row: true })
  const done = () => ({ ...changeDoc(configDoc({ running: {}, autostart: true }), { widening: [WIDENING.savedSettings] }), removed_unreadable_row: true })
  const resetCall = () => App.APIConfigReset.mockImplementation(async (confirm, dryRun) => {
    if (dryRun) return dry()
    if (!confirm) throw new Error(`invalid_input: this change makes the server reach further: ${WIDENING.savedSettings.reason} Pass --yes to make the change anyway.`)
    return done()
  })
  const block = () => within(screen.getByTestId('api-config-block'))
  async function reset(label = c.reset.button, confirm = c.widening.confirmReset) {
    fireEvent.click(await block().findByRole('button', { name: label }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: confirm }))
  }

  it('says it in the CLI\'s words, with the way out, when the settings cannot be read because of it, and only then', async () => {
    App.APIConfigShow.mockRejectedValue(`invalid_input: ${DAMAGED}`) // a binding's rejection may be the bare text
    await mountExpanded()
    openBlock()
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent(`${c.loadError} ${DAMAGED}`)
    expect(screen.getByTestId('api-config-chip-damaged')).toHaveTextContent(c.chipDamaged)
    expect(block().getByRole('button', { name: c.reset.button })).toBeInTheDocument()
    cleanup()

    // a row in a newer format is exit 1: its message and nothing else, never a reset
    App.APIConfigShow.mockRejectedValue(new Error(NEWER))
    await mountExpanded()
    openBlock()
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent(`${c.loadError} ${NEWER}`)
    expect(screen.getByTestId('api-config-chip-error')).toBeInTheDocument()
    expect(screen.queryByTestId('api-config-chip-damaged')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: c.reset.button })).not.toBeInTheDocument()
  })

  it('removes the saved settings when asked, shows what is saved now, and reads again what failed for the same row', async () => {
    App.APIStatus.mockRejectedValueOnce(damagedErr()) // the status and the models read the same row
    App.APIConfigShow.mockRejectedValue(damagedErr())
    App.APIModels.mockRejectedValue(damagedErr())
    resetCall()
    await mountExpanded()
    openBlock()
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
    expect(screen.queryByRole('table', { name: 'Models' })).not.toBeInTheDocument()
    expect(App.APIStatus).toHaveBeenCalledTimes(1)

    App.APIStatus.mockResolvedValue(statusOf([mainListener()])) // the CLI answers again, once the row is gone
    App.APIModels.mockResolvedValue(modelsDoc())
    await reset()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(null)
    expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-chip-damaged')).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('api-config-reset-done')).toHaveFocus())
    expect(await screen.findByTestId('api-base-url')).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Models' })).toBeInTheDocument()
    expect(App.APIStatus).toHaveBeenCalledTimes(2)
    expect(App.APIConfigShow).toHaveBeenCalledTimes(1) // what the reset gave is the document on show: nothing reads it again
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true], [true, false]])
  })

  it('does not let a read that began before the reset overwrite what it gave', async () => {
    let fail
    App.APIConfigShow.mockRejectedValueOnce(damagedErr())
    resetCall()
    await mountExpanded()
    openBlock()
    await screen.findByTestId('api-config-load-error')
    App.APIConfigShow.mockReturnValueOnce(new Promise((_, reject) => { fail = reject })) // a read that is out, and slow
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(App.APIConfigShow).toHaveBeenCalledTimes(2))

    await reset()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toBeInTheDocument()
    await act(async () => { fail(damagedErr()) }) // the read that began before the reset comes back: it found the row damaged
    await settle()
    expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-chip-damaged')).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })).toBeInTheDocument()
  })

  it('takes the way out away when the row can be read again, without having removed anything', async () => {
    App.APIConfigShow.mockRejectedValueOnce(damagedErr())
    await mountExpanded()
    openBlock()
    await screen.findByTestId('api-config-load-error')
    expect(block().getByRole('button', { name: c.reset.button })).toBeInTheDocument()
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '9' }, running: {}, autostart: true })) // fixed by hand meanwhile
    fireEvent.click(block().getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(9)
    expect(screen.queryByRole('button', { name: c.reset.button })).not.toBeInTheDocument()
    expect(App.APIConfigReset).not.toHaveBeenCalled() // nothing was removed: nobody asked
  })

  it('brings the way out when a change finds the row damaged: the settings are read again, and what was typed stays', async () => {
    await mountExpanded()
    openBlock()
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(6) // read fine
    App.APIConfigSet.mockRejectedValue(damagedErr()) // and damaged since
    App.APIConfigShow.mockRejectedValue(damagedErr())
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    expect(await screen.findByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
    expect(block().getByRole('button', { name: c.reset.button })).toBeInTheDocument()
    expect(screen.getByTestId('api-config-chip-damaged')).toBeInTheDocument()
    expect(maxInput()).toHaveValue(8)
    expect(App.APIConfigShow).toHaveBeenCalledTimes(2)
  })

  it('does not remove what was fixed meanwhile: asked to reset a row that can be read, it reads again and asks nothing', async () => {
    App.APIConfigShow.mockRejectedValueOnce(damagedErr())
    App.APIConfigReset.mockResolvedValue(changeDoc(configDoc({ saved: { max_concurrent: '9' } }), { applied: false })) // a dry run that does not find a row that cannot be read
    await mountExpanded()
    openBlock()
    fireEvent.click(await block().findByRole('button', { name: c.reset.button }))
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '9' }, running: {}, autostart: true }))
    expect(await screen.findByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(9)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true]]) // the dry run, and nothing after it
  })

  it('speaks the chosen language, and the CLI\'s words stay English', async () => {
    await act(() => i18n.changeLanguage('es'))
    App.APIConfigShow.mockRejectedValue(damagedErr())
    resetCall()
    await mountExpanded()
    openBlock()
    const message = await screen.findByTestId('api-config-load-error')
    expect(message).toHaveTextContent(`${es.settings.api.config.loadError} ${DAMAGED}`)
    expect(within(message).getByText(DAMAGED)).toHaveAttribute('lang', 'en')
    await reset(es.settings.api.config.reset.button, es.settings.api.config.widening.confirmReset)
    expect(await screen.findByTestId('api-config-reset-done')).toHaveTextContent(es.settings.api.config.reset.done)
  })
})

describe('ApiSection: after a restart', () => {
  beforeEach(() => { vi.useFakeTimers({ shouldAdvanceTime: true }) })
  afterEach(() => { vi.useRealTimers() })

  const pending = () => configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['4', 'default'] }, autostart: true })
  const tick = (ms) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })
  async function restart() {
    fireEvent.click(await screen.findByRole('button', { name: c.restart.button }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: c.restart.confirm }))
    await tick(0)
  }

  it('reads the settings through the section until the daemon runs what is saved, then reads the status again, and the models when the listener changed', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener()], { daemon: { running: true } }))
    App.APIConfigShow.mockResolvedValueOnce(pending())
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    await mountExpanded()
    openBlock()
    await restart()
    expect(App.DaemonRestart).toHaveBeenCalledTimes(1)
    expect(App.APIConfigShow).toHaveBeenCalledTimes(1)
    // the daemon is back, now with a dedicated listener beyond this machine
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }))
    App.APIStatus.mockResolvedValue(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
    await tick(1100)
    expect(App.APIConfigShow).toHaveBeenCalledTimes(2)
    expect(await screen.findByText(c.restart.back)).toBeInTheDocument()
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(App.APIModels).toHaveBeenCalledTimes(2))
    expect(App.APIModels).toHaveBeenLastCalledWith('network', 'chat-only', 'chat-only', 'chat-only')
    await tick(30000)
    expect(App.APIConfigShow).toHaveBeenCalledTimes(2) // and it stopped
  })

  it('says the dedicated listener is not up when the daemon is back without it, not that it runs the saved settings, and reads the status again all the same', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener()], { daemon: { running: true } }))
    App.APIConfigShow.mockResolvedValueOnce(pending())
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    await mountExpanded()
    openBlock()
    await restart()
    // the daemon is back with what is saved, and the dedicated listener it was given is not up
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { v1_addr: '0.0.0.0:9443' }, running: {}, autostart: true, notServing: ['v1_addr'] }))
    await tick(1100)
    expect(await screen.findByText(c.restart.backListenerDown)).toBeInTheDocument()
    expect(screen.queryByText(c.restart.back)).not.toBeInTheDocument()
    expect(screen.getByTestId('api-config-state-v1_addr')).toHaveTextContent(c.state.notServing)
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2)) // the daemon came back: what the status says may have changed
  })

  it('brings the way out when the restart is refused because the saved row is damaged: the CLI\'s words, no commands for a daemon that is not registered, and the reset', async () => {
    App.APIConfigShow.mockResolvedValueOnce(pending()).mockRejectedValueOnce(new Error(`invalid_input: ${DAMAGED}`)) // fine, and damaged since
    App.DaemonRestart.mockRejectedValue(new Error(`invalid_input: ${DAMAGED}`))
    await mountExpanded()
    openBlock()
    await restart()
    const block = within(screen.getByTestId('api-config-block'))
    expect(await block.findByRole('button', { name: c.reset.button })).toBeInTheDocument() // the read after the refusal found the row damaged
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
    const banner = within(screen.getByTestId('api-config-banner'))
    expect(banner.getByRole('alert')).toHaveTextContent(DAMAGED)
    expect(banner.queryByText('monoagentcli daemon install')).not.toBeInTheDocument()
    expect(App.APIConfigShow).toHaveBeenCalledTimes(2)
  })

  it('does not scan the runtimes again when the daemon came back with the same listener', async () => {
    App.APIStatus.mockResolvedValue(statusOf([mainListener()], { daemon: { running: true } }))
    App.APIConfigShow.mockResolvedValueOnce(pending())
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    await mountExpanded()
    openBlock()
    await screen.findByRole('table', { name: 'Models' })
    await restart()
    App.APIConfigShow.mockResolvedValue(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }))
    await tick(1100)
    await waitFor(() => expect(App.APIStatus).toHaveBeenCalledTimes(2))
    await tick(100)
    expect(App.APIModels).toHaveBeenCalledTimes(1)
  })
})

describe('ApiSection: the block in Spanish', () => {
  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mountExpanded()
    await waitFor(() => expect(screen.getByTestId('api-config-toggle')).toHaveAccessibleName(new RegExp(es.settings.api.config.title)))
    openBlock()
    expect(await screen.findByRole('spinbutton', { name: es.settings.api.config.rows.max_concurrent.label })).toHaveValue(6)
  })
})
