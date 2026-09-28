// Line diff for the coder-mode Edit/MultiEdit cards: turns an edit's
// old_string → new_string into unified-diff lines. Plain LCS over lines —
// an edit's strings are bounded by the CLI (MaxToolFieldBytes), so the
// quadratic table stays small; past MAX_CELLS it falls back to "all old
// lines removed, all new lines added", which is still a correct diff.

const MAX_CELLS = 250000

function splitLines(s) {
  if (s === '' || s == null) return []
  const lines = String(s).split('\n')
  // A trailing newline ends the last line; it doesn't start an empty one.
  if (lines[lines.length - 1] === '') lines.pop()
  return lines
}

// diffLines returns [{ type: ' ' | '-' | '+', text }] in order.
export function diffLines(oldText, newText) {
  const a = splitLines(oldText)
  const b = splitLines(newText)
  if (a.length * b.length > MAX_CELLS) {
    return [...a.map(text => ({ type: '-', text })), ...b.map(text => ({ type: '+', text }))]
  }
  // lcs[i][j] = LCS length of a[i:] and b[j:].
  const lcs = Array.from({ length: a.length + 1 }, () => new Uint32Array(b.length + 1))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) { out.push({ type: ' ', text: a[i] }); i++; j++ }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) { out.push({ type: '-', text: a[i] }); i++ }
    else { out.push({ type: '+', text: b[j] }); j++ }
  }
  while (i < a.length) out.push({ type: '-', text: a[i++] })
  while (j < b.length) out.push({ type: '+', text: b[j++] })
  return out
}

// diffStats counts added and removed lines.
export function diffStats(lines) {
  let added = 0
  let removed = 0
  for (const l of lines) {
    if (l.type === '+') added++
    else if (l.type === '-') removed++
  }
  return { added, removed }
}
