import { useId } from 'react'

import {
  ANATOMY_PHASE_DEFINITIONS,
  ANATOMY_PHASE_LABELS,
  ANATOMY_PHASE_ORDER,
  OUTSIDE_REQUESTS_DEFINITION,
  OUTSIDE_REQUESTS_LABEL,
  flattenNodes,
  formatAnatomyMs,
  reportPhaseRowId,
  type AnatomyNode,
  type AnatomyPhaseKey,
} from './timeAnatomyModel'

interface Props<T> {
  roots: AnatomyNode<T>[]
  spanMs: number
  ariaLabel: string
  selectedId?: string
  onSelect?: (id: string) => void
  /** Prefix for each row's DOM id, so a caller can scroll a row into view. */
  rowIdPrefix?: string
  phaseLabels?: Partial<Record<AnatomyPhaseKey, string>>
}

const pct = (value: number, span: number) => (span > 0 ? Math.max(0, Math.min(100, (value / span) * 100)) : 0)

/** The screen-reader summary: each phase's total once, in drawing order, then the time outside requests. */
function rowSummary<T>(node: AnatomyNode<T>, labels: Record<AnatomyPhaseKey, string>): string {
  const totals = new Map<AnatomyPhaseKey, number>()
  for (const phase of node.phases) totals.set(phase.key, (totals.get(phase.key) ?? 0) + (phase.endMs - phase.startMs))
  const parts = Array.from(totals, ([key, ms]) => `${labels[key]} ${formatAnatomyMs(ms)}`)
  const outsideMs = (node.outside || []).reduce((sum, gap) => sum + (gap.endMs - gap.startMs), 0)
  if (outsideMs > 0) parts.push(`${OUTSIDE_REQUESTS_LABEL.toLowerCase()} ${formatAnatomyMs(outsideMs)}`)
  return parts.length ? parts.join(', ') : formatAnatomyMs(node.durationMs ?? 0)
}

/**
 * Fallback stacked-bar waterfall for the report until the shared
 * `<Waterfall>` lands on main. It takes the same node tree, draws every row
 * (no virtualization) and keeps its colours in `.rr-phase-*` tokens.
 */
export default function ReportPhaseBars<T>({ roots, spanMs, ariaLabel, selectedId, onSelect, rowIdPrefix, phaseLabels }: Props<T>) {
  const fallbackPrefix = useId().replace(/:/g, '')
  const prefix = rowIdPrefix || `rr-bars-${fallbackPrefix}`
  const labels = { ...ANATOMY_PHASE_LABELS, ...phaseLabels }
  const rows = flattenNodes(roots)
  const present = new Set(rows.flatMap(({ node }) => node.phases.map(span => span.key)))
  const legend = ANATOMY_PHASE_ORDER.filter(key => present.has(key))
  const hasOutside = rows.some(({ node }) => node.kind === 'think' || (node.outside?.length ?? 0) > 0)
  const definitions = legend.filter(key => ANATOMY_PHASE_DEFINITIONS[key]).map(key => `${labels[key]}: ${ANATOMY_PHASE_DEFINITIONS[key]}`)
  if (hasOutside) definitions.push(`${OUTSIDE_REQUESTS_LABEL}: ${OUTSIDE_REQUESTS_DEFINITION}`)
  const span = spanMs > 0 ? spanMs : 1
  return (
    <div className="rr-phase-bars">
      {(legend.length > 0 || hasOutside) && <ul className="rr-phase-legend" aria-label="Phase legend">
        {legend.map(key => <li key={key} title={ANATOMY_PHASE_DEFINITIONS[key]}><span className={`rr-phase-swatch rr-phase-${key}`} aria-hidden="true" />{labels[key]}</li>)}
        {hasOutside && <li title={OUTSIDE_REQUESTS_DEFINITION}><span className="rr-phase-swatch rr-phase-gap" aria-hidden="true" />{OUTSIDE_REQUESTS_LABEL}</li>}
      </ul>}
      {definitions.length > 0 && <p className="rr-phase-definitions">{definitions.join(' ')}</p>}
      <ul className="rr-phase-rows" aria-label={ariaLabel}>
        {rows.map(({ node, depth }) => {
          const start = node.startMs ?? 0
          const duration = node.durationMs ?? 0
          const summary = rowSummary(node, labels)
          const drawn = node.phases.length > 0 || (node.outside?.length ?? 0) > 0
          const selected = selectedId === node.id
          const className = [
            'rr-phase-row',
            `rr-phase-row-${node.kind}`,
            node.derived ? 'rr-phase-row-muted' : '',
            node.status === 'failed' ? 'rr-phase-row-failed' : '',
            selected ? 'selected' : '',
          ].filter(Boolean).join(' ')
          const content = <>
            <span className="rr-phase-name" style={{ paddingInlineStart: `${depth * 14}px` }}>
              <span className="rr-phase-label">{node.label}</span>
              {node.detail && <small className="rr-phase-detail">{node.detail}</small>}
            </span>
            <span className="rr-phase-track" aria-hidden="true">
              {drawn
                ? <>
                  {node.phases.map((phase, index) => <span key={`${phase.key}-${index}`} className={`rr-phase-seg rr-phase-${phase.key}`}
                    style={{ left: `${pct(start + phase.startMs, span)}%`, width: `${Math.max(0.3, pct(phase.endMs - phase.startMs, span))}%` }} />)}
                  {(node.outside || []).map((gap, index) => <span key={`outside-${index}`} className="rr-phase-seg rr-phase-gap"
                    style={{ left: `${pct(start + gap.startMs, span)}%`, width: `${pct(gap.endMs - gap.startMs, span)}%` }} />)}
                </>
                : duration > 0 && <span className={`rr-phase-seg ${node.kind === 'think' ? 'rr-phase-gap' : 'rr-phase-parent'}`}
                  style={{ left: `${pct(start, span)}%`, width: `${Math.max(0.3, pct(duration, span))}%` }} />}
            </span>
            <span className="rr-phase-duration">{formatAnatomyMs(duration)}</span>
            <span className="sr-only">{`, starts at ${formatAnatomyMs(start)}: ${summary}`}{node.status === 'failed' ? ', failed' : ''}</span>
          </>
          return <li key={node.id} id={reportPhaseRowId(prefix, node.id)} className={className}>
            {onSelect && node.kind !== 'think'
              ? <button type="button" className="rr-phase-row-button" aria-pressed={selected} onClick={() => onSelect(node.id)}>{content}</button>
              : <div className="rr-phase-row-button">{content}</div>}
          </li>
        })}
      </ul>
    </div>
  )
}
