// @vitest-environment jsdom
// The "Server settings" block, editing: typing, saving a setting, going back to the default, what a call does to the
// controls and what comes of a failure. (A change that widens the server has its own file: ApiConfigBlock.widening.)
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { changeDoc, configDoc } from './__fixtures__/configFixtures.js'

const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIConfigSet', 'APIConfigUnset', 'DaemonRestart']) App[k] = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

const c = en.settings.api.config
const base = () => configDoc({ saved: { max_concurrent: '6' }, running: {}, autostart: true })

async function mountOpen(props = {}) {
  const { default: ApiConfigBlock } = await import('./ApiConfigBlock.jsx')
  const p = { config: base(), err: null, onRetry: vi.fn(), onAdopt: vi.fn(), onReload: vi.fn(), ...props }
  const view = render(<ApiConfigBlock {...p} />)
  fireEvent.click(screen.getByTestId('api-config-toggle'))
  return { ...p, rerender: (more) => view.rerender(<ApiConfigBlock {...p} {...more} />) }
}
const row = (id) => screen.getByTestId(`api-config-row-${id}`)
const typeInto = (el, value) => fireEvent.change(el, { target: { value } })
const maxInput = () => screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })
const saveBtn = (label) => screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', label) })
const defaultBtn = (label) => screen.getByRole('button', { name: c.useDefaultLabel.replace('{{setting}}', label) })
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })
const after = (doc, over) => changeDoc(doc, over)

describe('editing a setting', () => {
  it('enables Save only for a change, and never for an empty field: that is "Use the default"', async () => {
    await mountOpen()
    const save = saveBtn(c.rows.max_concurrent.label)
    expect(save).toBeDisabled() // nothing was typed
    typeInto(maxInput(), '8')
    expect(save).toBeEnabled()
    typeInto(maxInput(), '6') // back to what is saved
    expect(save).toBeDisabled()
    typeInto(maxInput(), '')
    expect(save).toBeDisabled()
    typeInto(maxInput(), '9')
    expect(save).toBeEnabled()
    // another row is untouched by it
    expect(saveBtn(c.rows.turn_timeout.label)).toBeDisabled()
  })

  it('enables "Use the default" only where something is saved, and keeps a saved value shown while it is edited', async () => {
    await mountOpen()
    expect(defaultBtn(c.rows.max_concurrent.label)).toBeEnabled()
    expect(defaultBtn(c.rows.turn_timeout.label)).toBeDisabled() // nothing saved: it already is the default
    typeInto(maxInput(), '')
    expect(defaultBtn(c.rows.max_concurrent.label)).toBeEnabled()
  })

  it('keeps what is typed in one row when another is saved, and when the document is read again', async () => {
    const m = await mountOpen()
    typeInto(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), '15m')
    m.rerender({ config: configDoc({ saved: { max_concurrent: '7' }, running: {}, autostart: true }) }) // a read that found another value
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveValue('15m')
    expect(maxInput()).toHaveValue(7) // what was not edited follows what is saved
  })

  it('has accessible names that say which setting a button is for', async () => {
    await mountOpen()
    expect(saveBtn(c.rows.tool_runtimes.label)).toHaveTextContent('Save')
    expect(defaultBtn(c.rows.tool_runtimes.label)).toHaveTextContent('Use the default')
    expect(screen.getAllByRole('button', { name: /^Save / })).toHaveLength(9)
    expect(screen.getAllByRole('button', { name: /^Use the default for / })).toHaveLength(9)
  })
})

