import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Maximize, ZoomIn, ZoomOut, PenLine, Globe, Mail, Trophy, X } from 'lucide-react'
import { tidyTree, fitCamera, reportPath, NODE_H } from '../orgdesigner/orgGraph.js'
import {
  initialStage, hasTeam, leasesOf, questProgress, structureKey, LEAD_ID, RUNNING,
} from '../../lib/orgStage.js'
import { usePageVisible } from '../../lib/usePageVisible.js'
import { StageNode, CARD_W, CARD_H, formatCost } from './StageNode.jsx'
import { useStageMotion, useThrottled, prefersReducedMotion, isFresh } from './useStageMotion.js'
import './stage.css'

const ZOOM_MIN = 0.3
const ZOOM_MAX = 1.8
const FIT_MAX = 1.1
const GAP_X = 26
const GAP_Y = 46
const THROTTLE_MS = 120
const SPEECH_ROOM = 40

// fitStage fits the cards into the viewport with the Org Designer's
// fitCamera (its cards are shorter, so each card also counts its bottom
// edge), never zooming a small org past FIT_MAX.
export function fitStage(positions, width, height) {
  // Room above each card for the speech bubble a result leaves there.
  const boxes = Object.values(positions).flatMap(p => [p, { x: p.x, y: p.y + CARD_H - NODE_H }, { x: p.x, y: p.y - SPEECH_ROOM }])
  if (!boxes.length || !width || !height) return { x: 0, y: 0, zoom: 1 }
  const cam = fitCamera(boxes, width, height)
  if (cam.zoom <= FIT_MAX) return cam
  const cx = (width / 2 - cam.x) / cam.zoom
  const cy = (height / 2 - cam.y) / cam.zoom
  return { x: width / 2 - cx * FIT_MAX, y: height / 2 - cy * FIT_MAX, zoom: FIT_MAX }
}

// layoutStage places the nodes as a tidy tree: the lead on top, its
// workers below, native subagents under the agent that called them.
export function layoutStage(stage) {
  const items = stage.order.map(id => ({ id, parentId: stage.nodes[id].parentId }))
  const out = {}
  for (const n of tidyTree(items, { nodeW: CARD_W, nodeH: CARD_H, gapX: GAP_X, gapY: GAP_Y })) out[n.id] = { x: n.x, y: n.y }
  return out
}

function useViewportSize(ref) {
  const [size, setSize] = useState({ width: 0, height: 0 })
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const read = () => setSize(s => (s.width === el.clientWidth && s.height === el.clientHeight ? s : { width: el.clientWidth, height: el.clientHeight }))
    read()
    if (typeof ResizeObserver === 'undefined') return undefined
    const ro = new ResizeObserver(read)
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return size
}

function Flight({ flight, positions }) {
  const from = positions[flight.from]
  const to = positions[flight.to]
  if (!from || !to) return null
  const down = to.y >= from.y
  const fx = from.x + CARD_W / 2
  const fy = from.y + (down ? CARD_H : 0)
  const tx = to.x + CARD_W / 2
  const ty = to.y + (down ? 0 : CARD_H)
  return (
    <span className={`stage-flight ${flight.kind}`} data-testid="stage-flight" data-kind={flight.kind}
      style={{ '--fx': `${fx}px`, '--fy': `${fy}px`, '--tx': `${tx}px`, '--ty': `${ty}px` }} aria-hidden="true">
      {flight.kind !== 'result' && <Mail size={12} />}
    </span>
  )
}

function QuestLog({ stage, onSelect, nameOf }) {
  const { t } = useTranslation()
  const progress = questProgress(stage)
  const leases = leasesOf(stage)
  return (
    <div className="stage-strip" data-testid="stage-quests">
      <span title={t('stage.questProgress', { done: progress.done, total: progress.total })} style={{ display: 'flex', alignItems: 'center', gap: 5, flexShrink: 0 }}>
        {t('stage.quests')}
        <span className="stage-progress" role="progressbar" aria-valuemin={0} aria-valuemax={progress.total} aria-valuenow={progress.done} aria-label={t('stage.questProgress', { done: progress.done, total: progress.total })}>
          <div style={{ width: `${Math.round(progress.ratio * 100)}%` }} />
        </span>
        {progress.done}/{progress.total}
      </span>
      <div className="stage-quests">
        {stage.quests.map(q => (
          <button key={q.id} type="button" className="stage-quest" data-status={q.status} data-testid="stage-quest" onClick={() => onSelect(q.agentId)}
            title={`${nameOf(q.agentId)}: ${q.text}`}>
            <span className="stage-ring" data-status={q.status === 'active' ? 'working' : q.status} />
            <b style={{ fontWeight: 600, flexShrink: 0 }}>{nameOf(q.agentId)}</b>
            <span className="q-text">{q.text}</span>
          </button>
        ))}
      </div>
      <span className="stage-lease" data-testid="stage-lease-pen" title={t('stage.penHelp')}>
        <PenLine size={10} aria-hidden="true" /> {leases.pen ? nameOf(leases.pen) : t('stage.free')}
      </span>
      <span className="stage-lease" data-testid="stage-lease-browser" title={t('stage.browserHelp')}>
        <Globe size={10} aria-hidden="true" /> {leases.browser ? nameOf(leases.browser) : t('stage.free')}
      </span>
      {leases.waiting.length > 0 && <span className="stage-lease" style={{ color: '#fbbf24' }}>{t('stage.waitingCount', { count: leases.waiting.length })}</span>}
    </div>
  )
}

