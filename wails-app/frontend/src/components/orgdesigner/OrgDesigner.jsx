// OrgDesigner — the container tying the Org Designer canvas together.
// Owns the authoritative canvas node state, all persistence calls, and the
// live-update subscription. Every child component here is a "leaf" (canvas,
// palette, inspector, icon picker, delete modal) that only reports intent
// upward via props — this file is the only place that calls the Go-bound
// api.* org-design methods or mutates role state.
//
// Backend contract (wails-app/app_orgs_design.go via src/services/api.js):
//   api.getOrgDesign(name)              -> {v, org, valid, errors}
//   api.addOrgRole(name, role)          -> {ok, rev, org} | {error}
//   api.updateOrgRole(name, id, patch)  -> {ok, rev, org} | {error}
//   api.setOrgRoleReportsTo(name,id,pid)-> {ok, rev, org} | {error}
//   api.removeOrgRole(name,id,strategy) -> {ok, rev, deletedIds} | {error}
//   api.saveOrgLayout(name, layoutMap)  -> {ok, rev}
//   onOrgDesignUpdated(cb) -> unsubscribe; payload {orgName, origin, deleted, valid, errors, org}
//
// Two backend/frontend contract gaps, bridged here rather than in the leaf
// components (documented at their call sites below):
//   1. RoleInspector's `_renameId` patch key has no backend support (no
//      role-id-rename mutator exists) — surfaced as a "not yet supported"
//      notify() rather than silently doing nothing.
//   2. DeleteRoleModal's three strategies (promote/reassign/cascade) map to
//      the backend's two (reparent/cascade) — 'reassign' is composed from
//      setOrgRoleReportsTo calls for each direct child, then a 'reparent'
//      removal (which is then a no-op reparent since no children remain).

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Maximize2, Minimize2, GitBranch, Milestone, RefreshCw, Search, SlidersHorizontal, ChevronLeft, ChevronRight } from 'lucide-react'
import { api, onOrgDesignUpdated, notify } from '../../services/api.js'
import { hydrate, tidyTree, validateStructure, buildTree, wouldCycle } from './orgGraph.js'
import { suggestIcon, loadIconManifest, CATEGORY_TYPE, iconUrl } from './roleIcons.js'
import OrgCanvas from './OrgCanvas.jsx'
import RolePalette from './RolePalette.jsx'
import RoleInspector from './RoleInspector.jsx'
import IconPickerModal from './IconPickerModal.jsx'
import DeleteRoleModal from './DeleteRoleModal.jsx'

const EDGE_STYLE_KEY = 'od-edge-style'
const PALETTE_PANEL_OPEN_KEY = 'od-palette-panel-open'
const INSPECTOR_PANEL_OPEN_KEY = 'od-inspector-panel-open'
const LAYOUT_SAVE_DEBOUNCE_MS = 400
const FLASH_WINDOW_MS = 1500

// Wraps hydrate() with a visual-only icon fallback: the AI chat is
// deliberately allowed to skip ui.icon (mastermind-createorg's own
// guidance — "never force-fit a role into a nearby-but-wrong archetype"),
// so most roles arrive icon-less. Rather than every icon-less role
// rendering the same hardcoded 'coder' avatar (RoleNode.jsx's fallback),
// resolve a sensible per-role match via suggestIcon()'s token-overlap
// scoring — memoized per role id, so this is deterministic across repeated
// hydrations and never needs to be written back to the org config.
async function hydrateWithIcons(roles) {
  const nodes = hydrate(roles || [])
  const manifest = await loadIconManifest()
  const known = new Set(manifest.map(a => a.id))
  await Promise.all(nodes.map(async (n) => {
    if (n.icon && known.has(n.icon)) return
    n.icon = await suggestIcon({ id: n.id, title: n.title, type: n.type, ui: { icon: n.icon } })
  }))
  return nodes
}

