// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MetricsHistoryPoint } from '../types'
import TestExecutionCharts, { type GraphGroupTabId } from './TestExecutionCharts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function parsePathYValues(d: string | null): number[] {
  if (!d) return []
  const values: number[] = []
  const pairRegex = /[MLC]\s*(-?\d+(?:\.\d+)?)[,\s]+(-?\d+(?:\.\d+)?)/g
  let match = pairRegex.exec(d)
  while (match) {
    const y = Number(match[2])
    if (Number.isFinite(y)) values.push(y)
    match = pairRegex.exec(d)
  }
  return values
}

describe('TestExecutionCharts real Recharts overlay rendering', () => {
  let container: HTMLDivElement
  let root: Root
  const originalResizeObserver = globalThis.ResizeObserver
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
  const originalOffsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  const originalGetBoundingClientRect = HTMLElement.prototype.getBoundingClientRect

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      get() {
        return 520
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get(this: HTMLElement) {
        if (this.classList?.contains('recharts-legend-wrapper')) return 20
        return 170
      },
    })
    HTMLElement.prototype.getBoundingClientRect = function getBoundingClientRect(this: HTMLElement) {
      const height = this.classList?.contains('recharts-legend-wrapper') ? 20 : 170
      return {
        x: 0,
        y: 0,
        top: 0,
        left: 0,
        right: 520,
        bottom: height,
        width: 520,
        height,
        toJSON: () => ({}),
      } as DOMRect
    }

    globalThis.ResizeObserver = class MockResizeObserver {
      private readonly callback: ResizeObserverCallback

      constructor(callback: ResizeObserverCallback) {
        this.callback = callback
      }

      observe(target: Element) {
        this.callback(
          [
            {
              target,
              contentRect: {
                x: 0,
                y: 0,
                top: 0,
                left: 0,
                right: 520,
                bottom: 170,
                width: 520,
                height: 170,
                toJSON: () => ({}),
              } as DOMRectReadOnly,
            } as ResizeObserverEntry,
          ],
          this as unknown as ResizeObserver,
        )
      }

      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth)
    }
    if (originalOffsetHeight) {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', originalOffsetHeight)
    }
    HTMLElement.prototype.getBoundingClientRect = originalGetBoundingClientRect
    globalThis.ResizeObserver = originalResizeObserver
  })

  const samples: MetricsHistoryPoint[] = [
    {
      time: '10:00:00',
      timestampMs: 0,
      vusers: 4000,
      tps: 12,
      targetTps: 18,
      Browse_tps: 12,
      avgMs: 95,
      Browse_avgMs: 95,
      errorPct: 0.4,
      Browse_errorPct: 0.4,
      p50: 70,
      Browse_p50: 70,
      p90: 90,
      Browse_p90: 90,
      p95: 110,
      Browse_p95: 110,
      p99: 140,
      Browse_p99: 140,
      cpuProcess: 35,
      cpuFree: 55,
      cpuProcessMax: 48,
      memProcess: 512,
      memFree: 1536,
      netSent: 120,
      netRecv: 240,
      diskRead: 15,
      diskWrite: 22,
      txDnsMs: 2,
      txConnectMs: 4,
      txTlsMs: 6,
      txTtfbMs: 12,
      txWaitMs: 8,
      errTimeout: 1,
      errConnect: 0,
      err5xx: 1,
      err4xx: 0,
      errStatus: 0,
      errAssert: 0,
      containerCPUUsagePct: 38,
      containerCPUThrottledSecondsRate: 0.2,
      containerMemoryUsagePct: 44,
      gcPauseMs: 3.5,
      netDropsPerSec: 0,
      netErrorsPerSec: 0,
      tcpRetransmitsPerSec: 1,
      diskIOTimePct: 8,
      latency_api_443: 4.5,
    },
    {
      time: '10:00:01',
      timestampMs: 1_000,
      vusers: 5000,
      tps: 16,
      targetTps: 18,
      Browse_tps: 16,
      avgMs: 115,
      Browse_avgMs: 115,
      errorPct: 0.6,
      Browse_errorPct: 0.6,
      p50: 80,
      Browse_p50: 80,
      p90: 105,
      Browse_p90: 105,
      p95: 130,
      Browse_p95: 130,
      p99: 165,
      Browse_p99: 165,
      cpuProcess: 42,
      cpuFree: 48,
      cpuProcessMax: 56,
      memProcess: 540,
      memFree: 1508,
      netSent: 150,
      netRecv: 300,
      diskRead: 18,
      diskWrite: 28,
      txDnsMs: 3,
      txConnectMs: 5,
      txTlsMs: 7,
      txTtfbMs: 14,
      txWaitMs: 9,
      errTimeout: 2,
      errConnect: 1,
      err5xx: 1,
      err4xx: 0,
      errStatus: 0,
      errAssert: 0,
      containerCPUUsagePct: 45,
      containerCPUThrottledSecondsRate: 0.3,
      containerMemoryUsagePct: 47,
      gcPauseMs: 4.2,
      netDropsPerSec: 0,
      netErrorsPerSec: 0,
      tcpRetransmitsPerSec: 2,
      diskIOTimePct: 10,
      latency_api_443: 6.2,
    },
  ]

  async function renderTab(activeGraphGroupTab: GraphGroupTabId) {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={samples}
          loadImpactData={samples}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup
          groupNames={['Browse']}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
          surface="command-center"
          storageScope="apexload.commandCenter.grafana"
          defaultDensity="command_center"
          enableGraphGroupTabs
          activeGraphGroupTab={activeGraphGroupTab}
        />,
      )
    })
  }

  function curvePaths(card: HTMLElement) {
    return Array.from(card.querySelectorAll<SVGPathElement>('path.recharts-line-curve, path.recharts-area-curve'))
      .filter(path => Boolean(path.getAttribute('d')))
  }

  async function triggerContextAction(card: HTMLElement, label: string) {
    await act(async () => {
      card.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 160, clientY: 120 }))
    })
    const action = Array.from(container.querySelectorAll<HTMLButtonElement>('.chart-context-menu button'))
      .find(button => button.textContent === label)
    expect(action, `context action "${label}" on ${card.getAttribute('data-chart-id')}`).toBeDefined()
    await act(async () => {
      action!.click()
    })
  }

  it('adds exactly one SVG overlay curve without removing or crushing primary curves across all Command Center live charts', async () => {
    // Seed stale filters from an earlier run/group selection to exercise the exact Command Center failure mode.
    window.localStorage.setItem(
      'apexload.commandCenter.grafana.chartMetricFilters',
      JSON.stringify({
        tps: ['Checkout_tps'],
        latency: ['Checkout_avgMs'],
        errors: ['vusers'],
        network_latency: ['latency_legacy_443'],
        load_impact_p95: ['Checkout__loadImpactAverage'],
      }),
    )

    await renderTab('test_results')
    const vusersCard = container.querySelector<HTMLElement>('[data-chart-id="vusers"]')!
    expect(vusersCard).not.toBeNull()
    const vusersBefore = curvePaths(vusersCard)
    const vusersYBefore = parsePathYValues(vusersBefore[0].getAttribute('d'))
    await triggerContextAction(vusersCard, 'Overlay ART')
    const vusersAfter = curvePaths(vusersCard)
    expect(vusersAfter.length).toBe(vusersBefore.length + 1)
    expect(parsePathYValues(vusersAfter[0].getAttribute('d'))).toEqual(vusersYBefore)

    for (const chartId of ['tps', 'latency', 'latency_p50', 'latency_p90', 'latency_p95', 'latency_p99', 'errors', 'error_taxonomy']) {
      const card = container.querySelector<HTMLElement>(`[data-chart-id="${chartId}"]`)!
      expect(card, chartId).not.toBeNull()
      const before = curvePaths(card)
      expect(before.length, `${chartId} primary curves before overlay`).toBeGreaterThan(0)
      const primaryYBefore = parsePathYValues(before[0].getAttribute('d'))

      await triggerContextAction(card, 'Overlay VUsers')

      const after = curvePaths(card)
      expect(after.length, `${chartId} curves after Overlay VUsers`).toBe(before.length + 1)
      const primaryYAfter = parsePathYValues(after[0].getAttribute('d'))
      // Even with vusers=5000 vs tps=16 or errorPct=0.6, the primary curve uses its own left Y-axis
      // so its vertical coordinates remain untouched instead of collapsing to the chart floor.
      expect(primaryYAfter).toEqual(primaryYBefore)
    }

    await renderTab('infrastructure')
    for (const chartId of ['cpu', 'mem', 'io', 'tx_network_phases', 'generator_impact', 'network_latency']) {
      const card = container.querySelector<HTMLElement>(`[data-chart-id="${chartId}"]`)!
      expect(card, chartId).not.toBeNull()
      const before = curvePaths(card)
      expect(before.length, `${chartId} primary curves before overlay`).toBeGreaterThan(0)
      const primaryYBefore = parsePathYValues(before[0].getAttribute('d'))

      await triggerContextAction(card, 'Overlay VUsers')

      const after = curvePaths(card)
      expect(after.length, `${chartId} curves after Overlay VUsers`).toBe(before.length + 1)
      const primaryYAfter = parsePathYValues(after[0].getAttribute('d'))
      expect(primaryYAfter).toEqual(primaryYBefore)
    }

    await renderTab('under_load')
    const loadImpactCard = container.querySelector<HTMLElement>('[data-chart-id="load_impact_p95"]')!
    expect(loadImpactCard).not.toBeNull()
    const loadImpactBefore = curvePaths(loadImpactCard)
    expect(loadImpactBefore.length).toBeGreaterThan(0)
    await triggerContextAction(loadImpactCard, 'Overlay ART')
    const loadImpactAfter = curvePaths(loadImpactCard)
    expect(loadImpactAfter.length).toBe(loadImpactBefore.length + 1)
  })
})
