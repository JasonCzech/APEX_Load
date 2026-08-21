import { type EditorState, type Extension, RangeSetBuilder, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView } from '@codemirror/view'

type HighlightRange = { from: number; to: number; className: string }

function yamlDecorations(state: EditorState): DecorationSet {
  const ranges: HighlightRange[] = []
  for (let lineNumber = 1; lineNumber <= state.doc.lines; lineNumber += 1) {
    const line = state.doc.line(lineNumber)
    const text = line.text
    const key = /^(?:\s*-\s+|\s*)([A-Za-z_][\w.-]*)(?=\s*:)/.exec(text)
    if (key?.[1]) {
      const keyOffset = text.indexOf(key[1])
      ranges.push({ from: line.from + keyOffset, to: line.from + keyOffset + key[1].length, className: 'apex-yaml-key' })
    }

    const comment = /(^|\s)(#[^\n]*)$/.exec(text)
    if (comment?.[2]) {
      const commentOffset = text.lastIndexOf(comment[2])
      ranges.push({ from: line.from + commentOffset, to: line.to, className: 'apex-yaml-comment' })
    }

    const scalar = /:\s*(true|false|null|~|-?\d+(?:\.\d+)?)\s*(?:#.*)?$/i.exec(text)
    if (scalar?.[1]) {
      const scalarOffset = text.indexOf(scalar[1], scalar.index)
      ranges.push({ from: line.from + scalarOffset, to: line.from + scalarOffset + scalar[1].length, className: 'apex-yaml-atom' })
    }

    for (const match of text.matchAll(/<\?xml[^>]*\?>|<\/?[A-Za-z_][^>\n]*>/g)) {
      if (match.index == null) continue
      ranges.push({
        from: line.from + match.index,
        to: line.from + match.index + match[0].length,
        className: 'apex-embedded-xml-tag',
      })
    }

    for (const match of text.matchAll(/\{\{[^}\n]+}}/g)) {
      if (match.index == null) continue
      ranges.push({
        from: line.from + match.index,
        to: line.from + match.index + match[0].length,
        className: 'apex-source-template-token',
      })
    }
  }

  ranges.sort((left, right) => left.from - right.from || left.to - right.to)
  const builder = new RangeSetBuilder<Decoration>()
  let previousTo = -1
  for (const range of ranges) {
    if (range.from < previousTo || range.to <= range.from) continue
    builder.add(range.from, range.to, Decoration.mark({ class: range.className }))
    previousTo = range.to
  }
  return builder.finish()
}

const yamlHighlightField = StateField.define<DecorationSet>({
  create: yamlDecorations,
  update(decorations, transaction) {
    return transaction.docChanged ? yamlDecorations(transaction.state) : decorations.map(transaction.changes)
  },
  provide: field => EditorView.decorations.from(field),
})

export const yamlSyntaxHighlighting: Extension = yamlHighlightField
