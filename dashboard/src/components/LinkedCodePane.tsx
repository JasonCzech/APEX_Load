import { useEffect, useMemo, useRef, useState } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { cpp } from '@codemirror/lang-cpp'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { type Extension, RangeSetBuilder, StateEffect, StateField } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, GutterMarker, gutter, lineNumbers } from '@codemirror/view'

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

// Scroll a target into view inside this pane's own scroller and nowhere else.
// CodeMirror's default handler also scrolls every scrollable ancestor, which
// dragged the import review page down to the pane. CodeMirror calls this only
// after it has rendered and measured the target's line, so the position holds
// even far down a long, wrapped document, where a position read before the
// scroll lands on an estimate.
const scrollWithinPane = EditorView.scrollHandler.of((view, range, { y, yMargin, xMargin }) => {
  const scroller = view.scrollDOM
  // Sideways first (an unwrapped long line): bring the position past the
  // sticky gutters, as CodeMirror's own "nearest" would.
  const caret = view.coordsAtPos(range.head)
  if (caret) {
    const box = scroller.getBoundingClientRect()
    const textLeft = view.dom.querySelector('.cm-gutters')?.getBoundingClientRect().right ?? box.left
    if (caret.left < textLeft + xMargin) scroller.scrollLeft -= textLeft + xMargin - caret.left
    else if (caret.right > box.right - xMargin) scroller.scrollLeft += caret.right - (box.right - xMargin)
  }
  const block = view.lineBlockAt(range.head)
  const height = scroller.clientHeight
  const offset = view.documentTop - scroller.getBoundingClientRect().top + scroller.scrollTop
  const top = block.top + offset
  const bottom = block.bottom + offset
  let next = scroller.scrollTop
  if (y === 'center' && block.height < height) next = top - (height - block.height) / 2
  else if (y === 'end') next = bottom - height + yMargin
  else if (y !== 'nearest' || top < next) next = top - yMargin
  else if (bottom > next + height) next = Math.min(top - yMargin, bottom - height + yMargin)
  scroller.scrollTop = Math.max(0, next)
  return true
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
  contextLines?: number
  revealRequestId?: number
  revealOnMount?: boolean
  revealMode?: 'auto' | 'request'
  value: string
  language?: string
  semanticMode?: 'apex-dsl'
  gutterStatuses?: Map<number, GutterStatus>
  activeRange?: LineRange | null
  searchQuery?: string
  onLineClick?: (line: number) => void
  ariaLabel?: string
  maxHeight?: string
  wrapLines?: boolean
}

export default function LinkedCodePane({
  value: fullValue,
  contextLines, revealRequestId = 0, revealOnMount = false, revealMode = 'auto',
  language,
  semanticMode,
  gutterStatuses: fullStatuses,
  activeRange: fullRange,
  searchQuery,
  onLineClick,
  ariaLabel,
  maxHeight = '420px',
  wrapLines = true,
}: Props) {
  const offset = contextLines != null && fullRange ? Math.max(0, fullRange.startLine - contextLines - 1) : 0
  const value = contextLines != null && fullRange ? fullValue.split('\n').slice(offset, fullRange.endLine + contextLines).join('\n') : fullValue
  const activeRange = fullRange ? { startLine: fullRange.startLine - offset, endLine: fullRange.endLine - offset } : null
  const gutterStatuses = useMemo(() => fullStatuses ? new Map([...fullStatuses].map(([line, status]) => [line - offset, status])) : undefined, [fullStatuses, offset])
  const offsetRef = useRef(offset)
  offsetRef.current = offset
  // The view lives in state, tagged with the editor key it was created under.
  // react-codemirror creates its view a render after mounting, so an effect
  // that read a ref on mount ran before the view existed and never ran again:
  // a newly mounted pane (a destination appearing, every selection in mapped
  // context) neither highlighted nor revealed the linked range.
  const editorKey = contextLines == null ? 'full' : `context-${offset}`
  const [editor, setEditor] = useState<{ key: string; view: EditorView } | null>(null)
  const view = editor?.key === editorKey ? editor.view : null
  const viewRef = useRef(view)
  viewRef.current = view
  const containerRef = useRef<HTMLDivElement>(null)
  const pendingRevealRef = useRef<{ view: EditorView; line: number } | null>(null)
  useEffect(() => {
    const element = containerRef.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const flushPendingReveal = () => {
      const currentView = viewRef.current
      const pending = pendingRevealRef.current
      if (!currentView || !pending || pending.view !== currentView || currentView.scrollDOM.clientHeight <= 0) return
      pendingRevealRef.current = null
      if (pending.line >= 1 && pending.line <= currentView.state.doc.lines) {
        currentView.dispatch({
          effects: [EditorView.scrollIntoView(currentView.state.doc.line(pending.line).from, { y: 'center' })],
        })
      }
    }
    const observer = new ResizeObserver(() => {
      viewRef.current?.requestMeasure()
      flushPendingReveal()
    })
    observer.observe(element)
    const visibility = typeof IntersectionObserver === 'undefined'
      ? null
      : new IntersectionObserver(entries => {
          if (entries.some(entry => entry.isIntersecting)) {
            viewRef.current?.requestMeasure()
            flushPendingReveal()
          }
        })
    visibility?.observe(element)
    return () => { observer.disconnect(); visibility?.disconnect() }
  }, [])
  const clickRef = useRef(onLineClick)
  clickRef.current = onLineClick
  // The range (and reveal request) this pane last scrolled to, so a re-render
  // that changes nothing about the selection never scrolls it again.
  const revealedRef = useRef<{ view: EditorView; key: string } | null>(null)

  const extensions = useMemo<Extension[]>(() => {
    const exts: Extension[] = [
      apexCodeMirrorTheme,
      lineNumbers({ formatNumber: line => String(line + offset) }),
      linkedRangeField,
      filterDecorationsField,
      scrollWithinPane,
      ...(wrapLines ? [EditorView.lineWrapping] : []),
      EditorView.editable.of(false),
      EditorView.contentAttributes.of({ 'aria-label': ariaLabel || 'Read-only code' }),
      EditorView.domEventHandlers({
        wheel(event, view) {
          return routeLinkedCodePaneWheel(event, view.scrollDOM)
        },
        mousedown(event, view) {
          const pos = view.posAtCoords({ x: event.clientX, y: event.clientY })
          if (pos == null) return false
          const lineNo = view.state.doc.lineAt(pos).number
          clickRef.current?.(lineNo + offsetRef.current)
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
  }, [language, semanticMode, gutterStatuses, wrapLines, offset, ariaLabel])

  // Apply the linked-line highlight and scroll it into view whenever the active
  // range (or the underlying document) changes. Depend on the range's line
  // numbers rather than the object identity: callers rebuild the range object on
  // every render, so keying on the object would re-dispatch scrollIntoView on
  // unrelated re-renders (e.g. a 2s status poll), yanking the pane back to the
  // highlighted line.
  const activeStartLine = activeRange?.startLine ?? null
  const activeEndLine = activeRange?.endLine ?? null

  useEffect(() => {
    if (!view) return

    const effects: StateEffect<unknown>[] = []

    // 1. Linked Line Range
    const range = activeStartLine != null ? { startLine: activeStartLine, endLine: activeEndLine ?? activeStartLine } : null
    effects.push(setLinkedRange.of(range))

    // With revealOnMount, a new view, range (in 'auto' mode) or reveal request
    // scrolls. In 'request' mode, changing activeRange alone only updates the
    // linked-line highlight so selecting a line inside a pane keeps that pane's
    // scroll position steady while the opposite pane reveals its counterpart.
    const revealKey = range
      ? revealMode === 'request'
        ? String(revealRequestId)
        : `${range.startLine}:${range.endLine}${revealOnMount ? `:${revealRequestId}` : ''}`
      : ''
    const revealed = revealedRef.current
    revealedRef.current = { view, key: revealKey }
    const reveal = revealOnMount
      ? revealed?.view !== view || revealed.key !== revealKey
      : revealed !== null && revealed.key !== revealKey
    if (range && reveal && range.startLine >= 1 && range.startLine <= view.state.doc.lines) {
      if (view.scrollDOM.clientHeight > 0) {
        pendingRevealRef.current = null
        effects.push(EditorView.scrollIntoView(view.state.doc.line(range.startLine).from, { y: 'center' }))
      } else {
        pendingRevealRef.current = { view, line: range.startLine }
      }
    } else if (!range) {
      pendingRevealRef.current = null
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
  }, [view, activeStartLine, activeEndLine, searchQuery, value, revealRequestId, revealOnMount, revealMode])

  return (
    <div ref={containerRef} className="linked-code-pane" role="region" aria-label={ariaLabel}>
      <CodeMirror
        key={editorKey}
        value={value}
        extensions={extensions}
        editable={false}
        height={maxHeight}
        maxHeight={maxHeight}
        readOnly
        theme="none"
        basicSetup={{
          lineNumbers: false,
          foldGutter: false,
          highlightActiveLine: false,
          highlightActiveLineGutter: false,
          dropCursor: false,
          allowMultipleSelections: false,
          highlightSelectionMatches: false,
        }}
        onCreateEditor={created => setEditor({ key: editorKey, view: created })}
      />
    </div>
  )
}
