// One library item: what it is, who published it, whether this machine has
// it (and an update), and the Add button. An org whose name is taken here
// gets a rename-or-replace prompt, as the CLI asks for one.
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { BadgeCheck, Download, Loader, RefreshCw } from 'lucide-react'
import { library, isNameCollision } from '../../services/library.js'

const mono = { fontFamily: 'var(--font-mono)' }

function Tag({ children, color = 'var(--text-muted)' }) {
  return (
    <span style={{ ...mono, fontSize: 9, color, border: `1px solid ${color}`, borderRadius: 8, padding: '0 6px', lineHeight: '15px', whiteSpace: 'nowrap', display: 'inline-flex', alignItems: 'center', gap: 3 }}>
      {children}
    </span>
  )
}

export default function LibraryItemCard({ item, kind, onInstalled }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(null) // install result
  const [collision, setCollision] = useState(null) // { name }
  const [rename, setRename] = useState('')

  const inst = item.installed
  const update = !!inst?.update_available
  const official = item.visibility === 'official'
  const owner = item.owner?.name || item.owner?.username || ''

  const add = async (opts = {}) => {
    setBusy(true); setError('')
    // A reinstall over what is already here (an update) is confirmed by
    // the click itself.
    const res = await library.install(kind, item.id, { yes: update, ...opts })
    setBusy(false)
    if (isNameCollision(res)) {
      setCollision({ name: item.slug || item.name })
      setRename(`${item.slug || 'org'}-2`)
      return
    }
    if (res?.error) { setError(res.error); return }
    setCollision(null); setDone(res)
    onInstalled?.(res)
  }

  let action
  if (busy) {
    action = <><Loader size={11} style={{ animation: 'spin .7s linear infinite' }} /> {t('library.adding')}</>
  } else if (update) {
    action = <><RefreshCw size={11} /> {t('library.update')}</>
  } else if (inst || done) {
    action = t('library.added')
  } else {
    action = <><Download size={11} /> {t('library.add')}</>
  }

  return (
    <div className="card" style={{ padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
            <span style={{ fontFamily: 'var(--font-body)', fontSize: 13, fontWeight: 600, color: 'var(--text)' }}>{item.name || item.slug}</span>
            <span style={{ ...mono, fontSize: 10, color: 'var(--text-muted)' }}>{item.version}</span>
            {official && <Tag color="var(--cyan)"><BadgeCheck size={10} /> {t('library.official')}</Tag>}
            {inst && !update && <Tag color="var(--green)">{t('library.installedVersion', { version: inst.version })}</Tag>}
            {update && <Tag color="var(--yellow)">{t('library.updateAvailable')}</Tag>}
          </div>
          {item.description && (
            <div style={{ fontFamily: 'var(--font-body)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.45, marginTop: 3 }}>{item.description}</div>
          )}
          <div style={{ display: 'flex', gap: 5, flexWrap: 'wrap', marginTop: 5, alignItems: 'center' }}>
            {owner && <span style={{ ...mono, fontSize: 9.5, color: 'var(--text-muted)' }}>{t('library.by', { owner })}</span>}
            {(item.tags || []).map(tag => <Tag key={tag}>{tag}</Tag>)}
          </div>
        </div>
        <button className={`btn btn-sm ${update ? 'btn-primary' : 'btn-secondary'}`} style={{ gap: 5, flexShrink: 0 }}
          disabled={busy || ((!!inst || !!done) && !update) || !!collision}
          onClick={() => add()} aria-label={`${t('library.add')} ${item.name || item.slug}`}>
          {action}
        </button>
      </div>

      {collision && (
        <div role="group" aria-label={t('library.collision.title')}
          style={{ display: 'flex', flexDirection: 'column', gap: 6, padding: 10, borderRadius: 'var(--radius)', background: 'var(--elevated)', border: '1px solid var(--border-bright)' }}>
          <span style={{ ...mono, fontSize: 10.5, color: 'var(--yellow)' }}>{t('library.collision.title')}</span>
          <span style={{ fontFamily: 'var(--font-body)', fontSize: 12, color: 'var(--text-secondary)' }}>{t('library.collision.body', { name: collision.name })}</span>
          <label className="form-label" htmlFor={`rename-${item.id}`} style={{ marginBottom: 0 }}>{t('library.collision.renameLabel')}</label>
          <input id={`rename-${item.id}`} className="form-input" style={{ padding: '6px 10px' }} value={rename} onChange={e => setRename(e.target.value)} />
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <button className="btn btn-primary btn-sm" disabled={busy || !rename.trim()} onClick={() => add({ rename: rename.trim() })}>{t('library.collision.rename')}</button>
            <button className="btn btn-danger btn-sm" disabled={busy} onClick={() => add({ yes: true })}>{t('library.collision.replace')}</button>
            <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setCollision(null)}>{t('library.cancel')}</button>
          </div>
        </div>
      )}

      {error && <div role="alert" style={{ ...mono, fontSize: 10.5, color: 'var(--red)' }}>{error}</div>}
      {done && (
        <div role="status" style={{ ...mono, fontSize: 10.5, color: 'var(--green)', display: 'flex', flexDirection: 'column', gap: 3 }}>
          {done.local_id && <span>{t('library.addedAs', { id: done.local_id })}</span>}
          {(done.missing_automations || []).length > 0 && (
            <span style={{ color: 'var(--yellow)' }}>{t('library.missingAutomations', { ids: done.missing_automations.join(', ') })}</span>
          )}
          {(done.warnings || []).map((w, i) => <span key={i} style={{ color: 'var(--text-muted)' }}>{w}</span>)}
        </div>
      )}
    </div>
  )
}
