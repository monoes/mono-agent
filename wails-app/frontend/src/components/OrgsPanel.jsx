import { useState, useEffect, useCallback, useRef } from 'react'
import {
  X, RefreshCw, Building2, CheckCircle2, XCircle, Circle, Network, Maximize2, Minimize2, Plus,
  MessageCircleQuestion, ShieldAlert, Coins, GitBranch, ScrollText, ListTree, Play, Loader2, KeyRound,
} from 'lucide-react'
import { api, onOrgEvent, onOrgEventsClosed, onOrgDesignUpdated, onOrgRunStatus, notify } from '../services/api.js'
import OrgDesigner from './orgdesigner/OrgDesigner.jsx'
import { KVBlock } from './KVBlock.jsx'
import MonomindInitPrompt from './MonomindInitPrompt.jsx'

const TABS = [
  { id: 'design',     label: 'Design',     icon: Network },
  { id: 'overview',   label: 'Overview',   icon: Circle },
  { id: 'approvals',  label: 'Approvals',  icon: ShieldAlert },
  { id: 'logs',       label: 'Logs',       icon: ScrollText },
  { id: 'costs',      label: 'Costs',      icon: Coins },
  { id: 'flow',       label: 'Flow',       icon: GitBranch },
  { id: 'decisions',  label: 'Decisions',  icon: ListTree },
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

// The merged Approvals tab's three source shapes each identify an item
// differently — questions/gates have a real id field, but approvals.json
// entries don't (they're keyed by role+action instead).
function approvalItemId(item, index) {
  if (item.kind === 'question') return item.questionId || String(index)
  if (item.kind === 'gate') return item.id || String(index)
  if (item.kind === 'approval') return `${item.roleId}:${item.action}`
  return String(index)
}

// The org's actual deliverable (the post/document/answer it produced) isn't
// a distinct field anywhere — org_complete's own `summary` is often just a
// vague meta-note ("post was created and approved") rather than the verbatim
// result, and orgs rarely emit `asset` events. What IS reliable: the
// coordinator (report.outcome.by) almost always narrates the finished result
// in its own words as its last chat message right after calling
// org_complete. Pull that out so Overview can show it prominently instead of
// making people dig through the raw event log for it.
function extractFinalMessage(events, outcome) {
  if (!outcome?.by) return null
  const list = itemsOf(events)
  const chats = list.filter(e => e.type === 'chat' && e.from === outcome.by)
  if (!chats.length) return null
  return chats[chats.length - 1].msg || null
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

// Overview's promoted "what actually happened" summary — a colored outcome
// banner plus, when we could find one, the org's own words describing its
// result (see extractFinalMessage above). Renders nothing if there's no
// outcome yet (run still going, or never called org_complete).
function OutcomeBanner({ outcome, finalMessage }) {
  if (!outcome) return null
  const color = outcome.status === 'achieved' ? 'var(--green-neon)'
    : outcome.status === 'partial' ? '#eab308'
    : outcome.status === 'failed' ? 'var(--red)'
    : 'var(--text-muted)'
  const label = outcome.status === 'achieved' ? 'Achieved'
    : outcome.status === 'partial' ? 'Partial'
    : outcome.status === 'failed' ? 'Failed'
    : outcome.status || 'Unknown'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 6, padding: '6px 10px',
        background: 'var(--surface)', border: `1px solid ${color}`, borderRadius: 'var(--radius)',
      }}>
        <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, boxShadow: `0 0 5px ${color}`, flexShrink: 0 }} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, color, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase' }}>{label}</span>
        {outcome.by && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>by {outcome.by}</span>}
      </div>
      {finalMessage && (
        <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', padding: '12px 14px', fontSize: 12.5, color: 'var(--text)', lineHeight: 1.5, whiteSpace: 'pre-wrap' }}>
          {finalMessage}
        </div>
      )}
    </div>
  )
}

