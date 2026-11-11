import { useEffect, useMemo, useRef, useState } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import type { DebugTransactionRow } from './debugTransactionModel'
import { Waterfall } from './waterfall/Waterfall'
import { defaultWaterfallExpanded, filterTree, findNode, walk } from './waterfall/analysis'
import { buildDebugWaterfallTree, type DebugWaterfallNode } from './waterfall/debugTree'
import type { WaterfallNode } from './waterfall/types'
import { DebugWaterfallDrawer } from './DebugWaterfallDrawer'

export interface DebugWaterfallPanelProps {
  events: readonly BrowserReplayEvent[]
  /** Caller's index for each event; defaults to the event's position. */
  eventIndices?: readonly number[]
  transactions?: readonly DebugTransactionRow[] | null
  /** Unfiltered events, indexed by eventIndices, for iteration detection. */
  allEvents?: readonly BrowserReplayEvent[]
  /** The run dropped events (evidence is incomplete for every transaction). */
  evidenceDropped?: boolean
  /** Externally selected event (for example the debugger's current execution). */
  selectedEventIndex?: number | null
  /** A row was clicked. */
  onSelectEvent?: (eventIndex: number) => void
  /** Enter, double-click or the Inspect action. */
  onInspectEvent?: (eventIndex: number) => void
  canRevealInScript?: (event: BrowserReplayEvent) => boolean
  onRevealInScript?: (event: BrowserReplayEvent) => void
  canEditAction?: (event: BrowserReplayEvent) => boolean
  onEditAction?: (event: BrowserReplayEvent) => void
  variant?: 'dock' | 'summary'
  ariaLabel?: string
}

const leafFor = (roots: readonly DebugWaterfallNode[], eventIndex: number): DebugWaterfallNode | null => {
  let hit: DebugWaterfallNode | null = null
  walk(roots, n => {
    if (!hit && n.kind !== 'hop' && n.payload?.position != null && n.payload.eventIndex === eventIndex && !n.payload.transaction) hit = n
  })
  return hit
}

