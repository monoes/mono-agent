import { useState, useEffect, useCallback, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import {
  LayoutDashboard, Users,
  Terminal, PlayCircle, Settings, Image, Mail, KeyRound,
  ChevronDown, Plus, Check, Building2, FolderOpen, FolderCog, Loader2
} from 'lucide-react'
import { GetVersion } from '../wailsjs/go/main/App'
import * as WailsApp from '../wailsjs/go/main/App'
import { notify } from '../services/api.js'
import { confirm } from './ConfirmDialog.jsx'

const GetHILItems          = WailsApp.GetHILItems          ?? (async () => [])
const GetProfiles          = WailsApp.GetProfiles          ?? (async () => [])
const CreateProfile        = WailsApp.CreateProfile        ?? (async () => {})
const SwitchProfile        = WailsApp.SwitchProfile        ?? (async () => {})
const ChooseProfileFolder  = WailsApp.ChooseProfileFolder  ?? (async () => '')
const MoveProfileFolder    = WailsApp.MoveProfileFolder    ?? (async () => {})
const RevealProfileFolder  = WailsApp.RevealProfileFolder  ?? (async () => {})

// labelKey indexes sidebar.nav.* in src/locales/<lang>.json — see docs/i18n.md.
const NAV_ITEMS = [
  { id: 'dashboard',   labelKey: 'dashboard',   icon: LayoutDashboard, section: 'MAIN' },
  { id: 'noderunner',  labelKey: 'noderunner',  icon: PlayCircle,      section: 'MAIN' },
  { id: 'orgs',        labelKey: 'orgs',        icon: Building2,       section: 'MAIN' },
  { id: 'people',      labelKey: 'people',      icon: Users,           section: 'DATA' },
  { id: 'communications', labelKey: 'communications', icon: Mail,      section: 'DATA' },
  { id: 'vault',       labelKey: 'vault',       icon: Image,           section: 'DATA' },
  { id: 'secretsVault', labelKey: 'secretsVault', icon: KeyRound,      section: 'DATA' },
  { id: 'logs',        labelKey: 'logs',        icon: Terminal,        section: 'DEBUG' },
  { id: 'settings',    labelKey: 'settings',    icon: Settings,        section: 'SYSTEM' },
]

function requestNotifyPermission() {
  if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
    Notification.requestPermission().catch(() => {})
  }
}

function notifyNewHIL(items) {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
  const count = items.length
  const label = count === 1
    ? `"${items[0].workflow_name || items[0].node_name}" needs your review`
    : `${count} items are waiting for your review`
  try {
    new Notification('Human in Loop', { body: label, tag: 'hil-pending' })
  } catch { /* sandboxed webview may block */ }
}

