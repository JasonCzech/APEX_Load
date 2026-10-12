// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MetricsHistoryPoint, TestSummary } from '../../types'
import RunExecutionDetails from './RunExecutionDetails'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const captured = vi.hoisted(() => ({ chart: [] as Array<Record<string, number | undefined>>, exportRows: [] as Array<Record<string, unknown>> }))

vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ComposedChart: ({ children, data }: { children?: ReactNode; data: Array<Record<string, number | undefined>> }) => { captured.chart = data; return <div>{children}</div> },
  CartesianGrid: () => null,
  Tooltip: () => null,
  ReferenceArea: () => null,
  ReferenceLine: () => null,
  XAxis: () => null,
  YAxis: () => null,
  Area: () => null,
  Line: () => null,
}))
vi.mock('../SeriesChartExport', () => ({
  default: ({ rows, children }: { rows: Array<Record<string, unknown>>; children?: ReactNode }) => { captured.exportRows = rows; return <div>{children}</div> },
}))
vi.mock('../TelemetryPolicy', () => ({ default: () => <section aria-label="Telemetry budget policy" /> }))
vi.mock('../TelemetryAcceptanceVerdict', () => ({ default: () => null }))

const START = Date.parse('2026-10-11T22:00:00Z')
const run: TestSummary = {
  id: 'run-1', name: 'Run', status: 'COMPLETE', duration: '2m', total_vusers: 4, created_at: '',
  started_at: '2026-10-11T22:00:00Z', ended_at: '2026-10-11T22:02:00Z',
  groups: [{ name: 'Browse', script_id: 's', script_name: 'Browse', protocol: 'http', vusers: 4, ramp_up: '40s' }],
} as TestSummary
const manifestModel = { manifest: null, manifestError: '', manifestLoading: false, reloadManifest: vi.fn() } as never

describe('RunExecutionDetails load timeline', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    captured.chart = []
    captured.exportRows = []
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('draws the plan as the engine ran it with the observed samples on the run clock', async () => {
    const history = [2000, 12_000].map((offset, index) => ({ time: '', timestampMs: START + offset, vusers: index + 1 }) as MetricsHistoryPoint)
    await act(async () => root.render(<RunExecutionDetails test={run} report={null} history={history} manifestModel={manifestModel} />))

    const timeline = container.querySelector('[aria-label="Execution load timeline"]')!
    expect(timeline.textContent).toContain("The plan is the engine's schedule")
    expect(timeline.textContent).not.toContain('not a ramp schedule')
    expect(timeline.querySelector('.load-plan-legend')?.textContent).toBe('BrowseObserved VUsers')
    // One VUser every 10 s; the samples sit at 2 s and 12 s from the start.
    expect(captured.chart.filter(row => row.observedVusers !== undefined).map(row => [row.second, row.group0, row.observedVusers])).toEqual([[2, 1, 1], [12, 2, 2]])
    // The export holds the planned steps and the observed samples.
    expect(captured.exportRows.filter(row => 'planned' in row).map(row => [row.second, row.planned])).toEqual(expect.arrayContaining([[0, 1], [10, 2], [20, 3], [30, 4]]))
    expect(captured.exportRows.filter(row => 'vusers' in row).map(row => [row.second, row.vusers])).toEqual([[2, 1], [12, 2]])
  })

  it('still draws and exports the plan when no samples were retained', async () => {
    await act(async () => root.render(<RunExecutionDetails test={run} report={null} history={[]} manifestModel={manifestModel} />))

    expect(container.textContent).toContain('No interval history was retained.')
    expect(container.querySelector('.load-plan-legend')?.textContent).toBe('Browse')
    expect(captured.exportRows.length).toBeGreaterThan(0)
    expect(captured.exportRows.every(row => 'planned' in row)).toBe(true)
  })

  it('says when the plan moves on by the run\'s pauses, and when it cannot place them', async () => {
    const paused = { ...run, cumulative_pause_ns: 30e9 } as TestSummary
    const withEvents = { ...(manifestModel as object), manifest: { runtime_changes: [
      { id: 'p', action: 'test.pause', label: 'Run paused', status: 'success', created_at: '2026-10-11T22:00:30Z' },
      { id: 'r', action: 'test.resume', label: 'Run resumed', status: 'success', created_at: '2026-10-11T22:01:00Z' },
    ] } } as never
    await act(async () => root.render(<RunExecutionDetails test={paused} report={null} history={[]} manifestModel={withEvents} />))
    expect(container.querySelector('[aria-label="Execution load timeline"]')?.textContent).toContain('paused for 30s and made it up, so the plan moves on by its pauses')
    await act(async () => root.render(<RunExecutionDetails test={paused} report={null} history={[]} manifestModel={manifestModel} />))
    expect(container.querySelector('[aria-label="Execution load timeline"]')?.textContent).toContain('do not say when, so the plan is drawn without the pause')
  })

  it('keeps the telemetry receipt with the manifest on Execution, and in view on the evidence tabs', async () => {
    const withManifest = { ...(manifestModel as object), manifest: { telemetry_policy: {}, runtime_changes: [] } } as never
    await act(async () => root.render(<RunExecutionDetails test={run} report={null} history={[]} manifestModel={withManifest} />))
    expect(container.querySelector('[aria-label="Telemetry budget policy"]')?.closest('details')?.querySelector('summary')?.textContent).toBe('Configuration and infrastructure evidence')

    await act(async () => root.render(<RunExecutionDetails test={run} report={null} manifestModel={withManifest} evidenceOnly />))
    expect(container.querySelectorAll('[aria-label="Telemetry budget policy"]')).toHaveLength(1)
    expect(container.querySelector('[aria-label="Telemetry budget policy"]')?.closest('details')).toBeNull()
  })
})
