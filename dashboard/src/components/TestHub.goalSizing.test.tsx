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
  listTests: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  sizeScenarioFromGoal: vi.fn(),
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
      tests: [{ id: 'run-1', name: 'Baseline Run', status: 'COMPLETE', groups: [], total_vusers: 1, duration: '1m', created_at: '' }],
    })
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: { provenance: { groups: [{ name: 'Checkout', script_id: 'script-a', script_name: 'Checkout', protocol: 'http', vusers: 1 }] } },
      overview: { avg_response_ms: 3000 },
      by_action: {},
      by_group: { Checkout: { transactions: 1, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 3000, max_response_ms: 3000 } },
    })
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

    // Select the completed baseline run.
    const baselineSelect = container.querySelector('select[aria-label="Baseline run"]') as HTMLSelectElement
    await act(async () => { setSelectValue(baselineSelect, 'run-1') })
    await flush()
    expect(apiMock.getTestArchiveReport).toHaveBeenCalledWith('run-1')

    // Enter the target TPS for the Checkout group; the instant estimate should appear.
    const tpsInput = container.querySelector('input[aria-label="Target TPS for Checkout"]') as HTMLInputElement
    expect(tpsInput).toBeTruthy()
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

    // Apply the draft to the scenario form.
    await act(async () => { findButton(container, 'Apply to scenario')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(container.querySelector('[data-testid="goal-sizing-panel"]')).toBeNull()
    const numberInputs = Array.from(container.querySelectorAll('input[type="number"]')) as HTMLInputElement[]
    expect(numberInputs.some(input => input.value === '300')).toBe(true)
  })
})
