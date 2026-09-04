import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Link2, Brain, ExternalLink, Download } from 'lucide-react'
import { api } from '../services/api.js'
import { GetVersion, CheckForUpdate, AppSelfUpdate } from '../wailsjs/go/main/App'
import { getAssistantTools, getAssistantAllowRuns, setAssistantTools, setAssistantAllowRuns } from '../lib/assistantTools.js'
import RefreshButton from '../components/RefreshButton.jsx'

// ── VersionRow ──────────────────────────────────────────────────────────────

function VersionRow() {
  const [ver, setVer] = useState(null)
  const [update, setUpdate] = useState(null)

  useEffect(() => { GetVersion().then(setVer).catch(() => {}) }, [])

  function check() {
    setUpdate({ checking: true })
    CheckForUpdate().then(info => {
      if (info.error) setUpdate({ error: info.error })
      else if (info.update_available) setUpdate({ available: true, latest: info.latest_version })
      else { setUpdate({ upToDate: true }); setTimeout(() => setUpdate(null), 3000) }
    }).catch(e => setUpdate({ error: String(e) }))
  }

  function doUpdate() {
    setUpdate(u => ({ ...u, updating: true }))
    AppSelfUpdate().then(r => {
      if (r.success) setUpdate({ done: true, latest: r.new_version })
      else setUpdate({ error: r.error })
    }).catch(e => setUpdate({ error: String(e) }))
  }

  const vText = ver ? `v${ver.version.replace(/^v/, '')}` : '...'

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>
          Version
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
            MonoAgent {vText}
          </span>
          <button
            onClick={check}
            disabled={update?.checking || update?.updating}
            style={{
              background: 'rgba(0,180,216,.15)',
              color: '#00b4d8',
              border: '1px solid rgba(0,180,216,.2)',
              borderRadius: 4,
              padding: '2px 10px',
              cursor: 'pointer',
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              opacity: (update?.checking || update?.updating) ? 0.5 : 1,
            }}
          >
            {update?.checking ? 'Checking...' : 'Check for updates'}
          </button>
        </div>
      </div>
      {update?.upToDate && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00f5d4', marginTop: 6 }}>
          You're on the latest version.
        </div>
      )}
      {update?.available && !update.updating && !update.done && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, marginTop: 6, display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ color: '#fbbf24' }}>{update.latest} is available</span>
          <button onClick={doUpdate} style={{
            background: '#00b4d8', color: '#fff', border: 'none', borderRadius: 4,
            padding: '3px 12px', cursor: 'pointer', fontFamily: 'var(--font-mono)', fontSize: 10,
          }}>Install Update</button>
        </div>
      )}
      {update?.updating && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00b4d8', marginTop: 6 }}>
          Updating...
        </div>
      )}
      {update?.done && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00f5d4', marginTop: 6 }}>
          Updated to {update.latest} — restart the app to apply.
        </div>
      )}
      {update?.error && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--red)', marginTop: 6 }}>
          {update.error}
        </div>
      )}
    </div>
  )
}

// ── ExportRow ───────────────────────────────────────────────────────────────

function ExportRow() {
  const [state, setState] = useState(null) // { exporting } | { done, summary } | { error }

  function doExport() {
    setState({ exporting: true })
    api.exportData().then(r => {
      if (!r || r.cancelled) { setState(null); return }
      setState({ done: true, summary: `Exported ${r.people_count} people and ${r.actions_count} actions to ${r.output_dir}` })
    }).catch(e => setState({ error: String(e) }))
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>
          Data Export
        </span>
        <button
          onClick={doExport}
          disabled={state?.exporting}
          style={{
            display: 'flex', alignItems: 'center', gap: 5,
            background: 'rgba(0,180,216,.15)',
            color: '#00b4d8',
            border: '1px solid rgba(0,180,216,.2)',
            borderRadius: 4,
            padding: '2px 10px',
            cursor: 'pointer',
            fontFamily: 'var(--font-mono)',
            fontSize: 9,
            opacity: state?.exporting ? 0.5 : 1,
          }}
        >
          <Download size={10} />
          {state?.exporting ? 'Exporting...' : 'Export to JSON'}
        </button>
      </div>
      {state?.done && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#00f5d4', marginTop: 6, wordBreak: 'break-all' }}>
          {state.summary}
        </div>
      )}
      {state?.error && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--red)', marginTop: 6 }}>
          {state.error}
        </div>
      )}
    </div>
  )
}