function Scoreboard({ score, onClose }) {
  const { t } = useTranslation()
  const secs = Math.round(score.durationMs / 1000)
  const time = secs >= 60 ? `${Math.floor(secs / 60)}m ${secs % 60}s` : `${secs}s`
  return (
    <div className="stage-score" data-testid="stage-scoreboard" role="status">
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: 'var(--text)', fontWeight: 600 }}>
        <Trophy size={12} color="#fbbf24" aria-hidden="true" /> {t('stage.scoreTitle')}
        <span style={{ flex: 1 }} />
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t('stage.closeScore')} style={{ padding: '1px 4px' }}><X size={11} /></button>
      </div>
      <dl>
        <dt>{t('stage.scoreAgents')}</dt><dd>{score.agents}{score.failed ? ` (${t('stage.failedCount', { count: score.failed })})` : ''}</dd>
        <dt>{t('stage.scoreTime')}</dt><dd>{time}</dd>
        <dt>{t('stage.scoreCost')}</dt><dd>{score.costUsd == null ? '—' : formatCost(score.costUsd, score.costEstimated)}</dd>
        <dt>{t('stage.scoreFiles')}</dt><dd>{score.filesChanged.length}</dd>
        <dt>{t('stage.scoreTests')}</dt><dd>{score.testsRun ? t('stage.testsPassed', { passed: score.testsPassed, run: score.testsRun }) : '—'}</dd>
      </dl>
    </div>
  )
}