export function DebugWaterfallPanel({
  events,
  eventIndices,
  transactions,
  allEvents,
  evidenceDropped,
  selectedEventIndex,
  onSelectEvent,
  onInspectEvent,
  canRevealInScript,
  onRevealInScript,
  canEditAction,
  onEditAction,
  variant = 'dock',
  ariaLabel = 'Execution timeline waterfall',
}: DebugWaterfallPanelProps) {
  const [search, setSearch] = useState('')
  const [failuresOnly, setFailuresOnly] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const tree = useMemo(() => buildDebugWaterfallTree({ events, eventIndices, transactions, allEvents, evidenceDropped }), [events, eventIndices, transactions, allEvents, evidenceDropped])

  const q = search.trim().toLowerCase()
  const filtering = Boolean(q || failuresOnly)
  // Nodes kept only because a descendant matched; they open so matches show.
  const { roots, matched } = useMemo(() => {
    const hits = new Set<string>()
    if (!q && !failuresOnly) return { roots: tree.roots, matched: hits }
    const kept = filterTree(tree.roots, n => {
      const hit = matches(n)
      if (hit) hits.add(n.id)
      return hit
    })
    return { roots: kept, matched: hits }
    function matches(n: DebugWaterfallNode): boolean {
      if (failuresOnly && n.status !== 'failed') return false
      if (failuresOnly && n.children.length && n.kind !== 'request') return false
      if (!q) return true
      // Search the target and location too: a failed row's detail is its error.
      const row = n.payload?.position != null && !n.payload.transaction ? tree.timeline.rows[n.payload.position] : undefined
      return `${n.label} ${n.detail ?? ''} ${n.statusCode ?? ''} ${row ? `${row.location} ${row.methodOrType} ${row.target}` : ''}`.toLowerCase().includes(q)
    }
  }, [tree.roots, tree.timeline.rows, q, failuresOnly])
  // Ancestors kept only for a matching descendant open even if the user collapsed them.
  const forceExpanded = useMemo(() => {
    const ids = new Set<string>()
    if (filtering) walk(roots, n => {
      if (n.children.length > 0 && !matched.has(n.id)) ids.add(n.id)
    })
    return ids
  }, [filtering, roots, matched])
  const defaultExpanded = useMemo(
    () => (n: WaterfallNode<unknown>) => (filtering && n.children.length > 0 && !matched.has(n.id) ? true : defaultWaterfallExpanded(n)),
    [filtering, matched],
  )

  // Follow the external selection (the debugger's current execution) only when
  // it changes. Rebuilding the tree as events stream in must not undo a row the
  // user picked, and the echo of a click on a hop must not jump to its request.
  const syncedIndexRef = useRef<number | null>(null)
  useEffect(() => {
    if (selectedEventIndex == null) {
      // Cleared (Go to current, a new run): drop the highlight that mirrored the
      // debugger, and let the same index sync again later.
      if (syncedIndexRef.current != null) setSelectedId(null)
      syncedIndexRef.current = null
      return
    }
    const current = selectedId ? findNode(tree.roots, selectedId) : null
    // Already synced and the selection still exists: a rebuild must not undo the user's pick.
    if (syncedIndexRef.current === selectedEventIndex && current) return
    if (current && !current.payload?.transaction && current.payload?.eventIndex === selectedEventIndex) {
      syncedIndexRef.current = selectedEventIndex
      return
    }
    const leaf = leafFor(tree.roots, selectedEventIndex)
    if (!leaf) return // Not streamed in yet; retry when the tree grows.
    syncedIndexRef.current = selectedEventIndex
    setSelectedId(leaf.id)
  }, [selectedEventIndex, tree.roots, selectedId])

  // The unfiltered node: a filter's trimmed copy would skew the drawer's self time and totals.
  const selected = selectedId ? findNode(tree.roots, selectedId) ?? findNode(roots, selectedId) : null
  const position = selected?.payload?.position
  const row = position != null && selected && !selected.payload?.transaction && selected.kind !== 'iteration' ? tree.timeline.rows[position] : undefined
  // Only execution rows (not transaction or iteration rows) map to one event.
  const eventIndexOf = (n: DebugWaterfallNode) => (n.payload?.transaction ? undefined : n.payload?.eventIndex)
  // Inspect also works on a transaction row, through its own (end) marker event.
  const inspectIndexOf = (n: DebugWaterfallNode) => (n.payload?.aggregate ? undefined : n.payload?.eventIndex)

  const toolbar = (
    <>
      <label>
        {variant === 'summary' ? 'Filter waterfall' : 'Find execution'}
        <input type="search" autoComplete="off" value={search} onChange={e => setSearch(e.target.value)} placeholder="Name, URL or status" />
      </label>
      <label><input type="checkbox" checked={failuresOnly} onChange={e => setFailuresOnly(e.target.checked)} />Failures only</label>
    </>
  )

  const drawer = selected ? (
    <DebugWaterfallDrawer
      key={selected.id}
      node={selected}
      row={row}
      events={events}
      position={position}
      onInspect={onInspectEvent && inspectIndexOf(selected) != null ? () => onInspectEvent(inspectIndexOf(selected)!) : undefined}
      canRevealInScript={row ? (canRevealInScript ? canRevealInScript(row.event) : true) : false}
      onRevealInScript={onRevealInScript && row ? () => onRevealInScript(row.event) : undefined}
      canEdit={row ? (canEditAction ? canEditAction(row.event) : Boolean(onEditAction)) : false}
      onEdit={onEditAction && row ? () => onEditAction(row.event) : undefined}
    />
  ) : (
    <p className="wf-drawer-status" style={{ padding: 12 }}>Select a row to see its timing, request, response, headers, correlation and transport details.</p>
  )

  return (
    <div className={variant === 'summary' ? 'debug-summary-waterfall' : 'debug-execution-timeline'}>
      <Waterfall<DebugWaterfallNode['payload']>
        roots={roots as DebugWaterfallNode[]}
        spanMs={tree.spanMs}
        ariaLabel={ariaLabel}
        selectedId={selectedId}
        defaultExpanded={defaultExpanded}
        forceExpanded={forceExpanded}
        forceExpandedKey={`${q}|${failuresOnly}`}
        onSelect={n => setSelectedId(n.id)}
        onRowClick={n => {
          const idx = eventIndexOf(n as DebugWaterfallNode)
          if (idx != null) onSelectEvent?.(idx)
        }}
        onActivate={n => {
          const idx = inspectIndexOf(n as DebugWaterfallNode)
          if (idx != null) onInspectEvent?.(idx)
        }}
        toolbar={toolbar}
        drawer={drawer}
        emptyMessage={events.length ? 'No executions match these filters.' : 'No completed executions captured yet.'}
      />
    </div>
  )
}

export default DebugWaterfallPanel
