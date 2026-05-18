import { describe, expect, it } from 'vitest'

import { chartStateForMonitorPreset } from './TestExecutionCharts'

describe('chartStateForMonitorPreset', () => {
  it('applies role-specific chart order and hides non-preset charts', () => {
    const state = chartStateForMonitorPreset('release_approver', ['vusers', 'tps', 'latency', 'errors', 'cpu'])

    expect(state.chartOrder).toEqual(['latency', 'errors', 'tps', 'vusers', 'cpu'])
    expect(state.hiddenChartIds).toEqual(['vusers', 'cpu'])
  })

  it('falls back to all known charts for an unknown preset', () => {
    const state = chartStateForMonitorPreset('unknown', ['vusers', 'tps'])

    expect(state.chartOrder).toEqual(['vusers', 'tps'])
    expect(state.hiddenChartIds).toEqual([])
  })
})
