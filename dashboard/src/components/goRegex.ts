// The engine compiles extractor regexes with Go's regexp package (RE2 syntax,
// pkg/protocol/http extract.go: regexp.Compile, no flags implied). This turns
// such an expression into a JavaScript RegExp that matches the same way, so a
// browser preview agrees with the replay:
//   - inline flags (?i) (?m) (?s), and scoped groups (?i:…), become JS flags
//     or explicit forms (JS has no inline flags);
//   - Go's ".", "\s", multi-line "^"/"$", \A and \z are written out, because
//     JS reads them differently (JS "." also stops at \r, and "\s" includes
//     Unicode spaces);
//   - (?P<name>…), \Q…\E, \a and \x{…} are rewritten.
// Syntax Go rejects (lookaround, backreferences, unknown escapes) is an error,
// as the replay would fail to compile it. Go syntax with no faithful JS form
// (Unicode classes, POSIX classes, (?U), case-insensitivity on part of the
// expression) is reported as untestable rather than as an invalid expression.

export type GoRegexTranslation =
  | { kind: 'ok'; source: string; flags: string }
  /** Go would reject it too: the replay fails with "invalid regex". */
  | { kind: 'invalid'; reason: string }
  /** Valid for the engine, but the browser cannot evaluate it the same way. */
  | { kind: 'untestable'; reason: string }

interface FlagState { i: boolean; m: boolean; s: boolean }

class Untestable extends Error {}
class Invalid extends Error {}

