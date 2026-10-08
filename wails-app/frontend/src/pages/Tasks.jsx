import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Archive, Check, CircleHelp } from 'lucide-react'
import { tasksApi } from '../services/tasks.js'
import { useTaskBoard } from '../lib/useTaskBoard.js'
import { COLUMNS, findTask, filterBoard, placeFor, isNoopDrop, dropIndex, keyMove, focusTarget, isTypingTarget } from '../lib/taskModel.js'
import TaskDrawer from '../components/TaskDrawer.jsx'
import { useFlip } from '../lib/useFlip.js'

const ARROWS = { ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down' }

function QuickAdd({ status, onAdd, t }) {
  const [title, setTitle] = useState('')
  const submit = (e) => {
    e.preventDefault()
    const v = title.trim()
    if (!v) return
    onAdd({ title: v, ...(status === 'ready' ? { ready: true } : {}) })
    setTitle('')
  }
  return (
    <form onSubmit={submit} style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
      <input aria-label={t('tasks.quickAddPlaceholder')} placeholder={t('tasks.quickAddPlaceholder')} value={title} onChange={e => setTitle(e.target.value)} style={{ flex: 1, minWidth: 0 }} />
      <button type="submit" disabled={!title.trim()}>{status === 'ready' ? t('tasks.addToReady') : t('tasks.add')}</button>
    </form>
  )
}

const CAPTURE_COMMANDS = ['monoagentcli task add -- "Title"', 'monoagentcli task add --stdin < notes.txt']

function CaptureHelp({ t }) {
  return (
    <details>
      <summary><CircleHelp size={12} /> {t('tasks.capture.title')}</summary>
      <ul style={{ margin: '4px 0', paddingLeft: 18 }}>
        <li>{t('tasks.capture.chrome')}</li>
        <li>{t('tasks.capture.mac')}</li>
        <li>{t('tasks.capture.cli')} {CAPTURE_COMMANDS.map(c => <code key={c} style={{ display: 'block' }}>{c}</code>)}</li>
        <li>{t('tasks.capture.agents')}</li>
      </ul>
    </details>
  )
}

function Card({ task, status, readOnly, selected, onOpen, onKey, onDragStart, onApprove, onArchive, t }) {
  return (
    <div
      data-task-id={task.id}
      draggable={!readOnly}
      tabIndex={0}
      role="button"
      aria-label={task.title}
      aria-pressed={selected}
      onClick={() => onOpen(task.id)}
      onKeyDown={e => onKey(e, task)}
      onDragStart={e => { e.dataTransfer?.setData('text/plain', String(task.id)); onDragStart(task.id) }}
      style={{ padding: 8, marginBottom: 6, border: `1px solid ${selected ? 'var(--accent, #00b4d8)' : 'var(--border)'}`, borderRadius: 6, background: 'var(--elevated)', cursor: 'pointer' }}
    >
      <div style={{ fontWeight: 600 }}>{task.title}</div>
      {task.claim && <div style={{ fontSize: 11, opacity: 0.8 }}>{task.claim.stale ? t('tasks.stale') : t('tasks.claimedBy', { by: task.claim.by })}</div>}
      {!readOnly && (
        <div style={{ display: 'flex', gap: 4, marginTop: 4 }}>
          {status === 'inbox' && (
            <button type="button" title={t('tasks.approve')} aria-label={`${t('tasks.approve')}: ${task.title}`} onClick={e => { e.stopPropagation(); onApprove(task.id) }}><Check size={12} /></button>
          )}
          <button type="button" title={t('tasks.archive')} aria-label={`${t('tasks.archive')}: ${task.title}`} onClick={e => { e.stopPropagation(); onArchive(task.id) }}><Archive size={12} /></button>
        </div>
      )}
    </div>
  )
}

export default function Tasks({ isActive = true }) {
  const { t } = useTranslation()
  const b = useTaskBoard(isActive)
  const [query, setQuery] = useState('')
  const [openId, setOpenId] = useState(null)
  const [shell, setShell] = useState(null) // null until agentShell() answers
  const dragging = useRef(null)
  const root = useRef(null)
  const refocus = useRef(null) // a card moved by key: a move to another column remounts it

  useFlip(root)
  useEffect(() => { tasksApi.agentShell().then(v => setShell(v || '')) }, [])
  const readOnly = shell === null || !!shell
  const shown = useMemo(() => filterBoard(b.board, query), [b.board, query])
  const open = openId && b.board ? findTask(b.board, openId)?.task : null

  // No dependency list: any render may have remounted the card the keys moved.
  useLayoutEffect(() => {
    const id = refocus.current
    if (!id) return
    const el = root.current?.querySelector(`[data-task-id="${id}"]`)
    if (!el) return
    refocus.current = null
    if (document.activeElement !== el) el.focus()
  })

  useEffect(() => {
    if (!openId) return undefined
    // Escape in a field only leaves it (an edit in progress is not lost); elsewhere it closes the drawer.
    const onEsc = (e) => {
      if (e.key !== 'Escape') return
      if (isTypingTarget(e.target)) e.target.blur()
      else setOpenId(null)
    }
    window.addEventListener('keydown', onEsc)
    return () => window.removeEventListener('keydown', onEsc)
  }, [openId])

  const focusCard = (id) => root.current?.querySelector(`[data-task-id="${id}"]`)?.focus()

  const onKey = (e, task) => {
    if (e.target !== e.currentTarget) return
    const key = ARROWS[e.key]
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpenId(task.id); return }
    if (!key) return
    e.preventDefault()
    if (e.shiftKey || e.altKey) {
      if (readOnly) return
      const m = keyMove(shown, task.id, key)
      if (m && !m.blocked) { refocus.current = task.id; b.move(task.id, m.to, m.place) }
      return
    }
    const next = focusTarget(shown, task.id, key)
    if (next) focusCard(next)
  }

  const onDrop = (e, status) => {
    e.preventDefault()
    const id = dragging.current
    dragging.current = null
    const found = id && findTask(shown, id)
    if (!found || readOnly) return
    const cards = [...e.currentTarget.querySelectorAll('[data-task-id]')].filter(el => Number(el.dataset.taskId) !== id)
    const ids = cards.map(el => Number(el.dataset.taskId))
    const index = dropIndex(cards.map(el => el.getBoundingClientRect()), e.clientY)
    const fromIds = shown.columns[found.status].map(x => x.id)
    if (isNoopDrop(fromIds, found.status, status, ids, index, id)) return
    b.move(id, status, placeFor(ids, index))
  }

  if (!b.board) {
    return <div className="page" style={{ padding: 16 }}>{b.error ? <div role="alert">{t('tasks.loadError')}: {b.error}</div> : t('tasks.loading')}</div>
  }

  return (
    <div className="page" ref={root} style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: 16, gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <h1 style={{ margin: 0 }}>{t('tasks.title')}</h1>
        <input type="search" aria-label={t('tasks.search')} placeholder={t('tasks.search')} value={query} onChange={e => setQuery(e.target.value)} />
      </div>
      <CaptureHelp t={t} />
      {readOnly && <div role="note">{t('tasks.readOnly')}</div>}
      {b.error && <div role="alert">{t('tasks.loadError')}: {b.error}</div>}
      {b.notice && (
        <div role="alert" onClick={b.clearNotice}>{t('tasks.refused', { error: b.notice.error })}</div>
      )}
      <div style={{ display: 'flex', flex: 1, minHeight: 0, gap: 8 }}>
        <div style={{ display: 'flex', flex: 1, gap: 8, minWidth: 0 }}>
          {COLUMNS.map(status => (
            <section
              key={status}
              aria-label={t(`tasks.column.${status}`)}
              onDragOver={e => e.preventDefault()}
              onDrop={e => onDrop(e, status)}
              style={{ flex: 1, minWidth: 0, overflowY: 'auto', padding: 8, border: '1px solid var(--border)', borderRadius: 6 }}
            >
              <h2 style={{ fontSize: 14, marginTop: 0 }}>{t(`tasks.column.${status}`)} <span>{shown.counts[status] ?? shown.columns[status].length}</span></h2>
              {!readOnly && (status === 'inbox' || status === 'ready') && <QuickAdd status={status} onAdd={b.add} t={t} />}
              {shown.columns[status].length === 0 && <div style={{ opacity: 0.6 }}>{t(`tasks.emptyBy.${status}`, { defaultValue: t('tasks.empty') })}</div>}
              {shown.columns[status].map(task => (
                <Card
                  key={task.id} task={task} status={status} readOnly={readOnly} selected={openId === task.id}
                  onOpen={setOpenId} onKey={onKey} onDragStart={id => { dragging.current = id }}
                  onApprove={id => b.approve(id)} onArchive={id => { if (openId === id) setOpenId(null); b.archive(id) }} t={t}
                />
              ))}
            </section>
          ))}
        </div>
        {open && <TaskDrawer task={open} readOnly={readOnly} onClose={() => setOpenId(null)} onEdit={b.edit} onComment={b.comment} />}
      </div>
    </div>
  )
}
