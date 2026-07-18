import { describe, expect, it } from 'vitest'

import type { ImportedDashboardResult } from '../types'
import {
  buildImportedDashboardCharts,
  chartConfigs,
  isImportedDashboardChart,
  isPrometheusChart,
  isStatTileChart,
  presetExemptChartIds,
} from './testExecutionChartModel'
import { quantizeMs } from './prometheusQueryWindow'

function dashboard(id: string, panels: ImportedDashboardResult['panels']): ImportedDashboardResult {
  return { dashboard_id: id, dashboard_title: `Dash ${id}`, panels }
}

describe('buildImportedDashboardCharts', () => {
  it('maps line panels to line-prometheus and single_value panels to stat-tile', () => {
    const charts = buildImportedDashboardCharts([
      dashboard('d1', [
        { panel_id: 1, title: 'CPU', kind: 'line', series: [{ key: 'gdash_d1_1_A', label: 'a', values: [] }] },
        { panel_id: 2, title: 'Up', kind: 'single_value', value: 7, unit: '' },
      ]),
    ])
    expect(charts).toHaveLength(2)
    expect(charts[0].type).toBe('line-prometheus')
		expect(charts[0].title).toBe('Dash d1 · CPU')
    expect(charts[0].importedPanel?.kind).toBe('line')
    expect(charts[1].type).toBe('stat-tile')
    expect(charts[1].statValue?.value).toBe(7)
    expect(isStatTileChart(charts[1])).toBe(true)
    expect(isImportedDashboardChart(charts[0])).toBe(true)
  })

  it('namespaces ids and de-dups so it cannot collide with static chartConfigs', () => {
    const staticIds = new Set(chartConfigs.map(c => c.id))
    const charts = buildImportedDashboardCharts([
      dashboard('d1', [
        { panel_id: 1, title: 'A', kind: 'line', series: [] },
        { panel_id: 1, title: 'A dup', kind: 'line', series: [] }, // same id -> deduped
      ]),
    ])
    expect(charts).toHaveLength(1)
    expect(charts[0].id).toBe('gdash_d1_1')
    expect(staticIds.has(charts[0].id)).toBe(false)
  })

  it('imported line charts satisfy isPrometheusChart but are exempted from presets and routed as imported', () => {
    const charts = buildImportedDashboardCharts([
      dashboard('d1', [{ panel_id: 1, title: 'A', kind: 'line', series: [] }]),
    ])
    // Imported line charts reuse the prometheus renderer (isPrometheusChart true)...
    expect(isPrometheusChart(charts[0])).toBe(true)
    // ...and are included in the preset-exempt (default-visible) set.
    expect(presetExemptChartIds(charts)).toContain('gdash_d1_1')
  })
})

describe('quantizeMs', () => {
  it('floors and ceils to bucket boundaries', () => {
    expect(quantizeMs(10_500, 10_000, 'floor')).toBe(10_000)
    expect(quantizeMs(10_500, 10_000, 'ceil')).toBe(20_000)
    expect(quantizeMs(10_000, 10_000, 'floor')).toBe(10_000)
  })
  it('returns the value unchanged for a non-positive bucket', () => {
    expect(quantizeMs(1234, 0, 'floor')).toBe(1234)
  })
})
