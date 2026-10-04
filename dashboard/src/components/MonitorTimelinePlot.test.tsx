// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { AnalysisPoint } from '../types/analysis'
import MonitorTimelinePlot from './MonitorTimelinePlot'

interface ExportRow { time: number; monitor?: number | null; point?: AnalysisPoint }
interface ExportProps {
  rows: ExportRow[]
  axes: { key: string; value: (row: ExportRow) => unknown }[]
  series: { key: string }[]
  children: ReactNode
}
const capture = vi.hoisted(() => ({ value: undefined as ExportProps | undefined }))
vi.mock('./SeriesChartExport', () => ({ default: (props: ExportProps) => { capture.value = props; return <div>{props.children}</div> } }))
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  LineChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Line: () => null, XAxis: () => null, YAxis: () => null, Tooltip: () => null,
}))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
beforeEach(() => { container = document.createElement('div'); root = createRoot(container); capture.value = undefined })
afterEach(() => act(() => root.unmount()))

it('retains displayed zero and provenance in the chart export', async () => {
  const point: AnalysisPoint = { timestamp_utc: '2026-10-03T12:00:00Z', source: 'source', metric_name: 'calls', unit: 'count', value: 0, quality: 'valid', host: 'node', dimensions: { application: '1' } }
  await act(async () => root.render(<MonitorTimelinePlot points={[point]} history={[]} />))
  const exported = capture.value!
  expect(exported.rows).toHaveLength(1)
  expect(exported.rows[0].monitor).toBe(0)
  expect(exported.axes.find(axis => axis.key === 'quality')?.value(exported.rows[0])).toBe('valid')
  expect(exported.axes.find(axis => axis.key === 'dimensions')?.value(exported.rows[0])).toEqual({ application: '1' })
  expect(exported.series.map(series => series.key)).toEqual(['monitor', 'load'])
})

it('exports only the displayed latest whole-query aggregate', async () => {
  const point: AnalysisPoint = { timestamp_utc: '2026-10-03T12:00:00Z', metric_name: 'calls', unit: 'count', value: 10, quality: 'valid', provider: { name: 'appdynamics', metric_id: 'analytics', bucket_start: '2026-10-03T12:00:00Z', bucket_end: '2026-10-03T12:01:00Z', resolution_seconds: 60, statistic: 'sum', settled: true, aggregation_eligible: true, query_window: true } }
  const latest = { ...point, value: 0, provider: { ...point.provider!, bucket_end: '2026-10-03T12:02:00Z', resolution_seconds: 120 } }
  await act(async () => root.render(<MonitorTimelinePlot points={[point, latest]} history={[]} />))
  expect(container.textContent).toContain('Most recent query window')
  expect(capture.value!.rows).toHaveLength(1)
  expect(capture.value!.rows[0].monitor).toBe(0)
  expect(capture.value!.series.map(series => series.key)).toEqual(['monitor'])
})
