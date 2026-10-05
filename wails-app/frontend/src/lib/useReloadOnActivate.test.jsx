// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useReloadOnActivate } from './useReloadOnActivate'

describe('useReloadOnActivate', () => {
  it('reloads on re-activation only, not on mount or while active', () => {
    const reload = vi.fn()
    const { rerender } = renderHook(({ a }) => useReloadOnActivate(a, reload), { initialProps: { a: true } })
    expect(reload).not.toHaveBeenCalled()
    rerender({ a: false })
    expect(reload).not.toHaveBeenCalled()
    rerender({ a: true })
    expect(reload).toHaveBeenCalledTimes(1)
    rerender({ a: true })
    expect(reload).toHaveBeenCalledTimes(1)
  })
})
