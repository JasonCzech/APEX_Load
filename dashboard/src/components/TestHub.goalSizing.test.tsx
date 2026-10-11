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
  getConfigDefaults: vi.fn().mockResolvedValue({ config: {} }),
}
const projectMockState = vi.hoisted(() => ({ activeProjectId: 'project-1' }))

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))
vi.mock('../hooks/useProject', () => ({
  useProject: () => ({ activeProject: { id: projectMockState.activeProjectId, name: 'Checkout', created_at: '', updated_at: '' } }),
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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('TestHub build-from-goal', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    projectMockState.activeProjectId = 'project-1'
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

  // Goal setup is started from the Goal-Driven family on the Model & Shape step.
  async function openBuildFromGoal() {
    const click = async (element: Element | undefined) => {
      expect(element).toBeDefined()
      await act(async () => { element?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    }
    await click(Array.from(container.querySelectorAll('.scenario-wizard-step')).find(step => step.textContent?.includes('Model & Shape')))
    await click(Array.from(container.querySelectorAll('.scenario-source-card')).find(card => card.querySelector('span')?.textContent === 'Goal-Driven'))
    await click(findButton(container, 'Use Goal-Driven Scenario'))
  }

  it('opens Build from Goal in the guided worksheet and imports matching baseline ART', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()

    await act(async () => { findButton(container, 'Scenario builder')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await openBuildFromGoal()
    await flush()

    expect(apiMock.listTests).toHaveBeenCalled()
    expect(container.textContent).toContain('Goal-Driven Scenario Setup')
    expect(container.textContent).toContain('Throughput goal')
    expect(container.textContent).toContain('Estimate adaptive demand')
    expect(container.textContent).not.toContain('Scenario Mode')
    expect(container.querySelector('[aria-label="Average response time for Group 1"]')).not.toBeNull()
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    const picker = container.querySelector('select[aria-label="Completed baseline run"]') as HTMLSelectElement
    expect(Array.from(picker.options).map(option => option.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Baseline Run'),
      expect.stringContaining('Search Baseline'),
    ]))
    await act(async () => {
      picker.value = 'run-1'
      picker.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(apiMock.getTestArchiveReport).toHaveBeenCalledWith('run-1')
	await act(async () => { findButton(container, 'Enter ART manually')?.click() })
	const artInput = container.querySelector('input[aria-label="Average response time for Group 1"]') as HTMLInputElement
	await act(async () => { setInputValue(artInput, '3000') })
    const tpsInput = container.querySelector('input[aria-label="Target TPS for Group 1"]') as HTMLInputElement
    expect(tpsInput).toBeTruthy()
    await act(async () => { setInputValue(tpsInput, '100') })
    await flush()
    expect(container.textContent).toContain('300 seed VUsers')
	expect(container.textContent).toContain('300–600 VUsers')
  })

  it('drains bounded test pages so older completed baselines remain selectable', async () => {
    const newest = { id: 'run-newest', name: 'Newest Baseline', status: 'COMPLETE', groups: [], total_vusers: 4, duration: '3m', created_at: '2026-05-02T10:00:00Z' }
    const oldest = { id: 'run-oldest', name: 'Oldest Baseline', status: 'COMPLETE', groups: [], total_vusers: 1, duration: '1m', created_at: '2026-05-01T10:00:00Z' }
    apiMock.listTests.mockImplementation(({ offset = 0 }: { offset?: number }) => Promise.resolve({
      tests: offset === 0 ? [newest] : [oldest],
      count: 1,
      total: 2,
      limit: 1000,
      offset,
    }))

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()
    await act(async () => { findButton(container, 'Scenario builder')?.click() })
    await openBuildFromGoal()
    await flush()

    expect(apiMock.listTests).toHaveBeenNthCalledWith(1, { limit: 1000, offset: 0, project_id: 'project-1', summary: true })
    expect(apiMock.listTests).toHaveBeenNthCalledWith(2, { limit: 1000, offset: 1, project_id: 'project-1', summary: true })
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    expect(container.textContent).toContain('Newest Baseline')
    expect(container.textContent).toContain('Oldest Baseline')
    expect(container.textContent).toContain('2 completed runs available')
  })

  it('ignores a completed-run list that resolves after the active project changes', async () => {
    const projectAResponse = deferred<{ tests: Array<{ id: string; name: string; status: string; groups: never[]; total_vusers: number; duration: string; created_at: string }> }>()
    apiMock.listTests.mockReturnValueOnce(projectAResponse.promise)

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()
    await act(async () => { findButton(container, 'Scenario builder')?.click() })
    await openBuildFromGoal()
    expect(apiMock.listTests).toHaveBeenCalledWith({ limit: 1000, offset: 0, project_id: 'project-1', summary: true })

    projectMockState.activeProjectId = 'project-2'
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()

    await act(async () => {
      projectAResponse.resolve({
        tests: [{ id: 'project-a-run', name: 'Project A Baseline', status: 'COMPLETE', groups: [], total_vusers: 1, duration: '1m', created_at: '2026-05-01T10:00:00Z' }],
      })
      await projectAResponse.promise
    })

    expect(container.textContent).not.toContain('Project A Baseline')
    apiMock.listTests.mockResolvedValueOnce({
      tests: [{ id: 'project-b-run', name: 'Project B Baseline', status: 'COMPLETE', groups: [], total_vusers: 2, duration: '2m', created_at: '2026-05-02T10:00:00Z' }],
    })
    await openBuildFromGoal()
    await flush()

    expect(apiMock.listTests).toHaveBeenLastCalledWith({ limit: 1000, offset: 0, project_id: 'project-2', summary: true })
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    expect(container.textContent).toContain('Project B Baseline')
    expect(container.textContent).not.toContain('Project A Baseline')
  })

  it('ignores a baseline response from a discarded draft in the same project', async () => {
    const report = deferred<Awaited<ReturnType<typeof apiMock.getTestArchiveReport>>>()
    apiMock.getTestArchiveReport.mockReturnValueOnce(report.promise)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} initialView="builder" />))
    await flush()
    await openBuildFromGoal()
    await flush()
    expect(container.querySelector('[aria-label="Average response time for Group 1"]')).not.toBeNull()
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    const picker = container.querySelector<HTMLSelectElement>('[aria-label="Completed baseline run"]')!
    await act(async () => { picker.value = 'run-1'; picker.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => findButton(container, 'Start Over')?.click())
    await act(async () => report.resolve({
      manifest: { provenance: { groups: [{ name: 'Checkout', script_id: 'script-a', script_name: 'Checkout', protocol: 'http', vusers: 1 }] } },
      by_group: { Checkout: { transactions: 10, avg_response_ms: 3000 } },
      overview: { transactions: 10, avg_response_ms: 3000 },
    }))
    await act(async () => findButton(container, 'Run Settings')?.click())
    expect(container.querySelector('[aria-label="Scenario Mode"]')).toBeNull()
    expect(container.querySelector('.goal-driven-worksheet')).toBeNull()
    expect(container.querySelector('.composition-timeline')).not.toBeNull()
    confirm.mockRestore()
  })

  it('shows baseline import failures on the Goal-Driven worksheet', async () => {
    apiMock.getTestArchiveReport.mockRejectedValueOnce(new Error('Archive unavailable'))
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} initialView="builder" />))
    await flush()
    await openBuildFromGoal()
    await flush()
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    const picker = container.querySelector<HTMLSelectElement>('[aria-label="Completed baseline run"]')!
    await act(async () => { picker.value = 'run-1'; picker.dispatchEvent(new Event('change', { bubbles: true })) })
    await flush()
    await flush()

    expect(container.querySelector('.goal-driven-baseline-picker [role="alert"]')?.textContent).toContain('Archive unavailable')
  })

  it('keeps the latest baseline selection when an older report resolves later', async () => {
    const firstReport = deferred<Awaited<ReturnType<typeof apiMock.getTestArchiveReport>>>()
    apiMock.getTestArchiveReport.mockImplementationOnce(() => firstReport.promise)

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flush()
    await act(async () => { findButton(container, 'Scenario builder')?.click() })
    await openBuildFromGoal()
    await flush()

    expect(container.querySelector('[aria-label="Average response time for Group 1"]')).not.toBeNull()
    await act(async () => { findButton(container, 'Import completed baseline')?.click() })
    const picker = container.querySelector('select[aria-label="Completed baseline run"]') as HTMLSelectElement
    await act(async () => { picker.value = 'run-1'; picker.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => { picker.value = 'run-2'; picker.dispatchEvent(new Event('change', { bubbles: true })) })
    await flush()

    expect(picker.value).toBe('run-2')

    await act(async () => {
      firstReport.resolve({
        manifest: { provenance: { groups: [{ name: 'Checkout', script_id: 'script-a', script_name: 'Checkout', protocol: 'http', vusers: 1 }] } },
        overview: { transactions: 10, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 900, max_response_ms: 3800, resource_samples: 0, interval_summaries: 0, peak_tps: 42.4, peak_active_vusers: 1 },
        by_action: {},
        by_group: { Checkout: { transactions: 10, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 900, max_response_ms: 3800, peak_tps: 42.4 } },
        resource_peaks: { cpu_process_pct: 0, mem_process_mb: 0, net_sent_kbps: 0, net_recv_kbps: 0, disk_read_kbps: 0, disk_write_kbps: 0 },
      })
      await firstReport.promise
    })

    expect(picker.value).toBe('run-2')
  })
})
