import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AppWindow, Archive, Check, CircleHelp, Globe, LayoutGrid, Sparkles, Terminal } from 'lucide-react'
import { tasksApi } from '../services/tasks.js'
import { useTaskBoard } from '../lib/useTaskBoard.js'
import { COLUMNS, findTask, filterBoard, placeFor, isNoopDrop, dropIndex, keyMove, focusTarget, isTypingTarget, claimState, sourceChip, shortAge, splitMinutes, actorColor } from '../lib/taskModel.js'
import TaskDrawer from '../components/TaskDrawer.jsx'
import { useFlip } from '../lib/useFlip.js'
import { useMarks } from '../lib/useMarks.js'
import { usePageVisible } from '../lib/usePageVisible.js'

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

const SOURCE_ICONS = { chrome: Globe, os: AppWindow, agent: Sparkles, app: LayoutGrid, cli: Terminal }

function span(minutes, t) {
  const { h, m } = splitMinutes(minutes)
  return h ? t('tasks.span.hm', { h, m }) : t('tasks.span.m', { m })
}

// The card is a plain container: the title is its one control that opens it
// (and takes the move keys), and the approve and archive buttons are siblings
// of it, so no control sits inside another.
function Card({ task, status, readOnly, selected, pulse, now, onOpen, onKey, onDragStart, onApprove, onArchive, t }) {
  const chip = sourceChip(task)
  const Icon = SOURCE_ICONS[chip.kind] || Terminal
  const age = shortAge(task.created_at, now)
  const cs = claimState(task.claim, now)
  const tag = status === 'review' && (task.last_event?.kind === 'question' || task.last_event?.kind === 'result') ? task.last_event.kind : null
  const meta = { fontSize: 11, opacity: 0.8, display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', fontFamily: 'var(--mono, monospace)' }
  return (
    <div
      data-task-id={task.id}
      className={pulse ? `tb-card--${pulse}` : undefined}
      draggable={!readOnly}
      onClick={e => { if (!e.target.closest('button')) onOpen(task.id) }}
      onDragStart={e => { e.dataTransfer?.setData('text/plain', String(task.id)); onDragStart(task.id) }}
      style={{ padding: 8, marginBottom: 6, border: `1px solid ${selected ? 'var(--accent, #00b4d8)' : cs?.stale ? 'var(--orange, #f59e0b)' : 'var(--border)'}`, borderRadius: 6, background: 'var(--elevated)', cursor: 'pointer' }}
    >
      <button
        type="button"
        data-card-open
        aria-pressed={selected}
        onClick={() => onOpen(task.id)}
        onKeyDown={e => onKey(e, task)}
        style={{ display: 'block', width: '100%', textAlign: 'left', background: 'none', border: 0, padding: 0, color: 'inherit', font: 'inherit', fontWeight: 600, cursor: 'pointer' }}
      >{task.title}</button>
      <div style={{ ...meta, marginTop: 4 }}>
        <span title={t(`tasks.source.${chip.kind}`)} style={{ display: 'inline-flex', gap: 3, alignItems: 'center' }}>
          <Icon size={11} aria-hidden="true" /><span>{chip.text || t(`tasks.source.${chip.kind}`)}</span>
        </span>
        {task.created_at && <span data-testid="age">{t(`tasks.age.${age.unit}`, { n: age.n })}</span>}
        <span>#{task.id}</span>
        {tag && <span data-testid="tag" style={{ color: tag === 'question' ? 'var(--orange, #f59e0b)' : 'var(--green, #10b981)' }}>{t(`tasks.tag.${tag}`)}</span>}
      </div>
      {cs && (
        <div style={{ ...meta, marginTop: 4 }} title={cs.stale ? t('tasks.leaseEnded') : undefined}>
          <span aria-hidden="true" style={{ width: 16, height: 16, borderRadius: '50%', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 9, border: `1.5px ${cs.stale ? 'dashed var(--orange, #f59e0b)' : `solid ${actorColor(cs.by)}`}` }}>{cs.initial}</span>
          <span>{cs.stale ? t('tasks.stale') : t('tasks.claimedBy', { by: cs.by })}</span>
          <span data-testid="lease">{cs.stale ? t('tasks.ended', { time: span(cs.minutes, t) }) : t('tasks.left', { time: span(cs.minutes, t) })}</span>
        </div>
      )}
      {!readOnly && (
        <div style={{ display: 'flex', gap: 4, marginTop: 4 }}>
          {status === 'inbox' && (
            <button type="button" title={t('tasks.approve')} aria-label={`${t('tasks.approve')}: ${task.title}`} onClick={() => onApprove(task.id)}><Check size={12} /></button>
          )}
          <button type="button" title={t('tasks.archive')} aria-label={`${t('tasks.archive')}: ${task.title}`} onClick={() => onArchive(task.id)}><Archive size={12} /></button>
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
  const marks = useMarks(b.board)
  // A lease ends with no write, so no read follows: the clock decides.
  const [now, setNow] = useState(() => Date.now())
  const visible = usePageVisible()
  useEffect(() => {
    if (!isActive || !visible) return undefined // a hidden window does not tick
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(timer)
  }, [isActive, visible])
  useEffect(() => { tasksApi.agentShell().then(v => setShell(v || '')) }, [])
  const readOnly = shell === null || !!shell
  const shown = useMemo(() => filterBoard(b.board, query), [b.board, query])
  const [archiving, setArchiving] = useState(null)
  const [archiveError, setArchiveError] = useState('')
  const lastOpen = useRef(null)
  const found = openId && b.board ? findTask(b.board, openId)?.task : null
  if (found) lastOpen.current = found
  const open = found || (archiving && archiving === openId ? lastOpen.current : null)
  useEffect(() => { setArchiveError('') }, [openId])

  // No dependency list: any render may have remounted the card the keys moved.
  useLayoutEffect(() => {
    const id = refocus.current
    if (!id) return
    const el = root.current?.querySelector(`[data-task-id="${id}"] [data-card-open]`)
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

  const focusCard = (id) => root.current?.querySelector(`[data-task-id="${id}"] [data-card-open]`)?.focus()

  // Archive closes the drawer only once the CLI has said yes. While it works the
  // drawer stays on the card as last shown (the optimistic removal takes the
  // card off the board); a refusal brings the card back and the drawer says why.
  const archiveRes = async (id) => {
    const inDrawer = openId === id
    if (inDrawer) { setArchiving(id); setArchiveError('') }
    const res = await b.archive(id)
    setArchiving(null)
    if (res?.error) { if (inDrawer) setArchiveError(res.error) } else setOpenId(cur => (cur === id ? null : cur))
    return res
  }

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
    <div className={visible ? 'page' : 'page tb-paused'} ref={root} style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: 16, gap: 8 }}>
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
                  key={task.id} task={task} status={status} readOnly={readOnly} selected={openId === task.id} now={now}
                  pulse={status === 'done' && marks.done.has(task.id) ? 'done-pulse' : status === 'in_progress' && task.claim && marks.claimed.has(task.id) ? 'claim-pulse' : ''}
                  onOpen={setOpenId} onKey={onKey} onDragStart={id => { dragging.current = id }}
                  onApprove={id => b.approve(id)} onArchive={archiveRes} t={t}
                />
              ))}
            </section>
          ))}
        </div>
        {open && <TaskDrawer task={open} readOnly={readOnly} onClose={() => setOpenId(null)} onEdit={b.edit} onComment={b.comment} onArchive={() => archiveRes(open.id)} archiving={archiving === open.id} archiveError={archiveError} />}
      </div>
    </div>
  )
}
