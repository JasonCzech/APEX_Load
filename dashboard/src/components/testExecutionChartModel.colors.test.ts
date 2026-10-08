import { describe, expect, it } from 'vitest'

import { chartConfigs, chartMetricRows } from './testExecutionChartModel'

// Status tokens (--danger, --warning, --accent, --success) alias chart slots in
// most themes, so a series coloured with one collides with a --chart-N series.
const STATUS_TOKEN = /var\(--(danger|warning|accent|success|info)\b/

describe('test execution chart series colours', () => {
  it('gives every Error Taxonomy category its own chart slot', () => {
    const taxonomy = chartConfigs.find(chart => chart.id === 'error_taxonomy')
    const colors = taxonomy?.keys?.map(key => key.c) ?? []
    expect(colors.length).toBeGreaterThan(1)
    expect(new Set(colors).size).toBe(colors.length)
    for (const color of colors) expect(color).toMatch(/^var\(--chart-\d\)$/)
  })

  it.each(chartConfigs.map(chart => [chart.id, chart] as const))('%s does not colour series with status tokens', (_id, chart) => {
    const keyColors = chart.keys?.map(key => key.c) ?? []
    const rowColors = chartMetricRows(chart, [], [], []).map(row => row.color)
    for (const color of [...keyColors, ...rowColors]) expect(color).not.toMatch(STATUS_TOKEN)
  })

  it('keeps Generator Impact series on distinct colours', () => {
    const impact = chartConfigs.find(chart => chart.id === 'generator_impact')
    expect(impact).toBeDefined()
    const colors = chartMetricRows(impact!, [], [], []).map(row => row.color)
    expect(colors.length).toBeGreaterThan(1)
    expect(new Set(colors).size).toBe(colors.length)
  })
})
