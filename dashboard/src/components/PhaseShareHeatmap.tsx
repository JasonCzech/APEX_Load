import { useId, useMemo, useState, type MouseEventHandler, type ReactNode, type Ref } from 'react'
import type { MetricsHistoryPoint } from '../types'
import { PHASE_SHARE_ROWS, formatShare, phaseShareColumns, phaseShareSummary } from './phaseShareHeatmapModel'
import './PhaseShareHeatmap.css'

interface Props {
  title: string
  points?: readonly MetricsHistoryPoint[]
  className?: string
  dataChartId?: string
  cardRef?: Ref<HTMLElement>
  headerActions?: ReactNode
  onContextMenu?: MouseEventHandler<HTMLElement>
  onMouseEnter?: MouseEventHandler<HTMLElement>
  onMouseLeave?: MouseEventHandler<HTMLElement>
}

/** L6: each network phase's share of the sampled time, interval by interval. */
export default function PhaseShareHeatmap({ title, points, className, dataChartId, cardRef, headerActions, onContextMenu, onMouseEnter, onMouseLeave }: Props) {
  const columns = useMemo(() => phaseShareColumns(points), [points])
  const [showTable, setShowTable] = useState(false)
  const summaryId = useId()
  const merged = columns.some(c => c.intervals > 1)
  const anyReported = columns.some(c => c.reported)
  const summary = phaseShareSummary(columns)
  return (
    <section
      ref={cardRef}
      className={['chart-card', 'phase-share-card', className].filter(Boolean).join(' ')}
      data-chart-id={dataChartId}
      aria-label={title}
      onContextMenu={onContextMenu}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <div className="chart-card-header">
        <div className="chart-title-wrap">
          <h3 className="chart-title">{title}</h3>
        </div>
        {headerActions && <div className="chart-card-actions">{headerActions}</div>}
      </div>
      <p className="phase-share-copy" id={summaryId}>
        Each cell is a phase&apos;s share of the interval&apos;s sampled transaction time, from the phase averages (darker is a larger share).{merged ? ' Neighbouring intervals are merged, each counting equally.' : ''} {summary}
      </p>
      {!anyReported ? (
        <p className="phase-share-empty" role="status">{columns.length ? 'No interval in view reported network phases (load generators that predate them, or protocols that time no phases).' : 'Waiting for live metrics.'}</p>
      ) : (
        <>
          <div className="phase-share-toolbar">
            <button type="button" className={`wf-chip-toggle${showTable ? ' active' : ''}`} aria-pressed={showTable} onClick={() => setShowTable(v => !v)}>
              Show as table
            </button>
            <ul className="phase-share-legend" aria-label="Phases">
              {PHASE_SHARE_ROWS.map(row => (
                <li key={row.key}><span className="phase-share-swatch" style={{ background: row.color }} aria-hidden="true" />{row.label}</li>
              ))}
            </ul>
          </div>
          {showTable ? (
            <div className="phase-share-table-wrap">
              <table className="phase-share-table">
                <caption className="sr-only">{title}: share of sampled time per phase and interval</caption>
                <thead>
                  <tr>
                    <th scope="col">Interval</th>
                    {PHASE_SHARE_ROWS.map(row => <th key={row.key} scope="col">{row.label}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {columns.map((column, index) => (
                    <tr key={`${column.startMs ?? index}-${index}`}>
                      <th scope="row">{column.label}{column.intervals > 1 ? ` (${column.intervals} intervals)` : ''}</th>
                      {PHASE_SHARE_ROWS.map((row, rowIndex) => <td key={row.key}>{column.reported ? formatShare(column.shares[rowIndex]) : 'Not reported'}</td>)}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="phase-share-grid" role="img" aria-describedby={summaryId} aria-label={`${title} heatmap`} style={{ gridTemplateColumns: `minmax(96px, max-content) repeat(${columns.length}, minmax(4px, 1fr))` }}>
              {PHASE_SHARE_ROWS.map((row, rowIndex) => (
                <div key={row.key} className="phase-share-row" style={{ display: 'contents' }}>
                  <span className="phase-share-row-label" aria-hidden="true">{row.label}</span>
                  {columns.map((column, index) => {
                    const share = column.shares[rowIndex]
                    const cellTitle = `${row.label} at ${column.label}: ${column.reported ? formatShare(share) : 'Not reported'}`
                    return (
                      <span
                        key={`${column.startMs ?? index}-${index}`}
                        className={`phase-share-cell${share === undefined ? ' not-reported' : ''}`}
                        title={cellTitle}
                        aria-hidden="true"
                        style={share === undefined ? undefined : { background: `color-mix(in srgb, ${row.color} ${Math.round(share * 100)}%, transparent)` }}
                      />
                    )
                  })}
                </div>
              ))}
              <span aria-hidden="true" />
              <span className="phase-share-axis" aria-hidden="true" style={{ gridColumn: `2 / span ${columns.length}` }}>
                <span>{columns[0]?.label}</span>
                <span>{columns[columns.length - 1]?.label}</span>
              </span>
            </div>
          )}
        </>
      )}
    </section>
  )
}
