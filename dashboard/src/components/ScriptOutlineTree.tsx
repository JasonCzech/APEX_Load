import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { defaultRangeExtractor, useVirtualizer } from '@tanstack/react-virtual'

import { debugSequenceKey, type DebugRailRenderRow, type ScriptPhaseActionRow } from './scriptEditorWorkbench'
import { fitOutlineName, outlineStartsCollapsed, splitMeasureName, visibleOutlineRows, type OutlineMeasure, type OutlineModel, type OutlineRow } from './scriptOutlineModel'
import './ux/TooltipButton.css'

const ROW_HEIGHT = 28
/** Longer outlines render only the rows in view. */
const VIRTUALIZE_ABOVE = 120
const INDENT = 12

interface ExpansionState {
  /** Groups follow the base unless flipped; phases are open unless collapsed. */
  base: 'expanded' | 'collapsed'
  flipped: ReadonlySet<string>
  collapsedPhases: ReadonlySet<string>
  /** Whether the base was chosen from a script with steps (an empty draft decides nothing). */
  sized: boolean
}

function freshExpansion(model: OutlineModel): ExpansionState {
  return { base: outlineStartsCollapsed(model) ? 'collapsed' : 'expanded', flipped: new Set(), collapsedPhases: new Set(), sized: model.stepCount > 0 }
}

function isPhaseKey(key: string) {
  return key.startsWith('phase:')
}

function expandedIn(state: ExpansionState, key: string) {
  return isPhaseKey(key) ? !state.collapsedPhases.has(key) : (state.base === 'expanded') !== state.flipped.has(key)
}

function toggled(state: ExpansionState, key: string, open?: boolean): ExpansionState {
  if (open !== undefined && expandedIn(state, key) === open) return state
  if (isPhaseKey(key)) {
    const collapsedPhases = new Set(state.collapsedPhases)
    if (!collapsedPhases.delete(key)) collapsedPhases.add(key)
    return { ...state, collapsedPhases }
  }
  const flipped = new Set(state.flipped)
  if (!flipped.delete(key)) flipped.add(key)
  return { ...state, flipped }
}

type FontKind = 'phase' | 'group' | 'step' | 'small'
let measureContext: CanvasRenderingContext2D | null | undefined

/**
 * Text width in the outline's fonts (as ScriptEditor.css sets them per row
 * kind), or an estimate where there is no canvas (tests).
 */
function textMeasure(tree: HTMLElement | null): (kind: FontKind, text: string) => number {
  if (tree && measureContext === undefined && typeof navigator !== 'undefined' && !/jsdom/i.test(navigator.userAgent)) {
    measureContext = document.createElement('canvas').getContext('2d')
  }
  if (tree && measureContext) {
    const context = measureContext
    const style = getComputedStyle(tree)
    const fonts: Record<FontKind, string> = {
      step: `${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`,
      group: `${style.fontStyle} 650 ${style.fontSize} ${style.fontFamily}`,
      phase: `${style.fontStyle} 700 11px ${style.fontFamily}`,
      small: `${style.fontStyle} 400 11px ${style.fontFamily}`,
    }
    return (kind, text) => { context.font = fonts[kind]; return context.measureText(text).width }
  }
  return (kind, text) => text.length * (kind === 'phase' || kind === 'small' ? 6 : 7)
}

export interface ScriptOutlineSelection {
  phase: string
  actionIndex: number
  stepPath?: string[]
}

interface Props {
  model: OutlineModel
  /** Changes when another script opens, so expansion starts over. */
  resetKey: string
  query: string
  expandedContainers: Set<string>
  onToggleContainer: (key: string) => void
  selection: ScriptOutlineSelection | null
  onSelect: (item: DebugRailRenderRow) => void
  hasBreakpoint: (key: string) => boolean
  hasIssue: (row: ScriptPhaseActionRow) => boolean
  showMarkers: boolean
  onShowMarkersChange: (show: boolean) => void
  /** Set to a function giving the width every row's whole name needs, for the column's "fit to content". */
  naturalWidthRef?: { current: (() => number) | null }
}

