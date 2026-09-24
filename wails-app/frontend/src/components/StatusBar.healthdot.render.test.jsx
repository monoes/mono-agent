// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'

const mockHealth = { report: null, cliMissing: false }
vi.mock('../lib/health.js', async importOriginal => ({
  ...(await importOriginal()),
  getHealth: () => mockHealth,
  subscribeHealth: () => () => {},
}))
vi.mock('../wailsjs/go/main/App', () => ({
  GetVersion: () => Promise.resolve({ version: 'dev' }),
  CheckForUpdate: vi.fn(),
  AppSelfUpdate: vi.fn(),
}))
vi.mock('../services/api.js', () => ({ subscribeEvent: () => () => {} }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key, o) => (o && o.n !== undefined ? `${key}:${o.n}` : key) }),
}))

import StatusBar from './StatusBar.jsx'

afterEach(cleanup)

// #146 item 4: the dot's accessible name carries the status it shows.
describe('StatusBar health dot', () => {
  it('names the visible status for screen readers', () => {
    mockHealth.report = { results: [{ id: 'core.db', group: 'core', status: 'fail', required: true }] }
    render(<StatusBar stats={{}} dbConnected onOpenHealth={() => {}} />)
    const dot = screen.getByTestId('health-dot')
    expect(dot).toHaveTextContent('settings.health.statusBroken')
    expect(dot).toHaveAccessibleName('settings.health.statusBroken — settings.health.statusTitle')
  })

  it('includes the issue count', () => {
    mockHealth.report = { results: [{ id: 'browser.bridge', group: 'browser', status: 'warn' }] }
    render(<StatusBar stats={{}} dbConnected onOpenHealth={() => {}} />)
    expect(screen.getByTestId('health-dot')).toHaveAccessibleName('settings.health.statusIssues:1 — settings.health.statusTitle')
  })
})
