import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { defaultRangeExtractor, useVirtualizer, type Range } from '@tanstack/react-virtual'
import { type WaterfallTimeSink, ancestorIds, criticalPathIds, defaultWaterfallExpanded, isFailureCandidate, flattenAll, flattenVisible, isTimed, topTimeSinks, walk } from './analysis'
import { clampViewport, fitViewport, fullViewport, isFullViewport, niceTicks, panViewport, zoomViewport, type WaterfallViewport } from './axis'
import { WATERFALL_TOGGLE_PHASES, type WaterfallPhaseKey } from './phases'
import { WaterfallMinimap } from './WaterfallMinimap'
import { WaterfallRow } from './WaterfallRow'
import { WaterfallTooltip, type WaterfallTooltipState } from './WaterfallTooltip'
import type { WaterfallNode, WaterfallScaleMode, WaterfallSortMode } from './types'
import './Waterfall.css'

export const WATERFALL_ROW_HEIGHT = 34
/** Above this many visible rows, rows are virtualized. */
export const WATERFALL_VIRTUALIZE_THRESHOLD = 120

export interface WaterfallProps<T> {
  roots: WaterfallNode<T>[]
  spanMs: number
  ariaLabel: string
  selectedId: string | null
  /** Selection follows focus: called on click and on keyboard moves. */
  onSelect: (node: WaterfallNode<T>) => void
  /** Explicit activation: click (not keyboard moves). */
  onRowClick?: (node: WaterfallNode<T>) => void
  /** Enter or double-click. */
  onActivate?: (node: WaterfallNode<T>) => void
  /** Open by default over earlier toggles (ancestors of filter matches). */
  forceExpanded?: ReadonlySet<string>
  /** Changes when the filter changes; earlier toggles on forceExpanded ids reset then. */
  forceExpandedKey?: string
  /** Default open state for nodes the user has not toggled. */
  defaultExpanded?: (node: WaterfallNode<T>) => boolean
  /** Caller controls shown at the start of the toolbar (search, filters). */
  toolbar?: ReactNode
  /** Docked detail drawer for the selection. */
  drawer?: ReactNode
  emptyMessage?: string
  /** Replace the computed time sinks (a comparison shows its own headline facts). */
  sinks?: WaterfallTimeSink[]
  /** Accessible name for the facts strip. */
  sinksLabel?: string
  /** Sort to start with. */
  initialSortMode?: WaterfallSortMode
  className?: string
}

const SORT_LABELS: Record<WaterfallSortMode, string> = {
  chronological: 'Chronological',
  slowest: 'Slowest first',
  dominant: 'Dominant phase',
  change: 'Largest change',
}

const EMPTY_SET: ReadonlySet<string> = new Set()
// Stacked mode has no time axis, so the overview cannot zoom it.
const IGNORE_VIEWPORT = () => {}
const safeDomPart = (s: string) => s.replace(/[^A-Za-z0-9_-]/g, '_').slice(0, 48)

