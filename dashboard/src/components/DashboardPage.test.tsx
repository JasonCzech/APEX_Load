// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DashboardPage from './DashboardPage'
import { downloadBlob } from '../utils/fileUtils'

const apiMock = {
  listTests: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  downloadReleaseTrend: vi.fn(),
  getReleaseTrend: vi.fn(),
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

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationState,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' },
  }),
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
    apiMock.downloadReleaseTrend.mockResolvedValue(new Blob(['release trend csv'], { type: 'text/csv' }))
    apiMock.getReleaseTrend.mockResolvedValue({
      release_id: 'rel-1',
      run_count: 0,
      points: [],
      summary: { latest_regressed: false, avg_response_trend_pct: 0, error_pct_trend: 0 },
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

    expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', project_id: 'project-1' })
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
    expect(getTabByText('Performance Trend').getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('SLA Breaches')
    expect(container.textContent).toContain('Avg Completeness')
    expect(container.textContent).toContain('88%')
    expect(container.textContent).toContain('Max CPU')
    expect(container.textContent).toContain('64.2%')
    expect(container.textContent).toContain('Max Memory')
    expect(container.textContent).toContain('512 MB')
    expect(container.textContent).toContain('Telemetry Breaches')
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

  it('adds Run Dashboard actions without changing dashboard archive navigation', async () => {
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
          onOpenCommandCenter={onOpenCommandCenter}
          onNavigate={onNavigate}
        />,
      )
    })

    await flushDashboard()

    const recentRow = tableRowContaining('Release Smoke')
    expect(recentRow.textContent).toContain('Run Dashboard')

    clickButtonInRow(recentRow, 'Release Smoke')
    expect(onSelectTest).toHaveBeenCalledWith('test-complete')
    onSelectTest.mockClear()

    clickButtonInRow(recentRow, 'Run Dashboard')
    expect(onOpenCommandCenter).toHaveBeenCalledWith('test-complete')
    expect(onSelectTest).not.toHaveBeenCalled()

    clickButtonByText('Performance Trend')
    await flushDashboard()

    const performanceRow = tableRowContaining('Checkout Archive')
    expect(performanceRow.textContent).toContain('Run Dashboard')

    clickButtonInRow(performanceRow, 'Run Dashboard')
    expect(onOpenCommandCenter).toHaveBeenCalledWith('test-complete-2')
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
      expect(apiMock.listTests).toHaveBeenCalledWith({ application_id: 'app-1', project_id: 'project-1' })
      expect(apiMock.getHealth).toHaveBeenCalledTimes(1)
      expect(apiMock.getSystemInventory).not.toHaveBeenCalled()
    } finally {
      applicationState.activeApplication = previousApplication
      applicationState.activeRelease = previousRelease
    }
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
