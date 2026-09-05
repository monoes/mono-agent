import { useState, useEffect, useCallback, useMemo } from 'react'
import { Plus, Search, ExternalLink } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'
import { notify } from '../services/api.js'
import ApplicationsProcessPendingFlow from './ApplicationsProcessPendingFlow.jsx'

const STATUS_TABS = ['pending', 'applied', 'rejected', 'cancelled', 'all']

const STATUS_COLORS = {
  pending: { bg: 'rgba(245,158,11,0.12)', color: '#f59e0b' },
  applied: { bg: 'rgba(16,185,129,0.12)', color: '#10b981' },
  rejected: { bg: 'rgba(239,68,68,0.12)', color: '#ef4444' },
  cancelled: { bg: 'rgba(100,116,139,0.12)', color: '#64748b' },
}

// Pure so it's directly unit-testable (see Applications.test.js) without
// rendering the component -- mirrors Vault.jsx's filterAndSortEntries
// convention.
export function filterApplications(applications, { statusTab = 'all', search = '' } = {}) {
  const q = search.trim().toLowerCase()
  return applications.filter(a => {
    if (statusTab !== 'all' && a.status !== statusTab) return false
    if (!q) return true
    return (
      a.title?.toLowerCase().includes(q) ||
      a.company?.toLowerCase().includes(q) ||
      a.tags?.some(t => t.toLowerCase().includes(q))
    )
  })
}

const inputStyle = {
  background: '#060b11', border: '1px solid #1e3a4f', borderRadius: 5,
  padding: '6px 8px', color: '#e2e8f0', fontFamily: 'var(--font-mono)', fontSize: 11,
}
const headerBtnStyle = {
  background: 'rgba(0,180,216,0.1)', border: '1px solid rgba(0,180,216,0.3)',
  borderRadius: 6, padding: '6px 12px', color: '#00b4d8',
  fontFamily: 'var(--font-mono)', fontSize: 11, cursor: 'pointer',
  display: 'flex', alignItems: 'center', gap: 5,
}

function StatusBadge({ status }) {
  const s = STATUS_COLORS[status] || { bg: '#1a2332', color: '#64748b' }
  return (
    <span style={{ background: s.bg, borderRadius: 3, padding: '1px 6px', fontFamily: 'var(--font-mono)', fontSize: 9, color: s.color }}>
      {status}
    </span>
  )
}

function emptyAddForm() {
  return { kind: 'job', title: '', company: '', url: '', issuingOrg: '', submissionDeadline: '' }
}

