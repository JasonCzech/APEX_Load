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
})
