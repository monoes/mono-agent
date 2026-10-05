// @vitest-environment jsdom
// A change that makes the server reach further, in the block: the CLI says so (in a dry run), the dialog lists what, and
// only its confirmation makes the change, confirmed. Cancelling makes nothing and changes nothing on screen.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { WIDENING, changeDoc, configDoc } from './__fixtures__/configFixtures.js'

const App = {}
let offsetParent
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIConfigSet', 'APIConfigUnset', 'DaemonRestart']) App[k] = vi.fn()
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
const base = () => configDoc({ saved: { context_confinement: 'chat-only' }, running: {}, autostart: true })

async function mountOpen(props = {}) {
  const { default: ApiConfigBlock } = await import('./ApiConfigBlock.jsx')
  const p = { config: base(), err: null, onRetry: vi.fn(), onAdopt: vi.fn(), ...props }
  render(<ApiConfigBlock {...p} />)
  fireEvent.click(screen.getByTestId('api-config-toggle'))
  return p
}
const row = (id) => screen.getByTestId(`api-config-row-${id}`)
const select = (label) => screen.getByRole('combobox', { name: label })
const save = (label) => screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', label) })
const dialog = (name = w.title) => screen.getByRole('alertdialog', { name })
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })

// A save of the class that `context_confinement` may reach, which the CLI says widens: its dry run lists a reason, the
// call that is confirmed makes it, and an unconfirmed one is refused.
function widenContext(widening = [WIDENING.contextLoopback]) {
  const saved = changeDoc(configDoc({ saved: { context_confinement: 'sandboxed' }, running: { context_confinement: ['chat-only', 'saved'] }, autostart: true }), { changed: ['context_confinement'], widening })
  App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
    if (dryRun) return { ...saved, applied: false }
    if (!confirm) throw new Error('invalid_input: this change makes the server reach further: ' + widening.map(x => x.reason).join(' ') + ' Pass --yes to make the change anyway.')
    return saved
  })
  return saved
}
const choose = (value) => fireEvent.change(select(c.rows.context_confinement.label), { target: { value } })

