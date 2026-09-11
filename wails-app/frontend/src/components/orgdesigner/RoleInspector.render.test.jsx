// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import RoleInspector from './RoleInspector.jsx'

vi.mock('../../services/api.js', () => ({
  api: { chooseInstructionsFile: vi.fn() },
}))

afterEach(() => {
  cleanup()
})

function makeNode(overrides = {}) {
  return {
    id: 'writer',
    title: 'Staff Writer',
    type: 'specialist',
    parentId: 'root',
    responsibilities: ['Draft articles', 'Incorporate feedback', 'Hand off to reviewer'],
    icon: '',
    rest: { adapter_config: { model: 'gpt-4' } },
    ...overrides,
  }
}

// Regression test for the reported bug: "the fields seem like they don't
// reflect the selected role." Root cause: RoleInspector's field-sync effect
// keys on the `node` PROP's object identity ([node] dependency), but
// OrgDesigner.jsx's load()/applyLivePatch()/refreshFromServer() all
// unconditionally build a brand-new node object (fresh reference) for every
// role on every server round-trip -- including the round-trip triggered by
// saving a *different* field on the *same* role, or an unrelated live
// org-update event -- none of which are gated by the app's own
// "user is interacting" guard (isInteractingRef only covers canvas drag,
// never inspector text fields). Result: any not-yet-blurred edit in the
// panel is silently discarded and replaced by the last-saved server value
// the moment any such refresh fires while that same role stays selected.
it('keeps an in-progress (not yet blurred) edit when the same role re-renders with a new object reference', () => {
  const nodeA = makeNode()
  const { rerender } = render(
    <RoleInspector node={nodeA} allNodes={[nodeA]} onPatch={() => {}} />,
  )

  const modelInput = screen.getByPlaceholderText('claude-sonnet-4-5')
  fireEvent.change(modelInput, { target: { value: 'claude-opus-5' } })
  expect(modelInput).toHaveValue('claude-opus-5')

  // Same role, same underlying data, but a fresh object reference -- exactly
  // what applyLivePatch/refreshFromServer produce for an "unchanged" role.
  const nodeAAgain = makeNode()
  rerender(
    <RoleInspector node={nodeAAgain} allNodes={[nodeAAgain]} onPatch={() => {}} />,
  )

  expect(modelInput).toHaveValue('claude-opus-5')
})

// Guards the behavior the fix must not regress: selecting a genuinely
// different role must still reset the panel to that role's own values.
it('resets fields when a different role becomes selected', () => {
  const nodeA = makeNode()
  const { rerender } = render(
    <RoleInspector node={nodeA} allNodes={[nodeA]} onPatch={() => {}} />,
  )
  const modelInput = screen.getByPlaceholderText('claude-sonnet-4-5')
  fireEvent.change(modelInput, { target: { value: 'unsaved-edit' } })

  const nodeB = makeNode({ id: 'editor', title: 'Editor', rest: { adapter_config: { model: 'gpt-3.5' } } })
  rerender(
    <RoleInspector node={nodeB} allNodes={[nodeB]} onPatch={() => {}} />,
  )

  expect(screen.getByPlaceholderText('claude-sonnet-4-5')).toHaveValue('gpt-3.5')
  expect(screen.getByDisplayValue('Editor')).toBeInTheDocument()
})
