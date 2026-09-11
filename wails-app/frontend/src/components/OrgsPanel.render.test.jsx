// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, waitFor, cleanup, act } from '@testing-library/react'
import OrgsPanel from './OrgsPanel.jsx'

vi.mock('../services/api.js', () => ({
  api: {
    isReady: vi.fn(() => Promise.resolve(true)),
    isMonomindInitialized: vi.fn(() => Promise.resolve(true)),
    listOrgDesigns: vi.fn(() => Promise.resolve({
      items: [{ name: 'test-org', goal: 'a goal', status: 'active', roleCount: 1 }],
    })),
  },
  onOrgEvent: vi.fn(() => () => {}),
  onOrgEventsClosed: vi.fn(() => () => {}),
  onOrgRunStatus: vi.fn(() => () => {}),
  onOrgDesignUpdated: vi.fn(() => () => {}),
  notify: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

// Regression test for the exact bug reported live: opening the Orgs page
// shows a spinner that never clears, even though the org list actually
// loaded successfully underneath. Root cause: OrgsPanel's first-activation
// effect tracks "is this the first load" with a plain ref (firstLoadRef)
// that it flips to false as soon as the effect body runs -- but the ref
// persists across React StrictMode's dev-only mount->cleanup->remount
// double-invoke of every effect on initial mount, while the *cancellation*
// of the first invocation's in-flight isReady-polling correctly stops it
// from ever reaching its loadOrgs(false) call. The second (surviving)
// invocation then sees firstLoadRef.current already false and takes the
// "silent re-activation" branch (loadOrgs(true)), which populates the org
// list but deliberately never touches the loadingOrgs state -- so the
// spinner shown from the initial useState(true) is never cleared, even
// though `orgs` itself is correctly populated underneath it.
//
// This only reproduces under React.StrictMode (development), which is
// exactly the `wails dev` environment the bug was reported in -- a
// production build never double-invokes effects and would not show this.
it('clears the loading spinner and shows the org list after StrictMode double-invokes the first-load effect', async () => {
  render(
    <React.StrictMode>
      <OrgsPanel />
    </React.StrictMode>,
  )

  await waitFor(() => {
    expect(screen.getByText('test-org')).toBeInTheDocument()
  })
  expect(screen.queryByText('No orgs found.')).not.toBeInTheDocument()
})

// Guards the other half of this effect's contract, which the fix above
// must not regress: a genuine re-activation (navigating away from Orgs and
// back, without the panel ever unmounting -- this app keeps visited pages
// mounted and just toggles pageActive) should silently refresh in the
// background, never flashing the spinner a second time.
it('does not re-show the spinner on a later re-activation after the first load already completed', async () => {
  const { rerender } = render(
    <React.StrictMode>
      <OrgsPanel pageActive={true} />
    </React.StrictMode>,
  )
  await waitFor(() => {
    expect(screen.getByText('test-org')).toBeInTheDocument()
  })

  await act(async () => {
    rerender(
      <React.StrictMode>
        <OrgsPanel pageActive={false} />
      </React.StrictMode>,
    )
  })
  await act(async () => {
    rerender(
      <React.StrictMode>
        <OrgsPanel pageActive={true} />
      </React.StrictMode>,
    )
  })

  // Still showing the org list immediately, never a spinner flash.
  expect(screen.getByText('test-org')).toBeInTheDocument()
})
