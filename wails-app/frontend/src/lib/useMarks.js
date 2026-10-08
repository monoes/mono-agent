// Lasting marks for the board's two pulses: a card that just reached Done
// glows, a card an agent just claimed pulses softly. A mark starts on the
// first board that shows the change and lasts its own time whatever boards
// follow, so the read that confirms an optimistic move does not cut it short.
// Under prefers-reduced-motion (followed live) no mark is made at all.
import { useEffect, useRef, useState } from 'react'
import { transitions } from './taskModel.js'
import { useReducedMotion } from './useFlip.js'

export const DONE_MS = 1000
export const CLAIM_MS = 2200
const NONE = new Set()

export function useMarks(board) {
  const reduced = useReducedMotion()
  const prev = useRef(null)
  const timers = useRef(new Set())
  const [marks, setMarks] = useState({ done: NONE, claimed: NONE })

  useEffect(() => () => { timers.current.forEach(clearTimeout); timers.current.clear() }, [])

  useEffect(() => {
    const before = prev.current
    prev.current = board
    if (reduced.current) return
    const { done, claimed } = transitions(before, board)
    const start = (kind, ids, ms) => {
      if (!ids.size) return
      setMarks(m => ({ ...m, [kind]: new Set([...m[kind], ...ids]) }))
      const timer = setTimeout(() => {
        timers.current.delete(timer)
        setMarks(m => ({ ...m, [kind]: new Set([...m[kind]].filter(id => !ids.has(id))) }))
      }, ms)
      timers.current.add(timer)
    }
    start('done', done, DONE_MS)
    start('claimed', claimed, CLAIM_MS)
  }, [board, reduced])

  return marks
}