// Go's \s: [\t\n\f\r ] (JS adds \v and Unicode spaces).
const GO_SPACE_CLASS_BODY = '\\t\\n\\f\\r '
const GO_NOT_SPACE_CLASS_BODY = '\\x00-\\x08\\x0b\\x0e-\\x1f\\x21-\\uffff'
const OCTAL_OR_DIGIT = /[0-9]/
const PUNCTUATION = /[!-/:-@[-`{-~]/

function escapeLiteral(text: string): string {
  return text.replace(/[\\^$.*+?()[\]{}|/-]/g, '\\$&')
}

/** "\x{…}" or "\xHH" at index (the "x"); returns [js escape, chars consumed after the backslash]. */
function hexEscape(expr: string, at: number): [string, number] {
  if (expr[at + 1] === '{') {
    const close = expr.indexOf('}', at + 2)
    const digits = close < 0 ? '' : expr.slice(at + 2, close)
    if (!/^[0-9a-fA-F]{1,8}$/.test(digits)) throw new Invalid('invalid \\x{…} escape')
    const code = parseInt(digits, 16)
    if (code > 0x10ffff) throw new Invalid('invalid \\x{…} escape')
    if (code > 0xffff) throw new Untestable('a character beyond U+FFFF (\\x{…})')
    return [`\\u${code.toString(16).padStart(4, '0')}`, close - at + 1]
  }
  const digits = expr.slice(at + 1, at + 3)
  if (!/^[0-9a-fA-F]{2}$/.test(digits)) throw new Invalid('invalid \\x escape')
  return [`\\x${digits}`, 3]
}

/**
 * An escape at expr[at] === "\\". Returns the JS text and how many characters
 * it used. inClass: inside [...], where class shorthands are written as
 * their members.
 */
function translateEscape(expr: string, at: number, inClass: boolean): { text: string; used: number; atom: boolean } {
  const next = expr[at + 1]
  if (next === undefined) throw new Invalid('trailing backslash')
  switch (next) {
    case 'd': case 'D': case 'w': case 'W':
      return { text: `\\${next}`, used: 2, atom: true }
    case 's':
      return { text: inClass ? GO_SPACE_CLASS_BODY : `[${GO_SPACE_CLASS_BODY}]`, used: 2, atom: true }
    case 'S':
      return { text: inClass ? GO_NOT_SPACE_CLASS_BODY : `[^${GO_SPACE_CLASS_BODY}]`, used: 2, atom: true }
    case 'b': case 'B':
      if (inClass) throw new Invalid(`\\${next} inside a character class`)
      return { text: `\\${next}`, used: 2, atom: false }
    case 'A':
      if (inClass) throw new Invalid('\\A inside a character class')
      return { text: '(?<![\\s\\S])', used: 2, atom: false }
    case 'z':
      if (inClass) throw new Invalid('\\z inside a character class')
      return { text: '(?![\\s\\S])', used: 2, atom: false }
    case 'a':
      return { text: '\\x07', used: 2, atom: true }
    case 'f': case 'n': case 'r': case 't': case 'v':
      return { text: `\\${next}`, used: 2, atom: true }
    case 'x': {
      const [text, used] = hexEscape(expr, at + 1)
      return { text, used: used + 1, atom: true }
    }
    case 'Q': {
      const end = expr.indexOf('\\E', at + 2)
      const literal = expr.slice(at + 2, end < 0 ? expr.length : end)
      const used = (end < 0 ? expr.length : end + 2) - at
      return { text: inClass ? literal.replace(/[\\\]^-]/g, '\\$&') : escapeLiteral(literal), used, atom: literal.length > 0 }
    }
    case 'E':
      // \E only ends a \Q…\E literal (consumed above); Go rejects it alone.
      throw new Invalid('invalid escape sequence \\E')
    case 'p': case 'P':
      throw new Untestable('Unicode character classes (\\p…)')
    case 'C':
      throw new Untestable('\\C (any byte)')
    default:
      break
  }
  if (OCTAL_OR_DIGIT.test(next)) {
    if (next !== '0' && !OCTAL_OR_DIGIT.test(expr[at + 2] ?? '')) throw new Invalid('backreferences (\\1…\\9) are not supported')
    throw new Untestable('octal escapes')
  }
  if (PUNCTUATION.test(next)) return { text: `\\${next}`, used: 2, atom: true }
  throw new Invalid(`invalid escape sequence \\${next}`)
}

/** A character class starting at expr[at] === "[": its JS text and length. */
function translateClass(expr: string, at: number): { text: string; used: number } {
  let pos = at + 1
  let out = '['
  if (expr[pos] === '^') { out += '^'; pos++ }
  // A "]" first in the class is a literal in Go; JS would read "[]" as empty.
  if (expr[pos] === ']') { out += '\\]'; pos++ }
  while (pos < expr.length) {
    const ch = expr[pos]
    if (ch === ']') return { text: `${out}]`, used: pos + 1 - at }
    if (ch === '[' && expr[pos + 1] === ':') throw new Untestable('POSIX character classes ([[:alpha:]])')
    if (ch === '\\') {
      const escape = translateEscape(expr, pos, true)
      out += escape.text
      pos += escape.used
      continue
    }
    // "[" is a literal inside a Go class; escape it for clarity in JS.
    out += ch === '[' ? '\\[' : ch
    pos++
  }
  throw new Invalid('missing closing ]')
}

const FLAG_GROUP = /^\(\?([a-zA-Z]*)(?:-([a-zA-Z]*))?([:)])/

function applyFlags(state: FlagState, set: string, clear: string): FlagState {
  const next = { ...state }
  for (const [letters, value] of [[set, true], [clear, false]] as const) {
    for (const letter of letters) {
      if (letter === 'U') throw new Untestable('the ungreedy flag (?U)')
      if (letter !== 'i' && letter !== 'm' && letter !== 's') throw new Invalid(`invalid or unsupported Perl syntax: (?${set}${clear ? `-${clear}` : ''}`)
      next[letter] = value
    }
  }
  return next
}

/** Translate a Go (RE2) regular expression into a JS RegExp source and flags. */
export function translateGoRegex(expr: string): GoRegexTranslation {
  try {
    const stack: FlagState[] = []
    let flags: FlagState = { i: false, m: false, s: false }
    // The case-sensitivity each matching atom was written under: JS has one
    // "i" for the whole expression.
    const caseStates = new Set<boolean>()
    let out = ''
    let pos = 0
    while (pos < expr.length) {
      const ch = expr[pos]
      if (ch === '\\') {
        const escape = translateEscape(expr, pos, false)
        if (escape.atom) caseStates.add(flags.i)
        out += escape.text
        pos += escape.used
        continue
      }
      if (ch === '[') {
        const cls = translateClass(expr, pos)
        caseStates.add(flags.i)
        out += cls.text
        pos += cls.used
        continue
      }
      if (ch === '(') {
        if (expr[pos + 1] !== '?') {
          stack.push(flags)
          out += '('
          pos++
          continue
        }
        const rest = expr.slice(pos)
        const named = /^\(\?P?<([A-Za-z_][A-Za-z0-9_]*)>/.exec(rest)
        if (named) {
          stack.push(flags)
          out += `(?<${named[1]}>`
          pos += named[0].length
          continue
        }
        if (/^\(\?(?:=|!|<=|<!)/.test(rest)) throw new Invalid('lookahead and lookbehind are not supported')
        const group = FLAG_GROUP.exec(rest)
        if (!group) throw new Invalid(`invalid or unsupported Perl syntax: ${rest.slice(0, 3)}`)
        const scoped = applyFlags(flags, group[1], group[2] ?? '')
        if (group[3] === ':') {
          stack.push(flags)
          flags = scoped
          out += '(?:'
        } else {
          // (?flags) sets them until the end of the enclosing group.
          flags = scoped
        }
        pos += group[0].length
        continue
      }
      if (ch === ')') {
        if (!stack.length) throw new Invalid('unexpected )')
        flags = stack.pop() as FlagState
        out += ')'
        pos++
        continue
      }
      if (ch === '.') {
        caseStates.add(flags.i)
        out += flags.s ? '[\\s\\S]' : '[^\\n]'
        pos++
        continue
      }
      if (ch === '^') {
        out += flags.m ? '(?<![^\\n])' : '^'
        pos++
        continue
      }
      if (ch === '$') {
        out += flags.m ? '(?![^\\n])' : '$'
        pos++
        continue
      }
      if (ch === '*' || ch === '+' || ch === '?' || ch === '|' || ch === '{' || ch === '}') {
        out += ch
        pos++
        continue
      }
      caseStates.add(flags.i)
      out += ch
      pos++
    }
    if (stack.length) throw new Invalid('missing closing )')
    if (caseStates.size > 1) throw new Untestable('case-insensitivity on only part of the expression')
    const caseInsensitive = caseStates.has(true)
    return { kind: 'ok', source: out, flags: caseInsensitive ? 'i' : '' }
  } catch (error) {
    if (error instanceof Untestable) return { kind: 'untestable', reason: error.message }
    if (error instanceof Invalid) return { kind: 'invalid', reason: error.message }
    throw error
  }
}
