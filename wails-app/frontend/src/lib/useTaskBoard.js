// The board hook: the last read of the board plus the moves shown before the
// CLI answers (spec §10). An operation stays laid over the read until the CLI
// has answered and the board was read again; a refusal drops it, so the card
// slides back, and is reported in `notice`.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { tasksApi, onTasksChanged } from '../services/tasks.js'
import { normalizeBoard, applyOps } from './taskModel.js'

export function useTaskBoard(active = true) {
  const [base, setBase] = useState(null)
  const [ops, setOps] = useState([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState(null) // {id, error, code}
  const seq = useRef(0)
  const opId = useRef(0)

  // An op settles with the first board read applied after its call answered:
  // pending maps op key -> the lowest read sequence that may release it.
  const pending = useRef(new Map())
  const release = useCallback((upTo) => {
    const gone = [...pending.current].filter(([, min]) => min <= upTo).map(([k]) => k)
    if (!gone.length) return
    gone.forEach(k => pending.current.delete(k))
    setOps(list => list.filter(o => !gone.includes(o.key)))
  }, [])

  const load = useCallback(async () => {
    const mine = ++seq.current
    const doc = await tasksApi.board()
    if (mine !== seq.current) return // a newer read answers for this one
    if (doc?.error) { setError(doc.error); release(mine); return }
    setError('')
    setBase(normalizeBoard(doc))
    release(mine)
  }, [release])

  useEffect(() => {
    if (!active) return undefined
    load()
    const off = onTasksChanged(() => { load() })
    return () => { seq.current++; if (typeof off === 'function') off() }
  }, [active, load])

  // mutate lays op (or none) over the board, runs call, and settles.
  const mutate = useCallback(async (op, call) => {
    const key = ++opId.current
    if (op) setOps(list => [...list, { ...op, key }])
    const res = await call()
    if (res?.error) {
      setNotice({ id: op?.id ?? null, error: res.error, code: res.code })
      if (op) setOps(list => list.filter(o => o.key !== key))
    } else {
      if (op) pending.current.set(key, seq.current + 1)
      await load()
    }
    return res
  }, [load])

  const move = useCallback((id, to, place) =>
    mutate({ type: 'move', id, to, place }, () => tasksApi.move(id, to, place)), [mutate])
  const approve = useCallback((id, top = false) =>
    mutate({ type: 'move', id, to: 'ready', place: { where: top ? 'top' : 'bottom' } }, () => tasksApi.approve([id], top)), [mutate])
  const archive = useCallback((id) =>
    mutate({ type: 'remove', id }, () => tasksApi.archive([id])), [mutate])
  const add = useCallback((spec) => mutate(null, () => tasksApi.add(spec)), [mutate])
  const edit = useCallback((id, change) => mutate(null, () => tasksApi.edit(id, change)), [mutate])
  const comment = useCallback((id, text) => mutate(null, () => tasksApi.comment(id, text)), [mutate])

  const board = useMemo(() => applyOps(base, ops), [base, ops])
  return { board, error, notice, clearNotice: () => setNotice(null), reload: load, move, approve, archive, add, edit, comment }
}
