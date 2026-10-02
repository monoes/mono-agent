// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, act, within } from '@testing-library/react'
import i18n from '../../../i18n.js'
import es from '../../../locales/es.json'
import ApiStatusBlock from './ApiStatusBlock.jsx'
import { mainListener, dedicatedListener, withoutScheme, statusOf } from './__fixtures__/apiFixtures.js'

beforeEach(async () => {
  await i18n.changeLanguage('en')
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.runtime })

function mount(status, props = {}) {
  const onRefresh = vi.fn(); const onRetry = vi.fn()
  render(<ApiStatusBlock status={status} err="" refreshing={false} onRefresh={onRefresh} onRetry={onRetry} {...props} />)
  return { onRefresh, onRetry }
}
const text = (id) => screen.getByTestId(id).textContent
const row = (name) => within(screen.getByTestId(`api-listener-${name}`))
const rows = () => screen.queryAllByTestId(/^api-listener-/)
const down = { reachable: false, v1_answers: false }

describe('ApiStatusBlock: a listener that serves /v1', () => {
  it('shows the base URL, that it runs, and where and how far it serves', () => {
    mount(statusOf([mainListener()]))
    expect(text('api-base-url')).toBe('http://127.0.0.1:9322/v1')
    expect(text('api-state')).toBe('Running')
    expect(text('api-exposure')).toBe('Bound to loopback')
    expect(text('api-confinement')).toBe('Confinement: any')
    expect(screen.getByText('Keys with context: up to chat-only · auto picks up to chat-only')).toBeInTheDocument()
  })

  it('says the policy is assumed unless the running daemon reported it', () => {
    mount(statusOf([mainListener()]))
    expect(screen.getByText(/Assumed from this app's environment/)).toBeInTheDocument()
    cleanup()
    mount(statusOf([mainListener({ confinement_source: 'daemon' })], { daemon: { running: true } }))
    expect(screen.queryByText(/Assumed from this app's environment/)).not.toBeInTheDocument()
  })

  it('shows a dedicated network listener over https, with the TLS and wildcard notes', () => {
    mount(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
    expect(rows()).toHaveLength(1) // the main listener does not serve /v1: nothing to show of it
    expect(text('api-base-url')).toBe('https://localhost:9443/v1')
    expect(text('api-exposure')).toBe('Network')
    expect(text('api-confinement')).toBe('Confinement: chat-only')
    expect(screen.getByText(/TLS only\. Clients must trust the server's certificate/)).toBeInTheDocument()
    expect(screen.getByText(/listens on every interface/)).toBeInTheDocument()
  })

  it('has no wildcard note for a real host', () => {
    mount(statusOf([dedicatedListener({ addr: 'api.example.com:9443' })]))
    expect(text('api-base-url')).toBe('https://api.example.com:9443/v1')
    expect(screen.queryByText(/listens on every interface/)).not.toBeInTheDocument()
    expect(screen.getByText(/TLS only/)).toBeInTheDocument()
  })

  it('leaves out the auto cap when the CLI reports none', () => {
    const l = mainListener(); delete l.auto_confinement
    mount(statusOf([l]))
    expect(screen.getByText('Keys with context: up to chat-only')).toBeInTheDocument()
    expect(screen.queryByText(/auto picks up to/)).not.toBeInTheDocument()
  })

  it('copies the base URL and says so', async () => {
    mount(statusOf([mainListener()]))
    fireEvent.click(screen.getByRole('button', { name: 'Copy the base URL' }))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
    expect(window.runtime.ClipboardSetText).toHaveBeenCalledWith('http://127.0.0.1:9322/v1')
    window.runtime.ClipboardSetText.mockResolvedValue(false)
    fireEvent.click(screen.getByRole('button', { name: 'Copy the base URL' }))
    expect(await screen.findByText("Couldn't copy")).toBeInTheDocument()
  })

  it('takes the scheme from what the CLI saw answer: a dedicated loopback listener with a certificate is https', () => {
    mount(statusOf([dedicatedListener({ loopback: true, addr: '127.0.0.1:9443', scheme: 'https' })]))
    expect(text('api-base-url')).toBe('https://127.0.0.1:9443/v1')
    expect(screen.getByText(/TLS only/)).toBeInTheDocument() // and so is its note
    cleanup()
    // From a CLI that does not say, it is derived: plain http on loopback.
    mount(statusOf([withoutScheme(dedicatedListener({ loopback: true, addr: '127.0.0.1:9443' }))]))
    expect(text('api-base-url')).toBe('http://127.0.0.1:9443/v1')
    expect(screen.queryByText(/TLS only/)).not.toBeInTheDocument()
  })
})

describe('ApiStatusBlock: every listener that serves /v1 is shown', () => {
  const both = () => statusOf([mainListener(), dedicatedListener()], { daemon: { running: true } })

  it('one row each, with its own base URL, state, exposure and confinement', () => {
    mount(both())
    expect(rows()).toHaveLength(2)
    const main = row('main'), v1 = row('v1')
    expect(main.getByTestId('api-base-url')).toHaveTextContent('http://127.0.0.1:9322/v1')
    expect(main.getByTestId('api-state')).toHaveTextContent('Running')
    expect(main.getByTestId('api-exposure')).toHaveTextContent('Bound to loopback')
    expect(main.getByTestId('api-confinement')).toHaveTextContent('Confinement: any')
    expect(v1.getByTestId('api-base-url')).toHaveTextContent('https://localhost:9443/v1')
    expect(v1.getByTestId('api-state')).toHaveTextContent('Running')
    expect(v1.getByTestId('api-exposure')).toHaveTextContent('Network')
    expect(v1.getByTestId('api-confinement')).toHaveTextContent('Confinement: chat-only')
    expect(v1.getByText('Keys with context: up to chat-only · auto picks up to chat-only')).toBeInTheDocument()
  })

  it('does not leave a network-exposed /v1 out because a loopback one answers', () => {
    mount(both())
    expect(screen.getAllByTestId('api-exposure').map(e => e.textContent)).toEqual(['Bound to loopback', 'Network'])
    // The notes of the exposed one are there too: TLS, and every interface.
    expect(row('v1').getByText(/TLS only/)).toBeInTheDocument()
    expect(row('v1').getByText(/listens on every interface/)).toBeInTheDocument()
  })

  it('keeps the notes of a row in that row', () => {
    mount(both())
    expect(row('main').queryByText(/TLS only/)).not.toBeInTheDocument()
    expect(row('main').queryByText(/listens on every interface/)).not.toBeInTheDocument()
    expect(row('main').getByText(/Assumed from this app's environment/)).toBeInTheDocument() // its policy is not the daemon's
    expect(row('v1').queryByText(/Assumed from this app's environment/)).not.toBeInTheDocument()
  })

  it('names the rows when there are several, and not when there is one', () => {
    mount(both())
    expect(row('main').getByText('Main HTTP API listener')).toBeInTheDocument()
    expect(row('v1').getByText('Dedicated /v1 listener (--v1-addr)')).toBeInTheDocument()
    cleanup()
    mount(statusOf([mainListener()]))
    expect(screen.queryByText('Main HTTP API listener')).not.toBeInTheDocument()
  })

  it('shows a listener that does not answer beside one that does', () => {
    mount(statusOf([mainListener(down), dedicatedListener()]))
    expect(row('main').getByTestId('api-state')).toHaveTextContent('Not running')
    expect(row('main').getByText(/Nothing answers at 127\.0\.0\.1:9322/)).toBeInTheDocument()
    expect(row('main').getByTestId('api-base-url')).toHaveTextContent('http://127.0.0.1:9322/v1') // where it will listen
    expect(row('v1').getByTestId('api-state')).toHaveTextContent('Running')
  })

  it('has one Refresh, however many listeners', () => {
    const { onRefresh } = mount(both())
    const refresh = screen.getAllByRole('button', { name: 'Refresh' })
    expect(refresh).toHaveLength(1)
    fireEvent.click(refresh[0])
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('gives each Copy button a name of its own, copies the URL of its row and says so in that row only', async () => {
    mount(both())
    expect(screen.queryByRole('button', { name: 'Copy the base URL' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Copy the base URL (Dedicated /v1 listener (--v1-addr))' }))
    expect(await row('v1').findByText('Copied')).toBeInTheDocument()
    expect(window.runtime.ClipboardSetText).toHaveBeenLastCalledWith('https://localhost:9443/v1')
    expect(row('main').queryByText('Copied')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Copy the base URL (Main HTTP API listener)' }))
    expect(await row('main').findByText('Copied')).toBeInTheDocument()
    expect(window.runtime.ClipboardSetText).toHaveBeenLastCalledWith('http://127.0.0.1:9322/v1')
    expect(row('v1').queryByText('Copied')).not.toBeInTheDocument()
  })

  it('says a copy failed in the row that tried', async () => {
    window.runtime.ClipboardSetText.mockResolvedValue(false)
    mount(both())
    fireEvent.click(screen.getByRole('button', { name: 'Copy the base URL (Dedicated /v1 listener (--v1-addr))' }))
    expect(await row('v1').findByText("Couldn't copy")).toBeInTheDocument()
    expect(row('main').queryByText("Couldn't copy")).not.toBeInTheDocument()
  })
})

describe('ApiStatusBlock: loopback is not "this computer only"', () => {
  it('says it is bound to loopback, and that a proxy, a tunnel or a port forward can still expose it', () => {
    mount(statusOf([mainListener()]))
    expect(text('api-exposure')).toBe('Bound to loopback')
    expect(screen.getByTestId('api-exposure')).toHaveAttribute('title', expect.stringMatching(/reverse proxy, tunnel or port forward on this computer can still expose it/))
    expect(screen.queryByText(/This computer only/i)).not.toBeInTheDocument()
  })
  it('says a network listener is reachable from other computers', () => {
    mount(statusOf([dedicatedListener()]))
    expect(screen.getByTestId('api-exposure')).toHaveAttribute('title', 'Other computers can reach it, over TLS.')
  })
})

describe('ApiStatusBlock: an address that is not a host and a port', () => {
  const bad = '127.0.0.1@evil.example:80'

  it('is described without a URL or a Copy button, and says why', () => {
    mount(statusOf([mainListener({ addr: bad })]))
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Copy the base URL/ })).not.toBeInTheDocument()
    expect(screen.queryByText('Base URL')).not.toBeInTheDocument()
    expect(text('api-state')).toBe('Running') // the listener is still there, still described
    expect(text('api-exposure')).toBe('Bound to loopback')
    expect(text('api-confinement')).toBe('Confinement: any')
    expect(screen.getByText(`The address ${bad} is not a host name or IP address with a port, so no base URL is shown for it.`)).toBeInTheDocument()
  })

  it('does not stop another listener from showing its URL', () => {
    mount(statusOf([mainListener({ addr: bad }), dedicatedListener()]))
    expect(row('main').queryByTestId('api-base-url')).not.toBeInTheDocument()
    expect(row('v1').getByTestId('api-base-url')).toHaveTextContent('https://localhost:9443/v1')
    expect(screen.getAllByRole('button', { name: /Copy the base URL/ })).toHaveLength(1)
  })

  it('says nothing of an address when the listener does not serve /v1', () => {
    mount(statusOf([mainListener({ addr: bad, v1: false })]))
    expect(screen.queryByText(/is not a host name or IP address/)).not.toBeInTheDocument()
  })
})

describe('ApiStatusBlock: the other states of a listener (listenerNote of the CLI)', () => {
  it('not running: nothing answers, and what to start', () => {
    mount(statusOf([mainListener(down)]))
    expect(text('api-state')).toBe('Not running')
    expect(screen.getByText('Nothing answers at 127.0.0.1:9322. Start the daemon (monoagentcli daemon) or monoagentcli httpapi.')).toBeInTheDocument()
    expect(text('api-base-url')).toBe('http://127.0.0.1:9322/v1') // where it will listen
  })

  it('not answering although the daemon runs: says so, and does not say to start the daemon', () => {
    mount(statusOf([mainListener(down)], { daemon: { running: true } }))
    expect(text('api-state')).toBe('Not answering')
    expect(screen.getByText(/Nothing answers at 127\.0\.0\.1:9322, although the daemon is running\./)).toBeInTheDocument()
    expect(screen.queryByText(/Start the daemon/)).not.toBeInTheDocument()
  })

  it('reachable but not answering /v1: restart it', () => {
    mount(statusOf([mainListener({ v1_answers: false })]))
    expect(text('api-state')).toBe('Not answering /v1')
    expect(screen.getByText(/answers \/health but not \/v1: a server that predates the API may still be running\. Restart it\./)).toBeInTheDocument()
  })

  it('the daemon does not serve /v1 on a loopback main listener: no URL', () => {
    mount(statusOf([mainListener({ v1: false, v1_answers: false })], { daemon: { running: true } }))
    expect(text('api-state')).toBe('Not serving /v1')
    expect(screen.getByText(/The daemon does not serve \/v1 on 127\.0\.0\.1:9322/)).toBeInTheDocument()
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
    expect(screen.queryByText('Base URL')).not.toBeInTheDocument() // no label without a value
    expect(screen.queryByTestId('api-confinement')).not.toBeInTheDocument()
    expect(screen.queryByTestId('api-exposure')).not.toBeInTheDocument()
  })

  it('a main listener off loopback does not serve /v1: use --v1-addr', () => {
    mount(statusOf([mainListener({ addr: '0.0.0.0:9322', loopback: false, v1: false, v1_answers: false, confinement: 'chat-only' })]))
    expect(text('api-state')).toBe('Not serving /v1')
    expect(screen.getByText(/0\.0\.0\.0:9322 is not loopback, so \/v1 is not served there\. Give it its own listener with --v1-addr/)).toBeInTheDocument()
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
  })

  it('no listener at all, with and without a daemon', () => {
    mount(statusOf([]))
    expect(text('api-state')).toBe('Not running')
    expect(screen.getByText('No listener serves /v1.')).toBeInTheDocument()
    cleanup()
    mount(statusOf([], { daemon: { running: true } }))
    expect(screen.getByText(/The daemon is running but serves no \/v1/)).toBeInTheDocument()
    expect(screen.queryByTestId('api-base-url')).not.toBeInTheDocument()
  })
})

describe('ApiStatusBlock: loading, errors and refresh', () => {
  it('shows a loading line, and the CLI error with a retry', () => {
    mount(null)
    expect(screen.getByText('Loading…')).toBeInTheDocument()
    cleanup()
    const { onRetry } = mount(null, { err: 'unknown command "api" for "monoagentcli"' })
    expect(screen.getByText(/Couldn't read the API status: unknown command "api" for "monoagentcli"/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('refreshes on request, and not while it already does', () => {
    const { onRefresh } = mount(statusOf([mainListener()]))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
    cleanup()
    mount(statusOf([mainListener()]), { refreshing: true })
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
  })

  it('can be refreshed whatever the state: with no listener, or one that serves nothing', () => {
    for (const status of [statusOf([]), statusOf([mainListener({ v1: false })]), statusOf([mainListener({ addr: 'nonsense' })])]) {
      const { onRefresh } = mount(status)
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
      expect(onRefresh).toHaveBeenCalledTimes(1)
      cleanup()
    }
  })

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    mount(statusOf([mainListener(), dedicatedListener()], { daemon: { running: true } }))
    const s = es.settings.api.status
    expect(row('main').getByTestId('api-state')).toHaveTextContent(s.stateServing)
    expect(row('main').getByTestId('api-exposure')).toHaveTextContent(s.exposureLoopback)
    expect(row('main').getByTestId('api-exposure')).toHaveAttribute('title', s.exposureLoopbackHint)
    expect(row('main').getByTestId('api-confinement')).toHaveTextContent(s.confinement.replace('{{class}}', 'any'))
    expect(row('main').getByText(s.listenerMain)).toBeInTheDocument()
    expect(row('v1').getByText(s.listenerDedicated)).toBeInTheDocument()
    expect(row('v1').getByRole('button', { name: s.copyUrlOf.replace('{{name}}', s.listenerDedicated) })).toBeInTheDocument()
    expect(row('main').getByText(s.baseUrl)).toBeInTheDocument()
  })

  it('speaks the chosen language for a listener that is down while the daemon runs, and for a bad address', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.status
    mount(statusOf([mainListener(down)], { daemon: { running: true } }))
    expect(text('api-state')).toBe(s.stateDownDaemon)
    expect(screen.getByText(s.noteDownDaemon.replace('{{addr}}', '127.0.0.1:9322'))).toBeInTheDocument()
    cleanup()
    mount(statusOf([mainListener({ addr: 'a@b:80' })]))
    expect(screen.getByText(s.noteBadAddr.replace('{{addr}}', 'a@b:80'))).toBeInTheDocument()
  })
})
