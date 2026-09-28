// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'

const go = vi.hoisted(() => ({
  LibraryStatus: vi.fn(),
  LibraryList: vi.fn(),
  LibraryInstall: vi.fn(),
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
import LibraryModal from './LibraryModal.jsx'

const j = (v) => Promise.resolve(JSON.stringify(v))
const LOGGED_OUT = { base_url: 'https://monoes.me', logged_in: false, user: null, scopes: [] }
const LOGGED_IN = { base_url: 'https://monoes.me', logged_in: true, user: { username: 'ana', email: 'ana@x.io' }, scopes: [] }

const item = (over = {}) => ({
  id: 'i1', kind: 'automation', slug: 'hackernews', name: 'Hacker News', description: 'Read HN', version: '1.2.0',
  visibility: 'official', tags: ['news'], owner: { id: 'o', username: 'monoes', name: 'Monoes' }, installed: null, ...over,
})

beforeEach(async () => {
  await i18n.changeLanguage('en')
  go.LibraryStatus.mockImplementation(() => j(LOGGED_OUT))
  go.LibraryList.mockImplementation(() => j({ items: [item()], page: 1, per_page: 20, total: 1 }))
})
afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('LibraryModal', () => {
  it('lists official items for its kind with the official badge', async () => {
    render(<LibraryModal kind="automation" onClose={() => {}} />)
    expect(await screen.findByText('Hacker News')).toBeInTheDocument()
    expect(screen.getByText('Web automations from monoes.me')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Official' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getAllByText('Official')).toHaveLength(2) // the tab and the badge
    expect(screen.getByText('by Monoes')).toBeInTheDocument()
    expect(go.LibraryList).toHaveBeenCalledWith('automation', 'official', '', 1)
  })

  it('Community hides official items; search is passed to the CLI', async () => {
    go.LibraryList.mockImplementation((kind, scope, search) => j({
      items: [item(), item({ id: 'i2', name: 'Weather', visibility: 'public', owner: { username: 'bo' } })].filter(i => !search || i.name.includes(search)),
      total: 2,
    }))
    render(<LibraryModal kind="automation" onClose={() => {}} />)
    await screen.findByText('Hacker News')
    fireEvent.click(screen.getByRole('tab', { name: 'Community' }))
    expect(await screen.findByText('Weather')).toBeInTheDocument()
    expect(screen.queryByText('Hacker News')).not.toBeInTheDocument()
    expect(go.LibraryList).toHaveBeenLastCalledWith('automation', 'public', '', 1)
    fireEvent.change(screen.getByLabelText('Search…'), { target: { value: 'Wea' } })
    await waitFor(() => expect(go.LibraryList).toHaveBeenLastCalledWith('automation', 'public', 'Wea', 1))
  })

  it('Mine asks a logged-out person to log in instead of listing', async () => {
    render(<LibraryModal kind="workflow" onClose={() => {}} />)
    await screen.findByText('Hacker News')
    go.LibraryList.mockClear()
    fireEvent.click(screen.getByRole('tab', { name: 'Mine' }))
    expect(await screen.findByText('Log in to see what you have published.')).toBeInTheDocument()
    expect(go.LibraryList).not.toHaveBeenCalled()
  })

  it('Mine lists your items once logged in and shows the account', async () => {
    go.LibraryStatus.mockImplementation(() => j(LOGGED_IN))
    go.LibraryList.mockImplementation((k, scope) => j({ items: scope === 'mine' ? [item({ id: 'm1', name: 'My flow', visibility: 'private' })] : [], total: 1 }))
    render(<LibraryModal kind="workflow" onClose={() => {}} />)
    expect(await screen.findByText('Logged in as @ana')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: 'Mine' }))
    expect(await screen.findByText('My flow')).toBeInTheDocument()
    expect(go.LibraryList).toHaveBeenLastCalledWith('workflow', 'mine', '', 1)
  })

  it('adds an item, marks it installed and reports the result', async () => {
    const onInstalled = vi.fn()
    go.LibraryInstall.mockImplementation(() => j({ kind: 'workflow', installed: true, local_id: 'wf-9', warnings: [], missing_automations: ['gemini'] }))
    render(<LibraryModal kind="workflow" onClose={() => {}} onInstalled={onInstalled} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add Hacker News' }))
    expect(await screen.findByText('Added as wf-9.')).toBeInTheDocument()
    expect(screen.getByText('It uses web automations that are not installed yet: gemini.')).toBeInTheDocument()
    expect(go.LibraryInstall).toHaveBeenCalledWith('workflow', 'i1', '', false)
    expect(onInstalled).toHaveBeenCalledWith(expect.objectContaining({ local_id: 'wf-9' }))
    expect(screen.getByText('Installed 1.2.0')).toBeInTheDocument()
  })

  it('shows an install error inline', async () => {
    go.LibraryInstall.mockImplementation(() => j({ error: 'sha256 mismatch: download rejected', code: '' }))
    render(<LibraryModal kind="automation" onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add Hacker News' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('sha256 mismatch')
  })

  it('offers rename or replace when an org name is taken', async () => {
    go.LibraryList.mockImplementation(() => j({ items: [item({ id: 'o1', kind: 'org', slug: 'growth', name: 'Growth team' })], total: 1 }))
    go.LibraryInstall
      .mockImplementationOnce(() => j({ error: 'org "growth" already exists here; pass --rename <name> or --yes to replace it', code: 'invalid_input' }))
      .mockImplementationOnce(() => j({ kind: 'org', installed: true, local_id: 'growth-eu' }))
    render(<LibraryModal kind="org" onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add Growth team' }))
    const prompt = await screen.findByRole('group', { name: 'That name is already in use' })
    const input = within(prompt).getByLabelText('New name')
    expect(input).toHaveValue('growth-2')
    fireEvent.change(input, { target: { value: 'growth-eu' } })
    fireEvent.click(within(prompt).getByText('Add under the new name'))
    expect(await screen.findByText('Added as growth-eu.')).toBeInTheDocument()
    expect(go.LibraryInstall).toHaveBeenLastCalledWith('org', 'o1', 'growth-eu', false)
  })

  it('replace confirms with --yes', async () => {
    go.LibraryList.mockImplementation(() => j({ items: [item({ id: 'o1', kind: 'org', slug: 'growth', name: 'Growth team' })], total: 1 }))
    go.LibraryInstall
      .mockImplementationOnce(() => j({ error: 'pass --rename <name> or --yes', code: 'invalid_input' }))
      .mockImplementationOnce(() => j({ installed: true, local_id: 'growth' }))
    render(<LibraryModal kind="org" onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add Growth team' }))
    fireEvent.click(await screen.findByText('Replace the existing org'))
    await screen.findByText('Added as growth.')
    expect(go.LibraryInstall).toHaveBeenLastCalledWith('org', 'o1', '', true)
  })

  it('an installed item with a newer version offers Update, which reinstalls with --yes', async () => {
    go.LibraryList.mockImplementation(() => j({ items: [item({ installed: { local_id: 'hackernews', version: '1.1.0', update_available: true } })], total: 1 }))
    go.LibraryInstall.mockImplementation(() => j({ installed: true, local_id: 'hackernews' }))
    render(<LibraryModal kind="automation" onClose={() => {}} />)
    expect(await screen.findByText('Update available')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Add Hacker News' }))
    await screen.findByText('Added as hackernews.')
    expect(go.LibraryInstall).toHaveBeenCalledWith('automation', 'i1', '', true)
  })

  it('shows a list error with Retry', async () => {
    go.LibraryList.mockImplementationOnce(() => j({ error: 'monoes.me is unreachable', code: 'auth_or_connection' }))
    render(<LibraryModal kind="automation" onClose={() => {}} />)
    expect(await screen.findByText('monoes.me is unreachable')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Retry'))
    expect(await screen.findByText('Hacker News')).toBeInTheDocument()
  })
})

describe('library strings', () => {
  it('es has every en library key', async () => {
    const en = (await import('../../locales/en.json')).default.library
    const es = (await import('../../locales/es.json')).default.library
    const keys = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (typeof v === 'object' ? keys(v, `${p}${k}.`) : [`${p}${k}`]))
    expect(keys(es).sort()).toEqual(keys(en).sort())
  })
})
