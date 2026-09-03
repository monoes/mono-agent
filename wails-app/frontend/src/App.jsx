import { useState, useEffect, useCallback, useRef } from 'react'
import Sidebar from './components/Sidebar.jsx'
import StatusBar from './components/StatusBar.jsx'
import Toasts from './components/Toasts.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import ConfirmHost from './components/ConfirmDialog.jsx'
import AIChatPanel from './components/AIChatPanel.jsx'
import Dashboard from './pages/Dashboard.jsx'
import People from './pages/People.jsx'
import Profile from './pages/Profile.jsx'
import PostDetail from './pages/PostDetail.jsx'
import Connections from './pages/Connections.jsx'
import Communications from './pages/Communications.jsx'
import Agents from './pages/Agents.jsx'
import Orgs from './pages/Orgs.jsx'
import Logs from './pages/Logs.jsx'
import NodeRunner from './pages/NodeRunner.jsx'
import SettingsPage from './pages/Settings.jsx'
import ImageVault from './pages/ImageVault.jsx'
import Vault from './pages/Vault.jsx'
import { api, onLogEntry, onOrgDesignUpdated, subscribeEvent } from './services/api.js'

export default function App() {
  const [activePage, setActivePage] = useState('dashboard')
  const [navData, setNavData] = useState(null) // extra data passed to a page (e.g., executionId)
  const [profileId, setProfileId] = useState(null)
  const [postId, setPostId] = useState(null)
  const [dbConnected, setDbConnected] = useState(false)
  const [stats, setStats] = useState(null)
  const [logs, setLogs] = useState([])
  const [peopleRefreshKey, setPeopleRefreshKey] = useState(0)
  // Pages mount lazily on first visit, then stay mounted forever after —
  // avoids paying every page's initial data-fetch cost at app startup while
  // still keeping state alive once a page has actually been opened.
  const [visitedPages, setVisitedPages] = useState(() => new Set(['dashboard']))
  // Global AI Assistant chat — lifted here (rather than owned by whichever
  // page happens to render it) so it's reachable from anywhere via the
  // always-visible toggle in the top bar, and so opening/closing it, its
  // selected runtime, and its conversation all survive navigating between
  // pages. This is the general assistant (canvasMode=false); the Workflows
  // editor's own chat toggle is a different tool — a workflow-builder
  // assistant scoped to the currently open canvas — and stays separate.
  const [globalChatOpen, setGlobalChatOpen] = useState(false)
  const [globalChatRuntime, setGlobalChatRuntime] = useState('')

  const openGlobalChat = useCallback((runtimeId) => {
    if (runtimeId) setGlobalChatRuntime(runtimeId)
    setGlobalChatOpen(true)
  }, [])

  const openProfile = useCallback((id) => {
    setProfileId(id)
    setActivePage('profile')
  }, [])

  const closeProfile = useCallback(() => {
    setActivePage('people')
    setProfileId(null)
  }, [])

  const openPost = useCallback((id) => {
    setPostId(id)
    setActivePage('postDetail')
  }, [])

  const closePost = useCallback(() => {
    setPostId(null)
    setActivePage('profile')
  }, [])

  const navigate = useCallback((page, data) => {
    if (page !== 'postDetail') setPostId(null)
    if (page !== 'profile' && page !== 'postDetail') setProfileId(null)
    setNavData(data || null)
    setActivePage(page)
  }, [])

  // Marks whatever page is active as visited — deliberately keyed off
  // activePage itself (not routed through navigate()) so it also catches
  // the couple of places that call setActivePage directly (Settings'
  // onNavigate, closeProfile) instead of the navigate() wrapper.
  useEffect(() => {
    setVisitedPages(prev => (prev.has(activePage) ? prev : new Set(prev).add(activePage)))
  }, [activePage])

  // Bring the user to a newly-created org even if they've never visited
  // Orgs this session. Pages below only render/mount once visitedPages
  // has seen their id (see persistentPages filter further down) — OrgsPanel
  // has its own onOrgDesignUpdated listener that auto-selects a new org,
  // but that listener doesn't exist until OrgsPanel has mounted at least
  // once, which never happens if the user went straight to Chat and asked
  // it to build an org there. This top-level listener is always mounted,
  // so it's the only thing that can react the very first time. It tracks
  // known org names itself (OrgsPanel's own rail state isn't reachable
  // from here) and hands off the name to select via pendingOrgSelect,
  // which Orgs/OrgsPanel consumes once mounted (including on this exact
  // mount, triggered by the navigate() call below).
  const knownOrgNamesRef = useRef(null) // null = not yet loaded
  const [pendingOrgSelect, setPendingOrgSelect] = useState(null)
  useEffect(() => {
    api.listOrgDesigns().then(res => {
      const items = Array.isArray(res) ? res : (res?.items || [])
      knownOrgNamesRef.current = new Set(items.map(o => o.name))
    }).catch(() => { knownOrgNamesRef.current = new Set() })
    const off = onOrgDesignUpdated((payload) => {
      if (!payload?.orgName || payload.deleted) return
      if (knownOrgNamesRef.current?.has(payload.orgName)) return
      knownOrgNamesRef.current?.add(payload.orgName)
      setPendingOrgSelect(payload.orgName)
      navigate('orgs')
    })
    return off
  }, [navigate])

  // Initial data load
  useEffect(() => {
    const checkDB = async () => {
      const connected = await api.isDBConnected()
      setDbConnected(!!connected)
    }
    const loadStats = async () => {
      const s = await api.getDashboardStats()
      if (s) setStats(s)
    }
    const loadLogs = async () => {
      const l = await api.getLogs()
      if (l) setLogs(l)
    }
    checkDB()
    loadStats()
    loadLogs()
  }, [])

  // Exposed to Logs.jsx's Refresh button — re-fetches the full log buffer
  // from the backend (in addition to the live event stream below, which can
  // miss entries if the app was backgrounded).
  const refreshLogs = useCallback(async () => {
    const l = await api.getLogs()
    if (l) setLogs(l)
  }, [])

  // Live log streaming
  useEffect(() => {
    const off = onLogEntry((entry) => {
      setLogs(prev => {
        const next = [...prev, entry]
        return next.length > 500 ? next.slice(-500) : next
      })
    })
    return off
  }, [])

  // Workflow-run completion refresh
  useEffect(() => {
    const off = subscribeEvent('workflow:complete', async () => {
      const s = await api.getDashboardStats()
      if (s) setStats(s)
      setPeopleRefreshKey(k => k + 1)
    })
    return off
  }, [])

  const refreshStats = useCallback(async () => {
    const s = await api.getDashboardStats()
    if (s) setStats(s)
  }, [])

  // These pages stay mounted for the lifetime of the app once first
  // visited — switching tabs only toggles CSS visibility, it never
  // unmounts them — so per-page state (scanned runtimes, open chat panel,
  // selected org, canvas edits, filters, scroll position, …) survives
  // navigating away and back. Without this, every tab switch destroyed and
  // recreated the page component from scratch, discarding all local state
  // and — for pages like Agents/Orgs that fetch on mount — re-running
  // their (multi-second) initial data load every single time.
  const persistentPages = {
    dashboard: <Dashboard stats={stats} onRefresh={refreshStats} onNavigate={navigate} />,
    noderunner: <NodeRunner onNavigate={navigate} navData={navData} />,
    people:    <People key={peopleRefreshKey} onProfile={openProfile} />,
    communications: <Communications onProfile={openProfile} />,
    connections: <Connections onRefresh={refreshStats} />,
    vault: <ImageVault />,
    secretsVault: <Vault />,
    ai: <Agents onOpenChat={openGlobalChat} />,
    orgs: <Orgs isActive={activePage === 'orgs'} onNavigate={navigate} pendingSelectOrgName={pendingOrgSelect} onConsumePendingSelect={() => setPendingOrgSelect(null)} />,
    logs:      <Logs logs={logs} onClear={() => { api.clearLogs(); setLogs([]) }} onRefresh={refreshLogs} />,
    settings:  <SettingsPage onNavigate={setActivePage} />,
  }

  // Detail views keyed by a changing id (which profile/post) — these SHOULD
  // reset when you open a different record, so they stay conditionally
  // rendered rather than kept alive like the tab pages above.
  const detailPages = {
    profile:   <Profile id={profileId} onBack={closeProfile} onOpenURL={api.openURL} onOpenPost={openPost} />,
    postDetail: <PostDetail id={postId} onBack={closePost} onOpenURL={api.openURL} />,
  }

  const isDetailPage = activePage in detailPages
  // Guard against an unrecognized activePage (e.g. a stale/typo'd nav
  // target) — fall back to showing dashboard rather than a blank screen.
  const effectiveActivePage = (activePage in persistentPages || isDetailPage) ? activePage : 'dashboard'

  return (
    <div className="app-layout">
      <Sidebar
        activePage={activePage}
        onNavigate={navigate}
        stats={stats}
        dbConnected={dbConnected}
      />
      <div style={{ display: 'flex', flexDirection: 'row', minWidth: 0, minHeight: 0, overflow: 'hidden' }}>
        <main className="main-content">
          <ErrorBoundary>
            {Object.entries(persistentPages)
              .filter(([id]) => visitedPages.has(id))
              .map(([id, el]) => (
                <div
                  key={id}
                  style={{
                    display: effectiveActivePage === id ? 'flex' : 'none',
                    flexDirection: 'column',
                    height: '100%',
                    overflow: 'hidden',
                  }}
                >
                  {el}
                </div>
              ))}
            {isDetailPage && detailPages[activePage]}
          </ErrorBoundary>
        </main>

        {/* Global AI Assistant — always available, opened/closed via the
            fixed toggle button below; state and conversation persist across
            page navigation since this is mounted once at the App level. */}
        <AIChatPanel
          workflowID="general"
          canvasMode={false}
          isOpen={globalChatOpen}
          initialRuntime={globalChatRuntime}
          onClose={() => setGlobalChatOpen(false)}
        />
      </div>

      {/* The global chat toggle lives inside StatusBar itself now (bottom
          bar, far right) — a small icon there rather than a separate
          floating overlay button, which kept colliding with whatever
          page-specific controls happened to also live near a screen
          corner (the Workflow editor's own toolbar, Dashboard's
          Refresh/Workflow Editor buttons, ...) no matter where it was
          placed. */}
      <StatusBar
        stats={stats}
        dbConnected={dbConnected}
        chatOpen={globalChatOpen}
        onToggleChat={() => setGlobalChatOpen(v => !v)}
      />
      <Toasts />
      <ConfirmHost />
    </div>
  )
}
