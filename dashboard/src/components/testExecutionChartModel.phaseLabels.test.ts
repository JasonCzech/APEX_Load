import { describe, expect, it } from 'vitest'
import { chartConfigs } from './testExecutionChartModel'
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
