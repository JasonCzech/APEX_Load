import { RangeSetBuilder, StateEffect, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view'
import type { LineRange } from './dslJsonPointers'

// CodeMirror decoration plumbing for the Script JSON editor: an "active line"
// highlight driven by the selected DSL pointer range, plus parameter-match and
// auto-parameter-candidate mark decorations. Extracted from ScriptEditor.tsx so
// the editor component does not carry low-level CodeMirror StateField wiring.

export interface ScriptCodeMatchRange {
  from: number
  to: number
}

export const setScriptCodeRange = StateEffect.define<LineRange | null>()
export const setScriptCodeParameterMatches = StateEffect.define<ScriptCodeMatchRange[]>()
export const setScriptCodeCandidateMatches = StateEffect.define<ScriptCodeMatchRange[]>()

function buildScriptCodeDecorations(state: EditorView['state'], range: LineRange | null): DecorationSet {
  if (!range) return Decoration.none
  const builder = new RangeSetBuilder<Decoration>()
  const lineDeco = Decoration.line({ class: 'script-code-active-line' })
  const start = Math.max(1, range.startLine)
  const end = Math.min(state.doc.lines, range.endLine)
  for (let lineNo = start; lineNo <= end; lineNo += 1) {
    const line = state.doc.line(lineNo)
    builder.add(line.from, line.from, lineDeco)
  }
  return builder.finish()
}

export const scriptCodeRangeField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setScriptCodeRange)) {
        next = buildScriptCodeDecorations(tr.state, effect.value)
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})

function buildScriptCodeParameterDecorations(state: EditorView['state'], ranges: ScriptCodeMatchRange[]): DecorationSet {
  if (ranges.length === 0) return Decoration.none
  const builder = new RangeSetBuilder<Decoration>()
  const matchDeco = Decoration.mark({ class: 'script-code-parameter-match' })
  const firstMatchDeco = Decoration.mark({ class: 'script-code-parameter-match first' })
  ranges
    .filter(range => range.from >= 0 && range.to > range.from && range.to <= state.doc.length)
    .sort((left, right) => left.from - right.from || left.to - right.to)
    .forEach((range, index) => {
      builder.add(range.from, range.to, index === 0 ? firstMatchDeco : matchDeco)
    })
  return builder.finish()
}

export const scriptCodeParameterMatchField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setScriptCodeParameterMatches)) {
        next = buildScriptCodeParameterDecorations(tr.state, effect.value)
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})

function buildScriptCodeCandidateDecorations(state: EditorView['state'], ranges: ScriptCodeMatchRange[]): DecorationSet {
  if (ranges.length === 0) return Decoration.none
  const builder = new RangeSetBuilder<Decoration>()
  const matchDeco = Decoration.mark({ class: 'script-code-auto-parameter-match' })
  const firstMatchDeco = Decoration.mark({ class: 'script-code-auto-parameter-match first' })
  ranges
    .filter(range => range.from >= 0 && range.to > range.from && range.to <= state.doc.length)
    .sort((left, right) => left.from - right.from || left.to - right.to)
    .forEach((range, index) => {
      builder.add(range.from, range.to, index === 0 ? firstMatchDeco : matchDeco)
    })
  return builder.finish()
}

export const scriptCodeCandidateMatchField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setScriptCodeCandidateMatches)) {
        next = buildScriptCodeCandidateDecorations(tr.state, effect.value)
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})

// --- Inline resolved values (VS Code-style inlineValues for a debug run) -----
// While paused/after a debug run, render the concrete value each {{...}} token
// resolved to as a dimmed annotation after the token, scoped to the active
// action's line range so the editor stays readable.

const TEMPLATE_TOKEN_RE = /\{\{\s*([^{}]+?)\s*\}\}/g

export interface ScriptCodeResolvedValues {
  values: Record<string, string>
  range: LineRange | null
}

export const setScriptCodeResolvedValues = StateEffect.define<ScriptCodeResolvedValues | null>()

/** Look up the resolved value for a `{{ns.name}}` token, or undefined. Exported for tests. */
export function resolvedValueForToken(expression: string, values: Record<string, string>): string | undefined {
  const trimmed = expression.trim()
  const dot = trimmed.indexOf('.')
  const namespace = dot === -1 ? trimmed : trimmed.slice(0, dot)
  const rest = dot === -1 ? '' : trimmed.slice(dot + 1).trim()
  // Secrets are masked; fn/fake have no fixed value.
  if (!['var', 'param', 'env', 'data'].includes(namespace)) return undefined
  return Object.prototype.hasOwnProperty.call(values, rest) ? values[rest] : undefined
}

function truncateValue(value: string, max = 80): string {
  if (!value) return '(empty)'
  return value.length > max ? `${value.slice(0, max)}…` : value
}

class ResolvedValueWidget extends WidgetType {
  constructor(readonly value: string) { super() }
  eq(other: ResolvedValueWidget): boolean { return other.value === this.value }
  toDOM(): HTMLElement {
    const span = document.createElement('span')
    span.className = 'cm-inline-resolved-value'
    span.textContent = ` → ${truncateValue(this.value)}`
    return span
  }
  ignoreEvent(): boolean { return true }
}

function buildScriptCodeResolvedValueDecorations(state: EditorView['state'], payload: ScriptCodeResolvedValues | null): DecorationSet {
  if (!payload || !payload.range || Object.keys(payload.values).length === 0) return Decoration.none
  const builder = new RangeSetBuilder<Decoration>()
  const start = Math.max(1, payload.range.startLine)
  const end = Math.min(state.doc.lines, payload.range.endLine)
  for (let lineNo = start; lineNo <= end; lineNo += 1) {
    const line = state.doc.line(lineNo)
    TEMPLATE_TOKEN_RE.lastIndex = 0
    for (let match = TEMPLATE_TOKEN_RE.exec(line.text); match; match = TEMPLATE_TOKEN_RE.exec(line.text)) {
      const value = resolvedValueForToken(match[1], payload.values)
      if (value === undefined) continue
      const to = line.from + match.index + match[0].length
      builder.add(to, to, Decoration.widget({ widget: new ResolvedValueWidget(value), side: 1 }))
    }
  }
  return builder.finish()
}

export const scriptCodeResolvedValueField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setScriptCodeResolvedValues)) {
        next = buildScriptCodeResolvedValueDecorations(tr.state, effect.value)
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})
