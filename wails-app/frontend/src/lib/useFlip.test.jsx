// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { useRef } from 'react'
import { useFlip } from './useFlip.js'

function Board({ order }) {
  const ref = useRef(null)
  useFlip(ref)
  return <div ref={ref}>{order.map(id => <div key={id} data-task-id={id} />)}</div>
}

function stubRects(tops) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function () {
    const top = tops[this.dataset?.taskId] ?? 0
    return { left: 0, top, right: 0, bottom: 0, width: 0, height: 0 }
  })
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); delete Element.prototype.animate })

describe('useFlip', () => {
  it('slides a card that moved', () => {
    window.matchMedia = () => ({ matches: false })
    const animate = vi.fn()
    Element.prototype.animate = animate
    const tops = { 1: 0, 2: 40 }
    stubRects(tops)
    const { rerender } = render(<Board order={['1', '2']} />)
    expect(animate).not.toHaveBeenCalled()
    tops[1] = 40; tops[2] = 0
    rerender(<Board order={['2', '1']} />)
    expect(animate).toHaveBeenCalledTimes(2)
    expect(animate.mock.calls[0][0][0].transform).toBe('translate(0px, 40px)')
  })

  it('does nothing under prefers-reduced-motion', () => {
    window.matchMedia = () => ({ matches: true })
    const animate = vi.fn()
    Element.prototype.animate = animate
    const tops = { 1: 0, 2: 40 }
    stubRects(tops)
    const { rerender } = render(<Board order={['1', '2']} />)
    tops[1] = 40; tops[2] = 0
    rerender(<Board order={['2', '1']} />)
    expect(animate).not.toHaveBeenCalled()
  })
})
