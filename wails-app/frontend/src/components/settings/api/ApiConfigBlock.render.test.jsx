// @vitest-environment jsdom
// The "Server settings" block, read-only: what it shows for each state a setting can be in (editing, saving, the
// dialogs and the restart have their own files).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { configDoc } from './__fixtures__/configFixtures.js'

const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIConfigSet', 'APIConfigUnset', 'DaemonRestart']) App[k] = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

async function mount(props = {}) {
  const { default: ApiConfigBlock } = await import('./ApiConfigBlock.jsx')
  const p = { config: configDoc(), err: null, onRetry: vi.fn(), onAdopt: vi.fn(), onReload: vi.fn(), ...props }
  const view = render(<ApiConfigBlock {...p} />)
  return { ...p, rerender: (more) => view.rerender(<ApiConfigBlock {...p} {...more} />) }
}
const toggle = () => screen.getByTestId('api-config-toggle')
async function mountOpen(props = {}) {
  const m = await mount(props)
  fireEvent.click(toggle())
  return m
}
const row = (id) => screen.getByTestId(`api-config-row-${id}`)
const c = en.settings.api.config
const sc = es.settings.api.config

describe('ApiConfigBlock: folded', () => {
  it('is folded by default, a button named for the block and what it says of it', async () => {
    await mount({ config: configDoc({ saved: { max_concurrent: '8', turn_timeout: '15m' }, running: { max_concurrent: ['4', 'default'] }, autostart: true }) })
    expect(toggle().tagName).toBe('BUTTON')
    expect(toggle()).toHaveAttribute('aria-expanded', 'false')
    expect(toggle()).toHaveAccessibleName(/Server settings\s+Restart needed\s+2 saved/)
    expect(screen.queryByTestId('api-config-row-max_concurrent')).not.toBeInTheDocument()
    fireEvent.click(toggle())
    expect(toggle()).toHaveAttribute('aria-expanded', 'true')
    expect(document.getElementById(toggle().getAttribute('aria-controls'))).toBeInTheDocument()
    expect(row('max_concurrent')).toBeInTheDocument()
    fireEvent.click(toggle())
    expect(screen.queryByTestId('api-config-row-max_concurrent')).not.toBeInTheDocument()
  })

  it('says one saved setting in the singular, no chip for none, and no restart chip when none is needed', async () => {
    await mount({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }) })
    expect(screen.getByTestId('api-config-chip-saved')).toHaveTextContent('1 saved')
    expect(screen.queryByTestId('api-config-chip-restart')).not.toBeInTheDocument()
    cleanup()
    await mount({ config: configDoc() })
    expect(screen.queryByTestId('api-config-chip-saved')).not.toBeInTheDocument()
  })

  it('says problems in the folded header too: the problems are not for the unfolded to find', async () => {
    await mount({ config: configDoc({ saved: { max_concurrent: 'abc' }, problems: [{ key: 'max_concurrent', message: 'max_concurrent must be an integer from 1 to 64' }] }) })
    expect(screen.getByTestId('api-config-chip-problems')).toHaveTextContent('1 problem')
  })

  it('says loading, and an error that leaves the settings out, in the header', async () => {
    await mount({ config: null })
    expect(toggle()).toHaveAccessibleName(/Server settings\s+Loading…/)
    cleanup()
    await mount({ config: null, err: { text: 'boom', verbatim: true } })
    expect(screen.getByTestId('api-config-chip-error')).toHaveTextContent('Error reading settings')
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()
  })
})