describe('saving a setting', () => {
  it('asks the CLI what the change would do first, then makes it, and shows the state that came back', async () => {
    const doc = base()
    const saved = after(configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['6', 'saved'] }, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(saved, { applied: false }) : saved))
    const m = await mountOpen({ config: doc })
    typeInto(maxInput(), '8')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))

    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigSet.mock.calls).toEqual([
      [{ max_concurrent: '8' }, false, true], // what would it do
      [{ max_concurrent: '8' }, false, false], // do it: not confirmed, since nothing widens
    ])
    // only the state after the change is adopted, never the dry run's (which says applied: false)
    expect(m.onAdopt.mock.calls[0][0]).toBe(saved)
    expect(m.onAdopt.mock.calls[0][0].applied).toBe(true)
    expect(App.APIConfigUnset).not.toHaveBeenCalled()
    expect(await within(row('max_concurrent')).findByRole('status')).toHaveTextContent(c.note.saved) // said where a screen reader will hear it
  })

  it('shows what is saved after it, not what was typed: 900s is saved as 15m', async () => {
    const saved = after(configDoc({ saved: { turn_timeout: '15m' }, running: {}, autostart: true }), { changed: ['turn_timeout'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(saved, { applied: false }) : saved))
    const m = await mountOpen()
    typeInto(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), '900s')
    fireEvent.click(saveBtn(c.rows.turn_timeout.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    m.rerender({ config: m.onAdopt.mock.calls[0][0] }) // the section hands the adopted document back
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveValue('15m')
  })

  it('says nothing changed when the CLI found it already saved so: 900s is 15m', async () => {
    const doc = configDoc({ saved: { turn_timeout: '15m' }, running: {}, autostart: true })
    const same = after(doc, { changed: [] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(same, { applied: false }) : same))
    const m = await mountOpen({ config: doc })
    typeInto(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), '900s')
    fireEvent.click(saveBtn(c.rows.turn_timeout.label))
    expect(await within(row('turn_timeout')).findByText(c.note.unchanged)).toBeInTheDocument()
    expect(m.onAdopt).toHaveBeenCalledTimes(1)
  })

  it('saves the two TLS files in one call, only the ones that changed, and puts both back to the default in one call', async () => {
    const pair = { tls_cert_file: '/etc/api.pem', tls_key_file: '/etc/api.key' }
    const m = await mountOpen({ config: configDoc({ saved: pair, running: {}, autostart: true }) })
    const tls = within(row('tls'))
    const next = after(configDoc({ saved: { ...pair, tls_cert_file: '/etc/new.pem' }, running: {}, autostart: true }), { changed: ['tls_cert_file'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(next, { applied: false }) : next))
    typeInto(tls.getByRole('textbox', { name: c.rows.tls.certLabel }), '/etc/new.pem')
    fireEvent.click(saveBtn(c.rows.tls.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigSet.mock.calls.map(([values]) => values)).toEqual([{ tls_cert_file: '/etc/new.pem' }, { tls_cert_file: '/etc/new.pem' }])

    const gone = after(configDoc({ running: {}, autostart: true }), { changed: ['tls_cert_file', 'tls_key_file'] })
    App.APIConfigUnset.mockImplementation(async (keys, confirm, dryRun) => (dryRun ? after(gone, { applied: false }) : gone))
    await settle()
    fireEvent.click(defaultBtn(c.rows.tls.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(2))
    expect(App.APIConfigUnset.mock.calls).toEqual([
      [['tls_cert_file', 'tls_key_file'], false, true],
      [['tls_cert_file', 'tls_key_file'], false, false],
    ])
  })

  it('does not send a second file the user did not touch, and lets the CLI say a pair needs both', async () => {
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: tls_cert_file and tls_key_file must be set together'))
    const m = await mountOpen({ config: configDoc({ running: {}, autostart: true }) })
    typeInto(within(row('tls')).getByRole('textbox', { name: c.rows.tls.certLabel }), '/etc/api.pem')
    fireEvent.click(saveBtn(c.rows.tls.label))
    expect(await within(row('tls')).findByRole('alert')).toHaveTextContent(c.errors.tlsPair)
    expect(App.APIConfigSet).toHaveBeenCalledTimes(1) // the dry run said no: nothing was made
    expect(App.APIConfigSet.mock.calls[0][0]).toEqual({ tls_cert_file: '/etc/api.pem' })
    expect(m.onAdopt).not.toHaveBeenCalled()
  })
})

describe('going back to the default', () => {
  it('removes the saved value after asking the CLI what that would do, and says so', async () => {
    const gone = after(configDoc({ running: { max_concurrent: ['6', 'saved'] }, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigUnset.mockImplementation(async (keys, confirm, dryRun) => (dryRun ? after(gone, { applied: false }) : gone))
    const m = await mountOpen()
    fireEvent.click(defaultBtn(c.rows.max_concurrent.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigUnset.mock.calls).toEqual([[['max_concurrent'], false, true], [['max_concurrent'], false, false]])
    expect(m.onAdopt.mock.calls[0][0]).toBe(gone)
    expect(App.APIConfigSet).not.toHaveBeenCalled()
    expect(await within(row('max_concurrent')).findByText(c.note.removed)).toBeInTheDocument()
  })

  it('drops what was typed in that row, and only that row', async () => {
    const gone = after(configDoc({ running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigUnset.mockImplementation(async (keys, confirm, dryRun) => (dryRun ? after(gone, { applied: false }) : gone))
    const m = await mountOpen()
    typeInto(maxInput(), '12')
    typeInto(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), '20m')
    fireEvent.click(defaultBtn(c.rows.max_concurrent.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    m.rerender({ config: gone })
    expect(maxInput()).toHaveValue(null) // the default is a placeholder, not a value
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveValue('20m')
  })
})

describe('what a call does to the controls', () => {
  it('disables every control while a call runs, says which one is saving, and lets a second save start only after it', async () => {
    let release
    const saved = after(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation((values, confirm, dryRun) => (dryRun
      ? new Promise(r => { release = () => r(after(saved, { applied: false })) })
      : Promise.resolve(saved)))
    const m = await mountOpen()
    typeInto(maxInput(), '8')
    typeInto(screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), '20m')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    fireEvent.click(saveBtn(c.rows.max_concurrent.label)) // a second click while the call runs
    await waitFor(() => expect(App.APIConfigSet).toHaveBeenCalledTimes(1))

    expect(within(row('max_concurrent')).getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) })).toHaveTextContent(c.saving)
    expect(saveBtn(c.rows.turn_timeout.label)).toHaveTextContent(c.save) // only the row that is saving says so
    for (const el of [...screen.getAllByRole('button', { name: /^(Save|Use the default) / }), maxInput(), screen.getByRole('textbox', { name: c.rows.turn_timeout.label }), screen.getByRole('combobox', { name: c.rows.confinement.label })]) {
      expect(el).toBeDisabled()
    }
    await act(async () => { release() })
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigSet).toHaveBeenCalledTimes(2) // one dry run and one save: the second click started nothing
    expect(saveBtn(c.rows.turn_timeout.label)).toBeEnabled() // the other row's edit is still there, and can be saved now
    expect(maxInput()).toBeEnabled()
  })

  it('does not start a second call for a key pressed twice before the first has been drawn', async () => {
    let release
    const saved = after(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation((values, confirm, dryRun) => (dryRun
      ? new Promise(r => { release = () => r(after(saved, { applied: false })) })
      : Promise.resolve(saved)))
    const m = await mountOpen()
    typeInto(maxInput(), '8')
    act(() => { // both land in the same tick: the page has not had the chance to disable anything
      fireEvent.keyDown(maxInput(), { key: 'Enter' })
      fireEvent.keyDown(maxInput(), { key: 'Enter' })
    })
    expect(App.APIConfigSet).toHaveBeenCalledTimes(1)
    await act(async () => { release() })
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigSet).toHaveBeenCalledTimes(2) // one dry run and one save
  })

  it('says Saving only for a save: while a value is removed the Save button keeps its name', async () => {
    let release
    const gone = after(configDoc({ running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigUnset.mockImplementation((keys, confirm, dryRun) => (dryRun
      ? new Promise(r => { release = () => r(after(gone, { applied: false })) })
      : Promise.resolve(gone)))
    const m = await mountOpen()
    fireEvent.click(defaultBtn(c.rows.max_concurrent.label))
    await waitFor(() => expect(App.APIConfigUnset).toHaveBeenCalledTimes(1))
    expect(saveBtn(c.rows.max_concurrent.label)).toHaveTextContent(c.save)
    expect(saveBtn(c.rows.max_concurrent.label)).toBeDisabled()
    await act(async () => { release() })
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
  })

  it('gives the keyboard back to the row it was using, after a save and after a failure', async () => {
    const saved = after(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(saved, { applied: false }) : saved))
    const m = await mountOpen()
    typeInto(maxInput(), '8')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(maxInput()).toHaveFocus())

    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: max_concurrent must be an integer from 1 to 64'))
    const timeout = screen.getByRole('textbox', { name: c.rows.turn_timeout.label })
    typeInto(timeout, '1s')
    fireEvent.click(saveBtn(c.rows.turn_timeout.label))
    await within(row('turn_timeout')).findByRole('alert')
    await waitFor(() => expect(timeout).toHaveFocus())
  })
})

describe('when a call fails', () => {
  it('says why next to the setting, in the page\'s words, keeps what was typed, and makes nothing', async () => {
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: max_concurrent must be an integer from 1 to 64'))
    const m = await mountOpen()
    typeInto(maxInput(), '99')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    const alert = await within(row('max_concurrent')).findByRole('alert')
    expect(alert).toHaveTextContent(c.errors.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))
    expect(App.APIConfigSet).toHaveBeenCalledTimes(1) // the dry run refused it: there was nothing to make
    expect(maxInput()).toHaveValue(99)
    expect(saveBtn(c.rows.max_concurrent.label)).toBeEnabled() // it can be corrected and tried again
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(within(row('turn_timeout')).queryByRole('alert')).not.toBeInTheDocument() // only the row it is about
  })

  it('says it as the CLI said it when the page has no words for it, and marks it English on a page that is not', async () => {
    App.APIConfigSet.mockRejectedValue(new Error('the saved API settings (settings table, key api_gateway_config) are not a JSON object'))
    await mountOpen()
    typeInto(maxInput(), '8')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent('the saved API settings (settings table, key api_gateway_config) are not a JSON object')
    expect(screen.queryByText(/not a JSON object/)).not.toHaveAttribute('lang')
  })

  it('says it when the change itself fails after the CLI said it was fine, and does not adopt anything', async () => {
    const saved = after(base(), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
      if (dryRun) return after(saved, { applied: false })
      throw new Error('database is locked')
    })
    const m = await mountOpen()
    typeInto(maxInput(), '8')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent('database is locked')
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(maxInput()).toHaveValue(8)
  })

  it('says a change that widened meanwhile in the page\'s words, and asks to start over', async () => {
    const saved = after(base(), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
      if (dryRun) return after(saved, { applied: false })
      throw new Error('invalid_input: this change makes the server reach further: A /v1 listener beyond this machine would serve runtimes up to any. Pass --yes to make the change anyway.')
    })
    await mountOpen()
    typeInto(maxInput(), '8')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent(c.errors.widenedMeanwhile)
  })

  it('clears what was said when the row is edited again', async () => {
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: max_concurrent must be an integer from 1 to 64'))
    await mountOpen()
    typeInto(maxInput(), '99')
    fireEvent.click(saveBtn(c.rows.max_concurrent.label))
    await within(row('max_concurrent')).findByRole('alert')
    typeInto(maxInput(), '9')
    expect(within(row('max_concurrent')).queryByRole('alert')).not.toBeInTheDocument()
  })

  it('says a failure to remove a value, next to the setting', async () => {
    App.APIConfigUnset.mockRejectedValue(new Error('boom'))
    const m = await mountOpen()
    fireEvent.click(defaultBtn(c.rows.max_concurrent.label))
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent('boom')
    expect(m.onAdopt).not.toHaveBeenCalled()
  })
})

describe('with the keyboard', () => {
  it('saves a setting with Enter in its field, when there is something to save', async () => {
    const saved = after(configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true }), { changed: ['max_concurrent'] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? after(saved, { applied: false }) : saved))
    const m = await mountOpen()
    fireEvent.keyDown(maxInput(), { key: 'Enter' }) // nothing was typed
    await settle()
    expect(App.APIConfigSet).not.toHaveBeenCalled()
    typeInto(maxInput(), '')
    fireEvent.keyDown(maxInput(), { key: 'Enter' }) // an empty field is not a value
    await settle()
    expect(App.APIConfigSet).not.toHaveBeenCalled()
    typeInto(maxInput(), '8')
    fireEvent.keyDown(maxInput(), { key: 'Enter' })
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigSet.mock.calls[1]).toEqual([{ max_concurrent: '8' }, false, false])
  })

  it('does not take the Enter of a text being composed for a save', async () => {
    await mountOpen()
    typeInto(maxInput(), '8')
    fireEvent.keyDown(maxInput(), { key: 'Enter', isComposing: true })
    await settle()
    expect(App.APIConfigSet).not.toHaveBeenCalled()
  })

  it('reaches each control in the order of the page: the field, Save, Use the default, and on to the next row', async () => {
    await mountOpen({ config: configDoc({ saved: { v1_addr: ':9443' }, running: {}, autostart: true }) })
    const inRow = within(row('v1_addr'))
    const order = [inRow.getByRole('textbox', { name: c.rows.v1_addr.label }), inRow.getByRole('button', { name: /^Save / }), inRow.getByRole('button', { name: /^Use the default for / })]
    const all = [...document.querySelectorAll('input, select, button')]
    const idx = order.map(el => all.indexOf(el))
    expect(idx).toEqual([...idx].sort((a, b) => a - b))
    expect(idx[1]).toBe(idx[0] + 1)
    expect(idx[2]).toBe(idx[1] + 1)
  })
})

describe('in Spanish', () => {
  it('words the buttons, what a call says and what a failure says', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: max_concurrent must be an integer from 1 to 64'))
    await mountOpen()
    const input = screen.getByRole('spinbutton', { name: s.rows.max_concurrent.label })
    typeInto(input, '99')
    const save = screen.getByRole('button', { name: s.saveLabel.replace('{{setting}}', s.rows.max_concurrent.label) })
    expect(save).toHaveTextContent(s.save)
    expect(screen.getByRole('button', { name: s.useDefaultLabel.replace('{{setting}}', s.rows.max_concurrent.label) })).toHaveTextContent(s.useDefault)
    fireEvent.click(save)
    expect(await within(row('max_concurrent')).findByRole('alert')).toHaveTextContent(s.errors.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))
    expect(s.save).not.toBe(c.save)
  })
})
