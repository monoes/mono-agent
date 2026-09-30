import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, X, HelpCircle, ShieldQuestion, Flag, ChevronRight, Loader } from 'lucide-react'
import { ChatMarkdown } from '../chat/ChatMarkdown.jsx'
import { ChatComposer } from '../chat/ChatComposer.jsx'
import '../chat/chat.css'

const mono = 'var(--font-mono)'
const AMBER = '#fbbf24'

function Muted({ children, style }) {
  return <div style={{ fontFamily: mono, fontSize: 9.5, color: 'var(--text-muted)', ...style }}>{children}</div>
}

// ResolvedLine says how an item ended, and who ended it when that isn't
// the person.
function ResolvedLine({ item, outcome }) {
  const { t } = useTranslation()
  const state = outcome?.state || item.resolution
  if (!state) return null
  const by = item.resolved_by && item.resolved_by !== 'human' ? t('orgBubble.resolvedBy', { by: item.resolved_by }) : ''
  return (
    <Muted style={{ marginTop: 4 }}>
      {outcome?.already ? t('orgBubble.already', { state: t(`orgBubble.state.${state}`, { defaultValue: state }) }) : t(`orgBubble.state.${state}`, { defaultValue: state })}
      {by && ` · ${by}`}
    </Muted>
  )
}

function ActionError({ outcome }) {
  if (!outcome?.error) return null
  return <div role="alert" style={{ marginTop: 4, fontFamily: mono, fontSize: 9.5, color: 'var(--red, #ef4444)' }}>{outcome.error}</div>
}

// Card is a question, approval or gate: amber while it waits.
function Card({ item, icon: Icon, title, children }) {
  return (
    <div className="org-chat-card" data-testid={`org-chat-${item.kind}`} data-pending={item.pending ? 'true' : 'false'}
      style={{
        margin: '4px 0', padding: '8px 10px', borderRadius: 'var(--radius)',
        border: `1px solid ${item.pending ? 'rgba(251,191,36,0.45)' : 'var(--border)'}`,
        background: item.pending ? 'rgba(251,191,36,0.06)' : 'transparent',
      }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontFamily: mono, fontSize: 10, color: item.pending ? AMBER : 'var(--text-muted)' }}>
        <Icon size={11} aria-hidden="true" /> {title}
      </div>
      {children}
    </div>
  )
}

function QuestionCard({ item, nameOf, canAct, busy, outcome, onAnswer }) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const pending = item.pending && !outcome?.state
  const submit = () => { if (text.trim()) onAnswer(item.ref, text.trim()) }
  return (
    <Card item={{ ...item, pending }} icon={HelpCircle} title={t('orgBubble.asks', { name: nameOf(item.role) })}>
      <div style={{ marginTop: 4 }}><ChatMarkdown content={item.text || ''} /></div>
      {item.answer && <Muted style={{ marginTop: 4 }}>{t('orgBubble.yourAnswer', { answer: item.answer })}</Muted>}
      {pending ? (
        <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
          <input type="text" value={text} onChange={e => setText(e.target.value)} disabled={!canAct || busy}
            onKeyDown={e => { if (e.key === 'Enter' && !e.nativeEvent?.isComposing) submit() }}
            aria-label={t('orgBubble.answerLabel')} placeholder={t('orgBubble.answerPlaceholder')}
            style={{ flex: 1, minWidth: 0, fontFamily: mono, fontSize: 10.5, background: 'var(--elevated)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', color: 'var(--text)', padding: '5px 8px', outline: 'none' }} />
          <button type="button" className="btn btn-primary btn-sm" data-testid="org-chat-answer" disabled={!canAct || busy || !text.trim()} onClick={submit}>
            {busy ? <Loader size={11} className="spin" /> : t('orgBubble.answer')}
          </button>
        </div>
      ) : <ResolvedLine item={item} outcome={outcome} />}
      <ActionError outcome={outcome} />
    </Card>
  )
}

