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

let tops
let visible
let size

function stubLayout() {
  tops = {}
  visible = true
  size = 100
  Object.defineProperty(HTMLElement.prototype, 'offsetTop', { configurable: true, get() { return tops[this.dataset?.taskId] ?? 0 } })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get() { return size } })
  vi.spyOn(Element.prototype, 'getClientRects').mockImplementation(() => (visible ? [{}] : []))
}

function mq(matches) {
  const listeners = new Set()
  const q = {
    matches,
    addEventListener: (_, f) => listeners.add(f),
    removeEventListener: (_, f) => listeners.delete(f),
    fire(m) { q.matches = m; listeners.forEach(f => f({ matches: m })) },
    listeners,
  }
  window.matchMedia = () => q
  return q
}

afterEach(() => {
  cleanup(); vi.restoreAllMocks()
  delete Element.prototype.animate
  delete HTMLElement.prototype.offsetTop
  delete HTMLElement.prototype.clientHeight
})

describe('useFlip', () => {
  const setup = () => {
    mq(false)
    stubLayout()
    const animate = vi.fn()
    Element.prototype.animate = animate
    tops[1] = 0; tops[2] = 40
    const r = render(<Board order={['1', '2']} />)
    return { animate, ...r }
  }

  it('slides a card that moved', () => {
    const { animate, rerender } = setup()
    expect(animate).not.toHaveBeenCalled()
    tops[1] = 40; tops[2] = 0
    rerender(<Board order={['2', '1']} />)
    expect(animate).toHaveBeenCalledTimes(2)
    expect(animate.mock.calls[0][0][0].transform).toBe('translate(0px, 40px)')
  })

  it('animates nothing on a scroll-only change', () => {
    const { animate, rerender, container } = setup()
    container.firstChild.scrollTop = 300 // layout offsets do not change
    rerender(<Board order={['1', '2']} />)
    expect(animate).not.toHaveBeenCalled()
  })

  it('animates nothing when the container resized', () => {
    const { animate, rerender } = setup()
    size = 200; tops[1] = 40
    rerender(<Board order={['1', '2']} />)
    expect(animate).not.toHaveBeenCalled()
  })

  it('animates nothing on hidden -> visible', () => {
    const { animate, rerender } = setup()
    visible = false
    rerender(<Board order={['1', '2']} />)
    tops[1] = 80
    visible = true
    rerender(<Board order={['1', '2']} />)
    expect(animate).not.toHaveBeenCalled()
  })

  it('handles a removed card and still animates the survivor', () => {
    const { animate, rerender } = setup()
    tops[2] = 0
    rerender(<Board order={['2']} />)
    expect(animate).toHaveBeenCalledTimes(1)
    expect(animate.mock.calls[0][0][0].transform).toBe('translate(0px, 40px)')
  })

  it('follows prefers-reduced-motion toggled mid-session', () => {
    const q = mq(false)
    stubLayout()
    const animate = vi.fn()
    Element.prototype.animate = animate
    tops[1] = 0; tops[2] = 40
    const { rerender, unmount } = render(<Board order={['1', '2']} />)
    q.fire(true)
    tops[1] = 40; tops[2] = 0
    rerender(<Board order={['2', '1']} />)
    expect(animate).not.toHaveBeenCalled()
    q.fire(false)
    tops[1] = 0; tops[2] = 40
    rerender(<Board order={['1', '2']} />)
    expect(animate).toHaveBeenCalledTimes(2)
    unmount()
    expect(q.listeners.size).toBe(0)
  })

  it('does nothing under prefers-reduced-motion', () => {
    mq(true)
    stubLayout()
    const animate = vi.fn()
    Element.prototype.animate = animate
    tops[1] = 0; tops[2] = 40
    const { rerender } = render(<Board order={['1', '2']} />)
    tops[1] = 40; tops[2] = 0
    rerender(<Board order={['2', '1']} />)
    expect(animate).not.toHaveBeenCalled()
  })
})
