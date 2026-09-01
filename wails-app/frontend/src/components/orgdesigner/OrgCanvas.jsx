// Org Designer canvas viewport. Lifts NodeRunner.jsx's pan/zoom/drag-via-refs
// mechanics wholesale (see src/pages/NodeRunner.jsx), but with vertical
// parent-above/child-below connectors (org-chart convention) instead of
// NodeRunner's horizontal workflow ports, and single-parent "replace"
// reports_to semantics instead of arbitrary multi-edges.
//
// ── Prop contract ──────────────────────────────────────────────────────
// This component owns its own transient interaction state (pan, zoom, node
// drag, pending-edge drag — all via refs+local state, exactly like
// NodeRunner) but does NOT own the authoritative `nodes` array: that lives
// in the OrgDesigner.jsx container, which is the only thing allowed to
// create/delete roles or persist to disk. OrgCanvas reports finished
// interactions upward via callbacks; the container decides what to do with
// them (including rejecting/undoing).
//
//   nodes            canvas node array (orgGraph.hydrate() shape), each with
//                     numeric x/y already assigned (container runs tidyTree
//                     on newly-arrived roles with no ui.x/ui.y before
//                     passing them down — this component does not lay out
//                     missing positions itself, only renders whatever it's
//                     given)
//   camera           { x, y, zoom } — screenX = worldX*zoom + x
//   onCameraChange(camera)     — called on every pan/zoom/reset/fit; the
//                     container is the source of truth for camera state
//                     too (controlled component), mirroring NodeRunner's
//                     own useState-at-the-page-level pattern
//   edgeStyle        'bezier' | 'elbow' — which orgGraph path fn to use
//   selectedId        currently selected role id, or null
//   onSelectNode(id)  fired on node mousedown-select
//   onCanvasClick()   fired on empty-canvas click (deselect)
//   onNodesChange(updatedNodes)
//                     fired continuously while a node-position drag is in
//                     flight (and once more on drag end) — the FULL nodes
//                     array with the dragged node's x/y updated in place.
//                     The container is free to treat in-flight updates as
//                     "local echo only" and only actually persist on drag
//                     end; OrgCanvas doesn't know or care about persistence.
//   onNodeDragStart(id, e)
//                     optional — fired once when a node-drag begins, purely
//                     informational (e.g. so the container can suppress
//                     live-update reconciliation for this role until the
//                     drag ends, per the plan's mid-drag anti-clobber rule)
//   onNodeDragEnd(id)
//                     optional — fired once when a node-drag ends (mouseup)
//   pendingEdge       null, or the container's authoritative pending-edge
//                     state while a reports_to drag is in flight:
//                     { childId, sx, sy, tx, ty, validTargetId }
//                     OrgCanvas RAISES the raw drag geometry via
//                     onPendingEdgeChange as the mouse moves; the container
//                     computes wouldCycle() against the hovered target and
//                     passes the (possibly annotated) result back down as
//                     this prop, which is what's actually rendered. This
//                     keeps orgGraph's cycle logic out of OrgCanvas, per the
//                     plan's "OrgCanvas doesn't need to call wouldCycle
//                     itself" note.
//   onPendingEdgeStart(childId, sx, sy)
//   onPendingEdgeChange(childId, tx, ty, hoveredNodeId)
//                     fired continuously while dragging a new reports_to
//                     edge from a child's top handle; hoveredNodeId is
//                     whatever role card the pointer is currently over (or
//                     null), so the container can validate it live
//   onEdgeCommit(childId, parentId)
//                     fired on mouseup over a valid drop target — the
//                     container performs the actual SetReportsTo mutation
//   onEdgeCycleRejected(childId, parentId)
//                     fired instead of onEdgeCommit when the drop target
//                     was invalid (cycle) — for a toast/shake, no mutation
//   onRoleDropFromPalette(paletteItem, worldX, worldY, droppedOnNodeId)
//                     fired when an external palette drag-and-drop (see
//                     RolePalette.jsx, a sibling component) lands on the
//                     canvas — the container creates the actual role.
//                     paletteItem is whatever RolePalette hands off (opaque
//                     to OrgCanvas); droppedOnNodeId is set when the drop
//                     landed on top of an existing card (container may
//                     interpret that as "insert as a report of this role").
//                     OrgCanvas exposes this purely as a native HTML5 DnD
//                     target (onDragOver/onDrop) — RolePalette is expected
//                     to set draggable + dataTransfer accordingly.
//   onDeleteNode(id)
//   viewportSize      optional { width, height } override for fitCamera's
//                     target box; if omitted, OrgCanvas measures its own
//                     wrapper element at call time (Cmd/Ctrl+0, "F")
//
// Keyboard (attached while this component is mounted):
//   Cmd/Ctrl+0   reset camera to { x:0, y:0, zoom:1 }
//   F            fitCamera(nodes, viewportWidth, viewportHeight)
//   Space (hold) forces pan-drag mode even when not clicking empty canvas
//
// Rendering: two-layer, matching NodeRunner — an absolutely-positioned SVG
// with a <g transform="translate(cx,cy) scale(z)"> for edges (each edge is a
// thin visible <path> + an invisible wide hit-path on top, mirroring
// NodeRunner's click-to-delete edge affordance), and a sibling <div> with a
// matching CSS transform hosting the HTML RoleNode cards.

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  NODE_W, NODE_H,
  buildTree, validateStructure,
  reportPath, elbowPath,
  topHandlePos, bottomHandlePos,
  fitCamera,
} from './orgGraph'
import RoleNode from './RoleNode'

