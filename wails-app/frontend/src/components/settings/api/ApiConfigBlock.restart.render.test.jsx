// @vitest-environment jsdom
// What applying the saved settings takes, in the block: the banner, the button that restarts the daemon after a
// dialog, the re-reads that follow (the daemon takes a moment to come back), and the commands to run when the daemon
// cannot be restarted from here.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { configDoc } from './__fixtures__/configFixtures.js'

const App = {}
let offsetParent
beforeEach(async () => {
  vi.clearAllMocks()
  // The clock is the test's, but it also runs on its own, so that what waits for a real timer (the library's own) still ends.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  await i18n.changeLanguage('en')
  for (const k of ['APIConfigSet', 'APIConfigUnset', 'DaemonRestart']) App[k] = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
  offsetParent = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetParent')
  Object.defineProperty(HTMLElement.prototype, 'offsetParent', { configurable: true, get() { return this.parentNode } })
})
afterEach(() => {
  cleanup(); delete window.go; delete window.runtime
  vi.useRealTimers()
  if (offsetParent) Object.defineProperty(HTMLElement.prototype, 'offsetParent', offsetParent)
})

const c = en.settings.api.config
const r = c.restart
// A saved value the daemon has not read yet, and one that it has.
const pending = (autostart = true) => configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['4', 'default'] }, autostart })
const settled = () => configDoc({ saved: { max_concurrent: '8' }, running: {}, autostart: true })
const down = () => configDoc({ saved: { max_concurrent: '8' }, autostart: true }) // the daemon is not back yet

// The block as the section holds it: a document that a re-read replaces. `reloads` are the documents the next reads
// find, in order (a read after the last one fails: null), and `onReload` counts them.
async function mountOpen({ config = pending(), reloads = [], ...props } = {}, strict = false) {
  const { default: ApiConfigBlock } = await import('./ApiConfigBlock.jsx')
  const p = { err: null, onRetry: vi.fn(), onAdopt: vi.fn(), onReload: vi.fn(), onApplied: vi.fn(), ...props }
  const queue = [...reloads]
  let push
  function Harness() {
    const [doc, setDoc] = React.useState(config)
    push = setDoc // what the section does when a change is adopted
    const onReload = async () => {
      p.onReload()
      const next = queue.length ? queue.shift() : null
      if (next) setDoc(next)
      return next
    }
    return <ApiConfigBlock {...p} config={doc} onReload={onReload} />
  }
  const view = render(strict ? <React.StrictMode><Harness /></React.StrictMode> : <Harness />)
  fireEvent.click(screen.getByTestId('api-config-toggle'))
  return { ...p, view, push: (doc) => act(() => push(doc)) }
}
const restartBtn = () => screen.getByRole('button', { name: r.button })
const banner = () => screen.getByTestId('api-config-banner')
const dialog = () => screen.getByRole('alertdialog', { name: r.title })
const tick = (ms) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })
const confirmRestart = async () => {
  fireEvent.click(restartBtn())
  await screen.findByRole('alertdialog')
  fireEvent.click(within(dialog()).getByRole('button', { name: r.confirm }))
}

describe('the banner', () => {
  it('offers to restart the daemon when it is registered for auto-start and a restart is needed, or may be', async () => {
    await mountOpen()
    expect(within(banner()).getByText(c.banner.restartTitle)).toBeInTheDocument()
    expect(restartBtn()).toBeEnabled()
    expect(within(banner()).queryByText('monoagentcli daemon')).not.toBeInTheDocument()
    cleanup()
    await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: 'old', autostart: true }) })
    expect(within(banner()).getByText(c.banner.olderBody)).toBeInTheDocument()
    expect(restartBtn()).toBeEnabled()
  })

  it('offers no button when no daemon runs, or when the daemon runs what is saved: there is nothing to restart', async () => {
    await mountOpen({ config: down() })
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument()
    cleanup()
    await mountOpen({ config: settled() })
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-config-banner')).not.toBeInTheDocument()
  })

  it('shows the commands to run instead, each with a copy button, when the daemon is not registered for auto-start', async () => {
    await mountOpen({ config: pending(false) })
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument()
    expect(within(banner()).getByText(r.noAutostart)).toBeInTheDocument()
    expect(within(banner()).getByText(r.install)).toBeInTheDocument()
    expect(within(banner()).getByText('monoagentcli daemon')).toBeInTheDocument()
    expect(within(banner()).getByText('monoagentcli daemon install')).toBeInTheDocument()

    fireEvent.click(within(banner()).getByRole('button', { name: r.copyLabel.replace('{{command}}', 'monoagentcli daemon') }))
    await tick(0)
    expect(window.runtime.ClipboardSetText).toHaveBeenCalledWith('monoagentcli daemon')
    expect(await within(banner()).findByText(en.settings.api.status.copied)).toBeInTheDocument()
    fireEvent.click(within(banner()).getByRole('button', { name: r.copyLabel.replace('{{command}}', 'monoagentcli daemon install') }))
    await tick(0)
    expect(window.runtime.ClipboardSetText).toHaveBeenLastCalledWith('monoagentcli daemon install')
  })

  it('says a copy that did not work', async () => {
    window.runtime.ClipboardSetText.mockRejectedValue(new Error('no clipboard'))
    await mountOpen({ config: pending(false) })
    fireEvent.click(within(banner()).getByRole('button', { name: r.copyLabel.replace('{{command}}', 'monoagentcli daemon') }))
    expect(await within(banner()).findByText(en.settings.api.status.copyFailed)).toBeInTheDocument()
  })
})

