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
  listScenarios: vi.fn(),
}

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

const applicationState = {
  activeApplication: { id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' },
  activeRelease: { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' },
  releases: [] as Array<{ id: string; application_id: string; name: string; created_at: string; updated_at: string; archived_at?: string }>,
  releasesLoading: false,
  releasesError: '',
  setActiveRelease: vi.fn(),
  refreshReleases: vi.fn(),
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
    window.localStorage.clear()
    resetSharedTestListStoreForTests()
    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }
    applicationState.activeApplication = { id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }
    applicationState.activeRelease = { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }
    applicationState.releasesLoading = false
    applicationState.releasesError = ''
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
    applicationState.refreshReleases = vi.fn().mockResolvedValue(undefined)
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
    apiMock.listScenarios.mockResolvedValue({ scenarios: [], count: 0 })
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
    window.localStorage.clear()
    resetSharedTestListStoreForTests()
    vi.clearAllMocks()
  })

  it('recomputes the recent runs viewport on nested scrolling and accounts for zoom', async () => {
    const callbacks: ResizeObserverCallback[] = []
    const observed: Element[] = []
    const originalObserver = globalThis.ResizeObserver
    globalThis.ResizeObserver = class {
      constructor(callback: ResizeObserverCallback) { callbacks.push(callback) }
      observe(element: Element) { observed.push(element) }
      unobserve() {}
      disconnect() {}
    } as typeof ResizeObserver
    await act(async () => {
      root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />)
    })
    await flushDashboard()
    const table = container.querySelector<HTMLDivElement>('.dash-activity .trend-table-wrap')!
    expect(table).toBeTruthy()
    let top = 400
    let width = 500
    Object.defineProperty(table, 'offsetWidth', { configurable: true, value: 500 })
    vi.spyOn(table, 'getBoundingClientRect').mockImplementation(() => ({ top, width } as DOMRect))
    await act(async () => window.dispatchEvent(new Event('resize')))
    expect(table.style.getPropertyValue('--dashboard-table-max-height')).toBe(`${window.innerHeight - 400 - 24}px`)

    top = 200
    width = 750 // 150% dashboard zoom
    await act(async () => container.dispatchEvent(new Event('scroll')))
    expect(table.style.getPropertyValue('--dashboard-table-max-height')).toBe(`${Math.floor((window.innerHeight - 200) / 1.5 - 24)}px`)

    expect(observed.some(element => element.classList.contains('dash'))).toBe(true)
    top = 300 // Summary expands without changing row count or scrolling.
    await act(async () => callbacks.forEach(callback => callback([], {} as ResizeObserver)))
    expect(table.style.getPropertyValue('--dashboard-table-max-height')).toBe(`${Math.floor((window.innerHeight - 300) / 1.5 - 24)}px`)

    top = window.innerHeight + 100
    await act(async () => container.dispatchEvent(new Event('scroll')))
    expect(table.style.getPropertyValue('--dashboard-table-max-height')).toBe('80px')
    globalThis.ResizeObserver = originalObserver
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

    expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 1000, offset: 0, project_id: 'project-1', summary: true })
    expect(container.textContent).not.toContain('API Guide')
    expect(container.textContent).not.toContain('Customize Cards')

    expect(container.querySelectorAll('.dash-health-card')).toHaveLength(0)
    expect(container.textContent).not.toContain('API Server')
    expect(container.textContent).not.toContain('Active Runs')
    expect(container.querySelector('.dash-release-panel.is-open')).toBeTruthy()
    expect(container.querySelector('button[aria-label="Collapse Release snapshot"]')?.getAttribute('aria-expanded')).toBe('true')
    expect(container.textContent).toContain('Operations Activity')
    expect(container.textContent).toContain('Load generators ready')
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
      'Recent runs3',
      'Active tests1',
      'Load comparison2',
      'Release trend analysis2',
    ])
    expect(getTabByText('Recent runs').getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Payments Failure Drill')

    clickButtonByText('Load comparison')
    await flushDashboard()
    expect(getTabByText('Load comparison').getAttribute('aria-selected')).toBe('true')
    const performancePanelText = container.querySelector('.dash-performance-trend')?.textContent || ''
    expect(performancePanelText).toContain('Successful throughput')
    expect(performancePanelText).toContain('Error rate')
    expect(performancePanelText).toContain('Baseline')
    expect(performancePanelText).toContain('Candidate')
    expect(performancePanelText).toContain('Measured values and changes at matching load')
    expect(performancePanelText).not.toContain('SLA Breaches')
    expect(performancePanelText).not.toContain('Avg Completeness')
    expect(performancePanelText).not.toContain('Max CPU')
    expect(performancePanelText).not.toContain('Max Memory')
    expect(performancePanelText).not.toContain('Telemetry Breaches')
    expect(container.textContent).toContain('Not available')
    expect(container.textContent).toContain('Export this comparison')
    expect(container.textContent).toContain('View Results')
    expect(container.textContent).not.toContain('Test Summary')
    expect(container.textContent).toContain('pixel_8 · Landscape · Touch enabled · Headless')
    expect(container.querySelector('[data-testid="release-trend-panel"]')).toBeNull()

    clickButtonByText('Release trend analysis')
    await flushDashboard()
    expect(getTabByText('Release trend analysis').getAttribute('aria-selected')).toBe('true')
    expect(apiMock.getReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(container.querySelector('[data-testid="release-trend-panel"]')).toBeTruthy()
    expect(container.textContent).toContain('No archived runs yet for this release.')

    clickButtonByText('API Health')
    expect(onNavigate).toHaveBeenCalledWith('monitoring')

    clickButtonByText('Payments Failure Drill failed')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Latest result')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Failed runs')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Current throughput')
    expect(onSelectTest).toHaveBeenCalledWith('test-active')
    onSelectTest.mockClear()

    clickButtonByText('Completed')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Active Now')
    expect(onSelectTest).toHaveBeenCalledWith('test-active')

    clickButtonByText('Release trend analysis')
    clickButtonByText('Export complete release CSV')
    await act(async () => {
      await Promise.resolve()
    })
    expect(apiMock.downloadReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    expect(vi.mocked(downloadBlob).mock.calls[0][1]).toBe('sprint-24-rel-1-trend.csv')

    clickButtonByText('Active tests')
    expect(getTabByText('Active tests').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-panel.is-collapsed')).toBeTruthy()
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')

    clickButtonByText('Aggregate TPS (active tests)')
    expect(onNavigate).toHaveBeenCalledWith('command-center')
  })

  it('loads every bounded application-run page before computing dashboard counts and trends', async () => {
    const newest = scopedTest('test-newest', 'Newest Run', 'app-1', 'rel-1')
    const oldest = { ...scopedTest('test-oldest', 'Oldest Run', 'app-1', 'rel-1'), created_at: '2026-05-01T20:00:00Z' }
    apiMock.listTests.mockImplementation(({ offset = 0 }: { offset?: number }) => Promise.resolve({
      tests: offset === 0 ? [newest] : [oldest],
      count: 1,
      total: 2,
      limit: 1000,
      offset,
    }))

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

    expect(apiMock.listTests).toHaveBeenNthCalledWith(1, { application_id: 'app-1', limit: 1000, offset: 0, project_id: 'project-1', summary: true })
    expect(apiMock.listTests).toHaveBeenNthCalledWith(2, { application_id: 'app-1', limit: 1000, offset: 1, project_id: 'project-1', summary: true })
    expect(getTabByText('Recent runs').textContent).toContain('2')
    expect(container.textContent).toContain('Newest Run')
    expect(container.textContent).toContain('Oldest Run')

    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
      await Promise.resolve()
      await Promise.resolve()
    })
    await flushDashboard()

    expect(apiMock.listTests).toHaveBeenCalledTimes(3)
    expect(apiMock.listTests).toHaveBeenNthCalledWith(3, { application_id: 'app-1', limit: 1000, offset: 0, project_id: 'project-1', summary: true })
  })

  it('blocks the all-releases fallback when release scope fails and offers a retry', async () => {
    applicationState.activeRelease = null as never
    applicationState.releases = []
    applicationState.releasesError = 'Unable to load releases: release service unavailable'

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

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('release service unavailable')
    expect(container.textContent).not.toContain('All releases')
    expect(container.textContent).not.toContain('Checkout Load')
    expect(apiMock.listTests).not.toHaveBeenCalled()

    await act(async () => {
      getButtonByText('Retry release load').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(applicationState.refreshReleases).toHaveBeenCalledWith('app-1')
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

    act(() => { container.querySelector<HTMLButtonElement>('[aria-label="Collapse Release snapshot"]')!.click() })
    expect(panel?.hidden).toBe(true)
    const railButton = getButtonByText('Release snapshot')
    expect(railButton.getAttribute('aria-expanded')).toBe('false')
    expect(container.querySelector('.dash-release-panel.is-collapsed')).toBeTruthy()

    clickButtonByText('Release snapshot')
    expect(panel?.hidden).toBe(false)
    expect(container.querySelector('button[aria-label="Collapse Release snapshot"]')?.getAttribute('aria-expanded')).toBe('true')
  })

  it('surfaces a native release trend export failure', async () => {
    vi.mocked(downloadBlob).mockRejectedValueOnce(new Error('native save failed'))
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
    clickButtonByText('Release trend analysis')
    clickButtonByText('Export complete release CSV')
    await flushDashboard()

    expect(apiMock.downloadReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not export the release trend: native save failed')
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
    expect(panelText).toContain('Operations snapshot')
    expect(panelText).toContain('Operations Activity')
    expect(panelText).toContain('Load generators ready')
    expect(panelText).toContain('2/2')
    expect(panelText).toContain('WebSocket Clients')
    expect(panelText).toContain('Pending Runs')
    expect(panelText).not.toContain('Release Quality')

    clickButtonByText('New run')
    expect(onNavigate).toHaveBeenCalledWith('new-test')
  })

  it('counts queued and reserved runs as pending work', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        { id: 'queued-run', name: 'Queued Checkout', status: 'QUEUED', application_id: 'app-1', release_id: 'rel-1', project_id: 'project-1', created_at: '2026-05-10T10:00:00Z' },
        { id: 'reserved-run', name: 'Reserved Checkout', status: 'RESERVED', application_id: 'app-1', release_id: 'rel-1', project_id: 'project-1', created_at: '2026-05-10T10:01:00Z' },
      ],
      count: 2,
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

    expect(container.textContent).toContain('0 live · 2 queued')
    expect(container.textContent).toContain('Queued Checkout waiting')
    expect(container.textContent).toContain('Reserved Checkout waiting')
  })

  it('dismisses an attention item without opening it and remembers the choice for this release', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        { id: 'queued-run', name: 'Queued Checkout', status: 'QUEUED', application_id: 'app-1', release_id: 'rel-1', project_id: 'project-1', created_at: '2026-05-10T10:00:00Z' },
      ],
      count: 1,
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

    const attentionPanel = container.querySelector('[aria-label="Needs attention"]')
    expect(attentionPanel?.querySelector('h2')?.textContent).toContain('(1)')
    const dismiss = attentionPanel?.querySelector<HTMLButtonElement>('[aria-label="Dismiss Queued Checkout waiting"]')
    expect(dismiss).toBeTruthy()
    act(() => dismiss?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(attentionPanel?.querySelector('h2')?.textContent).toContain('(0)')
    expect(attentionPanel?.textContent).not.toContain('Queued Checkout waiting')
    expect(onSelectTest).not.toHaveBeenCalled()
    expect(onNavigate).not.toHaveBeenCalled()
    expect(JSON.parse(window.localStorage.getItem('apexload.dashboard.dismissedAttention') || '{}')).toEqual({
      'project-1:app-1:rel-1': ['run-pending:queued-run'],
    })
  })

  it('restores attention dismissals saved for the current release', async () => {
    window.localStorage.setItem('apexload.dashboard.dismissedAttention', JSON.stringify({
      'project-1:app-1:rel-1': ['run-pending:queued-run'],
    }))
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        { id: 'queued-run', name: 'Queued Checkout', status: 'QUEUED', application_id: 'app-1', release_id: 'rel-1', project_id: 'project-1', created_at: '2026-05-10T10:00:00Z' },
      ],
      count: 1,
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

    const attentionPanel = container.querySelector('[aria-label="Needs attention"]')
    expect(attentionPanel?.querySelector('h2')?.textContent).toContain('(0)')
    expect(attentionPanel?.textContent).not.toContain('Queued Checkout waiting')
  })

  it('keeps a saved dismissal while its archive-backed attention item is still loading', async () => {
    window.localStorage.setItem('apexload.dashboard.dismissedAttention', JSON.stringify({
      'project-1:app-1:rel-1': ['acceptance-failed:archived-run'],
    }))
    apiMock.listTests.mockResolvedValueOnce({
      tests: [
        {
          id: 'archived-run', name: 'Archived Checkout', status: 'COMPLETE', application_id: 'app-1', release_id: 'rel-1', project_id: 'project-1',
          groups: [], total_vusers: 1, duration: '1m', created_at: '2026-05-10T10:00:00Z', ended_at: '2026-05-10T10:01:00Z', archive: { transaction_count: 1 },
        },
      ],
      count: 1,
    })
    let resolveReports: ((value: { reports: Record<string, unknown> }) => void) | undefined
    apiMock.getTestArchiveReports.mockImplementationOnce(() => new Promise(resolve => { resolveReports = resolve }))

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

    expect(JSON.parse(window.localStorage.getItem('apexload.dashboard.dismissedAttention') || '{}')).toEqual({
      'project-1:app-1:rel-1': ['acceptance-failed:archived-run'],
    })

    await act(async () => {
      resolveReports?.({ reports: { 'archived-run': {
        verdict: { status: 'Fail', failed: true, explanation: 'Regression detected.' },
        overview: { peak_active_vusers: 1, transactions: 1 },
      } } })
      await Promise.resolve()
      await Promise.resolve()
    })

    const attentionPanel = container.querySelector('[aria-label="Needs attention"]')
    expect(attentionPanel?.textContent).not.toContain('Archived Checkout acceptance failed')
    expect(JSON.parse(window.localStorage.getItem('apexload.dashboard.dismissedAttention') || '{}')).toEqual({
      'project-1:app-1:rel-1': ['acceptance-failed:archived-run'],
    })
  })

  it('groups same-named missing archives without losing run identities', async () => {
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

      const archivePendingItems = Array.from(container.querySelectorAll('.dashboard-attention-item'))
        .filter(item => item.textContent?.includes('missing archives'))
      expect(archivePendingItems).toHaveLength(1)
      expect(archivePendingItems[0].textContent).toContain('2 runs missing archives')
      expect(container.textContent).toContain('web-test-a')
      expect(container.textContent).toContain('web-test-b')

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
    expect(panelText).not.toContain('Load generators ready')
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

    expect(getTabByText('Load comparison').textContent).toContain('2')
    expect(getTabByText('Active tests').textContent).toContain('1')
    expect(getTabByText('Recent runs').textContent).toContain('3')
    expect(getTabByText('Release trend analysis').textContent).toContain('2')
    expect(getTabByText('Recent runs').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-panel')).toBeTruthy()

    clickButtonByText('Load comparison')
    expect(getTabByText('Load comparison').getAttribute('aria-selected')).toBe('true')
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

    clickButtonByText('Recent runs')
    expect(getTabByText('Recent runs').getAttribute('aria-selected')).toBe('true')
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

    clickButtonByText('Load comparison')
    await flushDashboard()

    expect(apiMock.getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { population: 'action', testIds: ['test-complete', 'test-complete-2'] })
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
    expect(recentRow.textContent).toContain('View results')

    clickButtonInRow(recentRow, 'Release Smoke')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete')
    onSelectTest.mockClear()

    clickButtonInRow(recentRow, 'View results')
    expect(onOpenTestArchive).toHaveBeenCalledWith('test-complete')
    expect(onSelectTest).not.toHaveBeenCalled()

    clickButtonByText('Load comparison')
    await flushDashboard()

    const performanceRow = tableRowContaining('Checkout Archive')
    expect(performanceRow.textContent).toContain('View results')
    expect(performanceRow.textContent).not.toContain('Run Archive')
    clickButtonInRow(performanceRow, 'View results')
    expect(onOpenTestArchive).toHaveBeenCalledWith('test-complete-2')
    expect(onSelectTest).not.toHaveBeenCalled()
    onOpenTestArchive.mockClear()
    clickButtonInRow(performanceRow, 'Checkout Archive')
    expect(onOpenTestArchive).toHaveBeenCalledWith('test-complete-2')

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
    clickButtonByText('Active tests')

    const card = container.querySelector<HTMLElement>('.dash-active-card')
    expect(card?.textContent).toContain('Checkout Multi')
    expect(card?.getAttribute('role')).not.toBe('button')
    expect(card?.querySelector('[role="button"] button')).toBeNull()
    const openRun = card?.querySelector<HTMLButtonElement>('button[aria-label="Open live test Checkout Multi"]')
    expect(openRun).toBeTruthy()
    const csvButton = card?.querySelector<HTMLButtonElement>('button[aria-label$="data as CSV"]')
    expect(csvButton).toBeTruthy()
    await act(async () => { csvButton?.click() })
    expect(onSelectTest).not.toHaveBeenCalled()
    await act(async () => { openRun?.click() })
    expect(onSelectTest).toHaveBeenCalledWith('test-active-groups')
    const table = card?.querySelector<HTMLTableElement>('.dac-group-table')
    expect(table).toBeTruthy()
    expect(table?.getAttribute('aria-label')).toBe('Checkout Multi group metrics')
    expect(Array.from(table!.querySelectorAll('thead th')).map(cell => cell.textContent?.trim())).toEqual([
      'Group',
      'VUsers',
      'TPS',
      'ART (ms)',
      'Error rate (%)',
      'Passed',
      'Failed',
    ])
    expect(groupMetricRows(table!)).toEqual([
      ['Checkout', '12', '31.7', '148', '4.76%', '20', '1'],
      ['Search', '8', '12.5', '90', '0.00%', '40', '0'],
      ['Reporting', '4', '—', '—', '—', '—', '—'],
      // No attempts yet: no error rate, rather than a 0.00% it has not earned.
      ['Adhoc', '2', '0.0', '0', '—', '0', '0'],
    ])
    expect(table?.querySelector('caption')?.textContent).toContain('TPS from the last sample; transaction error rates and counts from the last sample')
  })

  // A script without transactions still sends requests: the card counts
  // them, as Command Center and the report do, and names the window.
  it('counts requests in active test cards once the run has sent any', async () => {
    apiMock.listTests.mockResolvedValueOnce({
      tests: [{
        id: 'test-requests', name: 'Inventory resilience', status: 'RUNNING', application_id: 'app-1', release_id: 'rel-1',
        groups: [{ name: 'Inventory', script_id: 'script-1', script_name: 'Inventory Flow', protocol: 'http', vusers: 4 }],
        total_vusers: 4, duration: '10m', created_at: '2026-05-12T20:00:00Z',
        live_metrics: {
          timestamp: '2026-05-12T20:00:03Z', active_vusers: 4, tps: 0, avg_response_ms: 128, error_pct: 100,
          total_transactions: 0, total_errors: 3, run_total_transactions: 0, run_total_errors: 387,
          requests: 30, request_errors: 15, request_rate: 30, run_total_requests: 1_729, run_request_errors: 863,
          p50_ms: 90, p90_ms: 150, p95_ms: 180, p99_ms: 240,
          groups: {
            Inventory: { name: 'Inventory', vusers: 4, transactions: 0, errors: 3, tps: 0, run_transactions: 0, run_errors: 387, requests: 30, request_errors: 15, request_rate: 30, run_requests: 1_729, run_request_errors: 863, avg_response_ms: 128, p50_ms: 90, p90_ms: 150, p99_ms: 240 },
            // Reported without being configured: averaged like the rest.
            Extra: { name: 'Extra', vusers: 1, transactions: 0, errors: 0, tps: 0, requests: 14, request_rate: 14, run_requests: 100, run_request_errors: 0, avg_response_ms: 90, p50_ms: 90, p90_ms: 90, p99_ms: 90 },
          },
        },
      }],
    })
    await act(async () => {
      root.render(
        <DashboardPage
          testStatuses={{}}
          metricsHistory={{
            'test-requests': [
              { time: 'a', timestampMs: 1_000, vusers: 4, tps: 0, requestRate: 36, Inventory_tps: 0, Inventory_requestRate: 26, Extra_tps: 0, Extra_requestRate: 10, avgMs: 128, errorPct: 100, p50: 90, p90: 150, p99: 240 },
              { time: 'b', timestampMs: 2_000, vusers: 4, tps: 0, requestRate: 44, Inventory_tps: 0, Inventory_requestRate: 30, Extra_tps: 0, Extra_requestRate: 14, avgMs: 128, errorPct: 100, p50: 90, p90: 150, p99: 240 },
            ],
          }}
          connectionState="connected"
          onSelectTest={vi.fn()}
          onNavigate={vi.fn()}
        />,
      )
    })
    await flushDashboard()
    clickButtonByText('Active tests')

    const card = container.querySelector<HTMLElement>('.dash-active-card')!
    const metric = (label: string) => Array.from(card.querySelectorAll('.dac-metric')).find(item => item.querySelector('.dac-metric-label')?.textContent === label)?.querySelector('.dac-metric-value')?.textContent?.trim()
    expect(metric('Requests/s (10 s avg)')).toBe('40.0')
    expect(metric('Request error rate (run to date)')).toBe('49.91%')
    expect(metric('ART ms (10 s avg)')).toBe('128')
    const table = card.querySelector<HTMLTableElement>('.dac-group-table')!
    expect(Array.from(table.querySelectorAll('thead th')).map(cell => cell.textContent?.trim())).toEqual(['Group', 'VUsers', 'Requests/s', 'ART (ms)', 'Error rate (%)', 'Requests', 'Failed'])
    expect(groupMetricRows(table)).toEqual([['Inventory', '4', '28.0', '128', '49.91%', '1,729', '863'], ['Extra', '1', '12.0', '90', '0.00%', '100', '0']])
    expect(table.querySelector('caption')?.textContent).toContain('Requests/s averaged over the last 10 s; request error rates and counts run to date')
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

    expect(getTabByText('Recent runs').getAttribute('aria-selected')).toBe('true')
    expect(getTabByText('Recent runs').textContent).toContain('12')
    expect(currentTableRunNames()).toHaveLength(12)

    clickButtonByText('Load comparison')
    await flushDashboard()

    expect(getTabByText('Load comparison').textContent).toContain('12')
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
      expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 1000, offset: 0, project_id: 'project-1', summary: true })
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
    expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', limit: 1000, offset: 0, project_id: 'project-1', summary: true })

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
    clickButtonByText('Active tests')

    const activeTestsEmptyState = Array.from(container.querySelectorAll('.dash-empty.compact'))
      .find(element => element.textContent?.includes('No tests currently running'))

    expect(activeTestsEmptyState).toBeDefined()
    expect(activeTestsEmptyState?.textContent).toContain('Start a scenario from the New run page')
    const startButton = Array.from(activeTestsEmptyState?.querySelectorAll('button') || [])
      .find(button => button.textContent?.trim() === 'Start a new run')
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
    clickButtonByText('Start a new run')
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

    clickButtonByText('Load comparison')
    await flushDashboard()
    expect(getTabByText('Load comparison').textContent).toContain('50')
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
    expect(getTabByText('Load comparison').textContent).toContain('52')
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
    clickButtonByText('Active tests')

    expect(getTabByText('Active tests').textContent).toContain('0')
    expect(container.querySelector('.dash-active-card')).toBeNull()
    expect(container.textContent).toContain('No tests currently running')

    clickButtonByText('Recent runs')
    expect(container.textContent).toContain('web test')
    expect(container.textContent).toContain('FAILED')
    expect(container.textContent).toContain('COMPLETE')
  })

  it('sums each active run in its own unit and counts requests once a run sends them', async () => {
    const live = (overrides: Record<string, unknown>) => ({ timestamp: '2026-05-12T20:00:30Z', active_vusers: 4, tps: 0, total_transactions: 0, total_errors: 0, error_pct: 0, avg_response_ms: 100, p50_ms: 90, p90_ms: 120, p95_ms: 130, p99_ms: 150, ...overrides })
    apiMock.listTests.mockResolvedValue({ tests: [
      // A script without transactions: requests flow, no transaction passes.
      { ...scopedTest('requests', 'Requests only', 'app-1', 'rel-1'), status: 'RUNNING', ended_at: undefined, live_metrics: live({ request_rate: 30, run_total_requests: 400, run_request_errors: 100, total_errors: 3 }) },
      { ...scopedTest('goal', 'Goal run', 'app-1', 'rel-1'), status: 'RUNNING', ended_at: undefined, scenario_mode: 'goal_oriented', live_metrics: live({ tps: 8, request_rate: 16, run_total_requests: 400, run_request_errors: 0 }) },
    ] })
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />) })
    await flushDashboard()
    expect(getButtonByText('Current throughput').textContent).toContain('30.0 req/s · 8.0 TPS')
    // 25% and 0%, each run to date in requests, averaged.
    expect(getButtonByText('Live error rate (active runs)').textContent).toContain('12.50%')
  })

  it('keeps paused runs active and does not report missing live metrics as zero', async () => {
    apiMock.listTests.mockResolvedValue({ tests: [{ ...scopedTest('paused', 'Paused checkout', 'app-1', 'rel-1'), status: 'PAUSED', ended_at: undefined }] })
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />) })
    await flushDashboard()
    expect(container.textContent).toContain('1 active (running or paused)')
    expect(getButtonByText('Current throughput').textContent).toContain('—')
    expect(getButtonByText('Live error rate (active runs)').textContent).toContain('—')
    await act(async () => { getTabByText('Active tests').click() })
    expect(container.textContent).toContain('Paused checkout')
  })

  it('opens the queue from the summary and routes the selected queued run to Command Center', async () => {
    const onOpenCommandCenter = vi.fn()
    apiMock.listTests.mockResolvedValue({ tests: [
      { ...scopedTest('queued-1', 'Queued Checkout', 'app-1', 'rel-1'), status: 'PENDING', queue_position: 1, pending_reason: 'Project VUser limit' },
      { ...scopedTest('queued-2', 'Reserved Checkout', 'app-1', 'rel-1'), status: 'RESERVED', queue_position: 2 },
    ] })
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onOpenCommandCenter={onOpenCommandCenter} onNavigate={onNavigate} />) })
    await flushDashboard()
    expect(getTabByText('Active & queued').textContent).toContain('2')
    clickButtonByText('View queued tests (2)')
    expect(container.querySelector('[aria-label="Queued runs"]')?.textContent).toContain('Project VUser limit')
    expect(container.querySelector('[aria-label="Queued runs"]')?.textContent).toContain('queued-2')
    clickButtonByText('View queued tests')
    // The first matching button is the overview shortcut; use the queue's CTA explicitly.
    const queueButton = container.querySelector<HTMLButtonElement>('.dashboard-queue .dashboard-summary-heading button')!
    await act(async () => { queueButton.click() })
    expect(onOpenCommandCenter).toHaveBeenCalledWith('queued-1')
    expect(onNavigate).not.toHaveBeenCalledWith('tests')
  })

  it('scopes history, live counts and readiness to the selected release', async () => {
    apiMock.listTests.mockResolvedValue({ tests: [scopedTest('one', 'First release', 'app-1', 'rel-1'), { ...scopedTest('two', 'Second release', 'app-1', 'rel-2'), status: 'RUNNING' }] })
    const render = () => root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />)
    await act(async () => { render() })
    await flushDashboard()
    expect(container.textContent).toContain('First release')
    expect(container.textContent).not.toContain('Second release')
    expect(getTabByText('Active tests').textContent).toContain('0')
    applicationState.activeRelease = { ...applicationState.activeRelease, id: 'rel-2', name: 'Sprint 23' }
    await act(async () => { render() })
    expect(container.textContent).not.toContain('First release')
    expect(getTabByText('Active tests').textContent).toContain('1')
    expect(container.querySelector('[aria-label="Release readiness and attention"]')?.textContent).toContain('0 of 0')
  })

  it('marks failed health refreshes stale and clears the warning when a later check succeeds', async () => {
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />) })
    await flushDashboard()
    apiMock.getHealth.mockRejectedValueOnce(new Error('offline'))
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    await flushDashboard()
    expect(container.textContent).toContain('System health is stale')
    expect(getButtonByText('API Health').textContent).toContain('Stale')
    expect(getButtonByText('Load generators ready').textContent).toContain('—')
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    await flushDashboard()
    expect(container.textContent).not.toContain('System health is stale')
    expect(getButtonByText('API Health').textContent).toContain('Healthy')
  })

  it('distinguishes a loading report from unavailable comparison evidence', async () => {
    const response = deferred<{ reports: Record<string, null> }>()
    apiMock.listTests.mockResolvedValue({ tests: [{ ...scopedTest('saved', 'Saved run', 'app-1', 'rel-1'), archive: { transaction_count: 12 } }] })
    apiMock.getTestArchiveReports.mockReturnValue(response.promise)
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onNavigate={onNavigate} />) })
    await flushDashboard()
    expect(container.textContent).toContain('Loading latest report completeness and comparison baseline')
    await act(async () => { response.resolve({ reports: { saved: null } }) })
    await flushDashboard()
    expect(container.textContent).toContain('Comparison baseline unavailable without a loaded report.')
    expect(container.textContent).not.toContain('No recorded comparison baseline for the latest run.')
  })

  it('filters grouped evidence gaps and preserves accessible run details for unavailable archives', async () => {
    apiMock.listTests.mockResolvedValue({ tests: [scopedTest('missing', 'Missing run', 'app-1', 'rel-1'), { ...scopedTest('saved', 'Saved run', 'app-1', 'rel-1'), archive: { transaction_count: 12 } }] })
    await act(async () => { root.render(<DashboardPage testStatuses={{}} metricsHistory={{}} connectionState="connected" onSelectTest={onSelectTest} onOpenTestArchive={vi.fn()} onOpenCommandCenter={vi.fn()} onNavigate={onNavigate} />) })
    await flushDashboard()
    clickButtonByText('1 run missing archives')
    expect(currentTableRunNames()).toEqual(['Missing run'])
    expect(container.querySelector('.dashboard-run-details')?.textContent).toContain('publication progress unknown')
    expect(container.querySelector('button[aria-label="View results for Missing run (missing)"]')).toBeTruthy()
    expect(container.querySelector('[aria-label="Release readiness and attention"]')?.textContent).toContain('1 of 2')
    clickButtonByText('Clear filters')
    expect(currentTableRunNames()).toHaveLength(2)
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