export default function Applications() {
  const [applications, setApplications] = useState([])
  const [statusTab, setStatusTab] = useState('pending')
  const [search, setSearch] = useState('')
  const [error, setError] = useState(null)
  const [selectedId, setSelectedId] = useState(null)
  const [detail, setDetail] = useState(null)
  const [showAdd, setShowAdd] = useState(false)
  const [addForm, setAddForm] = useState(emptyAddForm())
  const [showDiscover, setShowDiscover] = useState(false)
  const [discoverForm, setDiscoverForm] = useState({ keywords: '', location: '', limit: 25 })
  const [showProcessPending, setShowProcessPending] = useState(false)

  const load = useCallback(async () => {
    try {
      const list = await WailsApp.GetApplications('', '', '')
      setApplications(list || [])
    } catch (e) {
      setError('Failed to load applications: ' + e)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const visible = useMemo(
    () => filterApplications(applications, { statusTab, search }),
    [applications, statusTab, search]
  )

  const openDetail = async (id) => {
    setSelectedId(id)
    try {
      const d = await WailsApp.GetApplication(id)
      setDetail(d)
    } catch (e) {
      setError('Failed to load detail: ' + e)
    }
  }

  const closeDetail = () => { setSelectedId(null); setDetail(null) }

  const handleAdd = async (e) => {
    e.preventDefault()
    setError(null)
    try {
      await WailsApp.AddApplication(
        addForm.kind, addForm.title, addForm.company, addForm.url,
        addForm.issuingOrg, addForm.submissionDeadline,
      )
      setAddForm(emptyAddForm())
      setShowAdd(false)
      load()
    } catch (e) {
      setError('Add failed: ' + e)
    }
  }

  const handleDiscover = async (e) => {
    e.preventDefault()
    setError(null)
    try {
      await WailsApp.DiscoverJobs(discoverForm.keywords, discoverForm.location, '', Number(discoverForm.limit) || 25)
      setShowDiscover(false)
      load()
    } catch (e) {
      setError('Discovery failed: ' + e)
    }
  }

  const handleSetStatus = async (id, status) => {
    if (status === 'cancelled' && !(await confirm('Mark this application as cancelled?', { title: 'Cancel Application', confirmLabel: 'Cancel Application' }))) return
    try {
      await WailsApp.SetApplicationStatus(id, status, '')
      await load()
      if (selectedId === id) openDetail(id)
    } catch (e) {
      notify('application status', e?.message || String(e))
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: 16, gap: 12, overflow: 'hidden' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <h2 style={{ margin: 0, fontSize: 16, color: 'var(--text-primary)' }}>Applications</h2>
        <div style={{ flex: 1 }} />
        <button style={headerBtnStyle} onClick={() => setShowDiscover(true)}><Search size={13} /> Discover Jobs</button>
        <button style={headerBtnStyle} onClick={() => setShowAdd(true)}><Plus size={13} /> Add</button>
        <button style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }} onClick={() => setShowProcessPending(true)}>
          Process Pending
        </button>
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        {STATUS_TABS.map(tab => (
          <button
            key={tab}
            onClick={() => setStatusTab(tab)}
            style={{
              padding: '5px 12px', borderRadius: 5, cursor: 'pointer', fontSize: 11, fontFamily: 'var(--font-mono)',
              background: statusTab === tab ? 'rgba(0,180,216,0.15)' : 'transparent',
              border: `1px solid ${statusTab === tab ? 'rgba(0,180,216,0.4)' : 'rgba(255,255,255,0.1)'}`,
              color: statusTab === tab ? '#00b4d8' : 'var(--text-secondary)',
            }}
          >{tab}</button>
        ))}
        <input style={{ ...inputStyle, flex: 1, marginLeft: 8 }} placeholder="Search title, company, tags..." value={search} onChange={e => setSearch(e.target.value)} />
      </div>

      {error && <div style={{ color: '#ff6b6b', fontSize: 12 }}>{error}</div>}

      <div style={{ flex: 1, overflow: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ textAlign: 'left', color: 'var(--text-muted)', fontSize: 10, textTransform: 'uppercase' }}>
              <th style={{ padding: '6px 8px' }}>Title</th>
              <th style={{ padding: '6px 8px' }}>Company / Org</th>
              <th style={{ padding: '6px 8px' }}>Kind</th>
              <th style={{ padding: '6px 8px' }}>Status</th>
              <th style={{ padding: '6px 8px' }}>Tags</th>
              <th style={{ padding: '6px 8px' }}>Updated</th>
            </tr>
          </thead>
          <tbody>
            {visible.map(a => (
              <tr key={a.id} onClick={() => openDetail(a.id)} style={{ cursor: 'pointer', borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '8px' }}>{a.title}</td>
                <td style={{ padding: '8px' }}>{a.company}</td>
                <td style={{ padding: '8px' }}>{a.kind}</td>
                <td style={{ padding: '8px' }}><StatusBadge status={a.status} /></td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{(a.tags || []).join(', ')}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{a.updated_at}</td>
              </tr>
            ))}
            {visible.length === 0 && (
              <tr><td colSpan={6} style={{ padding: 16, textAlign: 'center', color: 'var(--text-muted)' }}>No applications.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {selectedId && detail && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && closeDetail()} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <div style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 520, maxHeight: '80vh', overflow: 'auto', fontFamily: 'var(--font-mono)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <h3 style={{ margin: 0, fontSize: 14 }}>{detail.title}</h3>
              <StatusBadge status={detail.status} />
            </div>
            <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 6 }}>
              {detail.kind === 'job' ? detail.company : detail.issuing_org}
              {' · '}
              <a href="#" onClick={(e) => { e.preventDefault(); WailsApp.OpenURL?.(detail.url) }} style={{ color: '#00b4d8' }}>
                {detail.url} <ExternalLink size={10} style={{ verticalAlign: 'middle' }} />
              </a>
            </div>
            {detail.description && <p style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 10 }}>{detail.description}</p>}
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              {detail.status === 'pending' && (
                <button style={headerBtnStyle} onClick={() => handleSetStatus(detail.id, 'cancelled')}>Cancel Application</button>
              )}
            </div>
            <h4 style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 16, textTransform: 'uppercase' }}>History</h4>
            {(detail.status_log || []).map((e, i) => (
              <div key={i} style={{ fontSize: 11, color: 'var(--text-muted)', padding: '3px 0' }}>
                {e.created_at}: {e.from_status || '(created)'} → {e.to_status} ({e.actor})
              </div>
            ))}
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
              <button style={headerBtnStyle} onClick={closeDetail}>Close</button>
            </div>
          </div>
        </div>
      )}

      {showAdd && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setShowAdd(false)} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <form onSubmit={handleAdd} style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 400, fontFamily: 'var(--font-mono)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <h3 style={{ margin: 0, fontSize: 14 }}>Add Application</h3>
            <select style={inputStyle} value={addForm.kind} onChange={e => setAddForm(f => ({ ...f, kind: e.target.value }))}>
              <option value="job">Job</option>
              <option value="tender">Tender</option>
            </select>
            <input style={inputStyle} placeholder="Title" required value={addForm.title} onChange={e => setAddForm(f => ({ ...f, title: e.target.value }))} />
            <input style={inputStyle} placeholder="URL" required value={addForm.url} onChange={e => setAddForm(f => ({ ...f, url: e.target.value }))} />
            {addForm.kind === 'job' ? (
              <input style={inputStyle} placeholder="Company" required value={addForm.company} onChange={e => setAddForm(f => ({ ...f, company: e.target.value }))} />
            ) : (
              <>
                <input style={inputStyle} placeholder="Issuing Organization" required value={addForm.issuingOrg} onChange={e => setAddForm(f => ({ ...f, issuingOrg: e.target.value }))} />
                <input style={inputStyle} placeholder="Submission Deadline" required value={addForm.submissionDeadline} onChange={e => setAddForm(f => ({ ...f, submissionDeadline: e.target.value }))} />
              </>
            )}
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
              <button type="button" style={headerBtnStyle} onClick={() => setShowAdd(false)}>Cancel</button>
              <button type="submit" style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }}>Add</button>
            </div>
          </form>
        </div>
      )}

      {showDiscover && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setShowDiscover(false)} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <form onSubmit={handleDiscover} style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 400, fontFamily: 'var(--font-mono)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <h3 style={{ margin: 0, fontSize: 14 }}>Discover Jobs</h3>
            <input style={inputStyle} placeholder="Keywords" required value={discoverForm.keywords} onChange={e => setDiscoverForm(f => ({ ...f, keywords: e.target.value }))} />
            <input style={inputStyle} placeholder="Location (optional)" value={discoverForm.location} onChange={e => setDiscoverForm(f => ({ ...f, location: e.target.value }))} />
            <input style={inputStyle} type="number" placeholder="Limit" value={discoverForm.limit} onChange={e => setDiscoverForm(f => ({ ...f, limit: e.target.value }))} />
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
              <button type="button" style={headerBtnStyle} onClick={() => setShowDiscover(false)}>Cancel</button>
              <button type="submit" style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }}>Search</button>
            </div>
          </form>
        </div>
      )}

      {showProcessPending && (
        <ApplicationsProcessPendingFlow
          pendingApplications={applications.filter(a => a.status === 'pending')}
          onClose={() => { setShowProcessPending(false); load() }}
        />
      )}
    </div>
  )
}
