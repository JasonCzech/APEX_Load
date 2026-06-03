// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ResultsPage, { buildComparisonSummarySeries, comparisonChartYAxisLabel } from './ResultsPage'
import { downloadBlob } from '../utils/fileUtils'

const apiMock = {
  listTests: vi.fn(),
  getReleaseSLAGovernance: vi.fn(),
  listExternalResultImports: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  getTestArchiveSummaries: vi.fn(),
  deleteTest: vi.fn(),
  downloadTestArchive: vi.fn(),
  downloadTestArchiveComparison: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
;(globalThis as typeof globalThis & { ResizeObserver?: typeof ResizeObserver }).ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as typeof ResizeObserver

describe('ResultsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    apiMock.listTests.mockResolvedValue({
      tests: [
        {
          id: 'run-a',
          ref_key: 'LOC-RUN-20260512-001',
          name: 'iPhone Checkout',
          status: 'COMPLETE',
          total_vusers: 10,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout', protocol: 'browser', vusers: 10 }],
          archive: { ref_key: 'LOC-RUN-20260512-001', transaction_count: 1000 },
        },
        {
          id: 'run-b',
          ref_key: 'LOC-RUN-20260512-002',
          name: 'iPad Checkout',
          status: 'COMPLETE',
          total_vusers: 12,
          duration: '5m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:05:00Z',
          groups: [{ name: 'buyers', script_id: 'script-2', script_name: 'Checkout', protocol: 'browser', vusers: 12 }],
          archive: { ref_key: 'LOC-RUN-20260512-002', transaction_count: 1100 },
        },
      ],
    })
    apiMock.getReleaseSLAGovernance.mockResolvedValue({
      generated_at: '2026-05-12T20:00:00Z',
      release_id: 'rel-1',
      release_name: 'Sprint 24',
      application_id: 'app-1',
      project_id: 'project-1',
      owner: { type: 'project_admin', display_name: 'Project administrators' },
      status: 'failed',
      summary: {
        total_runs: 2,
        archived_runs: 2,
        passed_runs: 1,
        failed_runs: 1,
        waived_breaches: 1,
        unwaived_breaches: 0,
      },
      breach_history: [
        { test_id: 'run-b', test_name: 'iPad Checkout', status: 'COMPLETE', sla_breached: true, sla_details: ['P95 breached'], completed_at: '2026-05-12T19:05:00Z' },
      ],
      waivers: [
        {
          id: 'waiver-1',
          release_id: 'rel-1',
          scope: 'release',
          reason: 'Known CDN maintenance window',
          approved_by: 'perf-admin',
          status: 'active',
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
        },
      ],
      audit_trail: [
        {
          id: 'audit-1',
          action: 'release.sla_waiver.create',
          actor_type: 'user',
          status: 'success',
          resource_type: 'release',
          resource_id: 'rel-1',
          actor_subject: 'perf-admin',
          created_at: '2026-05-12T20:00:00Z',
          details: { reason: 'Known CDN maintenance window' },
        },
      ],
      next_actions: ['Review breach history before promotion.'],
    })
    apiMock.listExternalResultImports.mockResolvedValue({
      imports: [
        {
          id: 'import-1',
          provider: 'k6',
          name: 'checkout-smoke.json',
          release_id: 'rel-1',
          created_at: '2026-05-12T20:10:00Z',
          summary: { transactions: 1200, error_pct: 0.25 },
        },
      ],
      count: 1,
      limit: 50,
      offset: 0,
    })
    apiMock.getTestArchiveReport.mockImplementation(async (id: string) => ({
      manifest: {
        test_id: id,
        provenance: {
          runtime_settings: {
            browser: id === 'run-a'
              ? { device_profile: 'iphone_se', orientation: 'portrait', touch_mode: true, headless: false }
              : { device_profile: 'ipad_air', orientation: 'landscape', touch_mode: true, headless: true },
          },
        },
      },
      overview: {
        transactions: id === 'run-a' ? 1000 : 1100,
        errors: 0,
        error_pct: 0,
        avg_response_ms: id === 'run-a' ? 95 : 102,
        min_response_ms: 80,
        max_response_ms: 140,
        resource_samples: 0,
        interval_summaries: 0,
        peak_tps: id === 'run-a' ? 30 : 28,
        peak_active_vusers: id === 'run-a' ? 10 : 12,
        browser: {
          samples: 2,
          avg_lcp_ms: id === 'run-a' ? 240 : 280,
          avg_inp_ms: id === 'run-a' ? 56 : 64,
        },
      },
      by_action: {},
      browser_by_engine: id === 'run-a'
        ? {
            webkit: {
              samples: 5,
              browser_engine: 'webkit',
              avg_lcp_ms: 255,
              avg_cls_score: 0.03,
              avg_inp_ms: 69,
            },
            chromium: {
              samples: 1,
              browser_engine: 'chromium',
              avg_lcp_ms: 190,
              avg_cls_score: 0.015,
              avg_inp_ms: 41,
            },
          }
        : {
            webkit: {
              samples: 2,
              browser_engine: 'webkit',
              avg_lcp_ms: 280,
              avg_cls_score: 0.018,
              avg_inp_ms: 64,
            },
          },
      browser_by_platform: {
        [id === 'run-a' ? 'ios' : 'ipados']: {
          samples: id === 'run-a' ? 4 : 2,
          device_platform: id === 'run-a' ? 'ios' : 'ipados',
          browser_engine: 'webkit',
          avg_lcp_ms: id === 'run-a' ? 250 : 280,
          avg_cls_score: id === 'run-a' ? 0.029 : 0.018,
          avg_inp_ms: id === 'run-a' ? 68 : 64,
        },
      },
      browser_by_device: {
        [id === 'run-a' ? 'iPhone 14 Pro' : 'iPad Air']: {
          samples: 2,
          device_profile: id === 'run-a' ? 'iPhone 14 Pro' : 'iPad Air',
          device_platform: id === 'run-a' ? 'ios' : 'ipados',
          device_tier: id === 'run-a' ? 'mobile' : 'tablet',
          browser_engine: 'webkit',
          avg_lcp_ms: id === 'run-a' ? 240 : 280,
          avg_cls_score: id === 'run-a' ? 0.023 : 0.018,
          avg_inp_ms: id === 'run-a' ? 56 : 64,
        },
        ...(id === 'run-a'
          ? {
              'iPhone SE': {
                samples: 1,
                device_profile: 'iPhone SE',
                device_platform: 'ios',
                device_tier: 'low',
                browser_engine: 'webkit',
                avg_lcp_ms: 260,
                avg_cls_score: 0.031,
                avg_inp_ms: 72,
              },
            }
          : {}),
      },
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 100,
        net_sent_kbps: 0,
        net_recv_kbps: 0,
        disk_read_kbps: 0,
        disk_write_kbps: 0,
      },
    }))
    apiMock.getTestArchiveSummaries.mockResolvedValue({ summaries: [] })
    apiMock.deleteTest.mockResolvedValue(undefined)
    apiMock.downloadTestArchive.mockResolvedValue(new Blob())
    apiMock.downloadTestArchiveComparison.mockResolvedValue(new Blob(['backend comparison csv'], { type: 'text/csv' }))

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('uses one line color per compared run across TPS and P95 series', () => {
    const tpsSeries = buildComparisonSummarySeries('ACME (90328d19)', 'ACME (44641072)', 'tps')
    const p95Series = buildComparisonSummarySeries('ACME (90328d19)', 'ACME (44641072)', 'p95')

    expect(tpsSeries).toEqual([
      { dataKey: 'leftTps', name: 'ACME (90328d19) TPS', stroke: 'var(--chart-1)', yAxisId: 'value' },
      { dataKey: 'rightTps', name: 'ACME (44641072) TPS', stroke: 'var(--chart-2)', yAxisId: 'value' },
    ])
    expect(p95Series).toEqual([
      { dataKey: 'leftP95', name: 'ACME (90328d19) P95', stroke: 'var(--chart-1)', yAxisId: 'value' },
      { dataKey: 'rightP95', name: 'ACME (44641072) P95', stroke: 'var(--chart-2)', yAxisId: 'value' },
    ])
  })

  it('labels comparison chart axes with the selected metric units', () => {
    expect(comparisonChartYAxisLabel({
      id: 'p95',
      label: 'P95',
      leftKey: 'leftP95',
      rightKey: 'rightP95',
      description: '95th percentile response time',
      valueFormat: 'ms',
    })).toBe('P95 (ms)')
    expect(comparisonChartYAxisLabel({
      id: 'errors',
      label: 'Errors',
      leftKey: 'leftErrorPct',
      rightKey: 'rightErrorPct',
      description: 'error percentage',
      valueFormat: 'percent',
    })).toBe('Error rate (%)')
  })

  it('shows a loading state before rendering an empty release result list', async () => {
    let resolveTests: (value: { tests: never[] }) => void = () => {}
    apiMock.listTests.mockReturnValueOnce(new Promise(resolve => {
      resolveTests = resolve
    }))
    apiMock.getReleaseSLAGovernance.mockResolvedValueOnce(null)
    apiMock.listExternalResultImports.mockResolvedValueOnce({ imports: [], count: 0, limit: 0, offset: 0 })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })

    expect(container.textContent).toContain('Loading release results...')
    expect(container.textContent).toContain('Loading results...')
    expect(container.textContent).not.toContain('No completed scenarios yet.')

    await act(async () => {
      resolveTests({ tests: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Loading release results...')
    expect(container.textContent).toContain('No completed scenarios yet.')
  })

  it('disambiguates comparison labels when selected runs have the same scenario name', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'run-a',
          ref_key: 'LOC-RUN-20260512-001',
          name: 'Checkout',
          status: 'COMPLETE',
          total_vusers: 10,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-001', transaction_count: 1000 },
        },
        {
          id: 'run-b',
          ref_key: 'LOC-RUN-20260512-002',
          name: 'Checkout',
          status: 'COMPLETE',
          total_vusers: 12,
          duration: '5m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-002', transaction_count: 1100 },
        },
      ],
    })
    apiMock.getTestArchiveReport.mockImplementation(async (id: string) => ({
      manifest: { test_id: id, provenance: { runtime_settings: {} } },
      overview: {
        transactions: id === 'run-a' ? 1000 : 1100,
        errors: 0,
        error_pct: 0,
        avg_response_ms: id === 'run-a' ? 95 : 102,
        min_response_ms: 80,
        max_response_ms: 140,
        resource_samples: 0,
        interval_summaries: 2,
        peak_tps: id === 'run-a' ? 30 : 28,
        peak_active_vusers: id === 'run-a' ? 10 : 12,
      },
      by_action: {
        Search: { count: 20, errors: 0, avg_response_ms: id === 'run-a' ? 90 : 120 },
      },
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 100,
        net_sent_kbps: 0,
        net_recv_kbps: 0,
        disk_read_kbps: 0,
        disk_write_kbps: 0,
      },
    }))
    apiMock.getTestArchiveSummaries.mockImplementation(async (id: string) => ({
      summaries: [
        { timestamp: '2026-05-12T18:01:00Z', tps: id === 'run-a' ? 10 : 12, p95_ms: id === 'run-a' ? 110 : 140 },
        { timestamp: '2026-05-12T18:02:00Z', tps: id === 'run-a' ? 11 : 13, p95_ms: id === 'run-a' ? 105 : 145 },
      ],
    }))

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
      await Promise.resolve()
    })

    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(2)

    await act(async () => {
      rowCheckboxes[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[0].dispatchEvent(new Event('change', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('LOC-RUN-20260512-001')
    expect(container.textContent).toContain('LOC-RUN-20260512-002')
    expect(container.textContent).toContain('Checkout (LOC-RUN-20260512-001)')
    expect(container.textContent).toContain('Checkout (LOC-RUN-20260512-002)')
    expect(container.textContent).toContain('Search')
    expect(container.textContent).toContain('Run')
    expect(container.textContent).toContain('Avg ART')
    expect(container.textContent).toContain('Reference')
    expect(container.textContent).toContain('-30.0 ms')

    const tpsTab = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'TPS') as HTMLButtonElement | undefined
    const p95Tab = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'P95') as HTMLButtonElement | undefined
    expect(tpsTab?.getAttribute('aria-selected')).toBe('true')
    expect(p95Tab).toBeDefined()

    await act(async () => {
      p95Tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(p95Tab?.getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Showing 95th percentile response time; color identifies the run.')
  })

  it('shows archived browser metrics in the two-run comparison cards', async () => {
    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })

    await act(async () => {
      await Promise.resolve()
    })

    const reportLink = container.querySelector('a[href="/api/v1/tests/run-a/report"]') as HTMLAnchorElement | null
    expect(reportLink).toBeTruthy()
    expect(reportLink?.textContent).toBe('Download Report')

    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(2)

    await act(async () => {
      rowCheckboxes[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[0].dispatchEvent(new Event('change', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => {
      await Promise.resolve()
    })

    const platformToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Platform') as HTMLButtonElement | undefined
    const engineToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Engine') as HTMLButtonElement | undefined
    const overallToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Overall') as HTMLButtonElement | undefined
    const deviceToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Device') as HTMLButtonElement | undefined
    const exportButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Export CSV') as HTMLButtonElement | undefined

    expect(platformToggle).toBeDefined()
    expect(engineToggle).toBeDefined()
    expect(overallToggle).toBeDefined()
    expect(deviceToggle).toBeDefined()
    expect(exportButton).toBeDefined()

    expect(container.textContent).toContain('Run Comparison')
    expect(container.textContent).toContain('Browser Device Comparison (LCP / INP)')
    expect(container.textContent).toContain('Avg LCP')
    expect(container.textContent).toContain('Browser Runtime')
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')
    expect(container.textContent).toContain('ipad_air · Landscape · Touch enabled · Headless')
    expect(container.textContent).toContain('240.0 ms')
    expect(container.textContent).toContain('280.0 ms')
    expect(container.textContent).toContain('Avg INP')
    expect(container.textContent).toContain('56.0 ms')
    expect(container.textContent).toContain('64.0 ms')
    expect(container.textContent).not.toContain('Platform Rollup')
    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('iPhone 14 Pro')
    expect(container.textContent).toContain('iPhone SE')
    expect(container.textContent).toContain('iOS · WebKit · mobile tier')
    expect(container.textContent).toContain('iPad Air')
    expect(container.textContent).toContain('iPadOS · WebKit · tablet tier')

    if (!platformToggle || !engineToggle || !overallToggle || !deviceToggle) {
      return
    }

    await act(async () => {
      platformToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Platform Comparison (LCP / INP)')
    expect(container.textContent).toContain('Platform Rollup')
    expect(container.textContent).toContain('iOS')
    expect(container.textContent).toContain('WebKit · 4 samples')
    expect(container.textContent).toContain('250.0 ms')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    await act(async () => {
      engineToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Engine Comparison (LCP / INP)')
    expect(container.textContent).toContain('Engine Rollup')
    expect(container.textContent).toContain('Chromium')
    expect(container.textContent).toContain('190.0 ms')
    expect(container.textContent).toContain('WebKit')
    expect(container.textContent).toContain('255.0 ms')
    expect(container.textContent).toContain('Series: solid LCP and dashed INP for each selected run.')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    if (!exportButton) {
      return
    }

    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.downloadTestArchiveComparison).toHaveBeenCalledWith('run-b', 'run-a', 'engine')
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    const [blob, filename] = vi.mocked(downloadBlob).mock.calls[0]
    expect(filename).toBe('archive-comparison-run-b-run-a-engine.csv')
    const csv = await blob.text()
    expect(csv).toBe('backend comparison csv')

    await act(async () => {
      overallToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Overview Comparison (LCP / INP)')
    expect(container.textContent).not.toContain('Platform Rollup')
    expect(container.textContent).not.toContain('Devices')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    await act(async () => {
      deviceToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Device Comparison (LCP / INP)')
    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('iPhone 14 Pro')
  })

  it('surfaces C4 SLA governance, external imports, and root-cause actions', async () => {
    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })

    expect(container.textContent).toContain('Release SLA Gate')
    expect(container.textContent).toContain('Failed')
    expect(container.textContent).toContain('Project administrators')
    expect(container.textContent).toContain('Known CDN maintenance window')
    expect(container.textContent).toContain('External Results')
    expect(container.textContent).toContain('k6')
    expect(container.textContent).toContain('checkout-smoke.json')
    expect(container.textContent).toContain('1,200 tx / 0.25% errors')
    expect(container.textContent).toContain('Library Sources')

    const pageText = container.textContent || ''
    expect(pageText.indexOf('Release Results')).toBeLessThan(pageText.indexOf('Library Sources'))

    const rootCauseLink = container.querySelector('a[href="/api/v1/tests/run-a/archive/root-cause"]') as HTMLAnchorElement | null
    expect(rootCauseLink).toBeTruthy()
    expect(rootCauseLink?.textContent).toBe('Root Cause')
  })
})
