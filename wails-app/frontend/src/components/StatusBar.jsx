import { useState, useEffect } from 'react'
import { MessageSquare } from 'lucide-react'
import { GetVersion, CheckForUpdate, AppSelfUpdate } from '../wailsjs/go/main/App'
import { subscribeEvent } from '../services/api.js'

export default function StatusBar({ stats, dbConnected, chatOpen, onToggleChat }) {
  const running = stats?.executions_by_status?.RUNNING || 0
  const total   = stats?.total_workflows || 0
  const people  = stats?.total_people || 0
  const sessions = stats?.active_sessions || 0

  const [ver, setVer] = useState(null)
  const [update, setUpdate] = useState(null)   // { checking, available, latest, error, updating, progress }

  useEffect(() => {
    GetVersion().then(v => setVer(v)).catch(() => {})
  }, [])

  // Listen for update progress events
  useEffect(() => {
    const off = subscribeEvent('update:progress', msg => {
      setUpdate(u => ({ ...u, progress: msg }))
    })
    return off
  }, [])

  function handleVersionClick() {
    if (update?.updating) return
    setUpdate({ checking: true })
    CheckForUpdate()
      .then(info => {
        if (info.error) {
          setUpdate({ error: info.error })
        } else if (info.update_available) {
          setUpdate({ available: true, latest: info.latest_version, url: info.release_url })
        } else {
          setUpdate({ upToDate: true })
          setTimeout(() => setUpdate(null), 3000)
        }
      })
      .catch(e => setUpdate({ error: String(e) }))
  }

  function handleUpdate() {
    // AppSelfUpdate replaces the running GUI app binary itself and
    // restarts it -- SelfUpdate (used here previously) only replaces the
    // monoagentcli CLI sidecar, which is why clicking "Update" reported
    // success and a new version number but the app's own displayed
    // version (versionText below, from GetVersion()) never changed even
    // after a manual restart: the actual running executable was never
    // touched. Settings.jsx already calls AppSelfUpdate correctly; this
    // was the one place still calling the wrong function.
    setUpdate(u => ({ ...u, updating: true, progress: 'Starting update...' }))
    AppSelfUpdate()
      .then(result => {
        if (result.success) {
          setUpdate({ done: true, latest: result.new_version })
        } else {
          setUpdate({ error: result.error })
        }
      })
      .catch(e => setUpdate({ error: String(e) }))
  }

  const versionText = ver ? `v${ver.version.replace(/^v/, '')}` : 'v…'

  return (
    <div className="status-bar">
      <div className="status-bar-item">
        <span className={`status-dot ${dbConnected ? 'connected' : 'disconnected'}`}
              style={{ width: 5, height: 5 }} />
        <span>{dbConnected ? 'Connected' : 'Offline'}</span>
      </div>
      <div className="status-bar-item">
        Workflows: <span>{total}</span>
      </div>
      {running > 0 && (
        <div className="status-bar-item" style={{ color: 'var(--teal)' }}>
          <span className="live-dot" style={{ width: 5, height: 5 }} />
          <span style={{ color: 'inherit' }}>{running} running</span>
        </div>
      )}
      <div className="status-bar-item">
        People: <span>{people}</span>
      </div>
      <div className="status-bar-item">
        Sessions: <span>{sessions}</span>
      </div>

      {/* Version + update indicator */}
      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8, fontSize: 10 }}>
        {/* Update status popover */}
        {update && (
          <div style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            display: 'flex',
            alignItems: 'center',
            gap: 6,
          }}>
            {update.checking && (
              <span style={{ color: 'var(--text-muted)' }}>Checking...</span>
            )}
            {update.upToDate && (
              <span style={{ color: '#00f5d4' }}>Up to date</span>
            )}
            {update.error && (
              <span style={{ color: 'var(--red)', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                    title={update.error}>
                Update failed
              </span>
            )}
            {update.available && !update.updating && !update.done && (
              <>
                <span style={{ color: '#fbbf24' }}>{update.latest} available</span>
                <button
                  onClick={handleUpdate}
                  style={{
                    background: '#00b4d8',
                    color: '#fff',
                    border: 'none',
                    borderRadius: 3,
                    padding: '2px 8px',
                    cursor: 'pointer',
                    fontFamily: 'var(--font-mono)',
                    fontSize: 9,
                  }}
                >
                  Update
                </button>
                <button
                  onClick={() => setUpdate(null)}
                  style={{
                    background: 'transparent',
                    color: 'var(--text-muted)',
                    border: 'none',
                    cursor: 'pointer',
                    fontSize: 10,
                    padding: '0 2px',
                  }}
                >✕</button>
              </>
            )}
            {update.updating && (
              <span style={{ color: '#00b4d8' }}>{update.progress || 'Updating...'}</span>
            )}
            {update.done && (
              <span style={{ color: '#00f5d4' }}>Updated to {update.latest} — restart to apply</span>
            )}
          </div>
        )}

        <span
          onClick={handleVersionClick}
          title="Click to check for updates"
          style={{
            color: 'var(--text-dim)',
            cursor: 'pointer',
            userSelect: 'none',
            transition: 'color .15s',
          }}
          onMouseEnter={e => e.currentTarget.style.color = '#00b4d8'}
          onMouseLeave={e => e.currentTarget.style.color = 'var(--text-dim)'}
        >
          MonoAgent · {versionText}
        </span>

        {/* AI Assistant toggle — small icon at the far right of the bar,
            not a separate floating overlay (that kept colliding with
            page-specific top-right controls no matter where it was placed;
            see App.jsx). Only rendered when a handler is actually wired
            up, so a caller that doesn't pass onToggleChat gets no dead
            button. */}
        {onToggleChat && (
          <button
            onClick={onToggleChat}
            title={chatOpen ? 'Close AI Assistant' : 'Open AI Assistant'}
            aria-label={chatOpen ? 'Close AI Assistant' : 'Open AI Assistant'}
            style={{
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              width: 18, height: 18, padding: 0,
              background: 'transparent',
              border: 'none',
              borderRadius: 3,
              color: chatOpen ? '#00b4d8' : 'var(--text-dim)',
              cursor: 'pointer',
              transition: 'color .15s',
            }}
            onMouseEnter={e => { if (!chatOpen) e.currentTarget.style.color = '#00b4d8' }}
            onMouseLeave={e => { if (!chatOpen) e.currentTarget.style.color = 'var(--text-dim)' }}
          >
            <MessageSquare size={12} />
          </button>
        )}
      </div>
    </div>
  )
}
