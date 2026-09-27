import { useTranslation } from 'react-i18next'
import { Briefcase, GraduationCap, Pin } from 'lucide-react'

// Experience and Education from a profile read (people get: experience /
// education arrays). Renders nothing when the person has none.

const join = (...parts) => parts.filter(Boolean).join(' · ')

function EntryLink({ label, url, onOpenURL }) {
  if (!label) return null
  if (!url || !onOpenURL) return <span>{label}</span>
  return (
    <button className="profile-entry-link" onClick={() => onOpenURL(url)}>{label}</button>
  )
}

function Entry({ icon: Icon, title, org, orgUrl, orgExtra, when, where, description, extras, onOpenURL }) {
  return (
    <div className="profile-entry">
      <div className="profile-entry-icon"><Icon size={14} /></div>
      <div className="profile-entry-body">
        <div className="profile-entry-title">{title}</div>
        {(org || orgExtra) && (
          <div className="profile-entry-org">
            <EntryLink label={org} url={orgUrl} onOpenURL={onOpenURL} />
            {org && orgExtra ? ' · ' : ''}{orgExtra}
          </div>
        )}
        {when && <div className="profile-entry-meta">{when}</div>}
        {where && <div className="profile-entry-meta">{where}</div>}
        {description && <p className="profile-entry-desc">{description}</p>}
        {extras.filter(([, v]) => v).map(([label, v]) => (
          <div key={label} className="profile-entry-meta"><span className="profile-entry-label">{label}:</span> {v}</div>
        ))}
      </div>
    </div>
  )
}

export function ExperienceSection({ experience, onOpenURL }) {
  const { t } = useTranslation()
  if (!Array.isArray(experience) || experience.length === 0) return null
  return (
    <div className="profile-section">
      <div className="profile-section-title">{t('personProfile.experience')}</div>
      <div className="profile-entries">
        {experience.map((p, i) => (
          <Entry
            key={i}
            icon={Briefcase}
            title={p.title}
            org={p.company}
            orgUrl={p.company_url}
            orgExtra={p.employment_type}
            when={join(p.date_range, p.duration)}
            where={join(p.location, p.location_type)}
            description={p.description}
            extras={[[t('personProfile.skills'), p.skills]]}
            onOpenURL={onOpenURL}
          />
        ))}
      </div>
    </div>
  )
}

export function EducationSection({ education, onOpenURL }) {
  const { t } = useTranslation()
  if (!Array.isArray(education) || education.length === 0) return null
  return (
    <div className="profile-section">
      <div className="profile-section-title">{t('personProfile.education')}</div>
      <div className="profile-entries">
        {education.map((s, i) => (
          <Entry
            key={i}
            icon={GraduationCap}
            title={<EntryLink label={s.school} url={s.school_url} onOpenURL={onOpenURL} />}
            org={join(s.degree, s.field_of_study)}
            when={s.date_range}
            description={s.description}
            extras={[
              [t('personProfile.grade'), s.grade],
              [t('personProfile.activities'), s.activities],
              [t('personProfile.skills'), s.skills],
            ]}
            onOpenURL={onOpenURL}
          />
        ))}
      </div>
    </div>
  )
}

// ── Profile details (people get: profile_details) ──────────────────────────
// The platform extras a profile read stored: links, pronouns, counts, join
// date, verification, pinned post… Keys are generic across platforms; only
// the ones present render. Renders nothing when there are none.

const COUNT_KEYS = ['likes_count', 'friend_count', 'affiliates_count', 'connection_count']

export function formatCount(n, locale) {
  const v = typeof n === 'number' ? n : Number(n)
  if (!Number.isFinite(v)) return String(n ?? '')
  if (Math.abs(v) < 10000) return v.toLocaleString(locale)
  return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(v)
}

