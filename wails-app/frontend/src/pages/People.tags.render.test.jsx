// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'

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

describe('People Tag Suggestions & Color Customization', () => {
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
    await waitFor(() => {
      expect(screen.getByText('@alice')).toBeInTheDocument()
    })

    // Click on the tag chip or tag button to open TagEditor modal
    const manageTagButtons = screen.getAllByTitle('Click to manage tags')
    expect(manageTagButtons.length).toBeGreaterThan(0)
    fireEvent.click(manageTagButtons[0])

    // Verify modal is open
    expect(screen.getByRole('dialog', { name: /manage tags for/i })).toBeInTheDocument()

    // Verify the 3 category headers appear
    expect(screen.getByText('Sales Pipeline')).toBeInTheDocument()
    expect(screen.getByText('Role & Authority')).toBeInTheDocument()
    expect(screen.getByText('Advocacy & Relationships')).toBeInTheDocument()

    // Verify suggested tag buttons are rendered
    expect(screen.getByRole('button', { name: /^founder$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^champion$/i })).toBeInTheDocument()
  })

  it('toggles a suggested tag to add it to the contact', async () => {
    mockApi.addPersonTag.mockResolvedValue({ id: 't2', name: 'Founder', color: '#7c3aed' })
    render(<People />)

    await waitFor(() => {
      expect(screen.getByText('@alice')).toBeInTheDocument()
    })

    fireEvent.click(screen.getAllByTitle('Click to manage tags')[0])
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    // Click Founder tag button
    const founderBtn = screen.getByRole('button', { name: /^founder$/i })
    fireEvent.click(founderBtn)

    await waitFor(() => {
      expect(mockApi.addPersonTag).toHaveBeenCalledWith('p1', 'Founder', '#7c3aed')
    })
  })

  it('allows customizing tag color via updateTagColor', async () => {
    mockApi.updateTagColor.mockResolvedValue(true)
    render(<People />)

    await waitFor(() => {
      expect(screen.getByText('@alice')).toBeInTheDocument()
    })

    fireEvent.click(screen.getAllByTitle('Click to manage tags')[0])

    // Wait for tags to load in TagEditor
    await waitFor(() => {
      expect(screen.getByTitle("Click to customize this tag's color")).toBeInTheDocument()
    })

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
    })
  })
})
