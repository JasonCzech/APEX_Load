import type { ReactNode } from 'react'
import ExportableChart from './ExportableChart'
import type { ChartExportColumn } from './exportableChartUtils'

export interface ExportSeries { key: string; label: string; unit?: string }

// Export exactly the displayed rows/series. Empty cells retain missing evidence.
export default function SeriesChartExport<T extends object>({ id, title, rows, series, axes, context = {}, note, children }: {
  id: string; title: string; rows: readonly T[]; series: readonly ExportSeries[]
  axes: readonly ChartExportColumn<T>[]; context?: Record<string, unknown>; note?: string; children: ReactNode
}) {
  const columns: ChartExportColumn<T>[] = [
    ...axes,
    ...series.map(item => ({ key: item.key, label: `${item.label}${item.unit ? ` (${item.unit})` : ''} [${item.key}]`, value: (row: T) => Object.prototype.hasOwnProperty.call(row, item.key) ? (row as Record<string, unknown>)[item.key] : undefined })),
    ...Object.entries(context).map(([key, value]) => ({ key: `context:${key}`, label: key, value: () => value })),
  ]
  const description = `${title}. ${note ?? 'Only displayed series and measurements are exported; missing values remain empty.'} ${JSON.stringify(context)}`
  return <ExportableChart hideCaption definitionId={id} title={title} description={description} rows={rows} columns={columns} note={note}>{children}</ExportableChart>
}