describe('ApiConfigBlock: the rows', () => {
  it('has a row for each setting, the two TLS files as one, each with its label and what it is for', async () => {
    await mountOpen()
    const labels = ['v1_addr', 'tls', 'confinement', 'context_confinement', 'auto_confinement', 'max_concurrent', 'turn_timeout', 'image_runtimes', 'tool_runtimes']
    for (const id of labels) {
      expect(within(row(id)).getByText(c.rows[id].label), id).toBeInTheDocument()
      expect(within(row(id)).getByText(c.rows[id].hint), id).toBeInTheDocument()
    }
    expect(screen.getAllByTestId(/^api-config-row-/)).toHaveLength(9)
    expect(screen.getByText(c.hint)).toBeInTheDocument()
  })

  it('gives each setting the control that fits it, named by its label, holding what is saved', async () => {
    await mountOpen({ config: configDoc({ saved: { v1_addr: '0.0.0.0:9443', confinement: 'sandboxed', max_concurrent: '8', turn_timeout: '15m', image_runtimes: 'none', tls_cert_file: '/etc/api.pem', tls_key_file: '/etc/api.key' } }) })
    expect(screen.getByRole('textbox', { name: c.rows.v1_addr.label })).toHaveValue('0.0.0.0:9443')
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveValue('15m')
    expect(screen.getByRole('textbox', { name: c.rows.image_runtimes.label })).toHaveValue('none')
    expect(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(8)
    const select = screen.getByRole('combobox', { name: c.rows.confinement.label })
    expect(select).toHaveValue('sandboxed')
    expect(within(select).getAllByRole('option').map(o => o.value)).toEqual(['chat-only', 'sandboxed', 'any'])
    const tls = within(row('tls'))
    expect(tls.getByRole('textbox', { name: c.rows.tls.certLabel })).toHaveValue('/etc/api.pem')
    expect(tls.getByRole('textbox', { name: c.rows.tls.keyLabel })).toHaveValue('/etc/api.key')
    expect(screen.getByRole('group', { name: c.rows.tls.label })).toBeInTheDocument()
  })

  it('keeps a field as wide as what it holds: a count is not as wide as a path, whatever the window', async () => {
    await mountOpen()
    const widths = {
      count: screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label }),
      class: screen.getByRole('combobox', { name: c.rows.confinement.label }),
      text: screen.getByRole('textbox', { name: c.rows.turn_timeout.label }),
      path: within(row('tls')).getByRole('textbox', { name: c.rows.tls.certLabel }),
    }
    expect(Object.fromEntries(Object.entries(widths).map(([k, el]) => [k, el.style.maxWidth]))).toEqual({ count: '140px', class: '380px', text: '460px', path: '640px' })
  })

  it('says what the field holds while nothing is saved: the default, or what having none means', async () => {
    await mountOpen()
    expect(screen.getByRole('textbox', { name: c.rows.v1_addr.label })).toHaveAttribute('placeholder', c.rows.v1_addr.empty)
    expect(screen.getByRole('textbox', { name: c.rows.turn_timeout.label })).toHaveAttribute('placeholder', 'Default: 10m')
    expect(screen.getByRole('textbox', { name: c.rows.tool_runtimes.label })).toHaveAttribute('placeholder', 'Default: claude,codex')
    expect(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveAttribute('placeholder', 'Default: 4')
    expect(within(row('tls')).getByRole('textbox', { name: c.rows.tls.certLabel })).toHaveAttribute('placeholder', c.rows.tls.empty)
    // a class that is not saved is a choice of its own, which says what the default is
    const select = screen.getByRole('combobox', { name: c.rows.confinement.label })
    expect(select).toHaveValue('')
    expect(within(select).getByRole('option', { name: c.rows.confinement.empty })).toBeInTheDocument()
    expect(within(screen.getByRole('combobox', { name: c.rows.context_confinement.label })).getByRole('option', { name: 'Default: chat-only' })).toBeInTheDocument()
  })

  it('keeps a saved class that is not one the select knows, as stored: an edit by hand is not hidden', async () => {
    await mountOpen({ config: configDoc({ saved: { confinement: 'full' } }) })
    const select = screen.getByRole('combobox', { name: c.rows.confinement.label })
    expect(select).toHaveValue('full')
    expect(within(select).getByRole('option', { name: 'full' })).toBeInTheDocument()
  })

  it('describes each control by what it is for', async () => {
    await mountOpen()
    const input = screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })
    expect(input).toHaveAccessibleDescription(c.rows.max_concurrent.hint)
  })
})