// OrgStage is the live org at the top of an expanded coder bubble (#228):
// the lead and the agents it brought in as a tidy tree, briefs and results
// flying along the edges, the quest log and leases above, and the end-of-
// turn scoreboard. It renders a stage model (lib/orgStage.js) and nothing
// else, so a running org (#229) can hand it one too.
//
// leadInfo is the lead's {runtime, model, effort} (the conversation's);
// leadIdle is the lead's line when it has no turn yet. selectedId and
// onSelect are the clicked node (the overlay opens its drawer).
export function OrgStage({ stage: liveStage, leadInfo, leadIdle = '', working = false, selectedId = null, onSelect, turnId = '' }) {
  const { t } = useTranslation()
  const base = useMemo(() => liveStage || initialStage(), [liveStage])
  const stage = useThrottled(base, THROTTLE_MS)
  const visible = usePageVisible()
  const [reducedMotion] = useState(prefersReducedMotion)
  const { active, bubbles } = useStageMotion(stage.flights, { reducedMotion })
  const viewportRef = useRef(null)
  const size = useViewportSize(viewportRef)
  const skey = structureKey(stage)
  // Laid out again only when the tree changes, not on every status.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const positions = useMemo(() => layoutStage(stage), [skey])
  const [camera, setCamera] = useState({ x: 0, y: 0, zoom: 1 })
  const cameraRef = useRef(camera)
  cameraRef.current = camera
  const userMoved = useRef(false)
  const [dragging, setDragging] = useState(false)
  const [scoreClosedFor, setScoreClosedFor] = useState(null)

  useEffect(() => {
    if (!userMoved.current && size.width) setCamera(fitStage(positions, size.width, size.height))
  }, [positions, size.width, size.height])

  const fit = useCallback(() => {
    userMoved.current = false
    setCamera(fitStage(positions, size.width, size.height))
  }, [positions, size.width, size.height])

  const zoomBy = useCallback((factor, mx = size.width / 2, my = size.height / 2) => {
    userMoved.current = true
    setCamera(cam => {
      const z = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, cam.zoom * factor))
      return { x: mx - (mx - cam.x) * (z / cam.zoom), y: my - (my - cam.y) * (z / cam.zoom), zoom: z }
    })
  }, [size.width, size.height])

  // Wheel zoom, anchored at the cursor (as on the Org Designer canvas).
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return undefined
    const onWheel = (e) => {
      e.preventDefault()
      const rect = el.getBoundingClientRect()
      zoomBy(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX - rect.left, e.clientY - rect.top)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [zoomBy])

  const onPointerDown = (e) => {
    if (e.button !== 0 || e.target.closest('button')) return
    const start = { x: e.clientX, y: e.clientY }
    const cam0 = cameraRef.current
    setDragging(true)
    const onMove = (ev) => {
      userMoved.current = true
      setCamera({ ...cam0, x: cam0.x + ev.clientX - start.x, y: cam0.y + ev.clientY - start.y })
    }
    const onUp = () => {
      setDragging(false)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  const onKeyDown = (e) => {
    if (e.target !== e.currentTarget) return
    if (e.key === '+' || e.key === '=') { e.preventDefault(); zoomBy(1.15) }
    if (e.key === '-') { e.preventDefault(); zoomBy(1 / 1.15) }
    if (e.key === '0' || e.key === 'f') { e.preventDefault(); fit() }
  }

  const nameOf = useCallback((id) => {
    const n = stage.nodes[id]
    if (!n || id === LEAD_ID) return t('bubbles.lead')
    return n.role || (n.native ? t('stage.subagent') : id)
  }, [stage, t])

  // The lead shows the conversation's model and, with no turn running, its
  // idle line.
  const leadNode = useMemo(() => {
    const n = stage.nodes[LEAD_ID]
    if (!liveStage) return { ...n, status: working ? 'working' : 'idle', summary: leadIdle }
    return RUNNING.has(n.status) ? n : { ...n, summary: leadIdle }
  }, [stage, liveStage, working, leadIdle])

  const now = Date.now()
  const team = hasTeam(stage)
  const latest = stage.feed[stage.feed.length - 1]
  const showScore = team && stage.scoreboard && scoreClosedFor !== turnId

  return (
    <div className={`org-stage bubble-stage${visible ? '' : ' paused'}`} data-testid="org-stage" data-reduced-motion={reducedMotion ? 'true' : 'false'}>
      {team && <QuestLog stage={stage} onSelect={onSelect} nameOf={nameOf} />}
      <div ref={viewportRef} className={`stage-viewport${dragging ? ' dragging' : ''}`} tabIndex={0} role="group"
        aria-label={t('stage.graphLabel')} onPointerDown={onPointerDown} onKeyDown={onKeyDown}>
        <div className="stage-world" style={{ transform: `translate(${camera.x}px, ${camera.y}px) scale(${camera.zoom})` }}>
          <svg className="stage-edges" width={1} height={1} aria-hidden="true">
            {stage.edges.map(edge => {
              const a = positions[edge.from]
              const b = positions[edge.to]
              if (!a || !b) return null
              const child = stage.nodes[edge.to]
              const fresh = !reducedMotion && isFresh(child?.spawnAt, now)
              return <path key={edge.id} className={`stage-edge${child?.native ? ' native' : ''}${fresh ? ' fresh' : ''}`}
                d={reportPath(b.x + CARD_W / 2, b.y, a.x + CARD_W / 2, a.y + CARD_H)} />
            })}
          </svg>
          {stage.order.map(id => {
            const p = positions[id]
            if (!p) return null
            const node = id === LEAD_ID ? leadNode : stage.nodes[id]
            return <StageNode key={id} node={node} x={p.x} y={p.y} selected={selectedId === id} leadInfo={leadInfo} now={now} onSelect={onSelect} />
          })}
          {active.map(f => <Flight key={f.id} flight={f} positions={positions} />)}
          {bubbles.map(b => {
            const p = positions[b.nodeId]
            if (!p) return null
            return <div key={b.id} className="stage-speech" data-testid="stage-speech" style={{ left: p.x - 10, top: p.y - 8 }}>{b.text}</div>
          })}
        </div>
        {!team && (
          <div style={{ position: 'absolute', bottom: 8, left: 0, right: 0, textAlign: 'center', fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--text-muted)', opacity: 0.7, pointerEvents: 'none' }}>
            {t('bubbles.teamSoon')}
          </div>
        )}
        {team && latest && (
          <div className="stage-ticker" data-testid="stage-ticker" aria-live="polite">
            {t(`stage.feed.${latest.type}`, {
              defaultValue: '{{name}}: {{text}}',
              name: nameOf(latest.agentId),
              text: latest.type === 'status' ? t(`stage.status.${latest.text}`, { defaultValue: latest.text }) : latest.text,
              detail: latest.type === 'finished' ? t(`stage.status.${latest.detail}`, { defaultValue: latest.detail }) : latest.detail,
            })}
          </div>
        )}
        {showScore && <Scoreboard score={stage.scoreboard} onClose={() => setScoreClosedFor(turnId)} />}
        <div className="stage-tools">
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => zoomBy(1.15)} aria-label={t('stage.zoomIn')} title={t('stage.zoomIn')} style={{ padding: '2px 5px' }}><ZoomIn size={12} /></button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => zoomBy(1 / 1.15)} aria-label={t('stage.zoomOut')} title={t('stage.zoomOut')} style={{ padding: '2px 5px' }}><ZoomOut size={12} /></button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={fit} aria-label={t('stage.fit')} title={t('stage.fit')} style={{ padding: '2px 5px' }}><Maximize size={12} /></button>
        </div>
      </div>
    </div>
  )
}