function selectsSame(item: DebugRailRenderRow | undefined, selection: ScriptOutlineSelection) {
  if (!item || item.row.phase !== selection.phase || item.row.actionIndex !== selection.actionIndex) return false
  const path = 'stepPath' in item ? item.stepPath || [] : []
  return path.join('/') === (selection.stepPath || []).join('/')
}

/**
 * The Author outline: phases, transactions and steps as a keyboard-navigable
 * tree. A long outline renders only the rows in view, and names are cut at the
 * end on a word boundary to fit, never inside an identifier.
 */
export default function ScriptOutlineTree({ model, resetKey, query, expandedContainers, onToggleContainer, selection, onSelect, hasBreakpoint, hasIssue, showMarkers, onShowMarkersChange, naturalWidthRef }: Props) {
  const id = useId()
  const treeRef = useRef<HTMLDivElement>(null)
  const [expansion, setExpansion] = useState(() => ({ key: resetKey, state: freshExpansion(model) }))
  // Another script, or the first content of an empty draft, starts over.
  if (expansion.key !== resetKey || (!expansion.state.sized && model.stepCount > 0)) {
    setExpansion({ key: resetKey, state: freshExpansion(model) })
  }
  const state = expansion.key === resetKey ? expansion.state : freshExpansion(model)
  const update = (change: (current: ExpansionState) => ExpansionState) => setExpansion(previous => ({ key: previous.key, state: change(previous.state) }))

  // Selecting a step inside a closed group (from Problems, the debugger or the
  // action list) opens the groups above it.
  // Only a new selection opens groups; an edit to the script does not re-open
  // one the reader closed.
  const selectedActionKey = selection ? debugSequenceKey(selection.phase, selection.actionIndex) : ''
  const modelRef = useRef(model)
  modelRef.current = model
  useEffect(() => {
    if (!selectedActionKey) return
    const rowKey = modelRef.current.rowKeyByAction.get(selectedActionKey)
    const ancestors = rowKey ? modelRef.current.ancestorsByKey.get(rowKey) || [] : []
    if (!ancestors.length) return
    setExpansion(previous => {
      let next = previous.state
      for (const key of ancestors) next = toggled(next, key, true)
      return next === previous.state ? previous : { key: previous.key, state: next }
    })
  }, [selectedActionKey])

  const rows = useMemo(() => visibleOutlineRows(model, {
    isExpanded: key => expandedIn(state, key),
    expandedContainers,
    query,
  }), [expandedContainers, model, query, state])

  const selectedKey = useMemo(() => {
    if (!selection) return null
    if (selection.stepPath?.length) return rows.find(row => row.kind === 'nested' && selectsSame(row.select, selection))?.key ?? null
    return model.rowKeyByAction.get(debugSequenceKey(selection.phase, selection.actionIndex)) ?? null
  }, [model, rows, selection])

  const [focusKey, setFocusKey] = useState<string | null>(null)
  const tabStop = rows.some(row => row.key === focusKey) ? focusKey : rows.some(row => row.key === selectedKey) ? selectedKey : rows[0]?.key
  const pendingFocus = useRef<string | null>(null)

  const virtual = rows.length > VIRTUALIZE_ABOVE
  const tabStopIndex = rows.findIndex(row => row.key === tabStop)
  const virtualizer = useVirtualizer({
    count: virtual ? rows.length : 0,
    getScrollElement: () => treeRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    // The row in the tab order stays rendered when it scrolls away, so Tab can reach the tree.
    rangeExtractor: range => {
      const indexes = defaultRangeExtractor(range)
      return tabStopIndex >= 0 && !indexes.includes(tabStopIndex) ? [...indexes, tabStopIndex].sort((a, b) => a - b) : indexes
    },
  })

  // A selection made elsewhere scrolls its row into view, once the groups
  // above it have opened (which may take a render); expanding rows above an
  // earlier selection does not scroll.
  const selectedIndex = selectedKey ? rows.findIndex(row => row.key === selectedKey) : -1
  const scrollPending = useRef<string | null>(null)
  useEffect(() => { scrollPending.current = selectedKey }, [selectedKey])
  useEffect(() => {
    if (!scrollPending.current || scrollPending.current !== selectedKey || selectedIndex < 0) return
    scrollPending.current = null
    if (virtual) virtualizer.scrollToIndex(selectedIndex, { align: 'auto' })
    else treeRef.current?.querySelector(`[data-row-index="${selectedIndex}"]`)?.scrollIntoView?.({ block: 'nearest' })
  })

  // Keyboard moves land focus once the row is rendered.
  useEffect(() => {
    const key = pendingFocus.current
    if (!key) return
    let frames = 0
    const attempt = () => {
      const index = rows.findIndex(row => row.key === key)
      const element = treeRef.current?.querySelector<HTMLElement>(`[data-row-index="${index}"]`)
      if (element) { pendingFocus.current = null; element.focus({ preventScroll: true }); element.scrollIntoView?.({ block: 'nearest' }); return }
      if (frames++ < 5) requestAnimationFrame(attempt)
    }
    attempt()
  })

  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const element = treeRef.current
    if (!element) return undefined
    // Read now, so the first paint already fits; then follow the column's resizing.
    const style = getComputedStyle(element)
    setWidth(Math.floor(element.clientWidth - (parseFloat(style.paddingLeft) || 0) - (parseFloat(style.paddingRight) || 0)))
    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver(entries => setWidth(Math.floor(entries[0]?.contentRect.width || 0)))
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  const measure = useMemo(() => textMeasure(width > 0 ? treeRef.current : null), [width])

  const toggleRow = (row: OutlineRow, open?: boolean) => {
    if (!row.expandable || (open !== undefined && row.expanded === open)) return
    // Opening or closing can switch the list to (or from) rendering only the rows
    // in view, which remounts the rows: focus returns to this one.
    if (treeRef.current?.contains(document.activeElement)) pendingFocus.current = row.key
    if (row.kind === 'step' || row.kind === 'nested') onToggleContainer(row.key)
    else update(current => toggled(current, row.key))
  }
  const activate = (row: OutlineRow) => {
    if (row.select) onSelect(row.select)
    else toggleRow(row)
  }
  const moveTo = (index: number) => {
    const target = rows[Math.max(0, Math.min(rows.length - 1, index))]
    if (!target) return
    setFocusKey(target.key)
    pendingFocus.current = target.key
    if (virtual) virtualizer.scrollToIndex(rows.indexOf(target), { align: 'auto' })
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const index = rows.findIndex(row => row.key === event.currentTarget.getAttribute('data-row-key'))
    const row = rows[index]
    if (!row) return
    const handled = (() => {
      switch (event.key) {
        case 'ArrowDown': moveTo(index + 1); return true
        case 'ArrowUp': moveTo(index - 1); return true
        case 'Home': moveTo(0); return true
        case 'End': moveTo(rows.length - 1); return true
        case 'ArrowRight':
          if (row.expandable && !row.expanded) toggleRow(row, true)
          else if (row.expanded) moveTo(index + 1)
          return true
        case 'ArrowLeft':
          if (row.expandable && row.expanded) toggleRow(row, false)
          else if (row.parentKey) moveTo(rows.findIndex(candidate => candidate.key === row.parentKey))
          return true
        case 'Enter':
        case ' ':
          activate(row)
          return true
        default:
          return false
      }
    })()
    if (handled) event.preventDefault()
  }

  // The full name, for a row whose label is shorter: on hover and on focus.
  const [tip, setTip] = useState<{ key: string; text: string; left: number; top: number; focused: boolean } | null>(null)
  const showTip = (row: OutlineRow, shown: string, element: HTMLElement, focused: boolean) => {
    const text = row.title && row.title !== shown ? row.title : ''
    if (!text) { setTip(null); return }
    const box = element.getBoundingClientRect()
    setTip({ key: row.key, text, left: Math.max(8, box.left + 24), top: box.bottom + 4, focused })
  }
  useEffect(() => {
    if (!tip) return undefined
    const tree = treeRef.current
    const dismiss = (event: globalThis.KeyboardEvent) => { if (event.key === 'Escape') setTip(null) }
    // A hover tip goes when the list scrolls; a focused row's tip follows its row.
    const onScroll = () => {
      const element = tip.focused ? tree?.querySelector<HTMLElement>(`[data-row-key="${CSS.escape(tip.key)}"]`) : null
      if (!element) { setTip(null); return }
      const box = element.getBoundingClientRect()
      setTip(current => current && { ...current, left: Math.max(8, box.left + 24), top: box.bottom + 4 })
    }
    document.addEventListener('keydown', dismiss)
    tree?.addEventListener('scroll', onScroll)
    return () => { document.removeEventListener('keydown', dismiss); tree?.removeEventListener('scroll', onScroll) }
  }, [tip])

  const rowFlags = (row: OutlineRow) => ({
    breakpoint: row.kind === 'phase' ? false : row.rows.some(item => hasBreakpoint(debugSequenceKey(item.phase, item.actionIndex))) || (row.kind === 'nested' && hasBreakpoint(row.key)),
    issue: row.kind !== 'phase' && row.rows.some(hasIssue),
  })
  // A row's width besides its name: indent, toggle, occurrence, badge, flags, the
  // 6px gaps between them and its end padding (as ScriptEditor.css lays it out),
  // and a margin for the font's character variants, which canvas ignores.
  const rowChrome = (row: OutlineRow, flags: { breakpoint: boolean; issue: boolean }) => (row.level - 1) * INDENT + 16 + 6 + 6 + 8
    + (row.occurrence ? measure('small', `#${row.occurrence}`) + 6 : 0)
    + (row.badge ? measure('small', row.badge) + 6 : 0)
    + (flags.breakpoint ? 13 : 0) + (flags.issue ? 13 : 0)
  const fontOf = (row: OutlineRow): FontKind => row.kind === 'phase' ? 'phase' : row.kind === 'transaction' || row.kind === 'subtransaction' ? 'group' : 'step'
  // The width every row's whole name needs, measured only when the column is fit to content.
  useEffect(() => {
    if (!naturalWidthRef) return undefined
    naturalWidthRef.current = () => width > 0 ? Math.ceil(rows.reduce((widest, row) => Math.max(widest, rowChrome(row, rowFlags(row)) + measure(fontOf(row), row.label)), 0)) : 0
    return () => { naturalWidthRef.current = null }
  })

  const renderRow = (row: OutlineRow, index: number, style?: CSSProperties) => {
    const indent = (row.level - 1) * INDENT
    const occurrence = row.occurrence ? `#${row.occurrence}` : ''
    const { breakpoint, issue } = rowFlags(row)
    const selected = row.key === selectedKey
    const group = row.kind === 'phase' || row.kind === 'transaction' || row.kind === 'subtransaction'
    const room = width - rowChrome(row, { breakpoint, issue })
    const shown = width > 0 ? fitOutlineName(row.label, room, text => measure(fontOf(row), text), row.short) : row.label
    // The name read out is the whole one, whatever fits on screen.
    const spoken = [row.label, occurrence, row.badge, breakpoint ? 'Breakpoint' : '', issue ? 'Needs attention' : ''].filter(Boolean).join(' ')
    return (
      <div
        key={row.key}
        role="treeitem"
        id={`${id}-${index}`}
        data-row-key={row.key}
        data-row-index={index}
        data-outline-select={row.select ? '' : undefined}
        data-tooltip={row.title}
        aria-label={spoken}
        aria-level={row.level}
        aria-posinset={row.posInSet}
        aria-setsize={row.setSize}
        aria-expanded={row.expandable ? row.expanded : undefined}
        aria-selected={row.select ? selected : undefined}
        aria-describedby={tip?.key === row.key ? `${id}-tip` : undefined}
        tabIndex={row.key === tabStop ? 0 : -1}
        className={`script-outline-row kind-${row.kind}${group ? ' group' : ''}${selected ? ' selected' : ''}${row.measuredAs ? ' measured' : ''}`}
        style={{ ...style, paddingLeft: indent, ['--outline-level' as string]: row.level }}
        onClick={event => {
          setFocusKey(row.key)
          // The chevron opens or closes; the rest of the row selects.
          if (row.expandable && (event.target as HTMLElement).closest('.script-outline-toggle')) toggleRow(row)
          else activate(row)
        }}
        onKeyDown={onKeyDown}
        onFocus={event => { if (event.target === event.currentTarget) { setFocusKey(row.key); showTip(row, shown, event.currentTarget, true) } }}
        onBlur={() => setTip(null)}
        onMouseEnter={event => showTip(row, shown, event.currentTarget, false)}
        onMouseLeave={() => setTip(null)}
      >
        <span className="script-outline-toggle" aria-hidden="true">{row.expandable ? (row.expanded ? '▾' : '▸') : ''}</span>
        <span className="script-outline-name">{shown}</span>
        {occurrence && <span className="script-outline-occurrence">{occurrence}</span>}
        {row.badge && <small>{row.badge}</small>}
        {breakpoint && <span className="script-outline-flag breakpoint" aria-hidden="true" />}
        {issue && <span className="script-outline-flag attention" aria-hidden="true" />}
      </div>
    )
  }

  const groupCount = model.groupsByKey.size
  return (
    <>
      <div className="script-outline-tools">
        <button type="button" className="btn btn-ghost btn-sm" disabled={!groupCount} onClick={() => update(current => ({ ...current, base: 'expanded', flipped: new Set(), collapsedPhases: new Set() }))}>Expand all</button>
        <button type="button" className="btn btn-ghost btn-sm" disabled={!groupCount} onClick={() => update(current => ({ ...current, base: 'collapsed', flipped: new Set() }))}>Collapse all</button>
        <label className="script-outline-markers">
          <input type="checkbox" checked={showMarkers} onChange={event => onShowMarkersChange(event.target.checked)} />
          <span>Show markers</span>
        </label>
      </div>
      {/* A tree with no steps to own is not a tree (a search that matches nothing). */}
      <div ref={treeRef} className="script-outline-treeview" role={rows.length ? 'tree' : undefined} aria-label={rows.length ? 'Script steps' : undefined}>
        {virtual ? (
          <div className="script-outline-virtual" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map(item => renderRow(rows[item.index], item.index, { position: 'absolute', top: 0, left: 0, right: 0, height: ROW_HEIGHT, transform: `translateY(${item.start}px)` }))}
          </div>
        ) : rows.map((row, index) => renderRow(row, index))}
        {!rows.length && <p className="script-outline-empty">{query.trim() ? 'No matching steps' : 'No actions'}</p>}
      </div>
      {tip && createPortal(<div id={`${id}-tip`} role="tooltip" className="ux-tooltip" style={{ left: tip.left, top: tip.top }}>{tip.text}</div>, document.body)}
    </>
  )
}

