import { useState, useEffect, useCallback, useRef } from 'react'
import {
  X, RefreshCw, Building2, CheckCircle2, XCircle, Circle, Network, Maximize2, Minimize2, Plus,
  MessageCircleQuestion, ShieldAlert, Coins, GitBranch, ScrollText, ListTree, Play, Loader2,
} from 'lucide-react'
import { api, onOrgEvent, onOrgEventsClosed, onOrgDesignUpdated, onOrgRunStatus, notify } from '../services/api.js'
import OrgDesigner from './orgdesigner/OrgDesigner.jsx'
import { KVBlock } from './KVBlock.jsx'
import MonomindInitPrompt from './MonomindInitPrompt.jsx'

const TABS = [
  { id: 'design',    label: 'Design',    icon: Network },
  { id: 'overview',  label: 'Overview',  icon: Circle },
  { id: 'questions', label: 'Questions', icon: MessageCircleQuestion },
  { id: 'gates',     label: 'Gates',     icon: ShieldAlert },
  { id: 'logs',      label: 'Logs',      icon: ScrollText },
  { id: 'costs',     label: 'Costs',     icon: Coins },
  { id: 'flow',      label: 'Flow',      icon: GitBranch },
  { id: 'decisions', label: 'Decisions', icon: ListTree },
]

function statusColor(status) {
  if (status === 'running') return 'var(--green-neon)'
  if (status === 'crashed' || status === 'error') return 'var(--red)'
  if (status === 'paused') return '#eab308'
  return 'var(--text-muted)'
}

function StatusDot({ status }) {
  const c = statusColor(status)
  return <span style={{ width: 6, height: 6, borderRadius: '50%', background: c, boxShadow: c !== 'var(--text-muted)' ? `0 0 5px ${c}` : 'none', flexShrink: 0 }} />
}

function itemsOf(payload) {
  if (!payload) return []
  if (Array.isArray(payload.items)) return payload.items
  if (Array.isArray(payload)) return payload
  return []
}

// ── Detail JSON dump — the org shapes are intentionally opaque JSON, so a
// key/value table renders whatever monomind actually returned instead of
// guessing field names and silently dropping ones we didn't anticipate.
// (KVBlock itself now lives in ./KVBlock.jsx — see that file for why.) ──

function Card({ children }) {
  return (
    <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', padding: '10px 14px' }}>
      {children}
    </div>
  )
}

