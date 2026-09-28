// Browse the monoes.me library for one kind (workflow | automation | org)
// and add items to this machine. Tabs: Official · Community · Mine. All
// facts (what is installed, whether an update exists, name collisions) come
// from `monoagentcli library …`; this dialog only shows them.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { X, Search } from 'lucide-react'
import { library, LIBRARY_TABS } from '../../services/library.js'
import LogInToMonoesButton from './LogInToMonoesButton.jsx'
import LibraryItemCard from './LibraryItemCard.jsx'

const mono = { fontFamily: 'var(--font-mono)', fontSize: 11 }
const PER_PAGE = 20

export default function LibraryModal({ kind, onClose, onInstalled }) {
  const { t } = useTranslation()
  const [status, setStatus] = useState(null)
  const [tab, setTab] = useState('official')
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let live = true
    library.status(false).then(s => { if (live) setStatus(s?.error ? { logged_in: false, error: s.error } : s) })
    return () => { live = false }
  }, [])

  // Debounce typing into the search box.
  useEffect(() => {
    const id = setTimeout(() => setSearch(query.trim()), 300)
    return () => clearTimeout(id)
  }, [query])

  const needsLogin = tab === 'mine' && status && !status.logged_in
  // Only the Mine tab depends on the account; the others list the same
  // items whether or not you are logged in.
  const mineKey = tab === 'mine' ? String(status?.logged_in) : ''

  useEffect(() => {
    if (mineKey === 'undefined') { setLoading(true); return } // account not known yet
    if (needsLogin) { setItems([]); setLoading(false); return }
    let live = true
    setLoading(true); setError('')
    const scope = LIBRARY_TABS.find(x => x.id === tab).scope
    library.list(kind, scope, search, 1).then(res => {
      if (!live) return
      setLoading(false)
      if (res?.error) { setError(res.error); setItems([]); setTotal(0); return }
      let list = Array.isArray(res?.items) ? res.items : []
      if (tab === 'community') list = list.filter(i => i.visibility !== 'official')
      setItems(list); setTotal(res.total || list.length); setPage(1)
    })
    return () => { live = false }
  }, [kind, tab, search, needsLogin, mineKey, reload])

  const loadMore = useCallback(async () => {
    const scope = LIBRARY_TABS.find(x => x.id === tab).scope
    const res = await library.list(kind, scope, search, page + 1)
    if (res?.error) { setError(res.error); return }
    let more = Array.isArray(res?.items) ? res.items : []
    if (tab === 'community') more = more.filter(i => i.visibility !== 'official')
    setItems(prev => [...prev, ...more]); setPage(page + 1)
  }, [kind, tab, search, page])

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose?.() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const installed = (item, res) => {
    // Show the new installed state on the card without a round trip.
    setItems(prev => prev.map(i => i.id === item.id
      ? { ...i, installed: { local_id: res.local_id, version: item.version, update_available: false } }
      : i))
    onInstalled?.(res)
  }

  return (
    <div className="modal-overlay" style={{ zIndex: 1100 }} onMouseDown={e => { if (e.target === e.currentTarget) onClose?.() }}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="library-modal-title"
        style={{ width: 640, display: 'flex', flexDirection: 'column', gap: 12, padding: 20 }}>
        <div className="modal-title" style={{ marginBottom: 0 }}>
          <span id="library-modal-title">{t(`library.title.${kind}`)}</span>
          <button className="btn btn-ghost btn-icon" onClick={onClose} aria-label={t('library.close')}><X size={15} /></button>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap' }}>
          <div role="tablist" style={{ display: 'flex', gap: 4 }}>
            {LIBRARY_TABS.map(x => (
              <button key={x.id} role="tab" aria-selected={tab === x.id} onClick={() => setTab(x.id)}
                style={{
                  ...mono, fontSize: 10, fontWeight: 700, padding: '5px 10px', borderRadius: 'var(--radius)', cursor: 'pointer',
                  background: tab === x.id ? 'var(--cyan-glow-strong)' : 'transparent',
                  border: tab === x.id ? '1px solid var(--border-active)' : '1px solid transparent',
                  color: tab === x.id ? 'var(--cyan)' : 'var(--text-muted)',
                }}>
                {t(`library.tabs.${x.id}`)}
              </button>
            ))}
          </div>
          <LogInToMonoesButton status={status} onStatusChange={setStatus} compact />
        </div>

        <label style={{ position: 'relative', display: 'block' }}>
          <Search size={12} style={{ position: 'absolute', left: 10, top: 11, color: 'var(--text-muted)' }} />
          <input className="form-input" style={{ paddingLeft: 30 }} value={query} onChange={e => setQuery(e.target.value)}
            placeholder={t('library.searchPlaceholder')} aria-label={t('library.searchPlaceholder')} />
        </label>

        <div style={{ minHeight: 160, maxHeight: '52vh', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 8 }}>
          {needsLogin ? (
            <div className="empty-state" style={{ gap: 10 }}>
              <div className="empty-state-desc">{t('library.mineLoginPrompt')}</div>
              <LogInToMonoesButton status={status} onStatusChange={setStatus} />
            </div>
          ) : loading ? (
            <div style={{ ...mono, color: 'var(--text-muted)', padding: 24, textAlign: 'center' }}>{t('library.loading')}</div>
          ) : error ? (
            <div role="alert" style={{ ...mono, color: 'var(--red)', padding: 16, display: 'flex', gap: 10, alignItems: 'center', justifyContent: 'center' }}>
              <span>{error}</span>
              <button className="btn btn-ghost btn-sm" onClick={() => setReload(n => n + 1)}>{t('library.retry')}</button>
            </div>
          ) : items.length === 0 ? (
            <div style={{ ...mono, color: 'var(--text-muted)', padding: 24, textAlign: 'center' }}>
              {tab === 'mine' ? t('library.emptyMine') : t('library.empty')}
            </div>
          ) : (
            <>
              {items.map(item => <LibraryItemCard key={item.id} item={item} kind={kind} onInstalled={res => installed(item, res)} />)}
              {total > page * PER_PAGE && (
                <button className="btn btn-ghost btn-sm" style={{ alignSelf: 'center' }} onClick={loadMore}>{t('library.loadMore')}</button>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
