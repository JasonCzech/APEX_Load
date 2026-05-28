import { type CSSProperties, useMemo, useState } from 'react'

import type { MetricsHistoryPoint } from '../types'
import LoadImpact3DHeatmap from './LoadImpact3DHeatmap'
import {
  buildLoadImpactModel,
  formatLoadImpactCellTitle,
  formatLoadImpactValue,
  loadImpactCellKey,
} from './loadImpactModel'
import './LoadImpactHeatmap.css'

interface Props {
  points: MetricsHistoryPoint[]
  title?: string
  subtitle?: string
  loading?: boolean
  error?: string
  emptyMessage?: string
}

function formatPointTimestamp(point: MetricsHistoryPoint) {
  if (typeof point.timestampMs === 'number') {
    return new Date(point.timestampMs).toLocaleString()
  }
  return point.time || 'n/a'
}

function relatedValue(point: MetricsHistoryPoint, key: keyof MetricsHistoryPoint, unit?: string) {
  const value = point[key]
  return typeof value === 'number' && Number.isFinite(value) ? formatLoadImpactValue(value, unit) : 'n/a'
}

export default function LoadImpactHeatmap({
  points,
  title = 'Load Impact',
  subtitle = 'Performance and resource behavior grouped by active VUser load.',
  loading = false,
  error = '',
  emptyMessage = 'Load impact appears after metrics include active VUsers and performance or resource samples.',
}: Props) {
  const [selectedCellKey, setSelectedCellKey] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'2d' | '3d'>('2d')
  const model = useMemo(() => buildLoadImpactModel(points), [points])
  const selectedCell = useMemo(() => {
    if (!selectedCellKey) return null
    for (const row of model.rows) {
      const cell = row.cells.find(candidate => loadImpactCellKey(candidate) === selectedCellKey)
      if (cell) return cell
    }
    return null
  }, [model.rows, selectedCellKey])

  return (
    <section className="load-impact-card" aria-label={title}>
      <div className="load-impact-header">
        <div>
          <h3 className="chart-title">{title}</h3>
          <p className="load-impact-copy">{subtitle}</p>
        </div>
        <div className="load-impact-header-actions">
          {!loading && model.rows.length > 0 && model.buckets.length > 0 && (
            <div className="load-impact-view-toggle" role="tablist" aria-label="Load Impact view mode">
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '2d'}
                className={`btn-chip ${viewMode === '2d' ? 'active' : ''}`}
                onClick={() => setViewMode('2d')}
              >
                2D
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '3d'}
                className={`btn-chip ${viewMode === '3d' ? 'active' : ''}`}
                onClick={() => setViewMode('3d')}
              >
                3D
              </button>
            </div>
          )}
          <div className="load-impact-badges">
            {loading && <span className="archive-chip">Loading</span>}
            {!loading && model.pointCount > 0 && <span className="archive-chip">{model.pointCount} samples</span>}
            {!loading && model.buckets.length > 0 && <span className="archive-chip">{model.buckets.length} load buckets</span>}
          </div>
        </div>
      </div>

      {error && <p className="scenario-script-status error">{error}</p>}

      {loading ? (
        <div className="load-impact-state" role="status" aria-live="polite">
          Loading load impact samples...
        </div>
      ) : model.rows.length === 0 || model.buckets.length === 0 ? (
        <div className="load-impact-state">
          {emptyMessage}
        </div>
      ) : (
        <>
          {viewMode === '3d' ? (
            <LoadImpact3DHeatmap
              model={model}
              selectedCellKey={selectedCellKey}
              onSelectedCellKeyChange={setSelectedCellKey}
            />
          ) : (
            <div className="load-impact-table-wrap">
              <table className="load-impact-table">
                <thead>
                  <tr>
                    <th scope="col">Metric</th>
                    {model.buckets.map(bucket => (
                      <th key={bucket.id} scope="col">
                        <span>{bucket.label}</span>
                        <small>{bucket.sampleCount} total</small>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {model.rows.map(row => (
                    <tr key={row.metric.id}>
                      <th scope="row">
                        <span>{row.metric.label}</span>
                        {row.metric.unit && <small>{row.metric.unit}</small>}
                      </th>
                      {row.cells.map(cell => {
                        const selected = selectedCellKey === loadImpactCellKey(cell)
                        const alpha = cell.sampleCount > 0 ? 0.12 + cell.intensity * 0.68 : 0
                        const title = formatLoadImpactCellTitle(cell)
                        return (
                          <td key={cell.bucket.id}>
                            <button
                              type="button"
                              className={`load-impact-cell${selected ? ' selected' : ''}`}
                              disabled={cell.sampleCount === 0}
                              title={title}
                              aria-label={`${cell.metric.label} at ${cell.bucket.label}: ${title}`}
                              onClick={() => setSelectedCellKey(loadImpactCellKey(cell))}
                              style={{
                                '--load-impact-alpha': alpha,
                              } as CSSProperties}
                            >
                              <strong>{formatLoadImpactValue(cell.average, cell.metric.unit)}</strong>
                              <span>{cell.sampleCount}</span>
                            </button>
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {selectedCell && selectedCell.sampleCount > 0 && (
            <div className="load-impact-detail">
              <div className="load-impact-detail-header">
                <div>
                  <h4>{selectedCell.metric.label} at {selectedCell.bucket.label}</h4>
                  <p>
                    Avg {formatLoadImpactValue(selectedCell.average, selectedCell.metric.unit)} · Min {formatLoadImpactValue(selectedCell.min, selectedCell.metric.unit)} · Max {formatLoadImpactValue(selectedCell.max, selectedCell.metric.unit)}
                  </p>
                </div>
                <span className="archive-chip">{selectedCell.sampleCount} data points</span>
              </div>
              <div className="load-impact-detail-table-wrap">
                <table className="load-impact-detail-table">
                  <thead>
                    <tr>
                      <th>Timestamp</th>
                      <th>VUsers</th>
                      <th>{selectedCell.metric.label}</th>
                      <th>ART</th>
                      <th>TPS</th>
                      <th>P95</th>
                      <th>Error %</th>
                      <th>CPU</th>
                      <th>Memory</th>
                      <th>Network</th>
                      <th>Disk</th>
                    </tr>
                  </thead>
                  <tbody>
                    {selectedCell.points.map((point, index) => (
                      <tr key={`${point.timestampMs ?? point.time}-${index}`}>
                        <td className="tx-name">{formatPointTimestamp(point.source)}</td>
                        <td>{point.vusers}</td>
                        <td>{formatLoadImpactValue(point.value, selectedCell.metric.unit)}</td>
                        <td>{relatedValue(point.source, 'avgMs', 'ms')}</td>
                        <td>{relatedValue(point.source, 'tps')}</td>
                        <td>{relatedValue(point.source, 'p95', 'ms')}</td>
                        <td>{relatedValue(point.source, 'errorPct', '%')}</td>
                        <td>{relatedValue(point.source, 'cpuProcess', '%')}</td>
                        <td>{relatedValue(point.source, 'memProcess', 'MB')}</td>
                        <td>{relatedValue(point.source, 'netSent', 'KB/s')} / {relatedValue(point.source, 'netRecv', 'KB/s')}</td>
                        <td>{relatedValue(point.source, 'diskRead', 'KB/s')} / {relatedValue(point.source, 'diskWrite', 'KB/s')}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  )
}