export default function OrgsPanel({ embedded = false, isOpen = true, onClose, pageActive = true }) {
  // Effective openness: an embedded panel is open when its isOpen prop says
  // so; a page-mode panel (rendered by the Orgs page) stays mounted under
  // App.jsx's keep-alive navigation, so for it "open" additionally requires
  // the Orgs page to be the active tab — otherwise its SSE stream would
  // keep running (and the event tail growing) forever after the user
  // navigated away (FV4-10).
  const effectiveOpen = embedded ? isOpen : pageActive
  const [orgs, setOrgs] = useState([])
  const [loadingOrgs, setLoadingOrgs] = useState(true)
  const [selected, setSelected] = useState(null) // org name
  const [tab, setTab] = useState('overview')
  const [data, setData] = useState({}) // { [tab]: payload }
  const [tabLoading, setTabLoading] = useState(false)
  const [events, setEvents] = useState([])
  const [actionBusy, setActionBusy] = useState(null) // id of in-flight approve/deny/answer
  const [designerFullscreen, setDesignerFullscreen] = useState(false) // Design tab: collapse the org rail + tab strip for canvas room
  const [creatingOrg, setCreatingOrg] = useState(false)
  const [newOrgName, setNewOrgName] = useState('')
  const [newOrgGoal, setNewOrgGoal] = useState('')
  const [createBusy, setCreateBusy] = useState(false)
  const [createError, setCreateError] = useState('')
  const [notInitialized, setNotInitialized] = useState(false)
  const [runningOrgs, setRunningOrgs] = useState(() => new Set())
  const [runPromptOpen, setRunPromptOpen] = useState(false)
  const [runTaskText, setRunTaskText] = useState('')

  const loadOrgs = useCallback(async (silent = false) => {
    if (!silent) setLoadingOrgs(true)
    try {
      // Checked first: ListOrgDesigns silently succeeds with an empty list
      // on a never-initialized profile (EnsureLayout creates an empty
      // .monomind/ on every org call), so it can't distinguish "no orgs
      // yet" from "monomind was never set up here" on its own.
      const initialized = await api.isMonomindInitialized()
      setNotInitialized(!initialized)
      if (!initialized) { setOrgs([]); return }
      // listOrgDesigns reads config files directly (wails-app/app_orgs_design.go)
      // and so includes every org, including ones designed but never run —
      // listOrgs (below) proxies `monoagentcli org list`'s RUNTIME view,
      // which only knows about orgs that have actually been started at
      // least once. The rail needs the former or a freshly-designed org is
      // invisible until its first run.
      const res = await api.listOrgDesigns()
      setOrgs(itemsOf(res))
    } finally {
      if (!silent) setLoadingOrgs(false)
    }
  }, [])

  // First activation loads with the spinner; every later re-activation
  // (coming back to the Orgs page / reopening an embedded panel) silently
  // refreshes the org list so it doesn't drift while hidden.
  const firstLoadRef = useRef(true)
  useEffect(() => {
    if (!effectiveOpen) return
    loadOrgs(firstLoadRef.current)
    firstLoadRef.current = false
  }, [effectiveOpen, loadOrgs])

  // ── Tab data loading ──
  const loadTab = useCallback(async (orgName, tabId) => {
    if (!orgName) return
    // The Design tab owns its own load/save lifecycle (OrgDesigner.jsx
    // calls api.getOrgDesign itself, live-patched via onOrgDesignUpdated) —
    // it doesn't use this generic JSON-dump-per-tab path at all.
    if (tabId === 'design') return
    setTabLoading(true)
    try {
      let payload = null
      switch (tabId) {
        case 'overview':  payload = await api.getOrgStatus(orgName); break
        case 'questions': payload = await api.getOrgQuestions(orgName); break
        case 'gates':     payload = await api.getOrgGates(orgName); break
        case 'logs':      payload = await api.getOrgLogs(orgName); break
        case 'costs':     payload = await api.getOrgCosts(orgName); break
        case 'flow':      payload = await api.getOrgFlow(orgName); break
        case 'decisions': payload = await api.getOrgDecisions(orgName); break
        default: break
      }
      setData(prev => ({ ...prev, [tabId]: payload }))
    } finally {
      setTabLoading(false)
    }
  }, [])

  useEffect(() => {
    if (selected) loadTab(selected, tab)
  }, [selected, tab, loadTab])

  // ── Live event tail ──
  useEffect(() => {
    // Embedded panels render null when closed, but hooks still run — guard the
    // SSE stream + listeners on effectiveOpen so a closed panel (or a page-mode
    // panel whose page isn't active) stops streaming.
    if (!selected || !effectiveOpen) return
    setEvents([])
    api.streamOrgEvents(selected)
    const offEvent = onOrgEvent((payload) => {
      if (payload?.orgName !== selected) return
      setEvents(prev => {
        const next = [...prev, payload.event]
        return next.length > 300 ? next.slice(-300) : next
      })
    })
    const offClosed = onOrgEventsClosed(() => {})
    return () => {
      offEvent()
      offClosed()
      api.stopOrgEvents(selected)
    }
  }, [selected, effectiveOpen])

  // ── Run status tracking — independent of `selected` so the rail's
  // running-state indicator (if ever added) and this panel's Run button
  // both stay correct even if the user switches orgs mid-run. ──
  useEffect(() => {
    return onOrgRunStatus((payload) => {
      if (!payload?.orgName) return
      setRunningOrgs(prev => {
        const next = new Set(prev)
        if (payload.status === 'running') next.add(payload.orgName)
        else next.delete(payload.orgName)
        return next
      })
      if (payload.status === 'stopped') notify('org run', `${payload.orgName} finished`)
      else if (payload.status === 'error') notify('org run', `${payload.orgName} exited with an error`)
    })
  }, [])

  const handleRunOrg = useCallback(async () => {
    if (!selected) return
    const task = runTaskText.trim()
    setRunPromptOpen(false)
    setRunTaskText('')
    setRunningOrgs(prev => new Set(prev).add(selected)) // optimistic — org:runStatus confirms shortly after
    const res = await api.runOrg(selected, task)
    if (res?.error) {
      notify('org run', res.error)
      setRunningOrgs(prev => {
        const next = new Set(prev)
        next.delete(selected)
        return next
      })
    }
  }, [selected, runTaskText])

  const selectOrg = useCallback((name, { tab: tabOverride } = {}) => {
    setSelected(name)
    setTab(tabOverride || 'overview')
    setData({})
    setRunPromptOpen(false)
    setRunTaskText('')
  }, [])

  // New orgs created externally (e.g. by the chat, which shells out to a
  // separate monomind subprocess) never trigger this component's own state
  // updates — they only show up via the filesystem-watcher-driven
  // org:designUpdated event. Auto-select any org this component doesn't
  // already know about into the Design tab, so its role cards animate in
  // live as the chat creates them (see OrgDesigner.jsx's applyLivePatch).
  // Scoped to genuinely-new orgs only — an edit to an org already in the
  // rail shouldn't yank the user's view away from what they're doing.
  const orgsRef = useRef(orgs)
  useEffect(() => { orgsRef.current = orgs }, [orgs])
  useEffect(() => {
    const off = onOrgDesignUpdated((payload) => {
      if (!payload?.orgName) return
      if (orgsRef.current.some(o => o.name === payload.orgName)) return
      loadOrgs(true).then(() => selectOrg(payload.orgName, { tab: 'design' }))
    })
    return off
  }, [loadOrgs, selectOrg])

  const refreshTab = () => loadTab(selected, tab)

  // Refresh button: reload the org rail AND, if an org/tab is currently
  // open, re-fetch that tab's data too — "refresh" should mean the whole
  // visible page, not just the rail.
  const [refreshingAll, setRefreshingAll] = useState(false)
  const refreshAll = async () => {
    setRefreshingAll(true)
    try {
      await loadOrgs()
      if (selected) await loadTab(selected, tab)
    } finally {
      setRefreshingAll(false)
    }
  }

  const handleCreateOrg = async () => {
    const name = newOrgName.trim()
    if (!name) { setCreateError('name required'); return }
    setCreateBusy(true)
    setCreateError('')
    try {
      const res = await api.createOrgDesign({ name, goal: newOrgGoal.trim() })
      if (!res || res.error) {
        setCreateError(res?.error || 'failed to create org')
        return
      }
      setCreatingOrg(false)
      setNewOrgName('')
      setNewOrgGoal('')
      await loadOrgs(true)
      selectOrg(name, { tab: 'design' })
    } finally {
      setCreateBusy(false)
    }
  }

  // ── Actions ──
  const doAction = async (id, fn, successMsg) => {
    setActionBusy(id)
    try {
      const res = await fn()
      if (res?.error) notify('org action', res.error)
      else if (successMsg) notify('org action', successMsg)
      await refreshTab()
    } catch (e) {
      notify('org action', e.message || String(e))
    } finally {
      setActionBusy(null)
    }
  }

  const containerStyle = embedded
    ? { width: 420, flexShrink: 0, background: '#060b13', borderLeft: '1px solid rgba(0,180,216,0.1)', display: 'flex', flexDirection: 'column', overflow: 'hidden' }
    : { flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }

  if (embedded && !isOpen) return null

  return (
    <div style={containerStyle}>
      {embedded && (
        <div style={{ padding: '10px 12px', borderBottom: '1px solid rgba(0,180,216,0.1)', display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
          <Building2 size={13} style={{ color: 'var(--text-muted)' }} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 700, color: '#e2e8f0', flex: 1, letterSpacing: 1 }}>ORGS</span>
          <button onClick={refreshAll} disabled={refreshingAll} title="Refresh" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-muted)', padding: 2, display: 'flex' }}>
            <RefreshCw size={12} style={{ animation: refreshingAll ? 'spin 0.7s linear infinite' : 'none' }} />
          </button>
          {onClose && (
            <button onClick={onClose} title="Close panel" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-muted)', padding: 2, display: 'flex' }}>
              <X size={14} />
            </button>
          )}
        </div>
      )}

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Org list — collapsed while the Design canvas is in fullscreen, or
            while the profile isn't set up (the CTA below is the only thing
            shown then — one prompt, not a rail full of "no orgs" plus a
            second one in the detail pane). */}
        {!notInitialized && !(designerFullscreen && tab === 'design') && (
        <div style={{ width: embedded ? 130 : 220, flexShrink: 0, borderRight: '1px solid var(--border)', overflowY: 'auto', padding: 8, display: 'flex', flexDirection: 'column', gap: 8 }}>
          {!creatingOrg ? (
            <button
              onClick={() => setCreatingOrg(true)}
              style={{
                display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4,
                fontFamily: 'var(--font-mono)', fontSize: 10.5, padding: '6px 8px', borderRadius: 'var(--radius)',
                background: 'var(--elevated)', border: '1px solid var(--border-active)', color: 'var(--text)', cursor: 'pointer',
              }}
            >
              <Plus size={12} /> New org
            </button>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6, padding: 8, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)' }}>
              <input
                autoFocus
                placeholder="org-name"
                value={newOrgName}
                onChange={e => setNewOrgName(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') handleCreateOrg(); if (e.key === 'Escape') setCreatingOrg(false) }}
                style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, padding: '5px 6px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 4, color: 'var(--text)' }}
              />
              <input
                placeholder="goal (optional)"
                value={newOrgGoal}
                onChange={e => setNewOrgGoal(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') handleCreateOrg(); if (e.key === 'Escape') setCreatingOrg(false) }}
                style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, padding: '5px 6px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 4, color: 'var(--text)' }}
              />
              {createError && <div style={{ fontFamily: 'var(--font-mono)', fontSize: 9.5, color: '#f87171' }}>{createError}</div>}
              <div style={{ display: 'flex', gap: 6 }}>
                <button onClick={handleCreateOrg} disabled={createBusy} style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 6px', borderRadius: 4, background: 'var(--elevated)', border: '1px solid var(--border-active)', color: 'var(--text)', cursor: createBusy ? 'default' : 'pointer' }}>
                  {createBusy ? 'Creating…' : 'Create'}
                </button>
                <button onClick={() => { setCreatingOrg(false); setCreateError('') }} style={{ fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 4, background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-muted)', cursor: 'pointer' }}>
                  Cancel
                </button>
              </div>
            </div>
          )}
          {loadingOrgs ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 16 }}><div className="spinner" /></div>
          ) : orgs.length === 0 ? (
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', padding: 8, textAlign: 'center' }}>No orgs found.</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {orgs.map(o => (
                <button
                  key={o.name}
                  onClick={() => selectOrg(o.name)}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 6, textAlign: 'left',
                    background: selected === o.name ? 'var(--elevated)' : 'transparent',
                    border: selected === o.name ? '1px solid var(--border-active)' : '1px solid transparent',
                    borderRadius: 'var(--radius)', padding: '6px 8px', cursor: 'pointer',
                  }}
                >
                  <StatusDot status={o.status} />
                  <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.name}</span>
                </button>
              ))}
            </div>
          )}
        </div>
        )}

        {/* Detail */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          {notInitialized ? (
            <MonomindInitPrompt onInitialized={() => loadOrgs()} />
          ) : !selected ? (
            <div className="empty-state" style={{ flex: 1 }}>
              <div className="empty-state-icon"><Building2 size={30} /></div>
              <div className="empty-state-title">Select an org</div>
              <div className="empty-state-desc">Pick an org from the list to see its status, questions, and gates.</div>
            </div>
          ) : (
            <>
              {!(designerFullscreen && tab === 'design') && (
              <div style={{ display: 'flex', gap: 4, padding: '8px', borderBottom: '1px solid var(--border)', overflowX: 'auto', flexShrink: 0 }}>
                {TABS.map(t => {
                  const Icon = t.icon
                  const active = tab === t.id
                  return (
                    <button
                      key={t.id}
                      onClick={() => setTab(t.id)}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap',
                        fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                        background: active ? 'var(--elevated)' : 'transparent',
                        border: active ? '1px solid var(--border-active)' : '1px solid transparent',
                        color: active ? 'var(--text)' : 'var(--text-muted)', cursor: 'pointer',
                      }}
                    >
                      <Icon size={11} /> {t.label}
                    </button>
                  )
                })}
                {runningOrgs.has(selected) ? (
                  <button
                    disabled
                    style={{
                      marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 4,
                      fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                      background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-muted)', cursor: 'default',
                    }}
                  >
                    <Loader2 size={11} style={{ animation: 'spin 0.9s linear infinite' }} /> Running…
                  </button>
                ) : (
                  <button
                    onClick={() => setRunPromptOpen(v => !v)}
                    title="Run this org"
                    style={{
                      marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 4,
                      fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                      background: runPromptOpen ? 'var(--elevated)' : 'transparent',
                      border: runPromptOpen ? '1px solid var(--border-active)' : '1px solid var(--border)',
                      color: 'var(--text)', cursor: 'pointer',
                    }}
                  >
                    <Play size={11} /> Run
                  </button>
                )}
                {tab === 'design' && (
                  <button
                    onClick={() => setDesignerFullscreen(v => !v)}
                    title={designerFullscreen ? 'Exit fullscreen' : 'Fullscreen canvas'}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 4,
                      fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                      background: 'transparent', border: '1px solid transparent', color: 'var(--text-muted)', cursor: 'pointer',
                    }}
                  >
                    <Maximize2 size={11} />
                  </button>
                )}
              </div>
              )}

              {runPromptOpen && (
                <div style={{ display: 'flex', gap: 6, padding: '6px 8px', borderBottom: '1px solid var(--border)', flexShrink: 0 }}>
                  <input
                    autoFocus
                    type="text"
                    value={runTaskText}
                    onChange={e => setRunTaskText(e.target.value)}
                    onKeyDown={e => { if (e.key === 'Enter') handleRunOrg(); if (e.key === 'Escape') { setRunPromptOpen(false); setRunTaskText('') } }}
                    placeholder="First message for the org (optional) — Enter to run"
                    style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: 11, background: 'var(--elevated)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', color: 'var(--text)', padding: '6px 8px', outline: 'none' }}
                  />
                  <button className="btn btn-primary btn-sm" onClick={handleRunOrg}>
                    <Play size={11} /> Run
                  </button>
                </div>
              )}

              {tab === 'design' ? (
                // Full-bleed canvas — owns its own scrolling/panning, so it
                // deliberately sits outside the generic padded/overflow-auto
                // container every other tab uses.
                <div style={{ flex: 1, minHeight: 0, display: 'flex', position: 'relative' }}>
                  <OrgDesigner
                    orgName={selected}
                    fullscreen={designerFullscreen}
                    onToggleFullscreen={() => setDesignerFullscreen(v => !v)}
                  />
                </div>
              ) : (
              <div style={{ flex: 1, overflowY: 'auto', padding: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
                {tabLoading && <div style={{ display: 'flex', justifyContent: 'center', padding: 8 }}><div className="spinner" /></div>}

                {!tabLoading && tab === 'overview' && (
                  <>
                    <Card><KVBlock obj={data.overview} /></Card>
                    <div>
                      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 6 }}>Live events</div>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 260, overflowY: 'auto' }}>
                        {events.length === 0 && <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>Waiting for events…</div>}
                        {events.map((e, i) => (
                          <div key={i} style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '4px 8px', overflowWrap: 'anywhere' }}>
                            <span style={{ color: 'var(--cyan)' }}>{e.type}</span>{e.from ? ` ${e.from}→${e.to || '?'}` : ''}{e.subject ? `: ${e.subject}` : ''}{e.msg ? ` ${e.msg}` : ''}
                          </div>
                        ))}
                      </div>
                    </div>
                  </>
                )}

                {!tabLoading && tab === 'questions' && (
                  itemsOf(data.questions).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No pending questions.</div>
                    : itemsOf(data.questions).map((q, i) => {
                      const id = q.id || q.question_id || String(i)
                      return (
                        <Card key={id}>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            <KVBlock obj={q} />
                            <QuestionAnswer
                              busy={actionBusy === id}
                              onSubmit={(answer) => doAction(id, () => api.answerOrgQuestion(selected, id, answer), 'Answered')}
                            />
                          </div>
                        </Card>
                      )
                    })
                )}

                {!tabLoading && tab === 'gates' && (
                  itemsOf(data.gates).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No pending gates.</div>
                    : itemsOf(data.gates).map((g, i) => {
                      const id = g.id || g.gate_id || String(i)
                      return (
                        <Card key={id}>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            <KVBlock obj={g} />
                            <div style={{ display: 'flex', gap: 6 }}>
                              <button className="btn btn-primary btn-sm" disabled={actionBusy === id}
                                onClick={() => doAction(id, () => api.gateApproveOrgAction(selected, id, ''), 'Gate approved')}>
                                <CheckCircle2 size={11} /> Approve
                              </button>
                              <button className="btn btn-danger btn-sm" disabled={actionBusy === id}
                                onClick={() => doAction(id, () => api.gateRejectOrgAction(selected, id, ''), 'Gate rejected')}>
                                <XCircle size={11} /> Reject
                              </button>
                            </div>
                          </div>
                        </Card>
                      )
                    })
                )}

                {!tabLoading && tab === 'logs' && (
                  itemsOf(data.logs).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No log entries.</div>
                    : itemsOf(data.logs).map((e, i) => (
                      <div key={i} style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '4px 8px' }}>
                        <span style={{ color: 'var(--cyan)' }}>{e.type}</span>{e.from ? ` ${e.from}→${e.to || '?'}` : ''}{e.subject ? `: ${e.subject}` : ''}
                      </div>
                    ))
                )}

                {!tabLoading && tab === 'costs' && (
                  <>
                    {itemsOf(data.costs).length === 0
                      ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No cost data.</div>
                      : itemsOf(data.costs).map((c, i) => (
                        <Card key={i}><KVBlock obj={c} /></Card>
                      ))}
                    {data.costs?.totals && <Card><KVBlock obj={data.costs.totals} /></Card>}
                  </>
                )}

                {!tabLoading && tab === 'flow' && (
                  itemsOf(data.flow?.edges).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No flow data.</div>
                    : itemsOf(data.flow?.edges).map((e, i) => (
                      <div key={i} style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '6px 10px' }}>
                        {e.from} → {e.to}{e.subject ? `: ${e.subject}` : ''}
                      </div>
                    ))
                )}

                {!tabLoading && tab === 'decisions' && (
                  itemsOf(data.decisions).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No decisions recorded.</div>
                    : itemsOf(data.decisions).map((d, i) => (
                      <Card key={i}><KVBlock obj={d} /></Card>
                    ))
                )}
              </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function QuestionAnswer({ onSubmit, busy }) {
  const [text, setText] = useState('')
  return (
    <div style={{ display: 'flex', gap: 6 }}>
      <input
        type="text"
        value={text}
        onChange={e => setText(e.target.value)}
        placeholder="Type an answer…"
        style={{ flex: 1, fontFamily: 'var(--font-mono)', fontSize: 11, background: 'var(--elevated)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', color: 'var(--text)', padding: '6px 8px', outline: 'none' }}
      />
      <button
        className="btn btn-primary btn-sm"
        disabled={busy || !text.trim()}
        onClick={() => { onSubmit(text.trim()); setText('') }}
      >
        Answer
      </button>
    </div>
  )
}