// ── AssistantToolsSection ────────────────────────────────────────────────────

// GX2 contract: StreamAgentChat takes monoagentTools + allowRuns flags (both
// default ON — see lib/assistantTools.js). Persisted to localStorage and
// read by the AI chat panels at send time; a visible indicator in the panel
// shows when tools are active. Toggling applies to the next message sent.
function AssistantToolsSection() {
  const [tools, setTools] = useState(() => getAssistantTools())
  const [allowRuns, setAllowRuns] = useState(() => getAssistantAllowRuns())

  const toggleTools = (on) => {
    setAssistantTools(on)
    setTools(on)
    if (!on) setAllowRuns(false)
  }

  const toggleAllowRuns = (on) => {
    setAssistantAllowRuns(on)
    setAllowRuns(on && tools)
  }

  const checkbox = (checked, disabled, onChange) => ({
    type: 'checkbox',
    checked, disabled,
    onChange: e => onChange(e.target.checked),
    style: { marginTop: 2, accentColor: '#00b4d8', flexShrink: 0 },
  })

  return (
    <div style={{
      background: 'var(--surface)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-lg)',
      padding: '16px 20px',
      display: 'flex', flexDirection: 'column', gap: 12,
      marginBottom: 16,
    }}>
      <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer' }}>
        <input {...checkbox(tools, false, toggleTools)} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
          Enable monoagent tools (workflows, people, vault metadata)
        </span>
      </label>
      <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: tools ? 'pointer' : 'default', opacity: tools ? 1 : 0.45, paddingLeft: 24 }}>
        <input {...checkbox(allowRuns, !tools, toggleAllowRuns)} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
          Allow running workflows/actions from chat
        </span>
      </label>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 9.5, color: 'var(--text-muted)', lineHeight: 1.6 }}>
        Both options are on by default. The AI assistant can read your workflows,
        people and vault metadata; the second option additionally lets it trigger
        workflow runs and actions from chat.
      </div>
    </div>
  )
}

// ── QuickAccessCard ─────────────────────────────────────────────────────────

