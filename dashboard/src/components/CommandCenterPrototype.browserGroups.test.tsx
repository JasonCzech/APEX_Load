// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MetricsHistoryPoint, TestSummary } from '../types'
import { browserVitalsAlgorithm, browserVitalsAlgorithmSHA256 } from '../types/browserDocumentEvidence'

// Stand in for the live metrics stream: two samples 30 s apart, between
// which the browser group completed five journeys.
const streamHistory: MetricsHistoryPoint[] = []
vi.mock('./useExecutionMetricsStream', () => ({
  useExecutionMetricsStream: () => ({ localMetricsHistory: streamHistory, latestMetrics: undefined }),
}))

import CommandCenterPrototype from './CommandCenterPrototype'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const sample = (ageMs: number, iterations: number): MetricsHistoryPoint => ({
  time: '09:10:00', timestampMs: Date.now() - ageMs, vusers: 6, tps: 5, requestRate: 50, avgMs: 40, errorPct: 0, p50: 30, p90: 60, p95: 70, p99: 90,
  API_tps: 3, API_requestRate: 20, Shop_tps: 2, Shop_requestRate: 30, Shop_iterations: iterations,
})

function liveRun(groups: TestSummary['groups']): TestSummary {
  const startedAt = new Date(Date.now() - 120_000).toISOString()
  const group = (name: string, rate: number, extra: object = {}) => ({ name, vusers: 3, transactions: 10, errors: 0, tps: 2, requests: rate, request_rate: rate, run_requests: rate * 40, run_request_errors: 0, avg_response_ms: 40, p50_ms: 30, p90_ms: 60, p99_ms: 90, ...extra })
  return {
    id: 'live-browser', name: 'Browser and API', status: 'RUNNING', duration: '5m', total_vusers: 6, created_at: startedAt, started_at: startedAt,
    groups,
    live_metrics: {
      timestamp: new Date().toISOString(), active_vusers: 6, tps: 5, avg_response_ms: 40, error_pct: 0, total_transactions: 20, total_errors: 0,
      requests: 50, request_errors: 0, request_rate: 50, run_total_requests: 2_000, run_request_errors: 0,
      p50_ms: 30, p90_ms: 60, p95_ms: 70, p99_ms: 90,
      groups: { API: group('API', 20), Shop: group('Shop', 30, { completed_iterations: 12 }) },
    },
  }
}