export default function OrgDesigner({ orgName, fullscreen = false, onToggleFullscreen }) {
  const [loading, setLoading] = useState(true)
  const [orgMeta, setOrgMeta] = useState(null) // { name, goal, status, schedule, run_config, ... } minus roles
  const [nodes, setNodes] = useState([])
  const [camera, setCamera] = useState({ x: 0, y: 0, zoom: 1 })
  const [selectedId, setSelectedId] = useState(null)
  const [pendingEdgeRaw, setPendingEdgeRaw] = useState(null) // { childId, sx, sy, tx, ty, hoveredNodeId }
  const [iconPickerFor, setIconPickerFor] = useState(null) // role id, or null
  const [deleteTarget, setDeleteTarget] = useState(null) // node, or null
  const [pendingUpdateCount, setPendingUpdateCount] = useState(0)
  const [edgeStyle, setEdgeStyle] = useState(() => {
    try { return localStorage.getItem(EDGE_STYLE_KEY) === 'elbow' ? 'elbow' : 'bezier' } catch { return 'bezier' }
  })
  // Both side panels start folded — the canvas is the point of the Design
  // tab; the palette and inspector are opt-in via their toggle strips (or,
  // for the inspector, opened automatically by selecting a role below).
  const [paletteOpen, setPaletteOpenState] = useState(() => {
    try { return localStorage.getItem(PALETTE_PANEL_OPEN_KEY) === '1' } catch { return false }
  })
  const [inspectorOpen, setInspectorOpenState] = useState(() => {
    try { return localStorage.getItem(INSPECTOR_PANEL_OPEN_KEY) === '1' } catch { return false }
  })
  const setPaletteOpen = useCallback((v) => {
    setPaletteOpenState(v)
    try { localStorage.setItem(PALETTE_PANEL_OPEN_KEY, v ? '1' : '0') } catch { /* ignore */ }
  }, [])
  const setInspectorOpen = useCallback((v) => {
    setInspectorOpenState(v)
    try { localStorage.setItem(INSPECTOR_PANEL_OPEN_KEY, v ? '1' : '0') } catch { /* ignore */ }
  }, [])
  // Clicking a role opens ONLY the inspector — the palette's open/closed
  // state is left exactly as-is (manual toggle is the only way to open it).
  const handleSelectNode = useCallback((id) => {
    setSelectedId(id)
    setInspectorOpen(true)
  }, [setInspectorOpen])

  const nodesRef = useRef(nodes)
  nodesRef.current = nodes
  const revRef = useRef(null)
  const isInteractingRef = useRef(false)
  const pendingPatchRef = useRef(null) // queued live-update payload while interacting
  const layoutSaveTimerRef = useRef(null)
  const dirtyLayoutRef = useRef(new Map()) // roleId -> {x,y,icon,color} awaiting a debounced save

  // ── Palette → canvas ghost drag ──────────────────────────────────────────
  // Native HTML5 drag-and-drop (wired in RolePalette.jsx/OrgCanvas.jsx) is
  // kept as a first attempt, but NodeRunner.jsx's own Workflows palette
  // deliberately does NOT rely on it — it uses a plain mousedown-track-
  // mouseup ghost drag instead, which is the proven-working pattern in this
  // exact Wails/WebKit app. Mirroring that here as the reliable path rather
  // than gambling on native DnD behaving the same way in this webview.
  const canvasOuterRef = useRef(null)
  const cameraRef = useRef(camera)
  cameraRef.current = camera
  const ghostRef = useRef(null) // { item }, mirrors ghost state for the mouseup handler closure
  const [ghost, setGhost] = useState(null) // { item, x, y } — screen coords

  // ── Load ──────────────────────────────────────────────────────────────
  const load = useCallback(async () => {
    if (!orgName) { setOrgMeta(null); setNodes([]); setLoading(false); return }
    setLoading(true)
    const res = await api.getOrgDesign(orgName)
    if (!res || !res.org) { setOrgMeta(null); setNodes([]); setLoading(false); return }
    const { roles, ...meta } = res.org
    setOrgMeta(meta)
    revRef.current = res.rev ?? null
    let hydrated = await hydrateWithIcons(roles || [])
    hydrated = layOutMissingPositions(hydrated)
    setNodes(hydrated)
    setSelectedId(null)
    setLoading(false)
  }, [orgName])

  useEffect(() => { load() }, [load])

  // ── Live updates ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!orgName) return
    return onOrgDesignUpdated((payload) => {
      if (!payload || payload.orgName !== orgName) return
      if (payload.rev != null && payload.rev === revRef.current) return // echo of our own write
      if (isInteractingRef.current) {
        pendingPatchRef.current = payload
        setPendingUpdateCount(c => c + 1)
        return
      }
      applyLivePatch(payload)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgName])

  const applyLivePatch = useCallback(async (payload) => {
    revRef.current = payload.rev ?? revRef.current
    if (payload.deleted || !payload.org) {
      setOrgMeta(null)
      setNodes([])
      return
    }
    const { roles, ...meta } = payload.org
    setOrgMeta(meta)

    const incoming = await hydrateWithIcons(roles || [])
    const now = Date.now()
    setNodes(prev => {
      const prevById = new Map(prev.map(n => [n.id, n]))
      const next = incoming.map(n => {
        const old = prevById.get(n.id)
        if (!old) {
          // Added by someone else — lay it out under its new parent, keep a
          // fresh-enter flag for one render pass.
          return { ...n, _isNew: true, _flashAt: now }
        }
        prevById.delete(n.id)
        // Keep the user's own hand-placed position; only semantics changed.
        const changed = old.title !== n.title || old.type !== n.type || old.parentId !== n.parentId
          || JSON.stringify(old.responsibilities) !== JSON.stringify(n.responsibilities)
          || JSON.stringify(old.rest) !== JSON.stringify(n.rest) || old.icon !== n.icon
        return {
          ...n,
          x: old.x, y: old.y,
          _flashAt: changed ? now : old._flashAt,
          _isNew: false,
        }
      })
      return layOutMissingPositions(next)
    })
  }, [])

  // Drain a queued live patch once the user stops interacting.
  const endInteraction = useCallback(() => {
    isInteractingRef.current = false
    if (pendingPatchRef.current) {
      const p = pendingPatchRef.current
      pendingPatchRef.current = null
      setPendingUpdateCount(0)
      applyLivePatch(p)
    }
  }, [applyLivePatch])

  // ── Validation (recomputed on every node change) ─────────────────────
  const validation = useMemo(() => validateStructure(nodes), [nodes])

  // ── Selection / persistence helpers ──────────────────────────────────
  const refreshFromServer = useCallback(async (res) => {
    if (!res) return
    if (res.rev != null) revRef.current = res.rev
    if (res.org) {
      const { roles, ...meta } = res.org
      setOrgMeta(meta)
      const incoming = await hydrateWithIcons(roles || [])
      setNodes(prev => {
        const prevById = new Map(prev.map(n => [n.id, n]))
        return layOutMissingPositions(incoming.map(n => {
          const old = prevById.get(n.id)
          return old ? { ...n, x: old.x, y: old.y } : n
        }))
      })
    }
  }, [])

  // ── Palette → canvas: add a role ─────────────────────────────────────
  const addRoleFromArchetype = useCallback(async (item, worldX, worldY, droppedOnNodeId) => {
    if (!orgName) return
    const type = CATEGORY_TYPE[item.category] || 'specialist'
    const role = {
      title: item.label,
      type,
      reports_to: resolveDefaultParent(droppedOnNodeId),
      responsibilities: [],
      ui: { x: worldX, y: worldY, icon: item.id },
    }
    const res = await api.addOrgRole(orgName, role)
    if (!res || res.error) { notify('add role', res?.error || 'failed to add role'); return }
    refreshFromServer(res)
  }, [orgName, refreshFromServer])

  // resolveDefaultParent picks reports_to for a new role when the caller
  // didn't drop it directly onto an existing card: an explicit target wins,
  // otherwise fall back to the org's current root (never bare null unless
  // the canvas is genuinely empty) — a bare null default here is exactly
  // what produced "multiple root roles" the moment a second role was added
  // without a card selected.
  const resolveDefaultParent = useCallback((explicitParentId) => {
    if (explicitParentId) return explicitParentId
    const root = nodesRef.current.find(n => n.parentId == null)
    return root ? root.id : null
  }, [])

  const quickAddRole = useCallback(async (item) => {
    const anchor = selectedId ? nodesRef.current.find(n => n.id === selectedId) : null
    const parentId = resolveDefaultParent(anchor?.id || null)
    const parentNode = parentId ? nodesRef.current.find(n => n.id === parentId) : null
    const x = (parentNode?.x ?? 0) + 40
    const y = (parentNode ? parentNode.y + 140 : 0)
    await addRoleFromArchetype(item, x, y, parentId)
  }, [selectedId, addRoleFromArchetype, resolveDefaultParent])

  // "Define a new role directly" — a blank role not tied to any palette
  // archetype, for when the user wants to author a role from scratch
  // (title/type/responsibilities in the inspector) rather than starting
  // from an icon. Placed under the currently selected role, or the org's
  // root, or becomes the root itself if the canvas is genuinely empty.
  const addCustomRole = useCallback(async () => {
    if (!orgName) return
    const anchor = selectedId ? nodesRef.current.find(n => n.id === selectedId) : null
    const parentId = resolveDefaultParent(anchor?.id || null)
    const parentNode = parentId ? nodesRef.current.find(n => n.id === parentId) : null
    const x = (parentNode?.x ?? 0) + 40
    const y = (parentNode ? parentNode.y + 140 : 0)
    const role = {
      title: 'New Role',
      type: 'specialist',
      reports_to: parentId,
      responsibilities: [],
      ui: { x, y, icon: 'coder' },
    }
    const res = await api.addOrgRole(orgName, role)
    if (!res || res.error) { notify('add role', res?.error || 'failed to add role'); return }
    refreshFromServer(res)
    // Select the freshly created role so the inspector opens on it right
    // away — the whole point of "define a role directly" is to land in
    // edit mode, not just drop a placeholder card on the canvas.
    if (res.org?.roles) {
      const created = res.org.roles.find(r => r.title === 'New Role' && r.reports_to === parentId)
      if (created) setSelectedId(created.id)
    }
  }, [orgName, selectedId, resolveDefaultParent, refreshFromServer])

  const isOverCanvas = useCallback((clientX, clientY) => {
    const el = canvasOuterRef.current
    if (!el) return false
    const rect = el.getBoundingClientRect()
    return clientX >= rect.left && clientX <= rect.right && clientY >= rect.top && clientY <= rect.bottom
  }, [])

  const handlePaletteDragStart = useCallback((item, e) => {
    ghostRef.current = { item }
    setGhost({ item, x: e.clientX, y: e.clientY, inBounds: isOverCanvas(e.clientX, e.clientY) })
  }, [isOverCanvas])

  useEffect(() => {
    const onMove = (e) => {
      if (!ghostRef.current) return
      setGhost(g => (g ? { ...g, x: e.clientX, y: e.clientY, inBounds: isOverCanvas(e.clientX, e.clientY) } : null))
    }
    const onUp = (e) => {
      if (!ghostRef.current) return
      const { item } = ghostRef.current
      ghostRef.current = null
      setGhost(null)
      if (!isOverCanvas(e.clientX, e.clientY)) return // released outside the canvas — not a drop
      const el = canvasOuterRef.current
      const rect = el.getBoundingClientRect()
      const cam = cameraRef.current
      const wx = (e.clientX - rect.left - cam.x) / cam.zoom
      const wy = (e.clientY - rect.top - cam.y) / cam.zoom
      const target = document.elementFromPoint(e.clientX, e.clientY)
      const cardEl = target?.closest?.('[data-od-node-id]')
      addRoleFromArchetype(item, wx, wy, cardEl?.dataset?.odNodeId || null)
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    return () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
    }
  }, [addRoleFromArchetype, isOverCanvas])

  // ── Canvas node drag → debounced layout save ──────────────────────────
  const handleNodesChange = useCallback((updated) => {
    setNodes(updated)
    for (const n of updated) {
      dirtyLayoutRef.current.set(n.id, { x: n.x, y: n.y, icon: n.icon || '', color: n.color || '' })
    }
    if (layoutSaveTimerRef.current) clearTimeout(layoutSaveTimerRef.current)
    layoutSaveTimerRef.current = setTimeout(async () => {
      if (!orgName || dirtyLayoutRef.current.size === 0) return
      const layout = Object.fromEntries(dirtyLayoutRef.current)
      dirtyLayoutRef.current = new Map()
      const res = await api.saveOrgLayout(orgName, layout)
      if (res?.rev != null) revRef.current = res.rev
    }, LAYOUT_SAVE_DEBOUNCE_MS)
  }, [orgName])

  const handleNodeDragStart = useCallback(() => { isInteractingRef.current = true }, [])
  const handleNodeDragEnd = useCallback(() => { endInteraction() }, [endInteraction])

  // ── Reports-to edge drag ───────────────────────────────────────────────
  const handlePendingEdgeStart = useCallback((childId, sx, sy) => {
    isInteractingRef.current = true
    setPendingEdgeRaw({ childId, sx, sy, tx: sx, ty: sy, hoveredNodeId: null })
  }, [])
  const handlePendingEdgeChange = useCallback((childId, tx, ty, hoveredNodeId) => {
    setPendingEdgeRaw(prev => (prev ? { ...prev, tx, ty, hoveredNodeId } : prev))
  }, [])
  const pendingEdge = useMemo(() => {
    if (!pendingEdgeRaw) return null
    const { childId, hoveredNodeId } = pendingEdgeRaw
    const valid = !!hoveredNodeId && !wouldCycle(nodesRef.current, childId, hoveredNodeId)
    return { ...pendingEdgeRaw, validTargetId: valid ? hoveredNodeId : null, valid }
  }, [pendingEdgeRaw])

  const handleEdgeCommit = useCallback(async (childId, parentId) => {
    setPendingEdgeRaw(null)
    endInteraction()
    if (!orgName || childId === parentId) return
    const res = await api.setOrgRoleReportsTo(orgName, childId, parentId || '')
    if (!res || res.error) { notify('set reports to', res?.error || 'failed'); return }
    refreshFromServer(res)
  }, [orgName, refreshFromServer, endInteraction])

  const handleEdgeCycleRejected = useCallback((childId, parentId) => {
    setPendingEdgeRaw(null)
    endInteraction()
    notify('reports to', `That would create a circular reporting loop.`)
  }, [endInteraction])

  // ── Inspector patch ────────────────────────────────────────────────────
  const handlePatch = useCallback(async (roleId, patch) => {
    if (!orgName) return
    if (Object.prototype.hasOwnProperty.call(patch, '_renameId')) {
      notify('rename role', 'Renaming a role id is not supported yet — delete and recreate the role instead.')
      const rest = { ...patch }
      delete rest._renameId
      if (Object.keys(rest).length === 0) return
      patch = rest
    }
    const res = await api.updateOrgRole(orgName, roleId, patch)
    if (!res || res.error) { notify('update role', res?.error || 'failed to update role'); return }
    refreshFromServer(res)
  }, [orgName, refreshFromServer])

  // Reports-to is NOT one of UpdateOrgRole's patchable fields (see the Go
  // side's `raw` struct in app_orgs_design.go) — it has its own dedicated
  // mutator (SetOrgRoleReportsTo) because moving a role in the tree needs
  // its own cycle check, distinct from a plain field patch. The canvas's
  // edge-drag already calls this; the inspector's "Reports to" dropdown
  // needs the same path rather than routing through handlePatch, which
  // would just silently drop the field.
  const handleSetReportsTo = useCallback(async (roleId, parentId) => {
    if (!orgName || !roleId) return
    const res = await api.setOrgRoleReportsTo(orgName, roleId, parentId || '')
    if (!res || res.error) { notify('set reports to', res?.error || 'failed to update reports-to'); return }
    refreshFromServer(res)
  }, [orgName, refreshFromServer])

  // "Set as org boss" — distinct from handleSetReportsTo(id, null): that
  // path (SetOrgRoleReportsTo) just refuses when a different root already
  // exists, so making someone else the boss needs the dedicated swap
  // mutator (PromoteToRoot) instead, correct at any depth.
  const handlePromoteToRoot = useCallback(async (roleId) => {
    if (!orgName || !roleId) return
    const res = await api.promoteRoleToRoot(orgName, roleId)
    if (!res || res.error) { notify('set as org boss', res?.error || 'failed to promote role'); return }
    refreshFromServer(res)
  }, [orgName, refreshFromServer])

  // ── Delete flow ─────────────────────────────────────────────────────────
  const handleDeleteConfirm = useCallback(async (strategy, reassignToId) => {
    const node = deleteTarget
    setDeleteTarget(null)
    if (!orgName || !node) return
    if (strategy === 'reassign' && reassignToId) {
      const children = nodesRef.current.filter(n => n.parentId === node.id)
      for (const c of children) {
        const r = await api.setOrgRoleReportsTo(orgName, c.id, reassignToId)
        if (!r || r.error) { notify('reassign role', r?.error || 'failed'); return }
      }
      const res = await api.removeOrgRole(orgName, node.id, 'reparent')
      if (!res || res.error) { notify('delete role', res?.error || 'failed'); return }
      refreshFromServer(res)
      return
    }
    const backendStrategy = strategy === 'cascade' ? 'cascade' : 'reparent'
    const res = await api.removeOrgRole(orgName, node.id, backendStrategy)
    if (!res || res.error) { notify('delete role', res?.error || 'failed to delete role'); return }
    if (selectedId === node.id) setSelectedId(null)
    refreshFromServer(res)
  }, [orgName, deleteTarget, selectedId, refreshFromServer])

  // ── Icon picker ─────────────────────────────────────────────────────────
  const selectedNode = useMemo(() => nodes.find(n => n.id === selectedId) || null, [nodes, selectedId])
  const iconPickerNode = useMemo(() => nodes.find(n => n.id === iconPickerFor) || null, [nodes, iconPickerFor])

  const handleIconSelected = useCallback(async (iconId) => {
    const roleId = iconPickerFor
    setIconPickerFor(null)
    if (!roleId) return
    await handlePatch(roleId, { icon: iconId })
  }, [iconPickerFor, handlePatch])

  // ── Toolbar actions ────────────────────────────────────────────────────
  const setEdgeStylePersist = (v) => {
    setEdgeStyle(v)
    try { localStorage.setItem(EDGE_STYLE_KEY, v) } catch { /* best-effort */ }
  }
  const handleTidy = useCallback(async () => {
    if (!orgName) return
    const tidied = tidyTree(nodesRef.current)
    setNodes(tidied)
    const layout = Object.fromEntries(tidied.map(n => [n.id, { x: n.x, y: n.y, icon: n.icon || '', color: n.color || '' }]))
    const res = await api.saveOrgLayout(orgName, layout)
    if (res?.rev != null) revRef.current = res.rev
  }, [orgName])

  const subordinateCounts = useMemo(() => {
    const { childrenOf } = buildTree(nodes)
    const m = new Map()
    for (const n of nodes) m.set(n.id, (childrenOf.get(n.id) || []).length)
    return m
  }, [nodes])

  if (!orgName) {
    return (
      <div className="empty-state" style={{ flex: 1 }}>
        <div className="empty-state-icon"><GitBranch size={30} /></div>
        <div className="empty-state-title">Select an org</div>
        <div className="empty-state-desc">Pick an org from the list to design its role hierarchy.</div>
      </div>
    )
  }

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px', borderBottom: '1px solid var(--border)', flexShrink: 0 }}>
        <Milestone size={12} style={{ color: 'var(--text-muted)' }} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, color: 'var(--text-secondary)' }}>{orgName}</span>
        {!validation.valid && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 9.5, color: '#f87171' }}>{validation.errors.length} issue{validation.errors.length === 1 ? '' : 's'}</span>
        )}
        {pendingUpdateCount > 0 && (
          <button
            onClick={endInteraction}
            style={{ fontFamily: 'var(--font-mono)', fontSize: 9.5, color: 'var(--teal, #00f5d4)', background: 'transparent', border: '1px solid currentColor', borderRadius: 4, padding: '2px 6px', cursor: 'pointer' }}
          >
            {pendingUpdateCount} update{pendingUpdateCount === 1 ? '' : 's'} pending
          </button>
        )}
        <div style={{ flex: 1 }} />
        <button onClick={addCustomRole} title="Define a new role directly (no icon needed)" style={toolbarBtnStyle}>+ Role</button>
        <button onClick={handleTidy} title="Tidy layout" style={toolbarBtnStyle}>Tidy</button>
        <button
          onClick={() => setEdgeStylePersist(edgeStyle === 'bezier' ? 'elbow' : 'bezier')}
          title="Toggle connector style"
          style={toolbarBtnStyle}
        >
          {edgeStyle === 'bezier' ? 'Curved' : 'Elbow'}
        </button>
        <button onClick={load} title="Reload" style={toolbarBtnStyle}><RefreshCw size={11} /></button>
        {onToggleFullscreen && (
          <button onClick={onToggleFullscreen} title={fullscreen ? 'Exit fullscreen' : 'Fullscreen'} style={toolbarBtnStyle}>
            {fullscreen ? <Minimize2 size={11} /> : <Maximize2 size={11} />}
          </button>
        )}
      </div>

      <div style={{ flex: 1, display: 'flex', minHeight: 0 }}>
        {paletteOpen ? (
          <div style={{ width: 220, flexShrink: 0, minHeight: 0, borderRight: '1px solid var(--border)', overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
            <button onClick={() => setPaletteOpen(false)} title="Collapse role search" style={panelFoldBtnStyle}>
              <ChevronLeft size={11} /> Collapse
            </button>
            <div style={{ flex: 1, minHeight: 0, overflow: 'hidden', display: 'flex' }}>
              <RolePalette onDragStart={handlePaletteDragStart} onQuickAdd={quickAddRole} />
            </div>
          </div>
        ) : (
          <button
            onClick={() => setPaletteOpen(true)}
            title="Search / add roles"
            style={panelStripStyle('right')}
          >
            <Search size={13} />
          </button>
        )}

        <div ref={canvasOuterRef} style={{ flex: 1, position: 'relative', minWidth: 0 }}>
          {ghost && (
            <div style={{
              position: 'absolute', inset: 6, zIndex: 999, borderRadius: 10,
              border: `2px dashed ${ghost.inBounds ? 'var(--teal, #00f5d4)' : 'rgba(148,163,184,0.35)'}`,
              background: ghost.inBounds ? 'rgba(0,245,212,0.05)' : 'transparent',
              pointerEvents: 'none', transition: 'border-color 80ms, background 80ms',
            }} />
          )}
          {loading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 24 }}><div className="spinner" /></div>
          ) : (
            <OrgCanvas
              nodes={nodes}
              camera={camera}
              onCameraChange={setCamera}
              edgeStyle={edgeStyle}
              selectedId={selectedId}
              onSelectNode={handleSelectNode}
              onCanvasClick={() => setSelectedId(null)}
              onNodesChange={handleNodesChange}
              onNodeDragStart={handleNodeDragStart}
              onNodeDragEnd={handleNodeDragEnd}
              pendingEdge={pendingEdge}
              onPendingEdgeStart={handlePendingEdgeStart}
              onPendingEdgeChange={handlePendingEdgeChange}
              onEdgeCommit={handleEdgeCommit}
              onEdgeCycleRejected={handleEdgeCycleRejected}
              onRoleDropFromPalette={addRoleFromArchetype}
              onDeleteNode={(id) => setDeleteTarget(nodesRef.current.find(n => n.id === id) || null)}
            />
          )}
        </div>

        {inspectorOpen ? (
          <div style={{ width: 300, flexShrink: 0, minHeight: 0, borderLeft: '1px solid var(--border)', overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
            <button onClick={() => setInspectorOpen(false)} title="Collapse role editor" style={panelFoldBtnStyle}>
              Collapse <ChevronRight size={11} />
            </button>
            <div style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
              <RoleInspector
                node={selectedNode}
                allNodes={nodes}
                onPatch={(patch) => selectedId && handlePatch(selectedId, patch)}
                onSetReportsTo={(parentId) => selectedId && handleSetReportsTo(selectedId, parentId)}
                onPromoteToRoot={() => selectedId && handlePromoteToRoot(selectedId)}
                onOpenIconPicker={() => selectedId && setIconPickerFor(selectedId)}
                onDelete={() => selectedNode && setDeleteTarget(selectedNode)}
              />
            </div>
          </div>
        ) : (
          <button
            onClick={() => setInspectorOpen(true)}
            title="Role editor"
            style={panelStripStyle('left')}
          >
            <SlidersHorizontal size={13} />
          </button>
        )}
      </div>

      <IconPickerModal
        open={!!iconPickerFor}
        currentIconId={iconPickerNode?.icon}
        onSelect={handleIconSelected}
        onClose={() => setIconPickerFor(null)}
      />
      <DeleteRoleModal
        open={!!deleteTarget}
        node={deleteTarget}
        allNodes={nodes}
        onConfirm={handleDeleteConfirm}
        onClose={() => setDeleteTarget(null)}
      />

      {ghost && (
        <div
          style={{
            position: 'fixed', left: ghost.x + 12, top: ghost.y - 16, zIndex: 1000,
            pointerEvents: 'none', display: 'flex', alignItems: 'center', gap: 6,
            padding: '4px 8px 4px 4px', borderRadius: 16,
            background: 'rgba(8,12,20,0.92)', border: '1.5px solid rgba(0,180,216,0.4)',
            boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
          }}
        >
          <img src={iconUrl(ghost.item.id)} alt="" style={{ width: 22, height: 22, borderRadius: '50%' }} />
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10.5, color: '#e2e8f0', whiteSpace: 'nowrap' }}>
            {ghost.item.label}
          </span>
        </div>
      )}
    </div>
  )
}

