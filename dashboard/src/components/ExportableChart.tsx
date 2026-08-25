import { type ReactNode, useRef, useState } from 'react'

import { downloadBlob } from '../utils/fileUtils'
import './ExportableChart.css'
import { buildChartCSV, buildChartSVG, type ChartExportColumn } from './exportableChartUtils'

interface Props<T> {
  definitionId: string
  title: string
  description: string
  rows: readonly T[]
  columns: readonly ChartExportColumn<T>[]
  note?: string
  children: ReactNode
}

function safeChartFilename(title: string, definitionId: string): string {
  const source = `${title}-${definitionId}`.trim().toLowerCase()
  return source.replace(/[^a-z0-9._-]+/g, '-').replace(/-+/g, '-').replace(/^-+|-+$/g, '') || 'apexload-chart'
}

export default function ExportableChart<T>({ definitionId, title, description, rows, columns, note, children }: Props<T>) {
  const chartRef = useRef<HTMLDivElement>(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const filename = safeChartFilename(title, definitionId)

  const exportCSV = async () => {
    setBusy('csv')
    setError('')
    try {
      await downloadBlob(new Blob([buildChartCSV(rows, columns)], { type: 'text/csv;charset=utf-8' }), `${filename}.csv`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Unable to export chart CSV')
    } finally {
      setBusy('')
    }
  }

  const exportSVG = async () => {
    const svg = chartRef.current?.querySelector('svg')
    if (!svg) {
      setError('The chart is not ready to export yet.')
      return
    }
    setBusy('svg')
    setError('')
    try {
      await downloadBlob(new Blob([buildChartSVG(svg, title, description)], { type: 'image/svg+xml;charset=utf-8' }), `${filename}.svg`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Unable to export chart SVG')
    } finally {
      setBusy('')
    }
  }

  return (
    <figure className="report-chart-card exportable-chart" aria-label={description} data-chart-definition={definitionId}>
      <div className="exportable-chart-heading">
        <figcaption>{title}</figcaption>
        <div className="chart-export-actions" aria-label={`${title} export actions`}>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => void exportCSV()} disabled={Boolean(busy)} aria-label={`Export ${title} data as CSV`}>
            {busy === 'csv' ? 'Exporting…' : 'CSV'}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => void exportSVG()} disabled={Boolean(busy)} aria-label={`Export ${title} image as SVG`}>
            {busy === 'svg' ? 'Exporting…' : 'SVG'}
          </button>
        </div>
      </div>
      <p className="sr-only">{description}</p>
      {note && <p className="report-chart-note">{note}</p>}
      <div ref={chartRef}>{children}</div>
      {error && <p className="run-report-error" role="alert">{error}</p>}
    </figure>
  )
}