describe('asking before a restart', () => {
  it('asks first, says what a restart interrupts and nothing about how it stops, and starts on Cancel', async () => {
    await mountOpen()
    fireEvent.click(restartBtn())
    const d = await screen.findByRole('alertdialog', { name: r.title })
    expect(d).toHaveTextContent(r.body)
    expect(d).toHaveTextContent(r.after)
    expect(d).toHaveTextContent(/workflows/)
    expect(d).toHaveTextContent(/org runs/)
    expect(d).not.toHaveTextContent(/graceful/i) // the service manager ends the process its own way: nothing here claims otherwise
    expect(within(d).getByRole('button', { name: r.cancel })).toHaveFocus()
    expect(within(d).getAllByRole('button').map(b => b.textContent)).toEqual([r.cancel, r.confirm])
    expect(App.DaemonRestart).not.toHaveBeenCalled()
  })

  it('does nothing on Cancel or Escape, and gives the keyboard back to the button', async () => {
    await mountOpen()
    fireEvent.click(restartBtn())
    await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog()).getByRole('button', { name: r.cancel }))
    await tick(0)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(restartBtn()).toHaveFocus()
    fireEvent.click(restartBtn())
    await screen.findByRole('alertdialog')
    fireEvent.keyDown(dialog(), { key: 'Escape' })
    await tick(0)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(App.DaemonRestart).not.toHaveBeenCalled()
  })
})

