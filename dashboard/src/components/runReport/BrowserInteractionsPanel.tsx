import { useEffect, useMemo, useState } from 'react'

import { request } from '../../hooks/apiClient'
import { getErrorMessage } from '../../utils/errorUtils'

// Browser interaction patterns and heatmaps (E4) from a run's archived
// interaction evidence: measured patterns with their thresholds, and an
// abstract dispatch grid per compatible layout.

export interface BrowserInteractionPattern {
  id: string; kind: string; provenance: string; route?: string; layout_revision?: string; action_id?: string; action_name?: string
  count: number; start: string; end: string; declared?: string; deadline_ms?: number; detail: string; interactions: string[]
}
export interface BrowserInteractionHeatmap {
  key: string; route: string; layout_revision: string; viewport_width: number; viewport_height: number; orientation: string
  engine: string; device_profile: string; columns: number; rows: number; cells: [number, number, number][]; points: number
  targets?: { role: string; label_sha256?: string; count: number }[]; by_kind: Record<string, number>; by_provenance: Record<string, number>; notes?: string[]
}
export interface BrowserInteractionAnalysis {
  run_id: string; version: string; status: 'ready' | 'not_captured' | string
  thresholds: { repeat_min_gestures: number; repeat_window_ms: number; repeat_radius_px: number; heatmap_grid: number }
  coverage: { interactions: number; records: number; dropped: number; incomplete: boolean; by_provenance: Record<string, number>; mapped: number; excluded?: Record<string, number> }
  patterns: BrowserInteractionPattern[]; heatmaps: BrowserInteractionHeatmap[]; notes: string[]; limitations?: string[]
}

export const browserInteractionsApi = {
  get: (runId: string, signal?: AbortSignal) =>
    request<BrowserInteractionAnalysis>(`/tests/${encodeURIComponent(runId)}/archive/browser-interactions`, { signal }),
}

const PATTERN_LABELS: Record<string, string> = {
  scripted_repetition: 'Scripted repetition',
  repeated_gesture: 'Repeated gesture (recorded)',
  retry: 'Retry',
  unexpected_navigation: 'Unexpected navigation',
  dead_interaction: 'Dead interaction',
}

const label = (value: string) => value.replace(/_/g, ' ')

// HeatmapGrid draws the occupied cells of one map on its abstract grid; the
// cell's opacity follows its share of the busiest cell.
export function HeatmapGrid({ map }: { map: BrowserInteractionHeatmap }) {
  const max = useMemo(() => map.cells.reduce((value, cell) => Math.max(value, cell[2]), 0), [map.cells])
  const aspect = map.viewport_height / Math.max(1, map.viewport_width)
  const width = 256
  const height = Math.round(width * aspect)
  return (
    <svg role="img" aria-label={`Dispatch grid for ${map.route || 'unknown route'}: ${map.points} gestures in ${map.cells.length} cells`} width={width} height={height}
      viewBox={`0 0 ${map.columns} ${map.rows}`} preserveAspectRatio="none" className="interaction-heatmap">
      <rect x={0} y={0} width={map.columns} height={map.rows} fill="var(--bg-elevated, transparent)" stroke="var(--border-color, currentColor)" strokeWidth={0.2} />
      {map.cells.map(([row, column, count]) => (
        <rect key={`${row}-${column}`} data-cell={`${row},${column}`} x={column} y={row} width={1} height={1} fill="var(--accent, #2563eb)" fillOpacity={0.2 + 0.8 * (count / Math.max(1, max))}>
          <title>{`${count} gesture${count === 1 ? '' : 's'}`}</title>
        </rect>
      ))}
    </svg>
  )
}

