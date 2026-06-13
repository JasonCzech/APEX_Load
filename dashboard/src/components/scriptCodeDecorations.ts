import { RangeSetBuilder, StateEffect, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView } from '@codemirror/view'
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