function QuickAccessCard({ icon: Icon, title, description, stats, onClick }) {
  const [hov, setHov] = useState(false)
  return (
    <div
      onClick={onClick}
      onMouseEnter={() => setHov(true)}
      onMouseLeave={() => setHov(false)}
      style={{
        flex: 1,
        background: hov ? 'var(--elevated)' : 'var(--surface)',
        border: hov ? '1px solid var(--border-bright)' : '1px solid var(--border)',
        borderRadius: 'var(--radius-lg)',
        padding: '20px 18px',
        cursor: 'pointer',
        transition: 'all var(--transition)',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{
          width: 34, height: 34, borderRadius: 'var(--radius)',
          background: 'rgba(0,180,216,.1)', border: '1px solid rgba(0,180,216,.15)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <Icon size={16} style={{ color: '#00b4d8' }} />
        </div>
        <div>
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>
            {title}
          </div>
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', marginTop: 2 }}>
            {description}
          </div>
        </div>
      </div>
      {stats && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-dim)', paddingLeft: 44 }}>
          {stats}
        </div>
      )}
    </div>
  )
}

// ── Main ─────────────────────────────────────────────────────────────────────

// LanguageSection is a minimal pilot locale switcher — a real Settings UI
// might style this like the other cards, but the goal here is proving the
// i18next changeLanguage() mechanism works end-to-end, not visual polish.
function LanguageSection() {
  const { t, i18n } = useTranslation()
  return (
    <div style={{
      background: 'var(--surface)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-lg)',
      padding: '16px 20px',
      display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 16,
      marginBottom: 28,
    }}>
      <div>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', marginBottom: 2 }}>
          {t('settings.language.label')}
        </div>
        <div style={{ fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--text-muted)' }}>
          {t('settings.language.hint')}
        </div>
      </div>
      <select
        value={i18n.resolvedLanguage || i18n.language}
        onChange={e => i18n.changeLanguage(e.target.value)}
        style={{
          // backgroundColor, not the `background` shorthand: a shorthand here
          // would reset backgroundImage below to none regardless of order.
          backgroundColor: 'var(--elevated)', color: 'var(--text-primary)',
          border: '1px solid var(--border)', borderRadius: 6,
          padding: '6px 28px 6px 10px', fontFamily: 'var(--font-mono)', fontSize: 12,
          // WebKitGTK draws <select> with native GTK chrome (light bg, dark
          // text) unless appearance is explicitly reset — see AIChatPanel.jsx.
          appearance: 'none',
          backgroundImage: "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%2300b4d8' stroke-width='2'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E\")",
          backgroundRepeat: 'no-repeat',
          backgroundPosition: 'right 10px center',
        }}
      >
        <option value="en">English</option>
        <option value="es">Español</option>
      </select>
    </div>
  )
}

export default function Settings({ onNavigate }) {
  const { t } = useTranslation()
  const [dbPath, setDbPath] = useState('')
  const [dbConnected, setDbConnected] = useState(false)
  const [connCount, setConnCount] = useState(null)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async () => {
    api.getDBPath().then(p => setDbPath(p || ''))
    api.isDBConnected().then(c => setDbConnected(!!c))
    // Count active connections
    return api.listConnections('').then(conns => {
      if (Array.isArray(conns)) {
        const active = conns.filter(c => (c.Status || c.status) === 'active').length
        setConnCount(`${active} active connection${active !== 1 ? 's' : ''}`)
      }
    }).catch(() => {})
  }, [])

  useEffect(() => { load() }, [load])

  const handleRefresh = async () => {
    setRefreshing(true)
    try { await load() } finally { setRefreshing(false) }
  }

  return (
    <>
      <div className="page-header">
        <div className="page-header-left">
          <div className="page-title">{t('settings.title')}</div>
          <div className="page-subtitle">{t('settings.subtitle')}</div>
        </div>
        <div className="page-header-right">
          <RefreshButton onClick={handleRefresh} loading={refreshing} />
        </div>
      </div>

      <div className="page-body">
        {/* Quick access cards */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
            {t('settings.quickAccess')}
          </span>
          <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
        </div>

        <div style={{ display: 'flex', gap: 12, marginBottom: 28 }}>
          <QuickAccessCard
            icon={Link2}
            title={t('settings.connectionsTitle')}
            description={t('settings.connectionsDesc')}
            stats={connCount}
            onClick={() => onNavigate?.('connections')}
          />
          <QuickAccessCard
            icon={Brain}
            title={t('settings.aiProvidersTitle')}
            description={t('settings.aiProvidersDesc')}
            onClick={() => onNavigate?.('ai')}
          />
        </div>

        {/* Language */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
            {t('settings.language.sectionTitle')}
          </span>
          <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
        </div>

        <LanguageSection />

        {/* Assistant tool access */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
            Assistant Tool Access
          </span>
          <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
        </div>

        <AssistantToolsSection />

        {/* Application Info */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
            Application Info
          </span>
          <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
        </div>

        <div style={{
          background: 'var(--surface)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius-lg)',
          padding: '16px 20px',
          display: 'flex', flexDirection: 'column', gap: 14,
          marginBottom: 16,
        }}>
          {/* DB path */}
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16 }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1, flexShrink: 0, paddingTop: 1 }}>
              Database Path
            </span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', wordBreak: 'break-all', textAlign: 'right' }}>
              {dbPath || '—'}
            </span>
          </div>

          {/* DB status */}
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>
              Database Status
            </span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span style={{
                width: 7, height: 7, borderRadius: '50%',
                background: dbConnected ? 'var(--green-neon)' : 'var(--red)',
                boxShadow: dbConnected ? '0 0 5px var(--green-neon)' : 'none',
              }} />
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: dbConnected ? 'var(--green-neon)' : 'var(--red)' }}>
                {dbConnected ? 'Connected' : 'Disconnected'}
              </span>
            </div>
          </div>

          {/* Export people/actions as JSON */}
          <ExportRow />

          {/* App version + update */}
          <VersionRow />
        </div>

        {/* Note */}
        <div style={{
          fontFamily: 'var(--font-body)',
          fontSize: 11,
          color: 'var(--text-muted)',
          lineHeight: 1.6,
          padding: '10px 14px',
          background: 'rgba(0,245,212,.04)',
          border: '1px solid rgba(0,245,212,.12)',
          borderRadius: 'var(--radius)',
        }}>
          All authentication is managed in <strong style={{ color: 'var(--text-secondary)', cursor: 'pointer' }} onClick={() => onNavigate?.('connections')}>Connections</strong>.
          OAuth credentials, API keys, and browser sessions are configured there.
        </div>
      </div>
    </>
  )
}
