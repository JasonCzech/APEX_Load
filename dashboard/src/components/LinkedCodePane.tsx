import { useEffect, useMemo, useRef } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { cpp } from '@codemirror/lang-cpp'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { type Extension, RangeSetBuilder, StateEffect, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, GutterMarker, gutter } from '@codemirror/view'

import { type LineRange } from './dslJsonPointers'
import { type GutterStatus } from './sourceMappingModel'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { apexDslSemanticHighlighting } from './apexDslSemanticHighlighting'
import { routeLinkedCodePaneWheel } from './linkedCodePaneWheel'

// Effect + field that hold the "linked" line range highlighted from the other
// pane. Updated imperatively (see the effect below) so changing the selection
// never reconfigures the whole editor.
const setLinkedRange = StateEffect.define<LineRange | null>()
const setFilterDecorations = StateEffect.define<DecorationSet>()

function buildLineDecorations(state: EditorView['state'], range: LineRange | null): DecorationSet {
  if (!range) return Decoration.none
  const builder = new RangeSetBuilder<Decoration>()
  const lineDeco = Decoration.line({ class: 'apex-linked-line' })
  const lastLine = state.doc.lines
  const start = Math.max(1, range.startLine)
  const end = Math.min(lastLine, range.endLine)
  for (let lineNo = start; lineNo <= end; lineNo += 1) {
    const line = state.doc.line(lineNo)
    builder.add(line.from, line.from, lineDeco)
  }
  return builder.finish()
}

const linkedRangeField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setLinkedRange)) {
        next = buildLineDecorations(tr.state, effect.value)
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})

const filterDecorationsField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, tr) {
    let next = decorations.map(tr.changes)
    for (const effect of tr.effects) {
      if (effect.is(setFilterDecorations)) {
        next = effect.value
      }
    }
    return next
  },
  provide: field => EditorView.decorations.from(field),
})


class StatusMarker extends GutterMarker {
  constructor(readonly status: GutterStatus) {
    super()
  }

  eq(other: StatusMarker) {
    return other.status === this.status
  }

  toDOM() {
    const span = document.createElement('span')
    span.className = `apex-status-marker ${this.status}`
    return span
  }
}

function statusGutter(statuses: Map<number, GutterStatus>): Extension {
  return gutter({
    class: 'apex-status-gutter',
    lineMarker(view, lineBlock) {
      const lineNo = view.state.doc.lineAt(lineBlock.from).number
      const status = statuses.get(lineNo)
      return status ? new StatusMarker(status) : null
    },
    lineMarkerChange: () => false,
    initialSpacer: () => new StatusMarker('converted'),
  })
}

function languageExtension(language?: string): Extension[] {
  switch ((language || '').toLowerCase()) {
    case 'json':
      return [json()]
    case 'c':
    case 'cpp':
    case 'h':
      return [cpp()]
    case 'javascript':
    case 'js':
      return [javascript()]
    case 'xml':
      return [xml()]
    default:
      return []
  }
}

interface Props {
  value: string
  language?: string
  semanticMode?: 'apex-dsl'
  gutterStatuses?: Map<number, GutterStatus>
  activeRange?: LineRange | null
  searchQuery?: string
  onLineClick?: (line: number) => void
  ariaLabel?: string
  maxHeight?: string
}

