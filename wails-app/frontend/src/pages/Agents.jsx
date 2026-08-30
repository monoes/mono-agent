import { useState, useEffect, useCallback } from 'react'
import { RefreshCw, Bot, MessageSquare, KeyRound, Terminal } from 'lucide-react'
import { api } from '../services/api.js'
import AIProviders from './AIProviders.jsx'

function statusColor(installed) {
  return installed ? 'var(--green-neon)' : 'var(--text-muted)'
}

function RuntimeTile({ agent, onChat }) {
  const [hov, setHov] = useState(false)
  return (
    <div
      onMouseEnter={() => setHov(true)}
      onMouseLeave={() => setHov(false)}
      style={{
        background: agent.installed ? 'linear-gradient(145deg,var(--elevated),var(--surface))' : 'var(--surface)',
        border: agent.installed ? '1px solid var(--border-active)' : hov ? '1px solid var(--border-bright)' : '1px solid var(--border)',
        borderRadius: 'var(--radius-lg)',
        padding: '16px 12px 12px',
        display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8,
        transition: 'all var(--transition)',
        boxShadow: agent.installed ? 'var(--shadow-glow)' : 'none',
        minWidth: 0,
      }}
    >
      <Bot size={22} style={{ color: agent.installed ? 'var(--cyan)' : 'var(--text-muted)', opacity: agent.installed ? 1 : 0.5 }} />
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 600, color: agent.installed ? 'var(--text)' : 'var(--text-secondary)', textAlign: 'center' }}>
        {agent.id}
      </span>
      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <span style={{ width: 6, height: 6, borderRadius: '50%', background: statusColor(agent.installed), boxShadow: agent.installed ? '0 0 5px var(--green-neon)' : 'none', flexShrink: 0 }} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)' }}>
          {agent.installed ? (agent.version || 'installed') : 'not installed'}
        </span>
      </div>
      {agent.installed ? (
        <button className="btn btn-sm" onClick={() => onChat(agent.id)} style={{ gap: 4, marginTop: 2 }}>
          <MessageSquare size={11} /> Chat
        </button>
      ) : (
        agent.install_hint && (
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 8.5, color: 'var(--text-muted)', textAlign: 'center', lineHeight: 1.4, maxWidth: 140 }}>
            {agent.install_hint}
          </div>
        )
      )}
    </div>
  )
}

// `agent scan` is a genuinely slow operation (~6-7s: it spawns monomind,
// which itself spawns a handshake + a parallel probe of every known agent
// CLI binary). Within one running app session, keeping Agents mounted
// (App.jsx's keep-alive navigation) means this only runs once. But every
// fresh app launch is a new session with nothing cached — so we also
// persist the last scan result to localStorage and paint it immediately on
// mount, then silently refresh in the background. The user sees last-known
// state instantly instead of a multi-second spinner on every single app
// start, and the display self-corrects within a few seconds if anything
// changed (a runtime got installed/removed, monomind's version changed).
const SCAN_CACHE_KEY = 'monoagent:agentScanCache:v1'

