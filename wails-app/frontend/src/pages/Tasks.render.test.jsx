// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '../i18n.js'

const { api } = vi.hoisted(() => ({
  api: { board: vi.fn(), show: vi.fn(), add: vi.fn(), edit: vi.fn(), move: vi.fn(), approve: vi.fn(), archive: vi.fn(), comment: vi.fn(), agentShell: vi.fn() },
}))
vi.mock('../services/tasks.js', () => ({ tasksApi: api, onTasksChanged: () => () => {} }))
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
})
