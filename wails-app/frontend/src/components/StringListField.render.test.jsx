// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'
import StringListField from './StringListField.jsx'

afterEach(() => {
  cleanup()
})

// Regression test for a real bug found via the Org Designer: selecting a
// different role whose responsibilities list is the same length as the
// previously-selected role's left stale text showing for some rows (e.g.
// row 4 kept showing a THIRD role's text after switching between two other
// roles). Root cause: each row is an uncontrolled <input defaultValue={v}>
// keyed only by its array index. `defaultValue` is applied once, when that
// DOM node is first created -- React reuses the same node across renders
// whenever a row's index still exists in the new list, so a parent-driven
// `values` prop change is silently ignored for any row whose index persists
// across the switch, even though `values` itself is fully correct.
it('shows the new list\'s text after values changes, even when the list length is unchanged', () => {
  const { rerender } = render(
    <React.StrictMode>
      <StringListField
        values={['role A item 1', 'role A item 2', 'role A item 3', 'role A item 4']}
        onChange={() => {}}
        resetKey="role-a"
      />
    </React.StrictMode>,
  )
  expect(screen.getByDisplayValue('role A item 4')).toBeInTheDocument()

  rerender(
    <React.StrictMode>
      <StringListField
        values={['role B item 1', 'role B item 2', 'role B item 3', 'role B item 4']}
        onChange={() => {}}
        resetKey="role-b"
      />
    </React.StrictMode>,
  )

  expect(screen.queryByDisplayValue('role A item 4')).not.toBeInTheDocument()
  expect(screen.getByDisplayValue('role B item 1')).toBeInTheDocument()
  expect(screen.getByDisplayValue('role B item 2')).toBeInTheDocument()
  expect(screen.getByDisplayValue('role B item 3')).toBeInTheDocument()
  expect(screen.getByDisplayValue('role B item 4')).toBeInTheDocument()
})
