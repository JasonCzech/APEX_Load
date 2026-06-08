// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CommandCenterPrototype from './CommandCenterPrototype'
import type { MetricsHistoryPoint, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CommandCenterPrototype', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.restoreAllMocks()
    container.remove()
  })

  it('does not show the prototype run when no live run is selected', async () => {
    await act(async () => {
      root.render(<CommandCenterPrototype />)
    })

    expect(container.textContent).toContain('No active run selected')
    expect(container.textContent).not.toContain('Checkout Peak Soak - Command Center Prototype')
    expect(container.querySelector('[aria-label="Command center run controls"]')).toBeNull()
    expect(container.querySelector('[aria-label="No active command center run"]')).not.toBeNull()
  })

  it('switches between the OPS View and Graph Wall variants from the sidebar', async () => {
    await renderPrototype()

    expect(container.textContent).toContain('OPS View')
    expect(container.textContent).toContain('Graph Wall')
    expect(container.querySelector('[aria-label="Ops cockpit mockup"]')).not.toBeNull()
    expect(container.textContent).not.toContain('Triage Console')
    expect(container.querySelector('.command-center-intro')).toBeNull()

    const opsSidebar = container.querySelector('[aria-label="Command center sidebar"]')
    const opsPrimaryColumn = container.querySelector('.command-layout-ops > .command-primary-column')
    const opsSwitchPanel = opsSidebar?.querySelector('.command-view-switch-panel')
    const opsCentralGroupPanel = opsPrimaryColumn?.querySelector('.command-group-panel')
    const opsLoadModelRow = opsPrimaryColumn?.querySelector('.command-load-model-row')
    const opsLoadModelPanel = opsLoadModelRow?.querySelector('[aria-label="Load model profile"]')

    expect(opsSwitchPanel).not.toBeNull()
    expect(opsSidebar?.querySelector('.command-group-panel')).toBeNull()
    expect(opsPrimaryColumn).not.toBeNull()
    expect(opsCentralGroupPanel).not.toBeNull()
    expect(opsCentralGroupPanel?.classList.contains('paired')).toBe(true)
    expect(opsLoadModelRow).not.toBeNull()
    expect(opsLoadModelPanel).not.toBeNull()
    expect(opsPrimaryColumn?.querySelector('[aria-label="Command center run tables"]')).not.toBeNull()
    expect(opsPrimaryColumn?.querySelector('.command-runtime-graphs')).toBeNull()
    expect(opsPrimaryColumn?.textContent).toContain('Scenario Run')
    expect(opsPrimaryColumn?.textContent).toContain('Error Details')
    expect(opsPrimaryColumn?.textContent).toContain('Infrastructure')
    expect(opsLoadModelRow?.querySelector('.command-group-panel')).toBe(opsCentralGroupPanel)
    expect(opsLoadModelPanel?.textContent).toContain('Load Model')
    expect(opsLoadModelPanel?.textContent).toContain('elapsed')
    expect(opsLoadModelPanel?.getAttribute('data-progress-pct')).toBe('38')
    expect(opsLoadModelPanel?.getAttribute('data-target-vusers')).toBe('2140')
    expect(Array.from(opsPrimaryColumn!.children).indexOf(opsLoadModelRow!)).toBeGreaterThan(0)

    await click(buttonNamed('Graph Wall'))

    expect(container.querySelector('[aria-label="Grafana wall mockup"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Group filters"]')).toBeNull()
    expect(container.textContent).not.toContain('Graph-heavy view')

    const graphSidebar = container.querySelector('.command-grafana-side')
    const graphSwitchPanel = graphSidebar?.querySelector('.command-view-switch-panel')
    const graphSelectionPanel = graphSidebar?.querySelector('[aria-label="Group selection"]')
    const graphErrorPanel = graphSidebar?.querySelector('[aria-label="Error drill-down"]')
    const graphOptionsSlot = graphSidebar?.querySelector('.command-graph-options-slot')
    const graphOptionsPanel = graphOptionsSlot?.querySelector('[aria-label="Graph display options"]')

    expect(graphSwitchPanel).not.toBeNull()
    expect(graphSidebar?.querySelector('.command-group-panel')).toBeNull()
    expect(graphSelectionPanel).not.toBeNull()
    expect(graphSelectionPanel?.textContent).toContain('Group Selection')
    expect(graphSelectionPanel?.textContent).toContain('3 / 3 groups visible')
    expect(Array.from(graphSidebar!.children).indexOf(graphSwitchPanel!)).toBeLessThan(Array.from(graphSidebar!.children).indexOf(graphSelectionPanel!))
    expect(graphOptionsPanel).not.toBeNull()
    expect(graphOptionsPanel?.textContent).toContain('Graph Options')
    expect(graphOptionsPanel?.textContent).toContain('Display Window')
    expect(graphOptionsPanel?.textContent).toContain('Layout')
    expect(graphOptionsPanel?.textContent).toContain('2x2')
    expect(graphOptionsPanel?.textContent).toContain('3x3')
    expect(graphOptionsPanel?.textContent).toContain('Role View')
    expect(graphOptionsPanel?.textContent).toContain('Display Time')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph data range')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph density')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph preset')
    expect(graphOptionsPanel?.textContent).not.toContain('Graph x-axis labels')
    expect(Array.from(graphSidebar!.children).indexOf(graphOptionsSlot!)).toBeGreaterThan(Array.from(graphSidebar!.children).indexOf(graphErrorPanel!))

    const runtimeGraphs = container.querySelector('.command-runtime-graphs')
    expect(runtimeGraphs?.classList.contains('command-runtime-graphs-grafana')).toBe(true)
    expect(runtimeGraphs?.querySelector('.charts-toolbar-title-row')?.textContent).toContain('Runtime Graphs')
    expect(runtimeGraphs?.querySelector('.charts-toolbar-title-row')?.textContent).toContain('Configure Graphs')
    expect(runtimeGraphs?.querySelector('[role="tablist"][aria-label="Graph groups"]')).not.toBeNull()
    expect(tabNamed('Test Results')?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('Transactions Per Second')
    expect(runtimeGraphs?.textContent).not.toContain('CPU Usage (%)')

    await click(tabNamed('Under Load'))

    expect(tabNamed('Under Load')?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('ART Under Load')
    expect(runtimeGraphs?.textContent).toContain('Network Egress Under Load')

    await click(tabNamed('Infrastructure'))

    expect(tabNamed('Infrastructure')?.getAttribute('aria-selected')).toBe('true')
    expect(runtimeGraphs?.textContent).toContain('CPU Usage (%)')
    expect(runtimeGraphs?.textContent).toContain('Memory Usage (MB)')
    expect(runtimeGraphs?.textContent).toContain('Route Latency (ms)')

    expect(runtimeGraphs?.getAttribute('data-visible-groups')).toContain('browse')
    const initialLoadImpactRange = runtimeGraphs?.getAttribute('data-load-impact-vuser-range')
    expect(initialLoadImpactRange).toBeTruthy()

    const browseVisibility = buttonByAriaLabel('Browse Web graph visibility')
    expect(browseVisibility.getAttribute('aria-pressed')).toBe('true')

    await click(browseVisibility)

    expect(browseVisibility.getAttribute('aria-pressed')).toBe('false')
    expect(graphSelectionPanel?.textContent).toContain('2 / 3 groups visible')
    expect(runtimeGraphs?.getAttribute('data-visible-groups')).not.toContain('browse')
    expect(runtimeGraphs?.getAttribute('data-visible-groups')).toContain('checkout')
    expect(runtimeGraphs?.getAttribute('data-load-impact-vuser-range')).not.toBe(initialLoadImpactRange)

    await click(buttonWithin(graphOptionsPanel!, 'Relative Time'))

    expect(buttonWithin(graphOptionsPanel!, 'Relative Time').getAttribute('aria-pressed')).toBe('true')

    expect(buttonWithin(graphOptionsPanel!, '3x3').getAttribute('aria-pressed')).toBe('true')
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const tableRowsToggle = inputByLabel('Show table rows beneath graphs')
    expect(tableRowsToggle.checked).toBe(false)

    await click(tableRowsToggle)

    expect(tableRowsToggle.checked).toBe(true)
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).not.toBeNull()

    await click(tableRowsToggle)
    await click(buttonWithin(graphOptionsPanel!, '2x2'))

    expect(buttonWithin(graphOptionsPanel!, '2x2').getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).toBeNull()
    expect(runtimeGraphs?.querySelector('.chart-metrics-table')).toBeNull()

    const graphExpandButton = runtimeGraphs?.querySelector<HTMLButtonElement>('.btn-expand')
    expect(graphExpandButton).not.toBeNull()

    await click(graphExpandButton!)

    expect(runtimeGraphs?.querySelector('.chart-card.expanded .chart-metrics-table')).not.toBeNull()

    await click(buttonWithin(graphOptionsPanel!, '3x3'))

    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
  })

  it('updates local run state and staged VUser targets', async () => {
    await renderPrototype()

    await click(buttonNamed('Pause'))

    expect(container.textContent).toContain('PAUSED')
    expect(container.textContent).toContain('Run paused at the next action boundary.')

    const checkoutTarget = inputByLabel('Checkout API target VUsers')
    await changeNumberInput(checkoutTarget, '1330')

    expect(checkoutTarget.value).toBe('1330')
    expect(container.textContent).toContain('Checkout API target staged at 1,330 VUsers.')
    expect(container.textContent).toContain('2,270 VU')
  })

  it('renders OPS tables while Graph Wall keeps configurable runtime graphs', async () => {
    await renderPrototype()

    const tableWorkspace = container.querySelector('[aria-label="Command center run tables"]')
    expect(tableWorkspace).not.toBeNull()
    expect(container.querySelector('.command-layout-ops .command-runtime-graphs')).toBeNull()
    expect(tableWorkspace?.textContent).toContain('Scenario Run')
    expect(tableWorkspace?.textContent).toContain('POST /orders')
    expect(tableWorkspace?.textContent).toContain('Target TPS')

    await click(tabNamed('Error Details'))

    expect(tableWorkspace?.textContent).toContain('HTTP 401 token refresh race')
    expect(tableWorkspace?.textContent).toContain('Request body')
    expect(tableWorkspace?.textContent).toContain('invalid_token')

    await click(buttonNamed('Cart lookup p99 spike'))

    expect(container.textContent).toContain('Drill-down selected: Cart lookup p99 spike')
    expect(tableWorkspace?.textContent).toContain('gateway timeout waiting for cart-cache-primary')

    await click(tabNamed('Infrastructure'))

    expect(tableWorkspace?.textContent).toContain('gen-east-1')
    expect(tableWorkspace?.textContent).toContain('checkout.api.internal:443')

    const filterInput = inputByLabel('Filter command center table rows')
    await changeTextInput(filterInput, 'central')

    expect(tableWorkspace?.textContent).toContain('gen-central-2')
    expect(tableWorkspace?.textContent).not.toContain('gen-east-1')

    await click(buttonNamed('Graph Wall'))

    expect(container.textContent).toContain('Runtime Graphs')
    expect(container.textContent).toContain('Virtual Users')
    expect(container.textContent).toContain('Transactions Per Second')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
    expect(container.querySelector('.chart-card.command-center-chart-card')).not.toBeNull()

    await click(buttonNamed('Configure Graphs'))

    const cpuToggle = inputByLabel('Show CPU Usage (%)')
    expect(cpuToggle.checked).toBe(true)

    await click(cpuToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('cpu')

    const expandButton = container.querySelector<HTMLButtonElement>('.btn-expand')
    expect(expandButton).not.toBeNull()

    await click(expandButton!)

    expect(container.textContent).toContain('Runtime graph expanded.')
    expect(container.querySelector('.chart-card.expanded')).not.toBeNull()

    const vuserToggle = inputByLabel('Show Virtual Users')
    expect(vuserToggle.checked).toBe(true)
    expect(container.querySelector('.charts-config-drawer')?.textContent).toContain('Graph Cards')
    expect(container.querySelector('.charts-config-drawer')?.textContent).not.toContain('Display Window')

    await click(vuserToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('vusers')
  })

  it('renders live command-center data when a test is selected', async () => {
    const liveTest: TestSummary = {
      id: 'live-run-1',
      name: 'Live Checkout Ramp',
      status: 'COMPLETE',
      total_vusers: 50,
      duration: '10m',
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 42,
        tps: 75.4,
        target_tps: 90,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 42,
            transactions: 1200,
            errors: 6,
            tps: 75.4,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
        error_patterns: [{
          signature: 'HTTP 500 checkout spike',
          sample: 'inventory reservation returned status 500',
          count: 6,
          first_seen: '2026-06-08T13:09:00Z',
          latest_seen: '2026-06-08T13:10:00Z',
          actions: ['POST /checkout'],
          affected_groups: ['Live Checkout'],
          error_pct: 0.5,
        }],
      },
    }
    const history: MetricsHistoryPoint[] = [{
      time: '09:10:00',
      timestampMs: Date.parse('2026-06-08T13:10:00Z'),
      vusers: 42,
      tps: 75.4,
      targetTps: 90,
      avgMs: 134,
      errorPct: 0.5,
      p50: 82,
      p90: 210,
      p95: 260,
      p99: 410,
      'Live Checkout_tps': 75.4,
    }]

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-run-1" initialTest={liveTest} history={history} />)
    })

    expect(container.textContent).toContain('Live Checkout Ramp')
    expect(container.textContent).toContain('Live Checkout')
    expect(container.textContent).toContain('HTTP 500 checkout spike')
    expect(container.textContent).not.toContain('In-flight Run Command Center')
  })

  it('uses per-group TPS target edits for goal-oriented live runs', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      test_id: 'live-goal-1',
      group: 'Live Checkout',
      target_tps: 120,
      target_average_tps: 120,
      goal_targets: {
        'Live Checkout': {
          group: 'Live Checkout',
          target_tps: 120,
          updated_at: '2026-06-08T13:11:00Z',
        },
      },
      status: 'adjusted',
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    const liveTest: TestSummary = {
      id: 'live-goal-1',
      ref_key: 'RUN-20260608-GOAL',
      name: 'Live Goal Checkout',
      status: 'RUNNING',
      duration: '30m',
      total_vusers: 50,
      created_at: '2026-06-08T13:00:00Z',
      started_at: '2026-06-08T13:05:00Z',
      scenario_mode: 'goal_oriented',
      goal_config: {
        target_average_tps: 90,
        min_vusers: 10,
        max_vusers: 200,
      },
      goal_targets: {
        'Live Checkout': {
          group: 'Live Checkout',
          target_tps: 90,
          updated_at: '2026-06-08T13:05:00Z',
        },
      },
      groups: [{
        name: 'Live Checkout',
        protocol: 'http',
        script_id: 'script-live',
        script_name: 'Live Checkout Script',
        vusers: 50,
      }],
      live_metrics: {
        timestamp: '2026-06-08T13:10:00Z',
        active_vusers: 42,
        tps: 75.4,
        target_tps: 90,
        throughput_gap_pct: 16.2,
        throughput_target_missed: true,
        avg_response_ms: 134,
        error_pct: 0.5,
        total_transactions: 1200,
        total_errors: 6,
        p50_ms: 82,
        p90_ms: 210,
        p95_ms: 260,
        p99_ms: 410,
        goal_status: {
          mode: 'goal_oriented',
          current_tps: 75.4,
          current_total_vusers: 42,
          target_average_tps: 90,
          suggested_total_vusers: 56,
          min_vusers: 10,
          max_vusers: 200,
        },
        groups: {
          'Live Checkout': {
            name: 'Live Checkout',
            vusers: 42,
            transactions: 1200,
            errors: 6,
            tps: 75.4,
            target_tps: 90,
            throughput_gap_pct: 16.2,
            throughput_target_missed: true,
            avg_response_ms: 134,
            p50_ms: 82,
            p90_ms: 210,
            p99_ms: 410,
          },
        },
      },
    }

    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-goal-1" initialTest={liveTest} history={[]} />)
    })

    expect(container.textContent).toContain('Target TPS')
    expect(container.textContent).toContain('suggested 56 VU')
    expect(container.textContent).toContain('guardrail 10-200 VU')

    const targetInput = inputByLabel('Live Checkout target TPS')
    expect(targetInput.value).toBe('90')

    await changeNumberInput(targetInput, '120')
    expect(container.textContent).toContain('Live Checkout target staged at 120.0 TPS.')

    await click(buttonNamed('Apply'))

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/live-goal-1/goal-targets'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ group: 'Live Checkout', target_tps: 120 }),
      }),
    )
    expect(container.textContent).toContain('Live Checkout TPS target applied at 120.0. Aggregate goal is 120.0 TPS.')
    expect(container.textContent).toContain('120.0')
  })

  async function renderPrototype() {
    await act(async () => {
      root.render(<CommandCenterPrototype prototypeMode />)
    })
  }

  function buttonNamed(name: string) {
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent?.includes(name))
    expect(button, `Expected button named ${name}`).toBeDefined()
    return button as HTMLButtonElement
  }

  function buttonByAriaLabel(label: string) {
    const button = container.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement | null
    expect(button, `Expected button labeled ${label}`).not.toBeNull()
    return button as HTMLButtonElement
  }

  function tabNamed(name: string) {
    const tab = Array.from(container.querySelectorAll('button[role="tab"]')).find(candidate => candidate.textContent?.includes(name))
    expect(tab, `Expected tab named ${name}`).toBeDefined()
    return tab as HTMLButtonElement
  }

  function buttonWithin(rootElement: Element, name: string) {
    const button = Array.from(rootElement.querySelectorAll('button')).find(candidate => candidate.textContent?.includes(name))
    expect(button, `Expected button named ${name}`).toBeDefined()
    return button as HTMLButtonElement
  }

  function inputByLabel(label: string) {
    const input = container.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement | null
    expect(input, `Expected input labeled ${label}`).not.toBeNull()
    return input as HTMLInputElement
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.click()
    })
  }

  async function changeNumberInput(input: HTMLInputElement, value: string) {
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  async function changeTextInput(input: HTMLInputElement, value: string) {
    await changeNumberInput(input, value)
  }
})
