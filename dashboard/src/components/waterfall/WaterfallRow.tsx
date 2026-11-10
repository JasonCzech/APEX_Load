import { memo, type PointerEvent } from 'react'
import { childGaps, dominantPhase, isTimed, timingChildren } from './analysis'
import { projectSpan, type WaterfallTick, type WaterfallViewport } from './axis'
import { TIMING_SOURCE_LABELS, formatWaterfallMs, type WaterfallPhaseKey } from './phases'
import type { WaterfallFlatRow, WaterfallNode, WaterfallScaleMode } from './types'

export interface WaterfallRowProps<T> {
  row: WaterfallFlatRow<T>
  domId: string
  barId: string
  viewport: WaterfallViewport
  scaleMode: WaterfallScaleMode
  ticks: readonly WaterfallTick[]
  hiddenPhases: ReadonlySet<WaterfallPhaseKey>
  selected: boolean
  active: boolean
  critical: boolean
  /** 1-based row index within the grid (the header is row 1). */
  rowIndex: number
  /** Virtualized rows are absolutely positioned at this offset. */
  translateY?: number
  /** Id of the keyboard tooltip describing this row, while it is shown. */
  describedBy?: string
  onToggle: (node: WaterfallNode<T>) => void
  onTimelinePointer: (node: WaterfallNode<T>, event: PointerEvent<HTMLElement> | null) => void
}

type Delta = NonNullable<WaterfallNode['delta']>
// Outcome before sign: a missing or new execution is a difference in what ran,
// and a request that now fails fast is not an improvement.
const deltaTone = (d: Delta) =>
  d.outcome === 'added' || d.outcome === 'removed' ? 'same' : d.outcome === 'now_failing' ? 'failing' : d.ms >= 0.5 ? 'slower' : d.ms <= -0.5 ? 'faster' : 'same'
const formatDelta = (d: Delta) =>
  d.outcome === 'added' ? 'new' : d.outcome === 'removed' ? 'not run' : Math.abs(d.ms) < 0.5 ? '±0 ms' : `${d.ms > 0 ? '+' : '−'}${formatWaterfallMs(Math.abs(d.ms))}`

const STATUS_LABEL = { passed: 'Passed', failed: 'Failed', incomplete: 'Incomplete', not_run: 'Not run' } as const

const pct = (v: number) => `${Number.isFinite(v) ? v : 0}%`

function frame(node: WaterfallNode<unknown>, viewport: WaterfallViewport, scaleMode: WaterfallScaleMode) {
  if (!isTimed(node)) return null
  if (scaleMode === 'stacked') return { leftPct: 0, widthPct: 100, visible: true }
  return projectSpan(node.startMs, node.durationMs, viewport)
}

function LeafBar({ node, hidden }: { node: WaterfallNode<unknown>; hidden: ReadonlySet<WaterfallPhaseKey> }) {
  const dur = Math.max(node.durationMs ?? 0, ...node.phases.map(p => p.endMs), 0)
  if (!(dur > 0)) return null
  return (
    <>
      {node.phases.map((p, i) => (
        <span
          key={`${p.key}-${i}`}
          className={`wf-seg phase-${p.key}${hidden.has(p.key) ? ' dimmed' : ''}`}
          data-phase={p.key}
          style={{ left: pct((p.startMs / dur) * 100), width: pct(((p.endMs - p.startMs) / dur) * 100) }}
        />
      ))}
    </>
  )
}

