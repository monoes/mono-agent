// @vitest-environment jsdom
// Renaming a key in the keys table: Enter saves, Escape cancels, the store's rules and the clash are shown in the page's
// language next to the name, and the key itself is never needed or shown.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'

const KEY = 'sk-ma-' + 'X'.repeat(43) // built at run time: no key-shaped literal in the source

const App = {}
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.changeLanguage('en')
  for (const k of ['APIKeySetContext', 'APIKeyRevoke', 'APIKeyCreate', 'APIKeyRename']) App[k] = vi.fn()
  window.go = { main: { App } }
  window.runtime = { ClipboardSetText: vi.fn().mockResolvedValue(true) }
})
afterEach(() => { cleanup(); delete window.go; delete window.runtime })

const keys = () => [
  { id: 'key_abcdefghijkl', profile_id: 'default', name: 'my-app', prefix: 'sk-ma-AbCdEf', context: false, created_at: '2026-09-20T12:00:00Z', last_used_at: '', revoked_at: '' },
  { id: 'key_mnopqrstuvwx', profile_id: 'default', name: 'notes bot', prefix: 'sk-ma-GhIjKl', context: true, created_at: '2026-10-01T12:00:00Z', last_used_at: '', revoked_at: '' },
]
const k = en.settings.api.keys

async function mount(props = {}) {
  const { default: ApiKeysBlock } = await import('./ApiKeysBlock.jsx')
  const onChanged = props.onChanged || vi.fn()
  const view = render(<ApiKeysBlock keys={keys()} err="" contextClasses={['chat-only']} onRetry={vi.fn()} {...props} onChanged={onChanged} />)
  return { onChanged, rerender: (more) => view.rerender(<ApiKeysBlock keys={keys()} err="" contextClasses={['chat-only']} onRetry={vi.fn()} {...props} {...more} onChanged={onChanged} />) }
}
const row = (name) => screen.getByRole('row', { name: new RegExp(name) })
const renameBtn = (name) => screen.getByRole('button', { name: k.renameLabel.replace('{{name}}', name) })
const input = (name) => screen.getByRole('textbox', { name: k.renameInput.replace('{{name}}', name) })
const saveBtn = (name) => screen.getByRole('button', { name: k.renameSaveLabel.replace('{{name}}', name) })
const cancelBtn = (name) => screen.getByRole('button', { name: k.renameCancelLabel.replace('{{name}}', name) })
const typeInto = (el, value) => fireEvent.change(el, { target: { value } })
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)) })

describe('renaming a key: starting', () => {
  it('has a button for each key, named for it, and opens the name in a field the keyboard is in, with the rule beneath it', async () => {
    await mount()
    expect(screen.getAllByRole('button', { name: /^Rename / })).toHaveLength(2)
    fireEvent.click(renameBtn('my-app'))
    const field = input('my-app')
    expect(field).toHaveValue('my-app')
    expect(field).toHaveFocus()
    expect([field.selectionStart, field.selectionEnd]).toEqual([0, 'my-app'.length]) // the name is selected: typing replaces it
    expect(field).toHaveAttribute('maxlength', '64')
    expect(field).toHaveAccessibleDescription(en.settings.api.create.nameHint)
    expect(saveBtn('my-app')).toBeEnabled()
    expect(cancelBtn('my-app')).toBeEnabled()
    expect(screen.queryByRole('button', { name: k.renameLabel.replace('{{name}}', 'my-app') })).not.toBeInTheDocument() // it is being renamed
    expect(within(row('notes bot')).queryByRole('textbox')).not.toBeInTheDocument() // the other key is not
  })

  it('edits one key at a time: starting on another closes the first', async () => {
    await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'half typed')
    fireEvent.click(renameBtn('notes bot'))
    expect(screen.queryByRole('textbox', { name: k.renameInput.replace('{{name}}', 'my-app') })).not.toBeInTheDocument()
    expect(input('notes bot')).toHaveValue('notes bot')
    expect(renameBtn('my-app')).toBeInTheDocument()
    expect(within(row('my-app')).getByText('my-app')).toBeInTheDocument() // the name it had: nothing was saved
  })
})