const GRID_SIZE = 28
const ZOOM_MIN = 0.15
const ZOOM_MAX = 2.5

function edgeColorFor(parentNode) {
  return parentNode?.color || 'rgba(148,163,184,0.45)'
}

export default function OrgCanvas({
  nodes = [],
  camera = { x: 0, y: 0, zoom: 1 },
  onCameraChange,
  edgeStyle = 'bezier',
  selectedId = null,
  onSelectNode,
  onCanvasClick,
  onNodesChange,
  onNodeDragStart,
  onNodeDragEnd,
  pendingEdge = null,
  onPendingEdgeStart,
  onPendingEdgeChange,
  onEdgeCommit,
  onEdgeCycleRejected,
  onRoleDropFromPalette,
  onDeleteNode,
  viewportSize,
}) {
  const wrapperRef = useRef(null)
  const dragRef = useRef(null) // { type: 'canvas'|'node'|'edge', ... }
  const nodesRef = useRef(nodes)
  const cameraRef = useRef(camera)
  const spaceHeldRef = useRef(false)
  const hoveredDropIdRef = useRef(null)
  const [hoveredDropId, setHoveredDropId] = useState(null)

  useEffect(() => { nodesRef.current = nodes }, [nodes])
  useEffect(() => { cameraRef.current = camera }, [camera])

  const { childrenOf, depthOf, roots } = buildTree(nodes)
  const { valid, errors } = validateStructure(nodes)

  const getViewport = useCallback(() => {
    if (viewportSize) return viewportSize
    const rect = wrapperRef.current?.getBoundingClientRect()
    return { width: rect?.width || 900, height: rect?.height || 600 }
  }, [viewportSize])

  const toWorld = useCallback((cx, cy) => {
    const rect = wrapperRef.current?.getBoundingClientRect() || { left: 0, top: 0 }
    const cam = cameraRef.current
    return { x: (cx - rect.left - cam.x) / cam.zoom, y: (cy - rect.top - cam.y) / cam.zoom }
  }, [])

  // ── Global mouse handlers (pan / node-drag / edge-drag) ────────────────
  useEffect(() => {
    const onMove = (e) => {
      const d = dragRef.current
      if (!d) return
      if (d.type === 'canvas') {
        onCameraChange?.({ ...cameraRef.current, x: d.camX + (e.clientX - d.startX), y: d.camY + (e.clientY - d.startY) })
      } else if (d.type === 'node') {
        const cam = cameraRef.current
        const dx = (e.clientX - d.startX) / cam.zoom
        const dy = (e.clientY - d.startY) / cam.zoom
        const nx = d.nx + dx, ny = d.ny + dy
        onNodesChange?.(nodesRef.current.map(n => n.id === d.nodeId ? { ...n, x: nx, y: ny } : n))
      } else if (d.type === 'edge') {
        const w = toWorld(e.clientX, e.clientY)
        // Determine hovered card via elementFromPoint so we can report it
        const el = document.elementFromPoint(e.clientX, e.clientY)
        const cardEl = el?.closest?.('[data-od-node-id]')
        const hoveredId = cardEl?.dataset?.odNodeId || null
        hoveredDropIdRef.current = hoveredId
        setHoveredDropId(hoveredId)
        onPendingEdgeChange?.(d.childId, w.x, w.y, hoveredId)
      }
    }
    const onUp = () => {
      const d = dragRef.current
      if (d?.type === 'edge') {
        const targetId = hoveredDropIdRef.current
        if (targetId && targetId !== d.childId) {
          const wouldBeCycle = pendingEdge && pendingEdge.validTargetId != null
            ? pendingEdge.validTargetId !== targetId
            : false
          if (wouldBeCycle) {
            onEdgeCycleRejected?.(d.childId, targetId)
          } else {
            onEdgeCommit?.(d.childId, targetId)
          }
        }
        hoveredDropIdRef.current = null
        setHoveredDropId(null)
      } else if (d?.type === 'node') {
        onNodeDragEnd?.(d.nodeId)
      }
      dragRef.current = null
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    return () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
    }
  }, [toWorld, onCameraChange, onNodesChange, onPendingEdgeChange, onEdgeCommit, onEdgeCycleRejected, onNodeDragEnd, pendingEdge])

  // ── Wheel zoom, cursor-anchored, clamped ────────────────────────────────
  useEffect(() => {
    const el = wrapperRef.current
    if (!el) return
    const onWheel = (e) => {
      e.preventDefault()
      const factor = e.deltaY < 0 ? 1.1 : 0.9
      const cam = cameraRef.current
      const z = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, cam.zoom * factor))
      const rect = el.getBoundingClientRect()
      const mx = e.clientX - rect.left, my = e.clientY - rect.top
      onCameraChange?.({
        x: mx - (mx - cam.x) * (z / cam.zoom),
        y: my - (my - cam.y) * (z / cam.zoom),
        zoom: z,
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [onCameraChange])

  // ── Keyboard: Cmd/Ctrl+0 reset, F fit, Space pan-force ──────────────────
  useEffect(() => {
    const onKeyDown = (e) => {
      if (['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) return
      if ((e.metaKey || e.ctrlKey) && e.key === '0') {
        e.preventDefault()
        onCameraChange?.({ x: 0, y: 0, zoom: 1 })
      } else if (e.key === 'f' || e.key === 'F') {
        const { width, height } = getViewport()
        onCameraChange?.(fitCamera(nodesRef.current, width, height))
      } else if (e.code === 'Space') {
        spaceHeldRef.current = true
      }
    }
    const onKeyUp = (e) => {
      if (e.code === 'Space') spaceHeldRef.current = false
    }
    window.addEventListener('keydown', onKeyDown)
    window.addEventListener('keyup', onKeyUp)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('keyup', onKeyUp)
    }
  }, [onCameraChange, getViewport])

  // ── Native HTML5 DnD target for RolePalette drops ───────────────────────
  const handleDragOver = (e) => { e.preventDefault() }
  const handleDrop = (e) => {
    e.preventDefault()
    let paletteItem = null
    try {
      const raw = e.dataTransfer.getData('application/x-org-role') || e.dataTransfer.getData('text/plain')
      paletteItem = raw ? JSON.parse(raw) : null
    } catch {
      paletteItem = null
    }
    if (!paletteItem) return
    const w = toWorld(e.clientX, e.clientY)
    const el = document.elementFromPoint(e.clientX, e.clientY)
    const cardEl = el?.closest?.('[data-od-node-id]')
    onRoleDropFromPalette?.(paletteItem, w.x, w.y, cardEl?.dataset?.odNodeId || null)
  }

  const pathFn = edgeStyle === 'elbow' ? elbowPath : reportPath

  // Build edge list: each non-root node connects to its parent.
  const edges = nodes
    .filter(n => n.parentId != null)
    .map(n => {
      const parent = nodes.find(p => p.id === n.parentId)
      if (!parent) return null
      const s = topHandlePos(n)
      const t = bottomHandlePos(parent)
      const depth = depthOf.get(n.id) || 0
      return {
        id: `${n.id}->${parent.id}`,
        path: pathFn(s.x, s.y, t.x, t.y),
        color: edgeColorFor(parent),
        width: Math.max(1.0, 1.8 - depth * 0.15),
      }
    })
    .filter(Boolean)

  const pendingPath = pendingEdge
    ? pathFn(pendingEdge.sx, pendingEdge.sy, pendingEdge.tx, pendingEdge.ty)
    : null
  const pendingColor = pendingEdge
    ? (pendingEdge.validTargetId != null ? 'var(--green)' : 'var(--red)')
    : null

  const singleRoot = roots.length === 1 ? roots[0] : null

  return (
    // absolute+inset:0 (not flex:1) so this box always exactly fills
    // OrgDesigner.jsx's canvasOuterRef (a position:relative box with a
    // definite flex-stretched height) — flex:1 alone has no effect here
    // since canvasOuterRef isn't itself a flex container, which previously
    // left this root's height resolved from content instead of matching
    // its parent, and was the other half of the drop-coordinate mismatch.
    <div style={{ position: 'absolute', inset: 0 }}>
      {/* Overlaid (not flex-flow) on purpose — wrapperRef below must always
          exactly fill this outer box regardless of whether this banner is
          showing, since OrgDesigner.jsx's ghost-drag drop math measures the
          OUTER box's rect (it has no ref into this component's internals)
          and assumes it matches wrapperRef's rect 1:1. A flex-flow banner
          that pushes wrapperRef down would silently shift every subsequent
          drop's computed world position by the banner's height. */}
      {!valid && errors.length > 0 && (
        <div style={{
          position: 'absolute', top: 0, left: 0, right: 0, zIndex: 5,
          background: 'rgba(239,68,68,0.08)',
          borderBottom: '1px solid rgba(239,68,68,0.3)',
          padding: '6px 12px',
          display: 'flex', flexDirection: 'column', gap: 2,
        }}>
          {errors.map((err, i) => (
            <div key={i} style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--red)' }}>
              ⚠ {err}
            </div>
          ))}
        </div>
      )}

      <div
        ref={wrapperRef}
        style={{ position: 'absolute', inset: 0, overflow: 'hidden', cursor: spaceHeldRef.current ? 'grab' : 'default' }}
        onMouseDown={(e) => {
          if (e.target !== wrapperRef.current && !e.target.dataset.bg) return
          onSelectNode?.(null)
          onCanvasClick?.()
          dragRef.current = { type: 'canvas', startX: e.clientX, startY: e.clientY, camX: cameraRef.current.x, camY: cameraRef.current.y }
        }}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        {nodes.length === 0 ? (
          <div style={{
            position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
            pointerEvents: 'none',
          }}>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 13, color: 'var(--text-muted)', textAlign: 'center' }}>
              Drag a role from the left to start your org
            </div>
          </div>
        ) : (
          <>
            {/* Dot grid */}
            <div data-bg="1" style={{
              position: 'absolute', inset: 0,
              backgroundImage: 'radial-gradient(circle,rgba(0,180,216,0.18) 1.2px,transparent 1.2px)',
              backgroundSize: `${GRID_SIZE}px ${GRID_SIZE}px`,
              backgroundPosition: `${camera.x % GRID_SIZE}px ${camera.y % GRID_SIZE}px`,
              pointerEvents: 'none',
            }} />

            {/* Soft radial vignette behind a single root */}
            {singleRoot && (
              <div style={{
                position: 'absolute',
                left: singleRoot.x * camera.zoom + camera.x + (NODE_W * camera.zoom) / 2 - 220,
                top: singleRoot.y * camera.zoom + camera.y + (NODE_H * camera.zoom) / 2 - 220,
                width: 440, height: 440,
                background: 'radial-gradient(circle, rgba(234,179,8,0.10) 0%, transparent 70%)',
                pointerEvents: 'none',
              }} />
            )}

            {/* SVG edge layer */}
            <svg style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', overflow: 'visible', zIndex: 1, pointerEvents: 'none' }}>
              <g transform={`translate(${camera.x} ${camera.y}) scale(${camera.zoom})`}>
                {edges.map(ep => (
                  <g key={ep.id}>
                    <path d={ep.path} stroke={ep.color} strokeWidth={ep.width} fill="none" strokeOpacity={0.65} />
                    <path d={ep.path} stroke={ep.color} strokeWidth={4} fill="none" strokeOpacity={0} style={{ pointerEvents: 'stroke' }} />
                  </g>
                ))}
                {pendingPath && (
                  <path d={pendingPath} stroke={pendingColor} strokeWidth={1.8} fill="none" strokeDasharray="5 4" strokeOpacity={0.8} />
                )}
              </g>
            </svg>

            {/* HTML node layer */}
            <div style={{
              position: 'absolute', inset: 0, zIndex: 2,
              transformOrigin: '0 0',
              transform: `translate(${camera.x}px,${camera.y}px) scale(${camera.zoom})`,
            }}>
              {nodes.map(node => (
                <div
                  key={node.id}
                  data-od-node-id={node.id}
                  style={{ position: 'absolute', left: node.x, top: node.y }}
                >
                  <RoleNode
                    node={node}
                    isSelected={selectedId === node.id}
                    isRoot={node.parentId === null}
                    subordinateCount={(childrenOf.get(node.id) || []).length}
                    onSelect={onSelectNode}
                    onDelete={onDeleteNode}
                    isDropCandidate={pendingEdge != null && hoveredDropId === node.id}
                    isDropValid={pendingEdge?.validTargetId === node.id}
                    onStartEdgeDrag={(id, e) => {
                      const n = nodesRef.current.find(x => x.id === id)
                      if (!n) return
                      const pos = topHandlePos(n)
                      dragRef.current = { type: 'edge', childId: id }
                      onPendingEdgeStart?.(id, pos.x, pos.y)
                    }}
                    onDropTarget={() => {}}
                    onStartMove={(id, e) => {
                      const n = nodesRef.current.find(x => x.id === id)
                      if (!n) return
                      dragRef.current = { type: 'node', nodeId: id, startX: e.clientX, startY: e.clientY, nx: n.x, ny: n.y }
                      onNodeDragStart?.(id, e)
                    }}
                  />
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  )
}

export { NODE_W, NODE_H }
