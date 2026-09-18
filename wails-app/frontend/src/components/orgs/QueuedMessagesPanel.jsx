// Queued messages (C-35): monomind drains an org's offline queue
// (inbox.jsonl) only when the org starts, so a reply or `org send` to a
// stopped org waits there. This lists the queue read-only through
// `monoagentcli org queued` and offers "Start org now", which is the org
// view's own run path (RunOrg) passed in as onStartOrg.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Inbox, Play, RefreshCw, ArrowRight, Clock } from 'lucide-react'
import { api } from '../../services/api.js'
import { formatDuration, toMillis } from './waiting.js'
import { Card, Chip, errorOf, mono, mutedText, sectionLabel, smallBtn } from './ui.jsx'

function QueuedCard({ msg, now, t }) {
  const since = toMillis(msg.ts ?? msg.queued_at)
  return (
    <Card>
      <div data-testid="queued-message" style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)' }}>{msg.from || '?'}</span>
          <ArrowRight size={10} style={{ color: 'var(--text-muted)' }} aria-label={t('orgs.queued.to')} />
          <span style={{ ...mono, fontSize: 11, color: 'var(--text)' }}>{msg.to || '?'}</span>
          {msg.endpoint && <Chip color="var(--cyan)">{t('orgs.queued.endpoint')}</Chip>}
          {msg.draining && <Chip color="#eab308">{t('orgs.queued.draining')}</Chip>}
          {msg.trace && <Chip>{t('orgs.queued.hop', { hop: msg.trace.hop })}</Chip>}
          {since != null && (
            <span style={{ ...mutedText, marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 3 }}>
              <Clock size={10} />{t('orgs.queued.waited', { duration: formatDuration(now - since) })}
            </span>
          )}
        </div>
        <div style={{ ...mono, fontSize: 11.5, color: 'var(--text)', fontWeight: 600 }}>
          {msg.subject || <span style={mutedText}>{t('orgs.queued.noSubject')}</span>}
        </div>
        {msg.body && (
          <div style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxHeight: 160, overflow: 'auto' }}>
            {msg.body}
          </div>
        )}
      </div>
    </Card>
  )
}

export default function QueuedMessagesPanel({ orgName, running = false, onStartOrg }) {
  const { t } = useTranslation()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [orgRunning, setOrgRunning] = useState(false)
  const [starting, setStarting] = useState(false)
  const [now, setNow] = useState(Date.now())

  const load = useCallback(async () => {
    if (!orgName) return
    setLoading(true)
    const [res, status] = await Promise.all([
      api.listOrgQueuedMessages(orgName),
      api.getOrgStatus(orgName).catch(() => null),
    ])
    if (!res || res.error) {
      setError(errorOf(res, t('orgs.queued.loadFailed')))
      setData(null)
    } else {
      setError('')
      setData(res)
    }
    setOrgRunning(status?.status === 'running')
    setNow(Date.now())
    setLoading(false)
  }, [orgName, t])

  useEffect(() => {
    setStarting(false)
    load()
  }, [load, running])

  const isRunning = running || orgRunning
  const messages = Array.isArray(data?.messages) ? data.messages : []

  const start = () => {
    if (isRunning || starting || !onStartOrg) return
    setStarting(true)
    onStartOrg()
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Inbox size={12} style={{ color: 'var(--text-muted)' }} />
        <span style={sectionLabel}>{t('orgs.queued.title')}</span>
        {messages.length > 0 && <Chip>{messages.length}</Chip>}
        <button style={{ ...smallBtn, marginLeft: 'auto' }} onClick={load} disabled={loading} title={t('orgs.queued.refresh')} aria-label={t('orgs.queued.refresh')}>
          <RefreshCw size={10} />
        </button>
        {messages.length > 0 && (
          <button className="btn btn-primary btn-sm" onClick={start} disabled={isRunning || starting}>
            <Play size={11} /> {starting ? t('orgs.queued.starting') : t('orgs.queued.startNow')}
          </button>
        )}
      </div>

      {error && <div style={{ ...mono, fontSize: 11, color: 'var(--red, #ef4444)' }}>{error}</div>}

      {!error && data && (
        messages.length === 0 ? (
          <div style={mutedText}>{t('orgs.queued.empty')}</div>
        ) : (
          <>
            <div style={mutedText}>{isRunning ? t('orgs.queued.runningNote') : t('orgs.queued.deliveredAtStart')}</div>
            {data.skipped > 0 && (
              <div style={{ ...mutedText, color: '#eab308' }}>{t('orgs.queued.skipped', { count: data.skipped })}</div>
            )}
            {messages.map((m, i) => <QueuedCard key={m.messageId || `${m.ts}-${i}`} msg={m} now={now} t={t} />)}
          </>
        )
      )}
    </div>
  )
}
