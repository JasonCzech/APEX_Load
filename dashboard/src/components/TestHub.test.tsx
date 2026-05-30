// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
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
  validateScenarioLoadModel: vi.fn(),
  estimateTest: vi.fn(),
  createScenario: vi.fn(),
  updateScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listScenarioResults: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children }: { children?: ReactNode }) => <svg data-testid="scenario-model-chart">{children}</svg>
  const MockTooltip = () => <g data-testid="scenario-model-tooltip" />
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')
  return {
    Area: ({ dataKey, name }: { dataKey?: string; name?: string }) => <path data-testid="scenario-model-area" data-key={dataKey} data-name={name} />,
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Line: ({ dataKey, name }: { dataKey?: string; name?: string }) => <path data-testid="scenario-model-line" data-key={dataKey} data-name={name} />,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="scenario-model-responsive">{children}</div>,
    Tooltip: MockTooltip,
    XAxis: ({ label }: { label?: string | { value?: string } }) => <g data-testid="scenario-model-x-axis" data-label={labelText(label)} />,
    YAxis: ({ label, orientation }: { label?: string | { value?: string }; orientation?: string }) => (
      <g data-testid="scenario-model-y-axis" data-label={labelText(label)} data-orientation={orientation ?? 'left'} />
    ),
  }
})

