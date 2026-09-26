// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
const markRead = vi.fn(() => Promise.resolve(null))
vi.mock('../services/api.js', () => ({
  api: {
    getAllPersonMessages: () => Promise.resolve([
      { id: 'm1', person_id: 'p1', direction: 'inbound', source: 'linkedin', subject: 'New one', person_platform_username: 'sam' },
      { id: 'm2', person_id: 'p1', direction: 'inbound', source: 'linkedin', subject: 'Old one', read_at: '2026-09-25T10:00:00Z', person_platform_username: 'sam' },
      { id: 'm3', person_id: 'p1', direction: 'outbound', source: 'linkedin', subject: 'Mine', person_platform_username: 'sam' },
    ]),
    markPersonMessagesRead: (...a) => markRead(...a),
  },
}))
vi.mock('../components/MessageDetailModal.jsx', () => ({ default: () => null }))
import Communications from './Communications.jsx'

afterEach(cleanup)

describe('Communications unread state', () => {
  it('marks only unread inbound messages, filters them, and marks one read when opened', async () => {
    render(<Communications onProfile={vi.fn()} />)
    await screen.findByText('New one')
    expect(screen.getAllByTitle('Unread')).toHaveLength(1)
    fireEvent.click(screen.getByText(/^Unread/))
    expect(screen.queryByText('Old one')).toBeNull()
    fireEvent.click(screen.getByText('New one'))
    expect(markRead).toHaveBeenCalledWith('', ['m1'])
    expect(screen.queryAllByTitle('Unread')).toHaveLength(0)
    // The filter stays switchable after the last unread one is read.
    fireEvent.click(screen.getByText(/^Unread/))
    fireEvent.click(screen.getByText('Old one'))
    expect(markRead).toHaveBeenCalledTimes(1) // already read: no call
  })
})
