// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '../i18n.js'

const { api, bus } = vi.hoisted(() => ({
  bus: { fire: () => {} },
  api: { board: vi.fn(), show: vi.fn(), add: vi.fn(), edit: vi.fn(), move: vi.fn(), approve: vi.fn(), archive: vi.fn(), comment: vi.fn(), agentShell: vi.fn() },
}))
vi.mock('../services/tasks.js', () => ({ tasksApi: api, onTasksChanged: (f) => { bus.fire = f; return () => {} } }))
import Tasks from './Tasks.jsx'
import TaskDrawer from '../components/TaskDrawer.jsx'

const card = (id, extra = {}) => ({ id, title: `Task ${id}`, notes: '', position: id, claim: null, ...extra })
const doc = () => ({ rev: 1, counts: {}, tasks: { inbox: [card(1), card(2, { notes: 'needle' })], ready: [card(3)] } })

beforeEach(() => {
  Object.values(api).forEach(f => f.mockReset())
  api.board.mockResolvedValue(doc())
  api.agentShell.mockResolvedValue('')
  api.show.mockResolvedValue({ task: card(1), events: [{ id: 9, actor: 'you', kind: 'created', note: '' }] })
  api.move.mockResolvedValue({ id: 1 })
  api.approve.mockResolvedValue({ id: 1 })
  api.add.mockResolvedValue({ id: 4 })
  api.edit.mockResolvedValue({ id: 1 })
})
afterEach(cleanup)