describe('ApiConfigBlock: where each setting stands', () => {
  const doc = configDoc({
    saved: { v1_addr: '0.0.0.0:9443', max_concurrent: '8', tool_runtimes: 'claude', turn_timeout: '15m' },
    running: { max_concurrent: ['6', 'flag'], tool_runtimes: ['claude,codex', 'env'], v1_addr: ['', 'default'], turn_timeout: ['15m', 'saved'] },
    autostart: true,
  })

  it('says applied, restart needed and overridden, each in its row', async () => {
    await mountOpen({ config: doc })
    expect(screen.getByTestId('api-config-state-turn_timeout')).toHaveTextContent('Applied')
    expect(screen.getByTestId('api-config-state-v1_addr')).toHaveTextContent('Restart needed')
    expect(screen.getByTestId('api-config-state-max_concurrent')).toHaveTextContent('Overridden')
    expect(screen.getByTestId('api-config-state-tool_runtimes')).toHaveTextContent('Overridden')
    expect(screen.getByTestId('api-config-state-confinement')).toHaveTextContent('Applied')
    // what each means is its tooltip
    expect(screen.getByTestId('api-config-state-v1_addr')).toHaveAttribute('title', c.stateHint.pending)
  })

  it('says what the daemon runs and where that came from', async () => {
    await mountOpen({ config: doc })
    expect(screen.getByTestId('api-config-running-max_concurrent')).toHaveTextContent('Running: 6 (flag --max-concurrent)')
    expect(screen.getByTestId('api-config-running-tool_runtimes')).toHaveTextContent('Running: claude,codex (variable MONOAGENT_API_TOOL_RUNTIMES)')
    expect(screen.getByTestId('api-config-running-turn_timeout')).toHaveTextContent('Running: 15m (saved)')
    expect(screen.getByTestId('api-config-running-image_runtimes')).toHaveTextContent('Running: codex,antigravity (default)')
    // an empty running value is a value: it runs with none
    expect(screen.getByTestId('api-config-running-v1_addr')).toHaveTextContent('Running: not set (default)')
  })

  it('says why a saved value has no effect when the daemon was given its own flag or variable', async () => {
    await mountOpen({ config: doc })
    expect(within(row('max_concurrent')).getByText(c.overriddenFlag.replace('{{name}}', '--max-concurrent'))).toBeInTheDocument()
    expect(within(row('tool_runtimes')).getByText(c.overriddenEnv.replace('{{name}}', 'MONOAGENT_API_TOOL_RUNTIMES'))).toBeInTheDocument()
    expect(within(row('turn_timeout')).queryByText(/has no effect/)).not.toBeInTheDocument()
  })

  it('says only that no daemon is running, with nothing running to show, when none is', async () => {
    await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' } }) })
    for (const id of ['v1_addr', 'max_concurrent', 'tool_runtimes']) expect(screen.getByTestId(`api-config-state-${id}`)).toHaveTextContent('Daemon not running')
    expect(screen.queryByTestId('api-config-running-max_concurrent')).not.toBeInTheDocument()
    expect(screen.getByTestId('api-config-banner')).toHaveTextContent(c.banner.idleBody)
    expect(screen.getByTestId('api-config-banner')).not.toHaveTextContent(c.banner.restartTitle) // nothing is waiting for a restart
  })

  it('says unknown, and that the daemon predates the report, for a daemon that does not say what it runs', async () => {
    await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: 'old', autostart: true }) })
    expect(screen.getByTestId('api-config-state-max_concurrent')).toHaveTextContent('Unknown')
    expect(screen.queryByTestId('api-config-running-max_concurrent')).not.toBeInTheDocument()
    expect(screen.getByTestId('api-config-banner')).toHaveTextContent(c.banner.olderBody)
  })

  it('says a restart is needed, and for which settings, by their names on this page', async () => {
    await mountOpen({ config: doc })
    const banner = screen.getByTestId('api-config-banner')
    expect(banner).toHaveTextContent(c.banner.restartTitle)
    expect(banner).toHaveTextContent(c.banner.restartBody.replace('{{keys}}', c.rows.v1_addr.label))
  })

  it('says nothing about a restart when the daemon runs what is saved', async () => {
    await mountOpen({ config: configDoc({ saved: { max_concurrent: '8' }, running: {} }) })
    expect(screen.queryByTestId('api-config-banner')).not.toBeInTheDocument()
  })

  it('shows the state of the TLS pair as the one that needs most attention, and what each file runs', async () => {
    const tls = configDoc({
      saved: { tls_cert_file: '/etc/api.pem', tls_key_file: '/etc/api.key' },
      running: { tls_cert_file: ['', 'default'], tls_key_file: ['/etc/api.key', 'saved'] }, autostart: true,
    })
    await mountOpen({ config: tls })
    expect(screen.getByTestId('api-config-state-tls')).toHaveTextContent('Restart needed')
    expect(screen.getByTestId('api-config-running-tls_cert_file')).toHaveTextContent('Running: not set (default)')
    expect(screen.getByTestId('api-config-running-tls_key_file')).toHaveTextContent('Running: /etc/api.key (saved)')
  })
})

