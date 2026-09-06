// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import ApplicationsProcessPendingFlow from './ApplicationsProcessPendingFlow.jsx'
import * as WailsApp from '../wailsjs/go/main/App'

// Regression test for the HIGH finding in sendCurrent (~line 134): it used
// to call advance() unconditionally after the try/catch/finally around
// SendApplication, even when the send failed. advance() -> processCurrent()
// immediately clears `error` (and once stage becomes 'done' the error block
// never renders at all), so a failed send silently vanished, the item was
// never counted in `summary` as sent or skipped, and there was no way to
// see or retry the failure. This test drives the flow to the "applied"
// stage, makes SendApplication reject, clicks "Send Now", and asserts the
// flow stays on the same item with the error visible instead of advancing.

vi.mock('../components/ConfirmDialog.jsx', () => ({
  confirm: vi.fn(() => Promise.resolve(true)),
}))

vi.mock('../wailsjs/go/main/App', () => ({
  OpenJSONFilePicker: vi.fn(),
  ReadJSONFile: vi.fn(() => Promise.resolve({})),
  EvaluateApplication: vi.fn(),
  HasGeneratedDocuments: vi.fn(),
  ApplyToApplication: vi.fn(),
  SendApplication: vi.fn(),
  SetApplicationStatus: vi.fn(),
}))

const pendingApplications = [
  { id: 'app-1', title: 'Backend Engineer', company: 'Acme' },
  { id: 'app-2', title: 'Frontend Engineer', company: 'Widgets Inc' },
]

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

async function startAndReachApplied() {
  WailsApp.EvaluateApplication.mockResolvedValue({ verdict: 'GOOD', overall_score: 8, rationale: 'solid fit' })
  // alreadyPrepared: true -> computeNextStep returns 'ready-to-send' straight
  // from 'evaluated', landing us on the 'applied' stage (Send Now / Next)
  // without needing to also drive the apply step.
  WailsApp.HasGeneratedDocuments.mockResolvedValue(true)

  render(<ApplicationsProcessPendingFlow pendingApplications={pendingApplications} onClose={() => {}} />)

  fireEvent.click(screen.getByRole('button', { name: 'Start' }))

  await waitFor(() => expect(screen.getByRole('button', { name: 'Send Now' })).toBeInTheDocument())
}

describe('ApplicationsProcessPendingFlow sendCurrent', () => {
  it('does not advance to the next item when SendApplication fails, and keeps the error visible', async () => {
    WailsApp.SendApplication.mockRejectedValue(new Error('network error: connection refused'))

    await startAndReachApplied()

    expect(screen.getByText('1 / 2')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Send Now' }))

    await waitFor(() => expect(screen.getByText(/network error: connection refused/)).toBeInTheDocument())

    // Still on the first item -- did not silently advance to item 2 or to
    // the "done" summary screen.
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send Now' })).toBeInTheDocument()
    expect(screen.queryByText(/^Done\./)).not.toBeInTheDocument()

    expect(WailsApp.SendApplication).toHaveBeenCalledTimes(1)
  })

  it('advances to the next item once SendApplication succeeds', async () => {
    WailsApp.SendApplication.mockResolvedValue({})

    await startAndReachApplied()

    fireEvent.click(screen.getByRole('button', { name: 'Send Now' }))

    await waitFor(() => expect(screen.getByText('2 / 2')).toBeInTheDocument())
    expect(screen.queryByText(/network error/)).not.toBeInTheDocument()
  })
})