export default function BrowserInteractionsPanel({ testId }: { testId: string }) {
  const [analysis, setAnalysis] = useState<BrowserInteractionAnalysis | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    browserInteractionsApi.get(testId, controller.signal)
      .then(next => { if (!controller.signal.aborted) setAnalysis(next) })
      .catch(err => { if (!controller.signal.aborted) { setAnalysis(null); setError(getErrorMessage(err, 'Unable to load interaction patterns')) } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [testId])

  if (loading) return <p role="status">Loading interaction patterns…</p>
  if (error) return <p className="run-report-error" role="alert">{error}</p>
  if (!analysis || analysis.status === 'not_captured') return <section className="run-report-panel" aria-label="Interaction patterns"><h3>Interaction patterns</h3><p>This run recorded no interaction evidence.</p></section>
  const { coverage, thresholds } = analysis
  const excluded = Object.entries(coverage.excluded || {})
  return (
    <section className="run-report-panel" aria-label="Interaction patterns">
      <h3>Interaction patterns</h3>
      <p className="run-report-muted">
        {coverage.interactions} recorded gestures ({Object.entries(coverage.by_provenance).map(([provenance, count]) => `${count} ${provenance}`).join(', ') || 'none'}); {coverage.mapped} mapped.
        {coverage.dropped > 0 ? ` ${coverage.dropped} gestures were dropped by capture bounds.` : ''}{coverage.incomplete ? ' Some interaction evidence is incomplete.' : ''}
        {' '}A repeat is at least {thresholds.repeat_min_gestures} gestures within {thresholds.repeat_window_ms} ms and {thresholds.repeat_radius_px} CSS px.
      </p>
      {analysis.patterns.length === 0 ? <p>No pattern was found.</p> : (
        <table className="run-report-table" aria-label="Interaction patterns found">
          <thead><tr><th scope="col">Pattern</th><th scope="col">Provenance</th><th scope="col">Route</th><th scope="col">Action</th><th scope="col">Gestures</th><th scope="col">Detail</th></tr></thead>
          <tbody>{analysis.patterns.map(pattern => <tr key={pattern.id} data-pattern={pattern.kind}>
            <th scope="row">{PATTERN_LABELS[pattern.kind] || label(pattern.kind)}</th><td>{pattern.provenance}</td><td>{pattern.route || '—'}</td>
            <td>{pattern.action_name || '—'}</td><td>{pattern.count}</td><td>{pattern.detail}</td>
          </tr>)}</tbody>
        </table>
      )}
      {analysis.heatmaps.length > 0 && <div className="interaction-heatmaps">
        {analysis.heatmaps.map(map => <figure key={map.key} data-heatmap={map.key}>
          {map.points > 0 && <HeatmapGrid map={map} />}
          <figcaption>
            {map.route || 'Unknown route'} · {map.viewport_width}×{map.viewport_height} {map.orientation} · {map.engine} / {map.device_profile} · layout {map.layout_revision || 'unknown'} · {map.points} pointer gestures
            {map.notes?.map(note => <span key={note} className="run-report-muted"> · {note}</span>)}
          </figcaption>
          {(map.targets?.length ?? 0) > 0 && <table className="run-report-table" aria-label="Keyboard targets"><thead><tr><th scope="col">Target role</th><th scope="col">Label (hashed)</th><th scope="col">Gestures</th></tr></thead>
            <tbody>{map.targets!.map(target => <tr key={`${target.role}-${target.label_sha256 || ''}`}><td>{target.role}</td><td><code>{target.label_sha256 ? target.label_sha256.slice(0, 12) : '—'}</code></td><td>{target.count}</td></tr>)}</tbody></table>}
        </figure>)}
      </div>}
      {excluded.length > 0 && <p className="run-report-muted">Not mapped: {excluded.map(([reason, count]) => `${count} ${label(reason)}`).join(', ')}.</p>}
      {analysis.limitations?.map(limitation => <p key={limitation} className="run-report-muted" role="note">{label(limitation)}</p>)}
      <ul className="run-report-muted">{analysis.notes.map(note => <li key={note}>{note}</li>)}</ul>
    </section>
  )
}
