// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const api = vi.hoisted(() => ({
  doctorAutomations: vi.fn(),
  rerecordSelector: vi.fn(),
}))
vi.mock('../../services/api.js', () => ({ api }))

import HealthTab from './HealthTab.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

const doctor = { automations: [{ id: 'acme', issues: [], selectors: [
  { key: 'contact.email_input', ok: 40, fail: 0, healed: 0, status: 'ok' },
  { key: 'contact.save_button', ok: 1, fail: 5, healed: 0, status: 'broken' },
] }] }

describe('HealthTab re-record', () => {
  it('re-records a broken selector, shows where it went and refreshes', async () => {
    api.doctorAutomations.mockResolvedValue(doctor)
    let finish
    api.rerecordSelector.mockReturnValue(new Promise(r => { finish = r }))
    render(<HealthTab automationId="acme" />)
    const btn = await screen.findByRole('button', { name: /Re-record/ })
    fireEvent.click(btn)
    expect(api.rerecordSelector).toHaveBeenCalledWith('acme', 'contact.save_button')
    expect(await screen.findByText(/Switch to your browser and click the element for contact.save_button/)).toBeInTheDocument()
    finish({ automation: 'acme', key: 'contact.save_button', where: 'overlay', candidates: [{ css: '#save' }, { text: 'Save' }], url: 'https://app.acme.com' })
    expect(await screen.findByText(/Saved 2 selector candidates for contact.save_button in your local overlay/)).toBeInTheDocument()
    await waitFor(() => expect(api.doctorAutomations).toHaveBeenCalledTimes(2))
    expect(screen.queryByText(/Switch to your browser/)).not.toBeInTheDocument()
  })

  it('offers Re-record on a healthy row only through the menu', async () => {
    api.doctorAutomations.mockResolvedValue(doctor)
    api.rerecordSelector.mockResolvedValue({ key: 'contact.email_input', where: 'package', candidates: [{ css: 'input' }] })
    render(<HealthTab automationId="acme" />)
    await screen.findByText('contact.email_input')
    expect(screen.getAllByRole('button', { name: /Re-record/ })).toHaveLength(1)
    fireEvent.click(screen.getByLabelText('More for contact.email_input'))
    fireEvent.click(screen.getByRole('menuitem', { name: /Re-record/ }))
    await waitFor(() => expect(api.rerecordSelector).toHaveBeenCalledWith('acme', 'contact.email_input'))
    expect(await screen.findByText(/Saved 1 selector candidate for contact.email_input in the package/)).toBeInTheDocument()
  })

  it('shows cancelled, timeout and bridge errors inline without refreshing', async () => {
    api.doctorAutomations.mockResolvedValue(doctor)
    for (const err of ['cancelled', 'timeout', 'browser bridge not connected']) {
      api.rerecordSelector.mockResolvedValueOnce({ error: err })
      const { unmount } = render(<HealthTab automationId="acme" />)
      fireEvent.click(await screen.findByRole('button', { name: /Re-record/ }))
      expect(await screen.findByText(`Re-record contact.save_button: ${err}`)).toBeInTheDocument()
      unmount()
    }
    expect(api.doctorAutomations).toHaveBeenCalledTimes(3)
  })
})
