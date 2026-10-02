// apiError is the text the page shows for a failed `monoagentcli api …` call.
//
// What reaches the page is the CLI's last stderr line, in English, led by the
// class of its exit code when it has one (app_api.go: "not_found: " for exit 2,
// "invalid_input: " for exit 3). The cases a key can meet are worded in the
// language of the page; the class is what tells them from another failure that
// happens to share some words. Anything else is the CLI's own text, without the
// class, because it is all there is to show.

const CLASSED = /^(not_found|invalid_input): ([\s\S]*)$/

// [class, what the CLI says, the string that words it]
const KNOWN = [
  ['invalid_input', /already exists in this profile/, 'settings.api.errors.nameTaken'],
  ['invalid_input', /^key name must be/, 'settings.api.errors.nameInvalid'],
  ['not_found', /^api key not found/, 'settings.api.errors.keyNotFound'],
]

export function apiError(e, t) {
  const text = typeof e === 'string' ? e : typeof e?.message === 'string' ? e.message : ''
  const raw = text.trim()
  const m = CLASSED.exec(raw)
  const [cls, msg] = m ? [m[1], m[2]] : ['', raw]
  const known = KNOWN.find(([c, re]) => c === cls && re.test(msg))
  if (known) return t(known[2])
  return msg || t('settings.api.errors.unknown')
}