// Memoized: its bars are positioned relative to the parent, so pan and zoom never change them.
const ParentTrack = memo(function ParentTrack({ node, hidden }: { node: WaterfallNode<unknown>; hidden: ReadonlySet<WaterfallPhaseKey> }) {
  if (!isTimed(node) || !(node.durationMs > 0)) return null
  const rel = (ms: number) => ((ms - node.startMs) / node.durationMs) * 100
  return (
    <>
      {childGaps(node).map((g, i) => (
        <span key={`gap-${i}`} className="wf-gap" data-gap-ms={g.durationMs} style={{ left: pct(rel(g.startMs)), width: pct((g.durationMs / node.durationMs) * 100) }} />
      ))}
      {timingChildren(node).filter(isTimed).map(c => {
        const dom = dominantPhase(c)
        if (c.kind === 'marker') return null
        const cls = c.kind === 'think' ? 'wf-gap think' : c.status === 'failed' ? 'wf-mini failed' : `wf-mini phase-${dom?.key ?? 'total'}${dom && hidden.has(dom.key) ? ' dimmed' : ''}`
        return (
          <span
            key={c.id}
            className={cls}
            data-child={c.id}
            data-phase={dom?.key}
            style={{ left: pct(rel(c.startMs!)), width: pct(((c.durationMs ?? 0) / node.durationMs) * 100) }}
          />
        )
      })}
    </>
  )
})

