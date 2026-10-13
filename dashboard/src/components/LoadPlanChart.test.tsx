// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LoadPlanChart from './LoadPlanChart'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const chartProps = vi.hoisted(() => ({ data: [] as Array<Record<string, number | undefined>>, xTicks: [] as number[], yTicks: [] as number[], tpsTicks: [] as number[], areas: [] as string[], lines: [] as string[], refLines: [] as number[] }))

vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ComposedChart: ({ children, data }: { children?: ReactNode; data: Array<Record<string, number | undefined>> }) => { chartProps.data = data; return <div>{children}</div> },
  CartesianGrid: () => null,
  Tooltip: () => null,
  ReferenceArea: () => null,
  ReferenceLine: ({ x }: { x: number }) => { chartProps.refLines.push(x); return null },
  XAxis: ({ ticks }: { ticks: number[] }) => { chartProps.xTicks = ticks; return null },
  YAxis: ({ ticks, yAxisId }: { ticks?: number[]; yAxisId: string }) => { if (yAxisId === 'vusers') chartProps.yTicks = ticks || []; else chartProps.tpsTicks = ticks || []; return null },
  Area: ({ dataKey, type }: { dataKey: string; type: string }) => { chartProps.areas.push(`${dataKey}:${type}`); return null },
  Line: ({ dataKey, type }: { dataKey: string; type: string }) => { chartProps.lines.push(`${dataKey}:${type}`); return null },
}))

describe('LoadPlanChart', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    Object.assign(chartProps, { data: [], xTicks: [], yTicks: [], tpsTicks: [], areas: [], lines: [], refLines: [] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const groups = [{ key: 'group0', label: 'Browse', color: 'var(--chart-1)' }, { key: 'group1', label: 'Cart', color: 'var(--chart-2)' }]
  const points = [
    { second: 0, group0: 1, group1: 1, total: 2 },
    { second: 24, group0: 2, group1: 2, total: 4 },
    { second: 300, group0: 0, group1: 0, total: 0 },
  ]

  it('draws stacked stairs on round ticks and lays observed samples over the plan', async () => {
    await act(async () => root.render(<LoadPlanChart label="Load model" points={points} totalSeconds={300} groups={groups} observed={[{ second: 2, vusers: 1 }, { second: 24, vusers: 3 }]} nowSecond={30} />))

    expect(chartProps.areas).toEqual(['group0:stepAfter', 'group1:stepAfter'])
    expect(chartProps.lines).toEqual(['observedVusers:linear'])
    expect(chartProps.xTicks).toEqual([0, 60, 120, 180, 240, 300])
    expect(chartProps.yTicks).toEqual([0, 1, 2, 3, 4])
    expect(chartProps.refLines).toEqual([30])
    // A sample between steps carries the step before it; one on a step joins it.
    expect(chartProps.data.map(row => [row.second, row.group0, row.observedVusers])).toEqual([[0, 1, undefined], [2, 1, 1], [24, 2, 3], [300, 0, undefined]])
    expect(container.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe('Load model: planned peak 4 VUsers over 5m; observed peak 3 VUsers')
    expect(Array.from(container.querySelectorAll('.load-plan-legend span')).map(item => item.textContent)).toEqual(['Browse', 'Cart', 'Observed VUsers', 'Now'])
  })

  it('draws only TPS targets for a plan the goal controller sizes', async () => {
    await act(async () => root.render(<LoadPlanChart label="Model preview" points={[{ second: 0, totalTps: 50 }, { second: 60, totalTps: 0 }]} totalSeconds={60} groups={[]} showVUsers={false} tps={[{ key: 'totalTps', label: 'Target TPS', color: 'var(--warning)' }]} />))

    expect(chartProps.areas).toEqual([])
    expect(chartProps.lines).toEqual(['totalTps:stepAfter'])
    expect(container.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe('Model preview: planned TPS targets over 1m')
    expect(container.querySelector('.load-plan-legend')?.textContent).toBe('Target TPS')
  })

  it('stacks equal group TPS targets under the total, on round ticks', async () => {
    await act(async () => root.render(<LoadPlanChart label="Model preview" points={[{ second: 0, group0Tps: 12.5, group1Tps: 12.5, totalTps: 25 }, { second: 3600, group0Tps: 0, group1Tps: 0, totalTps: 0 }]} totalSeconds={3600} groups={[]} showVUsers={false} tps={[
      { key: 'group0Tps', label: 'Browse TPS', color: 'var(--chart-1)' },
      { key: 'group1Tps', label: 'Cart TPS', color: 'var(--chart-2)' },
      { key: 'totalTps', label: 'Target TPS', color: 'var(--warning)' },
    ]} />))

    // Equal targets would draw one on top of the other as lines; stacked, both show.
    expect(chartProps.areas).toEqual(['group0Tps:stepAfter', 'group1Tps:stepAfter'])
    expect(chartProps.lines).toEqual(['totalTps:stepAfter'])
    expect(chartProps.tpsTicks).toEqual([0, 10, 20, 30])
    expect(Array.from(container.querySelectorAll('.load-plan-legend span')).map(item => item.textContent)).toEqual(['Browse TPS', 'Cart TPS', 'Target TPS'])
  })
})
