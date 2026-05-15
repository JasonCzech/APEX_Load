// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DashboardPage from './DashboardPage'

const apiMock = {
  listTests: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getTestArchiveReport: vi.fn(),
}

const applicationState = {
  activeApplication: { id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' },
  activeRelease: { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' },
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationState,
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
          archive: { transaction_count: 1200 },
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
    apiMock.getTestArchiveReport.mockResolvedValue({
      overview: {
        peak_tps: 32,
        avg_response_ms: 95,
        browser: {
          avg_lcp_ms: 240,
        },
        error_pct: 0,
        peak_active_vusers: 12,
        transactions: 1200,
      },
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

    await act(async () => {
      await Promise.resolve()
    })

    expect(getButtonByText('Generators Available').textContent).toContain('1')
  expect(container.textContent).toContain('LCP ms')
  expect(container.textContent).toContain('240')

    clickButtonByText('API Server')
    expect(onNavigate).toHaveBeenCalledWith('monitoring')

    clickButtonByText('Active Runs')
    expect(onNavigate).toHaveBeenCalledWith('tests')

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

    clickButtonByText('Aggregate TPS (active tests)')
    expect(onNavigate).toHaveBeenCalledWith('tests')
  })

  function clickButtonByText(text: string) {
    const button = getButtonByText(text)
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  }

  function getButtonByText(text: string) {
    const button = Array.from(container.querySelectorAll('button')).find(element => element.textContent?.includes(text))
    if (!button) {
      throw new Error(`Missing button containing text: ${text}`)
    }
    return button
  }
})