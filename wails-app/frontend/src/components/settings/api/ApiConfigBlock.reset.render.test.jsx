// @vitest-environment jsdom
// A saved row the CLI cannot read, in the "Server settings" block: the CLI's own words, and the one way out it names,
// offered as a button that asks the CLI what resetting would do, lists that in the dialog, and only on a yes removes the
// saved settings. What is not damage (a row in a newer format, a failing database) gets the message and nothing else.
import React, { useState } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { describeConfigError } from './configError.js'
import { DAMAGED, NEWER, WIDENING, changeDoc, configDoc } from './__fixtures__/configFixtures.js'

const App = {}
let offsetParent
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIConfigSet', 'APIConfigUnset', 'APIConfigReset', 'DaemonRestart']) App[k] = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
  // jsdom has no layout: every element has a null offsetParent, which the Tab trap reads as "not visible".
  offsetParent = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetParent')
  Object.defineProperty(HTMLElement.prototype, 'offsetParent', { configurable: true, get() { return this.parentNode } })
})
afterEach(() => {
  cleanup(); delete window.go; delete window.runtime
  if (offsetParent) Object.defineProperty(HTMLElement.prototype, 'offsetParent', offsetParent)
})

const c = en.settings.api.config
const w = c.widening
const sc = es.settings.api.config

// What the section hands the block when the settings could not be read: the page's wording of what the binding rejected with.
const said = (e) => describeConfigError(e, (k, o) => i18n.t(k, o))
const damaged = () => said(new Error(`invalid_input: ${DAMAGED}`))

// The section's part in it: the document of a change that was made becomes the document on show, and the failure is gone;
// `read` is a later read that gave this failure (or none).
async function mountOpen({ config = null, err = damaged() } = {}) {
  const { default: ApiConfigBlock } = await import('./ApiConfigBlock.jsx')
  const spies = { onRetry: vi.fn(), onAdopt: vi.fn(), onReload: vi.fn(), onApplied: vi.fn(), read: null }
  function Section() {
    const [doc, setDoc] = useState(config)
    const [failure, setFailure] = useState(err)
    spies.read = (e) => act(() => setFailure(e))
    return (
      <ApiConfigBlock
        config={doc} err={failure} onRetry={spies.onRetry} onReload={spies.onReload} onApplied={spies.onApplied}
        onAdopt={(d) => { spies.onAdopt(d); setDoc(d); setFailure(null) }}
      />
    )
  }
  render(<Section />)
  fireEvent.click(screen.getByTestId('api-config-toggle'))
  return spies
}

// What `unset --all` says of a row that cannot be read: a dry run says what it would do and why it needs a yes, a call with
// --yes removes it, and one without is refused. The document is the state after: every setting at its default.
const dryDoc = () => ({ ...changeDoc(configDoc(), { applied: false, widening: [WIDENING.savedSettings] }), removed_unreadable_row: true })
const doneDoc = () => ({ ...changeDoc(configDoc({ running: {}, autostart: true }), { applied: true, widening: [WIDENING.savedSettings] }), removed_unreadable_row: true })
function resetCall({ dry = dryDoc(), done = doneDoc() } = {}) {
  App.APIConfigReset.mockImplementation(async (confirm, dryRun) => {
    if (dryRun) return dry
    if (!confirm) throw new Error(`invalid_input: this change makes the server reach further: ${WIDENING.savedSettings.reason} Pass --yes to make the change anyway.`)
    return done
  })
  return { dry, done }
}

const resetButton = () => screen.getByRole('button', { name: c.reset.button })
const retryButton = () => screen.getByRole('button', { name: 'Retry' })
const dialog = () => screen.getByRole('alertdialog', { name: w.title })
const confirmButton = () => within(dialog()).getByRole('button', { name: w.confirmReset })
const cancelButton = () => within(dialog()).getByRole('button', { name: w.cancel })
const block = () => screen.getByTestId('api-config-block')
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })
// The keyboard is moved by effects, which run a moment after what they follow is on screen (a little later when the machine is busy).
const focused = (element) => waitFor(() => expect(element()).toHaveFocus())
const maxInput = () => screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })

