import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { tasksApi } from '../services/tasks.js'

// The detail drawer of one card: edit title and notes, read its history,
// comment. Every change goes through the board hook, which shells out to the CLI.
export default function TaskDrawer({ task, readOnly, onClose, onEdit, onComment }) {
  const { t } = useTranslation()
  const [events, setEvents] = useState([])
  const [loadError, setLoadError] = useState('')
  const [title, setTitle] = useState(task.title)
  const [notes, setNotes] = useState(task.notes || '')
  const [text, setText] = useState('')

  useEffect(() => { setTitle(task.title); setNotes(task.notes || '') }, [task.id, task.title, task.notes])
  useEffect(() => {
    let live = true
    tasksApi.show(task.id).then(r => {
      if (!live) return
      if (r?.error) setLoadError(r.error)
      else { setLoadError(''); setEvents(r?.events || []) }
    })
    return () => { live = false }
  }, [task.id, task.updated_at, task.last_event?.at])

  const dirty = title !== task.title || notes !== (task.notes || '')
  const save = () => {
    const change = {}
    if (title !== task.title) change.title = title
    if (notes !== (task.notes || '')) change.notes = notes
    onEdit(task.id, change)
  }
  const send = () => {
    const v = text.trim()
    if (!v) return
    onComment(task.id, v)
    setText('')
  }

  return (
    <aside role="dialog" aria-label={task.title} style={{ width: 340, flexShrink: 0, borderLeft: '1px solid var(--border)', padding: 16, overflowY: 'auto', background: 'var(--elevated)' }}>
      <button type="button" aria-label={t('tasks.close')} onClick={onClose} style={{ float: 'right', background: 'none', border: 0, color: 'inherit', cursor: 'pointer' }}><X size={16} /></button>
      <label style={{ display: 'block', marginBottom: 8 }}>
        <div>{t('tasks.drawer.title')}</div>
        <input value={title} disabled={readOnly} onChange={e => setTitle(e.target.value)} style={{ width: '100%' }} />
      </label>
      <label style={{ display: 'block', marginBottom: 8 }}>
        <div>{t('tasks.drawer.notes')}</div>
        <textarea value={notes} disabled={readOnly} rows={5} onChange={e => setNotes(e.target.value)} style={{ width: '100%' }} />
      </label>
      {!readOnly && <button type="button" disabled={!dirty || !title.trim()} onClick={save}>{t('tasks.drawer.save')}</button>}

      <h3 style={{ marginTop: 16 }}>{t('tasks.drawer.history')}</h3>
      {loadError && <div role="alert">{t('tasks.drawer.loadError')}: {loadError}</div>}
      <ul style={{ listStyle: 'none', padding: 0, fontSize: 12 }}>
        {events.map(e => (
          <li key={e.id} style={{ marginBottom: 6 }}>
            <strong>{e.actor}</strong> {e.kind}{e.to_status ? ` → ${e.to_status}` : ''}
            {e.note && <div style={{ whiteSpace: 'pre-wrap', opacity: 0.85 }}>{e.note}</div>}
          </li>
        ))}
      </ul>

      {!readOnly && (
        <div>
          <textarea aria-label={t('tasks.drawer.comment')} placeholder={t('tasks.drawer.comment')} rows={2} value={text} onChange={e => setText(e.target.value)} style={{ width: '100%' }} />
          <button type="button" disabled={!text.trim()} onClick={send}>{t('tasks.drawer.send')}</button>
        </div>
      )}
    </aside>
  )
}
