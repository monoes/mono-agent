// Puts the monoes.me account in front of the app (spec §6.5). While the machine's
// sign-in does not allow work it renders the gate instead of the app: renderShell
// is not called, so the app and everything it polls is unmounted. Otherwise it
// renders the app, handing it the banners to wear (grace, warn).
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAccountView } from '../../lib/accountGate.js'
import { subscribeEvent } from '../../services/api.js'
import AccountGate from './AccountGate.jsx'
import { GraceBanner, WarnBanner } from './AccountBanners.jsx'

export default function AccountShell({ gate, renderShell }) {
  const { t } = useTranslation()
  const view = useAccountView(gate)
  useEffect(() => gate.start(), [gate])
  // The release the app's own update check found, kept for a gate that comes up later.
  const [update, setUpdate] = useState(null)
  useEffect(() => subscribeEvent('update:available', info => { if (info?.update_available) setUpdate(info) }), [])

  if (view.phase === 'checking') {
    return (
      <div role="status" style={{ width: '100vw', height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--void)', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)' }}>
        {t('account.checking')}
      </div>
    )
  }
  if (view.locked) return <AccountGate gate={gate} view={view} update={update} />
  return renderShell(
    <>
      {view.grace && <GraceBanner status={view.status} onSignedIn={() => gate.check('manual')} />}
      {view.warn && <WarnBanner enforceFrom={view.enforceFrom} onSignedIn={() => gate.check('manual')} />}
    </>,
  )
}