function readScanCache() {
  try {
    const raw = localStorage.getItem(SCAN_CACHE_KEY)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

function writeScanCache(res) {
  try {
    localStorage.setItem(SCAN_CACHE_KEY, JSON.stringify({ res, cachedAt: Date.now() }))
  } catch { /* localStorage unavailable/full — cache is best-effort */ }
}

export default function Agents({ onOpenChat }) {
  const [tab, setTab] = useState('agents')
  const cached = readScanCache()
  const [agents, setAgents] = useState(() => (cached && !cached.res?.error) ? (cached.res.agents || []) : [])
  const [scanError, setScanError] = useState(() => cached?.res?.error || null)
  // Only show the blocking spinner when there is truly nothing to paint yet
  // (first-ever run on this machine, or a cleared cache). Otherwise show
  // stale-but-instant data while refreshing quietly underneath it.
  const [loading, setLoading] = useState(() => !cached)

  const loadAgents = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const res = await api.scanAgentRuntimes()
      if (!res || res.error) {
        setScanError(res?.error || 'Unable to reach monomind.')
        setAgents([])
      } else {
        setScanError(null)
        setAgents(res.agents || [])
      }
      writeScanCache(res)
    } finally {
      if (!silent) setLoading(false)
    }
  }, [])

  // First mount: if we already painted from cache, refresh silently (no
  // spinner) — otherwise this is the one real blocking load.
  useEffect(() => { loadAgents(!!cached) }, [loadAgents])

  const installedCount = agents.filter(a => a.installed).length

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <div className="page-header">
          <div className="page-header-left">
            <div className="page-title">Agents</div>
            <div className="page-subtitle">
              {loading ? 'Loading…' : scanError ? 'monomind unavailable' : `${installedCount} / ${agents.length} runtimes installed`}
            </div>
          </div>
          <div className="page-header-right" style={{ display: 'flex', gap: 6 }}>
            <button className="btn btn-ghost btn-sm" onClick={() => loadAgents()} style={{ gap: 5 }}><RefreshCw size={12} /> Refresh</button>
          </div>
        </div>

        <div style={{ display: 'flex', gap: 4, padding: '8px 16px 0', borderBottom: '1px solid var(--border)' }}>
          <button
            onClick={() => setTab('agents')}
            style={{
              display: 'flex', alignItems: 'center', gap: 5, fontFamily: 'var(--font-mono)', fontSize: 11,
              padding: '7px 12px', borderRadius: 'var(--radius) var(--radius) 0 0',
              background: tab === 'agents' ? 'var(--surface)' : 'transparent',
              border: tab === 'agents' ? '1px solid var(--border)' : '1px solid transparent',
              borderBottom: tab === 'agents' ? '1px solid var(--surface)' : 'none',
              color: tab === 'agents' ? 'var(--text)' : 'var(--text-muted)', cursor: 'pointer',
              marginBottom: -1,
            }}
          >
            <Terminal size={11} /> Agents
          </button>
          <button
            onClick={() => setTab('providers')}
            style={{
              display: 'flex', alignItems: 'center', gap: 5, fontFamily: 'var(--font-mono)', fontSize: 11,
              padding: '7px 12px', borderRadius: 'var(--radius) var(--radius) 0 0',
              background: tab === 'providers' ? 'var(--surface)' : 'transparent',
              border: tab === 'providers' ? '1px solid var(--border)' : '1px solid transparent',
              borderBottom: tab === 'providers' ? '1px solid var(--surface)' : 'none',
              color: tab === 'providers' ? 'var(--text)' : 'var(--text-muted)', cursor: 'pointer',
              marginBottom: -1,
            }}
          >
            <KeyRound size={11} /> Providers (legacy)
          </button>
        </div>

        {tab === 'agents' ? (
          <div className="page-body" style={{ flex: 1, overflow: 'auto' }}>
            {loading ? (
              <div className="empty-state"><div className="spinner" /></div>
            ) : scanError ? (
              <div className="empty-state">
                <div className="empty-state-icon"><Bot size={36} /></div>
                <div className="empty-state-title">
                  {/not found/i.test(scanError) ? 'monomind not installed' : 'monomind is out of date'}
                </div>
                <div className="empty-state-desc" style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <span>
                    Agents run through the local monomind engine. {/not found/i.test(scanError)
                      ? <>Install it with <code>npm install -g monomind</code>.</>
                      : <>Update it with <code>npm install -g monomind@latest</code>.</>}
                  </span>
                  <code style={{ fontSize: 10, color: 'var(--text-muted)', wordBreak: 'break-word', textAlign: 'left' }}>{scanError}</code>
                </div>
              </div>
            ) : agents.length === 0 ? (
              <div className="empty-state">
                <div className="empty-state-icon"><Bot size={36} /></div>
                <div className="empty-state-title">No agent runtimes detected</div>
                <div className="empty-state-desc">monomind is installed but found no known agent CLIs on this machine.</div>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))', gap: 10, paddingBottom: 24 }}>
                {agents.map(a => (
                  <RuntimeTile key={a.id} agent={a} onChat={onOpenChat} />
                ))}
              </div>
            )}
          </div>
        ) : (
          <div style={{ flex: 1, overflow: 'hidden' }}>
            <AIProviders embedded />
          </div>
        )}
      </div>
    </div>
  )
}