export default function OrgsPanel({ embedded = false, isOpen = true, onClose, pageActive = true, onNavigate, pendingSelectOrgName, onConsumePendingSelect }) {
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
  // Org names with the Approvals tab's auto-approve toggle on — in-memory
  // only (mirrors runningOrgs), naturally persists across tab switches
  // within the session since OrgsPanel stays mounted.
  const [autoApproveOrgs, setAutoApproveOrgs] = useState(() => new Set())
  // Pending count for the selected org's Approvals tab badge — tracked
  // independently of which tab is active (the whole point of a badge is
  // to notice something without having to click into the tab first).
  const [pendingApprovalCount, setPendingApprovalCount] = useState(0)
  const [runPromptOpen, setRunPromptOpen] = useState(false)
  const [runTaskText, setRunTaskText] = useState('')
  // Overview's run picker: runsList is every run (current + historical).
  // selectedRun is null (no explicit choice yet — defaults to the most
  // recent PAST run once loaded), the literal string 'live' (only ever set
  // by pressing Play, or by the user manually picking it from the
  // dropdown — opening/switching to an org must never auto-select it just
  // because the org happens to still be running), or a specific run id.
  const [runsList, setRunsList] = useState([])
  const [selectedRun, setSelectedRun] = useState(null)
  const [streamGeneration, setStreamGeneration] = useState(0) // bumped to force the live event tail to re-subscribe against the current run
  const [runDetail, setRunDetail] = useState(null) // { run, loading, report, logs } for a selected historical run

  const loadOrgs = useCallback(async (silent = false) => {
    if (!silent) setLoadingOrgs(true)
    try {
      // Checked first: ListOrgDesigns silently succeeds with an empty list
      // on a never-initialized profile (EnsureLayout creates an empty
      // .monomind/ on every org call), so it can't distinguish "no orgs
      // yet" from "monomind was never set up here" on its own.
      const initialized = await api.isMonomindInitialized()
      setNotInitialized(!initialized)
      if (!initialized) { setOrgs([]); return 0 }
      // listOrgDesigns reads config files directly (wails-app/app_orgs_design.go)
      // and so includes every org, including ones designed but never run —
      // listOrgs (below) proxies `monoagentcli org list`'s RUNTIME view,
      // which only knows about orgs that have actually been started at
      // least once. The rail needs the former or a freshly-designed org is
      // invisible until its first run.
      const res = await api.listOrgDesigns()
      const items = itemsOf(res)
      setOrgs(items)
      return items.length
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
    const isFirst = firstLoadRef.current
    firstLoadRef.current = false
    loadOrgs(isFirst).then(count => {
      // The very first load of the app's session can race Go's own
      // startup (getActiveProfileID() defaults to "default" until
      // App.startup() finishes resolving the real active profile — Wails
      // doesn't guarantee the frontend waits for that) — landing on Orgs
      // before it resolves queries the wrong, empty profile. One silent
      // retry shortly after almost always lands after startup finishes,
      // instead of requiring the user to navigate away and back.
      if (isFirst && count === 0) setTimeout(() => loadOrgs(true), 700)
    })
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
        case 'overview': {
          const [status, history, current] = await Promise.all([
            api.getOrgStatus(orgName),
            api.getOrgReport(orgName, true),
            api.getOrgReport(orgName, false),
          ])
          payload = status
          // "Live" must be gated on the org's ACTUAL status (`running`),
          // not merely on "this run is absent from history.jsonl" — a run
          // can be missing from history for reasons other than currently
          // running (e.g. it crashed instead of stopping cleanly), and
          // treating that as "live" mislabeled a dead org as working.
          const isRunning = status?.status === 'running'
          const liveRun = isRunning && current?.run && !current?.error ? current : null
          const historyItemsAll = itemsOf(history)
          const historyItems = liveRun ? historyItemsAll.filter(h => h.run !== liveRun.run) : historyItemsAll
          setRunsList([
            ...(liveRun ? [{ run: liveRun.run, live: true, summary: liveRun }] : []),
            ...historyItems.map(h => ({ run: h.run, live: false, summary: h })),
          ])
          // Default to the most recent PAST run, never to Live — opening
          // or switching to an org should show what it did, not silently
          // jump to "live" just because it happens to still be running.
          // "Live" is only ever entered by pressing Play (handleRunOrg
          // sets 'live' directly) or picking it from the dropdown by hand
          // — both of which leave selectedRun non-null, so this default
          // computation is skipped and the explicit choice is kept.
          setSelectedRun(prev => (prev === null && historyItems.length > 0) ? historyItems[0].run : prev)
          break
        }
        case 'approvals': {
          // Three separate human-in-loop channels, merged into one list —
          // see doAction/render below for why each kind needs its own
          // resolve action (answering a question or approving a gate does
          // NOT touch approvals.json, and vice versa).
          const [questions, gates, approvals] = await Promise.all([
            api.getOrgQuestions(orgName),
            api.getOrgGates(orgName),
            api.getOrgApprovals(orgName),
          ])
          payload = [
            ...itemsOf(questions).map(q => ({ kind: 'question', ...q })),
            ...itemsOf(gates).map(g => ({ kind: 'gate', ...g })),
            ...itemsOf(approvals).map(a => ({ kind: 'approval', ...a })),
          ]
          break
        }
        // Scoped to whichever run is selected in the (shared, tab-strip-level)
        // run picker — 'live' means "let the CLI default to the newest run"
        // (matches what Live is already tailing), same as Overview's own
        // runDetail effect does for report/logs. No run picked yet (org has
        // no runs) → skip the fetch entirely; itemsOf(null) already renders
        // each tab's existing empty state.
        case 'logs': {
          const run = selectedRun === 'live' ? '' : selectedRun
          payload = run || selectedRun === 'live' ? await api.getOrgLogs(orgName, run) : null
          break
        }
        case 'costs': {
          const run = selectedRun === 'live' ? '' : selectedRun
          payload = run || selectedRun === 'live' ? await api.getOrgCosts(orgName, run) : null
          break
        }
        case 'flow': {
          const run = selectedRun === 'live' ? '' : selectedRun
          payload = run || selectedRun === 'live' ? await api.getOrgFlow(orgName, run) : null
          break
        }
        case 'decisions': {
          const run = selectedRun === 'live' ? '' : selectedRun
          payload = run || selectedRun === 'live' ? await api.getOrgDecisions(orgName, run) : null
          break
        }
        default: break
      }
      setData(prev => ({ ...prev, [tabId]: payload }))
    } finally {
      setTabLoading(false)
    }
  }, [selectedRun])

  useEffect(() => {
    if (selected) loadTab(selected, tab)
  }, [selected, tab, loadTab])

  // ── Overview's run picker: fetch a specific historical run's own report
  // + logs when picked. Live (selectedRun === 'live') needs nothing extra
  // — it's covered by the org status card + live event tail below. ──
  useEffect(() => {
    if (tab !== 'overview' || !selected || !selectedRun || selectedRun === 'live') { setRunDetail(null); return }
    let cancelled = false
    setRunDetail({ run: selectedRun, loading: true })
    Promise.all([
      api.getOrgReport(selected, false, selectedRun),
      api.getOrgLogs(selected, selectedRun),
    ]).then(([report, logs]) => {
      if (!cancelled) setRunDetail({ run: selectedRun, loading: false, report, logs })
    })
    return () => { cancelled = true }
  }, [tab, selected, selectedRun])

  // ── Live event tail ──
  // `streamGeneration` exists purely to force a restart: the underlying CLI
  // subscription (`org events --follow`, no explicit --run) resolves "which
  // run to tail" once, at subprocess start, and never re-resolves after —
  // so a subscription opened while an org was idle (or mid an older run)
  // keeps tailing that same stale/absent run forever, even once a brand new
  // run starts. Bumping this deliberately tears down and reopens the
  // subscription so it re-resolves to whatever run is current now.
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
  }, [selected, effectiveOpen, streamGeneration])

  // ── Approvals tab badge count — polls independently of which tab is
  // active, so the notification shows up without having to click in. ──
  useEffect(() => {
    if (!selected || !effectiveOpen) { setPendingApprovalCount(0); return }
    const org = selected
    let cancelled = false
    const refreshCount = async () => {
      const [questions, gates, approvals] = await Promise.all([
        api.getOrgQuestions(org),
        api.getOrgGates(org),
        api.getOrgApprovals(org),
      ])
      if (cancelled) return
      const count =
        itemsOf(questions).filter(q => q.answer === null).length +
        itemsOf(gates).filter(g => g.status === 'pending').length +
        itemsOf(approvals).filter(a => a.approved === null).length
      setPendingApprovalCount(count)
    }
    refreshCount()
    const iv = setInterval(refreshCount, 6000)
    return () => { cancelled = true; clearInterval(iv) }
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
      // Only a genuine failure is worth an error toast — "stopped" just
      // means the process exited, which includes completely normal runs.
      // notify()/Toasts.jsx render every call as a red "Failed: …" (no
      // success/info variant exists), so routing a clean finish through it
      // misleadingly reads as a crash. The Overview view (Running… badge
      // clearing, run picker updating) already communicates completion.
      if (payload.status === 'error') notify('org run', payload.message || `${payload.orgName} exited with an error`)
      // A run actually starting/stopping is the moment the "Live" view's
      // data (status card, run dropdown) goes stale — refresh it right
      // then, not just on tab/org switch. Scoped to "currently looking at
      // Live for this exact org" so it never disturbs someone deliberately
      // inspecting a historical run or a different org.
      //
      // On "running" specifically: this event fires the instant the OS
      // process starts (RunOrg's cmd.Start() succeeding), but monomind's
      // own daemon takes a beat longer to actually write status:"running"
      // to runtime.json — a single reload right here can (and did) still
      // observe the pre-run state and never pick up the live run. Poll a
      // few times over a couple seconds instead of reloading once, so the
      // dropdown reliably catches the real transition once it lands.
      //
      // The live event tail has the exact same race, but one level worse:
      // it's not just reading stale data, its subscription is pinned to
      // whatever run existed when it was opened (see the effect's own
      // comment) and never re-resolves on its own. Bump streamGeneration
      // on each poll tick too, so the tail's subscription gets torn down
      // and reopened until one of those reopens lands after the new run
      // directory actually exists and picks it up.
      if (payload.orgName === selected && tab === 'overview' && selectedRun === 'live') {
        if (payload.status === 'running') {
          let attempts = 0
          const iv = setInterval(() => {
            attempts++
            loadTab(selected, 'overview')
            setStreamGeneration(g => g + 1)
            if (attempts >= 6) clearInterval(iv)
          }, 600)
        } else {
          loadTab(selected, 'overview')
        }
      }
    })
  }, [selected, tab, selectedRun, loadTab])

  // ── Auto-approve: while ON for the selected org and the Approvals tab is
  // open, periodically re-check for pending items and resolve them
  // immediately — questions get a generic "proceed" reply (they need real
  // text, not a yes/no), gates and tool-approvals resolve as clean yes.
  // No dedup bookkeeping needed: a resolved item stops appearing as
  // pending on the next fetch, so the loop naturally converges to empty. ──
  useEffect(() => {
    if (tab !== 'approvals' || !selected || !autoApproveOrgs.has(selected)) return
    const org = selected
    const resolveAllPending = async () => {
      const [questions, gates, approvals] = await Promise.all([
        api.getOrgQuestions(org),
        api.getOrgGates(org),
        api.getOrgApprovals(org),
      ])
      const pendingQuestions = itemsOf(questions).filter(q => q.answer === null)
      const pendingGates = itemsOf(gates).filter(g => g.status === 'pending')
      const pendingApprovals = itemsOf(approvals).filter(a => a.approved === null)
      if (!pendingQuestions.length && !pendingGates.length && !pendingApprovals.length) return
      await Promise.all([
        ...pendingQuestions.map(q => api.answerOrgQuestion(org, q.questionId, 'Approved — proceed autonomously.')),
        ...pendingGates.map(g => api.gateApproveOrgAction(org, g.id, '')),
        ...pendingApprovals.map(a => api.approveOrgAction(org, a.roleId, a.action)),
      ])
      if (org === selected) loadTab(org, 'approvals')
    }
    resolveAllPending()
    const iv = setInterval(resolveAllPending, 4000)
    return () => clearInterval(iv)
  }, [tab, selected, autoApproveOrgs, loadTab])

  const handleRunOrg = useCallback(async () => {
    if (!selected) return
    const task = runTaskText.trim()
    setRunPromptOpen(false)
    setRunTaskText('')
    setRunningOrgs(prev => new Set(prev).add(selected)) // optimistic — org:runStatus confirms shortly after
    // Land on Overview watching the just-started run live, per the ask —
    // scoped to this button press only, so a run starting elsewhere never
    // yanks the view out from under someone inspecting a different run/tab.
    // The actual data refresh happens off the org:runStatus "running" event
    // below (not here) — the process hasn't started yet at this point, so
    // reloading now would just re-show the previous/finished run's data.
    setTab('overview')
    setSelectedRun('live')
    // The "Live event tail" effect only clears `events` on org-switch or
    // panel open/close — pressing Run again on the org you're ALREADY
    // looking at never triggers that, so the box stayed full of whatever
    // the previous run left behind. Clear it here, with an immediate
    // synthetic entry so there's visible feedback before the org's real
    // events start arriving over the live stream (which itself lags a
    // beat behind the process actually starting, same as the status card).
    setEvents([{ type: 'status', msg: `Starting run for ${selected}…` }])
    const res = await api.runOrg(selected, task)
    if (res?.error) {
      notify('org run', res.error)
      setEvents(prev => [...prev, { type: 'status', msg: `Failed to start: ${res.error}` }])
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
    setRunsList([])
    setSelectedRun(null)
  }, [])

  // New orgs created externally (e.g. by the chat, which shells out to a
  // separate monomind subprocess) never trigger this component's own state
  // updates — they only show up via the filesystem-watcher-driven
  // org:designUpdated event. Auto-select any org this component doesn't
  // already know about into the Design tab, so its role cards animate in
  // live as the chat creates them (see OrgDesigner.jsx's applyLivePatch).
  // Scoped to genuinely-new orgs only — an edit to an org already in the
  // rail shouldn't yank the user's view away from what they're doing.
  //
  // Selecting it internally isn't enough if the user is on a different
  // page (most likely Chat, since that's what's creating it) — they'd
  // never see it happen. onNavigate (App.jsx's page switcher, wired in via
  // pages/Orgs.jsx) brings them here too, same as pressing the Orgs tab
  // themselves. Only wired for the real page (App.jsx passes it); an
  // embedded/modal usage of this panel has no onNavigate and stays put.
  const orgsRef = useRef(orgs)
  useEffect(() => { orgsRef.current = orgs }, [orgs])
  useEffect(() => {
    const off = onOrgDesignUpdated((payload) => {
      if (!payload?.orgName) return
      if (orgsRef.current.some(o => o.name === payload.orgName)) return
      loadOrgs(true).then(() => selectOrg(payload.orgName, { tab: 'design' }))
      onNavigate?.('orgs')
    })
    return off
  }, [loadOrgs, selectOrg, onNavigate])

  // Covers the case the listener above can't: this panel didn't exist yet
  // when the org was created (App.jsx's own top-level listener caught it
  // instead, since pages here only mount after being visited once — see
  // Orgs.jsx). Runs once per pending name, on mount or whenever a new one
  // arrives while already mounted; onConsumePendingSelect clears it so it
  // doesn't re-fire if this component happens to remount later.
  useEffect(() => {
    if (!pendingSelectOrgName) return
    loadOrgs(true).then(() => selectOrg(pendingSelectOrgName, { tab: 'design' }))
    onConsumePendingSelect?.()
  }, [pendingSelectOrgName, loadOrgs, selectOrg, onConsumePendingSelect])

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
  const doAction = async (id, fn) => {
    setActionBusy(id)
    try {
      const res = await fn()
      // Success has no toast — notify() only feeds the app's error-toast
      // bus (Toasts.jsx renders everything it gets as "Failed: ..."), so a
      // real success would render identically to a failure. refreshTab()
      // below already reflects the change (item disappears/updates), which
      // is the same feedback pattern used everywhere else in this file.
      if (res?.error) notify('org action', res.error)
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
                  const badgeCount = t.id === 'approvals' ? pendingApprovalCount : 0
                  return (
                    <button
                      key={t.id}
                      onClick={() => setTab(t.id)}
                      style={{
                        position: 'relative',
                        display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap',
                        fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                        background: active ? 'var(--elevated)' : 'transparent',
                        border: active ? '1px solid var(--border-active)' : '1px solid transparent',
                        color: active ? 'var(--text)' : 'var(--text-muted)', cursor: 'pointer',
                      }}
                    >
                      <Icon size={11} /> {t.label}
                      {badgeCount > 0 && (
                        <span style={{
                          position: 'absolute', top: -5, right: -5,
                          minWidth: 14, height: 14, padding: '0 3px', borderRadius: 7,
                          background: 'var(--red, #ef4444)', color: '#fff',
                          fontFamily: 'var(--font-mono)', fontSize: 9, fontWeight: 700, lineHeight: '14px', textAlign: 'center',
                          boxShadow: '0 0 0 2px var(--surface, #060b13)',
                        }}>
                          {badgeCount > 99 ? '99+' : badgeCount}
                        </span>
                      )}
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
                {tab === 'approvals' && (
                  <button
                    onClick={() => setAutoApproveOrgs(prev => {
                      const next = new Set(prev)
                      next.has(selected) ? next.delete(selected) : next.add(selected)
                      return next
                    })}
                    title={autoApproveOrgs.has(selected) ? 'Stop auto-approving — resolve each item by hand' : 'Auto-approve everything pending, as it arrives'}
                    style={{
                      marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 4,
                      fontFamily: 'var(--font-mono)', fontSize: 10, padding: '5px 8px', borderRadius: 'var(--radius)',
                      background: autoApproveOrgs.has(selected) ? 'rgba(0,245,212,0.1)' : 'transparent',
                      border: autoApproveOrgs.has(selected) ? '1px solid rgba(0,245,212,0.3)' : '1px solid var(--border)',
                      color: autoApproveOrgs.has(selected) ? 'var(--teal, #00f5d4)' : 'var(--text-muted)', cursor: 'pointer',
                    }}
                  >
                    <CheckCircle2 size={11} /> Auto-approve {autoApproveOrgs.has(selected) ? 'ON' : 'OFF'}
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

              {tab !== 'design' && (runningOrgs.has(selected) || runsList.length > 0) && (
                // Shared run context — which run every tab (not just
                // Overview) is scoped to. Logs/Costs/Flow/Decisions all read
                // selectedRun too now, so this needs to be visible no matter
                // which tab is open, not buried inside Overview's own body.
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: '8px 12px', borderBottom: '1px solid var(--border)', flexShrink: 0 }}>
                  {runningOrgs.has(selected) && (
                    <div style={{
                      display: 'flex', alignItems: 'center', gap: 6, padding: '6px 10px',
                      background: 'rgba(0,245,212,0.06)', border: '1px solid rgba(0,245,212,0.2)', borderRadius: 'var(--radius)',
                    }}>
                      <span className="live-dot" />
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, color: 'var(--teal, #00f5d4)', fontWeight: 600, letterSpacing: 0.5 }}>Org is running</span>
                    </div>
                  )}
                  {runsList.length > 0 && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>Run</span>
                      <select
                        value={selectedRun || ''}
                        onChange={e => setSelectedRun(e.target.value || null)}
                        className="filter-select"
                        style={{ flex: 1, maxWidth: 360 }}
                      >
                        {runsList.some(r => r.live) && <option value="live">Live (current run)</option>}
                        {runsList.filter(r => !r.live).map(r => {
                          const outcome = r.summary?.outcome?.status
                          return <option key={r.run} value={r.run}>{r.run}{outcome ? ` — ${outcome}` : ''}</option>
                        })}
                      </select>
                    </div>
                  )}
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
                    {!selectedRun ? (
                      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>No completed runs yet.</div>
                    ) : selectedRun === 'live' ? (
                      <>
                        <OutcomeBanner
                          outcome={runsList.find(r => r.live)?.summary?.outcome}
                          finalMessage={extractFinalMessage(events, runsList.find(r => r.live)?.summary?.outcome)}
                        />
                        <details>
                          <summary style={{ cursor: 'pointer', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>Raw status</summary>
                          <Card><KVBlock obj={data.overview} /></Card>
                        </details>
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
                    ) : (
                      // A specific historical run is selected — its own
                      // static report + logs, not the live tail above.
                      runDetail?.loading || runDetail?.run !== selectedRun ? (
                        <div style={{ display: 'flex', justifyContent: 'center', padding: 16 }}><div className="spinner" /></div>
                      ) : (
                        <>
                          <OutcomeBanner
                            outcome={runDetail.report?.outcome}
                            finalMessage={extractFinalMessage(runDetail.logs, runDetail.report?.outcome)}
                          />
                          <details>
                            <summary style={{ cursor: 'pointer', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>Raw report</summary>
                            <Card><KVBlock obj={runDetail.report} /></Card>
                          </details>
                          <div>
                            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 6 }}>Events</div>
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 260, overflowY: 'auto' }}>
                              {itemsOf(runDetail.logs).length === 0 && <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>No events recorded.</div>}
                              {itemsOf(runDetail.logs).map((e, i) => (
                                <div key={i} style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)', background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '4px 8px', overflowWrap: 'anywhere' }}>
                                  <span style={{ color: 'var(--cyan)' }}>{e.type}</span>{e.from ? ` ${e.from}→${e.to || '?'}` : ''}{e.subject ? `: ${e.subject}` : ''}{e.msg ? ` ${e.msg}` : ''}
                                </div>
                              ))}
                            </div>
                          </div>
                        </>
                      )
                    )}
                  </>
                )}

                {!tabLoading && tab === 'approvals' && (
                  itemsOf(data.approvals).length === 0
                    ? <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>Nothing pending.</div>
                    : itemsOf(data.approvals).map((item, i) => {
                      const id = approvalItemId(item, i)
                      const Icon = item.kind === 'question' ? MessageCircleQuestion : item.kind === 'gate' ? ShieldAlert : KeyRound
                      return (
                        <Card key={id}>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
                              <Icon size={11} style={{ color: 'var(--text-muted)' }} />
                              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }}>{item.kind}</span>
                            </div>
                            <KVBlock obj={item} />
                            {item.kind === 'question' && (
                              <QuestionAnswer
                                busy={actionBusy === id}
                                onSubmit={(answer) => doAction(id, () => api.answerOrgQuestion(selected, item.questionId, answer))}
                              />
                            )}
                            {item.kind === 'gate' && (
                              <div style={{ display: 'flex', gap: 6 }}>
                                <button className="btn btn-primary btn-sm" disabled={actionBusy === id}
                                  onClick={() => doAction(id, () => api.gateApproveOrgAction(selected, item.id, ''))}>
                                  <CheckCircle2 size={11} /> Approve
                                </button>
                                <button className="btn btn-danger btn-sm" disabled={actionBusy === id}
                                  onClick={() => doAction(id, () => api.gateRejectOrgAction(selected, item.id, ''))}>
                                  <XCircle size={11} /> Reject
                                </button>
                              </div>
                            )}
                            {item.kind === 'approval' && (
                              <div style={{ display: 'flex', gap: 6 }}>
                                <button className="btn btn-primary btn-sm" disabled={actionBusy === id}
                                  onClick={() => doAction(id, () => api.approveOrgAction(selected, item.roleId, item.action))}>
                                  <CheckCircle2 size={11} /> Approve
                                </button>
                                <button className="btn btn-danger btn-sm" disabled={actionBusy === id}
                                  onClick={() => doAction(id, () => api.denyOrgAction(selected, item.roleId, item.action))}>
                                  <XCircle size={11} /> Deny
                                </button>
                              </div>
                            )}
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
