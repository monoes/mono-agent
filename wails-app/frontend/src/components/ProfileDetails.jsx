import { useTranslation } from 'react-i18next'
import { Briefcase, GraduationCap } from 'lucide-react'

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
