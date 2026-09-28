// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const go = vi.hoisted(() => ({
  LibraryStatus: vi.fn(),
  LibraryLogin: vi.fn(),
  LibraryLoginCancel: vi.fn(),
  LibraryLogout: vi.fn(),
  LibraryLoginEmailSend: vi.fn(),
  LibraryLoginEmailVerify: vi.fn(),
  LibraryPublish: vi.fn(),
  OpenURL: vi.fn(() => Promise.resolve()),
}))
vi.mock('../../wailsjs/go/main/App', () => go)

import i18n from '../../i18n.js'
import LogInToMonoesButton from './LogInToMonoesButton.jsx'
import PublishToMonoesDialog from './PublishToMonoesDialog.jsx'

const j = (v) => Promise.resolve(JSON.stringify(v))
const LOGGED_OUT = { logged_in: false, user: null }
const LOGGED_IN = { logged_in: true, user: { username: 'ana' } }

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('LogInToMonoesButton', () => {
  it('logs in through the browser flow and shows the account', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
    let finish
    go.LibraryLogin.mockImplementation(() => new Promise(r => { finish = r }))
    const onStatusChange = vi.fn()
    render(<LogInToMonoesButton onStatusChange={onStatusChange} />)
    fireEvent.click(await screen.findByText('Log in to monoes'))
    expect(await screen.findByText('Waiting for your browser…')).toBeInTheDocument()
    finish(JSON.stringify(LOGGED_IN))
    expect(await screen.findByText('Logged in as @ana')).toBeInTheDocument()
    expect(onStatusChange).toHaveBeenCalledWith(expect.objectContaining({ logged_in: true }))
  })

  it('Cancel stops a waiting browser login', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
    go.LibraryLogin.mockImplementation(() => new Promise(() => {}))
    go.LibraryLoginCancel.mockImplementation(() => j({ ok: true, cancelled: true }))
    render(<LogInToMonoesButton />)
    fireEvent.click(await screen.findByText('Log in to monoes'))
    fireEvent.click(await screen.findByText('Cancel'))
    await waitFor(() => expect(go.LibraryLoginCancel).toHaveBeenCalled())
    expect(await screen.findByText('Log in to monoes')).toBeInTheDocument()
  })

  it('logs in with an emailed code', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
    go.LibraryLoginEmailSend.mockImplementation(() => j({ code_sent: true, email: 'ana@x.io' }))
    go.LibraryLoginEmailVerify.mockImplementation(() => j(LOGGED_IN))
    render(<LogInToMonoesButton />)
    fireEvent.click(await screen.findByText('Use an email code instead'))
    fireEvent.change(screen.getByLabelText('you@example.com'), { target: { value: 'ana@x.io' } })
    fireEvent.click(screen.getByText('Send code'))
    expect(await screen.findByText('We emailed a code to ana@x.io.')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('6-digit code'), { target: { value: '123456' } })
    fireEvent.click(screen.getByText('Log in'))
    expect(await screen.findByText('Logged in as @ana')).toBeInTheDocument()
    expect(go.LibraryLoginEmailSend).toHaveBeenCalledWith('ana@x.io')
    expect(go.LibraryLoginEmailVerify).toHaveBeenCalledWith('ana@x.io', '123456')
  })

  it('shows a wrong code and keeps the code field', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
    go.LibraryLoginEmailSend.mockImplementation(() => j({ code_sent: true }))
    go.LibraryLoginEmailVerify.mockImplementation(() => j({ error: 'invalid or expired code', code: 'auth_or_connection' }))
    render(<LogInToMonoesButton />)
    fireEvent.click(await screen.findByText('Use an email code instead'))
    fireEvent.change(screen.getByLabelText('you@example.com'), { target: { value: 'a@b.c' } })
    fireEvent.click(screen.getByText('Send code'))
    fireEvent.change(await screen.findByLabelText('6-digit code'), { target: { value: '000000' } })
    fireEvent.click(screen.getByText('Log in'))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid or expired code')
    expect(screen.getByLabelText('6-digit code')).toBeInTheDocument()
  })

  it('logs out', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_IN))
    go.LibraryLogout.mockImplementation(() => j({ logged_out: true }))
    render(<LogInToMonoesButton />)
    fireEvent.click(await screen.findByText('Log out'))
    expect(await screen.findByText('Log in to monoes')).toBeInTheDocument()
  })
})

describe('PublishToMonoesDialog', () => {
  it('asks a logged-out person to log in first', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
    render(<PublishToMonoesDialog kind="org" localId="growth" defaultName="growth" onClose={() => {}} />)
    expect(await screen.findByText('Log in to monoes.me to publish.')).toBeInTheDocument()
    expect(screen.queryByText('Publish')).not.toBeInTheDocument()
  })

  it('publishes privately by default and shows the link', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_IN))
    go.LibraryPublish.mockImplementation(() => j({ created: true, item: { id: 'x', name: 'Daily digest', version: '1.0.0', url: 'https://monoes.me/library/workflows/daily-digest' } }))
    render(<PublishToMonoesDialog kind="workflow" localId="wf-1" defaultName="Daily digest" onClose={() => {}} />)
    await screen.findByLabelText('Name')
    fireEvent.change(screen.getByLabelText('Tags'), { target: { value: 'news, daily' } })
    fireEvent.click(screen.getByText('Publish'))
    expect(await screen.findByText('Published Daily digest 1.0.0.')).toBeInTheDocument()
    expect(go.LibraryPublish).toHaveBeenCalledWith('workflow', 'wf-1', false, 'Daily digest', '', 'news, daily', '')
    fireEvent.click(screen.getByText('Open on monoes.me'))
    expect(go.OpenURL).toHaveBeenCalledWith('https://monoes.me/library/workflows/daily-digest')
  })

  it('publishes publicly when ticked and shows server errors', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_IN))
    go.LibraryPublish.mockImplementation(() => j({ error: 'artifact too large (413)', code: '' }))
    render(<PublishToMonoesDialog kind="automation" localId="hackernews" defaultName="Hacker News" onClose={() => {}} />)
    fireEvent.click(await screen.findByLabelText(/Public/))
    fireEvent.click(screen.getByText('Publish'))
    expect(await screen.findByRole('alert')).toHaveTextContent('artifact too large (413)')
    await waitFor(() => expect(go.LibraryPublish).toHaveBeenCalledWith('automation', 'hackernews', true, 'Hacker News', '', '', ''))
  })
})
