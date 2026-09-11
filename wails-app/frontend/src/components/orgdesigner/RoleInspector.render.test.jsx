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

// Wrapped in StrictMode throughout -- this app is run via `wails dev`
// (development), which mounts every component under StrictMode, and this
// exact file already caught one bug (OrgsPanel) that only reproduced there.
function renderInspector(props) {
  return render(
    <React.StrictMode>
      <RoleInspector {...props} />
    </React.StrictMode>,
  )
}
function rerenderInspector(rerender, props) {
  return rerender(
    <React.StrictMode>
      <RoleInspector {...props} />
    </React.StrictMode>,
  )
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
  const { rerender } = renderInspector({ node: nodeA, allNodes: [nodeA], onPatch: () => {} })

  const modelInput = screen.getByPlaceholderText('claude-sonnet-4-5')
  fireEvent.change(modelInput, { target: { value: 'claude-opus-5' } })
  expect(modelInput).toHaveValue('claude-opus-5')

  // Same role, same underlying data, but a fresh object reference -- exactly
  // what applyLivePatch/refreshFromServer produce for an "unchanged" role.
  const nodeAAgain = makeNode()
  rerenderInspector(rerender, { node: nodeAAgain, allNodes: [nodeAAgain], onPatch: () => {} })

  expect(modelInput).toHaveValue('claude-opus-5')
})

// Guards the behavior the fix must not regress: selecting a genuinely
// different role must still reset the panel to that role's own values.
it('resets fields when a different role becomes selected', () => {
  const nodeA = makeNode()
  const { rerender } = renderInspector({ node: nodeA, allNodes: [nodeA], onPatch: () => {} })
  const modelInput = screen.getByPlaceholderText('claude-sonnet-4-5')
  fireEvent.change(modelInput, { target: { value: 'unsaved-edit' } })

  const nodeB = makeNode({ id: 'editor', title: 'Editor', rest: { adapter_config: { model: 'gpt-3.5' } } })
  rerenderInspector(rerender, { node: nodeB, allNodes: [nodeB], onPatch: () => {} })

  expect(screen.getByPlaceholderText('claude-sonnet-4-5')).toHaveValue('gpt-3.5')
  expect(screen.getByDisplayValue('Editor')).toBeInTheDocument()
})

// Regression test for a second, distinct bug found via manual testing of the
// fix above: title/id/label correctly updated on role switch, but
// Responsibilities did not, because it's rendered by StringListField as
// uncontrolled <input defaultValue> rows keyed only by array index --
// `defaultValue` only applies when a row's DOM node is first created, so
// switching to another role whose list is the same length reuses the same
// row nodes and leaves them showing the previous role's text. Fixed by
// threading a `syncedId` (set inside the same effect/batch as the
// responsibilities/tool-policy array state itself, NOT read directly from
// node.id at the call site -- node.id changes one render before those
// arrays catch up, which forces the remount too early and misses the
// correct data) through to StringListField's `resetKey`, so every row
// remounts -- and reapplies defaultValue from the right data -- in the same
// render the new role's arrays actually land.
it('updates responsibilities text when switching to a role with a same-length list', () => {
  const nodeA = makeNode({
    id: 'researcher',
    responsibilities: ['Verify claims', 'Produce findings', 'Identify audiences', 'Flag risks'],
  })
  const { rerender } = renderInspector({ node: nodeA, allNodes: [nodeA], onPatch: () => {} })
  expect(screen.getByDisplayValue('Flag risks')).toBeInTheDocument()

  const nodeB = makeNode({
    id: 'narrative-lead',
    title: 'Narrative Lead',
    responsibilities: ['Draft hooks', 'Draft posts', 'Design prompts', 'Hand off copy'],
  })
  rerenderInspector(rerender, { node: nodeB, allNodes: [nodeB], onPatch: () => {} })

  expect(screen.queryByDisplayValue('Flag risks')).not.toBeInTheDocument()
  expect(screen.getByDisplayValue('Draft hooks')).toBeInTheDocument()
  expect(screen.getByDisplayValue('Draft posts')).toBeInTheDocument()
  expect(screen.getByDisplayValue('Design prompts')).toBeInTheDocument()
  expect(screen.getByDisplayValue('Hand off copy')).toBeInTheDocument()
})