describe('ApiConfigBlock: problems of the saved settings', () => {
  const problems = [
    { key: 'max_concurrent', message: 'max_concurrent must be an integer from 1 to 64' },
    { key: 'confinement', message: 'a rule this page has no words for' },
    { key: '', message: 'the saved settings are damaged' },
  ]
  const doc = configDoc({ saved: { max_concurrent: 'abc', confinement: 'full' }, problems })

  it('shows each next to its setting, in the page\'s words where it has them and as the CLI said it where it has not, with the way out', async () => {
    await mountOpen({ config: doc })
    const mc = within(row('max_concurrent'))
    expect(mc.getByText(c.errors.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))).toBeInTheDocument()
    expect(mc.getByText(c.problemHint)).toBeInTheDocument()
    expect(within(row('confinement')).getByText('a rule this page has no words for')).toBeInTheDocument()
    expect(within(row('confinement')).getByText('a rule this page has no words for')).not.toHaveAttribute('lang') // the page is English
    expect(within(row('turn_timeout')).queryByText(c.problemHint)).not.toBeInTheDocument()
  })

  it('shows a problem of the document itself above the rows, with the command that starts over', async () => {
    await mountOpen({ config: doc })
    const box = screen.getByTestId('api-config-problems')
    expect(within(box).getByText('the saved settings are damaged')).toBeInTheDocument()
    expect(within(box).getByText(c.problemDocHint)).toBeInTheDocument()
    expect(within(box).queryByText(/must be an integer/)).not.toBeInTheDocument() // the others are in their rows
  })

  it('has no box for problems when there are none of the document\'s own', async () => {
    await mountOpen({ config: configDoc({ saved: { max_concurrent: 'abc' }, problems: [problems[0]] }) })
    expect(screen.queryByTestId('api-config-problems')).not.toBeInTheDocument()
  })

  it('marks the CLI\'s own words as English on a page that is not', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mountOpen({ config: doc })
    expect(within(row('confinement')).getByText('a rule this page has no words for')).toHaveAttribute('lang', 'en')
    expect(within(row('max_concurrent')).getByText(sc.errors.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))).not.toHaveAttribute('lang')
  })
})