/**
 * A folded step's sub-transaction, in the inspector: what measures the step,
 * with Rename and Remove. Its markers stay in the script; the outline shows
 * them on the step.
 */
export function OutlineMeasureCard({ measure, label, onRename, onRemove }: {
  measure: OutlineMeasure
  /** The step's name as the outline shows it. */
  label: string
  onRename: (fullName: string) => void
  onRemove: () => void
}) {
  const id = useId()
  const { parent, child } = splitMeasureName(measure.name)
  const [draft, setDraft] = useState({ name: measure.name, value: child })
  if (draft.name !== measure.name) setDraft({ name: measure.name, value: child })
  const clean = draft.value.trim()
  const problem = !clean ? 'Enter a name.' : clean.includes('||') ? 'A name cannot contain “||”.' : ''
  return (
    <div className="utility-card outline-measure-card">
      <h3>Measured as a sub-transaction</h3>
      <p>{label}{label !== child && <> <code>{child}</code></>}{parent && <> in transaction <code>{parent}</code></>}. Its start and end markers stay in the script.</p>
      <form onSubmit={event => { event.preventDefault(); if (!problem && clean !== child) onRename(parent ? `${parent}||${clean}` : clean) }}>
        <label>
          <span>Sub-transaction name</span>
          <input value={draft.value} aria-invalid={Boolean(problem)} aria-describedby={problem ? `${id}-problem` : undefined} onChange={event => setDraft({ name: measure.name, value: event.target.value })} />
        </label>
        {problem && <small id={`${id}-problem`} role="alert">{problem}</small>}
        <div className="outline-measure-actions">
          <button type="submit" className="btn btn-secondary btn-sm" disabled={Boolean(problem) || clean === child}>Rename</button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onRemove}>Remove sub-transaction</button>
        </div>
      </form>
    </div>
  )
}