export default function LinkedCodePane({
  value,
  language,
  semanticMode,
  gutterStatuses,
  activeRange,
  searchQuery,
  onLineClick,
  ariaLabel,
  maxHeight = '420px',
}: Props) {
  const viewRef = useRef<EditorView | null>(null)
  const clickRef = useRef(onLineClick)
  clickRef.current = onLineClick
  // Suppress the scroll on the first highlight after the editor mounts: the
  // initial mapping is auto-selected when the review opens, and scrolling there
  // would drag the whole page down to this pane (away from the Save button).
  // Subsequent selection changes are user-driven, so scrolling is wanted.
  const hasAppliedRangeRef = useRef(false)

  const extensions = useMemo<Extension[]>(() => {
    const exts: Extension[] = [
      apexCodeMirrorTheme,
      linkedRangeField,
      filterDecorationsField,
      EditorView.lineWrapping,
      EditorView.editable.of(false),
      EditorView.domEventHandlers({
        wheel(event, view) {
          return routeLinkedCodePaneWheel(event, view.scrollDOM)
        },
        mousedown(event, view) {
          const pos = view.posAtCoords({ x: event.clientX, y: event.clientY })
          if (pos == null) return false
          const lineNo = view.state.doc.lineAt(pos).number
          clickRef.current?.(lineNo)
          return false
        },
      }),
      ...languageExtension(language),
    ]
    if (semanticMode === 'apex-dsl') {
      exts.push(apexDslSemanticHighlighting())
    }
    if (gutterStatuses && gutterStatuses.size > 0) {
      exts.push(statusGutter(gutterStatuses))
    }
    return exts
  }, [language, semanticMode, gutterStatuses])

  // Apply the linked-line highlight and scroll it into view whenever the active
  // range (or the underlying document) changes. Depend on the range's line
  // numbers rather than the object identity: callers rebuild the range object on
  // every render, so keying on the object would re-dispatch scrollIntoView on
  // unrelated re-renders (e.g. a 2s status poll), yanking the page back to the
  // highlighted line.
  const activeStartLine = activeRange?.startLine ?? null
  const activeEndLine = activeRange?.endLine ?? null
  const prevRangeRef = useRef({ start: activeStartLine, end: activeEndLine })
  const isSelectionChange = prevRangeRef.current.start !== activeStartLine || prevRangeRef.current.end !== activeEndLine

  useEffect(() => {
    prevRangeRef.current = { start: activeStartLine, end: activeEndLine }
  }, [activeStartLine, activeEndLine])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return

    const effects: StateEffect<unknown>[] = []

    // 1. Linked Line Range
    const isInitialRange = !hasAppliedRangeRef.current
    hasAppliedRangeRef.current = true
    const range = activeStartLine != null ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine } : null
    effects.push(setLinkedRange.of(range))
    
    if (!isInitialRange && range && range.startLine >= 1 && range.startLine <= view.state.doc.lines && isSelectionChange) {
      const line = view.state.doc.line(range.startLine)
      effects.push(EditorView.scrollIntoView(line.from, { y: 'center' }))
    }

    // 2. Search Query Highlighting
    if (!searchQuery || searchQuery.trim().length === 0) {
      effects.push(setFilterDecorations.of(Decoration.none))
    } else {
      const text = view.state.doc.toString()
      const lowerText = text.toLowerCase()
      const lowerQuery = searchQuery.toLowerCase()
      const builder = new RangeSetBuilder<Decoration>()
      const deco = Decoration.mark({ class: 'apex-search-match' })
      
      let i = 0
      let lastI = -1
      while ((i = lowerText.indexOf(lowerQuery, i)) !== -1) {
        if (i > lastI) {
          builder.add(i, i + lowerQuery.length, deco)
          lastI = i
        }
        i += lowerQuery.length
      }
      effects.push(setFilterDecorations.of(builder.finish()))
    }

    view.dispatch({ effects })
  }, [activeStartLine, activeEndLine, searchQuery, value, isSelectionChange])

  return (
    <div className="linked-code-pane" role="region" aria-label={ariaLabel}>
      <CodeMirror
        value={value}
        extensions={extensions}
        editable={false}
        height={maxHeight}
        maxHeight={maxHeight}
        readOnly
        theme="none"
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: false,
          highlightActiveLineGutter: false,
          dropCursor: false,
          allowMultipleSelections: false,
          highlightSelectionMatches: false,
        }}
        onCreateEditor={view => {
          viewRef.current = view
        }}
      />
    </div>
  )
}
