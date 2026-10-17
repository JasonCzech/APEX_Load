import { renderToStaticMarkup } from 'react-dom/server'
import { describe, it, expect } from 'vitest'
import RunReportKpiScorecard from './RunReportKpiScorecard'
describe('RunReportKpiScorecard', () => {
 it('does not draw a flat trend for unavailable interval evidence', () => {
  const html = renderToStaticMarkup(<RunReportKpiScorecard kpis={[{ id: 'p95', label: 'P95', value: '120 ms', tone: 'neutral', sparkline: [] }]} />)
  expect(html).toContain('120 ms')
  expect(html).not.toContain('svg')
  expect(html).not.toContain('sparkline-empty')
 })
 it('marks a percentile that is not exact', () => {
  const html = renderToStaticMarkup(<RunReportKpiScorecard kpis={[
   { id: 'p95', label: 'P95', value: '120 ms', tone: 'neutral', sparkline: [], percentileQuality: 'approximate' },
   { id: 'avgLatency', label: 'Avg', value: '90 ms', tone: 'neutral', sparkline: [] },
  ]} />)
  expect(html.match(/percentile-quality-mark/g)).toHaveLength(1)
  expect(html).toContain('within 0.5%')
 })
})
