import { useState, useEffect, useCallback } from 'react'
import { MessageSquare } from 'lucide-react'
import Sidebar from './components/Sidebar.jsx'
import StatusBar from './components/StatusBar.jsx'
import Toasts from './components/Toasts.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import ConfirmHost from './components/ConfirmDialog.jsx'
import AIChatPanel from './components/AIChatPanel.jsx'
import Dashboard from './pages/Dashboard.jsx'
import Actions from './pages/Actions.jsx'
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
import HumanInLoop from './pages/HumanInLoop.jsx'
import { api, onLogEntry, onActionComplete } from './services/api.js'

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

  // Action completion refresh
  useEffect(() => {
    const off = onActionComplete(async () => {
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
    actions: <Actions onRefresh={refreshStats} />,
    hil: <HumanInLoop />,
    people:    <People key={peopleRefreshKey} onProfile={openProfile} />,
    communications: <Communications onProfile={openProfile} />,
    connections: <Connections onRefresh={refreshStats} />,
    vault: <ImageVault />,
    secretsVault: <Vault />,
    ai: <Agents onOpenChat={openGlobalChat} />,
    orgs: <Orgs />,
    logs:      <Logs logs={logs} onClear={() => { api.clearLogs(); setLogs([]) }} />,
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

      {/* Always-on-top toggle for the global chat — visible on every page,
          only while the panel is closed. Once open, the panel's own header
          has a close button, so this doesn't need to keep floating on top
          of it (which used to land right on top of page toolbars). */}
      {!globalChatOpen && (
        <button
          onClick={() => setGlobalChatOpen(true)}
          title="Open AI Assistant"
          aria-label="Open AI Assistant"
          style={{
            position: 'fixed',
            top: 10,
            right: 12,
            zIndex: 1000,
            width: 32,
            height: 32,
            borderRadius: '50%',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: 'var(--elevated)',
            border: '1px solid var(--border-bright)',
            color: 'var(--text-muted)',
            cursor: 'pointer',
            boxShadow: 'var(--shadow-glow)',
          }}
        >
          <MessageSquare size={15} />
        </button>
      )}

      <StatusBar stats={stats} dbConnected={dbConnected} />
      <Toasts />
      <ConfirmHost />
    </div>
  )
}