describe('CommandCenterPrototype browser groups', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    streamHistory.splice(0, streamHistory.length, sample(31_000, 7), sample(1_000, 12))
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    vi.restoreAllMocks()
    container.remove()
  })

  const pill = (label: string) => Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
    .find(metric => metric.querySelector('span')?.textContent === label)?.querySelector('strong')?.textContent

  // A browser group's pages request every document, script and image: the
  // run keeps one request rate and says how much of it their pages make, and
  // the group leads with journeys.
  it('shows the browser pages\' share of the run\'s request rate and journeys per minute', async () => {
    const run = liveRun([
      { name: 'API', protocol: 'http', script_id: 'api', script_name: 'API', vusers: 3 },
      { name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 },
    ])
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    expect(pill('Requests/s (10 s avg)')).toBe('50.0 · 30.0 from browser pages')
    const panel = container.querySelector('[aria-label="Browser groups"]')!
    const shop = Array.from(panel.querySelectorAll('tbody tr')).find(row => row.textContent?.includes('Shop'))!
    expect(Array.from(shop.querySelectorAll('td')).map(cell => cell.textContent)).toEqual(['Shop', '10.0', '30.0', '3'])
    expect(panel.querySelector('table')?.textContent).not.toContain('API')
    expect(panel.textContent).toContain('the run’s Requests/s includes them and shows their share')
    expect(panel.textContent).toContain('No page timings yet.')
    expect(panel.textContent).toContain('Runtime Settings › Browser')

    const groupLoad = container.querySelector('[aria-label="Group load and health"]')!
    const shopLoad = Array.from(groupLoad.querySelectorAll('[role="row"]')).find(row => row.textContent?.includes('Shop'))!
    expect(shopLoad.querySelector('.command-tps-stack small')?.textContent).toBe('10.0 journeys/min')
  })

  // Published page loads show as p75 Web Vitals by group, engine and device.
  it('shows live Web Vitals by group', async () => {
    const buckets = Array<number>(192).fill(0)
    buckets[Math.ceil(Math.log(1200 / 0.000001) / Math.log(1.2))] = 1
    const lcp = {
      metric: 'lcp', algorithm: browserVitalsAlgorithm, algorithm_sha256: browserVitalsAlgorithmSHA256, unit: 'ms', engine: 'chromium', device_profile: 'desktop', group: 'Shop',
      cohort: { version: 1, browser_version: '136.0.1', platform: 'linux', viewport_width: 800, viewport_height: 600, headless: true, mobile: false, touch: false, device_scale_factor: 1, cpu_throttle_rate: 1, network_latency_ms: 0, network_bandwidth_kbps: 0, shaping_applied: true, cache_enabled: false, cache_content_urls: false, check_newer_versions: false, new_user_policy: 'persistent_context', new_user_policy_applied: true },
      frame_scope: 'main_frame', lifecycle_phase: 'navigation', eligible_final: 1, unavailable_final: 0, unqualified_final: 0, censored: 0, provisional: 0,
      distribution: { count: 1, sum: 1200, min: 1200, max: 1200, buckets },
    }
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    run.live_metrics!.browser_documents = { version: 2, metrics: [lcp], dropped_observations: 0, incomplete: false } as never
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    const vitals = container.querySelector('table[aria-label="Live Web Vitals"]')!
    expect(Array.from(vitals.querySelectorAll('thead th')).map(cell => cell.textContent)).toEqual(['Group', 'Engine', 'Device profile', 'LCP', 'FCP', 'INP', 'CLS', 'TTFB'])
    expect(Array.from(vitals.querySelectorAll('tbody td')).slice(0, 4).map(cell => cell.textContent)).toEqual(['Shop', 'chromium', 'desktop', '1,200 ms'])
  })

  // With capture on, an empty summary is not a missing setting.
  it('says page timings are captured before any page load is measured', async () => {
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    run.live_metrics!.browser_documents = { version: 2, metrics: null, dropped_observations: 3, incomplete: true }
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    const panel = container.querySelector('[aria-label="Browser groups"]')!
    expect(panel.textContent).toContain('Page timings are captured, but no page load has been measured yet. Some page evidence was dropped.')
    expect(panel.textContent).not.toContain('Runtime Settings')
  })

  // Client metrics stand in until a page load is measured, without asking
  // for a capture setting that is already on.
  it('does not ask to turn on page timings that are already captured', async () => {
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    run.live_metrics!.browser_documents = { version: 2, metrics: null, dropped_observations: 0, incomplete: false }
    run.live_metrics!.browser = { samples: 4, avg_fcp_ms: 410, avg_lcp_ms: 900 }
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    const panel = container.querySelector('[aria-label="Browser groups"]')!
    expect(panel.textContent).toContain('FCP 410 ms · LCP 900 ms')
    expect(panel.textContent).toContain('Page timings are captured, but no page load has been measured yet.')
    expect(panel.textContent).not.toContain('turn on')
  })

  // A run whose fleet counts no requests reports no page request rate, not
  // zero; an unsupported document summary shows its reason, not a setting.
  it('says what it cannot report', async () => {
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    const live = run.live_metrics!
    delete live.requests; delete live.request_rate; delete live.run_total_requests; delete live.run_request_errors
    for (const group of Object.values(live.groups!)) { delete group.requests; delete group.request_rate; delete group.run_requests; delete group.run_request_errors }
    live.browser_documents = { version: 99 } as never
    live.browser = { samples: 4, avg_fcp_ms: 410 }
    streamHistory.forEach(point => { point.requestRate = undefined; point.Shop_requestRate = 0 })
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    const panel = container.querySelector('[aria-label="Browser groups"]')!
    const shop = Array.from(panel.querySelectorAll('tbody tr')).find(row => row.textContent?.includes('Shop'))!
    expect(shop.querySelectorAll('td')[2].textContent).toBe('Not reported')
    expect(panel.textContent).toContain('did not supply a supported document evidence version')
    expect(panel.textContent).not.toContain('turn on')
  })

  // A run of browser groups alone leads with its journeys per minute.
  it('leads a browser-only run with journeys per minute', async () => {
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    expect(pill('Journeys/min (1 min avg)')).toBe('10.0')
  })

  // Until journeys are measured the pill says so, rather than falling back
  // to the page requests it replaces.
  it('shows a dash for a browser-only run before its journeys are measured', async () => {
    streamHistory.splice(0, streamHistory.length, sample(1_000, 12))
    const run = liveRun([{ name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 }])
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    expect(pill('Journeys/min (1 min avg)')).toBe('—')
    expect(container.querySelector('[aria-label="Browser groups"]')?.textContent).toContain('Journeys are reported once a group has run for 15 s')
  })

  // The Groups table shows browser journeys by default once the run's groups
  // are known, even when the page first rendered while the run was loading.
  it('shows the journeys column once browser groups appear, and only then', async () => {
    const browserRun = liveRun([
      { name: 'API', protocol: 'http', script_id: 'api', script_name: 'API', vusers: 3 },
      { name: 'Shop', protocol: 'browser', script_id: 'shop', script_name: 'Shop', vusers: 3 },
    ])
    const loading = { ...browserRun, groups: [] }
    const headers = () => Array.from(container.querySelectorAll('[role="tabpanel"][aria-label="Groups"] th:not([hidden])')).map(header => header.textContent?.replace(/[▲▼·]$/, ''))
    await act(async () => root.render(<CommandCenterPrototype testId={browserRun.id} initialTest={loading} history={[]} />))
    expect(headers()).not.toContain('Journeys/min (1 min avg)')
    await act(async () => root.render(<CommandCenterPrototype testId={browserRun.id} initialTest={browserRun} history={[]} />))
    expect(headers()).toContain('Journeys/min (1 min avg)')
    const shop = Array.from(container.querySelectorAll('[role="tabpanel"][aria-label="Groups"] tbody tr')).find(row => row.textContent?.includes('Shop'))!
    const cells = Array.from(shop.querySelectorAll('td:not([hidden])'))
    expect(cells[cells.length - 1]?.textContent).toBe('10.0')
    // Filtering to the API group keeps the column: filters never change columns.
    await act(async () => {
      const filter = container.querySelector<HTMLInputElement>('input[aria-label="Filter command center table rows"]')!
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(filter, 'API')
      filter.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('[role="tabpanel"][aria-label="Groups"] tbody')?.textContent).not.toContain('Shop')
    expect(headers()).toContain('Journeys/min (1 min avg)')
  })

  it('leaves the run summary alone for a run without browser groups', async () => {
    const run = liveRun([
      { name: 'API', protocol: 'http', script_id: 'api', script_name: 'API', vusers: 3 },
      { name: 'Shop', protocol: 'http', script_id: 'shop', script_name: 'Shop', vusers: 3 },
    ])
    await act(async () => root.render(<CommandCenterPrototype testId={run.id} initialTest={run} history={[]} />))

    expect(pill('Requests/s (10 s avg)')).toBe('50.0')
    expect(container.querySelector('[aria-label="Browser groups"]')).toBeNull()
    const groupsTable = container.querySelector('[role="tabpanel"][aria-label="Groups"]')!
    expect(groupsTable.textContent).not.toContain('Journeys/min')
    expect(groupsTable.textContent).toContain('Show all 15 metrics')
  })
})
