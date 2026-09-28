// The one "Log in to monoes" control, shared by every library entry point.
// Logged out: a button that runs `library login` (the CLI opens the browser
// and waits), with the sign-in URL as a fallback link and an email-code
// alternative. Logged in: the account and a Log out button. The account
// state comes from `library status`; pass status/onStatusChange to share it
// with a surrounding dialog, or leave them out and the button loads its own.
// large: the library's login gate — a big button, the email code as a link.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { LogIn, LogOut, Loader, Mail } from 'lucide-react'
import { library, onLibraryLogin } from '../../services/library.js'
import { api } from '../../services/api.js'

const mono = { fontFamily: 'var(--font-mono)', fontSize: 10.5 }
const errStyle = { ...mono, color: 'var(--red)', maxWidth: 320 }

export default function LogInToMonoesButton({ status: statusProp, onStatusChange, compact = false, large = false }) {
  const { t } = useTranslation()
  const [ownStatus, setOwnStatus] = useState(null)
  const controlled = statusProp !== undefined
  const status = controlled ? statusProp : ownStatus
  const setStatus = (s) => { if (!controlled) setOwnStatus(s); onStatusChange?.(s) }

  const [phase, setPhase] = useState('idle') // idle | browser | email | code | busy
  const [url, setUrl] = useState('')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    if (controlled) return
    let live = true
    library.status(true).then(s => { if (live) setOwnStatus(s?.error ? { logged_in: false } : s) })
    return () => { live = false }
  }, [controlled])

  useEffect(() => onLibraryLogin(ev => { if (ev?.kind === 'url' && ev.url) setUrl(ev.url) }), [])

  const finish = (res) => {
    if (res?.error) {
      if (res.code !== 'cancelled') setError(res.error)
      return false
    }
    setStatus(res)
    setPhase('idle'); setUrl(''); setCode(''); setError('')
    return true
  }

  const loginBrowser = async () => {
    setError(''); setUrl(''); setPhase('browser')
    const res = await library.login()
    if (!finish(res)) setPhase('idle')
  }
  const cancel = async () => {
    if (phase === 'browser') await library.cancelLogin()
    setPhase('idle'); setError('')
  }
  const sendCode = async () => {
    setError(''); setPhase('busy')
    const res = await library.sendCode(email.trim())
    if (res?.error) { setError(res.error); setPhase('email'); return }
    setPhase('code')
  }
  const verify = async () => {
    setError(''); setPhase('busy')
    const res = await library.verifyCode(email.trim(), code)
    if (!finish(res)) setPhase('code')
  }
  const logout = async () => {
    const res = await library.logout()
    if (res?.error) { setError(res.error); return }
    setStatus({ ...(status || {}), logged_in: false, user: null })
  }

  if (!status) {
    return <span style={{ ...mono, color: 'var(--text-muted)' }}>{t('library.checking')}</span>
  }

  if (status.logged_in) {
    const u = status.user || {}
    const name = u.username ? `@${u.username}` : (u.name || u.email || '')
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        <span style={{ ...mono, color: 'var(--text-secondary)' }} title={u.email || ''}>{t('library.loggedInAs', { name })}</span>
        <button className="btn btn-ghost btn-sm" onClick={logout} style={{ gap: 5 }}><LogOut size={11} /> {t('library.logout')}</button>
        {error && <span role="alert" style={errStyle}>{error}</span>}
      </span>
    )
  }

  if (phase === 'browser') {
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span style={{ ...mono, color: 'var(--text-secondary)', display: 'inline-flex', alignItems: 'center', gap: 5 }}>
          <Loader size={11} style={{ animation: 'spin .7s linear infinite' }} /> {t('library.loggingIn')}
        </span>
        {url && <button className="btn btn-ghost btn-sm" onClick={() => api.openURL(url)}>{t('library.openLink')}</button>}
        <button className="btn btn-secondary btn-sm" onClick={cancel}>{t('library.cancel')}</button>
      </span>
    )
  }

  if (phase === 'email' || phase === 'code' || phase === 'busy') {
    const onCode = phase === 'code' || (phase === 'busy' && code)
    return (
      <span style={{ display: 'inline-flex', flexDirection: 'column', gap: 6, alignItems: 'flex-start' }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          {onCode ? (
            <input className="form-input" style={{ width: 130, padding: '5px 8px' }} inputMode="numeric" autoFocus
              aria-label={t('library.codePlaceholder')} placeholder={t('library.codePlaceholder')} value={code}
              onChange={e => setCode(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && code.trim()) verify() }} />
          ) : (
            <input className="form-input" style={{ width: 200, padding: '5px 8px' }} type="email" autoFocus
              aria-label={t('library.emailPlaceholder')} placeholder={t('library.emailPlaceholder')} value={email}
              onChange={e => setEmail(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && email.trim()) sendCode() }} />
          )}
          <button className="btn btn-primary btn-sm" disabled={phase === 'busy' || !(onCode ? code.trim() : email.trim())}
            onClick={onCode ? verify : sendCode}>
            {onCode ? t('library.verify') : t('library.sendCode')}
          </button>
          <button className="btn btn-ghost btn-sm" onClick={cancel}>{t('library.cancel')}</button>
        </span>
        {onCode && <span style={{ ...mono, color: 'var(--text-muted)' }}>{t('library.codeSent', { email })}</span>}
        {error && <span role="alert" style={errStyle}>{error}</span>}
      </span>
    )
  }

  if (large) {
    return (
      <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'center', gap: 10 }}>
        <button className="btn btn-primary" onClick={loginBrowser} style={{ gap: 8, fontSize: 13, padding: '10px 22px' }}>
          <LogIn size={14} /> {t('library.loginButton')}
        </button>
        <button type="button" onClick={() => { setError(''); setPhase('email') }}
          style={{ ...mono, background: 'none', border: 'none', padding: 0, cursor: 'pointer', color: 'var(--cyan)', textDecoration: 'underline' }}>
          {t('library.useEmail')}
        </button>
        {error && <span role="alert" style={errStyle}>{error}</span>}
      </span>
    )
  }

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <button className="btn btn-primary btn-sm" onClick={loginBrowser} style={{ gap: 5 }}>
        <LogIn size={11} /> {t('library.loginButton')}
      </button>
      {!compact && (
        <button className="btn btn-ghost btn-sm" onClick={() => { setError(''); setPhase('email') }} style={{ gap: 5 }}>
          <Mail size={11} /> {t('library.useEmail')}
        </button>
      )}
      {error && <span role="alert" style={errStyle}>{error}</span>}
    </span>
  )
}