function WaterfallRowInner<T>({
  row,
  domId,
  barId,
  viewport,
  scaleMode,
  ticks,
  hiddenPhases,
  selected,
  active,
  critical,
  rowIndex,
  translateY,
  describedBy,
  onToggle,
  onTimelinePointer,
}: WaterfallRowProps<T>) {
  const { node, depth, hasChildren, expanded } = row
  const f = frame(node, viewport, scaleMode)
  // Comparison overlay, aligned to this node's start: the baseline's length and
  // the stretch between the two ends (slower or faster).
  const ghost = scaleMode === 'waterfall' && node.ghost && isTimed(node) ? projectSpan(node.startMs, node.ghost.durationMs, viewport) : null
  const deltaBar = ghost && isTimed(node) && node.ghost && Math.abs(node.durationMs - node.ghost.durationMs) >= 0.5
    ? projectSpan(node.startMs + Math.min(node.durationMs, node.ghost.durationMs), Math.abs(node.durationMs - node.ghost.durationMs), viewport)
    : null
  const isParent = hasChildren && node.kind !== 'request'
  const source = node.timingSource ? TIMING_SOURCE_LABELS[node.timingSource] : null
  const statusWord = node.statusLabel ?? STATUS_LABEL[node.status]
  const statusText = node.statusCode != null ? `${statusWord} · ${node.statusCode}` : statusWord
  const cls = [
    'wf-row',
    `wf-kind-${node.kind}`,
    `wf-status-${node.status}`,
    selected ? 'selected' : '',
    active ? 'active' : '',
    critical ? 'critical' : '',
  ].filter(Boolean).join(' ')
  return (
    <div
      id={domId}
      role="row"
      className={cls}
      aria-level={depth + 1}
      aria-posinset={row.posInSet}
      aria-setsize={row.setSize}
      aria-expanded={hasChildren ? expanded : undefined}
      aria-selected={selected}
      data-node-id={node.id}
      aria-rowindex={rowIndex}
      aria-describedby={describedBy}
      style={translateY == null ? undefined : { position: 'absolute', top: 0, left: 0, right: 0, transform: `translateY(${translateY}px)` }}
      // Keyboard focus stays on the grid (aria-activedescendant); clicks are delegated to it.
      tabIndex={-1}
    >
      <div role="gridcell" className="wf-cell wf-cell-name" style={{ paddingInlineStart: `calc(6px + ${Math.min(depth, 8)} * var(--wf-indent, 12px))` }}>
        {hasChildren ? (
          <button
            type="button"
            tabIndex={-1}
            className="wf-toggle"
            aria-expanded={expanded}
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${node.label}`}
            // Keep focus on the treegrid so arrow keys keep working.
            onMouseDown={e => e.preventDefault()}
            onClick={e => {
              e.stopPropagation()
              onToggle(node)
            }}
          >
            {expanded ? '▾' : '▸'}
          </button>
        ) : (
          <span className="wf-toggle-spacer" aria-hidden="true" />
        )}
        <span className={`wf-status-dot ${node.status}`} aria-hidden="true" />
        <span className="wf-name-text">
          <span className="wf-name-label" title={node.detail ? `${node.label}\n${node.detail}` : node.label}>{node.label}</span>
          {node.detail && node.kind !== 'iteration' && <small>{node.detail}</small>}
        </span>
        {source && (node.kind === 'request' || node.kind === 'hop') && (
          <span className={`wf-badge wf-source-${node.timingSource}`} data-source={node.timingSource}>
            {source.label}
            <span className="sr-only"> timing: {source.description}</span>
          </span>
        )}
        {/* Shown only when narrow docks fold the status column away. */}
        {node.statusCode != null && <span className={`wf-code ${node.status}`} aria-hidden="true">{node.statusCode}</span>}
        {/* A custom status (an aggregate row's error share, "No samples") folds here the same way. */}
        {node.statusCode == null && node.statusLabel && node.statusLabel !== STATUS_LABEL[node.status] && (
          <span className={`wf-code wf-code-label ${node.status}`} aria-hidden="true">{node.statusLabel}</span>
        )}
        {node.redirect && node.redirect.count > 0 && (
          <span className="wf-badge wf-redirect">
            <span aria-hidden="true">↪ {node.redirect.count}</span>
            <span className="sr-only">{node.redirect.count} {node.redirect.count === 1 ? 'redirect' : 'redirects'}</span>
          </span>
        )}
        {critical && <span className="wf-critical-mark" aria-hidden="true">◆</span>}
        {critical && <span className="sr-only">On critical path</span>}
      </div>
      <div role="gridcell" className="wf-cell wf-cell-status">
        <span className={`wf-status-text ${node.status}`}>{statusText}</span>
      </div>
      <div role="gridcell" className={`wf-cell wf-cell-duration${node.delta ? ' has-delta' : ''}`}>
        <span>{node.durationMs == null ? '—' : formatWaterfallMs(node.durationMs)}</span>
        {node.delta && (
          <small className={`wf-delta-text ${deltaTone(node.delta)}`}>
            {formatDelta(node.delta)}
            <span className="sr-only"> vs baseline: {node.delta.label}</span>
          </small>
        )}
      </div>
      <div
        role="gridcell"
        className="wf-cell wf-cell-timeline"
        aria-label={f ? `${node.label}: ${formatWaterfallMs(node.durationMs ?? 0)}` : `${node.label}: no timing`}
        onPointerMove={e => onTimelinePointer(node, e)}
        onPointerLeave={() => onTimelinePointer(node, null)}
      >
        <div className="wf-track-area">
          {scaleMode === 'waterfall' && ticks.map(t => <span key={t.ms} className="wf-grid" style={{ left: pct(t.pct) }} aria-hidden="true" />)}
          {f?.visible && (
            <span
              id={barId}
              className={`wf-bar${isParent ? ' wf-bar-parent' : ''}${node.kind === 'think' ? ' wf-bar-think' : ''}${node.kind === 'marker' ? ' wf-bar-marker' : ''}${node.derived ? ' wf-bar-derived' : ''}`}
              style={{ left: pct(f.leftPct), width: pct(f.widthPct) }}
              aria-hidden="true"
            >
              {isParent ? <ParentTrack node={node} hidden={hiddenPhases} /> : node.kind === 'think' || node.kind === 'marker' ? null : <LeafBar node={node} hidden={hiddenPhases} />}
            </span>
          )}
          {/* After the bar so the change stays visible on top of it. */}
          {ghost && <span className="wf-ghost" style={{ left: pct(ghost.leftPct), width: pct(ghost.widthPct) }} aria-hidden="true" />}
          {deltaBar && node.delta && <span className={`wf-delta-bar ${deltaTone(node.delta)}`} style={{ left: pct(deltaBar.leftPct), width: pct(deltaBar.widthPct) }} aria-hidden="true" />}
          {scaleMode === 'stacked' && node.durationMs != null && <span className="wf-bar-label" aria-hidden="true">{formatWaterfallMs(node.durationMs)}</span>}
          {!f && <span className="wf-untimed">{node.status === 'not_run' ? 'Not run' : 'No timing'}</span>}
        </div>
      </div>
    </div>
  )
}

export const WaterfallRow = memo(WaterfallRowInner) as typeof WaterfallRowInner
