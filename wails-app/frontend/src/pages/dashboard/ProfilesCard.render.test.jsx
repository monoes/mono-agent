// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k, o) => (o?.name ? `${k}:${o.name}` : k) }) }))
import ProfilesCard from './ProfilesCard.jsx'

afterEach(cleanup)
const summary = { profiles: [
  { id: 'default', name: 'Default', current: true, workflows_active: 2, running: 1, failed_24h: 0, waiting_for_you: 3 },
  { id: 'p-work', name: 'Work', current: false, workflows_active: 5, running: 0, failed_24h: 2, waiting_for_you: 0 },
] }

describe('ProfilesCard', () => {
  it('lists every profile, marks the current one, and switches to another', () => {
    const onSwitch = vi.fn()
    render(<ProfilesCard summary={summary} onSwitch={onSwitch} />)
    expect(screen.getByText('Default')).toBeInTheDocument()
    expect(screen.getByText('dashboard.profiles.current')).toBeInTheDocument()
    const switches = screen.getAllByRole('button', { name: /dashboard.profiles.switch/ })
    expect(switches).toHaveLength(1)
    fireEvent.click(switches[0])
    expect(onSwitch).toHaveBeenCalledWith('p-work')
  })
  it('shows unavailable for a profile whose sections failed', () => {
    render(<ProfilesCard summary={{ profiles: [{ id: 'x', name: 'X', error: 'executions: locked' }] }} onSwitch={vi.fn()} />)
    expect(screen.getByTitle('executions: locked')).toHaveTextContent('dashboard.unavailable')
  })
})
