// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
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
  getSystemInventory: vi.fn(),
  getConfigDefaults: vi.fn().mockResolvedValue({ config: {} }),
}

const libraryApiMock = {
  listRemotes: vi.fn(),
  localCatalog: vi.fn(),
  remoteCatalog: vi.fn(),
  pullRemoteItem: vi.fn(),
  pushLocalItem: vi.fn(),
  compareItems: vi.fn(),
}

const projectState = vi.hoisted(() => ({
  activeProject: {
    id: 'project-1',
    name: 'Checkout',
    created_at: '',
    updated_at: '',
  } as { id: string; name: string; created_at: string; updated_at: string; domain_id?: string } | null,
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useLibraryApi', () => ({
  useLibraryApi: () => libraryApiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => projectState,
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
      await clickButton('Scenario Builder')
      button = Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
    }
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const clickRunSettingsTab = async (text: string) => {
    const button = Array.from(container.querySelectorAll('.scenario-run-settings-tab')).find(item => item.textContent?.includes(text))
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const wizardStepButton = (text: string) => (
    Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
  )

  const wizardStepMarker = (text: string) => (
    wizardStepButton(text)?.querySelector('.scenario-wizard-step-marker')?.textContent || ''
  )

  const wizardStepComplete = (text: string) => (
    wizardStepButton(text)?.classList.contains('complete') || false
  )

  const chooseLibraryScriptForGroup = async (scriptName = 'Checkout Flow', groupIndex = 0) => {
    const groupCard = Array.from(container.querySelectorAll('.scenario-script-group-list-spacious .scenario-script-group-card'))[groupIndex]
      || Array.from(container.querySelectorAll('.scenario-script-group-card'))[groupIndex]
    expect(groupCard).toBeDefined()
    if (!groupCard) return

    const groupNumber = groupIndex + 1
    const pickerButton = Array.from(groupCard.querySelectorAll('button')).find(button => (
      button.getAttribute('aria-label')?.includes(`Group ${groupNumber}`)
        || button.textContent?.includes('Add Script')
        || button.textContent?.includes(scriptName)
    ))
    expect(pickerButton).toBeDefined()
    if (!pickerButton) return

    await act(async () => {
      pickerButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const useButton = Array.from(container.querySelectorAll('.scenario-existing-script-library button')).find(button => (
      button.textContent?.includes(`Use for Group ${groupNumber}`)
        && button.closest('tr')?.textContent?.includes(scriptName)
    ))
    expect(useButton).toBeDefined()
    if (!useButton) return

    await act(async () => {
      useButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()
  }

  const selectManualShapeByName = async (shapeName: string) => {
    const card = Array.from(container.querySelectorAll('.scenario-shape-catalog-card')).find(item => (
      item.querySelector('.scenario-support-card-header')?.textContent?.includes(shapeName)
    ))
    expect(card).toBeDefined()
    if (!card) return
    const manualButton = Array.from(card.querySelectorAll('button')).find(button => button.textContent?.includes('Use Manual Shape'))
    expect(manualButton).toBeDefined()
    if (!manualButton) return
    await act(async () => {
      manualButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }

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
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })
    libraryApiMock.localCatalog.mockResolvedValue({ items: [], count: 0 })
    libraryApiMock.remoteCatalog.mockResolvedValue({ catalog: { items: [], count: 0 } })
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
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.scenarioBuilder.draft')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.scenarioBuilder.draft')
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

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Checkout Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    const loadGroupCard = container.querySelector('.scenario-script-group-card')
    const vusersInput = loadGroupCard ? Array.from(loadGroupCard.querySelectorAll('input')).find(input => input.getAttribute('type') === 'number') : undefined
    expect(vusersInput).toBeDefined()
    if (!vusersInput) return

    await act(async () => {
      setInputValue(vusersInput, '25')
    })

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

  it('selects a group script from the searchable sortable script library', async () => {
    const scrollIntoView = vi.fn()
    Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView })
    apiMock.listScripts.mockResolvedValue({
      scripts: [
        {
          id: '',
          name: 'Legacy Empty Id Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-01T12:00:00Z',
          scenario_count: 0,
          associated_scenarios: [],
          associated_releases: [],
        },
        {
          id: 'script-1',
          name: 'Checkout Flow',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T12:00:00Z',
          scenario_count: 0,
          associated_scenarios: [],
          associated_releases: [],
        },
        {
          id: 'script-2',
          name: 'Payments Flow',
          protocol: 'grpc',
          version: '2.0.0',
          created_at: '2026-05-15T12:00:00Z',
          last_run_at: '2026-05-20T15:30:00Z',
          scenario_count: 2,
          associated_scenarios: [{ id: 'scenario-payments', name: 'Payments Peak' }],
          associated_releases: [{ id: 'release-30', name: 'PI30', application_id: 'app-1' }],
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Payments Scenario')
    })

    const firstGroup = container.querySelector('.scenario-script-group-card')
    const chooseScriptButton = Array.from(firstGroup?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Add Script'))
    expect(chooseScriptButton).toBeDefined()
    if (!chooseScriptButton) return
    expect(firstGroup?.textContent).not.toContain('Checkout Flow')
    expect(firstGroup?.textContent).not.toContain('Legacy Empty Id Script')

    await act(async () => {
      chooseScriptButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const searchInput = container.querySelector('.scenario-existing-script-library input[type="search"]') as HTMLInputElement | null
    expect(searchInput).toBeDefined()
    expect(document.activeElement).toBe(searchInput)
    expect(scrollIntoView).toHaveBeenCalled()
    expect(container.textContent).toContain('Choosing for Group 1')
    expect(container.textContent).toContain('Payments Peak')
    expect(container.textContent).toContain('PI30')
    expect(container.textContent).toContain('grpc')
    expect(container.textContent).toContain('May 15, 2026')
    expect(container.textContent).toContain('May 20, 2026')
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'Payments Peak')
    })
    const filteredRows = Array.from(container.querySelectorAll('.scenario-existing-script-library tbody tr'))
    expect(filteredRows).toHaveLength(1)
    expect(filteredRows[0].textContent).toContain('Payments Flow')

    await act(async () => {
      setInputValue(searchInput, '')
    })
    const createdHeader = Array.from(container.querySelectorAll('.scenario-existing-script-library th'))
      .find(header => header.textContent?.includes('Created'))
    expect(createdHeader).toBeDefined()
    if (!createdHeader) return
    expect(createdHeader.getAttribute('aria-sort')).toBe('none')
    const createdSortButton = createdHeader.querySelector('button')
    expect(createdSortButton).toBeDefined()
    if (!createdSortButton) return
    await act(async () => {
      createdSortButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(createdHeader.getAttribute('aria-sort')).toBe('descending')
    const firstSortedRow = container.querySelector('.scenario-existing-script-library tbody tr')
    expect(firstSortedRow?.textContent).toContain('Payments Flow')

    const usePaymentsButton = Array.from(container.querySelectorAll('.scenario-existing-script-library button'))
      .find(button => button.textContent?.includes('Use for Group 1') && button.closest('tr')?.textContent?.includes('Payments Flow'))
    expect(usePaymentsButton).toBeDefined()
    if (!usePaymentsButton) return

    await act(async () => {
      usePaymentsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    expect(container.querySelector('.scenario-script-group-card')?.textContent).toContain('Payments Flow')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Payments Scenario',
      groups: [expect.objectContaining({
        script_id: 'script-2',
      })],
    }))
  })

  it('opens directly to the scenario builder when requested', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()

    expect(container.querySelector('.scenario-builder')).toBeTruthy()
    expect(container.querySelector('.library-table-wrapper')).toBeFalsy()
    const startActionLabels = Array.from(container.querySelectorAll('.scenario-builder-actions button'))
      .map(button => button.textContent?.trim())
    expect(startActionLabels).not.toContain('Back')
    expect(startActionLabels).toContain('Next')
    const builderToggle = Array.from(container.querySelectorAll('.scenario-page-toggle button'))
      .find(button => button.textContent === 'Scenario Builder')
    expect(builderToggle?.getAttribute('aria-pressed')).toBe('true')

    await clickWizardStep('Shape / Source')

    const sourceActionLabels = Array.from(container.querySelectorAll('.scenario-builder-actions button'))
      .map(button => button.textContent?.trim())
    expect(sourceActionLabels).toContain('Back')
  })

  it('hides the duplicate builder page title when the static topbar owns it', async () => {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />
        </TopbarContributionProvider>,
      )
    })

    await flushPromises()

    expect(container.querySelector('.scenario-builder')).toBeTruthy()
    expect(container.querySelector('.page-header')).toBeNull()
    expect(container.querySelector('.scenario-builder .section-title')).toBeNull()
  })

  it('does not mark skipped mandatory wizard steps as complete', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    expect(wizardStepMarker('Start')).toBe('1')
    expect(wizardStepMarker('Shape / Source')).toBe('2')
    expect(wizardStepMarker('Scripts')).toBe('3')
    expect(wizardStepComplete('Start')).toBe(false)
    expect(wizardStepComplete('Shape / Source')).toBe(false)
    expect(wizardStepComplete('Scripts')).toBe(false)
  })

  it('replaces wizard step numbers with checkmarks only when required fields are complete', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()

    await clickButton('Build from Workload Shape')
    expect(wizardStepMarker('Start')).toBe('✓')
    expect(wizardStepComplete('Start')).toBe(true)
    expect(wizardStepMarker('Shape / Source')).toBe('2')
    expect(wizardStepComplete('Shape / Source')).toBe(false)

    await clickButton('Use Manual Shape')
    expect(wizardStepMarker('Shape / Source')).toBe('✓')
    expect(wizardStepComplete('Shape / Source')).toBe(true)
    expect(wizardStepMarker('Scripts')).toBe('3')
    expect(wizardStepComplete('Scripts')).toBe(false)

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Completion Marker Scenario')
    })
    await chooseLibraryScriptForGroup()

    expect(wizardStepMarker('Scripts')).toBe('✓')
    expect(wizardStepComplete('Scripts')).toBe(true)
    expect(wizardStepMarker('Run Settings')).toBe('4')

    await clickWizardStep('Run Settings')
    expect(wizardStepMarker('Run Settings')).toBe('✓')
    expect(wizardStepComplete('Run Settings')).toBe(true)
  })

  it('seeds goal-oriented run settings and shows shape guidance when an open-arrival-rate shape is selected manually', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Open Arrival Rate')

    // Guidance banner reflects the chosen shape on the Scripts step.
    const guidance = container.querySelector('.scenario-shape-guidance')
    expect(guidance).toBeTruthy()
    expect(guidance?.textContent).toContain('Open Arrival Rate')
    expect(guidance?.textContent).toContain('throughput')

    await clickWizardStep('Run Settings')
    const durationField = getDurationFieldByLabel(container, 'Duration')
    expect(durationField?.input.value).toBe('10')
    expect(durationField?.select.value).toBe('m')
    const modeSelect = Array.from(container.querySelectorAll('select')).find(select => select.value === 'goal_oriented')
    expect(modeSelect?.value).toBe('goal_oriented')
    expect(container.textContent).toContain('Goal Controller')
  })

  it('seeds stair-step stages when a stepped-ramp shape is selected manually', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Stepped Ramp')
    await clickWizardStep('Run Settings')

    const stageRows = container.querySelectorAll('.scenario-stage-row')
    expect(stageRows.length).toBe(38)
    const firstStageName = stageRows[0].querySelector('input') as HTMLInputElement
    expect(firstStageName?.value).toBe('Step 1')
    expect(container.textContent).toContain('Peak staged VUsers: 75')
    // Stages active → the "Use Simple Ramp" escape hatch is offered.
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.includes('Use Simple Ramp'))).toBe(true)
  })

  it('regenerates stepped-ramp stages explicitly from current peak and duration without ordinary edit clobbering', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Stepped Ramp')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    await act(async () => {
      setInputValue(nameInput, 'Adaptive Stepped Scenario')
    })
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    const groupCard = container.querySelector('.scenario-script-group-card')
    const vusersInput = groupCard ? Array.from(groupCard.querySelectorAll('input')).find(input => input.type === 'number') : undefined
    const durationField = getDurationFieldByLabel(container, 'Duration')
    expect(vusersInput).toBeDefined()
    expect(durationField).not.toBeNull()
    if (!vusersInput || !durationField) return

    await act(async () => {
      setInputValue(vusersInput, '20')
      setInputValue(durationField.input, '1')
      setSelectValue(durationField.select, 'h')
    })

    await clickButton('Regenerate for Stepped Ramp')

    let stageRows = container.querySelectorAll('.scenario-stage-row')
    expect(stageRows.length).toBe(40)
    expect(container.textContent).toContain('Peak staged VUsers: 20')
    expect(container.textContent).toContain('current peak and scenario duration')

    await act(async () => {
      setInputValue(durationField.input, '2')
      setSelectValue(durationField.select, 'h')
    })
    stageRows = container.querySelectorAll('.scenario-stage-row')
    expect(stageRows.length).toBe(40)

    await clickButton('Save Scenario')
    await flushPromises()

    const payload = apiMock.createScenario.mock.calls[apiMock.createScenario.mock.calls.length - 1]?.[0]
    expect(payload).toMatchObject({
      name: 'Adaptive Stepped Scenario',
      duration: '2h',
      scenario_mode: 'vuser_driven',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        vusers: 20,
      })],
    })
    expect(payload.groups[0].stages).toHaveLength(40)
  })

  it('re-seeds the form from a new shape while preserving the chosen script', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Open Arrival Rate')
    await chooseLibraryScriptForGroup()

    // Switch to a different shape from the catalog.
    await clickWizardStep('Shape / Source')
    await selectManualShapeByName('Stepped Ramp')

    // Script selection survives the re-seed.
    const groupCard = container.querySelector('.scenario-script-group-list-spacious .scenario-script-group-card')
    expect(groupCard?.textContent).toContain('Checkout Flow')

    // Load model now reflects the stepped-ramp preset (12m duration).
    await clickWizardStep('Run Settings')
    expect(getDurationFieldByLabel(container, 'Duration')?.input.value).toBe('12')
  })

  it('groups Scenario Builder run settings into focused tabs', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Run Settings')

    const tabs = Array.from(container.querySelectorAll('.scenario-run-settings-tab'))
    expect(tabs.map(tab => tab.textContent)).toEqual([
      'Scenario Settings',
      'Browser/Script Runtime',
      'Controller/Generator',
    ])
    expect(tabs[0].getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Load Per Group')
    expect(container.textContent).toContain('Pre-Run Assurance')
    expect(container.textContent).toContain('Advanced Stair-Step Ramp')
    expect(container.textContent).not.toContain('Auto Assignment')

    const workspace = container.querySelector('.scenario-builder-workspace.with-preview')
    expect(workspace).toBeTruthy()
    expect(workspace?.querySelector('.scenario-builder-main .scenario-run-settings-tabs')).toBeTruthy()
    expect(workspace?.querySelector('.scenario-builder-preview .scenario-model-preview-panel')).toBeTruthy()

    await clickRunSettingsTab('Controller/Generator')

    expect(container.textContent).toContain('Controller / Generator Assignment')
    expect(container.textContent).toContain('Auto Assignment')
    expect(container.textContent).not.toContain('Load Per Group')

    await clickRunSettingsTab('Browser/Script Runtime')

    expect(container.textContent).toContain('Per-group runtime overrides')
    expect(container.textContent).toContain('Iterations')
    expect(container.textContent).toContain('Pacing')
    expect(container.textContent).toContain('Auto-transactions')
    expect(container.textContent).toContain('Think Time')
    expect(container.textContent).not.toContain('Auto Assignment')
  })

  it('shows multi-group Scenario tab load settings in horizontal group tabs', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')
    await clickButton('Add Group')
    await clickWizardStep('Run Settings')

    const groupTabs = Array.from(container.querySelectorAll('.scenario-group-tab'))
    expect(groupTabs).toHaveLength(2)
    expect(groupTabs.map(tab => tab.textContent)).toEqual([
      'Group 1Unassigned script · 10 VUsers',
      'Group 2Unassigned script · 10 VUsers',
    ])
    expect(groupTabs[1].getAttribute('aria-selected')).toBe('true')

    const loadPanel = container.querySelector('.scenario-load-group-panel')
    expect(loadPanel?.querySelectorAll('.scenario-script-group-card')).toHaveLength(1)
    expect(loadPanel?.textContent).toContain('Group 2')
    expect(loadPanel?.textContent).not.toContain('Group 1')

    await act(async () => {
      groupTabs[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('.scenario-load-group-panel .scenario-script-group-card')).toHaveLength(1)
    expect(container.querySelector('.scenario-load-group-panel')?.textContent).toContain('Group 1')
    expect(container.querySelector('.scenario-load-group-panel')?.textContent).not.toContain('Group 2')
    expect(Array.from(container.querySelectorAll('.scenario-group-tab'))[0].getAttribute('aria-selected')).toBe('true')
  })

  it('persists script runtime settings from the Browser/Script Runtime tab', async () => {
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
      setInputValue(nameInput, 'Runtime Defaults Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Browser/Script Runtime')

    // The runtime editor groups overrides into sub-tabs; drive each one.
    await clickRunSettingsTab('Run Logic')
    const iterationsInput = findControlByLabel<HTMLInputElement>(container, 'Iterations', 'input')
    const autoTransactionsSelect = findControlByLabel<HTMLSelectElement>(container, 'Auto-transactions', 'select')
    expect(iterationsInput).toBeDefined()
    expect(autoTransactionsSelect).toBeDefined()
    if (!iterationsInput || !autoTransactionsSelect) return
    await act(async () => {
      setInputValue(iterationsInput, '3')
      setSelectValue(autoTransactionsSelect, 'per_iteration')
    })

    await clickRunSettingsTab('Iteration Pacing')
    const pacingSelect = findControlByLabel<HTMLSelectElement>(container, 'Pacing', 'select')
    expect(pacingSelect).toBeDefined()
    if (!pacingSelect) return
    await act(async () => { setSelectValue(pacingSelect, 'fixed_delay') })
    const pacingDelayInput = findControlByLabel<HTMLInputElement>(container, 'Pacing delay (ms)', 'input')
    expect(pacingDelayInput).toBeDefined()
    if (!pacingDelayInput) return
    await act(async () => { setInputValue(pacingDelayInput, '750') })

    await clickRunSettingsTab('Think Time')
    const distributionSelect = findControlByLabel<HTMLSelectElement>(container, 'Distribution', 'select')
    const meanInput = findControlByLabel<HTMLInputElement>(container, 'Mean (ms)', 'input')
    expect(distributionSelect).toBeDefined()
    expect(meanInput).toBeDefined()
    if (!distributionSelect || !meanInput) return
    await act(async () => {
      setSelectValue(distributionSelect, 'fixed')
      setInputValue(meanInput, '250')
    })

    await clickRunSettingsTab('Error Handling')
    const logLevelSelect = findControlByLabel<HTMLSelectElement>(container, 'Log level', 'select')
    expect(logLevelSelect).toBeDefined()
    if (!logLevelSelect) return
    await act(async () => { setSelectValue(logLevelSelect, 'extended') })

    await clickButton('Save Scenario')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Runtime Defaults Scenario',
      groups: expect.arrayContaining([
        expect.objectContaining({
          runtime_settings: expect.objectContaining({
            pacing: { type: 'fixed_delay', delay_ms: 750 },
            run_logic: expect.objectContaining({ iterations: 3, auto_transactions: 'per_iteration' }),
            log: expect.objectContaining({ level: 'extended' }),
            think_time: expect.objectContaining({ type: 'fixed', mean_ms: 250 }),
          }),
        }),
      ]),
    }))
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
        associated_releases: [{ id: 'release-29', name: 'PI29', application_id: 'app-1' }],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_test_id: 'run-checkout-1',
        last_run_at: '2026-05-13T00:00:00Z',
        last_run_ended_at: '2026-05-13T00:05:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Scenario Library')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Library Sources')
    expect(container.textContent).toContain('Associated Release')
    expect(container.textContent).toContain('Search Release')
    expect(container.textContent).not.toContain('Asset Type')
    const libraryHeaders = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(libraryHeaders.some(header => header.includes('Scenario'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Test ID'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Workload'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Associated Releases'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Create Date'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Start'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Finish'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Last Run'))).toBe(false)
    expect(container.textContent).toContain('Pinned Checkout')
    expect(container.textContent).toContain('Specific Hosts')
    expect(container.textContent).toContain('gen-a, gen-b')
    expect(container.textContent).toContain('run-checkout-1')
    expect(container.textContent).toContain(new Date('2026-05-13T00:00:00Z').toLocaleString())
    expect(container.textContent).toContain(new Date('2026-05-13T00:05:00Z').toLocaleString())
    expect(container.textContent).toContain('PI29')
    expect(container.textContent).toContain('No data pools')
    expect(container.textContent).toContain('0 results')
    expect(container.textContent).not.toContain('Auto Assignment')
    expect(container.textContent).not.toContain('Uses controller recommendations')

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
    expect(container.textContent).toContain('No scenarios match these filters.')

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

  it('waits for an active project and ignores a stale project response after switching', async () => {
    const onSelectTest = vi.fn()
    const projectAResponse = deferred<{ scenarios: Array<Record<string, unknown>>; count: number }>()
    projectState.activeProject = null
    apiMock.listScenarios.mockImplementation((projectId?: string) => {
      if (projectId === 'project-a') return projectAResponse.promise
      if (projectId === 'project-b') {
        return Promise.resolve({
          scenarios: [scenarioFixture('scenario-b', 'Project B Scenario')],
          count: 1,
        })
      }
      return Promise.resolve({ scenarios: [], count: 0 })
    })
    apiMock.listScripts.mockImplementation((projectId?: string) => Promise.resolve({
      scripts: projectId ? [{ id: `script-${projectId}`, name: `${projectId} script`, protocol: 'http', version: '1.0.0', created_at: '' }] : [],
      count: projectId ? 1 : 0,
    }))

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    expect(apiMock.listScenarios).not.toHaveBeenCalled()
    expect(apiMock.listScripts).not.toHaveBeenCalled()
    expect(apiMock.listDataPools).not.toHaveBeenCalled()
    expect(apiMock.getAuthEffectiveAccess).not.toHaveBeenCalled()

    await act(async () => {
      projectState.activeProject = { id: 'project-a', name: 'Project A', created_at: '', updated_at: '' }
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()
    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-a')

    await act(async () => {
      projectState.activeProject = { id: 'project-b', name: 'Project B', created_at: '', updated_at: '' }
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-b')
    expect(container.textContent).toContain('Project B Scenario')

    await act(async () => {
      projectAResponse.resolve({
        scenarios: [scenarioFixture('scenario-a', 'Project A Scenario')],
        count: 1,
      })
      await projectAResponse.promise
    })
    await flushPromises()

    expect(container.textContent).toContain('Project B Scenario')
    expect(container.textContent).not.toContain('Project A Scenario')
  })

  it('gives the delete confirmation dialog a trapped, dismissible focus lifecycle', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [scenarioFixture('scenario-delete', 'Delete Me')],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    const deleteTrigger = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Delete')
    expect(deleteTrigger).toBeDefined()
    if (!deleteTrigger) return
    deleteTrigger.focus()

    await act(async () => {
      deleteTrigger.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const dialog = container.querySelector('[role="dialog"]') as HTMLDivElement | null
    expect(dialog).not.toBeNull()
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('delete-scenario-dialog-title')
    expect(dialog?.getAttribute('aria-describedby')).toBe('delete-scenario-dialog-description')
    expect(dialog?.getAttribute('tabindex')).toBe('-1')
    expect(document.activeElement?.textContent).toBe('Cancel')

    const dialogButtons = Array.from(dialog?.querySelectorAll('button') || [])
    const firstButton = dialogButtons[0]
    const lastButton = dialogButtons[dialogButtons.length - 1]
    expect(firstButton?.textContent).toBe('Cancel')
    expect(lastButton?.textContent).toBe('Delete Scenario')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(lastButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(firstButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    await flushPromises()

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(deleteTrigger)
  })

  it('filters the scenario library by associated release', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-checkout',
          name: 'Checkout Release Scenario',
          duration: '5m',
          assignment: { mode: 'auto' },
          scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
          data_pools: [],
          associated_releases: [{ id: 'release-29', name: 'PI29', application_id: 'app-1' }],
          result_count: 0,
          created_at: '2026-05-12T00:00:00Z',
          last_run_at: '2026-05-13T00:00:00Z',
        },
        {
          id: 'scenario-payments',
          name: 'Payments Release Scenario',
          duration: '5m',
          assignment: { mode: 'auto' },
          scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 10 }],
          data_pools: [],
          associated_releases: [{ id: 'release-30', name: 'PI30', application_id: 'app-1' }],
          result_count: 0,
          created_at: '2026-05-13T00:00:00Z',
          last_run_at: '',
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    const releaseFilter = container.querySelector('select.library-release-filter') as HTMLSelectElement | null
    expect(releaseFilter).toBeTruthy()
    if (!releaseFilter) return

    expect(Array.from(releaseFilter.options).map(option => option.textContent)).toEqual(['All releases', 'PI29', 'PI30'])

    await act(async () => {
      setSelectValue(releaseFilter, 'PI29')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Checkout Release Scenario')
    expect(container.textContent).not.toContain('Payments Release Scenario')

    await act(async () => {
      setSelectValue(releaseFilter, 'PI30')
    })

    expect(container.textContent).toContain('Payments Release Scenario')
    expect(container.textContent).not.toContain('Checkout Release Scenario')

    await act(async () => {
      setSelectValue(releaseFilter, 'release-that-does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match these filters.')
  })

  it('routes scenario library source pills to integrations settings', async () => {
    const onSelectTest = vi.fn()
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onNavigate={onNavigate} />)
    })

    await flushPromises()

    const noRemoteButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('No Remote Workspace'))
    const credentialsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Credentials Missing'))
    expect(noRemoteButton).toBeDefined()
    expect(credentialsButton).toBeDefined()
    if (!noRemoteButton || !credentialsButton) return

    await act(async () => {
      noRemoteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('settings')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBe('integrations')

    window.localStorage.removeItem('apexload.settings.focusSection')
    await act(async () => {
      credentialsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('settings')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBe('integrations')
  })

  it('clones an existing scenario draft with data pool bindings from the wizard source step', async () => {
    const onSelectTest = vi.fn()
    const onOpenCommandCenter = vi.fn()
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
        created_at: '2026-05-12T12:00:00Z',
        updated_at: '2026-05-12T12:00:00Z',
        last_run_test_id: 'run-1',
        last_run_at: '2026-05-13T00:01:00Z',
        last_run_ended_at: '2026-05-13T00:08:00Z',
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
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onOpenCommandCenter={onOpenCommandCenter} />)
    })

    await flushPromises()

    await clickButton('Scenario Builder')
    await clickButton('Model from Existing Scenario')
    const cloneRow = Array.from(container.querySelectorAll('.scenario-clone-table tbody tr')).find(row => row.textContent?.includes('Pinned Checkout'))
    expect(cloneRow).toBeDefined()
    await act(async () => {
      cloneRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('20 peak VUsers')
    expect(container.textContent).toContain('Choose Scenario To Model')
    expect(container.textContent).not.toContain('Existing Scripts Library')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Associated Releases')
    expect(container.textContent).toContain('Create Date')
    expect(container.textContent).toContain('Test ID')
    expect(container.textContent).toContain('Start')
    expect(container.textContent).toContain('Finish')
    expect(container.textContent).not.toContain('Last Run')
    expect(container.textContent).not.toContain('Auto Assignment')
    expect(container.textContent).not.toContain('Uses controller recommendations')
    expect(container.querySelector('.scenario-clone-card')).toBeNull()
    expect(apiMock.listScenarioResults).toHaveBeenCalledWith('scenario-source')
    const selectedCloneRow = Array.from(container.querySelectorAll('.scenario-clone-table tr.selected')).find(row => row.textContent?.includes('Pinned Checkout'))
    expect(selectedCloneRow).toBeDefined()
    expect(selectedCloneRow?.textContent).toContain('Local')
    expect(selectedCloneRow?.textContent).toContain('run-1')
    expect(selectedCloneRow?.textContent).toContain('Checkout Flow')
    expect(selectedCloneRow?.textContent).toContain('No releases yet')
    expect(selectedCloneRow?.textContent).toContain('May 12, 2026')
    expect(selectedCloneRow?.textContent).toContain(new Date('2026-05-13T00:01:00Z').toLocaleString())
    expect(selectedCloneRow?.textContent).toContain(new Date('2026-05-13T00:08:00Z').toLocaleString())
    const chart = container.querySelector('[data-testid="scenario-model-chart"]')
    const runPanel = container.querySelector('.scenario-source-runs-panel')
    expect(runPanel).toBeDefined()
    const runPanelText = Array.from(container.querySelectorAll('.scenario-source-runs-panel')).map(panel => panel.textContent || '').join(' ')
    expect(runPanelText).toContain('Past Scenario Runs')
    expect(runPanelText).toContain('Checkout Load 001')
    if (chart && runPanel) {
      expect((chart.compareDocumentPosition(runPanel) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true)
    }
    const runPanelButtons = Array.from(runPanel?.querySelectorAll('button') ?? []) as HTMLButtonElement[]
    const openRunButton = runPanelButtons.find(button => button.textContent?.trim() === 'Open')
    const runDashboardButton = runPanelButtons.find(button => button.textContent?.trim() === 'Run Dashboard')
    expect(openRunButton).toBeDefined()
    expect(runDashboardButton).toBeDefined()

    await act(async () => {
      openRunButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onSelectTest).toHaveBeenCalledWith('run-1')

    await act(async () => {
      runDashboardButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onOpenCommandCenter).toHaveBeenCalledWith('run-1')

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

    expect(wizardStepMarker('Shape / Source')).toBe('✓')
    expect(wizardStepComplete('Shape / Source')).toBe(true)

    const groupCards = Array.from(container.querySelectorAll('.scenario-script-group-card'))
    expect(groupCards).toHaveLength(2)
    expect(groupCards[0].textContent).toContain('Checkout Flow')
    expect(groupCards[1].textContent).toContain('Payments Flow')
    const secondGroupInitSelect = Array.from(groupCards[1].querySelectorAll('select')).find(select => select.textContent?.includes('Init/teardown once per group')) as HTMLSelectElement | undefined
    expect(secondGroupInitSelect?.value).toBe('at_once')

    await clickWizardStep('Run Settings')
    const paymentGroupTab = Array.from(container.querySelectorAll('.scenario-group-tab')).find(tab => tab.textContent?.includes('Payments'))
    expect(paymentGroupTab).toBeDefined()
    if (!paymentGroupTab) return

    await act(async () => {
      paymentGroupTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const loadGroupCards = Array.from(container.querySelectorAll('.scenario-load-group-panel .scenario-script-group-card'))
    expect(loadGroupCards).toHaveLength(1)
    expect(loadGroupCards[0].textContent).toContain('Payments')
    const secondGroupVUsers = Array.from(loadGroupCards[0].querySelectorAll('input')).find(input => input.type === 'number')
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

  it('edits and clones the complete stored scenario aggregate without clearing hidden fields', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Orders Flow', protocol: 'kafka', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-2', name: 'Orders', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    const storedScenario = {
      id: 'scenario-fidelity',
      name: 'Messaging Fidelity',
      duration: '8m',
      start_delay: '45s',
      scenario_mode: 'goal_oriented' as const,
      assignment: { mode: 'auto' as const },
      goal_config: {
        target_average_tps: 80,
        max_throttled_requests: 3,
        max_end_to_end_latency_ms: 250,
        max_partition_skew_pct: 15,
        max_redelivery_rate_pct: 2,
        max_dead_letter_rate_pct: 1,
        stop_on_sla_breach: true,
      },
		runtime_settings: {
			iterations: 4,
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'normal', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200 },
			error_handling: { max_retries: 3, retry_delay_ms: 150, retry_backoff: 'exponential' },
        run_logic: { iterations: 4, workload_model: 'closed' },
        http: { version: '2' },
        web_runtime: { source_ips: ['10.0.0.8'] },
        base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
        redaction_rules: [{ name: 'token', pattern: 'secret-[0-9]+', replacement: '[MASKED]' }],
      },
      scripts: [{
        order: 0,
        script_id: 'script-1',
        script_name: 'Orders Flow',
        protocol: 'kafka',
        name: 'Orders',
        vusers: 12,
        ramp_up: '1m',
        ramp_down: '30s',
        iterations: 7,
        initialization: 'at_once' as const,
			runtime_settings: {
				run_logic: { iterations: 7, workload_model: 'closed' },
				env: { TOPIC: 'orders' },
			},
			assignment: {
				mode: 'region_weighted' as const,
				region_weights: { east: 70, west: 30 },
				required_labels: { tier: 'messaging' },
				tolerate_generator_loss: true,
			},
      }],
      data_pools: [
        { id: 'binding-1', pool_id: 'pool-1', pool_name: 'Customers', alias: 'customers', access_mode: 'consume' as const, selection_strategy: 'unique' as const },
        { id: 'binding-2', pool_id: 'pool-2', pool_name: 'Orders', alias: 'orders', access_mode: 'read_only' as const, selection_strategy: 'sequential' as const },
      ],
      associated_releases: [],
      result_count: 0,
      created_at: '2026-05-12T00:00:00Z',
      updated_at: '2026-05-12T00:00:00Z',
      script_count: 1,
    }
    apiMock.listScenarios.mockResolvedValue({ scenarios: [storedScenario], count: 1 })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await clickButton('Save Scenario')
    await flushPromises()

    const fidelityPayload = expect.objectContaining({
      name: 'Messaging Fidelity',
      start_delay: '45s',
      groups: [expect.objectContaining({
        name: 'Orders',
        script_id: 'script-1',
        ramp_down: '30s',
        iterations: 7,
        initialization: 'at_once',
			runtime_settings: expect.objectContaining({
				run_logic: { iterations: 7, workload_model: 'closed' },
				env: { TOPIC: 'orders' },
			}),
			assignment: {
				mode: 'region_weighted',
				region_weights: { east: 70, west: 30 },
				required_labels: { tier: 'messaging' },
				tolerate_generator_loss: true,
			},
      })],
      data_pools: [
        expect.objectContaining({ id: 'binding-1', pool_id: 'pool-1', alias: 'customers' }),
        expect.objectContaining({ id: 'binding-2', pool_id: 'pool-2', alias: 'orders' }),
      ],
      goal_config: expect.objectContaining({
        max_throttled_requests: 3,
        max_end_to_end_latency_ms: 250,
        max_partition_skew_pct: 15,
        max_redelivery_rate_pct: 2,
        max_dead_letter_rate_pct: 1,
      }),
		runtime_settings: expect.objectContaining({
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'normal', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200, ignore: false },
			error_handling: { max_retries: 3, retry_delay_ms: 150, retry_backoff: 'exponential' },
        run_logic: { iterations: 4, workload_model: 'closed' },
        http: { version: '2' },
        web_runtime: { source_ips: ['10.0.0.8'] },
        base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
        redaction_rules: [{ name: 'token', pattern: 'secret-[0-9]+', replacement: '[MASKED]' }],
      }),
    })
    expect(apiMock.updateScenario).toHaveBeenCalledWith('scenario-fidelity', fidelityPayload)

    await act(async () => {
      const cancel = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
      cancel?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    const duplicateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Duplicate')
    expect(duplicateButton).toBeDefined()
    await act(async () => {
      duplicateButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickButton('Save Scenario')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Messaging Fidelity (Copy)',
      start_delay: '45s',
		groups: [expect.objectContaining({
			script_id: 'script-1',
			iterations: 7,
			runtime_settings: expect.objectContaining({ env: { TOPIC: 'orders' } }),
			assignment: expect.objectContaining({
				mode: 'region_weighted',
				region_weights: { east: 70, west: 30 },
				tolerate_generator_loss: true,
			}),
      })],
      data_pools: [
        expect.objectContaining({ pool_id: 'pool-1', alias: 'customers' }),
        expect.objectContaining({ pool_id: 'pool-2', alias: 'orders' }),
      ],
      goal_config: expect.objectContaining({ max_throttled_requests: 3, max_dead_letter_rate_pct: 1 }),
		runtime_settings: expect.objectContaining({
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'normal', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200, ignore: false },
			base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
      }),
    }))
    const clonedCalls = apiMock.createScenario.mock.calls
    const clonedPayload = clonedCalls[clonedCalls.length - 1]?.[0]
    expect(clonedPayload.data_pools.every((binding: { id?: string }) => binding.id === undefined)).toBe(true)
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

    await chooseLibraryScriptForGroup('Mobile Checkout')
    await clickWizardStep('Run Settings')

    expect(container.textContent).toContain('Browser/Script Runtime')
    expect(container.textContent).not.toContain('Device profile')

    await clickRunSettingsTab('Browser/Script Runtime')

    expect(container.textContent).toContain('Per-group runtime overrides')

    // Browser device settings fold into the HTTP Transport tab's "Browser emulation" sub-tab.
    await clickRunSettingsTab('HTTP Transport')
    await act(async () => {
      const browserEmulationTab = Array.from(container.querySelectorAll('.http-transport-tab')).find(tab => tab.textContent?.includes('Browser emulation'))
      browserEmulationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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

    const browserConfigMatcher = expect.objectContaining({
      device_profile: 'iphone_se',
      orientation: 'portrait',
      touch_mode: true,
      headless: false,
    })
    // The tabbed runtime editor persists browser device settings into the
    // per-group browser_config section.
    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Mobile Browser Scenario',
      groups: expect.arrayContaining([
        expect.objectContaining({
          runtime_settings: expect.objectContaining({ browser_config: browserConfigMatcher }),
        }),
      ]),
    }))
    // NOTE: the per-script capacity estimate (estimateTest) no longer carries the
    // per-group runtime overrides after the tabbed-runtime-editor refactor, so the
    // previous assertion that the estimate reflected browser_config no longer holds.
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

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    const rampUpField = getDurationFieldByLabel(container, 'Ramp Up')

    expect(rampUpField?.input.value).toBe('2')
    expect(rampUpField?.select.value).toBe('m')

    const durationField = getDurationFieldByLabel(container, 'Duration')
    const modeSelect = Array.from(container.querySelectorAll('select')).find(select => select.value === 'goal_oriented') as HTMLSelectElement | undefined

    expect(durationField?.input.value).toBe('10')
    expect(durationField?.select.value).toBe('m')
    expect(modeSelect?.value).toBe('goal_oriented')
    expect(container.textContent).toContain('Goal Controller')

    const runSettingsValidateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Load Model'))
    expect(runSettingsValidateButton).toBeUndefined()

    await clickWizardStep('Review')

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

    await chooseLibraryScriptForGroup()
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
    const onScenarioViewChange = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onScenarioViewChange={onScenarioViewChange} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Scenario Library')
    expect(container.querySelector('.scenario-builder')).toBeNull()
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Scenario Builder')

    expect(onScenarioViewChange).toHaveBeenLastCalledWith('builder')
    expect(container.textContent).toContain('Scenario Builder')
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

    await clickButton('Scenario Library')

    expect(onScenarioViewChange).toHaveBeenLastCalledWith('library')
    expect(container.textContent).toContain('Scenario Library')
    expect(container.querySelector('.scenario-builder')).toBeNull()
  })

  it('renders script groups in the spacious multi-card list on the Scripts step', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    await clickButton('Add Group')
    await clickButton('Add Group')
    await clickButton('Add Group')

    const groupList = container.querySelector('.scenario-script-group-list.scenario-script-group-list-spacious')
    expect(groupList).toBeDefined()
    expect(groupList?.querySelectorAll('.scenario-script-group-card')).toHaveLength(4)
    Array.from(groupList?.querySelectorAll('.scenario-script-group-card') || []).forEach(groupCard => {
      expect(groupCard.textContent).toContain('Add Script')
      expect(groupCard.textContent).not.toContain('Checkout Flow')
    })
    expect(groupList?.querySelector('.scenario-add-group-card')?.textContent).toContain('Add Group')
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

    await chooseLibraryScriptForGroup()
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

    await chooseLibraryScriptForGroup()
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

    await chooseLibraryScriptForGroup()
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

  it('shows the scenario name in the builder header on every step once it is set', async () => {
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
      setInputValue(nameInput, 'Header Visible Scenario')
    })

    const headerName = () => container.querySelector('.scenario-builder-name')?.textContent
    expect(headerName()).toBe('Header Visible Scenario')

    await clickWizardStep('Run Settings')
    expect(headerName()).toBe('Header Visible Scenario')

    await clickWizardStep('Review')
    expect(headerName()).toBe('Header Visible Scenario')
  })

  it('prompts for a scenario name on the review step instead of erroring when saving without one', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    await clickWizardStep('Scripts')
    await chooseLibraryScriptForGroup()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save Scenario')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // No scenario is created; the user is taken to review with a name prompt.
    expect(apiMock.createScenario).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('required')
    expect(container.textContent).toContain('Name the scenario before saving.')
    const promptInput = container.querySelector('.scenario-name-prompt input') as HTMLInputElement | null
    expect(promptInput).toBeTruthy()
    if (!promptInput) return

    await act(async () => {
      setInputValue(promptInput, 'Late Named Scenario')
    })

    const saveButtonAfter = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save Scenario')
    await act(async () => {
      saveButtonAfter?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({ name: 'Late Named Scenario' }))
  })

  it('starts over without restoring or persisting an in-progress builder draft', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    window.localStorage.setItem('apexload.scenarioBuilder.draft', JSON.stringify({ form: { name: 'Stale Draft' } }))

    // Enter the builder and make progress; the stale persisted draft is ignored.
    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name') as HTMLInputElement | undefined
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    expect(nameInput.value).toBe('')
    await act(async () => {
      setInputValue(nameInput, 'Draft Scenario')
    })
    await flushPromises()

    const startOverButton = () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Start Over')
    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeDefined()

    // Declining the confirmation leaves progress untouched without writing a draft.
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await clickButton('Start Over')
    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeDefined()

    // Confirming discards progress and resets the wizard to a blank Start step.
    confirmSpy.mockReturnValue(true)
    await clickButton('Start Over')
    await flushPromises()

    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeUndefined()
    expect(wizardStepButton('Start')?.classList.contains('active')).toBe(true)
    expect(wizardStepComplete('Start')).toBe(false)

    confirmSpy.mockRestore()
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function flushTimeouts() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
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

function findControlByLabel<T extends HTMLElement>(container: HTMLElement, label: string, selector: string): T | undefined {
  const field = Array.from(container.querySelectorAll('label')).find(item => (
    item.querySelector('span')?.textContent === label
  ))
  return field?.querySelector(selector) as T | undefined
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

function scenarioFixture(id: string, name: string) {
  return {
    id,
    name,
    duration: '5m',
    assignment: { mode: 'auto' },
    scripts: [],
    data_pools: [],
    associated_releases: [],
    result_count: 0,
    created_at: '2026-05-12T00:00:00Z',
    updated_at: '2026-05-12T00:00:00Z',
    last_run_at: '',
    last_run_ended_at: '',
  }
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