function DecisionCard({ item, nameOf, canAct, busy, outcome, onResolve }) {
  const { t } = useTranslation()
  const [note, setNote] = useState('')
  const pending = item.pending && !outcome?.state
  const isGate = item.kind === 'gate'
  const title = isGate
    ? t('orgBubble.gateTitle', { name: nameOf(item.role), gate: item.name || item.ref })
    : t('orgBubble.approvalTitle', { name: nameOf(item.role), action: item.action })
  return (
    <Card item={{ ...item, pending }} icon={isGate ? Flag : ShieldQuestion} title={title}>
      {item.text && <div style={{ marginTop: 4 }}><ChatMarkdown content={item.text} /></div>}
      {pending ? (
        <div style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap' }}>
          {isGate && (
            <input type="text" value={note} onChange={e => setNote(e.target.value)} disabled={!canAct || busy}
              aria-label={t('orgBubble.noteLabel')} placeholder={t('orgBubble.notePlaceholder')}
              style={{ flex: '1 1 160px', minWidth: 0, fontFamily: mono, fontSize: 10.5, background: 'var(--elevated)', border: '1px solid var(--border-bright)', borderRadius: 'var(--radius)', color: 'var(--text)', padding: '5px 8px', outline: 'none' }} />
          )}
          <button type="button" className="btn btn-primary btn-sm" data-testid="org-chat-approve" disabled={!canAct || busy}
            onClick={() => onResolve(item.ref, true, note.trim())} style={{ gap: 4 }}>
            {busy ? <Loader size={11} className="spin" /> : <Check size={11} />} {t('orgBubble.approve')}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" data-testid="org-chat-deny" disabled={!canAct || busy}
            onClick={() => onResolve(item.ref, false, note.trim())} style={{ gap: 4 }}>
            <X size={11} /> {isGate ? t('orgBubble.reject') : t('orgBubble.deny')}
          </button>
        </div>
      ) : <ResolvedLine item={item} outcome={outcome} />}
      <ActionError outcome={outcome} />
    </Card>
  )
}

// TeamRow is a role-to-role message: one compact line that opens to the
// text, so the boss's conversation stays readable.
function TeamRow({ item, nameOf }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const from = item.external ? item.from : nameOf(item.from)
  const to = item.external ? item.to : nameOf(item.to)
  return (
    <div data-testid="org-chat-team" style={{ margin: '2px 0' }}>
      <button type="button" onClick={() => setOpen(o => !o)} aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 5, width: '100%', textAlign: 'left', background: 'transparent', border: 'none', padding: '2px 0', cursor: 'pointer', fontFamily: mono, fontSize: 9.5, color: 'var(--text-muted)' }}>
        <ChevronRight size={10} style={{ transform: open ? 'rotate(90deg)' : 'none', transition: 'transform 120ms' }} aria-hidden="true" />
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {t('orgBubble.teamRow', { from, to })}{item.subject ? ` · ${item.subject}` : ''}
        </span>
      </button>
      {open && <div style={{ padding: '2px 0 4px 15px', fontSize: 11 }}><ChatMarkdown content={item.text || ''} /></div>}
    </div>
  )
}

function Speech({ who, mine, children, note }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: mine ? 'flex-end' : 'flex-start', margin: '6px 0' }}>
      <Muted style={{ marginBottom: 2 }}>{who}</Muted>
      <div style={{
        maxWidth: '85%', padding: '7px 10px', borderRadius: 10, fontSize: 12, lineHeight: 1.5,
        background: mine ? 'rgba(0,180,216,0.12)' : 'var(--elevated)', border: '1px solid var(--border)', color: 'var(--text)',
      }}>
        {children}
      </div>
      {note && <Muted style={{ marginTop: 2 }}>{note}</Muted>}
    </div>
  )
}

