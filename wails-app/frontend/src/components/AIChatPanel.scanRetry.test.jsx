// @vitest-environment jsdom
//
// The panel scans the local agent runtimes the first time it opens. A scan that failed (monomind
// killed by a timeout while the machine is overloaded, say) used to stay the panel's answer until
// the app was restarted: "monomind couldn't be used" and "No AI agent is installed", long after
// the cause was gone. It is tried again the next time the panel opens; a scan that worked is not
// repeated. Same focused scoping and mocks as AIChatPanel.liveRegion.test.jsx: agentRuntimes.js
// is mocked directly, which also sidesteps its module-level cache.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'

// jsdom implements neither — AIChatPanel's scroll handling touches both.
Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || (() => {})
Element.prototype.scrollTo = Element.prototype.scrollTo || (() => {})

const getAgentRuntimeModels = vi.fn().mockResolvedValue([{ id: 'sonnet' }])
const installedClaude = { agents: [{ id: 'claude', installed: true, binary: '' }] }
const killedScan = { error: 'handshake with /opt/homebrew/bin/monomind failed: signal: killed' }
const cachedAgentScan = vi.fn()

vi.mock('../lib/agentRuntimes.js', async (importOriginal) => ({
  isMonomindNotFound: (await importOriginal()).isMonomindNotFound,
  cachedAgentScan: (...args) => cachedAgentScan(...args),
}))

vi.mock('../services/api.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    onChatEvent: () => () => {},
    api: {
      ...actual.api,
      getAgentRuntimeModels: (...args) => getAgentRuntimeModels(...args),
      listChatConversations: vi.fn().mockResolvedValue({ items: [] }),
      getChatTurns: vi.fn().mockResolvedValue({ items: [] }),
      getChatEvents: vi.fn().mockResolvedValue({ items: [], hasMore: false }),
    },
  }
})

import AIChatPanel from './AIChatPanel.jsx'

beforeEach(() => {
  vi.clearAllMocks()
  cachedAgentScan.mockReset()
})

afterEach(() => {
  cleanup()
})

const panel = isOpen => <AIChatPanel workflowID="general" isOpen={isOpen} onClose={() => {}} />
// the runtime selector and the panel's body both say so: every element that does
const scanErrors = () => screen.queryAllByText(/monomind couldn.t be used/)

describe('AIChatPanel agent scan', () => {
  it('scans again the next time the panel opens after a scan that failed', async () => {
    cachedAgentScan.mockResolvedValueOnce(killedScan).mockResolvedValue(installedClaude)
    const { rerender } = render(panel(true))
    await waitFor(() => expect(scanErrors().length).toBeGreaterThan(0))
    expect(cachedAgentScan).toHaveBeenCalledTimes(1)

    rerender(panel(false))
    rerender(panel(true))

    await waitFor(() => expect(cachedAgentScan).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(scanErrors()).toHaveLength(0))
    expect(await screen.findByRole('option', { name: /claude/i })).toBeInTheDocument()
  })

  it('forgets "monomind is missing" too when the scan after reopening finds it', async () => {
    cachedAgentScan.mockResolvedValueOnce({ error: 'monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli`' })
      .mockResolvedValue(installedClaude)
    const { rerender } = render(panel(true))
    await waitFor(() => expect(screen.queryAllByText(/monomind missing/).length).toBeGreaterThan(0))

    rerender(panel(false))
    rerender(panel(true))

    await waitFor(() => expect(cachedAgentScan).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryAllByText(/monomind missing|monomind \(the local AI agent engine\) isn't installed/)).toHaveLength(0))
    expect(await screen.findByRole('option', { name: /claude/i })).toBeInTheDocument()
  })

  // A scan that worked but finds no agent installed is "No agent runtimes", and nothing of the
  // failure before it is left on the screen.
  it.each([
    ['a failed scan', killedScan, /monomind couldn.t be used/],
    ['monomind missing', { error: 'monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli`' }, /monomind missing/],
  ])('leaves nothing of %s when the scan after reopening finds no agent installed', async (_name, firstScan, staleText) => {
    cachedAgentScan.mockResolvedValueOnce(firstScan).mockResolvedValue({ agents: [{ id: 'claude', installed: false }] })
    const { rerender } = render(panel(true))
    await waitFor(() => expect(screen.queryAllByText(staleText).length).toBeGreaterThan(0))

    rerender(panel(false))
    rerender(panel(true))

    await waitFor(() => expect(cachedAgentScan).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('option', { name: 'No agent runtimes' })).toBeInTheDocument()
    expect(screen.queryAllByText(staleText)).toHaveLength(0)
  })

  it('does not scan again when the panel reopens after a scan that worked', async () => {
    cachedAgentScan.mockResolvedValue(installedClaude)
    const { rerender } = render(panel(true))
    expect(await screen.findByRole('option', { name: /claude/i })).toBeInTheDocument()

    rerender(panel(false))
    rerender(panel(true))

    expect(cachedAgentScan).toHaveBeenCalledTimes(1)
  })
})
