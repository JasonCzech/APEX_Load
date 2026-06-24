// @vitest-environment jsdom

import { act, type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestHub from './TestHub'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  listScripts: vi.fn(),
  listDataPools: vi.fn(),
  listLoadModelTemplates: vi.fn(),
  getLoadModelSupportMatrix: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  estimateTest: vi.fn(),
  getSystemInventory: vi.fn(),
  listTests: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  sizeScenarioFromGoal: vi.fn(),
  getConfigDefaults: vi.fn().mockResolvedValue({ config: {} }),
}

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))
vi.mock('../hooks/useProject', () => ({
  useProject: () => ({ activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' } }),
}))
vi.mock('recharts', () => ({
  Area: () => <span />,
  CartesianGrid: () => <span />,
  ComposedChart: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  Line: () => <span />,
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  Tooltip: () => <span />,
  XAxis: () => <span />,
  YAxis: () => <span />,
}))

function findButton(root: HTMLElement, text: string): HTMLButtonElement | undefined {
  return Array.from(root.querySelectorAll('button')).find(button => button.textContent?.includes(text)) as HTMLButtonElement | undefined
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input) as HTMLInputElement, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(select) as HTMLSelectElement, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function goalBaselineRunNames(root: HTMLElement): string[] {
  return Array.from(root.querySelectorAll('.goal-baseline-table tbody tr.clickable-row .cell-name > div:first-child'))
    .map(node => node.textContent || '')
}

describe('TestHub build-from-goal', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)

    apiMock.listScenarios.mockResolvedValue({ scenarios: [] })
    apiMock.listScripts.mockResolvedValue({ scripts: [{ id: 'script-a', name: 'Checkout', protocol: 'http' }] })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [] })
    apiMock.listLoadModelTemplates.mockResolvedValue({ templates: [] })
    apiMock.getLoadModelSupportMatrix.mockResolvedValue({ support_matrix: [] })
    apiMock.estimateTest.mockResolvedValue({})
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'tests_and_scenarios',
        name: 'Tests and Scenarios',
        operations: [
          { id: 'view_tests', label: 'View tests', allowed: true, grant_source: 'project' },
          { id: 'manage_tests', label: 'Manage tests', allowed: true, grant_source: 'project' },
        ],
      }],
    })
    apiMock.listTests.mockResolvedValue({
      tests: [
        { id: 'run-2', name: 'Search Baseline', status: 'COMPLETE', groups: [], total_vusers: 4, duration: '3m', created_at: '2026-05-02T10:00:00Z', start_time: '2026-05-02T10:00:00Z' },
        { id: 'run-1', name: 'Baseline Run', status: 'COMPLETE', total_vusers: 1, duration: '1m', created_at: '2026-05-01T10:00:00Z', start_time: '2026-05-01T10:00:00Z' },
      ],
    })
    apiMock.getTestArchiveReport.mockImplementation((id: string) => Promise.resolve({
      manifest: { provenance: { groups: id === 'run-1'
        ? [{ name: 'Checkout', script_id: 'script-a', script_name: 'Checkout', protocol: 'http', vusers: 1 }]
        : [{ name: 'Search', script_id: 'script-b', script_name: 'Search Catalog', protocol: 'http', vusers: 4 }] } },
      overview: id === 'run-1'
        ? { transactions: 10, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 900, max_response_ms: 3800, resource_samples: 0, interval_summaries: 0, peak_tps: 42.4, peak_active_vusers: 1 }
        : { transactions: 20, errors: 1, error_pct: 5, avg_response_ms: 500, min_response_ms: 150, max_response_ms: 900, resource_samples: 0, interval_summaries: 0, peak_tps: 12.2, peak_active_vusers: 4 },
      by_action: {},
      by_group: id === 'run-1'
        ? { Checkout: { transactions: 10, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 900, max_response_ms: 3800, peak_tps: 42.4 } }
        : { Search: { transactions: 20, errors: 1, error_pct: 5, avg_response_ms: 500, min_response_ms: 150, max_response_ms: 900, peak_tps: 12.2 } },
      resource_peaks: { cpu_process_pct: 0, mem_process_mb: 0, net_sent_kbps: 0, net_recv_kbps: 0, disk_read_kbps: 0, disk_write_kbps: 0 },
    }))
    apiMock.sizeScenarioFromGoal.mockResolvedValue({
      baseline_test_id: 'run-1',
      groups: [{ group: 'Checkout', script_id: 'script-a', baseline_art_ms: 3000, target_tps: 100, computed_vusers: 300, basis: 'group' }],
      total_target_tps: 100,
      total_vusers: 300,
      scenario_draft: {
        name: 'Goal 100 TPS',
        duration: '10m',
        scenario_mode: 'goal_oriented',
        goal_config: { target_average_tps: 100, min_vusers: 150, max_vusers: 600 },
        groups: [{ name: 'Checkout', script_id: 'script-a', vusers: 300, ramp_up: '10s' }],
      },
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
  })

  async function flush() {
    await act(async () => { await Promise.resolve() })
  }

  it('sizes VUsers from a baseline run and applies the draft to the form', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()

    // Open the scenario builder, then the Build-from-goal panel.
    await act(async () => { findButton(container, 'Scenario Builder')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { findButton(container, 'Build from Goal')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.listTests).toHaveBeenCalled()
    const panel = container.querySelector('[data-testid="goal-sizing-panel"]') as HTMLElement | null
    expect(panel).toBeTruthy()
    const modelingOptions = container.querySelector('[data-testid="goal-sizing-modeling-options"]') as HTMLElement | null
    const baselinePicker = container.querySelector('.goal-baseline-picker') as HTMLElement | null
    expect(modelingOptions).toBeTruthy()
    expect(baselinePicker).toBeTruthy()
    if (!modelingOptions || !baselinePicker) throw new Error('Goal sizing panel did not render the modeling options and baseline table')
    expect(modelingOptions.compareDocumentPosition(baselinePicker) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(modelingOptions.textContent).toContain('Modeling Options')
    expect(modelingOptions.textContent).toContain('Select a completed run from the table below')

    expect(container.querySelector('select[aria-label="Baseline run"]')).toBeNull()
    expect(container.textContent).toContain('Search Baseline')
    expect(container.textContent).toContain('Baseline Run')
    expect(goalBaselineRunNames(container)).toEqual(['Search Baseline', 'Baseline Run'])

    const runSortHeader = Array.from(container.querySelectorAll('.goal-baseline-table th'))
      .find(header => header.textContent?.includes('Run ↕')) as HTMLTableCellElement | undefined
    expect(runSortHeader).toBeTruthy()
    await act(async () => { runSortHeader?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(goalBaselineRunNames(container)).toEqual(['Baseline Run', 'Search Baseline'])

    // Filter the completed-run table, then expand the run to inspect script metrics.
    const runFilter = container.querySelector('.goal-baseline-search input') as HTMLInputElement
    expect(runFilter).toBeTruthy()
    await act(async () => { setInputValue(runFilter, 'Baseline Run') })
    expect(container.textContent).toContain('Baseline Run')
    expect(container.textContent).not.toContain('Search Baseline')

    const expandButton = container.querySelector('button[aria-label="Expand Baseline Run scripts"]') as HTMLButtonElement
    expect(expandButton).toBeTruthy()
    await act(async () => { expandButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(apiMock.getTestArchiveReport).toHaveBeenCalledWith('run-1')
    expect(container.textContent).toContain('Scripts in Baseline Run')
    expect(container.textContent).toContain('Checkout')
    expect(container.textContent).toContain('42.4')
    expect(container.textContent).toContain('3,000 ms')

    // Select the completed baseline run from the table.
    const selectButton = container.querySelector('button[aria-label="Select Baseline Run as goal baseline"]') as HTMLButtonElement
    expect(selectButton).toBeTruthy()
    await act(async () => { selectButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    // Enter the target TPS for the Checkout group; the instant estimate should appear.
    const tpsInput = container.querySelector('input[aria-label="Target TPS for Checkout"]') as HTMLInputElement
    expect(tpsInput).toBeTruthy()
    expect(modelingOptions?.contains(tpsInput)).toBe(true)
    await act(async () => { setInputValue(tpsInput, '100') })
    await flush()
    expect(container.textContent).toContain('Estimated total: 300 VUsers')

    // Size the scenario against the backend.
    await act(async () => { findButton(container, 'Size scenario')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(apiMock.sizeScenarioFromGoal).toHaveBeenCalledWith({
      baseline_test_id: 'run-1',
      groups: [{ script_id: 'script-a', group: 'Checkout', target_tps: 100 }],
    })
    const result = container.querySelector('[data-testid="goal-sizing-result"]') as HTMLElement
    expect(result.textContent).toContain('300 VUsers')
    expect(result.querySelector('.goal-sizing-result-summary')?.textContent).toContain('Total VUsers')
    expect(result.querySelector('.goal-sizing-result-summary')?.textContent).toContain('Target TPS')
    const resultTable = result.querySelector('.goal-sizing-result-table') as HTMLTableElement | null
    expect(resultTable).toBeTruthy()
    expect(resultTable?.textContent).toContain('Computed VUsers')
    expect(resultTable?.textContent).toContain('Group metrics')
    expect(resultTable?.textContent).toContain('script-a')

    // Apply the draft to the scenario form.
    await act(async () => { findButton(container, 'Apply to scenario')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(container.querySelector('[data-testid="goal-sizing-panel"]')).toBeNull()
    const numberInputs = Array.from(container.querySelectorAll('input[type="number"]')) as HTMLInputElement[]
    expect(numberInputs.some(input => input.value === '300')).toBe(true)
  })
})