describe('a change that widens', () => {
  it('asks first: the dialog lists what the CLI said, and nothing has been made, adopted or shown as saved', async () => {
    widenContext([WIDENING.contextLoopback, WIDENING.autoNetwork])
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog', { name: w.title })

    const items = within(dialog()).getAllByRole('listitem')
    expect(items.map(li => li.textContent)).toEqual([
      w.kind.context_confinement + WIDENING.contextLoopback.reason,
      w.kind.auto_confinement + WIDENING.autoNetwork.reason,
    ])
    await waitFor(() => expect(within(dialog()).getByRole('button', { name: w.cancel })).toHaveFocus()) // the dialog moves the focus in an effect
    expect(App.APIConfigSet.mock.calls).toEqual([[{ context_confinement: 'sandboxed' }, false, true]]) // the dry run, alone
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(within(row('context_confinement')).queryByText(c.note.saved)).not.toBeInTheDocument()
    expect(within(row('context_confinement')).queryByRole('alert')).not.toBeInTheDocument()
  })

  it('makes nothing, adopts nothing and keeps what was typed when it is cancelled, by its button or by Escape', async () => {
    widenContext()
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.cancel }))
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigSet).toHaveBeenCalledTimes(1) // the dry run, and nothing after it
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(select(c.rows.context_confinement.label)).toHaveValue('sandboxed') // the draft is still there, to change or to try again
    expect(select(c.rows.context_confinement.label)).toHaveFocus() // and the keyboard is back in the row

    fireEvent.click(save(c.rows.context_confinement.label)) // asks again, and is asked again
    await screen.findByRole('alertdialog')
    fireEvent.keyDown(dialog(), { key: 'Escape' })
    await settle()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.APIConfigSet.mock.calls.map(([, confirm, dryRun]) => [confirm, dryRun])).toEqual([[false, true], [false, true]])
    expect(m.onAdopt).not.toHaveBeenCalled()
  })

  it('makes the change, confirmed, only when the dialog says yes, and adopts what that gave', async () => {
    const saved = widenContext()
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))

    expect(App.APIConfigSet.mock.calls).toEqual([
      [{ context_confinement: 'sandboxed' }, false, true], // what would it do: nothing is confirmed in a dry run
      [{ context_confinement: 'sandboxed' }, true, false], // make it: confirmed, and only after the dialog said yes
    ])
    expect(m.onAdopt.mock.calls[0][0]).toBe(saved)
    expect(await within(row('context_confinement')).findByText(c.note.saved)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await waitFor(() => expect(App.APIConfigSet).toHaveBeenCalledTimes(2))
  })

  it('asks again for the next change that widens: a yes is for that change alone', async () => {
    widenContext()
    const m = await mountOpen()
    for (let i = 0; i < 2; i++) {
      choose('sandboxed') // the same change twice: what was typed went when it was saved, and the field shows what is saved
      fireEvent.click(save(c.rows.context_confinement.label))
      await screen.findByRole('alertdialog')
      fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
      await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(i + 1))
      await settle()
    }
    expect(App.APIConfigSet.mock.calls.filter(([, confirm, dryRun]) => confirm && !dryRun)).toHaveLength(2)
    expect(App.APIConfigSet.mock.calls.filter(([, confirm, dryRun]) => dryRun && !confirm)).toHaveLength(2) // each was asked about first
  })

  it('goes back to the default through the same dialog when that is what widens, in its own words', async () => {
    const gone = changeDoc(configDoc({ running: { image_runtimes: ['none', 'saved'] }, autostart: true }), { changed: ['image_runtimes'], widening: [WIDENING.toolRuntimes] })
    App.APIConfigUnset.mockImplementation(async (keys, confirm, dryRun) => {
      if (dryRun) return { ...gone, applied: false }
      if (!confirm) throw new Error('invalid_input: this change makes the server reach further: ' + WIDENING.toolRuntimes.reason + ' Pass --yes to make the change anyway.')
      return gone
    })
    const m = await mountOpen({ config: configDoc({ saved: { image_runtimes: 'none' }, running: {}, autostart: true }) })
    fireEvent.click(screen.getByRole('button', { name: c.useDefaultLabel.replace('{{setting}}', c.rows.image_runtimes.label) }))
    await screen.findByRole('alertdialog')
    expect(within(dialog()).getAllByRole('button').map(b => b.textContent)).toEqual([w.cancel, w.confirmUnset])
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmUnset }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    expect(App.APIConfigUnset.mock.calls).toEqual([[['image_runtimes'], false, true], [['image_runtimes'], true, false]])
    expect(App.APIConfigSet).not.toHaveBeenCalled()
    expect(await within(row('image_runtimes')).findByText(c.note.removed)).toBeInTheDocument()
  })

  it('says what a confirmed change did that the dialog had not listed, as the CLI worded it', async () => {
    // the saved settings changed between the dry run and the confirmed call: it widens in one more way
    const listed = [WIDENING.contextLoopback]
    const saved = changeDoc(configDoc({ saved: { context_confinement: 'sandboxed' }, running: {}, autostart: true }), { changed: ['context_confinement'], widening: [WIDENING.contextLoopback, WIDENING.autoNetwork] })
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => (dryRun ? { ...saved, applied: false, widening: listed } : saved))
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    const r = within(row('context_confinement'))
    expect(await r.findByText(c.note.also)).toBeInTheDocument()
    expect(r.getByText(WIDENING.autoNetwork.reason)).toBeInTheDocument()
    expect(r.queryByText(WIDENING.contextLoopback.reason)).not.toBeInTheDocument() // that one was listed, and agreed to
  })

  it('says nothing more when the confirmed change did what the dialog listed', async () => {
    widenContext()
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
    await waitFor(() => expect(m.onAdopt).toHaveBeenCalledTimes(1))
    await within(row('context_confinement')).findByText(c.note.saved)
    expect(within(row('context_confinement')).queryByText(c.note.also)).not.toBeInTheDocument()
  })

  it('says why when the confirmed call fails, next to the setting, and adopts nothing', async () => {
    App.APIConfigSet.mockImplementation(async (values, confirm, dryRun) => {
      if (dryRun) return { ...changeDoc(base(), { changed: ['context_confinement'], widening: [WIDENING.contextLoopback] }), applied: false }
      throw new Error('database is locked')
    })
    const m = await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: w.confirmSave }))
    expect(await within(row('context_confinement')).findByRole('alert')).toHaveTextContent('database is locked')
    expect(m.onAdopt).not.toHaveBeenCalled()
    expect(select(c.rows.context_confinement.label)).toHaveValue('sandboxed')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('does not open the dialog for a change that does not widen, or for one the CLI refused', async () => {
    App.APIConfigSet.mockRejectedValue(new Error('invalid_input: confinement must be chat-only, sandboxed or any'))
    await mountOpen()
    choose('sandboxed')
    fireEvent.click(save(c.rows.context_confinement.label))
    expect(await within(row('context_confinement')).findByRole('alert')).toHaveTextContent(c.errors.class)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('speaks Spanish in the dialog, and the CLI\'s reasons stay as it said them', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config
    widenContext()
    await mountOpen()
    fireEvent.change(screen.getByRole('combobox', { name: s.rows.context_confinement.label }), { target: { value: 'sandboxed' } })
    fireEvent.click(screen.getByRole('button', { name: s.saveLabel.replace('{{setting}}', s.rows.context_confinement.label) }))
    const d = await screen.findByRole('alertdialog', { name: s.widening.title })
    await waitFor(() => expect(within(d).getByRole('button', { name: s.widening.cancel })).toHaveFocus()) // the dialog moves the focus in an effect
    expect(within(d).getByText(WIDENING.contextLoopback.reason)).toHaveAttribute('lang', 'en')
  })
})
