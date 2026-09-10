// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import Documents from './Documents.jsx'
import * as WailsApp from '../wailsjs/go/main/App'

vi.mock('../components/ConfirmDialog.jsx', () => ({
  confirm: vi.fn(() => Promise.resolve(true)),
}))

vi.mock('../wailsjs/go/main/App', () => ({
  ListProfileDocuments: vi.fn(),
  IndexProfileDocument: vi.fn(),
  DeleteProfileDocument: vi.fn(),
  UploadProfileDocument: vi.fn(),
  OpenAnyFilePicker: vi.fn(),
  SearchProfileKnowledge: vi.fn(),
  IsMonomindInitialized: vi.fn(() => Promise.resolve(true)),
}))

// indexed & fresh — "Index all"/"Index selected" must skip this one
const freshDoc = { id: 'doc-1', filename: 'resume.pdf', source: 'upload', size_bytes: 100, created_at: '2026-01-01', indexed: true, stale: false }
// never indexed — needs indexing
const notIndexedDoc = { id: 'doc-2', filename: 'notes.md', source: 'upload', size_bytes: 200, created_at: '2026-01-02', indexed: false, stale: false }
// indexed but stale — needs re-indexing, and NOT deletable (discovered)
const staleDiscoveredDoc = { id: 'doc-3', filename: 'AGENTS.md', source: 'discovered', size_bytes: 300, created_at: '2026-01-03', indexed: true, stale: true }

beforeEach(() => {
  vi.clearAllMocks()
  WailsApp.ListProfileDocuments.mockResolvedValue([freshDoc, notIndexedDoc, staleDiscoveredDoc])
  WailsApp.IndexProfileDocument.mockResolvedValue({ indexed: true })
  WailsApp.DeleteProfileDocument.mockResolvedValue(undefined)
})

afterEach(() => {
  cleanup()
})

async function renderLoaded() {
  render(<Documents />)
  await waitFor(() => expect(screen.getByText('resume.pdf')).toBeInTheDocument())
}

describe('Documents multi-select', () => {
  it('renders a checkbox per row and a header select-all checkbox', async () => {
    await renderLoaded()
    const checkboxes = screen.getAllByRole('checkbox')
    // 1 header + 3 rows
    expect(checkboxes).toHaveLength(4)
  })

  it('header checkbox selects and deselects all visible rows', async () => {
    await renderLoaded()
    const [header, row1, row2, row3] = screen.getAllByRole('checkbox')
    fireEvent.click(header)
    expect(row1.checked).toBe(true)
    expect(row2.checked).toBe(true)
    expect(row3.checked).toBe(true)
    fireEvent.click(header)
    expect(row1.checked).toBe(false)
  })

  it('hides "Index selected"/"Delete selected" with no selection, shows them once something is checked', async () => {
    await renderLoaded()
    expect(screen.queryByRole('button', { name: /Index selected/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Delete selected/ })).not.toBeInTheDocument()

    const [, row1] = screen.getAllByRole('checkbox')
    fireEvent.click(row1)

    expect(screen.getByRole('button', { name: /Index selected/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Delete selected/ })).toBeInTheDocument()
  })
})

describe('Documents "Index all"', () => {
  it('indexes only the documents that need it, skipping already-indexed-and-fresh ones', async () => {
    await renderLoaded()

    fireEvent.click(screen.getByRole('button', { name: /Index all/ }))

    await waitFor(() => expect(WailsApp.IndexProfileDocument).toHaveBeenCalledTimes(2))
    expect(WailsApp.IndexProfileDocument).toHaveBeenCalledWith('doc-2')
    expect(WailsApp.IndexProfileDocument).toHaveBeenCalledWith('doc-3')
    expect(WailsApp.IndexProfileDocument).not.toHaveBeenCalledWith('doc-1')
  })
})

describe('Documents "Delete selected"', () => {
  it('confirms once, then deletes only the non-discovered documents in the selection', async () => {
    await renderLoaded()

    const [, row1, , row3] = screen.getAllByRole('checkbox')
    fireEvent.click(row1) // freshDoc, source: upload — deletable
    fireEvent.click(row3) // staleDiscoveredDoc, source: discovered — NOT deletable

    fireEvent.click(screen.getByRole('button', { name: /Delete selected/ }))

    const { confirm } = await import('../components/ConfirmDialog.jsx')
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))

    await waitFor(() => expect(WailsApp.DeleteProfileDocument).toHaveBeenCalledTimes(1))
    expect(WailsApp.DeleteProfileDocument).toHaveBeenCalledWith('doc-1')
    expect(WailsApp.DeleteProfileDocument).not.toHaveBeenCalledWith('doc-3')
  })
})