describe('a saved row that cannot be read: what the block says', () => {
  it('says it in the CLI\'s own words, with the way out next to the retry, and in the folded header too', async () => {
    await mountOpen()
    expect(screen.getByTestId('api-config-chip-damaged')).toHaveTextContent(c.chipDamaged)
    expect(screen.queryByTestId('api-config-chip-error')).not.toBeInTheDocument() // it is more than an error of reading
    expect(screen.getByTestId('api-config-toggle')).toHaveAccessibleName(new RegExp(c.chipDamaged))
    const message = screen.getByTestId('api-config-load-error')
    expect(message).toHaveTextContent(`${c.loadError} ${DAMAGED}`)
    expect(screen.queryByTestId('api-config-row-max_concurrent')).not.toBeInTheDocument() // nothing to show or change
    expect(retryButton().compareDocumentPosition(resetButton()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(resetButton().tagName).toBe('BUTTON')
    expect(resetButton()).toBeEnabled()
    // what the button is for is the message
    expect(document.getElementById(resetButton().getAttribute('aria-describedby'))).toBe(message)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument() // nothing is asked until it is pressed
    expect(App.APIConfigReset).not.toHaveBeenCalled()
  })

  it('offers nothing for what is not damage: a row in a newer format, a failing database, any other refusal', async () => {
    for (const e of [new Error(NEWER), new Error('database is locked'), new Error('invalid_input: unknown command "config" for "monoagentcli api"')]) {
      await mountOpen({ err: said(e) })
      expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(`${c.loadError} ${said(e).text}`)
      expect(screen.queryByRole('button', { name: c.reset.button })).not.toBeInTheDocument()
      expect(retryButton()).toBeInTheDocument()
      expect(screen.getByTestId('api-config-chip-error')).toBeInTheDocument()
      expect(screen.queryByTestId('api-config-chip-damaged')).not.toBeInTheDocument()
      cleanup()
    }
  })

  it('keeps what was on screen when a later read found the row damaged, and offers the way out all the same', async () => {
    await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }) })
    expect(maxInput()).toHaveValue(8)
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
    expect(resetButton()).toBeEnabled()
  })

  it('says the CLI\'s words are English on a page that is not, and words the rest in the language of the page', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mountOpen()
    expect(screen.getByTestId('api-config-chip-damaged')).toHaveTextContent(sc.chipDamaged)
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(`${sc.loadError} ${DAMAGED}`)
    expect(within(screen.getByTestId('api-config-load-error')).getByText(DAMAGED)).toHaveAttribute('lang', 'en')
    expect(screen.getByRole('button', { name: sc.reset.button })).toBeInTheDocument()
    expect(sc.reset.button).not.toBe(c.reset.button)
    expect(sc.chipDamaged).not.toBe(c.chipDamaged)
  })
})