const toolbarBtnStyle = {
  display: 'flex', alignItems: 'center', gap: 4,
  fontFamily: 'var(--font-mono)', fontSize: 10, padding: '4px 8px', borderRadius: 'var(--radius)',
  background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-muted)', cursor: 'pointer',
}

// Collapse button shown inside an expanded palette/inspector panel.
const panelFoldBtnStyle = {
  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4, flexShrink: 0,
  fontFamily: 'var(--font-mono)', fontSize: 9.5, padding: '5px 6px',
  background: 'transparent', border: 'none', borderBottom: '1px solid var(--border)',
  color: 'var(--text-muted)', cursor: 'pointer',
}

// Narrow vertical strip shown in place of a folded panel — click to expand.
// `borderSide` is which edge touches the canvas ('right' for the left
// panel's strip, 'left' for the right panel's strip).
function panelStripStyle(borderSide) {
  const base = {
    width: 26, flexShrink: 0, minHeight: 0,
    display: 'flex', alignItems: 'flex-start', justifyContent: 'center', paddingTop: 10,
    background: 'transparent', border: 'none',
    color: 'var(--text-muted)', cursor: 'pointer',
  }
  return borderSide === 'right'
    ? { ...base, borderRight: '1px solid var(--border)' }
    : { ...base, borderLeft: '1px solid var(--border)' }
}

// layOutMissingPositions runs tidyTree scoped to just the nodes missing an
// x/y (freshly arrived from the CLI or an AI tool with no ui.x/ui.y) so a
// newly-added role gets a sane position without relocating anything the
// user has already placed by hand. Simple approach: if ANY node is missing
// a position, tidy the whole tree once (cheap for typical org sizes) but
// only overwrite x/y on the nodes that were actually missing one.
function layOutMissingPositions(nodes) {
  const missing = nodes.filter(n => typeof n.x !== 'number' || typeof n.y !== 'number')
  if (missing.length === 0) return nodes
  const tidied = tidyTree(nodes)
  const tidiedById = new Map(tidied.map(n => [n.id, n]))
  return nodes.map(n => {
    if (typeof n.x === 'number' && typeof n.y === 'number') return n
    const t = tidiedById.get(n.id)
    return t ? { ...n, x: t.x, y: t.y } : { ...n, x: 0, y: 0 }
  })
}
