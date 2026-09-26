// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react'

const { mockApi } = vi.hoisted(() => ({
  mockApi: {
    getPersonTags: vi.fn(),
    getAllTags: vi.fn(),
    addPersonTag: vi.fn(),
    removePersonTag: vi.fn(),
    updateTagColor: vi.fn(),
    subscribeEvent: vi.fn(() => () => {}),
    getPeopleTagsMap: vi.fn(() => Promise.resolve({})),
    getPeople: vi.fn(() => Promise.resolve([])),
    getPeopleCount: vi.fn(() => Promise.resolve(1)),
  },
}))

vi.mock('../services/api.js', () => ({
  api: mockApi,
  subscribeEvent: () => () => {},
  notify: vi.fn(),
}))

vi.mock('../wailsjs/go/main/App', () => ({}))

import People, { TAG_SUGGESTION_GROUPS, TAG_COLORS } from './People.jsx'

// Each test renders the whole People page and opens the tag editor; on a
// busy CI box that alone can take several seconds, so these tests get more
// than vitest's 5s default and the async waits more than waitFor's 1s.
const SLOW = { timeout: 5000 }

describe('People Tag Suggestions & Color Customization', { timeout: 20000 }, () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    mockApi.getPersonTags.mockResolvedValue([
      { id: 't1', name: 'Lead', color: '#00b4d8' },
    ])
    mockApi.getAllTags.mockResolvedValue([
      { id: 't1', name: 'Lead', color: '#00b4d8' },
      { id: 't2', name: 'Founder', color: '#7c3aed' },
    ])
    mockApi.getPeople.mockResolvedValue([
      {
        id: 'p1',
        full_name: 'Alice Founder',
        username: 'alice',
        platform: 'LINKEDIN',
        platform_user_id: 'alice-id',
        tags: [{ id: 't1', name: 'Lead', color: '#00b4d8' }],
      },
    ])
    mockApi.getPeopleCount.mockResolvedValue(1)
    mockApi.getPeopleTagsMap.mockResolvedValue({
      p1: [{ id: 't1', name: 'Lead', color: '#00b4d8' }],
    })
  })

  it('exports curated tag suggestion groups with correct titles and tags', () => {
    expect(TAG_SUGGESTION_GROUPS).toHaveLength(3)
    const titles = TAG_SUGGESTION_GROUPS.map(g => g.title)
    expect(titles).toContain('Sales Pipeline')
    expect(titles).toContain('Role & Authority')
    expect(titles).toContain('Advocacy & Relationships')

    const salesTags = TAG_SUGGESTION_GROUPS.find(g => g.title === 'Sales Pipeline').tags.map(t => t.name)
    expect(salesTags).toContain('Lead')
    expect(salesTags).toContain('Won')
    expect(salesTags).toContain('Lost')
    expect(salesTags).toContain('Irrelevant')

    const roleTags = TAG_SUGGESTION_GROUPS.find(g => g.title === 'Role & Authority').tags.map(t => t.name)
    expect(roleTags).toContain('Founder')
    expect(roleTags).toContain('Decision Maker')
    expect(roleTags).toContain('Investor')

    const advocacyTags = TAG_SUGGESTION_GROUPS.find(g => g.title === 'Advocacy & Relationships').tags.map(t => t.name)
    expect(advocacyTags).toContain('Champion')
    expect(advocacyTags).toContain('Sales Agent')
    expect(advocacyTags).toContain('Influencer')
  })

  it('renders suggested tags in modal when opening TagEditor', async () => {
    render(<People />)

    // Wait for the table to load
    await screen.findByText('@alice', {}, SLOW)

    // Click on the tag chip or tag button to open TagEditor modal
    const manageTagButtons = screen.getAllByTitle('Click to manage tags')
    expect(manageTagButtons.length).toBeGreaterThan(0)
    fireEvent.click(manageTagButtons[0])

    // Verify modal is open, and let its tag load settle before asserting
    const dialog = screen.getByRole('dialog', { name: /manage tags for/i })
    await within(dialog).findByTitle("Click to customize this tag's color", {}, SLOW)

    // Verify the 3 category headers appear
    expect(within(dialog).getByText('Sales Pipeline')).toBeInTheDocument()
    expect(within(dialog).getByText('Role & Authority')).toBeInTheDocument()
    expect(within(dialog).getByText('Advocacy & Relationships')).toBeInTheDocument()

    // Verify suggested tag buttons are rendered. Scoped to the dialog and
    // read by text: role queries over the whole page compute every button's
    // accessible name and style, which took seconds on a loaded CI box and
    // ran this test past its timeout.
    expect(within(dialog).getByText('Founder', { selector: 'button, button *' }).closest('button')).toBeInTheDocument()
    expect(within(dialog).getByText('Champion', { selector: 'button, button *' }).closest('button')).toBeInTheDocument()
  })

  it('toggles a suggested tag to add it to the contact', async () => {
    mockApi.addPersonTag.mockResolvedValue({ id: 't2', name: 'Founder', color: '#7c3aed' })
    render(<People />)

    await screen.findByText('@alice', {}, SLOW)

    fireEvent.click(screen.getAllByTitle('Click to manage tags')[0])
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByTitle("Click to customize this tag's color", {}, SLOW)

    // Click Founder tag button
    const founderBtn = within(dialog).getByText('Founder', { selector: 'button, button *' }).closest('button')
    fireEvent.click(founderBtn)

    await waitFor(() => {
      expect(mockApi.addPersonTag).toHaveBeenCalledWith('p1', 'Founder', '#7c3aed')
    }, SLOW)
  })

  it('allows customizing tag color via updateTagColor', async () => {
    mockApi.updateTagColor.mockResolvedValue(true)
    render(<People />)

    await screen.findByText('@alice', {}, SLOW)

    fireEvent.click(screen.getAllByTitle('Click to manage tags')[0])

    // Wait for tags to load in TagEditor
    await screen.findByTitle("Click to customize this tag's color", {}, SLOW)

    // Find the color customization button on the assigned Lead tag
    const colorDotBtn = screen.getByTitle("Click to customize this tag's color")
    fireEvent.click(colorDotBtn)

    // Verify the color picker panel appears
    expect(screen.getByText(/Color for/i)).toBeInTheDocument()

    // Click emerald swatch (#10b981)
    const swatch = screen.getByTitle('Set color to #10b981')
    fireEvent.click(swatch)

    await waitFor(() => {
      expect(mockApi.updateTagColor).toHaveBeenCalledWith('t1', '#10b981')
    }, SLOW)
  })
})
