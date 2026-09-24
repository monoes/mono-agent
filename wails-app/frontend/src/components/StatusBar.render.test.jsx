// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import StatusBar from './StatusBar.jsx'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}))

vi.mock('../lib/health.js', () => ({
  getHealth: () => ({ report: null, cliMissing: false }),
  subscribeHealth: () => () => {},
  summarize: () => ({ level: 'ok', issues: 0 }),
}))

vi.mock('../wailsjs/go/main/App', () => ({
  GetVersion: () => Promise.resolve({ version: '1.0.0' }),
  CheckForUpdate: () => Promise.resolve({ update_available: false }),
  AppSelfUpdate: () => Promise.resolve({ success: true }),
}))

vi.mock('../services/api.js', () => ({
  subscribeEvent: () => () => {},
}))

describe('StatusBar HIL toggle', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders HIL toggle next to chat icon and handles click', () => {
    const onToggleHil = vi.fn()
    const onToggleChat = vi.fn()

    render(
      <StatusBar
        stats={{ total_workflows: 5, total_people: 10, active_sessions: 1 }}
        dbConnected={true}
        chatOpen={false}
        onToggleChat={onToggleChat}
        hilOpen={false}
        hilCount={3}
        onToggleHil={onToggleHil}
      />
    )

    const hilBtn = screen.getByTestId('hil-status-toggle')
    expect(hilBtn).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()

    fireEvent.click(hilBtn)
    expect(onToggleHil).toHaveBeenCalledTimes(1)

    const chatBtn = screen.getByTitle('Open AI Assistant')
    fireEvent.click(chatBtn)
    expect(onToggleChat).toHaveBeenCalledTimes(1)
  })
})
