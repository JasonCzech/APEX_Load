// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

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
    container.remove()
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
    expect(graphOptionsPanel?.textContent).toContain('Zoom')
    expect(graphOptionsPanel?.textContent).toContain('Role View')
    expect(graphOptionsPanel?.textContent).toContain('Display Time')
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

    await click(buttonWithin(graphOptionsPanel!, '6 Graphs'))

    expect(container.querySelector('.charts-grid.six-up.command-center-graphs')).not.toBeNull()
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

  it('configures runtime graphs, expands cards, persists per view, and selects error drill-down rows', async () => {
    await renderPrototype()

    expect(container.textContent).toContain('Runtime Graphs')
    expect(container.textContent).toContain('Virtual Users')
    expect(container.textContent).toContain('Transactions Per Second')
    expect(container.querySelector('.charts-grid.command-center-graphs.command-center-compact')).not.toBeNull()
    expect(container.querySelector('.chart-card.command-center-chart-card')).not.toBeNull()

    await click(buttonNamed('Configure Graphs'))

    const cpuToggle = inputByLabel('Show CPU Usage (%)')
    expect(cpuToggle.checked).toBe(false)

    await click(cpuToggle)

    expect(container.textContent).toContain('CPU Usage (%)')
    expect(window.localStorage.getItem('apexload.commandCenter.ops.hiddenCharts') || '').not.toContain('"cpu"')

    const expandButton = container.querySelector<HTMLButtonElement>('.btn-expand')
    expect(expandButton).not.toBeNull()

    await click(expandButton!)

    expect(container.textContent).toContain('Runtime graph expanded.')
    expect(container.querySelector('.chart-card.expanded')).not.toBeNull()

    await click(buttonNamed('Cart lookup p99 spike'))

    expect(container.textContent).toContain('Drill-down selected: Cart lookup p99 spike')
    expect(container.textContent).toContain('upstream response exceeded 2s timeout budget')

    await click(buttonNamed('Graph Wall'))
    await click(buttonNamed('Configure Graphs'))

    const vuserToggle = inputByLabel('Show Virtual Users')
    expect(vuserToggle.checked).toBe(true)
    expect(container.querySelector('.charts-config-drawer')?.textContent).toContain('Graph Cards')
    expect(container.querySelector('.charts-config-drawer')?.textContent).not.toContain('Display Window')

    await click(vuserToggle)

    expect(window.localStorage.getItem('apexload.commandCenter.grafana.hiddenCharts')).toContain('vusers')
    expect(window.localStorage.getItem('apexload.commandCenter.ops.hiddenCharts') || '').not.toContain('vusers')
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

  async function renderPrototype() {
    await act(async () => {
      root.render(<CommandCenterPrototype />)
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
})