describe('restarting', () => {
  it('restarts through the CLI once, says it is restarting, and keeps every other control still meanwhile', async () => {
    let release
    App.DaemonRestart.mockReturnValue(new Promise(res => { release = () => res({ restarted: true, via: 'launchd' }) }))
    const m = await mountOpen()
    await confirmRestart()
    await tick(0)
    expect(App.DaemonRestart).toHaveBeenCalledTimes(1)
    expect(within(banner()).getByRole('button', { name: r.working })).toBeDisabled()
    for (const el of [screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label }), ...screen.getAllByRole('button', { name: /^(Save|Use the default) / })]) expect(el).toBeDisabled()
    expect(m.onReload).not.toHaveBeenCalled() // it has not come back: the call has not returned
    await act(async () => { release() })
    expect(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })).toBeEnabled()
    expect(within(banner()).getByText(r.checking)).toBeInTheDocument()
  })

  it('reads the settings again, after a moment and then a few times, until the daemon is back and runs what is saved', async () => {
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    const m = await mountOpen({ reloads: [pending(), down(), settled()] }) // back with the old values, gone again, back for good
    await confirmRestart()
    await tick(0)
    expect(m.onReload).not.toHaveBeenCalled() // the daemon takes a moment: not at once
    await tick(900)
    expect(m.onReload).not.toHaveBeenCalled()
    await tick(200)
    expect(m.onReload).toHaveBeenCalledTimes(1)
    expect(within(banner()).getByText(r.checking)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument() // one restart at a time
    await tick(2100)
    expect(m.onReload).toHaveBeenCalledTimes(2)
    await tick(3100)
    expect(m.onReload).toHaveBeenCalledTimes(3)
    expect(within(banner()).getByText(r.back)).toBeInTheDocument()
    expect(within(banner()).queryByText(r.checking)).not.toBeInTheDocument()
    expect(m.onApplied).toHaveBeenCalledTimes(1) // what else the section shows (the listeners, the models) may have changed
    await tick(30000)
    expect(m.onReload).toHaveBeenCalledTimes(3) // and it stops
  })

  it('asks for a restart again when something is saved after the daemon came back', async () => {
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    const m = await mountOpen({ reloads: [settled()] })
    await confirmRestart()
    await tick(1100)
    expect(within(banner()).getByText(r.back)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument()
    m.push(pending()) // a change adopted since: the daemon does not run it yet
    expect(within(banner()).queryByText(r.back)).not.toBeInTheDocument()
    expect(within(banner()).getByText(c.banner.restartTitle)).toBeInTheDocument()
    expect(restartBtn()).toBeEnabled()
  })

  it('goes on when a read fails or is overtaken by a newer one, and gives up after a few, saying so', async () => {
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    const m = await mountOpen() // every read fails, or a newer read takes its place
    await confirmRestart()
    await tick(0)
    await tick(30000)
    expect(m.onReload.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(m.onReload.mock.calls.length).toBeLessThanOrEqual(6)
    expect(within(banner()).getByText(r.late)).toBeInTheDocument()
    expect(m.onApplied).not.toHaveBeenCalled()
    const calls = m.onReload.mock.calls.length
    await tick(60000)
    expect(m.onReload).toHaveBeenCalledTimes(calls) // no more after it gave up
    expect(restartBtn()).toBeEnabled() // and it can be tried again
  })

  it('stops reading when the block goes away, and keeps working under React strict mode', async () => {
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    const m = await mountOpen({}, true)
    await confirmRestart()
    await tick(0)
    await tick(1100)
    expect(m.onReload).toHaveBeenCalledTimes(1) // strict mode ran the effects twice: the loop still runs
    m.view.unmount()
    await tick(30000)
    expect(m.onReload).toHaveBeenCalledTimes(1)
  })

  it('does not start a second restart for a click made before the first was drawn', async () => {
    App.DaemonRestart.mockReturnValue(new Promise(() => {}))
    await mountOpen()
    fireEvent.click(restartBtn())
    await screen.findByRole('alertdialog')
    const ok = within(dialog()).getByRole('button', { name: r.confirm })
    act(() => { fireEvent.click(ok); fireEvent.click(ok) }) // both in the same tick
    expect(App.DaemonRestart).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
})

describe('when a restart cannot be made', () => {
  it('says that nothing can restart a daemon that is not registered, in the page\'s words, and shows the commands', async () => {
    // the registration was removed after the page last read: the document still says it can be restarted
    App.DaemonRestart.mockRejectedValue(new Error('invalid_input: the daemon is not registered for auto-start, so nothing can restart it: stop it and start `monoagentcli daemon` again, or run `monoagentcli daemon install` to have the system manage it'))
    const m = await mountOpen() // and the read that follows fails: the CLI's refusal alone puts the commands there
    await confirmRestart()
    expect(await within(banner()).findByRole('alert')).toHaveTextContent(c.errors.notRegistered)
    expect(m.onReload).toHaveBeenCalledTimes(1)
    expect(within(banner()).getByText('monoagentcli daemon')).toBeInTheDocument()
    expect(within(banner()).getByText('monoagentcli daemon install')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: r.button })).not.toBeInTheDocument()
  })

  it('says what the CLI said when it fails otherwise, and offers to try again', async () => {
    App.DaemonRestart.mockRejectedValue(new Error('restart: launchctl kickstart -k gui/501/com.monoagent.daemon: exit status 113'))
    const m = await mountOpen()
    await confirmRestart()
    expect(await within(banner()).findByRole('alert')).toHaveTextContent('restart: launchctl kickstart -k gui/501/com.monoagent.daemon: exit status 113')
    expect(restartBtn()).toBeEnabled()
    await waitFor(() => expect(restartBtn()).toHaveFocus())
    expect(m.onReload).toHaveBeenCalledTimes(1) // a failed call may have been done in part: what the page knows is read again
    expect(within(banner()).queryByText('monoagentcli daemon')).not.toBeInTheDocument() // it is registered: the commands are for the other case
  })

  it('says when the service manager did not restart it, though the CLI did not fail', async () => {
    App.DaemonRestart.mockResolvedValue({ restarted: false, via: 'launchd' })
    const m = await mountOpen()
    await confirmRestart()
    expect(await within(banner()).findByRole('alert')).toHaveTextContent(r.notDone)
    await tick(30000)
    expect(m.onReload).not.toHaveBeenCalled() // there is no daemon coming back to wait for
  })
})

describe('with a setting being saved', () => {
  it('does not offer to start a restart while a call for a setting runs', async () => {
    App.APIConfigSet.mockReturnValue(new Promise(() => {}))
    await mountOpen()
    fireEvent.change(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label }), { target: { value: '9' } })
    fireEvent.click(screen.getByRole('button', { name: c.saveLabel.replace('{{setting}}', c.rows.max_concurrent.label) }))
    await tick(0)
    expect(App.APIConfigSet).toHaveBeenCalledTimes(1)
    expect(restartBtn()).toBeDisabled()
  })
})

describe('in Spanish', () => {
  it('words the banner, the dialog and what follows', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config
    App.DaemonRestart.mockResolvedValue({ restarted: true, via: 'launchd' })
    await mountOpen({ reloads: [settled()] })
    fireEvent.click(screen.getByRole('button', { name: s.restart.button }))
    const d = await screen.findByRole('alertdialog', { name: s.restart.title })
    expect(d).toHaveTextContent(s.restart.body)
    expect(within(d).getByRole('button', { name: s.restart.cancel })).toHaveFocus()
    fireEvent.click(within(d).getByRole('button', { name: s.restart.confirm }))
    await tick(0)
    expect(within(banner()).getByText(s.restart.checking)).toBeInTheDocument()
    await tick(1100)
    expect(within(banner()).getByText(s.restart.back)).toBeInTheDocument()
    expect(s.restart.body).not.toBe(r.body)
  })

  it('words the commands to run', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.config
    await mountOpen({ config: pending(false) })
    expect(within(banner()).getByText(s.restart.noAutostart)).toBeInTheDocument()
    expect(within(banner()).getByRole('button', { name: s.restart.copyLabel.replace('{{command}}', 'monoagentcli daemon') })).toBeInTheDocument()
  })
})
