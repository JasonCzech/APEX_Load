import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { defaultRangeExtractor, useVirtualizer } from '@tanstack/react-virtual'

import { isMacLike } from '../hooks/KeyboardShortcutProvider'
import { debugSequenceKey, type DebugRailRenderRow, type ScriptPhaseActionRow } from './scriptEditorWorkbench'
import { carryGroupKeys, outlineNodeRows, outlineRowSelectable, outlineStartsCollapsed, splitMeasureName, visibleOutlineRows, type OutlineMeasure, type OutlineModel, type OutlineRow } from './scriptOutlineModel'
import TooltipButton from './ux/TooltipButton'
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

export type OutlineRunStatus = 'pass' | 'fail' | 'current' | 'not-run'

export interface OutlineBreakpoint {
  /** Set, set but disabled, set only on steps inside the block, or not set. */
  state: 'active' | 'disabled' | 'partial' | 'none'
  /** What clicking the dot does, for its tooltip and accessible name. */
  title: string
}

/**
 * What the Debug workspace adds to the outline: a breakpoint dot in each
 * breakable row's gutter (click, double-click the row or F9 to toggle) and each
 * row's run status.
 */
export interface OutlineDebug {
  /** Null for rows that take no breakpoint (phases, transaction groups, nested blocks). */
  breakpoint: (row: OutlineRow) => OutlineBreakpoint | null
  onToggleBreakpoint: (row: OutlineRow) => void
  status: (row: OutlineRow) => OutlineRunStatus | null
}

/**
 * What the Author outline adds for acting on several rows: Cmd-click (Ctrl-click
 * off macOS) adds or removes a row, Shift-click and Shift+arrows select a
 * range, Cmd/Ctrl+A the rows at one level and Escape clears. A right-click, the
 * context-menu key or Shift+F10 opens the row menu.
 */
export interface OutlineMultiSelect {
  /** The selected rows' keys; empty when only the editor's own selection applies. */
  keys: ReadonlySet<string>
  onChange: (keys: string[]) => void
  /** `keys` are the rows the menu acts on (the selection, or the row alone); `point` is in viewport pixels. */
  onContextMenu: (row: OutlineRow, point: { x: number; y: number }, keys: string[]) => void
  /** The selection bar's "Wrap in transaction…" (`point`: where its dialog opens), with why it is unavailable (or ''). */
  onWrap: (point: { x: number; y: number }) => void
  wrapUnavailable: string
}

const STATUS_TEXT: Record<OutlineRunStatus, string> = { pass: 'Passed', fail: 'Failed', current: 'Current', 'not-run': 'Not run' }
const BREAKPOINT_TEXT: Record<OutlineBreakpoint['state'], string> = { active: 'Breakpoint', disabled: 'Breakpoint disabled', partial: 'Breakpoints inside', none: '' }

interface Props {
  model: OutlineModel
  /** Changes when another script opens, so expansion starts over. */
  resetKey: string
  query: string
  expandedContainers: Set<string>
  onToggleContainer: (key: string) => void
  selection: ScriptOutlineSelection | null
  /** `fromMenu`: chosen by opening the row menu on it, so focus stays with the menu. */
  onSelect: (item: DebugRailRenderRow, options?: { fromMenu?: boolean }) => void
  hasBreakpoint: (key: string) => boolean
  hasIssue: (row: ScriptPhaseActionRow) => boolean
  showMarkers: boolean
  onShowMarkersChange: (show: boolean) => void
  /** Set to a function giving the width every row's whole name needs, for the column's "fit to content". */
  naturalWidthRef?: { current: (() => number) | null }
  /** Debug only: breakpoint gutter and run status. */
  debug?: OutlineDebug
  /**
   * Author: F9 and Cmd/Ctrl+B toggle the breakpoint of the row with keyboard
   * focus, which arrow keys move without selecting it. Called only for a row
   * that takes a breakpoint (see outlineRowTakesBreakpoint).
   */
  onToggleRowBreakpoint?: (item: DebugRailRenderRow) => void
  /** Author only: selecting several rows, and the row menu. */
  multiSelect?: OutlineMultiSelect
  /**
   * Opens the group that starts at this step (a transaction just made: its
   * start marker, or a tagged run's first step), then calls onRevealed, which
   * clears the request.
   */
  revealGroup?: { phase: string; actionIndex: number } | null
  onRevealed?: () => void
}