describe('Tasks page', () => {
  it('shows the five columns with their cards', async () => {
    render(<Tasks />)
    const inbox = await screen.findByRole('region', { name: 'Inbox' })
    expect(within(inbox).getByText('Task 1')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Ready' })).toContainElement(screen.getByText('Task 3'))
    expect(screen.getByRole('region', { name: 'Done' })).toBeInTheDocument()
  })

  it('filters cards by the search box', async () => {
    render(<Tasks />)
    await screen.findByText('Task 1')
    fireEvent.change(screen.getByLabelText('Search tasks'), { target: { value: 'needle' } })
    expect(screen.queryByText('Task 1')).not.toBeInTheDocument()
    expect(screen.getByText('Task 2')).toBeInTheDocument()
  })

  it('approves an Inbox card and moves one with Shift+ArrowRight', async () => {
    render(<Tasks />)
    fireEvent.click(await screen.findByRole('button', { name: 'Approve to Ready: Task 1' }))
    await waitFor(() => expect(api.approve).toHaveBeenCalledWith([1], false))
    fireEvent.keyDown(screen.getByRole('button', { name: 'Task 2' }), { key: 'ArrowRight', shiftKey: true })
    await waitFor(() => expect(api.move).toHaveBeenCalledWith(2, 'ready', { where: '' }))
  })

  it('keeps the focus on a card moved to another column by key', async () => {
    api.move.mockImplementation(async () => {
      api.board.mockResolvedValue({ rev: 2, counts: {}, tasks: { inbox: [card(1)], ready: [card(2, { notes: 'needle' }), card(3)] } })
      return { id: 2 }
    })
    render(<Tasks />)
    fireEvent.keyDown(await screen.findByRole('button', { name: 'Task 2' }), { key: 'ArrowRight', shiftKey: true })
    const ready = await screen.findByRole('region', { name: 'Ready' })
    await waitFor(() => expect(within(ready).getByRole('button', { name: 'Task 2' })).toHaveFocus())
  })

  it('closes the drawer on Escape, but only leaves a field it is typed in', async () => {
    render(<Tasks />)
    fireEvent.click(await screen.findByText('Task 1'))
    const drawer = await screen.findByRole('dialog')
    const title = within(drawer).getByLabelText('Title')
    title.focus()
    fireEvent.keyDown(title, { key: 'Escape' })
    expect(title).not.toHaveFocus()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    fireEvent.keyDown(document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('quick-adds to Ready straight into Ready', async () => {
    render(<Tasks />)
    const ready = await screen.findByRole('region', { name: 'Ready' })
    fireEvent.change(within(ready).getByLabelText('New task title'), { target: { value: 'Ship it' } })
    fireEvent.click(within(ready).getByText('Add to Ready'))
    await waitFor(() => expect(api.add).toHaveBeenCalledWith({ title: 'Ship it', ready: true }))
  })

  it('shows a refusal and puts the card back', async () => {
    api.move.mockResolvedValue({ error: 'nope', code: 'x' })
    render(<Tasks />)
    fireEvent.keyDown(await screen.findByRole('button', { name: 'Task 2' }), { key: 'ArrowRight', shiftKey: true })
    expect(await screen.findByText('Refused: nope')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Inbox' })).getByText('Task 2')).toBeInTheDocument()
  })

  it('opens the drawer, saves an edit and sends a comment', async () => {
    render(<Tasks />)
    fireEvent.click(await screen.findByText('Task 1'))
    const drawer = await screen.findByRole('dialog')
    await within(drawer).findByText('created')
    fireEvent.change(within(drawer).getByLabelText('Title'), { target: { value: 'Renamed' } })
    fireEvent.click(within(drawer).getByText('Save'))
    await waitFor(() => expect(api.edit).toHaveBeenCalledWith(1, { title: 'Renamed' }))
    fireEvent.change(within(drawer).getByLabelText('Add a comment'), { target: { value: 'hi' } })
    fireEvent.click(within(drawer).getByText('Comment'))
    await waitFor(() => expect(api.comment).toHaveBeenCalledWith(1, 'hi'))
  })

  it('is read-only when the app inherited an agent marker', async () => {
    api.agentShell.mockResolvedValue('monomind')
    render(<Tasks />)
    await screen.findByText(/Read-only/)
    expect(screen.queryByLabelText('New task title')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Archive/ })).not.toBeInTheDocument()
  })

  it('stays read-only until the agent shell answers', async () => {
    let answer
    api.agentShell.mockReturnValue(new Promise(r => { answer = r }))
    render(<Tasks />)
    await screen.findByText('Task 1')
    expect(screen.queryByLabelText('New task title')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Archive/ })).not.toBeInTheDocument()
    answer('')
    expect(await screen.findAllByLabelText('New task title')).not.toHaveLength(0)
  })

  it('keeps unsaved typing in the drawer when the task changes remotely', async () => {
    const { rerender } = render(<TaskDrawer task={card(1, { notes: 'a' })} onClose={() => {}} onEdit={() => {}} onComment={() => {}} />)
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Mine' } })
    rerender(<TaskDrawer task={card(1, { title: 'Theirs', notes: 'b' })} onClose={() => {}} onEdit={() => {}} onComment={() => {}} />)
    expect(screen.getByLabelText('Title')).toHaveValue('Mine') // dirty: kept
    expect(screen.getByLabelText('Notes')).toHaveValue('b') // untouched: follows the remote
    rerender(<TaskDrawer task={card(2)} onClose={() => {}} onEdit={() => {}} onComment={() => {}} />)
    expect(screen.getByLabelText('Title')).toHaveValue('Task 2') // another card resets
  })

  it('reports a board that cannot be read', async () => {
    api.board.mockResolvedValue({ error: 'no database' })
    render(<Tasks />)
    expect(await screen.findByRole('alert')).toHaveTextContent('no database')
  })

  it('shows a claim whose lease ended as stale even though the store did not say so', async () => {
    api.board.mockResolvedValue({ rev: 1, counts: {}, tasks: { in_progress: [
      card(6, { claim: { by: 'bot', until: '2000-01-01T00:00:00Z', stale: false } }),
      card(7, { claim: { by: 'bot', until: '2999-01-01T00:00:00Z', stale: false } }),
    ] } })
    render(<Tasks />)
    await screen.findByText('Task 6')
    expect(screen.getAllByText('Stale claim')).toHaveLength(1)
    expect(screen.getByText('Claimed by bot')).toBeInTheDocument()
  })

  describe('pulses', () => {
    const reviewDoc = () => ({ rev: 1, counts: {}, tasks: { review: [card(5)], in_progress: [card(6)] } })
    const matchMedia = (matches) => { window.matchMedia = () => ({ matches, addEventListener() {}, removeEventListener() {} }) }
    const moveToDone = async () => {
      api.board.mockResolvedValue(reviewDoc())
      api.move.mockImplementation(async () => {
        api.board.mockResolvedValue({ rev: 2, counts: {}, tasks: { done: [card(5)], in_progress: [card(6)] } })
        return { id: 5 }
      })
      render(<Tasks />)
      fireEvent.keyDown(await screen.findByRole('button', { name: 'Task 5' }), { key: 'ArrowRight', shiftKey: true })
      const done = await screen.findByRole('region', { name: 'Done' })
      return (await within(done).findByRole('button', { name: 'Task 5' })).closest('[data-task-id]')
    }
    afterEach(() => { delete window.matchMedia })

    it('glows a card that was moved to Done', async () => {
      matchMedia(false)
      expect(await moveToDone()).toHaveClass('tb-card--done-pulse')
    })

    it('pulses a card an agent claimed, and only that card', async () => {
      matchMedia(false)
      api.board.mockResolvedValue(reviewDoc())
      render(<Tasks />)
      await screen.findByText('Task 6')
      expect(screen.getByRole('button', { name: 'Task 6' }).closest('[data-task-id]')).not.toHaveClass('tb-card--claim-pulse')
      api.board.mockResolvedValue({ ...reviewDoc(), rev: 2, tasks: { review: [card(5)], in_progress: [card(6, { claim: { by: 'bot', until: '2099-01-01T00:00:00Z' } })] } })
      bus.fire()
      await waitFor(() => expect(screen.getByRole('button', { name: 'Task 6' }).closest('[data-task-id]')).toHaveClass('tb-card--claim-pulse'))
      expect(screen.getByRole('button', { name: 'Task 5' }).closest('[data-task-id]')).not.toHaveClass('tb-card--claim-pulse')
    })

    it('makes no pulse under reduced motion', async () => {
      matchMedia(true)
      expect(await moveToDone()).not.toHaveClass('tb-card--done-pulse')
    })
  })

  describe('card content', () => {
    it('shows the source chip, age, #id, lease countdown and the Question tag', async () => {
      const ago = new Date(Date.now() - 3 * 3600 * 1000).toISOString()
      const until = new Date(Date.now() + 12 * 60000 + 30000).toISOString()
      api.board.mockResolvedValue({ rev: 1, counts: {}, tasks: {
        in_progress: [card(6, { created_at: ago, source: { kind: 'chrome', url: 'https://www.example.com/x' }, claim: { by: 'bot', until } })],
        review: [card(7, { created_at: ago, last_event: { kind: 'question', actor: 'bot' } }), card(8, { last_event: { kind: 'result', actor: 'bot' } })],
      } })
      render(<Tasks />)
      await screen.findByText('Task 6')
      const c6 = screen.getByText('Task 6').closest('[data-task-id]')
      expect(within(c6).getByText('example.com')).toBeInTheDocument()
      expect(within(c6).getByTestId('age')).toHaveTextContent('3h')
      expect(within(c6).getByText('#6')).toBeInTheDocument()
      expect(within(c6).getByTestId('lease')).toHaveTextContent('12m left')
      expect(within(screen.getByText('Task 7').closest('[data-task-id]')).getByTestId('tag')).toHaveTextContent('Question')
      expect(within(screen.getByText('Task 8').closest('[data-task-id]')).getByTestId('tag')).toHaveTextContent('Result')
    })

    it('says a lease that ended, in Spanish too', async () => {
      const until = new Date(Date.now() - 4 * 60000 - 5000).toISOString()
      api.board.mockResolvedValue({ rev: 1, counts: {}, tasks: { in_progress: [card(6, { claim: { by: 'bot', until } })] } })
      render(<Tasks />)
      await screen.findByText('Task 6')
      expect(screen.getByTestId('lease')).toHaveTextContent('ended 4m ago')
      const en = (await import('../locales/en.json')).default.tasks
      const es = (await import('../locales/es.json')).default.tasks
      for (const k of ['left', 'ended', 'leaseEnded', 'archiveFailed', 'archiving']) expect(es[k]).toBeTruthy()
      expect(Object.keys(es.tag)).toEqual(Object.keys(en.tag))
      expect(Object.keys(es.age)).toEqual(Object.keys(en.age))
      expect(Object.keys(es.source)).toEqual(Object.keys(en.source))
    })
  })

  describe('structure and keys', () => {
    it('has no control nested in another, and the card itself is not a button', async () => {
      render(<Tasks />)
      await screen.findByText('Task 1')
      const c = screen.getByText('Task 1').closest('[data-task-id]')
      expect(c).not.toHaveAttribute('role')
      expect(c).not.toHaveAttribute('tabindex')
      for (const btn of screen.getAllByRole('button')) expect(btn.parentElement.closest('button, [role="button"]')).toBeNull()
    })

    it('opens the card from the title with Enter and moves it with the keys', async () => {
      render(<Tasks />)
      const title = await screen.findByRole('button', { name: 'Task 2' })
      fireEvent.keyDown(title, { key: 'Enter' })
      expect(await screen.findByRole('dialog')).toBeInTheDocument()
      fireEvent.keyDown(title, { key: 'ArrowRight', shiftKey: true })
      await waitFor(() => expect(api.move).toHaveBeenCalled())
    })
  })

  describe('archive from the drawer', () => {
    it('keeps the drawer open until the CLI says yes, then closes it', async () => {
      let answer
      api.archive.mockReturnValue(new Promise(r => { answer = r }))
      render(<Tasks />)
      fireEvent.click(await screen.findByText('Task 1'))
      const drawer = await screen.findByRole('dialog')
      fireEvent.click(within(drawer).getByText('Archive'))
      await waitFor(() => expect(api.archive).toHaveBeenCalledWith([1]))
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      api.board.mockResolvedValue({ rev: 2, counts: {}, tasks: { inbox: [card(2)], ready: [card(3)] } })
      answer({ id: 1 })
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    })

    it('keeps the drawer and shows the error when the archive is refused', async () => {
      api.archive.mockResolvedValue({ error: 'not allowed', code: 'x' })
      render(<Tasks />)
      fireEvent.click(await screen.findByText('Task 1'))
      const drawer = await screen.findByRole('dialog')
      fireEvent.click(within(drawer).getByText('Archive'))
      expect(await within(await screen.findByRole('dialog')).findByText('Could not archive: not allowed')).toBeInTheDocument()
      expect(within(screen.getByRole('region', { name: 'Inbox' })).getByText('Task 1')).toBeInTheDocument()
    })
  })

  describe('hidden window', () => {
    const setVisibility = (v) => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => v === 'hidden' })
      document.dispatchEvent(new Event('visibilitychange'))
    }
    afterEach(() => { delete document.hidden })

    it('does not read on a change while hidden, reads once when shown, and pauses pulses', async () => {
      render(<Tasks />)
      await screen.findByText('Task 1')
      const root = screen.getByText('Tasks').closest('.page')
      act(() => setVisibility('hidden'))
      expect(root).toHaveClass('tb-paused')
      const reads = api.board.mock.calls.length
      act(() => { bus.fire(); bus.fire() })
      expect(api.board.mock.calls.length).toBe(reads)
      act(() => setVisibility('visible'))
      await waitFor(() => expect(api.board.mock.calls.length).toBe(reads + 1))
      expect(root).not.toHaveClass('tb-paused')
    })
  })
})
