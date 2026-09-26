// Rules the workflow editor's inspector applies to a node: which credential
// or browser session it takes, and which text fields may take an image or
// file from the vault. Pure functions so they are unit-testable without the
// canvas.

// LEGACY FALLBACK for API nodes whose schemas don't set credential_platform
// yet. Browser automation nodes never need an entry here: their platform is
// the automation id, read from the installed automation list.
export const CREDENTIAL_PLATFORMS = {
  'service.github': 'github',
  'service.notion': 'notion',
  'service.airtable': 'airtable',
  'service.jira': 'jira',
  'service.linear': 'linear',
  'service.asana': 'asana',
  'service.stripe': 'stripe',
  'service.shopify': 'shopify',
  'service.salesforce': 'salesforce',
  'service.hubspot': 'hubspot',
  'service.google_sheets': 'google_sheets',
  'service.gmail': 'gmail',
  'service.google_drive': 'google_drive',
  'comm.slack': 'slack',
  'comm.discord': 'discord',
  'comm.twilio': 'twilio',
  'comm.whatsapp': 'whatsapp',
  'db.postgres': 'postgresql',
  'db.mysql': 'mysql',
  'db.mongodb': 'mongodb',
  'db.redis': 'redis',
  'service.openrouter': 'openrouter',
  'service.huggingface': 'huggingface',
}

// automationIdsFrom turns `automation list --all --json` into the set of
// installed automation ids (removed built-ins included: their nodes may still
// sit in a workflow and still take a session).
export function automationIdsFrom(res) {
  const list = Array.isArray(res?.automations) ? res.automations : []
  return new Set(list.map(a => a.id).filter(Boolean))
}

// browserAutomationOf returns the automation id when the node type is an
// action of an installed browser automation ("<automation>.<action>").
export function browserAutomationOf(nodeType, automationIds) {
  const prefix = String(nodeType || '').split('.')[0]
  return prefix && automationIds?.has(prefix) ? prefix : null
}

// derivePlatformId picks the platform whose credentials or sessions the
// node's picker lists: the schema's credential_platform first, then the
// automation the node belongs to, then the legacy API map.
export function derivePlatformId(node, schema, automationIds) {
  if (!node) return null
  if (schema?.credential_platform) return schema.credential_platform
  const automation = browserAutomationOf(node.subtype, automationIds)
  if (automation) return automation
  return CREDENTIAL_PLATFORMS[node.subtype] || null
}

// isSessionPicker: the picker lists browser sessions (not API credentials)
// when the node is an action of a browser automation.
export function isSessionPicker(node, automationIds) {
  return !!browserAutomationOf(node?.subtype, automationIds)
}

const MEDIA_HINTS = new Set(['image', 'file', 'path', 'media', 'photo', 'video'])
const MEDIA_WORDS = new Set(['image', 'images', 'img', 'photo', 'photos', 'picture', 'pictures', 'media', 'avatar', 'thumbnail', 'attachment', 'attachments', 'video', 'videos', 'cover', 'banner'])

// isMediaField reports whether a text field may take an image or file from
// the vault (the 🖼 picker and @-autocomplete): an explicit type/format/
// widget hint, or a key that names a media value (image_url, media, photo,
// file_path, …) — but not an id or name of one (video_id, file_name).
export function isMediaField(field) {
  if (!field) return false
  const hints = [field.type, field.format, field.widget, field.ui?.widget, field.ui?.kind].map(h => String(h || '').toLowerCase())
  if (hints.some(h => MEDIA_HINTS.has(h))) return true
  const tokens = String(field.key || '')
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)
  if (!tokens.length) return false
  const last = tokens[tokens.length - 1]
  if (['id', 'ids', 'name', 'names', 'count', 'type'].includes(last)) return false
  if (tokens.some(t => MEDIA_WORDS.has(t))) return true
  // file_path / local_path / file: a local file to upload.
  return (tokens.includes('file') && (last === 'path' || last === 'file')) || tokens.join('_') === 'local_path'
}
