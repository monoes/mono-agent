// Time labels for the dashboard. `t` is react-i18next's t; `now` is
// injectable for tests.
export function relTime(ts, t, now = Date.now()) {
  if (!ts) return '—'
  const diff = now - new Date(ts)
  if (isNaN(diff)) return '—'
  if (diff < 60000) return t('dashboard.time.justNow')
  if (diff < 3600000) return t('dashboard.time.minutesAgo', { count: Math.floor(diff / 60000) })
  if (diff < 86400000) return t('dashboard.time.hoursAgo', { count: Math.floor(diff / 3600000) })
  return new Date(ts).toLocaleDateString()
}

export function duration(startedAt, finishedAt, now = Date.now()) {
  if (!startedAt) return null
  const ms = (finishedAt ? new Date(finishedAt) : now) - new Date(startedAt)
  if (isNaN(ms) || ms < 0) return null
  const sec = Math.round(ms / 1000)
  return sec < 60 ? `${sec}s` : `${Math.floor(sec / 60)}m ${sec % 60}s`
}

export function untilTime(ts, t, now = Date.now()) {
  const diff = new Date(ts) - now
  if (isNaN(diff)) return '—'
  if (diff < 60000) return t('dashboard.time.now')
  if (diff < 3600000) return t('dashboard.time.inMinutes', { count: Math.round(diff / 60000) })
  if (diff < 86400000) return t('dashboard.time.inHours', { count: Math.round(diff / 3600000) })
  return new Date(ts).toLocaleString()
}
