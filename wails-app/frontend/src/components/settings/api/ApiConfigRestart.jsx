import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Copy, RotateCw } from 'lucide-react'
import { copyText } from '../../../pages/connections/ui.jsx'
import { ROWS, bannerOf } from './configModel.js'
import ApiConfirmDialog from './ApiConfirmDialog.jsx'
import { Said, errText, hint, mono, okText } from './ui.jsx'

// What applying the saved settings takes (D38: a server reads them when it starts, there is no hot reload), from the
// document alone as the CLI's own text says it: a restart is needed (which settings), a daemon that predates the
// report may not run what is saved, or no daemon runs and the server reads them when it starts. When the daemon is
// registered for auto-start the page can restart it, after a dialog (it interrupts whatever the daemon is running);
// when it is not, nothing can, and what is left is the commands to run, with a copy button.

const STOP_START = 'monoagentcli daemon'
const INSTALL = 'monoagentcli daemon install'

function Command({ command }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState('') // '' | 'ok' | 'failed'
  useEffect(() => {
    if (copied !== 'ok') return undefined
    const id = setTimeout(() => setCopied(''), 2000)
    return () => clearTimeout(id)
  }, [copied])
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
      <code style={{
        fontFamily: mono, fontSize: 12, color: 'var(--text)', background: 'var(--elevated)', border: '1px solid var(--border)',
        borderRadius: 'var(--radius)', padding: '4px 10px', userSelect: 'all', wordBreak: 'break-all',
      }}>{command}</code>
      <button
        type="button" className="btn btn-secondary btn-sm" aria-label={t('settings.api.config.restart.copyLabel', { command })}
        onClick={async () => setCopied((await copyText(command)) ? 'ok' : 'failed')}
      >
        <Copy size={12} /> {t('settings.api.status.copy')}
      </button>
      <span role="status" aria-live="polite">
        {copied === 'ok' && <span style={okText}><Check size={11} /> {t('settings.api.status.copied')}</span>}
        {copied === 'failed' && <span style={errText}>{t('settings.api.status.copyFailed')}</span>}
      </span>
    </div>
  )
}

/**
 * @param {object} config The document of `api config show` (or of a change).
 * @param {ReturnType<typeof import('./useRestart.js').default>} restart
 * @param {boolean} disabled A call for a setting is running.
 */
export default function ApiConfigRestart({ config, restart, disabled }) {
  const { t } = useTranslation()
  const button = useRef(null)
  const was = useRef(restart.phase)
  const banner = bannerOf(config)
  const { phase, err, fallback } = restart

  // The button that opened the dialog, or started a restart that did not happen, gets the keyboard back.
  useEffect(() => {
    if ((phase === 'idle' || phase === 'late') && was.current !== 'idle' && was.current !== 'late') button.current?.focus()
    was.current = phase
  })

  // The daemon is back and runs what is saved: said while the document says so too (a setting saved since needs a restart again).
  const back = phase === 'back' && banner.kind === 'none'
  if (banner.kind === 'none' && !back && phase === 'idle' && !err) return null
  const canRestart = config?.daemon?.autostart && !fallback
  const needs = banner.kind === 'restart' || banner.kind === 'older'
  const names = [...new Set(banner.keys.map(k => ROWS.find(r => r.keys.includes(k))).filter(Boolean))].map(r => t(r.label)).join(', ')
  const text = back ? t('settings.api.config.restart.back')
    : banner.kind === 'restart' ? t('settings.api.config.banner.restartBody', { keys: names || banner.keys.join(', ') })
      : banner.kind === 'older' ? t('settings.api.config.banner.olderBody')
        : banner.kind === 'idle' ? t('settings.api.config.banner.idleBody') : ''
  const warn = !back && banner.kind !== 'idle'
  const working = phase === 'restarting'

  return (
    <div data-testid="api-config-banner" style={{
      display: 'flex', flexDirection: 'column', gap: 8, padding: '10px 12px', borderRadius: 'var(--radius)',
      background: warn ? 'rgba(234,179,8,.05)' : 'rgba(255,255,255,.03)', border: `1px solid ${warn ? 'rgba(234,179,8,.22)' : 'var(--border-dim)'}`,
    }}>
      {!back && banner.kind === 'restart' && <div style={{ fontFamily: mono, fontSize: 11, fontWeight: 600, color: 'var(--yellow)' }}>{t('settings.api.config.banner.restartTitle')}</div>}
      {text && <div style={hint}>{text}</div>}

      {(needs || phase === 'late') && !back && canRestart && !working && phase !== 'checking' && (
        <div>
          <button ref={button} type="button" className="btn btn-secondary btn-sm" disabled={disabled} onClick={restart.ask}>
            <RotateCw size={12} /> {t('settings.api.config.restart.button')}
          </button>
        </div>
      )}
      {working && (
        <div>
          <button type="button" className="btn btn-secondary btn-sm" disabled>
            <RotateCw size={12} className="spin" /> {t('settings.api.config.restart.working')}
          </button>
        </div>
      )}
      {phase === 'checking' && <div role="status" style={hint}>{t('settings.api.config.restart.checking')}</div>}
      {phase === 'late' && <div role="status" style={hint}>{t('settings.api.config.restart.late')}</div>}

      {needs && !canRestart && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div style={hint}>{t('settings.api.config.restart.noAutostart')}</div>
          <Command command={STOP_START} />
          <div style={hint}>{t('settings.api.config.restart.install')}</div>
          <Command command={INSTALL} />
        </div>
      )}
      {err && <div role="alert" style={errText}><Said said={err} /></div>}

      {phase === 'confirming' && (
        <ApiConfirmDialog
          title={t('settings.api.config.restart.title')} cancelLabel={t('settings.api.config.restart.cancel')}
          confirmLabel={t('settings.api.config.restart.confirm')} onCancel={restart.cancel} onConfirm={restart.confirm}
        >
          <div style={{ ...hint, fontSize: 12 }}>{t('settings.api.config.restart.body')}</div>
          <div style={hint}>{t('settings.api.config.restart.after')}</div>
        </ApiConfirmDialog>
      )}
    </div>
  )
}