describe('renaming a key: with the keyboard', () => {
  it('saves with Enter: the CLI is asked for the new name, the list is read again, and the keyboard goes back to the button', async () => {
    App.APIKeyRename.mockResolvedValue({ id: 'key_abcdefghijkl', name: 'renamed', key: KEY }) // a key in the answer would show
    const { onChanged, rerender } = await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), '  renamed ')
    fireEvent.keyDown(input('my-app'), { key: 'Enter' })
    await waitFor(() => expect(App.APIKeyRename).toHaveBeenCalledWith('key_abcdefghijkl', 'renamed')) // as the create does: without the padding
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())
    rerender({ keys: [{ ...keys()[0], name: 'renamed' }, keys()[1]] })
    expect(within(row('renamed')).getByText('renamed')).toBeInTheDocument()
    await waitFor(() => expect(renameBtn('renamed')).toHaveFocus())
    expect(App.APIKeySetContext).not.toHaveBeenCalled() // the context switch is not what is being changed
    expect(document.body.textContent).not.toContain(KEY)
  })

  it('cancels with Escape: nothing is asked of the CLI, the name is as it was, and the keyboard goes back to the button', async () => {
    const { onChanged } = await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'something else')
    const seen = vi.fn()
    window.addEventListener('keydown', seen) // what the chat panel does: it stops a running turn on an Escape that gets this far
    const esc = fireEvent.keyDown(input('my-app'), { key: 'Escape' })
    window.removeEventListener('keydown', seen)
    expect(esc).toBe(false) // it was taken
    expect(seen).not.toHaveBeenCalled() // and the page behind does not see it
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())
    expect(App.APIKeyRename).not.toHaveBeenCalled()
    expect(onChanged).not.toHaveBeenCalled()
    expect(within(row('my-app')).getByText('my-app')).toBeInTheDocument()
    await waitFor(() => expect(renameBtn('my-app')).toHaveFocus())
    // and the next time it starts from the name again, not from what was typed
    fireEvent.click(renameBtn('my-app'))
    expect(input('my-app')).toHaveValue('my-app')
  })

  it('saves with its button and cancels with its button', async () => {
    App.APIKeyRename.mockResolvedValue({})
    await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'renamed')
    fireEvent.click(saveBtn('my-app'))
    await waitFor(() => expect(App.APIKeyRename).toHaveBeenCalledWith('key_abcdefghijkl', 'renamed'))
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())

    fireEvent.click(renameBtn('notes bot'))
    typeInto(input('notes bot'), 'x')
    fireEvent.click(cancelBtn('notes bot'))
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(App.APIKeyRename).toHaveBeenCalledTimes(1)
  })

  it('closes without asking the CLI when the name is the one it had, and does not save an empty one', async () => {
    const { onChanged } = await mount()
    fireEvent.click(renameBtn('my-app'))
    fireEvent.keyDown(input('my-app'), { key: 'Enter' }) // nothing was changed
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())
    expect(App.APIKeyRename).not.toHaveBeenCalled()
    expect(onChanged).not.toHaveBeenCalled()

    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), '   ')
    expect(saveBtn('my-app')).toBeDisabled()
    fireEvent.keyDown(input('my-app'), { key: 'Enter' })
    await settle()
    expect(App.APIKeyRename).not.toHaveBeenCalled()
    expect(input('my-app')).toBeInTheDocument() // still open: it is not cancelled by a mistake
  })

  it('does not take the Enter of a text being composed for a save', async () => {
    await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'renamed')
    fireEvent.keyDown(input('my-app'), { key: 'Enter', isComposing: true })
    await settle()
    expect(App.APIKeyRename).not.toHaveBeenCalled()
  })

  it('reaches the name, then its field, Save and Cancel, in that order', async () => {
    await mount()
    fireEvent.click(renameBtn('my-app'))
    const all = [...document.querySelectorAll('input, button, [role="switch"]')]
    const idx = [input('my-app'), saveBtn('my-app'), cancelBtn('my-app')].map(el => all.indexOf(el))
    expect(idx).toEqual([...idx].sort((a, b) => a - b))
    expect(idx[1]).toBe(idx[0] + 1)
    expect(idx[2]).toBe(idx[1] + 1)
  })
})

