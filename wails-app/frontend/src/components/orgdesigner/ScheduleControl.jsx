// Schedule button for the designer toolbar (#342). Any org can run on an
// interval, a sections org included: monomind starts each tick as a fresh run
// (its own run id and, for a sections org, an empty document store), so there
// is nothing to carry forward and nothing here blocks it.
import { useState } from 'react'
import { Clock } from 'lucide-react'

const toolbarBtnStyle = {
  display: 'flex', alignItems: 'center', gap: 4,
  fontFamily: 'var(--font-mono)', fontSize: 10, padding: '4px 8px', borderRadius: 'var(--radius)',
  background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-muted)', cursor: 'pointer',
}

export const FRESH_RUN_NOTE = 'A fresh run starts every tick, with its own run id: nothing carries forward from the tick before.'
export const SECTIONS_NOTE = 'In a sections org each tick also starts with an empty document store.'

// monomind's schedule is "45s" | "15m" | "2h" or a number of minutes.
export function scheduleLabel(schedule) {
  if (schedule == null || schedule === '' || schedule === 0) return ''
  return typeof schedule === 'number' ? `${schedule}m` : String(schedule)
}

export default function ScheduleControl({ schedule, sections = false, onSave }) {
  const current = scheduleLabel(schedule)
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState(current)
  const [error, setError] = useState('')

  const save = async (next) => {
    const res = await onSave(next)
    if (res?.error) { setError(res.error); return }
    setError('')
    setOpen(false)
  }

  return (
    <span style={{ position: 'relative' }}>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => { setValue(current); setError(''); setOpen(o => !o) }}
        title="Run this org on an interval"
        style={toolbarBtnStyle}
      >
        <Clock size={11} /> {current ? `Every ${current}` : 'Schedule'}
      </button>
      {open && (
        <div role="dialog" aria-label="Schedule" style={{
          position: 'absolute', top: '100%', right: 0, marginTop: 4, zIndex: 30, width: 280,
          padding: 10, display: 'flex', flexDirection: 'column', gap: 6,
          background: 'var(--surface, #0d1520)', border: '1px solid var(--border)', borderRadius: 8,
          boxShadow: '0 8px 24px rgba(0,0,0,0.45)', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
        }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            Interval (30s, 15m, 2h)
            <input
              aria-label="Interval"
              value={value}
              onChange={e => setValue(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') save(value) }}
              placeholder="none"
              style={{ fontFamily: 'inherit', fontSize: 11, padding: '3px 6px', background: 'var(--elevated)', color: 'var(--text)', border: '1px solid var(--border)', borderRadius: 4 }}
            />
          </label>
          <span data-testid="schedule-note" style={{ lineHeight: 1.5 }}>
            {FRESH_RUN_NOTE}{sections ? ` ${SECTIONS_NOTE}` : ''} A tick that cannot start, or lands on a live run, is listed under Logs.
          </span>
          {error && <span role="alert" style={{ color: '#f87171' }}>{error}</span>}
          <span style={{ display: 'flex', gap: 6 }}>
            <button type="button" onClick={() => save(value)} style={toolbarBtnStyle}>Save</button>
            {current && <button type="button" onClick={() => save('')} style={toolbarBtnStyle}>Clear</button>}
          </span>
        </div>
      )}
    </span>
  )
}
