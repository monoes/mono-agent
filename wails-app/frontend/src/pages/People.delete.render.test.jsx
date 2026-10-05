// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'

const { mockApi, mockConfirm } = vi.hoisted(() => ({
  mockApi: {
    getPeople: vi.fn(),
    getPeopleCount: vi.fn(() => Promise.resolve(2)),
    getPeopleTagsMap: vi.fn(() => Promise.resolve({})),
    deletePeople: vi.fn(() => Promise.resolve()),
  },
  mockConfirm: vi.fn(),
}))

vi.mock('../services/api.js', () => ({ api: mockApi, subscribeEvent: () => () => {}, notify: vi.fn() }))
vi.mock('../components/ConfirmDialog.jsx', () => ({ confirm: mockConfirm }))
vi.mock('../wailsjs/go/main/App', () => ({}))

import People from './People.jsx'

const rows = [
  { id: 'a', username: 'ada', platform: 'X' },
  { id: 'b', username: 'bob', platform: 'X' },
]

describe('People delete', { timeout: 20000 }, () => {
  afterEach(cleanup)
  beforeEach(() => {
    vi.clearAllMocks()
    mockApi.getPeople.mockResolvedValue(rows)
  })

  it('deletes the selected people after confirming', async () => {
    mockConfirm.mockResolvedValue(true)
    render(<People />)
    fireEvent.click(await screen.findByLabelText('Select @ada', {}, { timeout: 5000 }))
    fireEvent.click(await screen.findByText('Delete (1)'))
    await waitFor(() => expect(mockApi.deletePeople).toHaveBeenCalledWith(['a']))
    expect(mockApi.getPeople.mock.calls.length).toBeGreaterThan(1) // list reloaded
  })

  it('deletes nobody when the confirmation is declined', async () => {
    mockConfirm.mockResolvedValue(false)
    render(<People />)
    fireEvent.click(await screen.findByLabelText('Select all', {}, { timeout: 5000 }))
    fireEvent.click(await screen.findByText('Delete (2)'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled())
    expect(mockApi.deletePeople).not.toHaveBeenCalled()
  })
})