describe('renaming a key: when the CLI says no', () => {
  const fail = async (message) => {
    App.APIKeyRename.mockRejectedValue(new Error(message))
    const m = await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'notes bot')
    fireEvent.keyDown(input('my-app'), { key: 'Enter' })
    return m
  }

  it('says a name that is taken in the page\'s words, next to the field, keeps what was typed and gives the keyboard back to the field', async () => {
    const { onChanged } = await fail('invalid_input: an active key with that name already exists in this profile')
    const cell = within(row('my-app'))
    expect(await cell.findByRole('alert')).toHaveTextContent(en.settings.api.errors.nameTaken)
    expect(input('my-app')).toHaveValue('notes bot')
    await waitFor(() => expect(input('my-app')).toHaveFocus())
    expect(saveBtn('my-app')).toBeEnabled() // it can be corrected
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1)) // a failed call may have been done: the list is read again
    expect(screen.getAllByRole('alert')).toHaveLength(1)
  })

  it('gives the keyboard back to the field when the call was started from the Save button, which the call held', async () => {
    App.APIKeyRename.mockRejectedValue(new Error('invalid_input: an active key with that name already exists in this profile'))
    await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'notes bot')
    saveBtn('my-app').focus()
    fireEvent.click(saveBtn('my-app'))
    await within(row('my-app')).findByRole('alert')
    await waitFor(() => expect(input('my-app')).toHaveFocus())
  })

  it('says a name the store does not allow, in the page\'s words', async () => {
    await fail("invalid_input: key name must be 1-64 characters: letters, digits, space, '.', '_' or '-', starting with a letter or digit, not the shape of a key id (key_ followed by 12 characters from a-z and 2-7), and not holding sk-ma-, the start of every API key")
    expect(await within(row('my-app')).findByRole('alert')).toHaveTextContent(en.settings.api.errors.nameInvalid)
  })

  it('says what the CLI said when the page has no words for it, and says in Spanish that those are English', async () => {
    await act(() => i18n.changeLanguage('es'))
    App.APIKeyRename.mockRejectedValue(new Error('database is locked'))
    await mount()
    fireEvent.click(screen.getByRole('button', { name: es.settings.api.keys.renameLabel.replace('{{name}}', 'my-app') }))
    typeInto(screen.getByRole('textbox', { name: es.settings.api.keys.renameInput.replace('{{name}}', 'my-app') }), 'x')
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    const alert = await within(row('my-app')).findByRole('alert')
    expect(alert).toHaveTextContent('database is locked')
    expect(within(alert).getByText('database is locked')).toHaveAttribute('lang', 'en')
  })

  it('closes the field and says the key is gone, at the top as the other key failures are, when it no longer exists', async () => {
    App.APIKeyRename.mockRejectedValue(new Error('not_found: api key not found'))
    const { onChanged } = await mount()
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'x')
    fireEvent.keyDown(input('my-app'), { key: 'Enter' })
    expect(await screen.findByRole('alert')).toHaveTextContent(en.settings.api.errors.keyNotFound)
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())
    expect(onChanged).toHaveBeenCalledTimes(1)
  })
})

describe('renaming a key: while the CLI works', () => {
  it('holds every control still, the field read-only so that the keyboard stays in it, and starts one call', async () => {
    let release
    App.APIKeyRename.mockReturnValue(new Promise(r => { release = r }))
    const onChanged = vi.fn(() => new Promise(r => { release.reload = r }))
    await mount({ onChanged })
    fireEvent.click(renameBtn('my-app'))
    typeInto(input('my-app'), 'renamed')
    act(() => { fireEvent.keyDown(input('my-app'), { key: 'Enter' }); fireEvent.keyDown(input('my-app'), { key: 'Enter' }) }) // twice, in one tick
    expect(App.APIKeyRename).toHaveBeenCalledTimes(1)
    expect(input('my-app')).toHaveAttribute('readonly')
    expect(input('my-app')).toHaveFocus()
    expect(saveBtn('my-app')).toBeDisabled()
    expect(cancelBtn('my-app')).toBeDisabled()
    expect(within(row('notes bot')).getByRole('switch')).toBeDisabled()
    expect(renameBtn('notes bot')).toBeDisabled()
    expect(within(row('notes bot')).getByRole('button', { name: /^Revoke / })).toBeDisabled()
    // Escape does not cancel a call that is running
    fireEvent.keyDown(input('my-app'), { key: 'Escape' })
    expect(input('my-app')).toBeInTheDocument()

    await act(async () => { release({}) })
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    expect(renameBtn('notes bot')).toBeDisabled() // the list is still being read: it is stale until then
    await act(async () => { release.reload() })
    await waitFor(() => expect(renameBtn('notes bot')).toBeEnabled())
  })
})

describe('renaming a key: in Spanish', () => {
  it('words the buttons and the field by the key\'s name', async () => {
    await act(() => i18n.changeLanguage('es'))
    const s = es.settings.api.keys
    await mount()
    expect(screen.getByRole('button', { name: s.renameLabel.replace('{{name}}', 'my-app') })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: s.renameLabel.replace('{{name}}', 'my-app') }))
    expect(screen.getByRole('textbox', { name: s.renameInput.replace('{{name}}', 'my-app') })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: s.renameSaveLabel.replace('{{name}}', 'my-app') })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: s.renameCancelLabel.replace('{{name}}', 'my-app') })).toBeInTheDocument()
    expect(s.renameLabel).not.toBe(k.renameLabel)
  })
})