describe('a saved row that cannot be read: resetting the saved settings', () => {
  it('asks the CLI what it would do before anything is removed, and lists its reason, with Cancel where the focus starts', async () => {
    resetCall()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog', { name: w.title })

    expect(App.APIConfigReset.mock.calls).toEqual([[false, true]]) // a dry run, alone: nothing confirmed, nothing removed
    expect(within(dialog()).getAllByRole('listitem').map(li => li.textContent)).toEqual([w.kind.saved_settings + WIDENING.savedSettings.reason])
    expect(within(dialog()).getAllByRole('button').map(b => b.textContent)).toEqual([w.cancel, w.confirmReset])
    await focused(cancelButton)
    expect(dialog()).toHaveTextContent(w.introReset)
    expect(m.onAdopt).not.toHaveBeenCalled() // a dry run describes a state that does not exist
    expect(m.onApplied).not.toHaveBeenCalled()
    expect(screen.getByTestId('api-config-load-error')).toBeInTheDocument() // still as it was
  })

  it('removes nothing when it is cancelled, by its button or by Escape, and the keyboard is back on the button', async () => {
    resetCall()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(cancelButton())
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await focused(resetButton)
    fireEvent.click(resetButton()) // asks again, and is asked again
    await screen.findByRole('alertdialog')
    fireEvent.keyDown(dialog(), { key: 'Escape' })
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await focused(resetButton)
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true], [false, true]]) // nothing was ever confirmed
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(m.onApplied).not.toHaveBeenCalled()
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
  })

  it('removes them, confirmed, only when the dialog says yes, and adopts what that gave: the damage is gone, every row is at its default, the rest is read again', async () => {
    const { dry, done } = resetCall()
    const m = await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }) })
    fireEvent.change(maxInput(), { target: { value: '12' } }) // typed, not saved: the reset takes it with the rest
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))

    expect(App.APIConfigReset.mock.calls).toEqual([
      [false, true], // what would it do: nothing is confirmed in a dry run
      [true, false], // do it: confirmed, and only after the dialog said yes
    ])
    expect(m.onAdopt.mock.calls[0][0]).toBe(done) // what was made, and never the dry run
    expect(m.onAdopt.mock.calls[0][0]).not.toBe(dry)
    expect(m.onApplied).toHaveBeenCalledTimes(1) // the status and the models failed for the same row
    expect(m.onRetry).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-load-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-chip-damaged')).not.toBeInTheDocument()
    expect(maxInput()).toHaveValue(null) // nothing saved, nothing typed: the field says the default
    expect(maxInput()).toHaveAttribute('placeholder', 'Default: 4')
    expect(screen.queryByRole('button', { name: c.reset.button })).not.toBeInTheDocument()
  })

  it('says it was done, and moves the keyboard there: the button it was pressed on is gone', async () => {
    resetCall()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    const note = await screen.findByTestId('api-config-reset-done')
    expect(note).toHaveTextContent(c.reset.done)
    expect(note).toHaveAttribute('role', 'status')
    await focused(() => note)
  })

  it('takes what the rows said, and what was typed, away with the settings they were about', async () => {
    resetCall()
    const saved = changeDoc(configDoc({ saved: { max_concurrent: '8' }, running: {} }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
      if (values.turn_timeout) throw new Error(`invalid_input: ${DAMAGED}`) // the row was damaged after the page read it
      return dryRun ? { ...saved, applied: false } : saved
    })
    const m = await mountOpen({ config: configDoc({ running: {} }), err: null })
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    expect(await within(screen.getByTestId('api-config-row-max_concurrent')).findByText(c.note.saved)).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), { target: { value: '15m' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.turn_timeout.label) }))
    expect(await within(screen.getByTestId('api-config-row-turn_timeout')).findByRole('alert')).toHaveTextContent(DAMAGED)
    await m.read(damaged()) // and a read found it so too: the block offers the way out

    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(2)) // the save, and the reset
    expect(within(screen.getByTestId('api-config-row-max_concurrent')).queryByText(c.note.saved)).not.toBeInTheDocument()
    expect(within(screen.getByTestId('api-config-row-turn_timeout')).queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveValue('')
  })

  it('does not say it for a document that is not the one it came to', async () => {
    resetCall()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    await screen.findByTestId('api-config-reset-done')
    // another change is made (or the settings are read again): the document on show is a newer one
    const saved = changeDoc(configDoc({ saved: { max_concurrent: '8' }, running: {} }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? { ...saved, applied: false } : saved))
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(2))
    expect(screen.queryByTestId('api-config-reset-done')).not.toBeInTheDocument()
  })

  it('does not remove anything, and reads again, when the row can be read after all', async () => {
    // someone fixed it meanwhile: the dry run does not say a row that cannot be read would be removed
    const { dry } = resetCall({ dry: { ...changeDoc(configDoc({ saved: { max_concurrent: '8' } }), { applied: false, widening: [] }) } })
    expect(dry.removed_unreadable_row).toBeUndefined()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await waitFor(() => expect(m.onRetry).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument() // there is nothing to ask
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true]]) // and nothing was removed: the one call was a dry run
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(m.onApplied).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(resetButton()).toBeEnabled() // not left working
  })

  it('removes nothing when the settings were read while the dialog was open and the row can be read now: its yes was for a row that cannot', async () => {
    resetCall()
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    await m.read(null) // a read that found the row fixed meanwhile
    fireEvent.click(confirmButton())
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true]]) // never confirmed
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(m.onApplied).not.toHaveBeenCalled()
    cleanup()

    // nor when that read failed some other way: what is saved is not known to be damaged
    const other = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    await other.read(said(new Error('database is locked')))
    fireEvent.click(confirmButton())
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true], [false, true]])
    expect(other.onAdopt).not.toHaveBeenCalled()
  })

  it('asks even when the CLI lists no reason: removing the saved settings is not something to do on one click', async () => {
    resetCall({ dry: { ...dryDoc(), widening: [] } })
    await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog', { name: w.title })
    expect(within(dialog()).queryAllByRole('listitem')).toHaveLength(0)
    expect(confirmButton()).toBeInTheDocument()
  })
})

