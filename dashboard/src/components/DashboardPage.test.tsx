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

  it('wires summary and snapshot cards to navigation targets', async () => {
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

    const healthCards = Array.from(container.querySelectorAll('.dash-health-card'))
    expect(healthCards).toHaveLength(4)
    expect(healthCards.every(card => card.className.includes('dash-card-button'))).toBe(true)
    expect(healthCards.map(card => card.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Attention Needed'),
    ]))
    expect(container.querySelector('.dash-release-snapshot')).toBeTruthy()
    expect(container.querySelector('.dash-attention')).toBeNull()
    expect(container.querySelectorAll('.release-metric')).toHaveLength(6)
    const releaseOptions = Array.from(container.querySelectorAll<HTMLButtonElement>('.dash-release-option'))
    expect(releaseOptions.map(button => button.textContent?.trim())).toEqual(['Sprint 24', 'Sprint 23', 'Sprint 22', 'Sprint 21', 'Sprint 20', 'Sprint 19'])
    expect(releaseOptions.find(button => button.textContent?.trim() === 'Sprint 24')?.className).toContain('active')
    expect(container.textContent).not.toContain('Archived Sprint')
    clickButtonByText('Sprint 23')
    expect(applicationState.setActiveRelease).toHaveBeenCalledWith(expect.objectContaining({ id: 'rel-2', name: 'Sprint 23' }))
    expect(getButtonByText('Attention Needed').textContent).toContain('1')
    const dashboardTabs = Array.from(container.querySelectorAll<HTMLButtonElement>('.dash-tab'))
    expect(dashboardTabs.map(tab => tab.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Performance Trend'),
      expect.stringContaining('Active Tests'),
      expect.stringContaining('Recent Activity'),
    ]))
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

    clickButtonByText('API Guide')
    expect(onNavigate).toHaveBeenCalledWith('docs')

    clickButtonByText('API Server')
    expect(onNavigate).toHaveBeenCalledWith('monitoring')

    clickButtonByText('Active Runs')
    expect(onNavigate).toHaveBeenCalledWith('tests')

    clickButtonByText('Attention Needed')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Latest Result')
    expect(onSelectTest).toHaveBeenCalledWith('test-failed')

    clickButtonByText('Failed Runs')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Current TPS')
    expect(onSelectTest).toHaveBeenCalledWith('test-active')

    clickButtonByText('Completed')
    expect(onNavigate).toHaveBeenCalledWith('results')

    clickButtonByText('Active Now')
    expect(onNavigate).toHaveBeenCalledWith('tests')

    clickButtonByText('Export CSV')
    await act(async () => {
      await Promise.resolve()
    })
    expect(apiMock.downloadReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    expect(vi.mocked(downloadBlob).mock.calls[0][1]).toBe('sprint-24-rel-1-trend.csv')

    clickButtonByText('Active Tests')
    expect(getTabByText('Active Tests').getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.dash-release-snapshot')).toBeTruthy()
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')

    clickButtonByText('Aggregate TPS (active tests)')
    expect(onNavigate).toHaveBeenCalledWith('tests')
  })

  it('uses theme-aware accent fills for the first two dashboard rows', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const topRowsCss = [
      stylesheet.match(/\.dash-health-card \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-health-card\.status-idle \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-release-snapshot \.dash-section-heading-row \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-release-option\.active \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-tab\.active \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(topRowsCss).toContain('color-mix(in srgb, var(--accent)')
    expect(topRowsCss).toContain('var(--accent-border)')
    expect(topRowsCss).not.toContain('rgba(14, 165, 233')
    expect(topRowsCss).not.toContain('rgba(125, 211, 252')
  })

  it('keeps the first two dashboard rows proportionate to their summary content', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/DashboardPage.css`, 'utf8') as string
    const topRowsCss = [
      stylesheet.match(/\.dash-health-card \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.dash-release-snapshot \.dash-section-heading-row \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.release-metric \{[\s\S]*?\n\}/)?.[0] ?? '',
      stylesheet.match(/\.release-status-strip \{[\s\S]*?\n\}/)?.[0] ?? '',
    ].join('\n')

    expect(topRowsCss).toContain('min-height: 84px')
    expect(topRowsCss).toContain('padding: 12px 14px 12px 16px')
    expect(topRowsCss).toContain('padding: 12px 16px')
    expect(topRowsCss).toContain('min-height: 68px')
    expect(topRowsCss).toContain('padding: 10px 12px')
    expect(topRowsCss).toContain('padding: 10px 16px 14px')
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
    expect(container.querySelector('.dash-release-snapshot')).toBeTruthy()
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
    expect(container.querySelector('.dash-release-snapshot')).toBeTruthy()
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
})
