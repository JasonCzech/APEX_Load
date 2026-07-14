// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DashboardPage from './DashboardPage'
import { downloadBlob } from '../utils/fileUtils'
import { resetSharedTestListStoreForTests } from '../hooks/useSharedTestList'

const apiMock = {
  listTests: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveReports: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  downloadReleaseTrend: vi.fn(),
  getReleaseTrend: vi.fn(),
  getLoadComparison: vi.fn(),
}

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

const applicationState = {
  activeApplication: { id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' },
  activeRelease: { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' },
  releases: [] as Array<{ id: string; application_id: string; name: string; created_at: string; updated_at: string; archived_at?: string }>,
  setActiveRelease: vi.fn(),
}

const projectState = {
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' },
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationState,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => projectState,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
;(globalThis as typeof globalThis & { ResizeObserver?: typeof ResizeObserver }).ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as typeof ResizeObserver

describe('DashboardPage', () => {
  let container: HTMLDivElement
  let root: Root
  const onSelectTest = vi.fn()
  const onNavigate = vi.fn()

  beforeEach(() => {
    resetSharedTestListStoreForTests()
    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }
    applicationState.activeApplication = { id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }
    applicationState.activeRelease = { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }
    applicationState.releases = [
      { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-10T00:00:00Z' },
      { id: 'rel-2', application_id: 'app-1', name: 'Sprint 23', created_at: '2026-04-02T00:00:00Z', updated_at: '2026-05-09T00:00:00Z' },
      { id: 'rel-3', application_id: 'app-1', name: 'Sprint 22', created_at: '2026-03-02T00:00:00Z', updated_at: '2026-05-08T00:00:00Z' },
      { id: 'rel-4', application_id: 'app-1', name: 'Sprint 21', created_at: '2026-02-02T00:00:00Z', updated_at: '2026-05-07T00:00:00Z' },
      { id: 'rel-5', application_id: 'app-1', name: 'Sprint 20', created_at: '2026-01-02T00:00:00Z', updated_at: '2026-05-06T00:00:00Z' },
      { id: 'rel-6', application_id: 'app-1', name: 'Sprint 19', created_at: '2025-12-02T00:00:00Z', updated_at: '2026-05-05T00:00:00Z' },
      { id: 'rel-7', application_id: 'app-1', name: 'Sprint 18', created_at: '2025-11-02T00:00:00Z', updated_at: '2026-05-04T00:00:00Z' },
      { id: 'rel-archived', application_id: 'app-1', name: 'Archived Sprint', created_at: '2025-10-02T00:00:00Z', updated_at: '2026-05-03T00:00:00Z', archived_at: '2026-05-04T00:00:00Z' },
    ]
    applicationState.setActiveRelease = vi.fn()
    apiMock.getHealth.mockResolvedValue({
      status: 'healthy',
      version: 'dev',
      ws_clients: 2,
      test_count: 3,
      capacity_availability: {
        effective_available_generators: 2,
        configured_available_generators: 2,
        source: 'configured_plus_live',
        warnings: [],
      },
    })
    apiMock.listTests.mockResolvedValue({
      tests: [
        {
          id: 'test-active',
          name: 'Checkout Load',
          status: 'RUNNING',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-1', script_name: 'Checkout', protocol: 'http', vusers: 10 }],
          total_vusers: 10,
          duration: '10m',
          created_at: '2026-05-12T20:00:00Z',
          runtime_settings: {
            browser: {
              device_profile: 'iphone_se',
              orientation: 'portrait',
              touch_mode: true,
              headless: false,
            },
          },
          live_metrics: { active_vusers: 8, tps: 25, avg_response_ms: 120, error_pct: 0.2 },
        },
        {
          id: 'test-complete',
          name: 'Release Smoke',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-2', script_name: 'Smoke', protocol: 'http', vusers: 1 }],
          total_vusers: 1,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          runtime_settings: {
            browser: {
              device_profile: 'pixel_8',
              orientation: 'landscape',
              touch_mode: true,
              headless: true,
            },
          },
          archive: { transaction_count: 1200 },
        },
        {
          id: 'test-complete-2',
          name: 'Checkout Archive',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-4', script_name: 'Checkout', protocol: 'http', vusers: 5 }],
          total_vusers: 5,
          duration: '8m',
          created_at: '2026-05-12T17:00:00Z',
          ended_at: '2026-05-12T17:08:00Z',
          archive: { transaction_count: 2400 },
        },
        {
          id: 'test-failed',
          name: 'Payments Failure Drill',
          status: 'FAILED',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-3', script_name: 'Payments', protocol: 'http', vusers: 20 }],
          total_vusers: 20,
          duration: '15m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:15:00Z',
        },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'controller-1',
        status: 'healthy',
        address: ':8080',
        version: 'dev',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'gen-a',
          generator_id: 'gen-a',
          status: 'connected',
          available_capacity: 1000,
          active_vusers: 100,
          cpu_percent: 12,
          memory_bytes: 1048576,
          goroutine_count: 8,
          cpu_limit_cores: 2,
          memory_limit_bytes: 2147483648,
          max_vusers: 10000,
        },
        {
          target: 'gen-b',
          generator_id: 'gen-b',
          status: 'unreachable',
          available_capacity: 0,
          active_vusers: 0,
          cpu_percent: 0,
          memory_bytes: 0,
          goroutine_count: 0,
          cpu_limit_cores: 2,
          memory_limit_bytes: 2147483648,
          max_vusers: 10000,
        },
      ],
    })
    apiMock.getTestArchiveReport.mockImplementation((testId: string) => Promise.resolve({
      manifest: {
        sla_breached: testId !== 'test-complete-2',
        provenance: {
          runtime_settings: {
            browser: {
              device_profile: 'pixel_8',
              orientation: 'landscape',
              touch_mode: true,
              headless: true,
            },
          },
        },
      },
      overview: {
        peak_tps: testId === 'test-complete-2' ? 45 : 32,
        avg_response_ms: testId === 'test-complete-2' ? 140 : 95,
        browser: {
          avg_lcp_ms: 240,
        },
        telemetry: {
          status: 'breached',
          samples: 3,
          breached_intervals: 1,
        },
        error_pct: testId === 'test-complete-2' ? 3.4 : 0,
        peak_active_vusers: 12,
        transactions: testId === 'test-complete-2' ? 2400 : 1200,
      },
      completeness: {
        score_pct: 88,
        status: 'needs_review',
      },
      resource_peaks: {
        cpu_process_pct: 64.2,
        mem_process_mb: 512,
        net_sent_kbps: 120,
        net_recv_kbps: 140,
        disk_read_kbps: 0,
        disk_write_kbps: 0,
      },
    }))
    apiMock.getTestArchiveReports.mockImplementation(async (testIds: string[]) => {
      const reports = Object.fromEntries(await Promise.all(testIds.map(async testId => (
        [testId, await apiMock.getTestArchiveReport(testId)] as const
      ))))
      return { reports }
    })
    apiMock.downloadReleaseTrend.mockResolvedValue(new Blob(['release trend csv'], { type: 'text/csv' }))
    apiMock.getReleaseTrend.mockResolvedValue({
      release_id: 'rel-1',
      run_count: 0,
      points: [],
      summary: { latest_regressed: false, avg_response_trend_pct: 0, error_pct_trend: 0 },
    })
    apiMock.getLoadComparison.mockResolvedValue({
      application_id: 'app-1',
      scope: 'transaction',
      selected_key: 'Checkout Flow',
      options: [
        { key: 'Checkout Flow', label: 'Checkout Flow', scope: 'transaction', source_kind: 'source_transaction', release_count: 2, run_count: 2, transaction_count: 80, sample_count: 4 },
      ],
      load_levels: [
        { id: 'vusers-5', label: '5 VUsers', min_vusers: 5, max_vusers: 5, exact: true, sample_count: 2 },
      ],
      releases: [
        {
          release_id: 'rel-1',
          release_name: 'Sprint 24',
          run_count: 1,
          transaction_count: 40,
          points: [{ load_level_id: 'vusers-5', load_label: '5 VUsers', tps: 8, avg_response_ms: 120, transactions: 40, errors: 0, error_pct: 0, run_count: 1, sample_count: 2 }],
        },
      ],
    })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    resetSharedTestListStoreForTests()
    vi.clearAllMocks()
  })

  it('wires snapshot cards to navigation targets without the removed summary cards', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{
            'test-active': [
              { time: '8:00:00 PM', vusers: 8, tps: 20, avgMs: 120, errorPct: 0.2, p50: 100, p90: 140, p99: 180 },
              { time: '8:00:03 PM', vusers: 8, tps: 25, avgMs: 118, errorPct: 0.2, p50: 98, p90: 138, p99: 175 },
            ],
          }}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 500, project_id: 'project-1', summary: true })
    expect(container.textContent).not.toContain('API Guide')
    expect(container.textContent).not.toContain('Customize Cards')

    expect(container.querySelectorAll('.dash-health-card')).toHaveLength(0)
    expect(container.textContent).not.toContain('API Server')
    expect(container.textContent).not.toContain('Active Runs')
    expect(container.querySelector('.dash-release-panel.is-open')).toBeTruthy()
    expect(getButtonByText('Collapse').getAttribute('aria-expanded')).toBe('true')
    expect(container.textContent).toContain('Operations Activity')
    expect(container.textContent).toContain('Generators Ready')
    expect(container.querySelector('.dash-attention')).toBeNull()
    expect(container.querySelectorAll('.release-panel-stat-grid.shared .release-panel-stat')).toHaveLength(6)
    const releaseOptions = Array.from(container.querySelectorAll<HTMLButtonElement>('.dash-release-option'))
    expect(releaseOptions.map(button => button.textContent?.trim())).toEqual(['Sprint 24', 'Sprint 23', 'Sprint 22', 'Sprint 21', 'Sprint 20', 'Sprint 19'])
    expect(releaseOptions.find(button => button.textContent?.trim() === 'Sprint 24')?.className).toContain('active')
    expect(container.textContent).not.toContain('Archived Sprint')
    clickButtonByText('Sprint 23')
    expect(applicationState.setActiveRelease).toHaveBeenCalledWith(expect.objectContaining({ id: 'rel-2', name: 'Sprint 23' }))
    const dashboardTabs = Array.from(container.querySelectorAll<HTMLButtonElement>('.dash-tab'))
    expect(dashboardTabs.map(tab => tab.textContent?.trim())).toEqual([
      'Recent Activity3',
      'Active Tests1',
      'Performance Trend2',
      'Release Trend Analysis2',
    ])
    expect(getTabByText('Recent Activity').getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Payments Failure Drill')

    clickButtonByText('Performance Trend')
    await flushDashboard()
    expect(getTabByText('Performance Trend').getAttribute('aria-selected')).toBe('true')
    const performancePanelText = container.querySelector('.dash-performance-trend')?.textContent || ''
    expect(performancePanelText).toContain('Graph Releases')
    expect(performancePanelText).toContain('Graph Runs')
    expect(performancePanelText).toContain('Load Levels')
    expect(performancePanelText).toContain('Transactions')
    expect(performancePanelText).toContain('Peak TPS')
    expect(performancePanelText).toContain('Avg ART')
    expect(performancePanelText).toContain('Errors')
    expect(performancePanelText).toContain('40')
    expect(performancePanelText).toContain('8.0')
    expect(performancePanelText).toContain('120 ms')
    expect(performancePanelText).not.toContain('SLA Breaches')
    expect(performancePanelText).not.toContain('Avg Completeness')
    expect(performancePanelText).not.toContain('Max CPU')
    expect(performancePanelText).not.toContain('Max Memory')
    expect(performancePanelText).not.toContain('Telemetry Breaches')
    expect(container.textContent).toContain('Breached')
    expect(container.textContent).toContain('Export CSV')
    expect(container.textContent).toContain('View Results')
    expect(container.textContent).not.toContain('Test Summary')
    expect(container.textContent).toContain('pixel_8 · Landscape · Touch enabled · Headless')
    expect(container.querySelector('[data-testid="release-trend-panel"]')).toBeNull()

    clickButtonByText('Release Trend Analysis')
    await flushDashboard()
    expect(getTabByText('Release Trend Analysis').getAttribute('aria-selected')).toBe('true')
    expect(apiMock.getReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(container.querySelector('[data-testid="release-trend-panel"]')).toBeTruthy()
    expect(container.textContent).toContain('No archived runs yet for this release.')

    clickButtonByText('API Health')
    expect(onNavigate).toHaveBeenCalledWith('monitoring')

    clickButtonByText('Payments Failure Drill failed')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Latest Result')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Failed Runs')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Current TPS')
    expect(onSelectTest).toHaveBeenCalledWith('test-active')
    onSelectTest.mockClear()

    clickButtonByText('Completed')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Active Now')
    expect(onSelectTest).toHaveBeenCalledWith('test-active')

    clickButtonByText('Performance Trend')
    clickButtonByText('Export CSV')
    await act(async () => {
      await Promise.resolve()
    })
    expect(apiMock.downloadReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    expect(vi.mocked(downloadBlob).mock.calls[0][1]).toBe('sprint-24-rel-1-trend.csv')

    clickButtonByText('Active Tests')
    expect(getTabByText('Active Tests').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-panel.is-open')).toBeTruthy()
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')

    clickButtonByText('Aggregate TPS (active tests)')
    expect(onNavigate).toHaveBeenCalledWith('tests')
  })

  it('collapses and restores the release snapshot panel', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    const panel = container.querySelector<HTMLElement>('#dashboard-release-snapshot-panel')
    expect(panel).toBeTruthy()
    expect(panel?.hidden).toBe(false)

    clickButtonByText('Collapse')
    expect(panel?.hidden).toBe(true)
    const railButton = getButtonByText('Snapshot')
    expect(railButton.getAttribute('aria-expanded')).toBe('false')
    expect(container.querySelector('.dash-release-panel.is-collapsed')).toBeTruthy()

    clickButtonByText('Open')
    expect(panel?.hidden).toBe(false)
    expect(getButtonByText('Collapse').getAttribute('aria-expanded')).toBe('true')
  })

  it('shows operations-focused snapshot details for admin audiences', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
          snapshotAudience="admin"
        />,
      )
    })

    await flushDashboard()

    const panelText = releasePanelText()
    expect(panelText).toContain('Operations Snapshot')
    expect(panelText).toContain('Operations Activity')
    expect(panelText).toContain('Generators Ready')
    expect(panelText).toContain('2/2')
    expect(panelText).toContain('WebSocket Clients')
    expect(panelText).toContain('Pending Runs')
    expect(panelText).not.toContain('Release Quality')

    clickButtonByText('New Test')
    expect(onNavigate).toHaveBeenCalledWith('new-test')
  })

  it('renders same-named archive-pending attention items with unique keys', async () => {
    // Two completed runs share a name and have no archive, so they produce
    // attention items with identical title + detail (different ids). The feed
    // key must stay unique or React drops/duplicates a row and warns.
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'web-test-a', name: 'web test', status: 'COMPLETE',
          application_id: 'app-1', release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 's1', script_name: 'web', protocol: 'http', vusers: 1 }],
          total_vusers: 1, duration: '1m', created_at: '2026-05-12T10:00:00Z', ended_at: '2026-05-12T10:01:00Z',
        },
        {
          id: 'web-test-b', name: 'web test', status: 'COMPLETE',
          application_id: 'app-1', release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 's2', script_name: 'web', protocol: 'http', vusers: 1 }],
          total_vusers: 1, duration: '1m', created_at: '2026-05-12T09:00:00Z', ended_at: '2026-05-12T09:01:00Z',
        },
      ],
    })

    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      await act(async () => {
        root.render(
          <DashboardPage
            testStatuses={{}}
            metricsHistory={{}}
            connectionState="connected"
            onSelectTest={onSelectTest}
            onNavigate={onNavigate}
            snapshotAudience="admin"
          />,
        )
      })
      await flushDashboard()

      const archivePendingItems = Array.from(container.querySelectorAll('.release-panel-feed-item'))
        .filter(item => item.textContent?.includes('archive pending'))
      expect(archivePendingItems).toHaveLength(2)

      const duplicateKeyWarnings = errorSpy.mock.calls.filter(call => String(call[0]).includes('same key'))
      expect(duplicateKeyWarnings).toEqual([])
    } finally {
      errorSpy.mockRestore()
    }
  })

  it('shows release-quality snapshot details for non-admin audiences', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
          snapshotAudience="user"
        />,
      )
    })

    await flushDashboard()

    const panelText = releasePanelText()
    expect(panelText).toContain('Release Quality')
    expect(panelText).toContain('SLA Breaches')
    expect(panelText).toContain('Avg Completeness')
    expect(panelText).toContain('Peak VUsers')
    expect(panelText).toContain('Telemetry Breaches')
    expect(panelText).toContain('Max Memory')
    expect(panelText).toContain('Latest Outcome')
    expect(panelText).not.toContain('Operations Activity')
    expect(panelText).not.toContain('Generators Ready')
  })

  it('uses theme-aware accent fills for dashboard controls', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const dashboardControlCss = [
      stylesheet.match(/\.release-panel-header \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-release-option\.active \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-tab\.active \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(stylesheet).not.toContain('.dash-health-card')
    expect(dashboardControlCss).toContain('color-mix(in srgb, var(--accent)')
    expect(dashboardControlCss).toContain('var(--accent-border)')
    expect(dashboardControlCss).not.toContain('rgba(14, 165, 233')
    expect(dashboardControlCss).not.toContain('rgba(125, 211, 252')
  })

  it('defines a solarized-safe segmented control for load comparison scope', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const scopeToggleCss = [
      stylesheet.match(/\.load-comparison-scope-toggle \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.load-comparison-scope-toggle \.btn-chip \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.load-comparison-scope-toggle \.btn-chip\.active,\n\.load-comparison-scope-toggle \.btn-chip\[aria-pressed='true'\] \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/:root\[data-theme='solarized-dark'\] \.load-comparison-scope-toggle \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/:root\[data-theme='solarized-dark'\] \.load-comparison-scope-toggle \.btn-chip\.active,\n:root\[data-theme='solarized-dark'\] \.load-comparison-scope-toggle \.btn-chip\[aria-pressed='true'\] \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(scopeToggleCss).toContain('border-radius: 999px')
    expect(scopeToggleCss).toContain('color-mix(in srgb, var(--border)')
    expect(scopeToggleCss).toContain('var(--accent-contrast)')
    expect(scopeToggleCss).toContain("data-theme='solarized-dark'")
    expect(scopeToggleCss).toContain('var(--theme-toggle-active-bg)')
    expect(scopeToggleCss).toContain('var(--theme-toggle-active-text)')
  })

  it('keeps the dashboard snapshot in a right-side panel layout', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const layoutCss = [
      stylesheet.match(/\.dash-dashboard-layout \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-dashboard-layout\.snapshot-collapsed \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-main-column \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/@media \(max-width:\s980px\) \{[\s\S]*?\.dash-main-column \{[\s\S]*?\n\s{2}\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-release-panel \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.release-panel-card \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.release-panel-rail \{[\s\S]*?writing-mode: vertical-rl[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.trend-table-wrap \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(layoutCss).toContain('grid-template-columns: minmax(0, 1fr) minmax(320px, 360px)')
    expect(layoutCss).toContain('grid-template-areas: "main snapshot"')
    expect(layoutCss).toContain('grid-template-columns: minmax(0, 1fr) 58px')
    expect(layoutCss).toContain('grid-area: main')
    expect(layoutCss).toContain('flex-direction: column')
    expect(layoutCss).toContain('display: contents')
    expect(layoutCss).toContain('position: sticky')
    expect(layoutCss).toContain('max-height: calc(100vh - 112px)')
    expect(layoutCss).toContain('writing-mode: vertical-rl')
    expect(layoutCss).toContain('max-height: var(--dashboard-table-max-height, none)')
    expect(stylesheet).not.toContain('max-height: 260px')
    expect(stylesheet).not.toContain('max-height: 430px')
  })

  it('defines compact active group metrics table styles', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const groupTableCss = [
      stylesheet.match(/\.dac-group-table-wrap \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dac-group-table \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dac-group-table th,\n\.dac-group-table td \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dac-group-table \.num \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dac-group-name \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(groupTableCss).toContain('overflow-x: auto')
    expect(groupTableCss).toContain('min-width: 540px')
    expect(groupTableCss).toContain('white-space: nowrap')
    expect(groupTableCss).toContain('font-variant-numeric: tabular-nums')
    expect(groupTableCss).toContain('text-overflow: ellipsis')
  })

  it('switches lower dashboard tabs and sorts selectable table rows', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{
            'test-active': [
              { time: '8:00:00 PM', vusers: 8, tps: 20, avgMs: 120, errorPct: 0.2, p50: 100, p90: 140, p99: 180 },
              { time: '8:00:03 PM', vusers: 8, tps: 25, avgMs: 118, errorPct: 0.2, p50: 98, p90: 138, p99: 175 },
            ],
          }}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    expect(getTabByText('Performance Trend').textContent).toContain('2')
    expect(getTabByText('Active Tests').textContent).toContain('1')
    expect(getTabByText('Recent Activity').textContent).toContain('3')
    expect(getTabByText('Release Trend Analysis').textContent).toContain('2')
    expect(getTabByText('Recent Activity').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-panel')).toBeTruthy()

    clickButtonByText('Performance Trend')
    expect(getTabByText('Performance Trend').getAttribute('aria-selected')).toBe('true')
    expect(currentTableRunNames()).toEqual(['Release Smoke', 'Checkout Archive'])

    clickSortButton('Run')
    expect(currentTableRunNames()).toEqual(['Checkout Archive', 'Release Smoke'])
    clickSortButton('Run')
    expect(currentTableRunNames()).toEqual(['Release Smoke', 'Checkout Archive'])
    clickSortButton('Peak TPS')
    expect(currentTableRunNames()).toEqual(['Checkout Archive', 'Release Smoke'])

    clickButtonByText('Checkout Archive')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete-2')
    onSelectTest.mockClear()

    clickButtonByText('Recent Activity')
    expect(getTabByText('Recent Activity').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-panel')).toBeTruthy()
    expect(currentTableRunNames()).toEqual(['Payments Failure Drill', 'Release Smoke', 'Checkout Archive'])

    clickSortButton('Date / Time')
    expect(currentTableRunNames()).toEqual(['Checkout Archive', 'Release Smoke', 'Payments Failure Drill'])
    clickSortButton('Status')
    expect(currentTableRunNames()).toEqual(['Release Smoke', 'Checkout Archive', 'Payments Failure Drill'])
    clickSortButton('VUsers')
    expect(currentTableRunNames()).toEqual(['Payments Failure Drill', 'Checkout Archive', 'Release Smoke'])

    clickButtonByText('Release Smoke')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete')
  })

  it('places load comparison checkboxes alongside performance trend rows', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    clickButtonByText('Performance Trend')
    await flushDashboard()

    expect(apiMock.getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { testIds: ['test-complete', 'test-complete-2'] })
    expect(container.querySelector('[data-testid="load-comparison-panel"] .load-comparison-run-list')).toBeNull()
    expect(container.querySelector('[data-testid="load-comparison-panel"] .load-comparison-checkbox')).toBeNull()
    expect(container.querySelectorAll('.dash-performance-trend tbody .trend-table-checkbox input[type="checkbox"]')).toHaveLength(2)
  })

  it('adds explicit dashboard and archive actions without changing row default navigation', async () => {
    const onOpenTestArchive = vi.fn()
    const onOpenCommandCenter = vi.fn()

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{
            'test-active': [
              { time: '8:00:00 PM', vusers: 8, tps: 20, avgMs: 120, errorPct: 0.2, p50: 100, p90: 140, p99: 180 },
            ],
          }}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onOpenTestArchive={onOpenTestArchive}
          onOpenCommandCenter={onOpenCommandCenter}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    const recentRow = tableRowContaining('Release Smoke')
    expect(recentRow.textContent).toContain('Command Center')

    clickButtonInRow(recentRow, 'Release Smoke')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete')
    onSelectTest.mockClear()

    clickButtonInRow(recentRow, 'Command Center')
    expect(onOpenCommandCenter).toHaveBeenCalledWith('test-complete')
    expect(onSelectTest).not.toHaveBeenCalled()

    clickButtonByText('Performance Trend')
    await flushDashboard()

    const performanceRow = tableRowContaining('Checkout Archive')
    expect(performanceRow.textContent).toContain('Run Dashboard')
    expect(performanceRow.textContent).toContain('Run Archive')

    clickButtonInRow(performanceRow, 'Run Dashboard')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete-2')
    expect(onOpenTestArchive).not.toHaveBeenCalled()
    onSelectTest.mockClear()

    clickButtonInRow(performanceRow, 'Run Archive')
    expect(onOpenTestArchive).toHaveBeenCalledWith('test-complete-2')
    expect(onSelectTest).not.toHaveBeenCalled()

    clickButtonInRow(performanceRow, 'Checkout Archive')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete-2')
  })

  it('renders slim group metrics tables inside active test cards', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'test-active-groups',
          name: 'Checkout Multi',
          status: 'RUNNING',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [
            { name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 12 },
            { name: 'Search', script_id: 'script-2', script_name: 'Search Flow', protocol: 'http', vusers: 8 },
            { name: 'Reporting', script_id: 'script-3', script_name: 'Reporting Flow', protocol: 'http', vusers: 4 },
          ],
          total_vusers: 24,
          duration: '10m',
          created_at: '2026-05-12T20:00:00Z',
          live_metrics: {
            timestamp: '2026-05-12T20:00:03Z',
            active_vusers: 22,
            tps: 44.2,
            avg_response_ms: 128,
            error_pct: 1.2,
            total_transactions: 60,
            total_errors: 1,
            p50_ms: 90,
            p90_ms: 150,
            p95_ms: 180,
            p99_ms: 240,
            groups: {
              Checkout: {
                name: 'Checkout',
                vusers: 12,
                transactions: 20,
                errors: 1,
                tps: 31.7,
                avg_response_ms: 148,
                p50_ms: 100,
                p90_ms: 190,
                p99_ms: 280,
              },
              Search: {
                name: 'Search',
                vusers: 8,
                transactions: 40,
                errors: 0,
                tps: 12.5,
                avg_response_ms: 90,
                p50_ms: 70,
                p90_ms: 120,
                p99_ms: 160,
              },
              Adhoc: {
                name: 'Adhoc',
                vusers: 2,
                transactions: 0,
                errors: 0,
                tps: 0,
                avg_response_ms: 0,
                p50_ms: 0,
                p90_ms: 0,
                p99_ms: 0,
              },
            },
          },
        },
      ],
    })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{
            'test-active-groups': [
              { time: '8:00:00 PM', vusers: 20, tps: 40, avgMs: 130, errorPct: 1, p50: 90, p90: 150, p99: 240 },
              { time: '8:00:03 PM', vusers: 22, tps: 44.2, avgMs: 128, errorPct: 1.2, p50: 90, p90: 150, p99: 240 },
            ],
          }}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()
    clickButtonByText('Active Tests')

    const card = container.querySelector<HTMLElement>('.dash-active-card')
    expect(card?.textContent).toContain('Checkout Multi')
    expect(card?.getAttribute('role')).toBe('button')
    expect(card?.getAttribute('aria-label')).toBe('Open live test Checkout Multi')
    await act(async () => {
      card?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(onSelectTest).toHaveBeenCalledWith('test-active-groups')
    onSelectTest.mockClear()
    await act(async () => {
      card?.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
    })
    expect(onSelectTest).toHaveBeenCalledWith('test-active-groups')
    const table = card?.querySelector<HTMLTableElement>('.dac-group-table')
    expect(table).toBeTruthy()
    expect(table?.getAttribute('aria-label')).toBe('Checkout Multi group metrics')
    expect(Array.from(table!.querySelectorAll('thead th')).map(cell => cell.textContent?.trim())).toEqual([
      'Group',
      'VUsers',
      'TPS',
      'ART',
      'Errors %',
      'Passed',
      'Failed',
    ])
    expect(groupMetricRows(table!)).toEqual([
      ['Checkout', '12', '31.7', '148', '4.76%', '20', '1'],
      ['Search', '8', '12.5', '90', '0.00%', '40', '0'],
      ['Reporting', '4', '—', '—', '—', '—', '—'],
      ['Adhoc', '2', '0.0', '0', '0.00%', '0', '0'],
    ])
  })

  it('renders every dashboard table row instead of preview caps', async () => {
    const completedTests = Array.from({ length: 12 }, (_, index) => {
      const runNumber = index + 1
      const padded = String(runNumber).padStart(2, '0')
      return {
        id: `archived-run-${padded}`,
        name: `Archived Run ${padded}`,
        status: 'COMPLETE',
        application_id: 'app-1',
        release_id: 'rel-1',
        groups: [{ name: 'g1', script_id: `script-${padded}`, script_name: 'Smoke', protocol: 'http', vusers: runNumber }],
        total_vusers: runNumber,
        duration: '5m',
        created_at: `2026-05-12T18:${padded}:00Z`,
        ended_at: `2026-05-12T18:${padded}:30Z`,
        archive: { transaction_count: runNumber * 100 },
      }
    })
    apiMock.listTests.mockResolvedValueOnce({ tests: completedTests })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()
    await flushDashboard()

    expect(getTabByText('Recent Activity').getAttribute('aria-selected')).toBe('true')
    expect(getTabByText('Recent Activity').textContent).toContain('12')
    expect(currentTableRunNames()).toHaveLength(12)

    clickButtonByText('Performance Trend')
    await flushDashboard()

    expect(getTabByText('Performance Trend').textContent).toContain('12')
    expect(apiMock.getTestArchiveReport).toHaveBeenCalledTimes(12)
    expect(currentTableRunNames()).toHaveLength(12)
    expect(container.textContent).not.toContain('View 7 more archived')
    expect(container.textContent).not.toContain('View more archived')
  })

  it('waits for the active application before loading dashboard data', async () => {
    const previousApplication = applicationState.activeApplication
    const previousRelease = applicationState.activeRelease
    applicationState.activeApplication = null as never
    applicationState.activeRelease = null as never

    try {
      await act(async () => {
        root.render(
          <DashboardPage
            testStatuses={{}}
            metricsHistory={{}}
            connectionState="connected"
            onSelectTest={onSelectTest}
            onNavigate={onNavigate}
          />,
        )
      })

      await act(async () => {
        await Promise.resolve()
      })

      expect(apiMock.listTests).not.toHaveBeenCalled()
      expect(apiMock.getHealth).not.toHaveBeenCalled()
      expect(apiMock.getSystemInventory).not.toHaveBeenCalled()

      applicationState.activeApplication = {
        id: 'app-1',
        name: 'Storefront',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T00:00:00Z',
      } as typeof applicationState.activeApplication
      applicationState.activeRelease = {
        id: 'rel-1',
        application_id: 'app-1',
        name: 'Sprint 24',
        created_at: '2026-05-02T00:00:00Z',
        updated_at: '2026-05-02T00:00:00Z',
      } as typeof applicationState.activeRelease

      await act(async () => {
        root.render(
          <DashboardPage
            testStatuses={{}}
            metricsHistory={{}}
            connectionState="connected"
            onSelectTest={onSelectTest}
            onNavigate={onNavigate}
          />,
        )
      })

      await act(async () => {
        await Promise.resolve()
      })

      expect(apiMock.listTests).toHaveBeenCalledTimes(1)
      expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 500, project_id: 'project-1', summary: true })
      expect(apiMock.getHealth).toHaveBeenCalledTimes(1)
      expect(apiMock.getSystemInventory).not.toHaveBeenCalled()
    } finally {
      applicationState.activeApplication = previousApplication
      applicationState.activeRelease = previousRelease
    }
  })

  it('clears scoped test data and ignores a late response after the project changes', async () => {
    const projectOne = deferred<{ tests: Array<ReturnType<typeof scopedTest>> }>()
    apiMock.listTests.mockImplementation(({ project_id }: { project_id: string }) => {
      if (project_id === 'project-1') return projectOne.promise
      return Promise.resolve({ tests: [scopedTest('test-project-2', 'Catalog Run', 'app-2', 'rel-2')] })
    })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 500, project_id: 'project-1', summary: true })

    projectState.activeProject = { id: 'project-2', name: 'Catalog', created_at: '', updated_at: '' }
    applicationState.activeApplication = { id: 'app-2', name: 'Catalog', created_at: '', updated_at: '' }
    applicationState.activeRelease = { id: 'rel-2', application_id: 'app-2', name: 'Catalog 1.0', created_at: '', updated_at: '' }
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    await flushDashboard()

    expect(container.textContent).toContain('Catalog Run')
    expect(container.textContent).not.toContain('Checkout Late Run')

    await act(async () => {
      projectOne.resolve({ tests: [scopedTest('test-project-1', 'Checkout Late Run', 'app-1', 'rel-1')] })
    })
    await flushDashboard()

    expect(container.textContent).toContain('Catalog Run')
    expect(container.textContent).not.toContain('Checkout Late Run')
  })

  it('hides cached runs while a newly selected project scope performs its first load', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    await flushDashboard()
    expect(container.textContent).toContain('Payments Failure Drill')

    const projectTwo = deferred<{ tests: Array<ReturnType<typeof scopedTest>> }>()
    apiMock.listTests.mockReturnValueOnce(projectTwo.promise)
    projectState.activeProject = { id: 'project-2', name: 'Catalog', created_at: '', updated_at: '' }
    applicationState.activeApplication = { id: 'app-2', name: 'Catalog', created_at: '', updated_at: '' }
    applicationState.activeRelease = { id: 'rel-2', application_id: 'app-2', name: 'Catalog 1.0', created_at: '', updated_at: '' }

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    expect(container.textContent).toContain('Loading dashboard runs…')
    expect(container.textContent).not.toContain('Payments Failure Drill')
    expect(container.textContent).not.toContain('No completed tests yet')

    await act(async () => {
      projectTwo.resolve({ tests: [scopedTest('test-project-2', 'Catalog Run', 'app-2', 'rel-2')] })
    })
    await flushDashboard()
    expect(container.textContent).toContain('Catalog Run')
    expect(container.textContent).not.toContain('Payments Failure Drill')
  })

  it('uses the compact empty state when no tests are active', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'test-complete-only',
          name: 'Release Smoke',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-2', script_name: 'Smoke', protocol: 'http', vusers: 1 }],
          total_vusers: 1,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          archive: { transaction_count: 1200 },
        },
      ],
    })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()
    clickButtonByText('Active Tests')

    const activeTestsEmptyState = Array.from(container.querySelectorAll('.dash-empty.compact'))
      .find(element => element.textContent?.includes('No tests currently running'))

    expect(activeTestsEmptyState).toBeDefined()
    expect(activeTestsEmptyState?.textContent).toContain('Start a scenario from the New Test page')
    const startButton = Array.from(activeTestsEmptyState?.querySelectorAll('button') || [])
      .find(button => button.textContent?.trim() === 'Start New Test')
    expect(startButton).toBeDefined()
    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onNavigate).toHaveBeenCalledWith('new-test')
  })

  it('offers the primary next step from the default first-run activity view', async () => {
    apiMock.listTests.mockResolvedValueOnce({ tests: [], count: 0, total: 0 })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()
    expect(container.textContent).toContain('Start a test to build activity, performance, and release history.')
    clickButtonByText('Start New Test')
    expect(onNavigate).toHaveBeenCalledWith('new-test')
  })

  it('shows loading and retry states instead of first-run empties before the list is available', async () => {
    const pending = deferred<{ tests: [] }>()
    apiMock.listTests.mockReturnValueOnce(pending.promise)

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    expect(container.textContent).toContain('Loading dashboard runs…')
    expect(container.textContent).not.toContain('No completed tests yet')

    await act(async () => {
      pending.resolve({ tests: [] })
    })
    await flushDashboard()

    expect(container.textContent).toContain('No completed tests yet')
    expect(container.textContent).not.toContain('Loading dashboard runs…')
  })

  it('surfaces an initial list failure and retries it without presenting an empty account', async () => {
    apiMock.listTests.mockRejectedValueOnce(new Error('controller unavailable'))

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    await flushDashboard()

    expect(container.textContent).toContain('Unable to load dashboard runs. controller unavailable')
    expect(container.textContent).not.toContain('No completed tests yet')

    clickButtonByText('Retry')
    await flushDashboard()

    expect(container.textContent).toContain('Payments Failure Drill')
    expect(container.textContent).not.toContain('Unable to load dashboard runs')
  })

  it('retains cached runs and labels them stale after a refresh failure', async () => {
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    await flushDashboard()
    expect(container.textContent).toContain('Payments Failure Drill')

    apiMock.listTests.mockRejectedValueOnce(new Error('refresh failed'))
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await flushDashboard()

    expect(container.textContent).toContain('Dashboard run data may be stale. refresh failed')
    expect(container.textContent).toContain('Payments Failure Drill')
    expect(container.textContent).not.toContain('No completed tests yet')
  })

  it('chunks project-scoped archive reports and retries a rejected partial batch', async () => {
    expect('project_id' in applicationState.activeRelease).toBe(false)
    const completedTests = Array.from({ length: 51 }, (_, index) => ({
      id: `batch-run-${String(index + 1).padStart(2, '0')}`,
      name: `Batch Run ${String(index + 1).padStart(2, '0')}`,
      status: 'COMPLETE' as const,
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'rel-1',
      groups: [],
      total_vusers: 1,
      duration: '1m',
      created_at: `2026-05-12T18:${String(index).padStart(2, '0')}:00Z`,
      ended_at: `2026-05-12T18:${String(index).padStart(2, '0')}:30Z`,
      archive: { transaction_count: index + 1 },
    }))
    apiMock.listTests.mockResolvedValueOnce({ tests: completedTests })
    let rejectedFinalBatch = false
    apiMock.getTestArchiveReports.mockImplementation(async (testIds: string[]) => {
      if (testIds.length === 1 && !rejectedFinalBatch) {
        rejectedFinalBatch = true
        throw new Error('archive store unavailable')
      }
      return {
        reports: Object.fromEntries(testIds.map(testId => [testId, archiveReport(testId)])),
      }
    })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })
    await flushDashboard()
    await flushDashboard()

    expect(apiMock.getTestArchiveReports).toHaveBeenCalledTimes(2)
    expect(apiMock.getTestArchiveReports.mock.calls.map(call => call[0].length)).toEqual([50, 1])
    expect(apiMock.getTestArchiveReports.mock.calls.every(call => call[1] === 'project-1')).toBe(true)
    expect(apiMock.getTestArchiveReport).not.toHaveBeenCalled()

    clickButtonByText('Performance Trend')
    await flushDashboard()
    expect(getTabByText('Performance Trend').textContent).toContain('50')
    expect(container.textContent).toContain('1 archived performance report could not be loaded.')
    expect(currentTableRunNames()).toHaveLength(50)

    const laterRun = {
      ...completedTests[0],
      id: 'batch-run-52',
      name: 'Batch Run 52',
      created_at: '2026-05-12T19:00:00Z',
      ended_at: '2026-05-12T19:00:30Z',
    }
    apiMock.listTests.mockResolvedValueOnce({ tests: [...completedTests, laterRun] })
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await flushDashboard()
    await flushDashboard()

    expect(apiMock.getTestArchiveReports).toHaveBeenCalledTimes(3)
    expect(apiMock.getTestArchiveReports.mock.calls[2][0]).toEqual(['batch-run-52'])
    expect(container.textContent).toContain('1 archived performance report could not be loaded.')
    expect(currentTableRunNames()).toHaveLength(51)

    clickButtonByText('Retry reports')
    await flushDashboard()
    await flushDashboard()

    expect(apiMock.getTestArchiveReports).toHaveBeenCalledTimes(4)
    expect(apiMock.getTestArchiveReports.mock.calls[3][0]).toHaveLength(1)
    expect(container.textContent).not.toContain('archived performance report could not be loaded')
    expect(getTabByText('Performance Trend').textContent).toContain('52')
    expect(currentTableRunNames()).toHaveLength(52)
  })

  it('keeps terminal tests out of active cards when websocket overrides are stale', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'stale-running-failed',
          name: 'web test',
          status: 'FAILED',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-2', script_name: 'Smoke', protocol: 'http', vusers: 1 }],
          total_vusers: 1,
          duration: '5m',
          created_at: '2026-06-08T00:00:00Z',
          ended_at: '2026-06-08T00:05:00Z',
        },
        {
          id: 'stale-running-complete',
          name: 'web test archive',
          status: 'COMPLETE',
          application_id: 'app-1',
          release_id: 'rel-1',
          groups: [{ name: 'g1', script_id: 'script-3', script_name: 'Archive', protocol: 'http', vusers: 1 }],
          total_vusers: 1,
          duration: '5m',
          created_at: '2026-06-08T00:10:00Z',
          ended_at: '2026-06-08T00:15:00Z',
          archive: { transaction_count: 25 },
        },
      ],
    })

    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{
            'stale-running-failed': 'RUNNING',
            'stale-running-complete': 'INITIALIZING',
          }}
          metricsHistory={{}}
          connectionState="connected"
          onSelectTest={onSelectTest}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()
    clickButtonByText('Active Tests')

    expect(getTabByText('Active Tests').textContent).toContain('0')
    expect(container.querySelector('.dash-active-card')).toBeNull()
    expect(container.textContent).toContain('No tests currently running')

    clickButtonByText('Recent Activity')
    expect(container.textContent).toContain('web test')
    expect(container.textContent).toContain('FAILED')
    expect(container.textContent).toContain('COMPLETE')
  })

  function clickButtonByText(text: string) {
    const button = getButtonByText(text)
    act(() => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function clickSortButton(text: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('th.sortable button'))
      .find(element => element.textContent?.includes(text))
    if (!button) {
      throw new Error(`Missing sort button containing text: ${text}`)
    }
    act(() => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function clickButtonInRow(row: Element, text: string) {
    const button = Array.from(row.querySelectorAll<HTMLButtonElement>('button'))
      .find(element => element.textContent?.trim() === text)
    if (!button) {
      throw new Error(`Missing row button with text: ${text}`)
    }
    act(() => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function tableRowContaining(text: string) {
    const row = Array.from(container.querySelectorAll('tbody tr'))
      .find(element => element.textContent?.includes(text))
    if (!row) {
      throw new Error(`Missing table row containing text: ${text}`)
    }
    return row
  }

  function getTabByText(text: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]'))
      .find(element => element.textContent?.includes(text))
    if (!button) {
      throw new Error(`Missing tab containing text: ${text}`)
    }
    return button
  }

  function currentTableRunNames() {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('tbody .dash-source-link'))
      .map(button => button.textContent?.trim() || '')
  }

  function groupMetricRows(table: HTMLTableElement) {
    return Array.from(table.querySelectorAll('tbody tr')).map(row =>
      Array.from(row.querySelectorAll('td')).map(cell => cell.textContent?.trim() || ''),
    )
  }

  async function flushDashboard() {
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  function getButtonByText(text: string) {
    const button = Array.from(container.querySelectorAll('button')).find(element => element.textContent?.includes(text))
    if (!button) {
      throw new Error(`Missing button containing text: ${text}`)
    }
    return button
  }

  function releasePanelText() {
    const panel = container.querySelector('#dashboard-release-snapshot-panel')
    if (!panel) {
      throw new Error('Missing release snapshot panel')
    }
    return panel.textContent || ''
  }
})

function scopedTest(id: string, name: string, applicationId: string, releaseId: string) {
  return {
    id,
    name,
    status: 'COMPLETE' as const,
    application_id: applicationId,
    release_id: releaseId,
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: '2026-05-12T20:00:00Z',
    ended_at: '2026-05-12T20:01:00Z',
  }
}

function archiveReport(testId: string) {
  return {
    manifest: {
      test_id: testId,
      sla_breached: false,
    },
    overview: {
      peak_tps: 10,
      avg_response_ms: 100,
      error_pct: 0,
      peak_active_vusers: 1,
      transactions: 100,
    },
    completeness: {
      score_pct: 100,
      status: 'complete',
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}
