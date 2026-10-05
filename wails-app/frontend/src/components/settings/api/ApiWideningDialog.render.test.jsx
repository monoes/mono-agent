// @vitest-environment jsdom
// The dialog that lists what a change makes the server reach, before it is made: the CLI's reasons exactly as it
// worded them, Cancel where the focus starts, and a button that says what it does.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { WIDENING } from './__fixtures__/configFixtures.js'

let offsetParent
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  // jsdom has no layout: every element has a null offsetParent, which the Tab trap reads as "not visible".
  offsetParent = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetParent')
  Object.defineProperty(HTMLElement.prototype, 'offsetParent', { configurable: true, get() { return this.parentNode } })
})
afterEach(() => {
  cleanup()
  if (offsetParent) Object.defineProperty(HTMLElement.prototype, 'offsetParent', offsetParent)
})

const w = en.settings.api.config.widening
async function mount(props = {}) {
  const { default: ApiWideningDialog } = await import('./ApiWideningDialog.jsx')
  const p = { widening: [WIDENING.v1_addr, WIDENING.confinementNetwork], kind: 'set', onCancel: vi.fn(), onConfirm: vi.fn(), ...props }
  render(<ApiWideningDialog {...p} />)
  return p
}
const dialog = () => screen.getByRole('alertdialog', { name: w.title })
const cancel = () => within(dialog()).getByRole('button', { name: w.cancel })
const confirmButton = (name) => within(dialog()).getByRole('button', { name })

