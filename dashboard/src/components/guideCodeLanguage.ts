import { HighlightStyle, StreamLanguage, syntaxHighlighting } from '@codemirror/language'
import { tags as t } from '@lezer/highlight'

export const guideCodeHighlighting = syntaxHighlighting(HighlightStyle.define([
  { tag: t.variableName, color: 'var(--text-primary)' },
  { tag: t.function(t.variableName), color: 'var(--accent-text)', fontWeight: '600' },
]))

const KEYWORDS: Record<string, Set<string>> = {
  bash: new Set(['if', 'then', 'else', 'elif', 'fi', 'for', 'while', 'do', 'done', 'case', 'esac', 'function', 'in', 'export', 'local', 'return', 'set', 'source', 'alias']),
  sh: new Set(['if', 'then', 'else', 'elif', 'fi', 'for', 'while', 'do', 'done', 'case', 'esac', 'function', 'in', 'export', 'local', 'return', 'set', 'source', 'alias']),
  go: new Set(['package', 'import', 'func', 'return', 'var', 'const', 'type', 'struct', 'interface', 'map', 'chan', 'go', 'defer', 'if', 'else', 'for', 'range', 'switch', 'case', 'select', 'break', 'continue']),
  python: new Set(['def', 'class', 'return', 'import', 'from', 'as', 'if', 'elif', 'else', 'for', 'while', 'in', 'try', 'except', 'finally', 'with', 'lambda', 'yield', 'async', 'await', 'pass', 'raise']),
  java: new Set(['class', 'interface', 'enum', 'public', 'private', 'protected', 'static', 'final', 'void', 'return', 'new', 'package', 'import', 'if', 'else', 'for', 'while', 'try', 'catch', 'throw', 'throws']),
  proto: new Set(['syntax', 'package', 'import', 'option', 'message', 'service', 'rpc', 'returns', 'enum', 'repeated', 'optional', 'required', 'oneof', 'map']),
}

const ATOMS = new Set(['true', 'false', 'null', 'nil', 'none', 'undefined'])
const IDENTIFIER = /^[A-Za-z_$][\w$]*/
const NUMBER = /^(?:0[xX][\da-fA-F]+|\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/

/** A small read-only tokenizer for the documentation languages without a bundled Lezer parser. */
export function guideCodeLanguage(language: string) {
  const normalized = language.toLowerCase()
  const keywords = KEYWORDS[normalized] ?? new Set<string>()
  const hashComments = ['bash', 'sh', 'python', 'proto', 'dotenv'].includes(normalized)
  const dashComments = ['sql', 'dax', 'promql'].includes(normalized)

  return StreamLanguage.define({
    startState: () => ({ blockComment: false }),
    token(stream, state) {
      if (state.blockComment) {
        if (stream.skipTo('*/')) {
          stream.match('*/')
          state.blockComment = false
        } else stream.skipToEnd()
        return 'blockComment'
      }
      if (stream.eatSpace()) return null

      if (hashComments && stream.peek() === '#') {
        stream.skipToEnd()
        return 'lineComment'
      }
      if (dashComments && stream.match('--')) {
        stream.skipToEnd()
        return 'lineComment'
      }
      if (stream.match('//')) {
        stream.skipToEnd()
        return 'lineComment'
      }
      if (stream.match('/*')) {
        if (stream.skipTo('*/')) stream.match('*/')
        else {
          stream.skipToEnd()
          state.blockComment = true
        }
        return 'blockComment'
      }

      const quote = stream.peek()
      if (quote === '"' || quote === "'" || quote === '`') {
        stream.next()
        while (!stream.eol()) {
          const char = stream.next()
          if (char === '\\') stream.next()
          else if (char === quote) break
        }
        return 'string'
      }
      if (stream.match(NUMBER)) return 'number'
      const identifier = stream.match(IDENTIFIER)
      if (identifier) {
        const word = identifier === true ? '' : identifier[0]
        if (ATOMS.has(word.toLowerCase())) return 'bool'
        return keywords.has(word) ? 'keyword' : 'variableName'
      }
      const char = stream.next()
      return !char ? null : /[()[\]{}.,:;]/.test(char) ? 'punctuation' : /[=+*/%<>!&|?-]/.test(char) ? 'operator' : null
    },
  })
}
