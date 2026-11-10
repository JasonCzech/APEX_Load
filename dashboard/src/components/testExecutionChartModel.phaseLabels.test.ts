import { describe, expect, it } from 'vitest'
import { LIVE_WATERFALL_CHART_ID, chartConfigs, isLiveWaterfallChart, isOverlayMetricAvailable, monitorPresets } from './testExecutionChartModel'
import { LIVE_WATERFALL_PHASE_LABELS } from './liveWaterfallModel'
import { TIMELINE_GRAPHS } from './runReport/timelineGraphModel'

// avg_no_response_ms holds every sample with no TTFB split: a timeout or
// connection failure, but also any protocol that records no TTFB (gRPC, JDBC,
// WebSocket) and authored transactions holding such a child. "No first byte"
// read as a failure for healthy non-HTTP traffic, so both charts name the
// bucket for what it is.
describe('no-TTFB phase label', () => {
  it('labels the live transaction phase segment as unsplit, not as a failure', () => {
    const chart = chartConfigs.find(config => config.id === 'tx_network_phases')
    const segment = chart?.keys?.find(key => key.k === 'txNoResponseMs')
    expect(segment?.n).toBe('No TTFB (unsplit)')
    expect(chart?.keys?.some(key => /first byte/i.test(key.n))).toBe(false)
  })

  it('uses the same label on the report timeline', () => {
    const graph = TIMELINE_GRAPHS.find(item => item.id === 'networkPhases')
    const series = graph?.series.find(item => item.key === 'txNoResponseMs')
    expect(series?.name).toBe('No TTFB (unsplit)')
  })
})

describe('live waterfall catalog entry', () => {
  it('is a phase-waterfall chart next to the phase breakdown with the same no-TTFB label', () => {
    const ids = chartConfigs.map(config => config.id)
    const chart = chartConfigs.find(config => config.id === LIVE_WATERFALL_CHART_ID)!
    expect(chart).toMatchObject({ title: 'Live waterfall', type: 'phase-waterfall' })
    expect(ids.indexOf(LIVE_WATERFALL_CHART_ID)).toBe(ids.indexOf('tx_network_phases') + 1)
    expect(isLiveWaterfallChart(chart)).toBe(true)
    expect(isOverlayMetricAvailable(chart, 'vusers')).toBe(false)
    expect(LIVE_WATERFALL_PHASE_LABELS.total).toBe('No TTFB (unsplit)')
  })

  it('is in the presets that show the phase breakdown', () => {
    for (const preset of monitorPresets) {
      const ids: readonly string[] = preset.chartIds
      expect(ids.includes(LIVE_WATERFALL_CHART_ID)).toBe(ids.includes('tx_network_phases'))
    }
  })
})