// OrgChatView is the chat half of an expanded org bubble (#229): the boss
// thread from `org chat history` with its questions, approvals and gates
// answered inline, role-to-role messages as rows, and a composer that
// messages the boss. conv is useOrgBubble(); nameOf names a role.
export function OrgChatView({ conv, nameOf, draft, onDraftChange }) {
  const { t } = useTranslation()
  const scrollRef = useRef(null)
  const { history, historyError, items, echoes, running, sending, sendError, busy, outcome } = conv
  const canAct = running

  // Stick to the bottom as the thread grows, unless the person scrolled up.
  const atBottom = useRef(true)
  useEffect(() => {
    const el = scrollRef.current
    if (el && atBottom.current) el.scrollTop = el.scrollHeight
  }, [items, echoes])

  const onSend = async () => {
    if (await conv.send(draft)) onDraftChange('')
  }
  const answer = (ref, text) => conv.resolveItem(ref, { answer: text })
  const decide = (ref, approve, note) => conv.resolveItem(ref, { approve, note })

  const bossName = history?.boss_title || history?.boss || t('orgBubble.boss')
  let composerNote = ''
  if (history && !running) composerNote = t('orgBubble.queuedNote')

  return (
    <div style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      <div ref={scrollRef} data-testid="org-chat-thread" role="log" aria-live="polite"
        onScroll={e => { const el = e.currentTarget; atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40 }}
        style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: '8px 14px' }}>
        {!history && !historyError && <Muted style={{ padding: 8 }}>{t('bubbles.loadingChat')}</Muted>}
        {historyError && !history && <div role="alert" style={{ padding: 8, fontFamily: mono, fontSize: 10, color: 'var(--red, #ef4444)' }}>{t('orgBubble.couldNotLoad', { error: historyError })}</div>}
        {history && items.length === 0 && echoes.length === 0 && <Muted style={{ padding: 8 }}>{t('orgBubble.empty', { boss: bossName })}</Muted>}
        {history && !running && items.some(it => it.pending) && (
          <div data-testid="org-chat-stopped" style={{ margin: '4px 0 8px', padding: '6px 10px', borderRadius: 'var(--radius)', border: '1px solid rgba(251,191,36,0.3)', fontFamily: mono, fontSize: 10, color: AMBER }}>
            {t('orgBubble.notRunning')}
          </div>
        )}
        {items.map(item => {
          const key = `${item.kind}-${item.id || item.ref}`
          switch (item.kind) {
            case 'human':
              return <Speech key={key} who={t('orgBubble.you')} mine>{item.text}</Speech>
            case 'boss':
              return <Speech key={key} who={nameOf(item.role)}><ChatMarkdown content={item.text || ''} /></Speech>
            case 'question':
              return <QuestionCard key={key} item={item} nameOf={nameOf} canAct={canAct} busy={!!busy[item.ref]} outcome={outcome[item.ref]} onAnswer={answer} />
            case 'approval':
            case 'gate':
              return <DecisionCard key={key} item={item} nameOf={nameOf} canAct={canAct} busy={!!busy[item.ref]} outcome={outcome[item.ref]} onResolve={decide} />
            case 'team':
              return <TeamRow key={key} item={item} nameOf={nameOf} />
            case 'status':
              return <Muted key={key} style={{ textAlign: 'center', margin: '6px 0' }}>{/^org started/.test(item.text) ? t('orgBubble.started') : t('orgBubble.stopped')}</Muted>
            default:
              return null
          }
        })}
        {echoes.map(e => (
          <Speech key={e.id} who={t('orgBubble.you')} mine note={e.delivery === 'queued' ? t('orgBubble.queued') : t('orgBubble.sent')}>{e.text}</Speech>
        ))}
      </div>
      {sendError && <div role="alert" style={{ padding: '0 14px 4px', fontFamily: mono, fontSize: 10, color: 'var(--red, #ef4444)' }}>{t('orgBubble.couldNotSend', { error: sendError })}</div>}
      {composerNote && <Muted style={{ padding: '0 14px 2px' }}>{composerNote}</Muted>}
      <ChatComposer value={draft} onChange={onDraftChange} onSend={onSend} streaming={false}
        disabled={sending || !history} disabledReason="" />
    </div>
  )
}