describe('a saved row that cannot be read: when resetting fails', () => {
  it('says why when asking fails, next to the buttons, opens no dialog, and keeps the way out', async () => {
    App.APIConfigReset.mockRejectedValue(new Error('database is locked'))
    const m = await mountOpen()
    fireEvent.click(resetButton())
    const alert = await within(block()).findByRole('alert')
    expect(alert).toHaveTextContent('database is locked')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(resetButton()).toBeEnabled()
    expect(retryButton()).toBeEnabled()
    await focused(resetButton)
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(DAMAGED) // still as it was

    // tried again, it starts clean: what the last try said is not left on screen behind the dialog
    resetCall()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('says why when the confirmed call fails, adopts nothing, and leaves the settings as damaged as they were', async () => {
    App.APIConfigReset.mockImplementation(async (confirm, dryRun) => {
      if (dryRun) return dryDoc()
      throw new Error('database is locked')
    })
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    const alert = await within(block()).findByRole('alert')
    expect(alert).toHaveTextContent('database is locked')
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(m.onApplied).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(DAMAGED)
    expect(resetButton()).toBeEnabled()
    await focused(resetButton)
    expect(App.APIConfigReset.mock.calls).toEqual([[false, true], [true, false]])
  })

  it('says the CLI\'s words as English on a page that is not', async () => {
    await act(() => i18n.changeLanguage('es'))
    App.APIConfigReset.mockRejectedValue(new Error('database is locked'))
    await mountOpen()
    fireEvent.click(screen.getByRole('button', { name: sc.reset.button }))
    expect(await within(block()).findByRole('alert')).toHaveTextContent('database is locked')
    expect(within(block()).getByRole('alert').querySelector('[lang="en"]')).not.toBeNull()
  })

  it('forgets a failed reset once the settings have been read again, and does not bring it back with the next damage', async () => {
    App.APIConfigReset.mockRejectedValue(new Error('database is locked'))
    const m = await mountOpen()
    fireEvent.click(resetButton())
    await within(block()).findByRole('alert')
    await m.read(damaged()) // a read that found it as it was: what the last reset said is about a state that was read since
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    fireEvent.click(resetButton())
    await within(block()).findByRole('alert')
    await m.read(null) // the row can be read now
    await m.read(damaged()) // and is damaged again, later
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('a saved row that cannot be read: found while a setting is being changed', () => {
  const saveButton = (label) => screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', label) })
  const row = (id) => screen.getByTestId(`api-config-row-${id}`)

  it('reads the settings again, so that the way out comes with the read, and does so for no other failure: a save', async () => {
    App.APIConfigSet.mockRejectedValue(new Error(`invalid_input: ${DAMAGED}`))
    const m = await mountOpen({ config: configDoc({ running: {} }), err: null })
    fireEvent.change(maxInput(), { target: { value: '8' } })
    fireEvent.click(saveButton(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent(DAMAGED) // the CLI's words, next to the setting
    expect(m.onRetry).toHaveBeenCalledTimes(1)

    App.APIConfigSet.mockRejectedValue(new Error('database is locked')) // any other failure is only said
    fireEvent.click(saveButton(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByText('database is locked')).toBeInTheDocument()
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: max_concurrent must be an integer from 1 to 64'))
    fireEvent.click(saveButton(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByText(c.errors.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))).toBeInTheDocument()
    expect(m.onRetry).toHaveBeenCalledTimes(1)
  })

  it('reads the settings again when going back to the default finds it damaged', async () => {
    App.APIConfigUnset.mockRejectedValue(new Error(`invalid_input: ${DAMAGED}`))
    const m = await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }), err: null })
    fireEvent.click(screen.getByRole('button', { name: c.useDefaultLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent(DAMAGED)
    expect(m.onRetry).toHaveBeenCalledTimes(1)
  })

  it('reads the settings again when the change that was confirmed finds it damaged', async () => {
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
      if (dryRun) return { ...changeDoc(configDoc({ running: {} }), { applied: false, changed: ['context_confinement'], widening: [WIDENING.contextLoopback] }) }
      throw new Error(`invalid_input: ${DAMAGED}`) // damaged between the dry run and the yes
    })
    const m = await mountOpen({ config: configDoc({ running: {} }), err: null })
    fireEvent.change(screen.getByRole('combobox', { name: c.rows.context_confinement.label }), { target: { value: 'sandboxed' } })
    fireEvent.click(saveButton(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
    expect(await within(row('context_confinement')).findByRole('alert')).toHaveTextContent(DAMAGED)
    expect(m.onRetry).toHaveBeenCalledTimes(1)
  })
})

describe('a saved row that cannot be read: while a call runs', () => {
  it('disables both buttons while the CLI is asked, says it is working, and starts no second call', async () => {
    let release
    App.APIConfigReset.mockReturnValueOnce(new Promise(r => { release = r }))
    const m = await mountOpen()
    fireEvent.click(resetButton())
    const working = await screen.findByRole('button', { name: c.reset.working })
    expect(working).toBeDisabled()
    expect(retryButton()).toBeDisabled()
    fireEvent.click(working); fireEvent.click(retryButton())
    expect(App.APIConfigReset).toHaveBeenCalledTimes(1)
    expect(m.onRetry).not.toHaveBeenCalled()
    await act(async () => { release(dryDoc()) })
    await screen.findByRole('alertdialog')
  })

  it('disables them, and the rows that are on show, while the confirmed call runs', async () => {
    let release
    App.APIConfigReset.mockImplementation((confirm, dryRun) => (dryRun ? Promise.resolve(dryDoc()) : new Promise(r => { release = r })))
    const m = await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }) })
    fireEvent.click(resetButton())
    await screen.findByRole('alertdialog')
    fireEvent.click(confirmButton())
    const working = await screen.findByRole('button', { name: c.reset.working })
    expect(working).toBeDisabled()
    expect(retryButton()).toBeDisabled()
    expect(maxInput()).toBeDisabled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigReset).toHaveBeenCalledTimes(2)
    await act(async () => { release(doneDoc()) })
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigReset).toHaveBeenCalledTimes(2)
  })
})

describe('a saved row that cannot be read: in Spanish', () => {
  it('speaks the chosen language in the button, the dialog and the note, and the CLI\'s reason stays as it said it', async () => {
    await act(() => i18n.changeLanguage('es'))
    resetCall()
    const m = await mountOpen()
    fireEvent.click(screen.getByRole('button', { name: sc.reset.button }))
    const d = await screen.findByRole('alertdialog', { name: sc.widening.title })
    await focused(() => within(d).getByRole('button', { name: sc.widening.cancel }))
    expect(within(d).getByText(sc.widening.kind.saved_settings)).toBeInTheDocument()
    expect(within(d).getByText(WIDENING.savedSettings.reason)).toHaveAttribute('lang', 'en')
    fireEvent.click(within(d).getByRole('button', { name: sc.widening.confirmReset }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(await screen.findByTestId('api-config-reset-done')).toHaveTextContent(sc.reset.done)
    expect(sc.reset.done).not.toBe(c.reset.done)
    expect(sc.reset.working).not.toBe(c.reset.working)
  })
})
