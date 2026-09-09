// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ResultsPage from './ResultsPage'
import { buildComparisonSummarySeries, comparisonChartYAxisLabel } from './resultsComparisonChartModel'
import { downloadBlob } from '../utils/fileUtils'
import type { WailsDesktopBridge } from '../desktop/desktopBridge'

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
  downloadTestArchiveMultiComparison: vi.fn(),
}

const libraryApiMock = {
  listRemotes: vi.fn(),
  localCatalog: vi.fn(),
  remoteCatalog: vi.fn(),
  pullRemoteItem: vi.fn(),
  pushLocalItem: vi.fn(),
  compareItems: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useLibraryApi', () => ({
  useLibraryApi: () => libraryApiMock,
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

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

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
          duration: '10m',
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
      count: 2,
      total: 2,
      limit: 1000,
      offset: 0,
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
    apiMock.downloadTestArchiveMultiComparison.mockResolvedValue(new Blob(['backend multi comparison csv'], { type: 'text/csv' }))
    libraryApiMock.listRemotes.mockResolvedValue({
      remotes: [{
        id: 'remote-1',
        name: 'Team AKS',
        base_url: 'https://apexload.example.test',
        default_project_id: 'project-1',
        project_mappings: { 'project-1': 'project-1' },
        application_mappings: { 'app-1': 'remote-storefront-app' },
        release_mappings: { 'rel-1': 'remote-rel-1', 'rel-2': 'remote-rel-2' },
        auth_method: 'service_account',
        credential_configured: true,
        status: 'healthy',
      }],
      count: 1,
    })
    libraryApiMock.localCatalog.mockResolvedValue({
      items: [
        {
          kind: 'result',
          id: 'run-a',
          name: 'iPhone Checkout',
          project_id: 'project-1',
          release_id: 'rel-1',
          release_name: 'Sprint 24',
          status: 'COMPLETE',
          content_hash: 'runahash123456',
          execution_run_date: '2026-05-12T18:00:00Z',
          updated_at: '2026-05-12T18:05:00Z',
          has_archive: true,
        },
        {
          kind: 'result',
          id: 'run-b',
          name: 'iPad Checkout',
          project_id: 'project-1',
          release_id: 'rel-1',
          release_name: 'Sprint 24',
          status: 'COMPLETE',
          content_hash: 'runbhash123456',
          execution_run_date: '2026-05-12T19:00:00Z',
          updated_at: '2026-05-12T19:05:00Z',
          has_archive: true,
        },
      ],
      count: 2,
    })
    libraryApiMock.remoteCatalog.mockResolvedValue({
      remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
      catalog: { items: [], count: 0 },
    })
    libraryApiMock.pullRemoteItem.mockResolvedValue({ import: { items: [{ kind: 'result', local_item_id: 'pulled-run', action: 'created' }] } })
    libraryApiMock.pushLocalItem.mockResolvedValue({ import: { items: [{ kind: 'result', local_item_id: 'run-a', action: 'updated' }] } })
    libraryApiMock.compareItems.mockResolvedValue({ kind: 'result', status: 'synced', local_hash: 'localhash123456', remote_hash: 'remotehash123456' })

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    delete (window as BridgeWindow).go
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.clearAllMocks()
  })

  it('uses one line color per compared run across TPS and P95 series', () => {
    const runs = [
      { id: '90328d19', label: 'ACME (90328d19)' },
      { id: '44641072', label: 'ACME (44641072)' },
      { id: 'f4389ac7', label: 'ACME (f4389ac7)' },
    ]
    const tpsSeries = buildComparisonSummarySeries(runs, 'tps')
    const p95Series = buildComparisonSummarySeries(runs, 'p95')

    expect(tpsSeries).toEqual([
      { dataKey: 'run_0_90328d19_tps', name: 'ACME (90328d19) TPS', stroke: 'var(--chart-1)', yAxisId: 'value' },
      { dataKey: 'run_1_44641072_tps', name: 'ACME (44641072) TPS', stroke: 'var(--chart-2)', yAxisId: 'value' },
      { dataKey: 'run_2_f4389ac7_tps', name: 'ACME (f4389ac7) TPS', stroke: 'var(--chart-3)', yAxisId: 'value' },
    ])
    expect(p95Series).toEqual([
      { dataKey: 'run_0_90328d19_p95', name: 'ACME (90328d19) P95', stroke: 'var(--chart-1)', yAxisId: 'value' },
      { dataKey: 'run_1_44641072_p95', name: 'ACME (44641072) P95', stroke: 'var(--chart-2)', yAxisId: 'value' },
      { dataKey: 'run_2_f4389ac7_p95', name: 'ACME (f4389ac7) P95', stroke: 'var(--chart-3)', yAxisId: 'value' },
    ])
  })

  it('labels comparison chart axes with the selected metric units', () => {
    expect(comparisonChartYAxisLabel({
      id: 'p95',
      label: 'P95',
      description: '95th percentile response time',
      valueFormat: 'ms',
    })).toBe('P95 (ms)')
    expect(comparisonChartYAxisLabel({
      id: 'errors',
      label: 'Errors',
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
		  activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
		  activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })

    expect(container.textContent).toContain('Loading result sources...')
    expect(container.textContent).toContain('Loading results...')
    expect(container.textContent).not.toContain('No result sources yet.')

    await act(async () => {
      resolveTests({ tests: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Loading result sources...')
    expect(container.textContent).toContain('No result sources yet.')
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
        Search: {
          transactions: 20,
          errors: 0,
          error_pct: 0,
          avg_response_ms: id === 'run-a' ? 90 : 120,
          min_response_ms: 60,
          max_response_ms: 180,
          peak_tps: id === 'run-a' ? 4 : 6,
          p95_ms: id === 'run-a' ? 110 : 145,
        },
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
		  activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
		  activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
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

    expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()
    expect(container.querySelector('.results-compare-card')).toBeNull()
    expect(container.textContent).toContain('Ready to compare 2 archived runs')
    const runComparisonButton = buttonByText(container, 'Run Comparison')
    expect(runComparisonButton?.disabled).toBe(false)

    await act(async () => {
      runComparisonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('LOC-RUN-20260512-001')
    expect(container.textContent).toContain('LOC-RUN-20260512-002')
    expect(container.textContent).toContain('Checkout (LOC-RUN-20260512-001)')
    expect(container.textContent).toContain('Checkout (LOC-RUN-20260512-002)')
    expect(container.textContent).toContain('Search')
    expect(container.textContent).toContain('Run')
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('run-a', { sample_limit: 5000, action: undefined })
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('run-b', { sample_limit: 5000, action: undefined })
    expect(container.textContent).toContain('Curves use up to 5,000 representative samples per run.')
    let comparisonTable = container.querySelector('.results-compare-table') as HTMLElement
    expect(comparisonTable.textContent).toContain('Peak TPS')
    expect(comparisonTable.textContent).toContain('4.0')
    expect(comparisonTable.textContent).toContain('6.0')
    expect(comparisonTable.textContent).toContain('+2.0')
    expect(container.textContent).toContain('Reference')

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
    comparisonTable = container.querySelector('.results-compare-table') as HTMLElement
    expect(comparisonTable.textContent).toContain('P95')
    expect(comparisonTable.textContent).toContain('110.0 ms')
    expect(comparisonTable.textContent).toContain('145.0 ms')
    expect(comparisonTable.textContent).toContain('+35.0 ms')
    expect(comparisonTable.textContent).not.toContain('Avg ART')
  })

  it('filters pairwise comparison cards, table, graph, and export by common transaction', async () => {
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
        Checkout: {
          transactions: id === 'run-a' ? 50 : 60,
          errors: id === 'run-a' ? 5 : 0,
          error_pct: id === 'run-a' ? 10 : 0,
          avg_response_ms: id === 'run-a' ? 120 : 150,
          min_response_ms: 80,
          max_response_ms: 250,
        },
        [id === 'run-a' ? 'Search' : 'Browse']: {
          transactions: 10,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 90,
          min_response_ms: 70,
          max_response_ms: 110,
        },
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
    apiMock.getTestArchiveSummaries.mockImplementation(async (id: string, params?: { action?: string }) => ({
      summaries: params?.action === 'Checkout'
        ? [
            { timestamp: '2026-05-12T18:01:00Z', tps: id === 'run-a' ? 5 : 7, avg_response_ms: id === 'run-a' ? 120 : 150, p95_ms: id === 'run-a' ? 180 : 220, error_pct: id === 'run-a' ? 10 : 0 },
          ]
        : [
            { timestamp: '2026-05-12T18:01:00Z', tps: id === 'run-a' ? 30 : 28, avg_response_ms: id === 'run-a' ? 95 : 102, p95_ms: id === 'run-a' ? 110 : 140, error_pct: 0 },
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
    })
    await flushPromises()

    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    await act(async () => {
      rowCheckboxes[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[0].dispatchEvent(new Event('change', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => {
      buttonByText(container, 'Run Comparison')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    const transactionSelect = selectByLabel(container, 'Request / action')
    expect(transactionSelect).toBeTruthy()
    expect(Array.from(transactionSelect!.options).map(option => option.textContent)).toEqual(['Named transaction population', 'Checkout'])
    const comparisonCard = container.querySelector('.results-compare-card') as HTMLElement
    expect(comparisonCard.textContent).toContain('Search')
    expect(comparisonCard.textContent).toContain('Browse')

    await changeSelect(transactionSelect!, 'Checkout')
    await flushPromises()

    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('run-a', { sample_limit: 5000, action: 'Checkout' })
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('run-b', { sample_limit: 5000, action: 'Checkout' })
    expect(container.textContent).toContain('50')
    expect(container.textContent).toContain('60')
    expect(container.textContent).toContain('120.0 ms')
    expect(container.textContent).toContain('150.0 ms')
    expect(container.textContent).toContain('5.0')
    expect(container.textContent).toContain('7.0')
    let comparisonTable = container.querySelector('.results-compare-table') as HTMLElement
    expect(comparisonTable.textContent).toContain('Peak TPS')
    expect(comparisonTable.textContent).toContain('+2.0')
    expect(container.textContent).toContain('Showing transactions per second; color identifies the run.')
    expect(comparisonCard.textContent).not.toContain('Search')
    expect(comparisonCard.textContent).not.toContain('Browse')
    expect(comparisonCard.textContent).not.toContain('CPU Peak')
    expect(comparisonCard.textContent).not.toContain('Peak VUsers')

    const p95Tab = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'P95') as HTMLButtonElement | undefined
    await act(async () => {
      p95Tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    comparisonTable = container.querySelector('.results-compare-table') as HTMLElement
    expect(comparisonTable.textContent).toContain('P95')
    expect(comparisonTable.textContent).toContain('180.0 ms')
    expect(comparisonTable.textContent).toContain('220.0 ms')
    expect(comparisonTable.textContent).toContain('+40.0 ms')

    const exportButton = buttonByText(container, 'Export CSV')
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.downloadTestArchiveComparison).toHaveBeenCalledWith('run-b', 'run-a', 'device', 'Checkout')
    const [, filename] = vi.mocked(downloadBlob).mock.calls[0]
    expect(filename).toBe('archive-comparison-run-b-run-a-device-Checkout.csv')
  })

  it('allows selecting more than two results without auto-running comparison', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'run-a',
          ref_key: 'LOC-RUN-20260512-001',
          name: 'Checkout A',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          total_vusers: 10,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-001', transaction_count: 1000 },
        },
        {
          id: 'run-b',
          ref_key: 'LOC-RUN-20260512-002',
          name: 'Checkout B',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          total_vusers: 12,
          duration: '5m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-002', transaction_count: 1100 },
        },
        {
          id: 'run-c',
          ref_key: 'LOC-RUN-20260512-003',
          name: 'Checkout C',
          status: 'COMPLETE',
          release_id: 'rel-1',
          total_vusers: 14,
          duration: '5m',
          created_at: '2026-05-12T20:00:00Z',
          ended_at: '2026-05-12T20:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-003', transaction_count: 1200 },
        },
      ],
    })

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

    expect(container.querySelector<HTMLInputElement>('input[aria-label="Select all visible results"]')).not.toBeNull()
    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(3)

    await act(async () => {
      rowCheckboxes.forEach(checkbox => {
        checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        checkbox.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await Promise.resolve()
    })

    expect(container.textContent).toContain('3 selected')
    expect(container.textContent).toContain('Ready to compare 3 archived runs')
    const runComparisonButton = buttonByText(container, 'Run Comparison')
    expect(runComparisonButton?.disabled).toBe(false)
    expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()
    expect(apiMock.getTestArchiveSummaries).not.toHaveBeenCalled()
    expect(container.querySelector('.results-compare-card')).toBeNull()

    await act(async () => {
      runComparisonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.getTestArchiveReport).toHaveBeenCalledTimes(3)
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledTimes(3)
    expect(container.textContent).toContain('Compare 2-10 archived runs')
    expect(container.textContent).toContain('Baseline')
    expect(container.textContent).toContain('Run 2')
    expect(container.textContent).toContain('Run 3')
    expect(container.textContent).toContain('Checkout A')
    expect(container.textContent).toContain('Checkout B')
    expect(container.textContent).toContain('Checkout C')

    const exportButton = buttonByText(container, 'Export CSV')
    expect(exportButton).toBeDefined()

    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.downloadTestArchiveMultiComparison).toHaveBeenCalledWith('run-a', ['run-b', 'run-c'], 'device')
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    const [blob, filename] = vi.mocked(downloadBlob).mock.calls[0]
    expect(filename).toBe('archive-comparison-run-a-3-runs-device.csv')
    expect(await blob.text()).toBe('backend multi comparison csv')
  })

  it('passes the transaction filter to multi-run comparison export', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'run-a',
          ref_key: 'LOC-RUN-20260512-001',
          name: 'Checkout A',
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
          name: 'Checkout B',
          status: 'COMPLETE',
          total_vusers: 12,
          duration: '5m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-002', transaction_count: 1100 },
        },
        {
          id: 'run-c',
          ref_key: 'LOC-RUN-20260512-003',
          name: 'Checkout C',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          total_vusers: 14,
          duration: '5m',
          created_at: '2026-05-12T20:00:00Z',
          ended_at: '2026-05-12T20:05:00Z',
          archive: { ref_key: 'LOC-RUN-20260512-003', transaction_count: 1200 },
        },
      ],
    })
    apiMock.getTestArchiveReport.mockImplementation(async (id: string) => ({
      manifest: { test_id: id, provenance: { runtime_settings: {} } },
      overview: {
        transactions: 1000,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 100,
        min_response_ms: 80,
        max_response_ms: 140,
        resource_samples: 0,
        interval_summaries: 1,
        peak_tps: 30,
        peak_active_vusers: 10,
      },
      by_action: {
        Checkout: {
          transactions: id === 'run-a' ? 50 : id === 'run-b' ? 60 : 70,
          errors: 0,
          error_pct: 0,
          avg_response_ms: id === 'run-a' ? 100 : id === 'run-b' ? 120 : 140,
          min_response_ms: 80,
          max_response_ms: 180,
        },
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
    apiMock.getTestArchiveSummaries.mockResolvedValue({ summaries: [{ timestamp: '2026-05-12T18:01:00Z', tps: 5, avg_response_ms: 100, p95_ms: 120, error_pct: 0 }] })

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
    await flushPromises()

    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(3)
    await act(async () => {
      rowCheckboxes.forEach(checkbox => {
        checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        checkbox.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await Promise.resolve()
    })

    await act(async () => {
      buttonByText(container, 'Run Comparison')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })
    await flushPromises()

    expect(container.textContent).toContain('Run 3')
    expect(Array.from(selectByLabel(container, 'Request / action')!.options).map(option => option.textContent)).toEqual(['Named transaction population', 'Checkout'])

    await changeSelect(selectByLabel(container, 'Request / action')!, 'Checkout')
    await flushPromises()
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('run-c', expect.objectContaining({ action: 'Checkout' }))

    const exportButton = buttonByText(container, 'Export CSV')
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.downloadTestArchiveMultiComparison).toHaveBeenCalledWith('run-a', ['run-b', 'run-c'], 'device', 'Checkout')
    const [, filename] = vi.mocked(downloadBlob).mock.calls[0]
    expect(filename).toBe('archive-comparison-run-a-3-runs-device-Checkout.csv')
  })

  it('disables comparison when more than ten archived runs are selected', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: Array.from({ length: 11 }, (_, index) => ({
        id: `run-${index + 1}`,
        ref_key: `LOC-RUN-20260512-${String(index + 1).padStart(3, '0')}`,
        name: `Checkout ${index + 1}`,
        status: 'COMPLETE',
        application_id: 'app-1',
        release_id: 'rel-1',
        total_vusers: 10 + index,
        duration: '5m',
        created_at: `2026-05-12T${String(8 + index).padStart(2, '0')}:00:00Z`,
        ended_at: `2026-05-12T${String(8 + index).padStart(2, '0')}:05:00Z`,
        archive: { ref_key: `LOC-RUN-20260512-${String(index + 1).padStart(3, '0')}`, transaction_count: 1000 + index },
      })),
    })

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

    expect(container.querySelector<HTMLInputElement>('input[aria-label="Select all visible results"]')).not.toBeNull()
    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(11)

    await act(async () => {
      rowCheckboxes.forEach(checkbox => {
        checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        checkbox.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await Promise.resolve()
    })

    expect(container.textContent).toContain('11 selected')
    expect(container.textContent).toContain('11 archived runs selected; choose 10 or fewer to compare')
    expect(buttonByText(container, 'Run Comparison')?.disabled).toBe(true)
    expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()
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

    expect(container.querySelector('a[href="/api/v1/tests/run-a/report"]')).toBeNull()
    expect(container.textContent).not.toContain('Download Report')

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
      expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()
      expect(container.querySelector('.results-compare-card')).toBeNull()
      buttonByText(container, 'Run Comparison')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
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

  it('keeps the active comparison from reloading during results auto-refresh', async () => {
    vi.useFakeTimers()

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
    await flushPromises()

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
      buttonByText(container, 'Run Comparison')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.getTestArchiveReport).toHaveBeenCalledTimes(2)
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Comparison loaded')
    expect(container.textContent).not.toContain('Loading comparison...')

    apiMock.getTestArchiveReport.mockClear()
    apiMock.getTestArchiveSummaries.mockClear()

    await act(async () => {
      vi.advanceTimersByTime(10_000)
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.listTests).toHaveBeenCalledTimes(2)
    expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()
    expect(apiMock.getTestArchiveSummaries).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Comparison loaded')
    expect(container.textContent).not.toContain('Loading comparison...')
  })

  it('opens the completed-run workspace from both the row and its single results action', async () => {
    const onSelectTest = vi.fn()
    const onOpenTestArchive = vi.fn()
    const onOpenReport = vi.fn()

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={onSelectTest}
          onOpenTestArchive={onOpenTestArchive}
          onOpenReport={onOpenReport}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const row = Array.from(container.querySelectorAll('tbody tr')).find(candidate => candidate.textContent?.includes('iPhone Checkout'))
    expect(row).toBeDefined()
    const rowButtons = Array.from(row?.querySelectorAll('button') ?? []) as HTMLButtonElement[]
    const viewResults = rowButtons.find(button => button.textContent?.trim() === 'View results')
    expect(viewResults).toBeDefined()
    expect(rowButtons.some(button => ['Run Dashboard', 'Run Archive', 'Open Report'].includes(button.textContent?.trim() || ''))).toBe(false)
    await act(async () => row?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onOpenReport).toHaveBeenCalledWith('run-a')
    onOpenReport.mockClear()
    await act(async () => viewResults?.click())
    expect(onOpenReport).toHaveBeenCalledWith('run-a')
    expect(onSelectTest).not.toHaveBeenCalled()
    expect(onOpenTestArchive).not.toHaveBeenCalled()
  })

  it('opens results directly on filters, library sources, and row actions', async () => {
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

    expect(container.querySelector('.results-overview')).toBeNull()
    expect(container.querySelector('.results-governance-grid')).toBeNull()
    expect(container.textContent).not.toContain('Release SLA Gate')
    expect(container.textContent).not.toContain('External Results')
    expect(apiMock.getReleaseSLAGovernance).not.toHaveBeenCalled()
    expect(apiMock.listExternalResultImports).toHaveBeenCalledWith(expect.objectContaining({
      application_id: 'app-1',
      limit: 1000,
      offset: 0,
      release_id: 'rel-1',
    }))
    expect(container.textContent).toContain('Results Library')
    expect(container.querySelector('.results-filter-bar')).toBeNull()
    expect(selectByLabel(container, 'Associated Release')?.value).toBe('rel-1')
    expect(inputByPlaceholder(container, 'Find releases or results...')).toBeTruthy()
    expect(selectByLabel(container, 'Status')?.value).toBe('all')
    expect(selectByLabel(container, 'Archive State')?.value).toBe('all')
    expect(container.textContent).not.toContain('Release Results')
    expect(container.textContent).toContain('Library Sources')
    expect(container.querySelectorAll('.library-source-panel')).toHaveLength(1)
    expect(container.querySelectorAll('.library-source-table')).toHaveLength(1)
    expect(apiMock.listTests).toHaveBeenCalledWith(expect.objectContaining({
      application_id: 'app-1',
      limit: 1000,
      release_id: 'rel-1',
      summary: true,
    }))
    expect(container.querySelector('.results-truncation-status')).toBeNull()
    expect(Array.from(container.querySelectorAll('h3.section-title')).map(header => header.textContent)).toEqual(['Library Sources'])
    const headerLabels = Array.from(container.querySelectorAll('.library-source-table thead th'))
      .map(header => (header.textContent || '').replace(/[↕↑↓]/g, '').trim())
    expect(headerLabels).toEqual(['', 'Source', 'Scenario', 'Status', 'VUsers', 'Groups', 'Started', 'Duration', 'Archive', 'Actions'])
    const sourceSortButton = Array.from(container.querySelectorAll<HTMLButtonElement>('.library-source-table thead th.sortable > button'))
      .find(button => button.textContent?.includes('Source'))
    const startedSortButton = Array.from(container.querySelectorAll<HTMLButtonElement>('.library-source-table thead th.sortable > button'))
      .find(button => button.textContent?.includes('Started'))
    expect(sourceSortButton?.closest('th')?.getAttribute('aria-sort')).toBe('none')
    expect(startedSortButton?.closest('th')?.getAttribute('aria-sort')).toBe('descending')
    await act(async () => {
      sourceSortButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(sourceSortButton?.closest('th')?.getAttribute('aria-sort')).toBe('ascending')
    const sourceCell = sourceRowNamed(container, 'iPhone Checkout').querySelector('.results-source-cell')
    expect(sourceCell?.textContent?.trim()).toBe('Local')
    const actualDurationCell = sourceRowNamed(container, 'iPhone Checkout').querySelectorAll('td')[7]
    expect(actualDurationCell?.textContent?.trim()).toBe('5m')
    expect(container.textContent).not.toContain('local only')
    expect(container.textContent).not.toContain('Visible Sources')
    expect(container.querySelector('.results-selection-bar')).toBeNull()
    expect(container.querySelector('.results-table-selection-toolbar')).toBeNull()
    expect(buttonByText(container, 'Run Comparison')).toBeUndefined()
    expect(container.textContent).toContain('Select 2-10 archived local results to compare')
    expect(container.textContent).toContain('Push')
    expect(container.querySelector('.library-source-table thead th:last-child')?.classList.contains('results-actions-column')).toBe(true)
    expect(sourceRowNamed(container, 'iPhone Checkout').querySelector('td:last-child .results-row-actions')).toBeTruthy()

    await changeInput(inputByPlaceholder(container, 'Find releases or results...'), 'LOC-RUN-20260512-001')
    expect(sourceRowNamed(container, 'iPhone Checkout')).toBeDefined()
    expect(container.textContent).not.toContain('iPad Checkout')
    expect(container.textContent).not.toContain('Visible Sources')
    expect(container.querySelector('.results-selection-bar')).toBeNull()
    await changeInput(inputByPlaceholder(container, 'Find releases or results...'), '')

    expect(container.querySelector('a[href="/api/v1/tests/run-a/archive/root-cause"]')).toBeNull()
    expect(sourceRowNamed(container, 'iPhone Checkout').textContent).not.toContain('Root Cause')
  })

  it('reconciles partial bulk deletes, hides confirmed deletions, and retains only failed selections', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    apiMock.deleteTest.mockImplementation(async (id: string) => {
      if (id === 'run-b') throw new Error('result is locked')
    })

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
    await flushPromises()

    const selectAll = container.querySelector<HTMLInputElement>('input[aria-label="Select all visible results"]')
    expect(selectAll).not.toBeNull()
    await act(async () => {
      selectAll?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]')).every(checkbox => checkbox.checked)).toBe(true)
    expect(buttonByText(container, 'Delete Selected (2)')).toBeDefined()

    await act(async () => {
      buttonByText(container, 'Delete Selected (2)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    await flushPromises()

    expect(apiMock.deleteTest).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('iPhone Checkout')
    expect(container.textContent).toContain('iPad Checkout')
    expect(container.textContent).toContain('Deleted 1 of 2 selected results. run-b: result is locked')
    expect(container.querySelector('.results-table-selection-toolbar')?.textContent).toContain('1 selected')
    expect(buttonByText(container, 'Delete Selected (1)')).toBeDefined()
  })

  it('coalesces repeated row delete clicks while the first request is in flight', async () => {
    const confirmDelete = vi.fn(() => true)
    vi.stubGlobal('confirm', confirmDelete)
    let resolveDelete: (() => void) | undefined
    apiMock.deleteTest.mockReturnValue(new Promise<void>(resolve => { resolveDelete = resolve }))

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
    await flushPromises()

    const row = sourceRowNamed(container, 'iPhone Checkout')
    const deleteButton = buttonInRow(row, 'Delete')
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(confirmDelete).toHaveBeenCalledTimes(1)
    expect(apiMock.deleteTest).toHaveBeenCalledTimes(1)
    expect(buttonInRow(row, 'Deleting…')?.disabled).toBe(true)

    await act(async () => resolveDelete?.())
    await flushPromises()
  })

  it('does not resurrect a deleted result when an older paginated tail resolves later', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    const runA = {
      id: 'run-a',
      name: 'iPhone Checkout',
      status: 'COMPLETE',
      total_vusers: 10,
      duration: '5m',
      created_at: '2026-05-12T18:00:00Z',
      ended_at: '2026-05-12T18:05:00Z',
      archive: { transaction_count: 1000 },
    }
    const runB = {
      id: 'run-b',
      name: 'iPad Checkout',
      status: 'COMPLETE',
      total_vusers: 12,
      duration: '5m',
      created_at: '2026-05-12T19:00:00Z',
      ended_at: '2026-05-12T19:05:00Z',
      archive: { transaction_count: 1100 },
    }
    let deleted = false
    let resolveStaleTail: ((value: { tests: typeof runA[]; total: number; count: number; limit: number; offset: number }) => void) | undefined
    apiMock.listTests.mockImplementation(async (params: { offset?: number }) => {
      if ((params.offset || 0) > 0) {
        return new Promise(resolve => { resolveStaleTail = resolve })
      }
      const tests = deleted ? [runB] : [runA, runB]
      return { tests, total: deleted ? 1 : 3, count: tests.length, limit: 1000, offset: 0 }
    })
    apiMock.deleteTest.mockImplementation(async () => { deleted = true })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })
    await flushPromises()
    expect(resolveStaleTail).toBeDefined()

    const deleteButton = buttonInRow(sourceRowNamed(container, 'iPhone Checkout'), 'Delete')
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(container.textContent).not.toContain('iPhone Checkout')

    await act(async () => {
      resolveStaleTail?.({ tests: [runA], total: 3, count: 1, limit: 1000, offset: 2 })
      await Promise.resolve()
    })
    await flushPromises()

    expect(container.textContent).not.toContain('iPhone Checkout')
    expect(container.textContent).toContain('iPad Checkout')
  })

  it('prunes destructive selections when filters hide the selected row', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
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
    await flushPromises()

    const checkbox = container.querySelector<HTMLInputElement>('input[aria-label="Select iPhone Checkout"]')
    await act(async () => {
      checkbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      checkbox?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(buttonByText(container, 'Delete Selected (1)')).toBeDefined()

    await changeInput(inputByPlaceholder(container, 'Find releases or results...'), 'iPad Checkout')
    await flushPromises()

    expect(buttonByText(container, 'Delete Selected (0)')).toBeUndefined()
    expect(container.querySelector('.results-table-selection-toolbar')).toBeNull()
    expect(apiMock.deleteTest).not.toHaveBeenCalled()
  })

  it('filters unified local and remote result sources by active release and exposes remote actions', async () => {
    const activeReleaseTests = [
      {
        id: 'run-a',
        name: 'iPhone Checkout',
        status: 'COMPLETE',
        application_id: 'app-1',
        release_id: 'rel-1',
        created_at: '2026-05-12T18:00:00Z',
        archive: { transaction_count: 1000 },
      },
      {
        id: 'run-b',
        name: 'iPad Checkout',
        status: 'COMPLETE',
        application_id: 'app-1',
        release_id: 'rel-1',
        created_at: '2026-05-12T19:00:00Z',
        archive: { transaction_count: 1100 },
      },
    ]
    apiMock.listTests.mockImplementation(async (params: { release_id?: string }) => {
      const tests = params.release_id
        ? activeReleaseTests
        : [
            ...activeReleaseTests,
            {
              id: 'run-other-release',
              name: 'Local Other Release',
              status: 'COMPLETE',
              application_id: 'app-1',
              release_id: 'rel-2',
              created_at: '2026-05-14T10:00:00Z',
              archive: { transaction_count: 1200 },
            },
          ]
      return { tests, count: tests.length, total: tests.length, limit: 1000, offset: 0 }
    })
    libraryApiMock.remoteCatalog.mockImplementation(async (_remoteId: string, _kind: string, _projectId: string, page?: { offset?: number }) => {
      if (page?.offset === 0) {
        return {
          remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
          catalog: {
            items: Array.from({ length: 50 }, (_, index) => ({
              kind: 'result',
              id: `remote-filler-${index}`,
              name: `Remote filler ${index}`,
              project_id: 'project-1',
              application_id: 'remote-back-office-app',
              release_id: 'rel-1',
              release_name: 'Sprint 24',
              status: 'COMPLETE',
            })),
            count: 50,
            total: 56,
            limit: 100,
            offset: 0,
            has_more: true,
          },
        }
      }
      return {
        remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
        catalog: {
          items: [
            {
              kind: 'result',
              id: 'remote-run-a',
              name: 'Remote ID Match',
              project_id: 'project-1',
              application_id: 'remote-storefront-app',
              release_id: 'remote-rel-1',
              release_name: 'Sprint 24',
              status: 'COMPLETE',
              content_hash: 'remoteidmatch123456',
              execution_run_date: '2026-05-13T12:00:00Z',
              updated_at: '2026-05-13T12:05:00Z',
              has_archive: true,
              sync_status: 'synced',
              local_item_id: 'run-a',
              remote_item_id: 'remote-run-a',
            },
            {
              kind: 'result',
              id: 'remote-name-match',
              name: 'Remote Name Match',
              project_id: 'project-1',
              application_id: 'remote-storefront-app',
              release_id: 'remote-rel-1',
              release_name: 'Sprint 24',
              status: 'FAILED',
              content_hash: 'remotenamematch123456',
              execution_run_date: '2026-05-13T13:00:00Z',
              updated_at: '2026-05-13T13:05:00Z',
              has_archive: false,
              sync_status: 'host only',
            },
            {
              kind: 'result',
              id: 'remote-same-name-other-app',
              name: 'Remote Same Name Other App',
              project_id: 'project-1',
              application_id: 'remote-back-office-app',
              release_id: 'remote-rel-1',
              release_name: 'Sprint 24',
              status: 'COMPLETE',
              content_hash: 'remoteotherapp123456',
              execution_run_date: '2026-05-13T14:00:00Z',
              updated_at: '2026-05-13T14:05:00Z',
              has_archive: true,
              sync_status: 'host only',
            },
            {
              kind: 'result',
              id: 'remote-unmapped-application',
              name: 'Remote Unmapped Application',
              project_id: 'project-1',
              application_id: 'remote-unmapped-app',
              release_id: 'remote-rel-1',
              release_name: 'Sprint 24',
              status: 'COMPLETE',
              content_hash: 'remoteunmapped123456',
              execution_run_date: '2026-05-13T14:30:00Z',
              updated_at: '2026-05-13T14:35:00Z',
              has_archive: true,
              sync_status: 'host only',
            },
            {
              kind: 'result',
              id: 'remote-other-release',
              name: 'Remote Other Release',
              project_id: 'project-1',
              application_id: 'remote-storefront-app',
              release_id: 'remote-rel-2',
              release_name: 'Sprint 25',
              status: 'COMPLETE',
              content_hash: 'remoteother123456',
              execution_run_date: '2026-05-14T12:00:00Z',
              updated_at: '2026-05-14T12:05:00Z',
              has_archive: true,
              sync_status: 'host only',
            },
            {
              kind: 'result',
              id: 'remote-duplicate-release-name',
              name: 'Remote Duplicate Release Name',
              project_id: 'project-1',
              application_id: 'remote-storefront-app',
              release_id: 'remote-rel-duplicate',
              release_name: 'Sprint 24',
              status: 'COMPLETE',
              content_hash: 'remoteduplicate123456',
              execution_run_date: '2026-05-13T15:00:00Z',
              updated_at: '2026-05-13T15:05:00Z',
              has_archive: true,
              sync_status: 'host only',
            },
          ],
          count: 6,
          total: 56,
          limit: 100,
          offset: 50,
          has_more: false,
        },
      }
    })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })
    await flushPromises()

    expect(sourceRowNamed(container, 'Remote ID Match').textContent).toContain('Remote')
    expect(sourceRowNamed(container, 'Remote Name Match').textContent).toContain('FAILED')
    expect(libraryApiMock.remoteCatalog).toHaveBeenCalledWith(
      'remote-1',
      'result',
      'project-1',
      expect.objectContaining({ limit: 100, offset: 50 }),
    )
    expect(container.textContent).not.toContain('Remote Same Name Other App')
    expect(container.textContent).not.toContain('Remote Unmapped Application')
    expect(container.textContent).not.toContain('Remote Other Release')
    expect(container.textContent).not.toContain('Remote Duplicate Release Name')
    expect(container.textContent).not.toContain('Local Other Release')
    expect(sourceRowNamed(container, 'Remote ID Match').textContent).toContain('-')
    expect(selectByLabel(container, 'Associated Release')?.value).toBe('rel-1')
    expect(apiMock.listTests).toHaveBeenCalledWith(expect.objectContaining({
      application_id: 'app-1',
      release_id: 'rel-1',
    }))

    await changeSelect(selectByLabel(container, 'Associated Release')!, 'all')
    await flushPromises()
    expect(container.textContent).not.toContain('Remote Same Name Other App')
    expect(container.textContent).not.toContain('Remote Unmapped Application')
    expect(container.textContent).not.toContain('Remote Duplicate Release Name')
    const remoteOtherReleaseRow = sourceRowNamed(container, 'Remote Other Release')
    expect(remoteOtherReleaseRow).toBeDefined()
    expect(sourceRowNamed(container, 'Local Other Release')).toBeDefined()
    const applicationCatalogCall = apiMock.listTests.mock.calls.find(([params]) => !params.release_id)
    expect(applicationCatalogCall?.[0]).toEqual(expect.objectContaining({
      application_id: 'app-1',
      project_id: 'project-1',
    }))
    expect(applicationCatalogCall?.[0]).not.toHaveProperty('release_id')

    const otherReleasePull = buttonInRow(remoteOtherReleaseRow, 'Pull')
    expect(otherReleasePull?.disabled).toBe(false)
    await act(async () => {
      otherReleasePull?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(libraryApiMock.pullRemoteItem).toHaveBeenCalledWith('remote-1', expect.objectContaining({
      kind: 'result',
      id: 'remote-other-release',
      remote_id: 'remote-other-release',
      application_id: 'app-1',
      release_id: 'rel-2',
    }))

    await changeSelect(selectByLabel(container, 'Associated Release')!, 'rel-1')
    await flushPromises()
    expect(container.textContent).not.toContain('Remote Same Name Other App')
    expect(container.textContent).not.toContain('Remote Unmapped Application')
    expect(container.textContent).not.toContain('Remote Duplicate Release Name')
    expect(container.textContent).not.toContain('Remote Other Release')
    expect(container.textContent).not.toContain('Local Other Release')

    const remoteIDRow = sourceRowNamed(container, 'Remote ID Match')
    const pullButton = buttonInRow(remoteIDRow, 'Pull')
    const compareButton = buttonInRow(remoteIDRow, 'Compare')
    const remoteLink = Array.from(remoteIDRow.querySelectorAll('a')).find(link => link.textContent?.trim() === 'Open in Remote Workspace') as HTMLAnchorElement | undefined
    expect(pullButton).toBeDefined()
    expect(compareButton).toBeDefined()
    expect(remoteLink?.href).toBe('https://apexload.example.test/#/test/remote-run-a')

    await act(async () => {
      compareButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(libraryApiMock.compareItems).toHaveBeenCalledWith('remote-1', expect.objectContaining({
      kind: 'result',
      local_id: 'run-a',
      remote_id: 'remote-run-a',
    }))

    await act(async () => {
      pullButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(libraryApiMock.pullRemoteItem).toHaveBeenCalledWith('remote-1', expect.objectContaining({
      kind: 'result',
      id: 'remote-run-a',
      remote_id: 'remote-run-a',
      application_id: 'app-1',
      release_id: 'rel-1',
    }))

    await changeInput(inputByPlaceholder(container, 'Find releases or results...'), 'Remote Name')
    expect(sourceRowNamed(container, 'Remote Name Match')).toBeDefined()
    expect(container.textContent).not.toContain('Remote ID Match')
    expect(container.textContent).not.toContain('iPhone Checkout')

    await changeInput(inputByPlaceholder(container, 'Find releases or results...'), '')
    await changeSelect(selectByLabel(container, 'Status')!, 'failed')
    expect(sourceRowNamed(container, 'Remote Name Match')).toBeDefined()
    expect(container.textContent).not.toContain('Remote ID Match')

    await changeSelect(selectByLabel(container, 'Status')!, 'all')
    await changeSelect(selectByLabel(container, 'Archive State')!, 'pending')
    expect(sourceRowNamed(container, 'Remote Name Match')).toBeDefined()
    expect(container.textContent).not.toContain('iPhone Checkout')
    expect(container.textContent).not.toContain('Remote ID Match')
  })

  it('keeps pulled result evidence usable as a local archive row after its remote is disconnected', async () => {
    apiMock.listExternalResultImports.mockResolvedValue({
      imports: [{
        id: 'imported-result-1',
        provider: 'apexload',
        name: 'Pulled Checkout Evidence',
        project_id: 'project-1',
        application_id: 'app-1',
        release_id: 'rel-1',
        content_hash: 'sha256:durable-evidence',
        has_archive: true,
        summary: { status: 'complete', transactions: 321 },
        created_at: '2026-05-15T12:00:00Z',
      }],
      count: 1,
      limit: 1000,
      offset: 0,
    })
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })
    await flushPromises()

    const row = sourceRowNamed(container, 'Pulled Checkout Evidence')
    expect(row.querySelector('.results-source-cell')?.textContent?.trim()).toBe('Local')
    expect(row.textContent).toContain('321 tx')
    expect(buttonInRow(row, 'Push')).toBeUndefined()
    expect(buttonInRow(row, 'Pull')).toBeUndefined()
    const archiveLink = Array.from(row.querySelectorAll('a')).find(link => link.textContent?.trim() === 'Open Archive')
    expect(archiveLink?.getAttribute('href')).toBe('/api/v1/library/results/imported-result-1/archive')
  })

  it('routes a pulled result archive through the native desktop resource bridge', async () => {
    const saveAPIResourceNative = vi.fn().mockResolvedValue('/tmp/Pulled Checkout Evidence.zip')
    ;(window as BridgeWindow).go = { main: { App: { SaveAPIResourceNative: saveAPIResourceNative } } }
    apiMock.listExternalResultImports.mockResolvedValue({
      imports: [{
        id: 'imported-result-1',
        provider: 'apexload',
        name: 'Pulled Checkout Evidence',
        project_id: 'project-1',
        application_id: 'app-1',
        release_id: 'rel-1',
        content_hash: 'sha256:durable-evidence',
        has_archive: true,
        summary: { status: 'complete', transactions: 321 },
        created_at: '2026-05-15T12:00:00Z',
      }],
      count: 1,
      limit: 1000,
      offset: 0,
    })
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })
    await flushPromises()

    const row = sourceRowNamed(container, 'Pulled Checkout Evidence')
    expect(Array.from(row.querySelectorAll('a')).some(link => link.textContent?.trim() === 'Open Archive')).toBe(false)
    await act(async () => {
      buttonInRow(row, 'Open Archive')?.click()
      await Promise.resolve()
    })

    expect(saveAPIResourceNative.mock.calls[0]?.slice(0, 2)).toEqual([
      '/api/v1/library/results/imported-result-1/archive',
      'Pulled Checkout Evidence-imported-result-1.zip',
    ])
  })

  it('compares a durable pulled result by its local import identity', async () => {
    apiMock.listExternalResultImports.mockResolvedValue({
      imports: [{
        id: 'imported-result-1',
        provider: 'apexload',
        name: 'Pulled Checkout Evidence',
        project_id: 'project-1',
        application_id: 'app-1',
        release_id: 'rel-1',
        content_hash: 'sha256:durable-evidence',
        has_archive: true,
        summary: { status: 'complete' },
        created_at: '2026-05-15T12:00:00Z',
      }],
      count: 1,
      limit: 1000,
      offset: 0,
    })
    libraryApiMock.remoteCatalog.mockResolvedValue({
      remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
      catalog: {
        items: [{
          kind: 'result',
          id: 'remote-result-1',
          remote_item_id: 'remote-result-1',
          local_item_id: 'imported-result-1',
          name: 'Remote Checkout Evidence',
          project_id: 'project-1',
          application_id: 'remote-storefront-app',
          release_id: 'remote-rel-1',
          release_name: 'Sprint 24',
          status: 'COMPLETE',
          content_hash: 'sha256:durable-evidence',
          has_archive: true,
          sync_status: 'synced',
        }],
        count: 1,
        total: 1,
        limit: 100,
        offset: 0,
        has_more: false,
      },
    })

    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', project_id: 'project-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', project_id: 'project-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })
    await flushPromises()

    const row = sourceRowNamed(container, 'Pulled Checkout Evidence')
    const compareButton = buttonInRow(row, 'Compare')
    expect(compareButton).toBeDefined()
    await act(async () => {
      compareButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(libraryApiMock.compareItems).toHaveBeenCalledWith('remote-1', expect.objectContaining({
      kind: 'result',
      local_id: 'imported-result-1',
      remote_id: 'remote-result-1',
    }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

function sourceRowNamed(container: HTMLElement, text: string) {
  const row = Array.from(container.querySelectorAll('.library-source-table tbody tr')).find(row => row.textContent?.includes(text))
  expect(row).toBeTruthy()
  return row as HTMLTableRowElement
}

function buttonInRow(row: HTMLTableRowElement, label: string) {
  return Array.from(row.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

function buttonByText(container: HTMLElement, label: string) {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

function inputByPlaceholder(container: HTMLElement, placeholder: string) {
  const input = container.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)
  expect(input).toBeTruthy()
  return input as HTMLInputElement
}

function selectByLabel(container: HTMLElement, labelText: string) {
  const label = Array.from(container.querySelectorAll('label')).find(label => label.querySelector('span')?.textContent === labelText)
  return label?.querySelector('select') as HTMLSelectElement | null
}

async function changeInput(input: HTMLInputElement, value: string) {
  await act(async () => {
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    valueSetter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function changeSelect(select: HTMLSelectElement, value: string) {
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
