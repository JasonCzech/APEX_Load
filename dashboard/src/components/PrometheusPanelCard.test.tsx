// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import PrometheusPanelCard from './PrometheusPanelCard'
import { thresholdAwareYAxisMax } from './prometheusPanelCardModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const series = [{ key: 'cpu', label: 'api-1', labels: { instance: 'api-1' }, values: [{ timestamp: '2026-01-01T00:00:00Z', timestamp_ms: 1, value: 42 }] }]

describe('PrometheusPanelCard imported panel kinds', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('renders a latest-value table', async () => {
    await act(async () => root.render(<PrometheusPanelCard title="CPU" kind="table" unit="%" series={series} />))
    expect(container.querySelector('table')?.textContent).toContain('api-1')
    expect(container.querySelector('table')?.textContent).toContain('42 %')
  })

  it('renders heatmap cells and threshold-colored stat tiles', async () => {
    await act(async () => root.render(<PrometheusPanelCard title="CPU" kind="heatmap" series={series} />))
    expect(container.querySelectorAll('.imported-panel-heatmap-row span')).toHaveLength(1)
    await act(async () => root.render(<PrometheusPanelCard title="CPU" kind="single_value" value={95} series={series} thresholds={[{ value: 90, color: 'red', severity: 'critical' }]} />))
    expect(container.querySelector('.stat-tile-critical')).not.toBeNull()
  })

  it('expands the line-chart domain to include imported thresholds', () => {
    expect(thresholdAwareYAxisMax(300, [
      { value: undefined, color: 'green', severity: 'normal' },
      { value: 5_000, color: 'red', severity: 'critical' },
    ])).toBe(5_000)
    expect(thresholdAwareYAxisMax(6_000, [
      { value: 5_000, color: 'red', severity: 'critical' },
    ])).toBe(6_000)
  })
})
