import { useTranslation } from 'react-i18next'

// The profile a row belongs to. Only rows from the All profiles view carry
// one, so the profile view never shows a chip.
export default function ProfileChip({ row }) {
  const { t } = useTranslation()
  if (!row?.profile_id) return null
  const name = row.profile_name || row.profile_id
  return <span className="dash-chip dash-chip-profile" title={t('dashboard.profiles.chipTitle', { name })}>{name}</span>
}
