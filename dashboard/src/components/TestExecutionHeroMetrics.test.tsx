// @vitest-environment jsdom

import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import type { RunArchiveReport } from '../types'

describe('TestExecutionHeroMetrics', () => {
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

  it('labels the archived interval average as Avg TPS instead of the peak', async () => {
    const archiveReport = {
      overview: {
        peak_tps: 100,
        peak_active_vusers: 5,
        avg_response_ms: 10,
        error_pct: 0,
        transactions: 30,
      },
      summary_timeline: [{ tps: 10 }, { tps: 20 }, { tps: 30 }],
    } as RunArchiveReport

    await act(async () => root.render(
      <TestExecutionHeroMetrics archiveReport={archiveReport} summary={null} />,
    ))

    const cards = Array.from(container.querySelectorAll('.hero-card'))
    expect(cards[1]?.textContent).toContain('20.0')
    expect(cards[1]?.textContent).not.toContain('100.0')
  })
})

it('uses the named population rather than combined archive totals', () => {
 const host=document.createElement('div'); const root=createRoot(host)
 const report={overview:{transactions:506,avg_response_ms:5,error_pct:20},metric_populations:{transaction:{transactions:253,avg_response_ms:80,error_pct:0},action:{average_rate_per_second:10}},summary_timeline:[]} as unknown as RunArchiveReport
 act(()=>root.render(<TestExecutionHeroMetrics archiveReport={report} summary={null} />))
 expect(host.textContent).toContain('253Named transactions')
 expect(host.textContent).not.toContain('506')
 expect(host.textContent).toContain('80Named transaction latency')
 act(()=>root.unmount())
})

it('leads a live run that sends requests with its requests, and keeps a goal run in TPS', () => {
  const host = document.createElement('div'); const root = createRoot(host)
  // A script without transactions: requests flow while no transaction passes.
  const metrics = { timestamp: '', active_vusers: 4, tps: 0, total_transactions: 0, total_errors: 3, error_pct: 100, avg_response_ms: 90, p50_ms: 80, p90_ms: 120, p95_ms: 130, p99_ms: 150, request_rate: 31, run_total_requests: 400, run_request_errors: 100 }
  const summary = { avgTps: 0, avgRequestRate: 30, avgLatency: 90, avgError: 100, maxVusers: 4 }
  act(() => root.render(<TestExecutionHeroMetrics archiveReport={null} summary={summary} metrics={metrics} />))
  expect(host.textContent).toContain('30.0Requests/s')
  expect(host.textContent).toContain('25.00%Request error rate (run to date)')
  expect(host.textContent).toContain('400Requests (run to date)')
  expect(host.textContent).not.toContain('Successful transaction TPS')
  act(() => root.render(<TestExecutionHeroMetrics archiveReport={null} summary={{ ...summary, avgTps: 8 }} metrics={metrics} scenarioMode="goal_oriented" />))
  expect(host.textContent).toContain('8.0Successful transaction TPS')
  act(() => root.unmount())
})
