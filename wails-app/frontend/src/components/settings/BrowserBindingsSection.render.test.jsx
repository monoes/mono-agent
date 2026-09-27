// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const App = {}
beforeEach(() => {
  for (const k of ['GetBrowsers', 'BindBrowser', 'GetProfiles']) App[k] = vi.fn()
  window.go = { main: { App } }
  App.GetProfiles.mockResolvedValue([{ id: 'p-work', name: 'Work' }, { id: 'p-home', name: 'Personal' }])
})
afterEach(() => { cleanup(); delete window.go })

const edge = { instance: '3f2a91c0-0000-4000-8000-000000000001', label: 'Edge Work', profile_id: 'p-work', profile_name: 'Work', legacy: false, conflict: false, version: '1.5.0' }

async function mount(report) {
  App.GetBrowsers.mockResolvedValue(report)
  const { default: BrowserBindingsSection } = await import('./BrowserBindingsSection.jsx')
  render(<BrowserBindingsSection />)
  await screen.findByTestId('browser-bindings')
  await waitFor(() => expect(App.GetBrowsers).toHaveBeenCalled())
}

describe('BrowserBindingsSection', () => {
  it('shows each browser with its bound profile selected', async () => {
    await mount({ running: true, browsers: [edge] })
    const select = await screen.findByLabelText('Profile for Edge Work')
    expect(select).toHaveValue('p-work')
  })

  it('binds through the CLI when the select changes, then reloads', async () => {
    await mount({ running: true, browsers: [edge] })
    App.BindBrowser.mockResolvedValue({ ...edge, profile_id: 'p-home' })
    fireEvent.change(await screen.findByLabelText('Profile for Edge Work'), { target: { value: 'p-home' } })
    await waitFor(() => expect(App.BindBrowser).toHaveBeenCalledWith(edge.instance, 'p-home'))
    await waitFor(() => expect(App.GetBrowsers).toHaveBeenCalledTimes(2))
  })

  it('warns about a conflict and disables a legacy browser', async () => {
    await mount({ running: true, browsers: [
      { ...edge, conflict: true },
      { instance: 'legacy', label: '', profile_id: '', legacy: true },
    ] })
    expect(await screen.findByText(/only the most recently connected one is used/)).toBeInTheDocument()
    expect(screen.getByLabelText('Profile for legacy')).toBeDisabled()
  })

  it('says when no bridge is running', async () => {
    await mount({ running: false, hint: 'No bridge is running. Start one with `monoagentcli extension serve`.', browsers: [] })
    expect(await screen.findByText(/No bridge is running/)).toBeInTheDocument()
  })
})
