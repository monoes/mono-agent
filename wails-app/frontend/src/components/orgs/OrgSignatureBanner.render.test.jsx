// @vitest-environment jsdom
// Signed org definitions (#288): the banner for an unsigned or changed org,
// monomind's review behind "Review & sign", and signing only on confirm —
// with the reviewed file's sha256.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({
  api: { orgSignatureStatus: vi.fn(), orgSignatureReview: vi.fn(), orgSign: vi.fn() },
  notify: vi.fn(),
}))
import { api, notify } from '../../services/api.js'
import ConfirmHost from '../ConfirmDialog.jsx'
import OrgSignatureBanner, { isSignatureRefusal, requestSignatureRefresh } from './OrgSignatureBanner.jsx'

const CHANGED = { v: 1, org: 'growth', supported: true, state: 'changed', sha256: 'abc', signed: false }
const SIGNED = { v: 1, org: 'growth', supported: true, state: 'signed', sha256: 'def', signed: false }
const REVIEW = { v: 1, org: 'growth', supported: true, state: 'changed', sha256: 'abc', review: 'org growth (changed):\n  lead: runtime claude · git push', signed: false }

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { cleanup() })

function renderBanner() {
  return render(<><OrgSignatureBanner orgName="growth" /><ConfirmHost /></>)
}

describe('OrgSignatureBanner', () => {
  it('shows nothing for a signed org, or below monomind 2.21', async () => {
    api.orgSignatureStatus.mockResolvedValueOnce(SIGNED)
    const { unmount } = renderBanner()
    await waitFor(() => expect(api.orgSignatureStatus).toHaveBeenCalledWith('growth'))
    expect(screen.queryByTestId('org-signature-banner')).toBeNull()
    unmount()

    api.orgSignatureStatus.mockResolvedValueOnce({ v: 1, org: 'growth', supported: false, signed: false })
    renderBanner()
    await waitFor(() => expect(api.orgSignatureStatus).toHaveBeenCalledTimes(2))
    expect(screen.queryByTestId('org-signature-banner')).toBeNull()
  })

  it('shows monomind’s review and signs the reviewed file only on confirm', async () => {
    api.orgSignatureStatus.mockResolvedValue(CHANGED)
    api.orgSignatureReview.mockResolvedValue(REVIEW)
    api.orgSign.mockResolvedValue({ ...REVIEW, state: 'signed', signed: true })
    renderBanner()
    const banner = await screen.findByTestId('org-signature-banner')
    expect(banner).toHaveAttribute('data-state', 'changed')

    fireEvent.click(screen.getByRole('button', { name: 'Review & sign' }))
    expect(await screen.findByTestId('org-sign-review')).toHaveTextContent('git push')
    expect(api.orgSign).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Sign' }))
    await waitFor(() => expect(api.orgSign).toHaveBeenCalledWith('growth', 'abc'))
  })

  it('does not sign when the review is cancelled', async () => {
    api.orgSignatureStatus.mockResolvedValue({ ...CHANGED, state: 'unsigned' })
    api.orgSignatureReview.mockResolvedValue(REVIEW)
    renderBanner()
    fireEvent.click(await screen.findByRole('button', { name: 'Review & sign' }))
    await screen.findByTestId('org-sign-review')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('org-sign-review')).toBeNull())
    expect(api.orgSign).not.toHaveBeenCalled()
  })

  it('reports a refused signature', async () => {
    api.orgSignatureStatus.mockResolvedValue(CHANGED)
    api.orgSignatureReview.mockResolvedValue(REVIEW)
    api.orgSign.mockRejectedValue(Object.assign(new Error('org growth is not signed: the file changed after it was written or reviewed'), { code: 'org_not_signed' }))
    renderBanner()
    fireEvent.click(await screen.findByRole('button', { name: 'Review & sign' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Sign' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('sign org', expect.stringContaining('changed after'), 'org_not_signed'))
  })

  it('offers no signing for a forbidden key', async () => {
    api.orgSignatureStatus.mockResolvedValue({ ...CHANGED, state: 'forbidden-key', detail: '__proto__' })
    renderBanner()
    await screen.findByTestId('org-signature-banner')
    expect(screen.queryByRole('button', { name: 'Review & sign' })).toBeNull()
  })

  it('looks again when a run is refused for its signature', async () => {
    api.orgSignatureStatus.mockResolvedValueOnce(SIGNED).mockResolvedValue(CHANGED)
    renderBanner()
    await waitFor(() => expect(api.orgSignatureStatus).toHaveBeenCalledTimes(1))
    const refusal = 'org growth: the definition changed since the operator signed it — review it, then sign it: monoagentcli org sign growth'
    expect(isSignatureRefusal(refusal)).toBe(true)
    expect(isSignatureRefusal('org growth exited: rate limited')).toBe(false)
    act(() => requestSignatureRefresh('growth'))
    expect(await screen.findByTestId('org-signature-banner')).toHaveAttribute('data-state', 'changed')
  })
})
