import { useLayoutEffect, useRef, useState } from 'react'
import { phaseTotals, selfChildTime, timingChildren } from './analysis'
import { TIMING_SOURCE_LABELS, WATERFALL_PHASES, formatWaterfallMs, type WaterfallPhaseKey } from './phases'
import type { WaterfallNode } from './types'

export interface WaterfallTooltipState {
  nodeId: string
  phase?: WaterfallPhaseKey
  gapMs?: number
  childId?: string
  x: number
  y: number
}

const TOOLTIP_WIDTH = 300

/**
 * Floating, non-interactive tooltip shared by pointer hover and keyboard focus.
 * `showStart` false leaves out the "starts +…" offset, for rows that share no
 * timeline (independent aggregates all drawn from 0).
 */
export function WaterfallTooltip({ id, className, node, state, critical, phaseLabels, showStart = true }: { id: string; className?: string; node: WaterfallNode<unknown>; state: WaterfallTooltipState; critical: boolean; phaseLabels?: Partial<Record<WaterfallPhaseKey, string>>; showStart?: boolean }) {
  const totals = phaseTotals(node)
  const phases = WATERFALL_PHASES.filter(p => (totals.get(p.key) ?? 0) > 0)
  const split = selfChildTime(node)
  const child = state.childId ? timingChildren(node).find(c => c.id === state.childId) : undefined
  const vw = typeof window !== 'undefined' ? window.innerWidth : 1024
  const vh = typeof window !== 'undefined' ? window.innerHeight : 768
  const left = Math.max(8, Math.min(state.x + 14, vw - TOOLTIP_WIDTH - 8))
  // Measure the real height, then place below the pointer, flip above, or clamp.
  const ref = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState(0)
  useLayoutEffect(() => {
    const h = ref.current?.getBoundingClientRect().height ?? 0
    if (Math.abs(h - height) > 0.5) setHeight(h)
  }, [node, state, critical, height])
  const below = state.y + 18
  const above = state.y - 12 - height
  const top = below + height + 8 <= vh ? below : above >= 8 ? above : Math.max(8, vh - height - 8)
  const source = node.timingSource ? TIMING_SOURCE_LABELS[node.timingSource] : null
  return (
    <div ref={ref} id={id} role="tooltip" className={`wf-tooltip${className ? ` ${className}` : ''}`} style={{ left, top, width: TOOLTIP_WIDTH, maxBlockSize: 'calc(100vh - 16px)', overflow: 'hidden' }}>
      <strong>{node.label}</strong>
      <div className="wf-tooltip-meta">
        {node.statusCode != null ? `HTTP ${node.statusCode} · ` : ''}
        {node.durationMs != null ? formatWaterfallMs(node.durationMs) : 'No timing'}
        {showStart && node.startMs != null && ` · starts +${formatWaterfallMs(node.startMs)}`}
        {source && ` · ${source.label}`}
        {node.connectionReused ? ' · reused connection' : ''}
        {critical ? ' · critical path' : ''}
      </div>
      {node.delta && (
        <div className="wf-tooltip-gap">
          vs baseline: {node.delta.label}
          {node.ghost ? ` (baseline ${formatWaterfallMs(node.ghost.durationMs)})` : ''}
        </div>
      )}
      {state.gapMs != null && <div className="wf-tooltip-gap">Idle gap: {formatWaterfallMs(state.gapMs)} with no child running (think time, pacing or client work)</div>}
      {child && <div className="wf-tooltip-gap">{child.label}: {formatWaterfallMs(child.durationMs ?? 0)}</div>}
      {node.kind === 'think' && !node.tooltipNote && <div className="wf-tooltip-gap">Think time: a scripted pause, not network time</div>}
      {node.tooltipNote && <div className="wf-tooltip-gap">{node.tooltipNote}</div>}
      {node.kind === 'marker' && <div className="wf-tooltip-gap">Transaction marker: bookkeeping, not network time</div>}
      {phases.length > 0 && !(phases.length === 1 && phases[0].key === 'total') && (
        <ul>
          {phases.map(p => (
            <li key={p.key} className={state.phase === p.key ? 'current' : undefined}>
              <i className={`wf-swatch phase-${p.key}`} aria-hidden="true" />
              <span>{phaseLabels?.[p.key] ?? p.label}</span>
              <span>{formatWaterfallMs(totals.get(p.key) ?? 0)}</span>
            </li>
          ))}
        </ul>
      )}
      {split && (
        <div className="wf-tooltip-meta">
          Child time {formatWaterfallMs(split.childMs)} · self time {formatWaterfallMs(split.selfMs)}
          {split.overlapMs > 0 ? ` · ${formatWaterfallMs(split.overlapMs)} overlapping` : ''}
        </div>
      )}
      {node.redirect && node.redirect.count > 0 && <div className="wf-tooltip-meta">{node.redirect.count} redirects · {formatWaterfallMs(node.redirect.ms)}</div>}
      {node.errorMessage && <div className="wf-tooltip-error">{node.errorMessage}</div>}
      {source && node.timingSource !== 'har' && <div className="wf-tooltip-note">{source.description}</div>}
    </div>
  )
}
