// Guard for the global <select> style (see "UI style guide: form controls"
// in AGENTS.md). Every select gets its dark look from the `select` rule in
// index.css; WebKitGTK paints light native chrome otherwise. Inline styles
// on a select must not re-style that chrome: `appearance` belongs to the
// global rule, and a `background` shorthand wipes its chevron.
//
// The scan reads each <select> opening tag in src/**/*.jsx, takes its
// inline style object, and follows `style={name}` / `...name` to a
// `const name = {...}` in the same file (recursively, so a spread of a
// shared inputStyle is caught too).
import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, relative } from 'node:path'

const SRC = fileURLToPath(new URL('.', import.meta.url))
const GUIDE = 'see "UI style guide: form controls" in AGENTS.md'
const BANNED = [
  { re: /\bappearance\s*:/, what: 'appearance' },
  { re: /\bWebkitAppearance\s*:/, what: 'WebkitAppearance' },
  { re: /(^|[^\w-])background\s*:/, what: 'the background shorthand' },
]

function jsxFiles(dir) {
  return readdirSync(dir).flatMap(f => {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) return jsxFiles(p)
    return f.endsWith('.jsx') && !f.includes('.test.') ? [p] : []
  })
}

// Text from an opening `{` to its matching `}`.
function balanced(src, open) {
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}' && --depth === 0) return src.slice(open, i + 1)
  }
  return src.slice(open)
}

// The `<select ...>` opening tag, skipping `>` inside JSX expressions.
function openingTag(src, start) {
  let depth = 0
  for (let i = start; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') depth--
    else if (src[i] === '>' && depth === 0) return src.slice(start, i + 1)
  }
  return src.slice(start)
}

function constObject(src, name) {
  const m = new RegExp(`const\\s+${name}\\s*=\\s*\\{`).exec(src)
  return m ? balanced(src, m.index + m[0].length - 1) : ''
}

// Every style source a select's style={...} resolves to within the file.
function styleSources(src, expr, seen = new Set()) {
  const out = [expr]
  for (const [, name] of expr.matchAll(/(?:^\{|\.\.\.)\s*([A-Za-z_$][\w$]*)/g)) {
    if (seen.has(name)) continue
    seen.add(name)
    const obj = constObject(src, name)
    if (obj) out.push(...styleSources(src, obj, seen))
  }
  return out
}

function violations() {
  const found = []
  for (const file of jsxFiles(SRC)) {
    const src = readFileSync(file, 'utf8')
    for (const m of src.matchAll(/<select\b/g)) {
      const tag = openingTag(src, m.index)
      const at = tag.indexOf('style={')
      if (at < 0) continue
      const expr = balanced(tag, at + 'style='.length)
      const line = src.slice(0, m.index).split('\n').length
      for (const text of styleSources(src, expr)) {
        for (const { re, what } of BANNED) {
          if (re.test(text)) found.push(`${relative(SRC, file)}:${line} sets ${what} inline on a <select>`)
        }
      }
    }
  }
  return [...new Set(found)]
}

describe('select style guide', () => {
  it('finds the selects it guards', () => {
    const count = jsxFiles(SRC).reduce((n, f) => n + (readFileSync(f, 'utf8').match(/<select\b/g) || []).length, 0)
    expect(count).toBeGreaterThan(20)
  })

  it('never re-styles select chrome inline', () => {
    const found = violations()
    expect(found, `${found.join('\n')}\nUse the global select rule and its modifier classes instead — ${GUIDE}.`).toEqual([])
  })

  it('keeps the global dark select rule in index.css', () => {
    const css = readFileSync(join(SRC, 'index.css'), 'utf8')
    const rule = /(^|\n)select\s*\{([^}]*)\}/.exec(css)
    expect(rule, `index.css lost its global \`select\` rule — ${GUIDE}`).not.toBeNull()
    expect(rule[2]).toMatch(/(^|\s)appearance:\s*none/)
    expect(rule[2]).toMatch(/background-image:\s*url\(/)
    expect(rule[2]).toMatch(/color-scheme:\s*dark/)
  })

  it('catches the patterns it is meant to', () => {
    const src = `const inputStyle = { background: '#000' }
const s = { ...inputStyle, width: 1 }
<select style={s}></select>`
    const expr = '{s}'
    expect(styleSources(src, expr).some(t => BANNED[2].re.test(t))).toBe(true)
    expect(BANNED[2].re.test("{ backgroundColor: 'x' }")).toBe(false)
    expect(BANNED[0].re.test("{ appearance: 'none' }")).toBe(true)
  })
})
