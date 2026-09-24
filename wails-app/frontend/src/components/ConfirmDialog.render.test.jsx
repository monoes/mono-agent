// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import ConfirmHost, { confirm } from './ConfirmDialog.jsx'

afterEach(cleanup)

describe('ConfirmDialog', () => {
  // Enter on a focused Cancel used to confirm: the window-level Enter
  // handler answered yes before the button's own click.
  it('Enter on Cancel cancels; Enter on the confirm button confirms', async () => {
    render(<ConfirmHost />)
    let answer
    act(() => { confirm('Run it?', { confirmLabel: 'Run' }).then(v => { answer = v }) })
    const cancel = await screen.findByText('Cancel')
    cancel.focus()
    await act(async () => { fireEvent.keyDown(window, { key: 'Enter' }); fireEvent.click(cancel) })
    expect(answer).toBe(false)

    act(() => { confirm('Run it?', { confirmLabel: 'Run' }).then(v => { answer = v }) })
    const run = await screen.findByText('Run')
    run.focus()
    await act(async () => { fireEvent.keyDown(window, { key: 'Enter' }); fireEvent.click(run) })
    expect(answer).toBe(true)
  })

  it('Enter with no button focused confirms, Escape cancels', async () => {
    render(<ConfirmHost />)
    let answer
    act(() => { confirm('Run it?').then(v => { answer = v }) })
    await screen.findByText('Cancel')
    document.activeElement?.blur?.()
    await act(async () => { fireEvent.keyDown(window, { key: 'Enter' }) })
    expect(answer).toBe(true)
    act(() => { confirm('Again?').then(v => { answer = v }) })
    await screen.findByText('Cancel')
    await act(async () => { fireEvent.keyDown(window, { key: 'Escape' }) })
    expect(answer).toBe(false)
  })
})
