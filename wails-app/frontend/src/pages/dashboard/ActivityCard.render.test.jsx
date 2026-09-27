// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.count != null ? `${k}:${o.count}` : k) }) }))
import ActivityCard from './ActivityCard.jsx'

afterEach(cleanup)
const summary = {
  activity: { captures_7d: 14, captures_total: 40, messages_in_7d: 22, messages_out_7d: 5,
    documents: { total: 3, indexed: 2, index_errors: 1, summarising: 1, summary_errors: 1 } },
  applications: { by_status: { pending: 4, applied: 2 }, evaluated: 3, unevaluated_pending: 1 },
  people: { added_7d: 18, lists: 2 },
  vault: { secrets: 40, images: 1, image_bytes: 100 },
}

describe('ActivityCard', () => {
  it('a section that failed shows a dash, not zero', () => {
    render(<ActivityCard summary={{ ...summary, vault: { error: 'database unavailable', secrets: 0 } }} onNavigate={vi.fn()} />)
    expect(screen.getByTitle('database unavailable')).toHaveTextContent('—')
  })
  it('renders seven tiles that navigate', () => {
    const onNavigate = vi.fn()
    render(<ActivityCard summary={summary} onNavigate={onNavigate} />)
    expect(screen.getAllByRole('button')).toHaveLength(7)
    expect(screen.getByText('dashboard.activity.docErrors:2')).toBeInTheDocument()
    fireEvent.click(screen.getByText('dashboard.activity.messages'))
    expect(onNavigate).toHaveBeenCalledWith('communications')
    fireEvent.click(screen.getByText('dashboard.activity.vault'))
    expect(onNavigate).toHaveBeenCalledWith('secretsVault')
    expect(screen.getByText('40')).toBeInTheDocument()
  })
})