/**
 * Whether F9 on an outline row sets a breakpoint, as the Debug gutter has
 * one: a step, transaction marker or request, never a phase, a transaction or
 * sub-transaction group, a nested block or Debug's "All requests" row.
 */
function outlineRowTakesBreakpoint(row: OutlineRow): boolean {
  const item = row.select
  return Boolean(item) && row.kind !== 'phase' && row.kind !== 'transaction' && row.kind !== 'subtransaction' && item!.kind !== 'container' && !row.allRequests
}

function selectsSame(item: DebugRailRenderRow | undefined, selection: ScriptOutlineSelection) {
  if (!item || item.row.phase !== selection.phase || item.row.actionIndex !== selection.actionIndex) return false
  const path = 'stepPath' in item ? item.stepPath || [] : []
  return path.join('/') === (selection.stepPath || []).join('/')
}

/**
 * The Author outline: phases, transactions and steps as a keyboard-navigable
 * tree. A long outline renders only the rows in view; Aura steps switch to
 * their method name when narrow, and overflowing names truncate via CSS ellipsis.
 */
export default function ScriptOutlineTree({ model, resetKey, query, expandedContainers, onToggleContainer, selection, onSelect, hasBreakpoint, hasIssue, showMarkers, onShowMarkersChange, naturalWidthRef, debug, onToggleRowBreakpoint, multiSelect: multiSelectProp, revealGroup, onRevealed }: Props) {
  // Debug keeps one selection and no row menu.
  const multiSelect = debug ? undefined : multiSelectProp
  const multiKeys = multiSelect?.keys
  const multiActive = Boolean(multiKeys && multiKeys.size > 0)
  const id = useId()
  const treeRef = useRef<HTMLDivElement>(null)
  const [expansion, setExpansion] = useState(() => ({ key: resetKey, state: freshExpansion(model) }))
  // Another script, or the first content of an empty draft, starts over.
  if (expansion.key !== resetKey || (!expansion.state.sized && model.stepCount > 0)) {
    setExpansion({ key: resetKey, state: freshExpansion(model) })
  }
  const state = expansion.key === resetKey ? expansion.state : freshExpansion(model)
  const update = (change: (current: ExpansionState) => ExpansionState) => setExpansion(previous => ({ key: previous.key, state: change(previous.state) }))

  // A group's key holds its name and occurrence: its open or closed state
  // follows it through a rename or another group's removal (carryGroupKeys).
  const previousModel = useRef(model)
  useEffect(() => {
    const before = previousModel.current
    previousModel.current = model
    if (before === model) return
    setExpansion(previous => {
      const moved = carryGroupKeys(before, model, previous.state.flipped)
      if (moved.length === previous.state.flipped.size && moved.every(key => previous.state.flipped.has(key))) return previous
      return { key: previous.key, state: { ...previous.state, flipped: new Set(moved) } }
    })
  }, [model])

  // A transaction just made opens, so what it wraps shows (a long script's
  // groups start closed).
  const onRevealedRef = useRef(onRevealed)
  onRevealedRef.current = onRevealed
  useEffect(() => {
    if (!revealGroup) return
    const rowKey = model.rowKeyByAction.get(debugSequenceKey(revealGroup.phase, revealGroup.actionIndex))
    const ancestors = rowKey ? model.ancestorsByKey.get(rowKey) || [] : []
    const innermost = ancestors[ancestors.length - 1]
    const run = innermost ? model.groupsByKey.get(innermost) : undefined
    const first = run && !run.start ? outlineNodeRows(run)[0] : undefined
    const startsRun = Boolean(first) && debugSequenceKey(first!.phase, first!.actionIndex) === rowKey
    const key = rowKey && model.groupsByKey.has(rowKey) ? rowKey : startsRun ? innermost : undefined
    if (key) {
      setExpansion(previous => {
        const next = toggled(previous.state, key, true)
        return next === previous.state ? previous : { key: previous.key, state: next }
      })
    }
    onRevealedRef.current?.()
  }, [model, revealGroup])

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

  const debugging = Boolean(debug)
  const rows = useMemo(() => visibleOutlineRows(model, {
    isExpanded: key => expandedIn(state, key),
    expandedContainers,
    query,
    allRequestsRows: debugging,
  }), [debugging, expandedContainers, model, query, state])

  const selectedKey = useMemo(() => {
    if (!selection) return null
    if (selection.stepPath?.length) return rows.find(row => row.kind === 'nested' && selectsSame(row.select, selection))?.key ?? null
    return model.rowKeyByAction.get(debugSequenceKey(selection.phase, selection.actionIndex)) ?? null
  }, [model, rows, selection])

  const [focusKey, setFocusKey] = useState<string | null>(null)
  const tabStop = rows.some(row => row.key === focusKey) ? focusKey : rows.some(row => row.key === selectedKey) ? selectedKey : rows[0]?.key
  const pendingFocus = useRef<string | null>(null)
  // Where a Shift-click or Shift+arrow range starts.
  const anchorKey = useRef<string | null>(null)
  // A selection made elsewhere (Problems, the action list) starts the next range.
  useEffect(() => { if (!multiActive) anchorKey.current = selectedKey }, [selectedKey]) // eslint-disable-line react-hooks/exhaustive-deps
  const isSelected = (row: OutlineRow) => multiActive ? multiKeys!.has(row.key) : row.key === selectedKey
  const rangeKeys = (fromKey: string | null, toIndex: number) => {
    const fromIndex = fromKey ? rows.findIndex(row => row.key === fromKey) : -1
    const start = fromIndex < 0 ? toIndex : Math.min(fromIndex, toIndex)
    const end = fromIndex < 0 ? toIndex : Math.max(fromIndex, toIndex)
    return rows.slice(start, end + 1).filter(outlineRowSelectable).map(row => row.key)
  }
  // The selection a Cmd/Ctrl-click adds to: the rows already chosen, or the editor's own row.
  const currentKeys = () => multiActive ? [...multiKeys!] : selectedKey && rows.some(row => row.key === selectedKey && outlineRowSelectable(row)) ? [selectedKey] : []
  const openMenu = (row: OutlineRow, point: { x: number; y: number }) => {
    if (!multiSelect || !outlineRowSelectable(row)) return
    if (isSelected(row) && multiActive) {
      multiSelect.onContextMenu(row, point, [...multiKeys!])
      return
    }
    // A row outside the selection is chosen on its own, as a click would.
    if (multiActive) multiSelect.onChange([])
    anchorKey.current = row.key
    if (row.select && row.key !== selectedKey) {
      menuSelectedKey.current = row.key
      onSelect(row.select, { fromMenu: true })
    }
    multiSelect.onContextMenu(row, point, [row.key])
  }
  const menuPointFor = (element: HTMLElement) => {
    const box = element.getBoundingClientRect()
    return { x: box.left + 24, y: box.bottom }
  }

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
  // A row chosen by opening its menu is in view already; scrolling would close the menu.
  const menuSelectedKey = useRef<string | null>(null)
  useEffect(() => {
    scrollPending.current = selectedKey === menuSelectedKey.current ? null : selectedKey
    menuSelectedKey.current = null
  }, [selectedKey])
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
  // The focused row's breakpoint, for F9 and Cmd/Ctrl+B: false when the row
  // takes none, so the key does nothing here.
  const toggleFocusedBreakpoint = (row: OutlineRow) => {
    if (debug) {
      if (!debug.breakpoint(row)) return false
      debug.onToggleBreakpoint(row)
      return true
    }
    if (!onToggleRowBreakpoint || !outlineRowTakesBreakpoint(row)) return false
    onToggleRowBreakpoint(row.select!)
    return true
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const index = rows.findIndex(row => row.key === event.currentTarget.getAttribute('data-row-key'))
    const row = rows[index]
    if (!row) return
    const command = isMacLike() ? event.metaKey : event.ctrlKey
    const extend = (to: number) => {
      const target = Math.max(0, Math.min(rows.length - 1, to))
      if (!anchorKey.current || !rows.some(candidate => candidate.key === anchorKey.current)) anchorKey.current = row.key
      moveTo(target)
      multiSelect!.onChange(rangeKeys(anchorKey.current, target))
    }
    const handled = (() => {
      switch (event.key) {
        case 'ArrowDown':
          if (event.shiftKey && multiSelect) { extend(index + 1); return true }
          moveTo(index + 1); return true
        case 'ArrowUp':
          if (event.shiftKey && multiSelect) { extend(index - 1); return true }
          moveTo(index - 1); return true
        case 'a':
        case 'A':
          if (!multiSelect || !command || event.altKey || event.shiftKey) return false
          multiSelect.onChange(rows.filter(candidate => candidate.parentKey === row.parentKey && outlineRowSelectable(candidate)).map(candidate => candidate.key))
          return true
        case 'Escape':
          if (!multiActive) return false
          multiSelect!.onChange([])
          return true
        case 'ContextMenu':
          if (!multiSelect) return false
          openMenu(row, menuPointFor(event.currentTarget))
          return true
        case 'F10':
          // F10 alone is the debugger's Step Over, which the editor handles.
          if (!multiSelect || !event.shiftKey) return false
          openMenu(row, menuPointFor(event.currentTarget))
          return true
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
          // As a plain click: this row alone.
          if (multiSelect) {
            anchorKey.current = row.key
            if (multiActive) multiSelect.onChange([])
          }
          activate(row)
          return true
        case 'b':
        case 'B':
          // Cmd/Ctrl+B is F9's twin: both toggle the focused row's breakpoint.
          if (!(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey) return false
          return toggleFocusedBreakpoint(row)
        case 'F9':
          return toggleFocusedBreakpoint(row)
        default:
          return false
      }
    })()
    if (handled) {
      event.preventDefault()
      // Escape cleared this tree's selection; it goes no further.
      if (event.key === 'Escape' || event.key === 'ContextMenu' || event.key === 'F10') event.stopPropagation()
    }
  }

  // The full name, when a row has a distinct title, uses its short method name, or CSS ellipsis clips its label: on hover and on focus.
  const [tip, setTip] = useState<{ key: string; text: string; left: number; top: number; focused: boolean } | null>(null)
  const showTip = (row: OutlineRow, shown: string, truncated: boolean, element: HTMLElement, focused: boolean) => {
    const nameEl = element.querySelector<HTMLElement>('.script-outline-name')
    const overflowed = nameEl && nameEl.clientWidth > 0 ? nameEl.scrollWidth > nameEl.clientWidth : truncated
    const text = shown !== row.label || (row.title && row.title !== shown)
      ? (row.title || row.label)
      : overflowed
        ? (row.title || row.label)
        : ''
    if (!text) { setTip(null); return }
    const box = element.getBoundingClientRect()
    setTip({ key: row.key, text, left: Math.max(8, box.left + 24), top: box.bottom + 4, focused })
  }
  // An edit (a wrap, an undo) can give a row another name: its tip goes.
  useEffect(() => { setTip(null) }, [model])
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

  const rowDebug = (row: OutlineRow) => debug ? {
    breakpoint: debug.breakpoint(row),
    status: debug.status(row),
  } : null
  const rowFlags = (row: OutlineRow, gutter: OutlineBreakpoint | null = null) => ({
    // A row with a breakpoint gutter shows its own breakpoint there; the flag
    // stays for groups, which hold the breakpoints of the steps inside them.
    breakpoint: row.kind === 'phase' || gutter ? false : row.rows.some(item => hasBreakpoint(debugSequenceKey(item.phase, item.actionIndex))) || (row.kind === 'nested' && hasBreakpoint(row.key)),
    issue: row.kind !== 'phase' && row.rows.some(hasIssue),
  })
  // A row's width besides its name: indent, toggle, occurrence, badge, flags, the
  // 6px gaps between them and its end padding (as ScriptEditor.css lays it out),
  // and a margin for the font's character variants, which canvas ignores.
  const rowChrome = (row: OutlineRow, flags: { breakpoint: boolean; issue: boolean }, status: OutlineRunStatus | null = null) => (row.level - 1) * INDENT + 16 + 6 + 6 + 8
    + (debug ? 16 + 6 : 0) + (status ? 8 + 6 : 0)
    + (row.occurrence ? measure('small', `#${row.occurrence}`) + 6 : 0)
    + (row.badge ? measure('small', row.badge) + 6 : 0)
    + (flags.breakpoint ? 13 : 0) + (flags.issue ? 13 : 0)
  const fontOf = (row: OutlineRow): FontKind => row.kind === 'phase' ? 'phase' : row.kind === 'transaction' || row.kind === 'subtransaction' ? 'group' : 'step'
  // The width every row's whole name needs, measured only when the column is fit to content.
  useEffect(() => {
    if (!naturalWidthRef) return undefined
    naturalWidthRef.current = () => width > 0 ? Math.ceil(rows.reduce((widest, row) => {
      const state = rowDebug(row)
      return Math.max(widest, rowChrome(row, rowFlags(row, state?.breakpoint), state?.status) + measure(fontOf(row), row.label))
    }, 0)) : 0
    return () => { naturalWidthRef.current = null }
  })

  const renderRow = (row: OutlineRow, index: number, style?: CSSProperties) => {
    const indent = (row.level - 1) * INDENT
    const occurrence = row.occurrence ? `#${row.occurrence}` : ''
    const state = rowDebug(row)
    const gutter = state?.breakpoint ?? null
    const status = state?.status ?? null
    const { breakpoint, issue } = rowFlags(row, gutter)
    const selected = isSelected(row)
    const multiSelected = multiActive && selected
    const group = row.kind === 'phase' || row.kind === 'transaction' || row.kind === 'subtransaction'
    const room = width - rowChrome(row, { breakpoint, issue }, status)
    const labelFits = !(width > 0) || measure(fontOf(row), row.label) <= room
    const shown = !labelFits && row.short ? row.short : row.label
    const truncated = !labelFits && (!row.short || measure(fontOf(row), row.short) > room)
    // The name read out is the whole one, whatever fits on screen.
    const spoken = [row.label, occurrence, row.badge, breakpoint ? 'Breakpoint' : '', gutter ? BREAKPOINT_TEXT[gutter.state] : '', status ? STATUS_TEXT[status] : '', issue ? 'Needs attention' : ''].filter(Boolean).join(' ')
    const toggleBreakpoint = () => { if (gutter) debug?.onToggleBreakpoint(row) }
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
        aria-selected={row.select || (multiSelect && outlineRowSelectable(row)) ? selected : undefined}
        aria-current={status === 'current' ? 'step' : undefined}
        aria-keyshortcuts={gutter ? 'F9' : undefined}
        aria-describedby={tip?.key === row.key ? `${id}-tip` : undefined}
        tabIndex={row.key === tabStop ? 0 : -1}
        className={`script-outline-row kind-${row.kind}${group ? ' group' : ''}${selected ? ' selected' : ''}${multiSelected ? ' multi-selected' : ''}${row.measuredAs ? ' measured' : ''}${status === 'current' ? ' current' : ''}`}
        style={{ ...style, paddingLeft: indent, ['--outline-level' as string]: row.level }}
        onClick={event => {
          setFocusKey(row.key)
          // The chevron opens or closes; the rest of the row selects.
          if (row.expandable && (event.target as HTMLElement).closest('.script-outline-toggle')) { toggleRow(row); return }
          if (multiSelect) {
            const mac = isMacLike()
            // On macOS Ctrl-click is the context menu (contextmenu handles it).
            if (mac && event.ctrlKey) return
            if (event.shiftKey && outlineRowSelectable(row)) {
              if (!anchorKey.current || !rows.some(candidate => candidate.key === anchorKey.current)) anchorKey.current = selectedKey
              multiSelect.onChange(rangeKeys(anchorKey.current, index))
              return
            }
            if ((mac ? event.metaKey : event.ctrlKey) && outlineRowSelectable(row)) {
              const keys = currentKeys()
              anchorKey.current = row.key
              multiSelect.onChange(keys.includes(row.key) ? keys.filter(key => key !== row.key) : [...keys, row.key])
              return
            }
            anchorKey.current = row.key
            if (multiActive) multiSelect.onChange([])
          }
          activate(row)
        }}
        onContextMenu={multiSelect && outlineRowSelectable(row) ? event => {
          event.preventDefault()
          setFocusKey(row.key)
          openMenu(row, { x: event.clientX, y: event.clientY })
        } : undefined}
        // Not on the chevron: opening and closing a block quickly is two clicks there.
        onDoubleClick={gutter ? event => { if (!(event.target as HTMLElement).closest('.script-outline-toggle')) toggleBreakpoint() } : undefined}
        onKeyDown={onKeyDown}
        // A Shift-click selects a range, not the text between.
        onMouseDown={multiSelect ? event => {
          if (!event.shiftKey) return
          event.preventDefault()
          event.currentTarget.focus({ preventScroll: true })
        } : undefined}
        onFocus={event => { if (event.target === event.currentTarget) { setFocusKey(row.key); showTip(row, shown, truncated, event.currentTarget, true) } }}
        onBlur={() => setTip(null)}
        onMouseEnter={event => showTip(row, shown, truncated, event.currentTarget, false)}
        onMouseLeave={() => setTip(null)}
      >
        {debug && (gutter ? (
          // The row is the keyboard stop (F9 toggles); the dot is for the pointer.
          <button
            type="button"
            tabIndex={-1}
            className={`script-outline-gutter ${gutter.state}`}
            title={gutter.title}
            aria-label={gutter.title}
            onClick={event => { event.stopPropagation(); toggleBreakpoint() }}
            onDoubleClick={event => event.stopPropagation()}
          >
            <span className="script-outline-breakpoint-dot" />
          </button>
        ) : <span className="script-outline-gutter" aria-hidden="true" />)}
        <span className="script-outline-toggle" aria-hidden="true">{row.expandable ? (row.expanded ? '▾' : '▸') : ''}</span>
        <span className="script-outline-name">{shown}</span>
        {occurrence && <span className="script-outline-occurrence">{occurrence}</span>}
        {row.badge && <small>{row.badge}</small>}
        {status && <span className={`script-outline-status ${status}`} aria-hidden="true" />}
        {breakpoint && <span className="script-outline-flag breakpoint" aria-hidden="true" />}
        {issue && <span className="script-outline-flag attention" aria-hidden="true" />}
      </div>
    )
  }

  const groupCount = model.groupsByKey.size
  const selectionCount = multiKeys?.size || 0
  return (
    <>
      {multiSelect && selectionCount > 1 ? (
        <div className="script-outline-selection" role="toolbar" aria-label="Selected steps">
          <span>{selectionCount} selected</span>
          <TooltipButton type="button" className="btn btn-secondary btn-sm" disabled={Boolean(multiSelect.wrapUnavailable)} title={multiSelect.wrapUnavailable || 'Wrap the selected steps in a named transaction'} onClick={event => { const box = event.currentTarget.getBoundingClientRect(); multiSelect.onWrap({ x: box.left, y: box.bottom + 4 }) }}>Wrap in transaction…</TooltipButton>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => {
            // The bar goes; focus returns to the tree.
            pendingFocus.current = tabStop ?? null
            multiSelect.onChange([])
          }}>Clear</button>
        </div>
      ) : (
      <div className="script-outline-tools">
        <button type="button" className="btn btn-ghost btn-sm" disabled={!groupCount} onClick={() => update(current => ({ ...current, base: 'expanded', flipped: new Set(), collapsedPhases: new Set() }))}>Expand all</button>
        <button type="button" className="btn btn-ghost btn-sm" disabled={!groupCount} onClick={() => update(current => ({ ...current, base: 'collapsed', flipped: new Set() }))}>Collapse all</button>
        <label className="script-outline-markers">
          <input type="checkbox" checked={showMarkers} onChange={event => onShowMarkersChange(event.target.checked)} />
          <span>Show markers</span>
        </label>
      </div>
      )}
      {/* A tree with no steps to own is not a tree (a search that matches nothing). */}
      <div ref={treeRef} className="script-outline-treeview" role={rows.length ? 'tree' : undefined} aria-label={rows.length ? 'Script steps' : undefined} aria-multiselectable={rows.length && multiSelect ? true : undefined}>
        {virtual ? (
          <div className="script-outline-virtual" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map(item => renderRow(rows[item.index], item.index, { position: 'absolute', top: 0, left: 0, right: 0, height: ROW_HEIGHT, transform: `translateY(${item.start}px)` }))}
          </div>
        ) : rows.map((row, index) => renderRow(row, index))}
        {!rows.length && <p className="script-outline-empty">{query.trim() ? 'No matching steps' : 'No actions'}</p>}
      </div>
      {multiSelect && <span className="sr-only" aria-live="polite">{selectionCount > 1 ? `${selectionCount} selected` : ''}</span>}
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
