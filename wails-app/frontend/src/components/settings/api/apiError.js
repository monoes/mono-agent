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

// classify splits what a failed call rejected with into the class the app's Go side put in front of the CLI's
// message ('not_found', 'invalid_input', or '' for any other failure) and the message itself.
export function classify(e) {
  const text = typeof e === 'string' ? e : typeof e?.message === 'string' ? e.message : ''
  const raw = text.trim()
  const m = CLASSED.exec(raw)
  return m ? { cls: m[1], msg: m[2] } : { cls: '', msg: raw }
}

// describeApiError is apiError with one more answer: whether the text is the CLI's own words (verbatim: the page
// has nothing to word them with, and they are English whatever the language of the page) or the page's.
export function describeApiError(e, t) {
  const { cls, msg } = classify(e)
  const known = KNOWN.find(([c, re]) => c === cls && re.test(msg))
  if (known) return { text: t(known[2]), verbatim: false }
  return msg ? { text: msg, verbatim: true } : { text: t('settings.api.errors.unknown'), verbatim: false }
}

export function apiError(e, t) {
  return describeApiError(e, t).text
}