describe('TestHub', () => {
  let container: HTMLDivElement
  let root: Root

  const clickButton = async (text: string) => {
    const button = Array.from(container.querySelectorAll('button')).find(item => item.textContent?.includes(text))
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const clickWizardStep = async (text: string) => {
    let button = Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
    if (!button) {
      await clickButton('Scenario Builder Wizard')
      button = Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
    }
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.listScenarios.mockResolvedValue({ scenarios: [], count: 0 })
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [], count: 0 })
    apiMock.listLoadModelTemplates.mockResolvedValue({
      templates: [{
        id: 'open-api-arrival-rate',
        name: 'Open API Arrival Rate',
        shape_id: 'open-arrival-rate',
        description: 'Throughput-oriented API load shape for queueing-sensitive services.',
        scenario: {
          scenario_mode: 'goal_oriented',
          duration: '10m',
          goal_config: {
            target_average_tps: 25,
            min_vusers: 5,
            max_vusers: 50,
            p95_latency_ms: 800,
            max_error_pct: 1,
          },
          groups: [{ name: 'arrival-rate', vusers: 10, ramp_up: '2m' }],
        },
      }, {
        id: 'chaos-resilience-shakeout',
        name: 'Chaos Resilience With Shakeout Gate',
        shape_id: 'chaos-resilience',
        description: 'Staged resilience starter with a pre-run shakeout gate.',
        scenario: {
          scenario_mode: 'goal_oriented',
          duration: '18m',
          script_shakeout: true,
          resource_baseline: true,
          goal_config: {
            target_average_tps: 15,
            min_vusers: 5,
            max_vusers: 75,
            p95_latency_ms: 1200,
            max_error_pct: 5,
          },
          runtime_settings: {
            error_handling: {
              continue_on_error: true,
              snapshot_on_error: true,
            },
          },
          groups: [{
            name: 'chaos-steady-state',
            vusers: 25,
            ramp_up: '3m',
            ramp_down: '2m',
            stages: [
              { name: 'Baseline', type: 'ramp_up', duration: '3m', target_vusers: 25 },
              { name: 'Turbulence', type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 },
              { name: 'Recovery', type: 'ramp_down', duration: '5m', target_vusers: 5 },
            ],
          }],
        },
      }],
      count: 2,
    })
    apiMock.getLoadModelSupportMatrix.mockResolvedValue({
      support_matrix: [
        {
          id: 'closed-vuser',
          name: 'Closed VUser',
          category: 'vuser',
          status: 'executed',
          apexload_shape: 'Scenario groups with fixed VUsers and duration.',
          fields: ['groups[].vusers', 'duration', 'groups[].ramp_up'],
          notes: 'Primary execution model.',
        },
        {
          id: 'open-arrival-rate',
          name: 'Open Arrival Rate',
          category: 'throughput',
          status: 'executed',
          apexload_shape: 'Open workload runtime using target throughput controls.',
          fields: ['config.pacing.tps', 'goal_config.target_average_tps'],
          notes: 'Runtime emits target TPS and throughput miss flags.',
        },
        {
          id: 'stepped-ramp',
          name: 'Stepped Ramp',
          category: 'ramp',
          status: 'executed',
          apexload_shape: 'Staged segments with hold, ramp-up, and ramp-down.',
          fields: ['groups[].stages', 'groups[].stages[].target_vusers'],
          notes: 'Dedicated staged segments are available in scenario authoring.',
        },
        {
          id: 'chaos-resilience',
          name: 'Chaos Resilience',
          category: 'resilience',
          status: 'executed',
          apexload_shape: 'Guarded load envelope with pre-run script shakeout and staged recovery.',
          fields: ['script_shakeout', 'groups[].stages', 'runtime_settings.error_handling.snapshot_on_error'],
          notes: 'Preserves shakeout evidence before full load.',
        },
      ],
      status_counts: { executed: 4 },
      notes: [],
    })
    apiMock.validateScenarioLoadModel.mockResolvedValue({
      status: 'needs_review',
      status_counts: { ready: 2, needs_review: 1 },
      total_vusers: 10,
      peak_target_tps: 25,
      items: [
        { dimension: 'modeled_demand', status: 'ready', summary: 'Closed workload uses 10 VUsers.', target_vusers: 10 },
        { dimension: 'generator_placement', status: 'needs_review', summary: 'Auto-assignment will use configured generators.' },
      ],
    })
    apiMock.estimateTest.mockResolvedValue({
      duration: '1m',
      groups: [],
      assignment: { mode: 'auto' },
      footprint: {
        total_vusers: 10,
        duration: '1m',
        complexity_score: 1.25,
        estimated_cpu_cores: 0.01,
        estimated_memory_mb: 8,
        estimated_network_mbps: 1,
      },
      assignment_recommendation: {
        mode: 'auto',
        recommended_targets: ['gen-a'],
        summary: 'Auto-assignment recommends 1 host for the current scenario footprint.',
      },
    })
    apiMock.createScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.updateScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.deleteScenario.mockResolvedValue({ deleted: 'scenario-1' })
    apiMock.listScenarioResults.mockResolvedValue({ tests: [], count: 0 })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: false,
      authenticated: false,
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: '',
          operations: [{ id: 'manage_tests', label: 'Manage tests', allowed: true }],
        },
      ],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('creates a scenario from the typed form payload', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1')
    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')
    expect(apiMock.listLoadModelTemplates).toHaveBeenCalled()
    expect(apiMock.getLoadModelSupportMatrix).toHaveBeenCalled()

    await clickWizardStep('Scripts')

    const inputs = Array.from(container.querySelectorAll('input'))
    const nameInput = inputs.find(input => input.placeholder === 'Scenario name')
    const vusersInput = inputs.find(input => input.getAttribute('type') === 'number')

    expect(nameInput).toBeDefined()
    expect(vusersInput).toBeDefined()
    if (!nameInput || !vusersInput) return

    await act(async () => {
      setInputValue(nameInput, 'Checkout Scenario')
      setInputValue(vusersInput, '25')
    })

    await clickWizardStep('Run Settings')
    const durationField = getDurationFieldByLabel(container, 'Duration')
    const durationInput = durationField?.input
    const durationUnit = durationField?.select
    expect(durationInput).toBeDefined()
    expect(durationUnit).toBeDefined()
    if (!durationInput || !durationUnit) return

    await act(async () => {
      setInputValue(durationInput, '5')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith({
      name: 'Checkout Scenario',
      duration: '5m',
      scenario_mode: 'vuser_driven',
      script_shakeout: false,
      resource_baseline: false,
      assignment: { mode: 'auto' },
      groups: [
        {
          script_id: 'script-1',
          vusers: 25,
          initialization: 'at_runtime',
        },
      ],
      data_pools: [],
    })
  })

  it('shows saved assignment defaults in the scenario library', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Pinned Checkout',
        duration: '5m',
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a', 'gen-b'],
        },
        scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    const libraryHeaders = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(libraryHeaders.some(header => header.includes('Scenario'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Workload'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Releases'))).toBe(true)
    expect(container.textContent).toContain('Pinned Checkout')
    expect(container.textContent).toContain('Specific Hosts')
    expect(container.textContent).toContain('gen-a, gen-b')
    expect(container.textContent).toContain('No data pools')
    expect(container.textContent).toContain('0 results')

    const searchInput = container.querySelector('input[type="search"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'gen-b')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Pinned Checkout')

    await act(async () => {
      setInputValue(searchInput, 'does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match this search.')

    await act(async () => {
      setInputValue(searchInput, '')
    })

    const viewRunsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View Runs'))
    expect(viewRunsButton).toBeDefined()
    if (!viewRunsButton) return

    await act(async () => {
      viewRunsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScenarioResults).toHaveBeenCalledWith('scenario-1')
    expect(container.textContent).toContain('No runs for this scenario yet.')
  })

  it('clones an existing scenario draft with data pool bindings from the wizard source step', async () => {
    const onSelectTest = vi.fn()
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-source',
        name: 'Pinned Checkout',
        duration: '7m',
        assignment: { mode: 'auto' },
        scripts: [{ order: 0, script_id: 'script-1', script_name: 'Checkout Flow', name: 'Checkout', vusers: 20, ramp_up: '1m', initialization: 'at_runtime' }],
        data_pools: [{ pool_id: 'pool-1', pool_name: 'Customers', alias: 'customers', access_mode: 'consume', selection_strategy: 'unique' }],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        updated_at: '2026-05-12T00:00:00Z',
        script_count: 1,
      }],
      count: 1,
    })
    apiMock.listScenarioResults.mockResolvedValueOnce({
      tests: [{
        id: 'run-1',
        name: 'Checkout Load 001',
        status: 'COMPLETE',
        release_id: 'release-1',
        groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 20 }],
        total_vusers: 20,
        duration: '7m',
        created_at: '2026-05-13T00:00:00Z',
        start_time: '2026-05-13T00:01:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickButton('Scenario Builder Wizard')
    await clickButton('Model from Existing Scenario')
    await clickButton('Pinned Checkout')
    await flushPromises()

    expect(container.textContent).toContain('20 peak VUsers')
    expect(container.textContent).toContain('Choose Scenario To Model')
    expect(container.textContent).not.toContain('Existing Scripts Library')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(apiMock.listScenarioResults).toHaveBeenCalledWith('scenario-source')
    const selectedCloneCard = Array.from(container.querySelectorAll('.scenario-clone-card.selected')).find(card => card.textContent?.includes('Pinned Checkout'))
    expect(selectedCloneCard).toBeDefined()
    const chart = container.querySelector('[data-testid="scenario-model-chart"]')
    const runPanel = container.querySelector('.scenario-source-runs-panel')
    expect(runPanel).toBeDefined()
    const runPanelText = Array.from(container.querySelectorAll('.scenario-source-runs-panel')).map(panel => panel.textContent || '').join(' ')
    expect(runPanelText).toContain('Past Scenario Runs')
    expect(runPanelText).toContain('Checkout Load 001')
    if (chart && runPanel) {
      expect((chart.compareDocumentPosition(runPanel) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true)
    }

    await clickButton('Next')

    expect(Array.from(container.querySelectorAll('input')).some(input => input.value === 'Pinned Checkout (Copy)')).toBe(true)
    expect(container.textContent).not.toContain('Choose Scenario To Model')
    expect(container.textContent).toContain('Existing Scripts Library')

    await clickButton('Save Scenario')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Pinned Checkout (Copy)',
      duration: '7m',
      groups: [expect.objectContaining({
        name: 'Checkout',
        script_id: 'script-1',
        vusers: 20,
        ramp_up: '1m',
      })],
      data_pools: [expect.objectContaining({
        pool_id: 'pool-1',
        alias: 'customers',
        access_mode: 'consume',
        selection_strategy: 'unique',
      })],
    }))
  })

  it('edits scenarios with multiple script groups without dropping secondary scripts', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [
        { id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
        { id: 'script-2', name: 'Payments Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
      ],
      count: 2,
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-multi',
        name: 'Checkout Plus Payments',
        duration: '8m',
        assignment: { mode: 'auto' },
        scripts: [
          { order: 0, script_id: 'script-1', script_name: 'Checkout Flow', name: 'Checkout', vusers: 25, ramp_up: '2m', initialization: 'at_runtime' },
          { order: 1, script_id: 'script-2', script_name: 'Payments Flow', name: 'Payments', vusers: 5, ramp_up: '1m', initialization: 'at_once' },
        ],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        updated_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
        script_count: 2,
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    if (!editButton) return

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const groupCards = Array.from(container.querySelectorAll('.scenario-script-group-card'))
    expect(groupCards).toHaveLength(2)
    expect(groupCards[0].textContent).toContain('Checkout Flow')
    expect(groupCards[1].textContent).toContain('Payments Flow')
    const secondGroupInitSelect = Array.from(groupCards[1].querySelectorAll('select')).find(select => select.textContent?.includes('Init/teardown once per group')) as HTMLSelectElement | undefined
    expect(secondGroupInitSelect?.value).toBe('at_once')

    const secondGroupVUsers = Array.from(groupCards[1].querySelectorAll('input')).find(input => input.type === 'number')
    expect(secondGroupVUsers).toBeDefined()
    if (!secondGroupVUsers) return

    await act(async () => {
      setInputValue(secondGroupVUsers, '15')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateScenario).toHaveBeenCalledWith('scenario-multi', expect.objectContaining({
      name: 'Checkout Plus Payments',
      duration: '8m',
      groups: [
        expect.objectContaining({
          name: 'Checkout',
          script_id: 'script-1',
          vusers: 25,
          ramp_up: '2m',
          initialization: 'at_runtime',
        }),
        expect.objectContaining({
          name: 'Payments',
          script_id: 'script-2',
          vusers: 15,
          ramp_up: '1m',
          initialization: 'at_once',
        }),
      ],
    }))
  })

  it('creates a browser scenario with mobile runtime overrides', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-browser', name: 'Mobile Checkout', protocol: 'browser', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Mobile Browser Scenario')
    })

    await clickWizardStep('Run Settings')

    expect(container.textContent).toContain('Browser Runtime')

    const profileSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Device profile')) as HTMLSelectElement | undefined
    const orientationSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Orientation')) as HTMLSelectElement | undefined
    const touchSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Touch mode')) as HTMLSelectElement | undefined
    const headlessSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Headless')) as HTMLSelectElement | undefined

    expect(profileSelect).toBeDefined()
    expect(orientationSelect).toBeDefined()
    expect(touchSelect).toBeDefined()
    expect(headlessSelect).toBeDefined()
    if (!profileSelect || !orientationSelect || !touchSelect || !headlessSelect) return

    await act(async () => {
      setSelectValue(profileSelect, 'iphone_se')
      setSelectValue(orientationSelect, 'portrait')
      setSelectValue(touchSelect, 'true')
      setSelectValue(headlessSelect, 'false')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Mobile Browser Scenario',
      runtime_settings: {
        browser: {
          device_profile: 'iphone_se',
          orientation: 'portrait',
          touch_mode: true,
          headless: false,
        },
      },
    }))
    expect(apiMock.estimateTest).toHaveBeenCalledWith(expect.objectContaining({
      runtime_settings: {
        browser: expect.objectContaining({
          device_profile: 'iphone_se',
          orientation: 'portrait',
          touch_mode: true,
          headless: false,
        }),
      },
    }))
  })

  it('shows saved browser runtime defaults in the scenario library', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Mobile Checkout',
        duration: '5m',
        assignment: { mode: 'auto' },
        runtime_settings: {
          browser: {
            device_profile: 'iphone_se',
            orientation: 'portrait',
            touch_mode: true,
            headless: false,
          },
        },
        scripts: [{ script_id: 'script-browser', script_name: 'Mobile Checkout', vusers: 5 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Mobile Checkout')
    expect(container.textContent).toContain('Browser')
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')
  })

  it('applies workload templates and renders load-model validation findings', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Choose Workload Shape')
    const templateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open API Arrival Rate'))
    expect(templateButton).toBeDefined()
    if (!templateButton) return

    await act(async () => {
      templateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    let inputs = Array.from(container.querySelectorAll('input'))
    const rampUpField = getDurationFieldByLabel(container, 'Ramp Up')

    expect(rampUpField?.input.value).toBe('2')
    expect(rampUpField?.select.value).toBe('m')

    await clickWizardStep('Run Settings')
    inputs = Array.from(container.querySelectorAll('input'))
    const durationField = getDurationFieldByLabel(container, 'Duration')
    const modeSelect = Array.from(container.querySelectorAll('select')).find(select => select.value === 'goal_oriented') as HTMLSelectElement | undefined

    expect(durationField?.input.value).toBe('10')
    expect(durationField?.select.value).toBe('m')
    expect(modeSelect?.value).toBe('goal_oriented')
    expect(container.textContent).toContain('Goal Controller')

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Load Model'))
    expect(validateButton).toBeDefined()
    if (!validateButton) return

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateScenarioLoadModel).toHaveBeenCalledWith(expect.objectContaining({
      duration: '10m',
      scenario_mode: 'goal_oriented',
      goal_config: expect.objectContaining({
        target_average_tps: 25,
        min_vusers: 5,
        max_vusers: 50,
      }),
      groups: [expect.objectContaining({
        script_id: 'script-1',
        vusers: 10,
        ramp_up: '2m',
      })],
    }))
    expect(container.textContent).toContain('Needs Review')
    expect(container.textContent).toContain('Generator Placement')
    expect(container.textContent).toContain('Auto-assignment will use configured generators.')
  })

  it('applies the chaos resilience template with shakeout and evidence settings', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')
    await clickButton('Chaos Resilience With Shakeout Gate')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Chaos Checkout Scenario')
    })

    await clickWizardStep('Run Settings')

    expect(getDurationFieldByLabel(container, 'Duration')?.input.value).toBe('18')
    expect(getDurationFieldByLabel(container, 'Duration')?.select.value).toBe('m')
    expect(container.textContent).toContain('Pre-Run Assurance')
    expect(container.textContent).toContain('Script shakeout gate')
    expect(container.textContent).toContain('Snapshot on error')
    expect(container.textContent).toContain('Goal Controller')

    await clickButton('Save Scenario')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Chaos Checkout Scenario',
      duration: '18m',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      goal_config: expect.objectContaining({
        target_average_tps: 15,
        max_error_pct: 5,
      }),
      runtime_settings: {
        error_handling: {
          continue_on_error: true,
          snapshot_on_error: true,
        },
      },
      groups: [expect.objectContaining({
        name: 'chaos-steady-state',
        script_id: 'script-1',
        vusers: 25,
        ramp_up: '3m',
        ramp_down: '2m',
        stages: [
          { name: 'Baseline', type: 'ramp_up', duration: '3m', target_vusers: 25 },
          { name: 'Turbulence', type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 },
          { name: 'Recovery', type: 'ramp_down', duration: '5m', target_vusers: 5 },
        ],
      })],
    }))
  })

  it('renders the unified workload shape catalog in scenario authoring', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Choose Workload Shape')
    expect(container.textContent).toContain('Closed VUser')
    expect(container.textContent).toContain('Executed')
    expect(container.textContent).toContain('groups[].vusers')
    expect(container.textContent).toContain('Stepped Ramp')
    expect(container.textContent).toContain('Executed')
    expect(container.textContent).toContain('groups[].stages[].target_vusers')
    expect(container.textContent).toContain('Chaos Resilience')
    expect(container.textContent).toContain('script_shakeout')
    expect(container.querySelectorAll('.scenario-shape-visual')).toHaveLength(4)
    expect(container.querySelector('[aria-label="Closed VUser workload shape"]')).toBeTruthy()
  })

  it('toggles between the scenario library and builder wizard views', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Scenario Library')
    expect(container.querySelector('.scenario-builder')).toBeNull()
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Scenario Builder Wizard')

    expect(container.textContent).toContain('Scenario Builder Wizard')
    expect(container.textContent).not.toContain('Model Preview')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Model Preview')
    expect(container.querySelector('[data-testid="scenario-model-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(Array.from(container.querySelectorAll('[data-testid="scenario-model-y-axis"]')).map(axis => axis.getAttribute('data-label'))).toContain('VUsers')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Scripts')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Existing Scripts Library')
    expect(container.textContent).toContain('Checkout Flow')

    await clickWizardStep('Run Settings')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Data')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Review')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Cancel')

    expect(container.textContent).toContain('Scenario Library')
    expect(container.textContent).not.toContain('Existing Scripts Library')
  })

  it('renders script groups in the spacious multi-card list on the Scripts step', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    await clickButton('+ Add Script Group')
    await clickButton('+ Add Script Group')
    await clickButton('+ Add Script Group')

    const groupList = container.querySelector('.scenario-script-group-list.scenario-script-group-list-spacious')
    expect(groupList).toBeDefined()
    expect(groupList?.querySelectorAll('.scenario-script-group-card')).toHaveLength(4)
  })

  it('saves from review and opens a new test run for the saved scenario', async () => {
    const onSelectTest = vi.fn()
    const onCreateTestFromScenario = vi.fn()
    apiMock.createScenario.mockResolvedValueOnce({ id: 'scenario-new-run' })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onCreateTestFromScenario={onCreateTestFromScenario} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Review Handoff Scenario')
    })

    await clickWizardStep('Review')

    const saveAndCreateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save & Create Test Run'))
    expect(saveAndCreateButton).toBeDefined()
    if (!saveAndCreateButton) return

    await act(async () => {
      saveAndCreateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Review Handoff Scenario',
    }))
    expect(onCreateTestFromScenario).toHaveBeenCalledWith('scenario-new-run')
  })

  it('creates a scenario with explicit staged load segments', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Staged Checkout Scenario')
    })

    await clickWizardStep('Run Settings')

    const enableStagesButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Enable Stages'))
    expect(enableStagesButton).toBeDefined()
    if (!enableStagesButton) return

    await act(async () => {
      enableStagesButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Peak staged VUsers: 10')
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Staged Checkout Scenario',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        vusers: 10,
        stages: [
          { name: 'Warmup', type: 'ramp_up', duration: '24s', target_vusers: 10 },
          { name: 'Steady', type: 'hold', duration: '24s', target_vusers: 10 },
          { name: 'Cooldown', type: 'ramp_down', duration: '12s', target_vusers: 1 },
        ],
      })],
    }))
  })

  it('blocks saving when stair-step stages exceed scenario duration', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Blocked Stage Scenario')
    })

    await clickWizardStep('Run Settings')
    await clickButton('Enable Stages')

    let durationField = getDurationFieldByLabel(container, 'Duration')
    expect(durationField).not.toBeNull()
    if (!durationField) return

    const durationInput = durationField.input
    await act(async () => {
      setInputValue(durationInput, '30')
    })
    await flushPromises()

    durationField = getDurationFieldByLabel(container, 'Duration')
    expect(durationField).not.toBeNull()
    if (!durationField) return

    const durationUnitSelect = durationField.select
    await act(async () => {
      setSelectValue(durationUnitSelect, 's')
    })

    expect(container.textContent).toContain('longer than')
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario')) as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(true)
  })

  it('persists once-only init and teardown scope from the scenario form', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    const initializationSelect = Array.from(container.querySelectorAll('select')).find(select => select.textContent?.includes('Init/teardown once per group')) as HTMLSelectElement | undefined
    expect(nameInput).toBeDefined()
    expect(initializationSelect).toBeDefined()
    if (!nameInput || !initializationSelect) return

    await act(async () => {
      setInputValue(nameInput, 'Once Setup Scenario')
      setSelectValue(initializationSelect, 'at_once')
    })

    expect(initializationSelect.value).toBe('at_once')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Once Setup Scenario',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        initialization: 'at_once',
      })],
    }))
  })

  it('renders scenarios as read-only when manage_tests is denied', async () => {
    const onSelectTest = vi.fn()
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: false,
      authenticated: true,
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: '',
          operations: [{ id: 'manage_tests', label: 'Manage tests', allowed: false }],
        },
      ],
    })
    apiMock.listScenarios.mockResolvedValueOnce({
      scenarios: [{
        id: 'scenario-1',
        name: 'Read Only Checkout',
        duration: '5m',
        assignment: { mode: 'auto' },
        scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Read Only Checkout')
    expect(container.textContent).not.toContain('+ New Scenario')
    expect(container.textContent).not.toContain('Duplicate')
    expect(container.textContent).not.toContain('Delete')
    expect(container.textContent).toContain('Read-only')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const prototype = Object.getPrototypeOf(select) as HTMLSelectElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function getDurationFieldByLabel(container: HTMLElement, label: string) {
  const field = Array.from(container.querySelectorAll('.duration-field')).find(item => (
    item.querySelector('.duration-field-label')?.textContent === label
  ))
  if (!field) return null
  return {
    input: field.querySelector('input') as HTMLInputElement,
    select: field.querySelector('select') as HTMLSelectElement,
  }
}
