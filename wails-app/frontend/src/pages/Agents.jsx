import { useState, useEffect, useCallback } from 'react'
import { RefreshCw, Bot, MessageSquare, Download, Copy, Loader2, ArrowUpCircle } from 'lucide-react'
import { cachedAgentScan, invalidateAgentScan } from '../lib/agentRuntimes.js'
import { installRuntime, runHealth } from '../lib/health.js'
import { api } from '../services/api.js'
import { confirm } from '../components/ConfirmDialog.jsx'
import MonomindInitPrompt from '../components/MonomindInitPrompt.jsx'

function statusColor(installed) {
  return installed ? 'var(--green-neon)' : 'var(--text-muted)'
}

// installKind is monomind's structured recipe kind (agent scan protocol
// rev 9: npm | script | manual). Older monomind sends none — the Install
// button is offered then, and monoagentcli decides (it refuses manual ones).
function installKind(agent) {
  return agent.install?.kind || 'unknown'
}

const tileButton = { gap: 4, marginTop: 2, fontSize: 10 }

function RuntimeTile({ agent, onChat, onInstall, job }) {
  const [hov, setHov] = useState(false)
  const kind = installKind(agent)
  const running = job?.running
  return (
    <div
      data-runtime={agent.id}
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
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', justifyContent: 'center' }}>
        {agent.installed && (
          <button className="btn btn-sm" onClick={() => onChat(agent.id)} style={tileButton}>
            <MessageSquare size={11} /> Chat
          </button>
        )}
        {running ? (
          <button className="btn btn-sm" disabled style={tileButton}>
            <Loader2 size={11} className="spin" /> {agent.installed ? 'Updating…' : 'Installing…'}
          </button>
        ) : kind === 'manual' ? (
          !agent.installed && (
            <button className="btn btn-sm btn-ghost" onClick={() => onInstall(agent, 'copy')} title={agent.install_hint} style={tileButton}>
              <Copy size={11} /> Copy steps
            </button>
          )
        ) : agent.installed ? (
          <button className="btn btn-sm btn-ghost" onClick={() => onInstall(agent, 'update')} title={agent.install_hint} style={tileButton}>
            <ArrowUpCircle size={11} /> Update
          </button>
        ) : (
          <button className="btn btn-sm btn-primary" onClick={() => onInstall(agent, 'install')} title={agent.install_hint} style={tileButton}>
            <Download size={11} /> Install
          </button>
        )}
      </div>
      {agent.installed && agent.login_hint && !job && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 8.5, color: 'var(--text-muted)', textAlign: 'center' }}>
          sign in: {agent.login_hint}
        </div>
      )}
      {!agent.installed && kind === 'manual' && agent.install_hint && !job && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 8.5, color: 'var(--text-muted)', textAlign: 'center', lineHeight: 1.4, maxWidth: 140 }}>
          {agent.install_hint}
        </div>
      )}
      {job && (job.line || job.error || job.done) && (
        <div
          title={job.error || job.line}
          style={{ fontFamily: 'var(--font-mono)', fontSize: 8.5, textAlign: 'center', lineHeight: 1.4, maxWidth: 150, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: job.error ? 'var(--red)' : job.done ? 'var(--green-neon)' : 'var(--text-muted)' }}
        >
          {job.error || (job.done ? (job.message || 'done') : job.line)}
        </div>
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
      // Shared TTL-cached scan — an AIChatPanel first-open right after this
      // page scanned reconciles against the same result, no second 7s scan.
      const res = await cachedAgentScan()
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

  // Independent of the runtime scan above: monomind can be installed
  // globally (scanError below only fires when the binary itself is
  // missing/outdated) while this profile's own folder has never been
  // initialized — a separate, per-profile check.
  const [notInitialized, setNotInitialized] = useState(false)
  useEffect(() => {
    api.isMonomindInitialized().then(v => setNotInitialized(!v))
  }, [])

  // Install / update / copy-steps per runtime — all through monoagentcli
  // (`agent install`), shown inline on the tile.
  const [jobs, setJobs] = useState({})
  const setJob = (id, patch) => setJobs(j => ({ ...j, [id]: patch === null ? undefined : { ...(j[id] || {}), ...patch } }))
  const onInstall = useCallback(async (agent, action) => {
    if (action === 'copy') {
      try { await navigator.clipboard.writeText(agent.install_hint || '') } catch { /* clipboard may be blocked */ }
      setJob(agent.id, { line: 'copied — run it in a terminal', done: false, error: null })
      return
    }
    const script = installKind(agent) === 'script'
    const ok = await confirm(
      <span>
        {action === 'update' ? `Update ${agent.id}? ` : `Install ${agent.id}? `}
        {script ? 'This downloads and runs the vendor\u2019s install script:' : 'This runs:'}
        <code style={{ display: 'block', marginTop: 8, padding: '6px 8px', background: 'rgba(0,0,0,.35)', borderRadius: 4, wordBreak: 'break-all' }}>
          {agent.install_hint}
        </code>
      </span>,
      { title: action === 'update' ? `Update ${agent.id}` : `Install ${agent.id}`, confirmLabel: action === 'update' ? 'Update' : 'Install', danger: false },
    )
    if (!ok) return
    setJob(agent.id, { running: true, line: 'starting…', error: null, done: false })
    const res = await installRuntime(agent.id, action === 'update', line => setJob(agent.id, { line }))
    setJob(agent.id, { running: false, done: res.ok, error: res.ok ? null : res.message, message: res.message })
    invalidateAgentScan()
    loadAgents(true)
    runHealth()
  }, [loadAgents])

  const installedCount = agents.filter(a => a.installed).length

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <div className="page-header">
          <div className="page-header-left">
            <div className="page-title">AI agents</div>
            <div className="page-subtitle">
              {loading ? 'Loading…' : scanError ? 'monomind unavailable' : `${installedCount} / ${agents.length} runtimes installed`}
            </div>
          </div>
          <div className="page-header-right" style={{ display: 'flex', gap: 6 }}>
            <button className="btn btn-ghost btn-sm" onClick={() => { invalidateAgentScan(); loadAgents() }} style={{ gap: 5 }}><RefreshCw size={12} /> Refresh</button>
          </div>
        </div>

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
                  {/* Fallback copy aligned with internal/monomind/find.go's
                      ErrNotFound / update prescriptions; the backend's own
                      error text (which embeds the exact command) is rendered
                      verbatim below. */}
                  Agents run through the local monomind engine. {/not found/i.test(scanError)
                    ? <>Install it with <code>npm install -g @monoes/monomindcli</code>.</>
                    : <>Update it with <code>npm install -g @monoes/monomindcli@latest</code>.</>}
                </span>
                <code style={{ fontSize: 10, color: 'var(--text-muted)', wordBreak: 'break-word', textAlign: 'left' }}>{scanError}</code>
              </div>
            </div>
          ) : notInitialized ? (
            <MonomindInitPrompt onInitialized={() => { setNotInitialized(false); loadAgents() }} />
          ) : agents.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><Bot size={36} /></div>
              <div className="empty-state-title">No agent runtimes detected</div>
              <div className="empty-state-desc">monomind is installed but found no known agent CLIs on this machine.</div>
            </div>
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))', gap: 10, paddingBottom: 24 }}>
              {agents.map(a => (
                <RuntimeTile key={a.id} agent={a} onChat={onOpenChat} onInstall={onInstall} job={jobs[a.id]} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
