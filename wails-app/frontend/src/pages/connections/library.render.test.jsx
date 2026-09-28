// @vitest-environment jsdom
// The monoes.me library entry points on the Connections and Orgs pages.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

const go = vi.hoisted(() => ({
  LibraryStatus: vi.fn(),
  LibraryList: vi.fn(),
  LibraryInstall: vi.fn(),
  LibraryPublish: vi.fn(),
}))
vi.mock('../../wailsjs/go/main/App', () => go)

import i18n from '../../i18n.js'
import BrowserAutomations from './BrowserAutomations.jsx'
import { OrgTemplatesButton, PublishOrgButton } from '../../components/orgs/OrgLibraryActions.jsx'

const j = (v) => Promise.resolve(JSON.stringify(v))

beforeEach(async () => {
  await i18n.changeLanguage('en')
  go.LibraryStatus.mockImplementation(() => j({ logged_in: false, user: null }))
  go.LibraryList.mockImplementation(() => j({ items: [], total: 0 }))
})
afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('Browser automations and monoes.me', () => {
  it('with nothing installed, says automations come from monoes.me and offers log in + browse', async () => {
    const onLibrary = vi.fn()
    render(<BrowserAutomations automations={[]} onOpen={() => {}} onRecord={() => {}} onImport={() => {}} onLibrary={onLibrary} />)
    expect(screen.getByText('No web automations installed')).toBeInTheDocument()
    expect(screen.getByText(/now come from monoes.me/)).toBeInTheDocument()
    expect(await screen.findByText('Log in to monoes')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Browse monoes.me'))
    expect(onLibrary).toHaveBeenCalled()
  })

  it('with automations installed, keeps a compact browse action', async () => {
    render(<BrowserAutomations automations={[{ id: 'hackernews', name: 'Hacker News', version: '1.0.0', source: 'monoes', actions: 3 }]}
      onOpen={() => {}} onRecord={() => {}} onImport={() => {}} onLibrary={() => {}} />)
    expect(screen.queryByText('No web automations installed')).not.toBeInTheDocument()
    expect(screen.getByText('Browse monoes.me')).toBeInTheDocument()
    expect(await screen.findByText('Log in to monoes')).toBeInTheDocument()
  })
})

describe('Orgs page library actions', () => {
  it('Org templates opens the library for orgs', async () => {
    render(<OrgTemplatesButton onInstalled={() => {}} />)
    fireEvent.click(screen.getByText('Org templates'))
    expect(await screen.findByText('Org templates from monoes.me')).toBeInTheDocument()
    expect(go.LibraryList).toHaveBeenCalledWith('org', 'official', '', 1)
  })

  it('Publish to monoes opens the publish dialog for the org', async () => {
    render(<PublishOrgButton orgName="growth" />)
    fireEvent.click(screen.getByText('Publish to monoes'))
    expect(await screen.findByText('Publish to monoes.me')).toBeInTheDocument()
  })
})
