import { useTranslation } from 'react-i18next'
import { wideningHeading } from './configModel.js'
import ApiConfirmDialog from './ApiConfirmDialog.jsx'
import { Said, hint, mono } from './ui.jsx'

// What a change makes the server reach, listed before it is made: for each way, a plain heading of this page's own
// (a listener beyond this computer, a stronger class of runtime, more runtimes), and under it the CLI's sentence
// exactly as it worded it (internal/apiconfig/widen.go). The CLI decided that the change widens; this only shows it.
// A kind of reach this page has no heading for is listed with its sentence alone.

/**
 * @param {Array<{key: string, reason: string}>} widening The `widening` of a dry run.
 * @param {'set'|'unset'} kind Whether a value is being saved or removed.
 * @param {() => void} onCancel
 * @param {() => void} onConfirm
 */
export default function ApiWideningDialog({ widening, kind, onCancel, onConfirm }) {
  const { t } = useTranslation()
  return (
    <ApiConfirmDialog
      title={t('settings.api.config.widening.title')}
      cancelLabel={t('settings.api.config.widening.cancel')}
      confirmLabel={kind === 'unset' ? t('settings.api.config.widening.confirmUnset') : t('settings.api.config.widening.confirmSave')}
      onCancel={onCancel} onConfirm={onConfirm}
    >
      <div style={hint}>{kind === 'unset' ? t('settings.api.config.widening.introUnset') : t('settings.api.config.widening.introSave')}</div>
      <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 8, maxHeight: '40vh', overflowY: 'auto' }}>
        {widening.map((w, i) => {
          const heading = wideningHeading(w.key)
          return (
            <li key={`${w.key}-${i}`} style={{ fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }}>
              {heading && <div style={{ fontFamily: mono, fontWeight: 600, color: 'var(--yellow)' }}>{t(heading)}</div>}
              <Said said={{ text: w.reason, verbatim: true }} />
            </li>
          )
        })}
      </ul>
      <div style={hint}>{t('settings.api.config.widening.note')}</div>
    </ApiConfirmDialog>
  )
}