export function Waterfall<T>({
  roots,
  spanMs,
  ariaLabel,
  selectedId,
  onSelect,
  onRowClick,
  onActivate,
  defaultExpanded = defaultWaterfallExpanded,
  forceExpanded,
  forceExpandedKey,
  sinks: sinksOverride,
  sinksLabel = 'Top time sinks',
  initialSortMode,
  toolbar,
  drawer,
  emptyMessage = 'No executions captured yet.',
  className,
}: WaterfallProps<T>) {
  const baseId = useId().replace(/:/g, '')
  const tooltipId = `${baseId}-tooltip`
  const total = Math.max(spanMs, 1)
  const [toggled, setToggled] = useState<Map<string, boolean>>(() => new Map())
  const [sortMode, setSortMode] = useState<WaterfallSortMode>(initialSortMode ?? 'chronological')
  const [scaleMode, setScaleMode] = useState<WaterfallScaleMode>('waterfall')
  const [hidden, setHidden] = useState<Set<WaterfallPhaseKey>>(() => new Set())
  const [showCritical, setShowCritical] = useState(true)
  const [viewport, setViewportState] = useState<WaterfallViewport>(() => fullViewport(total))
  const [tip, setTip] = useState<WaterfallTooltipState | null>(null)
  const [keyboardTip, setKeyboardTip] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)
  const axisRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{ x: number; vp: WaterfallViewport; width: number; moved: boolean } | null>(null)
  const suppressClickRef = useRef(false)

  const setViewport = useCallback((next: WaterfallViewport) => setViewportState(clampViewport(next, total)), [total])
  // Keep the viewport valid as a live run grows; stay at full span when the user has not zoomed.
  const lastTotalRef = useRef(total)
  useEffect(() => {
    // Read the previous total now: the updater runs later, after the ref moved on.
    const previous = lastTotalRef.current
    lastTotalRef.current = total
    setViewportState(vp => (isFullViewport(vp, previous) ? fullViewport(total) : clampViewport(vp, total)))
  }, [total])

  const isExpanded = useCallback(
    // Forced open is a default: a toggle made while filtering still wins.
    (n: WaterfallNode<T>) => toggled.get(n.id) ?? (forceExpanded?.has(n.id) || defaultExpanded(n)),
    [toggled, defaultExpanded, forceExpanded],
  )
  const expandedSet = useMemo(() => {
    const s = new Set<string>()
    walk(roots, n => {
      if (n.children.length && isExpanded(n)) s.add(n.id)
    })
    return s
  }, [roots, isExpanded])
  const rows = useMemo(() => flattenVisible(roots, expandedSet, sortMode), [roots, expandedSet, sortMode])
  const allNodes = useMemo(() => flattenAll(roots, sortMode), [roots, sortMode])
  const domIndex = useMemo(() => new Map(allNodes.map((n, i) => [n.id, i])), [allNodes])
  const nodeById = useMemo(() => new Map(allNodes.map(n => [n.id, n])), [allNodes])
  const criticalPath = useMemo(() => criticalPathIds(roots), [roots])
  // When every request runs one after another, all of them are critical and an
  // outline on every bar says nothing; highlight only when the path excludes work.
  const criticalIsSelective = useMemo(() => {
    let excluded = false
    walk(roots, n => {
      // Markers are never on the path (bookkeeping), so they cannot make it selective.
      if (!excluded && isTimed(n) && n.kind !== 'iteration' && n.kind !== 'marker' && !criticalPath.has(n.id)) excluded = true
    })
    return excluded
  }, [roots, criticalPath])
  const critical = showCritical && criticalIsSelective ? criticalPath : EMPTY_SET
  const computedSinks = useMemo(() => (sinksOverride ? null : topTimeSinks(roots, total)), [roots, total, sinksOverride])
  const sinks = sinksOverride ?? computedSinks ?? []
  // Comparison-only controls appear only when some row carries a change.
  const hasDelta = useMemo(() => {
    let found = false
    walk(roots, n => {
      if (!found && n.delta) found = true
    })
    return found
  }, [roots])
  const viewportRef = useRef(viewport)
  viewportRef.current = viewport
  // Fit the tick count to the axis width so labels never overlap in narrow docks.
  const [axisWidth, setAxisWidth] = useState(0)
  const axisMounted = rows.length > 0
  useEffect(() => {
    const el = axisRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(entries => setAxisWidth(entries[0]?.contentRect.width ?? 0))
    ro.observe(el)
    return () => ro.disconnect()
  }, [axisMounted])
  const maxTicks = axisWidth > 0 ? Math.min(8, Math.max(2, Math.floor(axisWidth / 64))) : 8
  const ticks = useMemo(() => niceTicks(viewport, maxTicks), [viewport, maxTicks])
  const fullSpan = useMemo(() => fullViewport(total), [total])
  const rowDomId = (id: string) => `${baseId}-r${domIndex.get(id) ?? 'x'}-${safeDomPart(id)}`
  const activeIndex = rows.findIndex(r => r.node.id === selectedId)
  const activeRow = activeIndex >= 0 ? rows[activeIndex] : null
  const selectedNode = selectedId ? nodeById.get(selectedId) ?? null : null

  const hasRows = rows.length > 0
  const virtualize = rows.length > WATERFALL_VIRTUALIZE_THRESHOLD
  const virtualizer = useVirtualizer({
    count: virtualize ? rows.length : 0,
    getScrollElement: () => bodyRef.current,
    estimateSize: () => WATERFALL_ROW_HEIGHT,
    overscan: 12,
    // The sticky header row sits above the list inside the scroll element.
    scrollMargin: WATERFALL_ROW_HEIGHT,
    scrollPaddingStart: WATERFALL_ROW_HEIGHT,
    // Keep the active row mounted so aria-activedescendant always resolves.
    rangeExtractor: useCallback((range: Range) => {
      const base = defaultRangeExtractor(range)
      return activeIndex >= 0 && !base.includes(activeIndex) ? [...base, activeIndex].sort((a, b) => a - b) : base
    }, [activeIndex]),
  })

  const scrollToRow = useCallback((index: number, id: string) => {
    if (index < 0) return
    if (virtualize) {
      virtualizer.scrollToIndex(index, { align: 'auto' })
      requestAnimationFrame(() => {
        const el = document.getElementById(rowDomId(id))
        if (el) revealInScrollParent(el)
      })
      return
    }
    // Scroll only the grid: scrollIntoView would also move the page and the dock.
    const body = bodyRef.current
    const el = document.getElementById(rowDomId(id))
    if (!body || !el) return
    const top = el.offsetTop
    const bottom = top + el.offsetHeight
    if (top - WATERFALL_ROW_HEIGHT < body.scrollTop) body.scrollTop = Math.max(0, top - WATERFALL_ROW_HEIGHT)
    else if (bottom > body.scrollTop + body.clientHeight) body.scrollTop = bottom - body.clientHeight
    revealInScrollParent(el)
    // rowDomId depends on domIndex only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [virtualize, virtualizer, domIndex])

  // While the keyboard drives the grid, also keep the row visible inside the
  // nearest scrolling container (the debugger dock), never the page itself.
  const revealInScrollParent = (el: HTMLElement) => {
    if (document.activeElement !== bodyRef.current) return
    let parent = bodyRef.current?.parentElement ?? null
    while (parent && parent !== document.body) {
      const style = getComputedStyle(parent)
      if (/(auto|scroll)/.test(style.overflowY) && parent.scrollHeight > parent.clientHeight) break
      parent = parent.parentElement
    }
    if (!parent || parent === document.body) return
    const box = parent.getBoundingClientRect()
    const rect = el.getBoundingClientRect()
    if (rect.top < box.top) parent.scrollTop -= box.top - rect.top
    else if (rect.bottom > box.bottom) parent.scrollTop += rect.bottom - box.bottom
  }

  const setOpen = (id: string, open: boolean) => setToggled(prev => new Map(prev).set(id, open))
  // A new filter forces its match ancestors open over earlier collapses. Keyed
  // on the filter, not the set: live rebuilds must not undo a collapse made
  // while filtering.
  const forceExpandedRef = useRef(forceExpanded)
  forceExpandedRef.current = forceExpanded
  useEffect(() => {
    const ids = forceExpandedRef.current
    if (!ids?.size) return
    setToggled(prev => {
      if (![...ids].some(id => prev.has(id))) return prev
      const next = new Map(prev)
      for (const id of ids) next.delete(id)
      return next
    })
  }, [forceExpandedKey])
  const isExpandedRef = useRef(isExpanded)
  isExpandedRef.current = isExpanded
  // Stable so memoized rows only re-render when their own props change.
  const toggle = useCallback((node: WaterfallNode<T>) => {
    setToggled(prev => new Map(prev).set(node.id, !isExpandedRef.current(node)))
    // A chevron click must not strand keyboard focus on the button.
    bodyRef.current?.focus({ preventScroll: true })
  }, [])

  const revealNode = (id: string) => {
    const trail = ancestorIds(roots, id)
    if (trail?.length) setToggled(prev => {
      const next = new Map(prev)
      for (const a of trail) next.set(a, true)
      return next
    })
    const node = nodeById.get(id)
    if (!node) return
    onSelect(node)
    // Explicit navigation (failure stepping, sink chips) always scrolls, even
    // when it re-selects the row that is already selected.
    scrolledRef.current = null
    const at = rows.findIndex(r => r.node.id === id)
    if (at >= 0) {
      scrolledRef.current = id
      scrollToRow(at, id)
    }
  }

  // A selection made elsewhere (the debugger's current execution) may sit in a
  // collapsed subtree: open its ancestors once per new selection.
  const revealedRef = useRef<string | null>(null)
  useEffect(() => {
    // A cleared selection must reveal and scroll again when the same row returns.
    if (!selectedId) {
      revealedRef.current = null
      scrolledRef.current = null
      return
    }
    if (!selectedId || revealedRef.current === selectedId) return
    // Filtered out for now: try again when the filter changes.
    if (!nodeById.has(selectedId)) return
    revealedRef.current = selectedId
    if (activeIndex >= 0) return
    const trail = ancestorIds(roots, selectedId)
    if (trail?.length) setToggled(prev => {
      const next = new Map(prev)
      for (const a of trail) next.set(a, true)
      return next
    })
  }, [selectedId, activeIndex, nodeById, roots])

  // Scroll the selection into view after expansion or external changes.
  // Once per selection: rebuilding the tree as events stream in must not pull
  // the list back while the user scrolls elsewhere.
  const scrolledRef = useRef<string | null>(null)
  useEffect(() => {
    if (activeIndex < 0 || !selectedId || scrolledRef.current === selectedId) return
    scrolledRef.current = selectedId
    scrollToRow(activeIndex, selectedId)
  }, [activeIndex, selectedId, scrollToRow])

  // A selection hidden in a collapsed subtree moves from its nearest visible ancestor.
  const effectiveIndex = () => {
    if (activeIndex >= 0 || !selectedId) return activeIndex
    const trail = ancestorIds(roots, selectedId) ?? []
    for (let i = trail.length - 1; i >= 0; i--) {
      const at = rows.findIndex(r => r.node.id === trail[i])
      if (at >= 0) return at
    }
    return -1
  }

  const moveTo = (index: number) => {
    const r = rows[Math.max(0, Math.min(rows.length - 1, index))]
    if (r) onSelect(r.node)
  }

  const stepFailure = (dir: 1 | -1) => {
    const failures = allNodes.filter(isFailureCandidate)
    if (!failures.length) return
    const cur = selectedId ? domIndex.get(selectedId) ?? -1 : -1
    const pos = (n: WaterfallNode<T>) => domIndex.get(n.id) ?? -1
    let next: WaterfallNode<T> | undefined
    if (dir > 0) next = failures.find(n => pos(n) > cur) ?? failures[0]
    else for (let i = failures.length - 1; i >= 0 && !next; i--) if (pos(failures[i]) < cur) next = failures[i]
    next ??= failures[failures.length - 1]
    revealNode(next.id)
  }

  const fitSelection = () => {
    if (selectedNode && isTimed(selectedNode)) setViewport(fitViewport(selectedNode.startMs, selectedNode.startMs + selectedNode.durationMs, total))
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return
    // Leave modified keys (Cmd/Ctrl+F, Cmd+[, Cmd+0, …) to the browser and app.
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const cur = effectiveIndex()
    const stacked = scaleMode === 'stacked'
    let handled = true
    switch (e.key) {
      case 'ArrowDown': moveTo(cur < 0 ? 0 : cur + 1); break
      case 'ArrowUp': moveTo(cur < 0 ? 0 : cur - 1); break
      case 'Home': moveTo(0); break
      case 'End': moveTo(rows.length - 1); break
      case 'PageDown': moveTo(cur < 0 ? 0 : cur + 10); break
      case 'PageUp': moveTo(cur < 0 ? 0 : cur - 10); break
      case 'ArrowRight':
        if (activeRow?.hasChildren && !activeRow.expanded) setOpen(activeRow.node.id, true)
        else if (activeRow?.expanded) moveTo(cur + 1)
        break
      case 'ArrowLeft':
        if (activeRow?.expanded) setOpen(activeRow.node.id, false)
        else if (activeRow?.parentId) moveTo(rows.findIndex(r => r.node.id === activeRow.parentId))
        break
      case 'Enter':
        if (activeRow) onActivate?.(activeRow.node)
        break
      case ' ':
        if (activeRow?.hasChildren) toggle(activeRow.node)
        break
      case ']': stepFailure(1); break
      case '[': stepFailure(-1); break
      case '+': case '=': if (stacked) handled = false; else setViewport(zoomViewport(viewport, 0.8, 0.5, total)); break
      case '-': case '_': if (stacked) handled = false; else setViewport(zoomViewport(viewport, 1.25, 0.5, total)); break
      case '0': if (stacked) handled = false; else setViewport(fullViewport(total)); break
      case 'f': if (stacked) handled = false; else fitSelection(); break
      case 'Escape':
        // Only consume Escape when it closes our tooltip; otherwise the
        // workspace's Escape shortcut still applies.
        if (keyboardTip) setKeyboardTip(false)
        else handled = false
        break
      default: handled = false
    }
    if (handled) {
      e.preventDefault()
      if (e.key !== 'Escape') setKeyboardTip(true)
    }
  }

  // Stacked mode has no time axis: wheel zoom/pan would change a hidden viewport.
  const scaleModeRef = useRef(scaleMode)
  scaleModeRef.current = scaleMode
  // Wheel: zoom on the axis, ctrl/⌘+wheel zoom and shift+wheel pan on rows.
  useEffect(() => {
    const body = bodyRef.current
    const axis = axisRef.current
    const anchorFor = (clientX: number) => {
      const rect = axisRef.current?.getBoundingClientRect()
      return rect && rect.width > 0 ? (clientX - rect.left) / rect.width : 0.5
    }
    // Each returns true only when the viewport changes; otherwise the wheel
    // belongs to the page or dock and must not be swallowed.
    const apply = (e: WheelEvent, next: WaterfallViewport) => {
      const vp = viewportRef.current
      if (Math.abs(next.startMs - vp.startMs) < 1e-9 && Math.abs(next.endMs - vp.endMs) < 1e-9) return
      e.preventDefault()
      viewportRef.current = next
      setViewportState(next)
    }
    const zoomAt = (e: WheelEvent) => apply(e, clampViewport(zoomViewport(viewportRef.current, e.deltaY > 0 ? 1.25 : 0.8, anchorFor(e.clientX), total), total))
    const panBy = (e: WheelEvent) => {
      const rect = axisRef.current?.getBoundingClientRect()
      const delta = Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY
      const vp = viewportRef.current
      apply(e, panViewport(vp, rect && rect.width > 0 ? (delta / rect.width) * (vp.endMs - vp.startMs) : 0, total))
    }
    const onAxisWheel = (e: WheelEvent) => {
      if (scaleModeRef.current === 'stacked') return
      // A sideways swipe or shift+wheel pans; only vertical wheel zooms.
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) panBy(e)
      else if (e.deltaY !== 0) zoomAt(e)
    }
    const onBodyWheel = (e: WheelEvent) => {
      const target = e.target as HTMLElement | null
      const inTimeline = target?.closest?.('.wf-cell-timeline')
      // The axis has its own listener; handling it here too would pan or zoom twice.
      if (!inTimeline || target?.closest?.('.wf-axis') || scaleModeRef.current === 'stacked') return
      if ((e.ctrlKey || e.metaKey) && e.deltaY !== 0) zoomAt(e)
      else if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) panBy(e)
    }
    axis?.addEventListener('wheel', onAxisWheel, { passive: false })
    body?.addEventListener('wheel', onBodyWheel, { passive: false })
    return () => {
      axis?.removeEventListener('wheel', onAxisWheel)
      body?.removeEventListener('wheel', onBodyWheel)
    }
  }, [total, hasRows])

  // Drag to pan on the axis or the timeline column.
  const onPanStart = (e: PointerEvent<HTMLElement>) => {
    // Every fresh press starts clean, whether or not it begins a pan.
    suppressClickRef.current = false
    dragRef.current = null
    if (e.button !== 0 || scaleMode === 'stacked') return
    const target = e.target as HTMLElement
    if (!target.closest('.wf-axis, .wf-cell-timeline')) return
    const width = axisRef.current?.getBoundingClientRect().width ?? 0
    dragRef.current = { x: e.clientX, vp: viewport, width, moved: false }
  }
  useEffect(() => {
    const onMove = (e: globalThis.PointerEvent) => {
      const d = dragRef.current
      if (!d || d.width <= 0) return
      if (e.buttons === 0) {
        dragRef.current = null
        return
      }
      const dx = e.clientX - d.x
      if (!d.moved && Math.abs(dx) < 4) return
      d.moved = true
      setTip(null)
      setViewportState(panViewport(d.vp, (-dx / d.width) * (d.vp.endMs - d.vp.startMs), total))
    }
    const onUp = () => {
      if (dragRef.current?.moved) {
        suppressClickRef.current = true
        // The drag-ending click may land outside the grid or never fire.
        window.setTimeout(() => {
          suppressClickRef.current = false
        }, 0)
      }
      dragRef.current = null
    }
    window.addEventListener('pointermove', onMove)
    // A touch scroll the browser takes over ends in pointercancel, not pointerup.
    const onCancel = () => {
      dragRef.current = null
    }
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onCancel)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onCancel)
    }
  }, [total])

  const nodeFromEvent = (target: EventTarget | null): WaterfallNode<T> | null => {
    // A click on a control inside the row (the chevron) is not a row click.
    const t = target instanceof Element ? target : null
    if (!t || t.closest('button, input, select, textarea, a')) return null
    const el = t.closest<HTMLElement>('[data-node-id]')
    if (!el) return null
    return (el.dataset.nodeId && nodeById.get(el.dataset.nodeId)) || null
  }

  const onRowClickInternal = (node: WaterfallNode<T>) => {
    setKeyboardTip(false)
    onSelect(node)
    onRowClick?.(node)
    bodyRef.current?.focus({ preventScroll: true })
  }

  const onTimelinePointer = useCallback((node: WaterfallNode<T>, e: PointerEvent<HTMLElement> | null) => {
    if (!e || dragRef.current?.moved) {
      setTip(prev => (prev && prev.nodeId === node.id ? null : prev))
      return
    }
    const el = e.target as HTMLElement
    const phase = el.dataset?.phase as WaterfallPhaseKey | undefined
    const gap = el.dataset?.gapMs ? Number(el.dataset.gapMs) : undefined
    const childId = el.dataset?.child
    setKeyboardTip(false)
    setTip({ nodeId: node.id, phase, gapMs: gap, childId, x: e.clientX, y: e.clientY })
  }, [])

  // A hovered row can disappear (collapse, filter) without a pointerleave.
  useEffect(() => {
    if (tip && !rows.some(r => r.node.id === tip.nodeId)) setTip(null)
  }, [rows, tip])

  // Escape dismisses a hover tooltip (WCAG 1.4.13) without leaving the workspace.
  useEffect(() => {
    if (!tip) return
    // Capture phase so the workspace's own Escape shortcut does not run first,
    // but a focused text field, menu or dialog keeps its own Escape.
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      const t = e.target as HTMLElement | null
      if (t?.closest?.('input, textarea, select, [contenteditable="true"], [role="menu"], [role="dialog"], [role="listbox"]')) {
        setTip(null)
        return
      }
      e.preventDefault()
      setTip(null)
      setKeyboardTip(false)
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [tip])

  // A hover tooltip is stale once anything scrolls under the pointer.
  useEffect(() => {
    // Only scrolls that move the grid (its own, the dock's, the page's) stale the tip.
    const clear = (e: Event) => {
      const t = e.target
      if (t === document || t === window || (t instanceof Node && bodyRef.current && t.contains(bodyRef.current))) setTip(null)
    }
    window.addEventListener('scroll', clear, true)
    return () => window.removeEventListener('scroll', clear, true)
  }, [])

  // Keyboard tooltip anchors to the active row's bar.
  const [kbAnchor, setKbAnchor] = useState<{ x: number; y: number } | null>(null)
  const activeNodeId = activeRow?.node.id
  useEffect(() => {
    if (!keyboardTip || !activeNodeId) {
      setKbAnchor(null)
      return
    }
    // Measure after the virtualizer has scrolled and rendered the row, and
    // again whenever the list scrolls under the anchored tooltip.
    const measure = () => {
      const id = rowDomId(activeNodeId)
      const rect = (document.getElementById(`${id}-bar`) ?? document.getElementById(id))?.getBoundingClientRect()
      // Hide rather than float over other UI when the row is scrolled out of the grid.
      const view = bodyRef.current?.getBoundingClientRect()
      // No layout (zero-size grid) means nothing to compare against: treat as visible.
      const visible = rect && (!view || view.height === 0 || (rect.bottom > view.top + WATERFALL_ROW_HEIGHT && rect.top < view.bottom))
      setKbAnchor(prev => {
        const next = rect && visible ? { x: rect.left, y: rect.bottom - 14 } : null
        return prev && next && prev.x === next.x && prev.y === next.y ? prev : next
      })
    }
    const frame = requestAnimationFrame(measure)
    // Capture phase: the grid, the dock or the page may be the one scrolling.
    window.addEventListener('scroll', measure, true)
    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener('scroll', measure, true)
    }
    // rowDomId depends on domIndex only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyboardTip, activeNodeId, viewport, scaleMode, domIndex, rows])

  const tipState: WaterfallTooltipState | null = keyboardTip && activeRow && kbAnchor ? { nodeId: activeRow.node.id, x: kbAnchor.x, y: kbAnchor.y } : tip
  const tipNode = tipState ? nodeById.get(tipState.nodeId) ?? null : null

  const togglePhase = (key: WaterfallPhaseKey, solo: boolean) => {
    setHidden(prev => {
      if (solo) {
        const othersHidden = WATERFALL_TOGGLE_PHASES.every(p => p.key === key || prev.has(p.key))
        return othersHidden && !prev.has(key) ? new Set() : new Set(WATERFALL_TOGGLE_PHASES.filter(p => p.key !== key).map(p => p.key))
      }
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const zoomed = !isFullViewport(viewport, total)
  const virtualItems = virtualize ? virtualizer.getVirtualItems() : null
  const renderRow = (index: number, translateY?: number) => {
    const r = rows[index]
    const domId = rowDomId(r.node.id)
    return (
      <WaterfallRow<T>
        key={r.node.id}
        row={r}
        domId={domId}
        barId={`${domId}-bar`}
        viewport={viewport}
        scaleMode={scaleMode}
        ticks={ticks}
        hiddenPhases={hidden}
        selected={r.node.id === selectedId}
        active={index === activeIndex}
        critical={critical.has(r.node.id)}
        rowIndex={index + 2}
        describedBy={keyboardTip && index === activeIndex && tipState ? tooltipId : undefined}
        translateY={translateY}
        onToggle={toggle}
        onTimelinePointer={onTimelinePointer}
      />
    )
  }

  return (
    <div className={`wf-root${className ? ` ${className}` : ''}`}>
      <div className="wf-toolbar">
        {toolbar && <div className="wf-toolbar-group wf-toolbar-caller">{toolbar}</div>}
        <div className="wf-toolbar-group">
          <label className="wf-inline-label">
            Sort
            <select value={sortMode} onChange={e => setSortMode(e.target.value as WaterfallSortMode)}>
              {(Object.keys(SORT_LABELS) as WaterfallSortMode[]).filter(m => m !== 'change' || hasDelta).map(m => <option key={m} value={m}>{SORT_LABELS[m]}</option>)}
            </select>
          </label>
          <div className="wf-segmented" role="group" aria-label="Timeline scale">
            {([['waterfall', 'Waterfall (session)'], ['stacked', 'Stacked (100%)']] as const).map(([mode, label]) => (
              <button key={mode} type="button" className={scaleMode === mode ? 'active' : ''} aria-pressed={scaleMode === mode} onClick={() => setScaleMode(mode)}>{label}</button>
            ))}
          </div>
          <button
            type="button"
            className={`wf-chip-toggle${showCritical ? ' active' : ''}`}
            aria-pressed={showCritical}
            aria-describedby={criticalIsSelective ? undefined : `${baseId}-critical-note`}
            onClick={() => setShowCritical(v => !v)}
          >
            Critical path
          </button>
          {!criticalIsSelective && hasRows && <span id={`${baseId}-critical-note`} className="wf-toolbar-note">All work is sequential, so every request is on the critical path.</span>}
        </div>
        <div className="wf-toolbar-group" role="group" aria-label="Zoom">
          <button type="button" className="wf-icon-btn" aria-label="Zoom out" disabled={scaleMode === 'stacked' || !zoomed} onClick={() => setViewport(zoomViewport(viewport, 1.25, 0.5, total))}>−</button>
          <button type="button" className="wf-icon-btn" aria-label="Zoom in" disabled={scaleMode === 'stacked'} onClick={() => setViewport(zoomViewport(viewport, 0.8, 0.5, total))}>+</button>
          <button type="button" className="wf-text-btn" disabled={scaleMode === 'stacked' || !selectedNode || !isTimed(selectedNode)} onClick={fitSelection}>Fit to selection</button>
          <button type="button" className="wf-text-btn" disabled={!zoomed} onClick={() => setViewport(fullViewport(total))}>Reset</button>
        </div>
      </div>
      <ul className="wf-legend" aria-label="Traffic phases (toggle to dim; Shift-click to show only one)">
        {WATERFALL_TOGGLE_PHASES.map(p => (
          <li key={p.key}>
            <button
              type="button"
              className={`wf-legend-chip${hidden.has(p.key) ? ' off' : ''}`}
              aria-pressed={!hidden.has(p.key)}
              onClick={e => togglePhase(p.key, e.shiftKey || e.altKey)}
            >
              <i className={`wf-swatch phase-${p.key}`} aria-hidden="true" />
              {p.shortLabel}
            </button>
          </li>
        ))}
        <li><span className="wf-legend-chip static"><i className="wf-swatch wf-swatch-gap" aria-hidden="true" />Think / idle</span></li>
        {hasDelta && <li><span className="wf-legend-chip static"><i className="wf-swatch wf-swatch-ghost" aria-hidden="true" />Baseline</span></li>}
        {hidden.size > 0 && <li><button type="button" className="wf-legend-chip" onClick={() => setHidden(new Set())}>Show all</button></li>}
      </ul>
      {sinks.length > 0 && (
        <div className="wf-sinks" role="list" aria-label={sinksLabel}>
          {sinks.map(s => (
            <div role="listitem" key={s.id}>
              {s.targetId ? (
                <button type="button" className={`wf-sink ${s.tone}`} onClick={() => { revealNode(s.targetId!); bodyRef.current?.focus({ preventScroll: true }) }}>
                  <strong>{s.label}</strong>
                  <small>{s.detail}</small>
                </button>
              ) : (
                <span className={`wf-sink ${s.tone}`}>
                  <strong>{s.label}</strong>
                  <small>{s.detail}</small>
                </span>
              )}
            </div>
          ))}
        </div>
      )}
      {!rows.length ? (
        <p className="wf-empty">{emptyMessage}</p>
      ) : (
        <div className={`wf-layout${drawer ? ' has-drawer' : ''}`}>
          <div className="wf-main">
            <div className="wf-minimap-row">
              <span className="wf-minimap-caption">Overview</span>
              <WaterfallMinimap roots={roots} spanMs={total} viewport={scaleMode === 'stacked' ? fullSpan : viewport} onViewport={scaleMode === 'stacked' ? IGNORE_VIEWPORT : setViewport} disabled={scaleMode === 'stacked'} />
            </div>
            <div
              ref={bodyRef}
              className="wf-body"
              role="treegrid"
              aria-label={ariaLabel}
              aria-rowcount={rows.length + 1}
              aria-multiselectable={false}
              aria-activedescendant={activeRow ? rowDomId(activeRow.node.id) : undefined}
              aria-keyshortcuts="ArrowUp ArrowDown ArrowLeft ArrowRight Enter [ ] + - 0 f"
              tabIndex={0}
              onKeyDown={onKeyDown}
              onBlur={() => setKeyboardTip(false)}
              onPointerDown={onPanStart}
              onMouseDown={e => {
                // Rows must not take focus on press (they carry tabIndex=-1 for
                // aria-activedescendant); focus stays on the grid for the keyboard.
                const target = e.target as HTMLElement
                if (e.button === 0 && target.closest('[data-node-id]') && !target.closest('button, input, select, textarea, a')) {
                  e.preventDefault()
                  bodyRef.current?.focus({ preventScroll: true })
                }
              }}
              onClick={e => {
                // The click that ends a pan drag is not a selection, whatever it lands on.
                if (suppressClickRef.current) {
                  suppressClickRef.current = false
                  return
                }
                const node = nodeFromEvent(e.target)
                if (node) onRowClickInternal(node)
              }}
              onDoubleClick={e => {
                const node = nodeFromEvent(e.target)
                if (node) onActivate?.(node)
              }}
            >
              <div role="row" aria-rowindex={1} className="wf-row wf-header">
                <div role="columnheader" className="wf-cell wf-cell-name">Iteration / transaction / request</div>
                <div role="columnheader" className="wf-cell wf-cell-status">Status</div>
                <div role="columnheader" className="wf-cell wf-cell-duration">Duration</div>
                <div role="columnheader" className="wf-cell wf-cell-timeline">
                  <div ref={axisRef} className="wf-axis" aria-hidden="true">
                    {scaleMode === 'stacked'
                      ? [0, 25, 50, 75, 100].map(p => <span key={p} className="wf-tick" style={{ left: `${p}%` }}>{p}%</span>)
                      : ticks.map(t => <span key={t.ms} className="wf-tick" style={{ left: `${t.pct}%` }}>{t.label}</span>)}
                  </div>
                  <span className="sr-only">Timeline</span>
                </div>
              </div>
              {virtualItems ? (
                <div className="wf-virtual" style={{ height: virtualizer.getTotalSize() }}>
                  {virtualItems.map(item => renderRow(item.index, item.start - WATERFALL_ROW_HEIGHT))}
                </div>
              ) : (
                rows.map((_, i) => renderRow(i))
              )}
            </div>
            <p className="wf-hint">
              ↑/↓ move · ←/→ collapse/expand · Enter inspect · [ / ] previous/next failure · +/− zoom · 0 reset · drag the axis to pan, wheel on it to zoom, brush the overview to zoom in.
            </p>
          </div>
          {drawer && <aside className="wf-drawer" aria-label="Selected row details">{drawer}</aside>}
        </div>
      )}
      {/* Portaled: .wf-root's container query makes it the containing block for position: fixed. */}
      {tipState && tipNode && typeof document !== 'undefined' && createPortal(<WaterfallTooltip id={tooltipId} node={tipNode} state={tipState} critical={critical.has(tipNode.id)} />, document.body)}
    </div>
  )
}
