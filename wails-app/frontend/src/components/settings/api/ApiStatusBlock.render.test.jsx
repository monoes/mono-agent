// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import es from '../../../locales/es.json'
import ApiStatusBlock from './ApiStatusBlock.jsx'
import { mainListener, dedicatedListener, statusOf } from './__fixtures__/apiFixtures.js'

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

describe('ApiStatusBlock: a listener that serves /v1', () => {
  it('shows the base URL, that it runs, and where and how far it serves', () => {
    mount(statusOf([mainListener()]))
    expect(text('api-base-url')).toBe('http://127.0.0.1:9322/v1')
    expect(text('api-state')).toBe('Running')
    expect(text('api-exposure')).toBe('This computer only')
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
    mount(statusOf([mainListener(), dedicatedListener()], { daemon: { running: true } }))
    // The main listener is loopback and serves /v1, so it is the one described.
    expect(text('api-base-url')).toBe('http://127.0.0.1:9322/v1')
    cleanup()
    mount(statusOf([mainListener({ v1: false }), dedicatedListener()], { daemon: { running: true } }))
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
})

describe('ApiStatusBlock: the other states of a listener (listenerNote of the CLI)', () => {
  it('not running: nothing answers, and what to start', () => {
    mount(statusOf([mainListener({ reachable: false, v1_answers: false })]))
    expect(text('api-state')).toBe('Not running')
    expect(screen.getByText('Nothing answers at 127.0.0.1:9322. Start the daemon (monoagentcli daemon) or monoagentcli httpapi.')).toBeInTheDocument()
    expect(text('api-base-url')).toBe('http://127.0.0.1:9322/v1') // where it will listen
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

  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    mount(statusOf([mainListener()]))
    const s = es.settings.api.status
    expect(text('api-state')).toBe(s.stateServing)
    expect(text('api-exposure')).toBe(s.exposureLoopback)
    expect(text('api-confinement')).toBe(s.confinement.replace('{{class}}', 'any'))
    expect(screen.getByLabelText(s.copyUrl)).toBeInTheDocument()
    expect(screen.getByText(s.baseUrl)).toBeInTheDocument()
  })
})
