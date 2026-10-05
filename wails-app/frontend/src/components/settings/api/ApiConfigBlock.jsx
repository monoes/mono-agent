import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight, Loader2, Settings2 } from 'lucide-react'
import { describeConfigError } from './configError.js'
import { ROWS, bannerOf, byKey, otherProblems, summary } from './configModel.js'
import ApiConfigRow from './ApiConfigRow.jsx'
import { Badge, Said, block, errText, hint, label, mono } from './ui.jsx'

// The server's settings (internal/apiconfig): each setting with what is saved, what the running daemon started with,
// where that came from and where it stands. Folded, like the section's other parts: it is for the one who runs the
// server. Everything comes from `api config show --json` (Go side: app_api_config.go); this block shows it and asks
// the CLI to change it, and never judges a value, a widening change or a state itself.
//
// What the section hands it is the document of the last read or of the last change that was made, never of a dry run.

const rowsBox = { listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 10 }

// Banner of what applying the settings takes, from the document alone, as the CLI's own text says it.
function Banner({ config }) {
  const { t } = useTranslation()
  const banner = bannerOf(config)
  if (banner.kind === 'none') return null
  const names = [...new Set(banner.keys.map(k => ROWS.find(r => r.keys.includes(k))).filter(Boolean))].map(r => t(r.label)).join(', ')
  const text = banner.kind === 'restart' ? t('settings.api.config.banner.restartBody', { keys: names || banner.keys.join(', ') })
    : banner.kind === 'older' ? t('settings.api.config.banner.olderBody')
      : t('settings.api.config.banner.idleBody')
  const warn = banner.kind !== 'idle'
  return (
    <div data-testid="api-config-banner" style={{
      display: 'flex', flexDirection: 'column', gap: 6, padding: '10px 12px', borderRadius: 'var(--radius)',
      background: warn ? 'rgba(234,179,8,.05)' : 'rgba(255,255,255,.03)', border: `1px solid ${warn ? 'rgba(234,179,8,.22)' : 'var(--border-dim)'}`,
    }}>
      {banner.kind === 'restart' && <div style={{ fontFamily: mono, fontSize: 11, fontWeight: 600, color: 'var(--yellow)' }}>{t('settings.api.config.banner.restartTitle')}</div>}
      <div style={hint}>{text}</div>
    </div>
  )
}

/**
 * @param {object|null} config `api config show --json` (or the document of the last change), null while it loads.
 * @param {{text: string, verbatim: boolean}|null} err Why the settings could not be read. What was on screen stays.
 * @param {() => void} onRetry
 */
export default function ApiConfigBlock({ config, err, onRetry }) {
  const { t } = useTranslation()
  const bodyId = useId()
  const [open, setOpen] = useState(false)
  const [drafts, setDrafts] = useState({})
  const settings = byKey(config)
  const sum = summary(config)
  const problems = otherProblems(config)

  return (
    <div data-testid="api-config-block" style={block}>
      <button
        type="button" data-testid="api-config-toggle" aria-expanded={open} aria-controls={bodyId} onClick={() => setOpen(v => !v)}
        style={{
          width: '100%', textAlign: 'left', font: 'inherit', color: 'inherit', background: 'transparent', border: 'none', padding: 0,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, cursor: 'pointer', userSelect: 'none',
        }}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <Settings2 size={13} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
            <span style={label}>{t('settings.api.config.title')}</span>
          </span>
          {!config && !err && <Badge data-testid="api-config-chip-loading" tone="info"><Loader2 size={11} className="spin" /> {t('settings.api.loading')}</Badge>}
          {err && <Badge data-testid="api-config-chip-error" tone="bad">{t('settings.api.config.chipError')}</Badge>}
          {config && sum.restart && <Badge data-testid="api-config-chip-restart" tone="warn">{t('settings.api.config.chipRestart')}</Badge>}
          {config && sum.problems > 0 && <Badge data-testid="api-config-chip-problems" tone="bad">{t('settings.api.config.chipProblems', { count: sum.problems })}</Badge>}
          {config && sum.saved > 0 && <Badge data-testid="api-config-chip-saved" tone="muted">{t('settings.api.config.chipSaved', { count: sum.saved })}</Badge>}
        </span>
        {open ? <ChevronDown size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} /> : <ChevronRight size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />}
      </button>

      {open && (
        <div id={bodyId} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={hint}>{t('settings.api.config.hint')}</div>

          {err && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div data-testid="api-config-load-error" style={{ ...errText, flex: 1 }}>
                {t('settings.api.config.loadError')} <Said said={err} />
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>{t('settings.api.retry')}</button>
            </div>
          )}
          {!config && !err && <div style={hint}>{t('settings.api.config.loading')}</div>}

          {config && (
            <>
              <Banner config={config} />
              {problems.length > 0 && (
                <div data-testid="api-config-problems" style={{ ...errText, display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {problems.map((p, i) => <div key={i}><Said said={describeConfigError(`invalid_input: ${p.message}`, t)} /></div>)}
                  <div style={hint}>{t('settings.api.config.problemDocHint')}</div>
                </div>
              )}
              <ul aria-label={t('settings.api.config.title')} style={rowsBox}>
                {ROWS.map(row => (
                  <ApiConfigRow
                    key={row.id} row={row} doc={config} settings={settings} drafts={drafts} busy={false}
                    onChange={(key, text) => setDrafts(d => ({ ...d, [key]: text }))}
                  />
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </div>
  )
}