// formatJoinDate renders "YYYY-MM-DD" as a date and "YYYY-MM" as month and
// year; anything else is shown as stored.
export function formatJoinDate(s, locale) {
  if (typeof s !== 'string') return ''
  let m = s.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  if (m) {
    return new Date(Date.UTC(+m[1], +m[2] - 1, +m[3])).toLocaleDateString(locale, { year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC' })
  }
  m = s.match(/^(\d{4})-(\d{2})$/)
  if (m) {
    return new Date(Date.UTC(+m[1], +m[2] - 1, 1)).toLocaleDateString(locale, { year: 'numeric', month: 'long', timeZone: 'UTC' })
  }
  return s
}

const hostOf = (url) => {
  try {
    return new URL(/^[a-z]+:/i.test(url) ? url : 'https://' + url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

const withScheme = (url) => (/^[a-z][a-z0-9+.-]*:/i.test(url) ? url : 'https://' + url)

function Row({ label, children }) {
  return (
    <div className="profile-detail-row">
      <span className="profile-detail-label">{label}</span>
      <span className="profile-detail-value">{children}</span>
    </div>
  )
}

export function ProfileDetailsSection({ details, onOpenURL }) {
  const { t, i18n } = useTranslation()
  if (!details || typeof details !== 'object' || Object.keys(details).length === 0) return null
  const locale = i18n?.language
  const d = details
  const open = (url) => onOpenURL && onOpenURL(url)
  const link = (label, url) => (
    <button className="profile-entry-link profile-detail-link" onClick={() => open(url)}>{label}</button>
  )
  const list = (v) => (Array.isArray(v) ? v.filter(Boolean) : v ? [v] : [])
  const links = list(d.links).filter((l) => l && l.url)
  const pronouns = list(d.pronouns)
  const highlights = list(d.highlights)
  const contact = d.contact && typeof d.contact === 'object' ? d.contact : {}
  const pinned = d.pinned_post && typeof d.pinned_post === 'object' ? d.pinned_post : null
  const rows = []

  if (d.profile_category) rows.push([t('profileDetails.category'), d.profile_category])
  if (d.account_type) rows.push([t('profileDetails.accountType'), t(`profileDetails.accountTypes.${d.account_type}`, { defaultValue: d.account_type })])
  if (d.verification_type) rows.push([t('profileDetails.verification'), t(`profileDetails.verificationTypes.${d.verification_type}`, { defaultValue: d.verification_type })])
  if (typeof d.is_private === 'boolean') rows.push([t('profileDetails.visibility'), d.is_private ? t('profileDetails.private') : t('profileDetails.public')])
  if (pronouns.length) rows.push([t('profileDetails.pronouns'), pronouns.join(', ')])
  if (d.current_company) rows.push([t('profileDetails.company'), d.current_company])
  if (d.connection_degree) rows.push([t('profileDetails.connectionDegree'), d.connection_degree])
  for (const k of COUNT_KEYS) {
    if (d[k] !== undefined && d[k] !== null && d[k] !== '') rows.push([t(`profileDetails.${k}`), formatCount(d[k], locale)])
  }
  if (d.join_date) rows.push([t('profileDetails.joined'), formatJoinDate(d.join_date, locale)])
  if (d.birth_date) rows.push([t('profileDetails.born'), d.birth_date])
  if (d.language) rows.push([t('profileDetails.language'), d.language])
  if (d.threads_handle) {
    const h = String(d.threads_handle).replace(/^@/, '')
    rows.push([t('profileDetails.threads'), link('@' + h, `https://www.threads.net/@${h}`)])
  }
  links.forEach((l, i) => rows.push([i === 0 ? t('profileDetails.links') : '', link(l.title || hostOf(l.url), withScheme(l.url))]))
  if (contact.email) rows.push([t('profileDetails.email'), link(contact.email, `mailto:${contact.email}`)])
  if (contact.phone) rows.push([t('profileDetails.phone'), link(contact.phone, `tel:${String(contact.phone).replace(/[^\d+]/g, '')}`)])
  if (contact.address) rows.push([t('profileDetails.address'), contact.address])

  if (!rows.length && !highlights.length && !pinned && !d.banner_url) return null
  return (
    <div className="profile-section">
      <div className="profile-section-title">{t('profileDetails.title')}</div>
      {d.banner_url && <img className="profile-detail-banner" src={d.banner_url} alt={t('profileDetails.banner')} />}
      {rows.length > 0 && (
        <div className="profile-detail-rows">
          {rows.map(([label, value], i) => <Row key={i} label={label}>{value}</Row>)}
        </div>
      )}
      {highlights.length > 0 && (
        <div className="profile-detail-block">
          <div className="profile-detail-label">{t('profileDetails.highlights')}</div>
          <div className="profile-detail-chips">
            {highlights.map((h, i) => <span key={i} className="profile-detail-chip">{h}</span>)}
          </div>
        </div>
      )}
      {pinned && (pinned.text || pinned.url) && (
        <div className="profile-detail-block">
          <div className="profile-detail-label"><Pin size={11} /> {t('profileDetails.pinnedPost')}</div>
          {pinned.text && <p className="profile-entry-desc">{pinned.text}</p>}
          {pinned.url && link(t('profileDetails.openPost'), pinned.url)}
        </div>
      )}
    </div>
  )
}