describe('ApiWideningDialog', () => {
  it('lists each reason exactly as the CLI worded it, under a plain heading for what kind of reach it is', async () => {
    await mount({ widening: [WIDENING.v1_addr, WIDENING.confinementNetwork, WIDENING.contextLoopback, WIDENING.autoNetwork, WIDENING.imageRuntimes, WIDENING.toolRuntimes] })
    const items = within(dialog()).getAllByRole('listitem')
    expect(items.map(li => li.textContent)).toEqual([
      w.kind.v1_addr + WIDENING.v1_addr.reason,
      w.kind.confinement + WIDENING.confinementNetwork.reason,
      w.kind.context_confinement + WIDENING.contextLoopback.reason,
      w.kind.auto_confinement + WIDENING.autoNetwork.reason,
      w.kind.image_runtimes + WIDENING.imageRuntimes.reason,
      w.kind.tool_runtimes + WIDENING.toolRuntimes.reason,
    ])
    expect(dialog()).toHaveAccessibleDescription(new RegExp(w.introSave))
    expect(dialog()).toHaveTextContent(w.note) // and when it takes effect
  })

  it('shows the reason of a kind this page has no heading for, all the same', async () => {
    await mount({ widening: [{ key: 'something_new.network', reason: 'The server would do something new.' }, WIDENING.v1_addr] })
    const items = within(dialog()).getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('The server would do something new.')
    expect(items[0].textContent).toBe('The server would do something new.')
    expect(items[1]).toHaveTextContent(w.kind.v1_addr)
  })

  it('starts with the focus on Cancel, which comes first, and says what the other button does', async () => {
    await mount()
    expect(cancel()).toHaveFocus()
    const buttons = within(dialog()).getAllByRole('button')
    expect(buttons.map(b => b.textContent)).toEqual([w.cancel, w.confirmSave])
    expect(screen.queryByRole('button', { name: /^(OK|Ok|Yes|Confirm)$/ })).not.toBeInTheDocument()
  })

  it('says it for removing a saved value too, in its own words', async () => {
    await mount({ kind: 'unset' })
    expect(within(dialog()).getAllByRole('button').map(b => b.textContent)).toEqual([w.cancel, w.confirmUnset])
    expect(dialog()).toHaveAccessibleDescription(new RegExp(w.introUnset))
    expect(w.confirmUnset).not.toBe(w.confirmSave)
  })

  it('says it for resetting the saved settings too (the CLI\'s reason under what is unknown), in words of its own', async () => {
    await mount({ kind: 'reset', widening: [WIDENING.savedSettings] })
    expect(within(dialog()).getAllByRole('listitem').map(li => li.textContent)).toEqual([w.kind.saved_settings + WIDENING.savedSettings.reason])
    expect(within(dialog()).getAllByRole('button').map(b => b.textContent)).toEqual([w.cancel, w.confirmReset])
    expect(dialog()).toHaveAccessibleDescription(new RegExp(w.introReset))
    expect(dialog()).toHaveTextContent(w.note)
    expect(cancel()).toHaveFocus()
    // three things that are done, and each says which
    expect(new Set([w.confirmSave, w.confirmUnset, w.confirmReset]).size).toBe(3)
    expect(new Set([w.introSave, w.introUnset, w.introReset]).size).toBe(3)
  })

  it('cancels with its button, with Escape, and with a click on the page behind it, and confirms with nothing but its own button', async () => {
    const p = await mount()
    fireEvent.click(cancel())
    expect(p.onCancel).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(dialog(), { key: 'Escape' })
    expect(p.onCancel).toHaveBeenCalledTimes(2)
    fireEvent.click(dialog().parentElement) // the overlay
    expect(p.onCancel).toHaveBeenCalledTimes(3)
    fireEvent.click(dialog()) // a click inside is not a click outside
    expect(p.onCancel).toHaveBeenCalledTimes(3)
    fireEvent.keyDown(dialog(), { key: 'Enter' }) // Enter does not stand for a yes
    fireEvent.keyDown(cancel(), { key: 'Enter' })
    fireEvent.keyDown(document.body, { key: 'Enter' })
    expect(p.onConfirm).not.toHaveBeenCalled()
    fireEvent.click(confirmButton(w.confirmSave))
    expect(p.onConfirm).toHaveBeenCalledTimes(1)
    expect(p.onCancel).toHaveBeenCalledTimes(3)
  })

  it('keeps Tab inside it, in both directions', async () => {
    await mount()
    const ok = confirmButton(w.confirmSave)
    ok.focus()
    fireEvent.keyDown(ok, { key: 'Tab' })
    expect(cancel()).toHaveFocus()
    fireEvent.keyDown(cancel(), { key: 'Tab', shiftKey: true })
    expect(ok).toHaveFocus()
  })

  it('speaks the chosen language, and says the CLI\'s sentences are English', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config.widening
    await mount()
    const d = screen.getByRole('alertdialog', { name: s.title })
    expect(within(d).getByRole('button', { name: s.cancel })).toHaveFocus()
    expect(within(d).getByRole('button', { name: s.confirmSave })).toBeInTheDocument()
    expect(within(d).getByText(s.kind.v1_addr)).toBeInTheDocument()
    const reason = within(d).getByText(WIDENING.v1_addr.reason)
    expect(reason).toHaveAttribute('lang', 'en') // the CLI's words, whatever the language of the page
    expect(within(d).getByText(s.kind.v1_addr)).not.toHaveAttribute('lang')
    expect(s.title).not.toBe(w.title)
  })

  it('speaks Spanish for resetting the saved settings too, and the CLI\'s reason stays English', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config.widening
    await mount({ kind: 'reset', widening: [WIDENING.savedSettings] })
    const d = screen.getByRole('alertdialog', { name: s.title })
    expect(within(d).getAllByRole('button').map(b => b.textContent)).toEqual([s.cancel, s.confirmReset])
    expect(within(d).getByText(s.kind.saved_settings)).toBeInTheDocument()
    expect(within(d).getByText(s.introReset)).toBeInTheDocument()
    expect(within(d).getByText(WIDENING.savedSettings.reason)).toHaveAttribute('lang', 'en')
    expect(s.confirmReset).not.toBe(w.confirmReset)
    expect(s.introReset).not.toBe(w.introReset)
    expect(s.kind.saved_settings).not.toBe(w.kind.saved_settings)
  })

  it('does not mark the CLI\'s sentences when the page is in English', async () => {
    await mount()
    expect(within(dialog()).getByText(WIDENING.v1_addr.reason)).not.toHaveAttribute('lang')
  })
})
