// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
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
  OpenPathWithOS: vi.fn(() => Promise.resolve()),
  OpenURL: vi.fn(() => Promise.resolve()),
  GetProfileDocumentText: vi.fn(() => Promise.resolve('# Captured page')),
  GetProfileDocumentData: vi.fn(() => Promise.resolve('')),
  IsMonomindInitialized: vi.fn(() => Promise.resolve(true)),
}))

const inbox = '/home/u/.monoagent/profiles/p/.monomind/inbox'
const capture = {
  id: 'doc-9', filename: 'Jev Picker Plan', source: 'extension', size_bytes: 389357,
  path: `${inbox}/2026-09-22T10-20-19Z-claude-ai/page.mhtml`, capture_dir: `${inbox}/2026-09-22T10-20-19Z-claude-ai`,
  url: 'https://claude.ai/artifact/RFS7TQBkQX2MctLJqNtMDt', created_at: '2026-09-22 10:20:19', indexed: false, stale: false,
}
const readableCapture = {
  ...capture, id: 'doc-10', filename: 'An article', size_bytes: 120,
  path: `${inbox}/2026-09-22T11-00-00Z-example-com/readable.md`, capture_dir: `${inbox}/2026-09-22T11-00-00Z-example-com`,
  url: 'https://example.com/article', created_at: '2026-09-22 11:00:00',
}
const upload = { id: 'doc-1', filename: 'resume.pdf', source: 'upload', path: '/v/doc-001.pdf', size_bytes: 100, created_at: '2026-01-01 09:00:00', indexed: true, stale: false }
const discovered = { id: 'doc-2', filename: 'notes.txt', source: 'discovered', path: '/p/notes.txt', size_bytes: 50, created_at: '2026-03-01 09:00:00', indexed: true, stale: false }

beforeEach(() => {
  vi.clearAllMocks()
  try { localStorage.clear() } catch { /* no storage in this environment */ }
  WailsApp.ListProfileDocuments.mockResolvedValue([upload, discovered, capture, readableCapture])
})

afterEach(() => cleanup())

async function renderLoaded() {
  render(<Documents />)
  await waitFor(() => expect(screen.getByText('resume.pdf')).toBeInTheDocument())
}

// The document names in table order.
function rowNames() {
  return screen.getAllByRole('row').slice(1).map(r => within(r).getAllByRole('cell')[1].firstChild.textContent)
}

describe('Documents: browser captures', () => {
  it('shows a capture once, titled after the page, with its source and URL', async () => {
    await renderLoaded()
    const row = screen.getByText('Jev Picker Plan').closest('tr')
    expect(within(row).getByText('Browser extension')).toBeInTheDocument()
    expect(within(row).getByText('MHTML')).toBeInTheDocument()
    expect(within(row).getByText(capture.url)).toBeInTheDocument()
    expect(screen.getAllByText('Jev Picker Plan')).toHaveLength(1)
  })

  it('opens the original page from the link button', async () => {
    await renderLoaded()
    const row = screen.getByText('Jev Picker Plan').closest('tr')
    fireEvent.click(within(row).getByTitle('Open the original page'))
    expect(WailsApp.OpenURL).toHaveBeenCalledWith(capture.url)
  })

  it('previews a readable.md capture in-app and hands an MHTML one to the OS', async () => {
    await renderLoaded()
    fireEvent.doubleClick(screen.getByText('An article').closest('tr'))
    await waitFor(() => expect(WailsApp.GetProfileDocumentText).toHaveBeenCalledWith('doc-10'))

    fireEvent.doubleClick(screen.getByText('Jev Picker Plan').closest('tr'))
    expect(WailsApp.OpenPathWithOS).toHaveBeenCalledWith(capture.path)
  })
})

describe('Documents: sorting', () => {
  it('defaults to newest first', async () => {
    await renderLoaded()
    expect(rowNames()).toEqual(['An article', 'Jev Picker Plan', 'notes.txt', 'resume.pdf'])
    expect(screen.getByRole('columnheader', { name: /Date/ })).toHaveAttribute('aria-sort', 'descending')
  })

  it('sorts by name, then flips direction on a second click', async () => {
    await renderLoaded()
    fireEvent.click(screen.getByRole('button', { name: /^Name/ }))
    expect(rowNames()).toEqual(['An article', 'Jev Picker Plan', 'notes.txt', 'resume.pdf'])
    expect(screen.getByRole('columnheader', { name: /Name/ })).toHaveAttribute('aria-sort', 'ascending')
    fireEvent.click(screen.getByRole('button', { name: /^Name/ }))
    expect(rowNames()).toEqual(['resume.pdf', 'notes.txt', 'Jev Picker Plan', 'An article'])
    expect(screen.getByRole('columnheader', { name: /Name/ })).toHaveAttribute('aria-sort', 'descending')
  })

  it('sorts by type and by source', async () => {
    await renderLoaded()
    fireEvent.click(screen.getByRole('button', { name: /^Type/ }))
    expect(rowNames()).toEqual(['An article', 'Jev Picker Plan', 'resume.pdf', 'notes.txt']) // md, mhtml, pdf, txt
    fireEvent.click(screen.getByRole('button', { name: /^Source/ }))
    expect(rowNames()).toEqual(['An article', 'Jev Picker Plan', 'notes.txt', 'resume.pdf']) // extension, folder, upload
  })

  it('remembers the sort across reloads', async () => {
    await renderLoaded()
    fireEvent.click(screen.getByRole('button', { name: /^Type/ }))
    fireEvent.click(screen.getByRole('button', { name: /^Type/ }))
    cleanup()
    await renderLoaded()
    expect(screen.getByRole('columnheader', { name: /Type/ })).toHaveAttribute('aria-sort', 'descending')
    expect(rowNames()).toEqual(['notes.txt', 'resume.pdf', 'Jev Picker Plan', 'An article'])
  })
})

describe('Documents: source filter', () => {
  it('shows only the chosen source, and select-all covers only what is shown', async () => {
    await renderLoaded()
    fireEvent.change(screen.getByLabelText('Filter by source'), { target: { value: 'extension' } })
    expect(rowNames()).toEqual(['An article', 'Jev Picker Plan'])
    fireEvent.click(screen.getByLabelText('Select all documents'))
    expect(screen.getByText('2 selected')).toBeInTheDocument()
  })
})
