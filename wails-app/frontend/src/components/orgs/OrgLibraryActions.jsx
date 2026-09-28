// The Orgs page's monoes.me entry points: "Org templates" (browse and add
// orgs from the library) and "Publish to monoes" for the selected org. Each
// button owns its dialog, so OrgsPanel only places them.
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Library, UploadCloud } from 'lucide-react'
import LibraryModal from '../library/LibraryModal.jsx'
import PublishToMonoesDialog from '../library/PublishToMonoesDialog.jsx'

const railBtn = {
  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4,
  fontFamily: 'var(--font-mono)', fontSize: 10.5, padding: '6px 8px', borderRadius: 'var(--radius)',
  background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-secondary)', cursor: 'pointer',
}

// onInstalled gets the CLI's install result ({local_id: <org name>, …}).
export function OrgTemplatesButton({ onInstalled }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <button onClick={() => setOpen(true)} style={railBtn}>
        <Library size={12} /> {t('library.orgTemplates')}
      </button>
      {open && <LibraryModal kind="org" onClose={() => setOpen(false)} onInstalled={onInstalled} />}
    </>
  )
}

export function PublishOrgButton({ orgName }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <button onClick={() => setOpen(true)} title={t('library.publish.button')}
        style={{ ...railBtn, fontSize: 10, padding: '3px 8px', gap: 4 }}>
        <UploadCloud size={11} /> {t('library.publish.button')}
      </button>
      {open && <PublishToMonoesDialog kind="org" localId={orgName} defaultName={orgName} onClose={() => setOpen(false)} />}
    </>
  )
}