export default function Sidebar({ activePage, onNavigate, stats, dbConnected }) {
  const { t } = useTranslation()
  const [ver, setVer] = useState(null)
  const [hilCount, setHilCount] = useState(0)
  const [profiles, setProfiles] = useState([])
  const [activeProfileID, setActiveProfileID] = useState('default')
  const [profileOpen, setProfileOpen] = useState(false)
  const [newProfileName, setNewProfileName] = useState('')
  const [newProfileFolder, setNewProfileFolder] = useState('')
  const [creatingProfile, setCreatingProfile] = useState(false)
  const [profileError, setProfileError] = useState('')
  const [movingProfileID, setMovingProfileID] = useState(null)
  const [dropPos, setDropPos] = useState({ top: 0, left: 0, width: 0 })
  const profileDropRef = useRef(null)
  const profileBtnRef = useRef(null)

  useEffect(() => { GetVersion().then(setVer).catch(() => {}) }, [])
  useEffect(() => { requestNotifyPermission() }, [])

  const loadProfiles = useCallback(async () => {
    try {
      const list = await GetProfiles()
      setProfiles(Array.isArray(list) ? list : [])
      const active = (list ?? []).find(p => p.is_active)
      if (active) setActiveProfileID(active.id)
    } catch { /* non-fatal */ }
  }, [])

  useEffect(() => { loadProfiles() }, [loadProfiles])

  // Close dropdown when clicking outside.
  useEffect(() => {
    if (!profileOpen) return
    const handler = (e) => {
      if (
        profileDropRef.current && !profileDropRef.current.contains(e.target) &&
        profileBtnRef.current && !profileBtnRef.current.contains(e.target)
      ) {
        setProfileOpen(false)
        setCreatingProfile(false)
        setNewProfileName('')
        setNewProfileFolder('')
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [profileOpen])

  const updateDropPos = () => {
    if (profileBtnRef.current) {
      const rect = profileBtnRef.current.getBoundingClientRect()
      setDropPos({ top: rect.bottom + 4, left: rect.left, width: rect.width })
    }
  }

  const toggleProfileOpen = () => {
    if (!profileOpen) updateDropPos()
    setProfileOpen(v => !v)
    setCreatingProfile(false)
    setNewProfileName('')
    setNewProfileFolder('')
    setProfileError('')
  }

  // Reposition dropdown when window resizes while open.
  useEffect(() => {
    if (!profileOpen) return
    window.addEventListener('resize', updateDropPos)
    return () => window.removeEventListener('resize', updateDropPos)
  }, [profileOpen])

  const handleSwitchProfile = async (id) => {
    setProfileError('')
    try {
      await SwitchProfile(id)
      setActiveProfileID(id)
      setProfileOpen(false)
      // Reload the page to re-fetch all profile-scoped data.
      window.location.reload()
    } catch (e) {
      setProfileError(e?.message || 'Failed to switch profile')
    }
  }

  const handleCreateProfile = async () => {
    const name = newProfileName.trim()
    if (!name) return
    setProfileError('')
    try {
      const p = await CreateProfile(name, newProfileFolder || '')
      setNewProfileName('')
      setNewProfileFolder('')
      setCreatingProfile(false)
      await loadProfiles()
      await handleSwitchProfile(p.id)
    } catch (e) {
      setProfileError(e?.message || 'Failed to create profile')
    }
  }

  const handleChooseFolderForCreate = async () => {
    try {
      const path = await ChooseProfileFolder()
      if (path) setNewProfileFolder(path)
    } catch (e) {
      setProfileError(e?.message || 'Failed to choose folder')
    }
  }

  const handleMoveProfileFolder = async (id) => {
    if (movingProfileID) return
    try {
      const path = await ChooseProfileFolder()
      if (!path) return
      // Moving the profile folder is destructive to anything currently
      // running out of it — confirm before proceeding, and surface the
      // outcome on the app toast bus rather than the dropdown's inline
      // error line (which closes with the dropdown and is easy to miss).
      const ok = await confirm(
        'This moves the entire profile folder — workflows keep running may fail. Continue?',
        { title: 'Move profile folder', confirmLabel: 'Move', danger: true },
      )
      if (!ok) return
      setMovingProfileID(id)
      await MoveProfileFolder(id, path)
      notify('profile move', `Profile folder moved to ${path}`)
      await loadProfiles()
    } catch (e) {
      notify('profile move', e?.message || 'Failed to move profile folder')
    } finally {
      setMovingProfileID(null)
    }
  }

  const handleRevealFolder = async (id) => {
    try {
      await RevealProfileFolder(id)
    } catch (e) {
      setProfileError(e?.message || 'Failed to open folder')
    }
  }

  const activeProfileName = profiles.find(p => p.id === activeProfileID)?.name ?? 'Default'

  const pollHIL = useCallback(async () => {
    try {
      const items = await GetHILItems()
      const next = Array.isArray(items) ? items.length : 0
      // Functional updater gives us the previous count without needing a ref.
      setHilCount(prev => {
        if (next > prev) notifyNewHIL(Array.isArray(items) ? items : [])
        return next
      })
    } catch {
      // non-fatal
    }
  }, [])

  useEffect(() => {
    pollHIL()
    const t = setInterval(pollHIL, 5000)
    return () => clearInterval(t)
  }, [pollHIL])

  const getBadge = (id) => {
    if (id === 'hil' && hilCount > 0) return hilCount
    if (!stats) return null
    if (id === 'people' && stats.total_people > 0) return stats.total_people
    return null
  }

  const sections = [...new Set(NAV_ITEMS.map(i => i.section))]

  return (
    <aside className="sidebar">
      <div className="sidebar-titlebar">
        <div className="sidebar-logo">
          <img src="/monkey-logo.png" alt="MonoAgent" style={{ width: 40, height: 40, objectFit: 'contain', background: 'none', flexShrink: 0 }} />
          <div>
            <div className="logo-text">MonoAgent</div>
            <div className="logo-sub">{ver ? `v${ver.version.replace(/^v/, '')}` : ''}</div>
          </div>
        </div>

        {/* Profile switcher */}
        <div style={{ marginTop: 10, marginBottom: 2 }}>
          <button
            ref={profileBtnRef}
            onClick={toggleProfileOpen}
            style={{
              display: 'flex', alignItems: 'center', gap: 6, width: '100%',
              padding: '6px 10px', background: 'rgba(0,180,216,0.07)',
              border: '1px solid rgba(0,180,216,0.15)', borderRadius: 6,
              cursor: 'pointer', color: 'var(--text-secondary)', fontSize: 12,
              fontFamily: 'var(--font-mono)',
            }}
          >
            <span style={{ flex: 1, textAlign: 'left', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {activeProfileName}
            </span>
            <ChevronDown size={11} style={{ flexShrink: 0, transform: profileOpen ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s' }} />
          </button>
        </div>

        {profileOpen && createPortal(
          <div
            ref={profileDropRef}
            style={{
              position: 'fixed', top: dropPos.top, left: dropPos.left, width: dropPos.width,
              zIndex: 9999, background: '#0d1a28',
              border: '1px solid rgba(0,180,216,0.2)', borderRadius: 8,
              overflow: 'hidden', boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
            }}
          >
            {profiles.map(p => (
              <div
                key={p.id}
                onClick={() => p.id !== activeProfileID && handleSwitchProfile(p.id)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  padding: '9px 12px', cursor: p.id === activeProfileID ? 'default' : 'pointer',
                  fontSize: 12, color: p.id === activeProfileID ? '#00b4d8' : 'var(--text-secondary)',
                  background: p.id === activeProfileID ? 'rgba(0,180,216,0.07)' : 'transparent',
                }}
                onMouseEnter={e => { if (p.id !== activeProfileID) e.currentTarget.style.background = 'rgba(255,255,255,0.05)' }}
                onMouseLeave={e => { e.currentTarget.style.background = p.id === activeProfileID ? 'rgba(0,180,216,0.07)' : 'transparent' }}
              >
                {p.id === activeProfileID ? <Check size={11} color="#00b4d8" /> : <span style={{ width: 11 }} />}
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</div>
                  {p.root_dir && (
                    <div
                      title={p.root_dir}
                      style={{
                        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        fontSize: 10, color: 'var(--text-muted)', marginTop: 1, cursor: 'default',
                      }}
                    >{p.root_dir}</div>
                  )}
                </div>
                <button
                  onClick={e => { e.stopPropagation(); handleRevealFolder(p.id) }}
                  title={t('sidebar.showInFinder')}
                  style={{
                    flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
                    width: 20, height: 20, padding: 0, background: 'transparent', border: 'none',
                    color: 'var(--text-muted)', cursor: 'pointer',
                  }}
                  onMouseEnter={e => { e.currentTarget.style.color = 'var(--text-secondary)' }}
                  onMouseLeave={e => { e.currentTarget.style.color = 'var(--text-muted)' }}
                >
                  <FolderOpen size={12} />
                </button>
                <button
                  onClick={e => { e.stopPropagation(); handleMoveProfileFolder(p.id) }}
                  disabled={movingProfileID === p.id}
                  title={t('sidebar.changeProfileFolder')}
                  style={{
                    flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
                    width: 20, height: 20, padding: 0, background: 'transparent', border: 'none',
                    color: 'var(--text-muted)', cursor: movingProfileID === p.id ? 'default' : 'pointer',
                  }}
                  onMouseEnter={e => { if (movingProfileID !== p.id) e.currentTarget.style.color = 'var(--text-secondary)' }}
                  onMouseLeave={e => { e.currentTarget.style.color = 'var(--text-muted)' }}
                >
                  {movingProfileID === p.id
                    ? <Loader2 size={12} style={{ animation: 'spin 0.7s linear infinite' }} />
                    : <FolderCog size={12} />}
                </button>
              </div>
            ))}

            {profileError && (
              <div style={{ padding: '6px 12px', fontSize: 11, color: '#ff6b6b', borderTop: '1px solid rgba(255,0,0,0.1)' }}>
                {profileError}
              </div>
            )}
            <div style={{ borderTop: '1px solid rgba(255,255,255,0.06)', padding: '8px' }}>
              {creatingProfile ? (
                <div>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <input
                      autoFocus
                      value={newProfileName}
                      onChange={e => setNewProfileName(e.target.value)}
                      onKeyDown={e => { if (e.key === 'Enter') handleCreateProfile(); if (e.key === 'Escape') { setCreatingProfile(false); setNewProfileName(''); setNewProfileFolder('') } }}
                      placeholder={t('sidebar.profileNamePlaceholder')}
                      style={{
                        flex: 1, padding: '5px 8px', background: 'rgba(255,255,255,0.06)',
                        border: '1px solid rgba(0,180,216,0.3)', borderRadius: 4,
                        color: 'var(--text-primary)', fontSize: 12, outline: 'none',
                      }}
                    />
                    <button
                      onClick={handleChooseFolderForCreate}
                      title={t('sidebar.chooseFolder')}
                      style={{
                        flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
                        width: 26, padding: '5px 0', background: 'rgba(255,255,255,0.06)',
                        border: '1px solid rgba(255,255,255,0.12)', borderRadius: 4,
                        color: 'var(--text-secondary)', cursor: 'pointer',
                      }}
                    ><FolderOpen size={12} /></button>
                    <button
                      onClick={handleCreateProfile}
                      style={{
                        padding: '5px 10px', background: 'rgba(0,180,216,0.2)',
                        border: '1px solid rgba(0,180,216,0.4)', borderRadius: 4,
                        color: '#00b4d8', fontSize: 12, cursor: 'pointer',
                      }}
                    >Create</button>
                  </div>
                  {newProfileFolder && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 4, padding: '0 2px' }}>
                      <span style={{
                        flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap', fontSize: 10, color: 'var(--text-muted)',
                      }}>→ {newProfileFolder}</span>
                      <span
                        onClick={() => setNewProfileFolder('')}
                        title={t('sidebar.useDefaultLocation')}
                        style={{ flexShrink: 0, fontSize: 11, color: 'var(--text-muted)', cursor: 'pointer' }}
                      >×</span>
                    </div>
                  )}
                </div>
              ) : (
                <button
                  onClick={() => setCreatingProfile(true)}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 6, width: '100%',
                    padding: '6px 4px', background: 'transparent', border: 'none',
                    color: 'var(--text-muted)', fontSize: 12, cursor: 'pointer',
                  }}
                  onMouseEnter={e => { e.currentTarget.style.color = 'var(--text-secondary)' }}
                  onMouseLeave={e => { e.currentTarget.style.color = 'var(--text-muted)' }}
                >
                  <Plus size={11} /> New profile
                </button>
              )}
            </div>
          </div>,
          document.body
        )}
      </div>

      <nav className="sidebar-nav" aria-label="Main navigation">
        {sections.map(section => (
          <div key={section}>
            <div className="nav-section-label">{t(`sidebar.section.${section}`)}</div>
            {NAV_ITEMS.filter(i => i.section === section).map(item => {
              const Icon = item.icon
              const badge = getBadge(item.id)
              const isActive = activePage === item.id
              const label = t(`sidebar.nav.${item.labelKey}`)
              return (
                <div
                  key={item.id}
                  className={`nav-item ${isActive ? 'active' : ''}`}
                  onClick={() => onNavigate(item.id)}
                  onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && onNavigate(item.id)}
                  role="button"
                  tabIndex={0}
                  aria-current={isActive ? 'page' : undefined}
                  aria-label={label}
                >
                  <Icon className="nav-icon" size={15} />
                  <span>{label}</span>
                  {badge != null && (
                    <span className="nav-badge" aria-label={`${badge} items`}>{badge > 999 ? '999+' : badge}</span>
                  )}
                </div>
              )
            })}
          </div>
        ))}
      </nav>

      <div className="sidebar-footer">
        {/* Platform session indicators — platform set derived from active sessions */}
        {stats?.sessions?.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            {[...new Set(stats.sessions.filter(s => s.active).map(s => s.platform))].map(p => {
              const session = stats.sessions.find(s => s.platform === p && s.active)
              if (!session) return null
              return (
                <div key={p} style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '4px 8px', borderRadius: 4,
                  fontSize: 11, fontFamily: 'var(--font-mono)',
                  color: 'var(--text-secondary)',
                }}>
                  <span className="status-dot connected" />
                  <span style={{ color: 'var(--text-muted)', fontSize: 9, textTransform: 'uppercase', letterSpacing: 1 }}>
                    {p.slice(0, 2)}
                  </span>
                  <span style={{ marginLeft: 2 }}>{session.username}</span>
                </div>
              )
            })}
          </div>
        )}

        <div className="db-status">
          <span className={`status-dot ${dbConnected ? 'connected pulse' : 'disconnected'}`} />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {dbConnected ? 'DB connected' : 'DB offline'}
          </span>
        </div>
      </div>
    </aside>
  )
}
