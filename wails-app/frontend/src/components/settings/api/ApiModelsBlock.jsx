import { useTranslation } from 'react-i18next'
import { Zap } from 'lucide-react'
import { missingIsAboutJev, pickListener, policyClass, servingListeners } from './apiModel.js'
import { Badge, CapBadges, ClassBadge, block, label, hint, errText, mono } from './ui.jsx'

// The models the API would serve under the policy of the listener the header
// describes (the first one that answers /v1: the status says which, and how many
// others there are), one row each, with the `auto` entry first (the API lists it
// last, so that a client taking the first model is not moved to it; here a long
// list would hide it). Everything comes from `api models --json`; a field an older
// CLI does not send is absent here too (a dash, or a sentence without the number).

// Headers wrap: a long translation ("Clave con contexto") must not widen the table.
const th = { fontSize: 9.5, padding: '6px 10px', whiteSpace: 'normal', verticalAlign: 'bottom', position: 'sticky', top: 0, background: 'var(--surface)', zIndex: 1 }
const td = { fontSize: 11.5, padding: '7px 10px', verticalAlign: 'top' }
const dash = <span style={{ ...hint, color: 'var(--text-dim)' }}>–</span>

/**
 * @param {object|null} models `api models --json`, or null while it loads.
 * @param {string} err Why it could not be read.
 * @param {object|null} status `api status --json`, or null while it loads or when it could not be read: the policy
 *   `models` was evaluated for is that of the listener it picks.
 * @param {string} statusErr Why the status could not be read: then no listener is known, and `models` was asked for
 *   the CLI's own defaults.
 * @param {(() => void)|undefined} onOpenJev Opens the Jev settings, where auto is switched on.
 * @param {() => void} onRetry
 */
export default function ApiModelsBlock({ models, err, status, statusErr, onOpenJev, onRetry }) {
  const { t } = useTranslation()
  const list = models?.models || []
  const listener = pickListener(status)
  const serving = servingListeners(status).length
  let caption = '' // nothing is known while the status loads
  if (statusErr) caption = t('settings.api.models.policyUnknown')
  else if (status && listener?.v1) {
    caption = t(listener.confinement_source === 'daemon' ? 'settings.api.models.policyDaemon' : 'settings.api.models.policyAssumed', { addr: listener.addr })
    if (serving > 1) caption += ' ' + t('settings.api.models.policyMany', { count: serving, addr: listener.addr })
  } else if (status) caption = t('settings.api.models.policyDefault')

  const yes = (v) => (
    <span style={{ fontFamily: mono, fontSize: 11, color: v ? 'var(--green-neon)' : 'var(--text-muted)' }}>
      {t(v ? 'settings.api.models.yes' : 'settings.api.models.no')}
    </span>
  )

  return (
    <div style={block}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
        <span style={label}>{t('settings.api.models.title')}</span>
        <span style={{ ...hint, flex: 1, minWidth: 220 }}>{caption}</span>
      </div>

      {err ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <div style={{ ...errText, flex: 1 }}>{t('settings.api.models.loadError', { error: err })}</div>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>{t('settings.api.retry')}</button>
        </div>
      ) : !models ? (
        <div style={hint}>{t('settings.api.models.loading')}</div>
      ) : list.length === 0 ? (
        <div style={hint}>{t('settings.api.models.empty')}</div>
      ) : (
        <div style={{ overflow: 'auto', maxHeight: 360 }}>
          <table className="data-table" aria-label={t('settings.api.models.title')}>
            <thead>
              <tr>
                <th style={th}>{t('settings.api.models.colModel')}</th>
                <th style={th}>{t('settings.api.models.colConfinement')}</th>
                <th style={th}>{t('settings.api.models.colCapabilities')}</th>
                <th style={th}>{t('settings.api.models.colValidated')}</th>
                <th style={th}>{t('settings.api.models.colServed')}</th>
                <th style={th}>{t('settings.api.models.colContext')}</th>
                <th style={th}>{t('settings.api.models.colAuto')}</th>
              </tr>
            </thead>
            <tbody>
              {models.auto && <AutoRow auto={models.auto} onOpenJev={onOpenJev} />}
              {list.map(m => (
                <tr key={m.id} style={m.allowed ? undefined : { opacity: 0.6 }}>
                  <td style={{ ...td, maxWidth: 220 }}>
                    <div style={{ fontFamily: mono, color: 'var(--text)', wordBreak: 'break-all' }}>{m.id}</div>
                    {m.label && (
                      <div title={m.label} style={{ ...hint, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.label}</div>
                    )}
                  </td>
                  <td style={td}><ClassBadge value={m.confinement} /></td>
                  <td style={{ ...td, minWidth: 152 }}><CapBadges caps={m.capabilities} model={m.id} /></td>
                  <td style={td}>
                    {m.validated ? <Badge tone="ok" title={t('settings.api.models.validatedHint')}>{t('settings.api.models.validated')}</Badge> : dash}
                  </td>
                  <td style={td}>
                    {m.allowed ? yes(true) : <span style={{ fontFamily: mono, fontSize: 11, color: 'var(--yellow)' }}>{t('settings.api.models.noPolicy')}</span>}
                  </td>
                  <td style={td}>{yes(!!m.context_allowed)}</td>
                  <td style={td}>{typeof m.auto_allowed === 'boolean' ? yes(m.auto_allowed) : dash}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// AutoRow is the auto model: on, and among how many models up to which class
// (and how many the listener serves that it may not pick), or off and what it
// needs. What `missing` names is for the server to fix unless it is about Jev:
// only then is there a jump to the Jev settings.
function AutoRow({ auto, onOpenJev }) {
  const { t } = useTranslation()
  const counted = typeof auto.candidates === 'number' && !!auto.confinement
  // The CLI says unconfined for the class and any for the policy (the flag, the status): one spelling on the page.
  // What is held back is above the cap, which is then chat-only or sandboxed in both.
  const cls = policyClass(auto.confinement)
  return (
    <tr style={{ background: 'rgba(0,180,216,.04)' }}>
      <td style={td}>
        <div style={{ fontFamily: mono, color: 'var(--text)' }}>auto</div>
        <div style={hint}>{t('settings.api.models.autoLabel')}</div>
      </td>
      <td colSpan={6} style={td}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {auto.available ? (
            <>
              <div style={{ fontFamily: 'var(--font-body)', fontSize: 11.5, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
                {counted
                  ? t('settings.api.models.autoOn', { count: auto.candidates, class: cls })
                  : t('settings.api.models.autoOnPlain')}
              </div>
              {auto.held_back > 0 && !!auto.confinement && (
                <div style={hint}>{t('settings.api.models.autoHeldBack', { count: auto.held_back, class: auto.confinement })}</div>
              )}
              {auto.key_source === 'env' && <div style={hint}>{t('settings.api.models.autoKeyEnv')}</div>}
            </>
          ) : (
            <>
              <div style={{ fontFamily: 'var(--font-body)', fontSize: 11.5, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
                {t('settings.api.models.autoOff', { missing: auto.missing })}
              </div>
              {onOpenJev && missingIsAboutJev(auto.missing) && (
                <div>
                  <button type="button" className="btn btn-secondary btn-sm" title={t('settings.api.models.openJevHint')} onClick={onOpenJev}>
                    <Zap size={12} /> {t('settings.api.models.openJev')}
                  </button>
                </div>
              )}
            </>
          )}
        </div>
      </td>
    </tr>
  )
}