describe('ApiConfigBlock: reading', () => {
  it('says loading while the first read runs, and an error with a retry when it failed and there is nothing to show', async () => {
    const { onRetry } = await mountOpen({ config: null })
    expect(screen.getByText(c.loading)).toBeInTheDocument()
    cleanup()
    const m = await mountOpen({ config: null, err: { text: 'unknown command "config" for "monoagentcli api"', verbatim: true } })
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent('Couldn\'t read the server settings: unknown command "config" for "monoagentcli api"')
    expect(screen.queryByTestId('api-config-row-max_concurrent')).not.toBeInTheDocument()
    expect(screen.queryByText(c.loading)).not.toBeInTheDocument() // it failed: it is not loading
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(m.onRetry).toHaveBeenCalledTimes(1)
    expect(onRetry).not.toHaveBeenCalled()
  })

  it('keeps what was on screen when a later read failed, and says so with a retry', async () => {
    const config = configDoc({ saved: { max_concurrent: '8' }, running: {} })
    const m = await mountOpen({ config, err: { text: 'database is locked', verbatim: true } })
    expect(screen.getByRole('spinbutton', { name: c.rows.max_concurrent.label })).toHaveValue(8)
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent('Couldn\'t read the server settings: database is locked')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(m.onRetry).toHaveBeenCalledTimes(1)
  })

  it('has no row for a setting the CLI lists that this page does not know, and no failure for it', async () => {
    const doc = configDoc()
    doc.settings.push({ key: 'a_setting_of_the_future', server_flag: '', env: 'X', default: '', effective: '', source: 'default', state: 'not_running' })
    await mountOpen({ config: doc })
    expect(screen.getAllByTestId(/^api-config-row-/)).toHaveLength(9)
  })

  it('has a row for a setting this page knows, and an older CLI does not list, with no state: nothing is claimed', async () => {
    const doc = configDoc({ saved: { turn_timeout: '15m' } })
    doc.settings = doc.settings.filter(s => s.key !== 'turn_timeout')
    await mountOpen({ config: doc })
    expect(row('turn_timeout')).toBeInTheDocument()
    expect(screen.getByTestId('api-config-state-turn_timeout')).toHaveTextContent('Unknown')
  })
})

describe('ApiConfigBlock: the language', () => {
  it('speaks the chosen language: the labels, the states, what is running and the banner', async () => {
    await act(() => i18n.changeLanguage('es'))
    const doc = configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['6', 'flag'], v1_addr: ['0.0.0.0:9443', 'saved'] }, autostart: true })
    await mountOpen({ config: doc })
    expect(toggle()).toHaveAccessibleName(new RegExp(sc.title))
    expect(within(row('max_concurrent')).getByText(sc.rows.max_concurrent.label)).toBeInTheDocument()
    expect(screen.getByTestId('api-config-state-max_concurrent')).toHaveTextContent(sc.state.overridden)
    expect(screen.getByTestId('api-config-state-v1_addr')).toHaveTextContent(sc.state.pending)
    expect(screen.getByTestId('api-config-running-max_concurrent')).toHaveTextContent(sc.runningLine.replace('{{value}}', '6').replace('{{source}}', sc.source.flag.replace('{{name}}', '--max-concurrent')))
    expect(screen.getByTestId('api-config-banner')).toHaveTextContent(sc.banner.restartTitle)
    expect(within(row('max_concurrent')).getByText(sc.overriddenFlag.replace('{{name}}', '--max-concurrent'))).toBeInTheDocument()
    expect(sc.state.pending).not.toBe(c.state.pending)
  })

  it('says a failure to read in the language of the page, and the CLI\'s words as English', async () => {
    await act(() => i18n.changeLanguage('es'))
    await mountOpen({ config: null, err: { text: 'database is locked', verbatim: true } })
    expect(screen.getByTestId('api-config-load-error')).toHaveTextContent(`${sc.loadError} database is locked`)
    expect(within(screen.getByTestId('api-config-load-error')).getByText('database is locked')).toHaveAttribute('lang', 'en')
    expect(screen.getByRole('button', { name: es.settings.api.retry })).toBeInTheDocument()
  })
})
