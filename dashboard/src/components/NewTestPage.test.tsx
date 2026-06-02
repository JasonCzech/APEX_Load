// @vitest-environment jsdom

import { act, type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import NewTestPage from './NewTestPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  listScenarioSchedules: vi.fn(),
  listScenarioScheduleOccurrences: vi.fn(),
  createScenarioSchedule: vi.fn(),
  updateScenarioSchedule: vi.fn(),
  deleteScenarioSchedule: vi.fn(),
  approveScenarioScheduleApproval: vi.fn(),
  rejectScenarioScheduleApproval: vi.fn(),
  createTest: vi.fn(),
  estimateTest: vi.fn(),
  startTest: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  getSystemInventory: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('recharts', () => ({
  Area: () => <span data-testid="mock-area" />,
  AreaChart: ({ children }: { children?: ReactNode }) => <div data-testid="mock-load-profile-chart">{children}</div>,
  CartesianGrid: () => <span data-testid="mock-cartesian-grid" />,
  Line: () => <span data-testid="mock-line" />,
  ReferenceArea: () => <span data-testid="mock-reference-area" />,
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="mock-responsive-container">{children}</div>,
  Tooltip: () => <span data-testid="mock-tooltip" />,
  XAxis: ({ label }: { label?: string | { value?: string } }) => (
    <span data-testid="mock-x-axis" data-label={typeof label === 'string' ? label : label?.value ?? ''} />
  ),
  YAxis: ({ label }: { label?: string | { value?: string } }) => (
    <span data-testid="mock-y-axis" data-label={typeof label === 'string' ? label : label?.value ?? ''} />
  ),
}))

describe('NewTestPage', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)

    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Multi-group (3 groups)',
          duration: '30m0s',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [
            { id: 'release-1', name: 'PI29', application_id: 'app-1' },
            { id: 'release-2', name: 'PI29', application_id: 'app-1' },
          ],
        },
      ],
    })
    apiMock.createTest.mockResolvedValue({ id: 'test-1' })
    apiMock.estimateTest.mockResolvedValue({})
    apiMock.startTest.mockResolvedValue({})
    apiMock.listScenarioSchedules.mockResolvedValue({ schedules: [], count: 0 })
    apiMock.listScenarioScheduleOccurrences.mockResolvedValue({ occurrences: [], count: 0 })
    apiMock.createScenarioSchedule.mockResolvedValue({ id: 'schedule-1' })
    apiMock.approveScenarioScheduleApproval.mockResolvedValue({ schedule: { id: 'schedule-1' } })
    apiMock.rejectScenarioScheduleApproval.mockResolvedValue({ schedule: { id: 'schedule-1' } })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: 'Scenario definitions and runs.',
          operations: [
            { id: 'view_tests', label: 'View tests', allowed: true, grant_source: 'project' },
            { id: 'manage_tests', label: 'Manage tests', allowed: true, grant_source: 'project' },
          ],
        },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [
        {
          target: 'gen-a:9090',
          generator_id: 'gen-a',
          region: 'east',
          status: 'connected',
          supported_protocols: ['http'],
          compatibility_state: 'compatible',
          drain_state: 'active',
          capacity_state: 'available',
          health_score: 100,
          available_capacity: 100,
          active_vusers: 0,
          cpu_percent: 0,
          memory_bytes: 0,
          goroutine_count: 1,
          cpu_limit_cores: 4,
          memory_limit_bytes: 8_589_934_592,
          max_vusers: 100,
        },
        {
          target: 'gen-b:9090',
          generator_id: 'gen-b',
          region: 'west',
          status: 'connected',
          supported_protocols: ['http'],
          compatibility_state: 'compatible',
          drain_state: 'active',
          capacity_state: 'available',
          health_score: 100,
          available_capacity: 200,
          active_vusers: 0,
          cpu_percent: 0,
          memory_bytes: 0,
          goroutine_count: 1,
          cpu_limit_cores: 4,
          memory_limit_bytes: 8_589_934_592,
          max_vusers: 200,
        },
      ],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('links from the scenario selection section to the scenario builder', async () => {
    const openScenarioBuilder = vi.fn()

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
          onOpenScenarioBuilder={openScenarioBuilder}
        />,
      )
    })

    await flushPromises()

    const builderLink = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Open Scenario Builder')
    expect(builderLink).toBeTruthy()
    expect(builderLink?.className).toContain('new-test-builder-button')

    await act(async () => {
      builderLink?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(openScenarioBuilder).toHaveBeenCalledTimes(1)
  })

  it('defaults to the provided scenario when opening from the scenario builder', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            { order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10 },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
        {
          id: 'scenario-2',
          name: 'Builder Created Scenario',
          duration: '20m',
          scenario_mode: 'goal_oriented',
          scripts: [
            { order: 1, name: 'Orders', script_id: 'script-2', script_name: 'Order API', protocol: 'http', vusers: 25 },
          ],
          script_count: 1,
          created_at: '2026-05-13T20:00:00Z',
          updated_at: '2026-05-13T20:00:00Z',
          associated_releases: [],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          initialScenarioId="scenario-2"
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    expect(container.textContent).toContain('Builder Created Scenario')
    expect(container.textContent).toContain('Goal')
    const selectedRow = container.querySelector('.new-test-scenario-table-row.selected')
    expect(selectedRow?.textContent).toContain('Builder Created Scenario')
    expect(container.querySelector('.new-test-scenario-expanded-row')).toBeNull()
    await act(async () => {
      selectedRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Order API')
    expect(container.querySelector('.new-test-scenario-expanded-row')?.textContent).toContain('Order API')
  })

  it('omits previously tested release tags without duplicate key warnings', async () => {
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1')

    const duplicateKeyWarning = consoleErrorSpy.mock.calls.some(([message]) =>
      typeof message === 'string' && message.includes('Encountered two children with the same key'),
    )

    expect(duplicateKeyWarning).toBe(false)

    const releaseTags = Array.from(container.querySelectorAll('.new-test-tag')).map((element) => element.textContent)
    expect(releaseTags).toEqual([])
    expect(container.textContent).not.toContain('Previously Tested')
  })

  it('creates a scenario schedule from the schedules view', async () => {
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    const schedulesButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedules')
    expect(schedulesButton).toBeTruthy()

    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Save Schedule')
    expect(saveButton).toBeTruthy()

    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.listScenarioSchedules).toHaveBeenCalledWith('scenario-1')
    expect(apiMock.createScenarioSchedule).toHaveBeenCalledWith('scenario-1', expect.objectContaining({
      name: 'Multi-group (3 groups) Schedule',
      cron_expr: '0 9 * * 1-5',
      timezone: 'UTC',
      enabled: true,
      approval_policy: 'auto',
      missed_run_policy: 'skip',
    }))
  })

  it('sorts, filters, and expands launch scenarios from the scenario table', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            { order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10 },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
        {
          id: 'scenario-2',
          name: 'Java Batch Review',
          duration: '45m',
          scenario_mode: 'goal_oriented',
          scripts: [
            { order: 1, name: 'Batch', script_id: 'script-2', script_name: 'Batch Script', protocol: 'java_http', vusers: 40 },
          ],
          data_pools: [{ pool_id: 'pool-1', alias: 'orders', pool_name: 'Order Rows', selection_strategy: 'sequential' }],
          script_count: 1,
          created_at: '2026-05-13T20:00:00Z',
          updated_at: '2026-05-13T20:00:00Z',
          associated_releases: [{ id: 'release-1', name: 'PI29', application_id: 'app-1' }],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    expect(container.textContent).toContain('2 available scenarios')
    expect(container.querySelector('.new-test-scenario-table')?.textContent).toContain('Most Recent Run')
    expect(container.querySelector('.new-test-scenario-table')?.textContent).toContain('Releases')
    expect(container.querySelector('.new-test-scenario-expanded-row')).toBeNull()
    const releaseFilter = container.querySelector('.new-test-scenario-filter select') as HTMLSelectElement | null
    const searchLabel = container.querySelector('.new-test-scenario-search')
    expect(releaseFilter).toBeTruthy()
    expect(searchLabel).toBeTruthy()
    if (!releaseFilter || !searchLabel) return
    const releaseFilterLabel = releaseFilter.closest('label')
    expect(releaseFilterLabel).toBeTruthy()
    if (!releaseFilterLabel) return
    expect(releaseFilterLabel.compareDocumentPosition(searchLabel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(releaseFilter?.options[releaseFilter.selectedIndex]?.textContent).toBe('All releases')
    await act(async () => {
      setSelectValue(releaseFilter, 'PI29')
    })
    expect(container.textContent).toContain('1 matching scenario')
    expect(scenarioRowTexts(container)).toHaveLength(1)
    expect(scenarioRowTexts(container)[0]).toContain('Java Batch Review')
    await act(async () => {
      setSelectValue(releaseFilter, '')
    })
    expect(scenarioRowTexts(container)[0]).toContain('Checkout Smoke')

    const vusersHeader = Array.from(container.querySelectorAll('.new-test-scenario-table th')).find(header => header.textContent?.includes('VUsers')) as HTMLElement | undefined
    expect(vusersHeader).toBeTruthy()
    await act(async () => {
      vusersHeader?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(scenarioRowTexts(container)[0]).toContain('Java Batch Review')

    const searchInput = Array.from(container.querySelectorAll('input[type="search"]')).find(input => input.getAttribute('placeholder')?.includes('Find by scenario')) as HTMLInputElement | undefined
    expect(searchInput).toBeDefined()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'java_http')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Java Batch Review')
    expect(container.querySelectorAll('.new-test-scenario-table-row')).toHaveLength(1)

    const javaRow = Array.from(container.querySelectorAll('.new-test-scenario-table-row')).find(row => row.textContent?.includes('Java Batch Review'))
    expect(javaRow).toBeDefined()
    if (!javaRow) return

    await act(async () => {
      javaRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Goal')
    expect(container.textContent).toContain('40')
    expect(container.textContent).toContain('Batch Script')
    expect(container.querySelector('.new-test-scenario-expanded-row')?.textContent).toContain('Iterations')
    expect(container.querySelector('.new-test-scenario-expanded-row')?.textContent).toContain('Log Level')

    await act(async () => {
      setInputValue(searchInput, 'does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match this search.')
  })

  it('places scenario first, merges launch summary, and saves card edits as run-only overrides', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: {
            log_level: 'standard',
            auto_transactions: 'per_iteration',
            error_handling: { snapshot_on_error: true },
          },
          script_shakeout: true,
          resource_baseline: true,
          scripts: [
            { order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10 },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [
            { id: 'release-0', name: 'PI28', application_id: 'app-1' },
          ],
        },
      ],
    })
    const selectTest = vi.fn()

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={selectTest}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    const headers = Array.from(container.querySelectorAll('.new-test-step-header h3'))
    const scenarioHeader = headers.find(element => element.textContent === 'Select Scenario')
    const setupHeader = headers.find(element => element.textContent === 'Run Setup')
    const defaultsHeader = headers.find(element => element.textContent === 'Global Run Settings')
    const groupsHeader = headers.find(element => element.textContent === 'Script Groups')
    expect(scenarioHeader).toBeTruthy()
    expect(setupHeader).toBeTruthy()
    expect(defaultsHeader).toBeTruthy()
    expect(groupsHeader).toBeFalsy()
    if (!scenarioHeader || !setupHeader || !defaultsHeader) return
    expect(scenarioHeader.compareDocumentPosition(setupHeader) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(setupHeader.compareDocumentPosition(defaultsHeader) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const setupSettingsRow = setupHeader.closest('.new-test-run-config-row')
    expect(setupSettingsRow).toBeTruthy()
    expect(defaultsHeader.closest('.new-test-run-config-row')).toBe(setupSettingsRow)
    const scenarioSection = scenarioHeader.closest('.new-test-step')
    expect(scenarioSection?.querySelector('.new-test-scenario-browser')).toBeTruthy()
    expect(scenarioSection?.querySelector('.new-test-scenario-table')).toBeTruthy()
    expect(scenarioSection?.querySelector('.new-test-scenario-facts')).toBeNull()
    expect(scenarioSection?.querySelector('.new-test-detail-grid.compact')).toBeNull()
    expect(scenarioSection?.textContent).toContain('Most Recent Run')
    expect(scenarioSection?.textContent).toContain('Releases')
    expect(scenarioSection?.textContent).toContain('VUsers')
    expect(scenarioSection?.textContent).toContain('Duration')
    expect(scenarioSection?.textContent).toContain('PI28')
    expect(scenarioSection?.querySelector('.new-test-scenario-expanded-row')).toBeNull()
    const selectedScenarioRow = scenarioSection?.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    expect(selectedScenarioRow).toBeTruthy()
    await act(async () => {
      selectedScenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(scenarioSection?.querySelector('.new-test-scenario-expanded-row')?.textContent).toContain('Script Groups')
    const setupSection = setupHeader.closest('.new-test-step')
    expect(setupSection?.textContent).not.toContain('Application')
    expect(setupSection?.textContent).not.toContain('Previously Tested')
    const runOnlyNotice = container.querySelector('.new-test-run-only-callout')
    expect(runOnlyNotice?.textContent).toContain('Note: Changes on this page only override the default settings for the current run.')
    expect(runOnlyNotice?.textContent).toContain('To change scenario defaults, edit the scenario through the Scenario Editor.')
    if (!runOnlyNotice) return
    expect(scenarioHeader.compareDocumentPosition(runOnlyNotice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(runOnlyNotice.compareDocumentPosition(setupHeader) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    const sidebar = container.querySelector('.new-test-sidebar-column')
    const readinessCard = sidebar?.querySelector('.new-test-summary')
    const profileCard = sidebar?.querySelector('.new-test-profile-card')
    expect(readinessCard).toBeTruthy()
    expect(profileCard).toBeTruthy()
    if (!readinessCard || !profileCard) return
    expect(readinessCard.textContent).toContain('Launch Summary')
    expect(readinessCard.querySelector('.readiness-list')).toBeNull()
    expect(readinessCard.querySelector('.new-test-summary-grid')).toBeNull()
    const summaryList = readinessCard.querySelector('.new-test-summary-list')
    expect(summaryList).toBeTruthy()
    expect(summaryList?.textContent).toContain('Run Name')
    expect(summaryList?.textContent).toContain('Checkout Smoke')
    expect(summaryList?.textContent).toContain('Release')
    expect(summaryList?.textContent).toContain('PI29')
    expect(summaryList?.textContent).toContain('Total VUsers')
    expect(summaryList?.textContent).toContain('Script Groups')
    expect(summaryList?.textContent).toContain('Capacity')
    expect(readinessCard.textContent).not.toContain('Application')
    expect(profileCard.textContent).toContain('Modeled Load Profile')
    expect(profileCard.textContent).toContain('Planned VUsers for this run.')
    expect(profileCard.textContent).toContain('10peak')
    expect(profileCard.textContent).toContain('20m total')
    expect(readinessCard.compareDocumentPosition(profileCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(profileCard.querySelector('[data-testid="mock-responsive-container"]')).toBeTruthy()
    expect(profileCard.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(profileCard.querySelector('[data-testid="mock-y-axis"]')?.getAttribute('data-label')).toBe('VUsers')
    expect(container.textContent).toContain('Apply runtime defaults to all script groups')
    const scriptGroupsTable = container.querySelector('.new-test-script-table')
    expect(scriptGroupsTable).toBeTruthy()
    const scriptGroupsTableText = scriptGroupsTable?.textContent || ''
    expect(scriptGroupsTableText).toContain('Iterations')
    expect(scriptGroupsTableText).toContain('Pacing')
    expect(scriptGroupsTableText).toContain('Think Time')
    expect(scriptGroupsTableText).toContain('Log Level')
    expect(scriptGroupsTableText).toContain('Auto Tx')
    expect(scriptGroupsTableText).toContain('Duration driven')
    expect(scriptGroupsTableText).toContain('Script timing')
    expect(scriptGroupsTableText).toContain('Script think time')
    expect(scriptGroupsTableText).toContain('Standard')
    expect(scriptGroupsTableText).toContain('Per iteration')
    expect(scriptGroupsTableText).toContain('Stop / Snapshot')
    expect(scriptGroupsTableText).not.toMatch(/\bdefault\b/i)

    const runtimeDefaultsToggle = container.querySelector('.new-test-runtime-defaults summary') as HTMLElement | null
    expect(runtimeDefaultsToggle).toBeTruthy()
    await act(async () => {
      runtimeDefaultsToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const logLevelCard = Array.from(container.querySelectorAll('button.new-test-default-card')).find(button => button.textContent?.includes('Log Level'))
    expect(logLevelCard).toBeTruthy()

    await act(async () => {
      logLevelCard?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Edit Log Level')
    expect(container.textContent).toContain('Saved changes apply only to the run created from this page')
    expect(container.textContent).toContain('Scenario default')
    expect(container.textContent).toContain('standard')

    const logLevelSelect = container.querySelector('.run-default-modal select') as HTMLSelectElement | null
    expect(logLevelSelect).toBeTruthy()
    if (!logLevelSelect) return

    await act(async () => {
      setSelectValue(logLevelSelect, 'extended')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save Run Override')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Saved Log Level for this run only. Scenario defaults were not changed.')
    const overriddenLogLevelCard = Array.from(container.querySelectorAll('button.new-test-default-card')).find(button => button.textContent?.includes('Log Level'))
    expect(overriddenLogLevelCard?.textContent).toContain('extended')
    expect(overriddenLogLevelCard?.textContent).toContain('Run override')

    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Smoke',
      duration: '10m',
      scenario_id: 'scenario-1',
      runtime_settings: {
        log_level: 'extended',
        auto_transactions: 'per_iteration',
        error_handling: {
          continue_on_error: false,
          snapshot_on_error: true,
        },
      },
    }))
    expect(selectTest).toHaveBeenCalledWith('test-1')
  })

  it('saves per-script runtime settings as run-only group overrides', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: {
            log_level: 'standard',
            auto_transactions: 'per_iteration',
          },
          scripts: [
            { order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10, ramp_up: '1m', ramp_down: '30s' },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    expect(container.querySelector('.new-test-scenario-expanded-row')).toBeNull()
    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    expect(scenarioRow).toBeTruthy()
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    expect(scriptGroupRow).toBeTruthy()
    expect(container.querySelector('.new-test-script-table')?.textContent).not.toContain('Browser')
    expect(container.querySelector('.new-test-script-table')?.textContent).not.toContain('Edit')

    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = container.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    expect(modal.textContent).toContain('Saved changes apply only to the run created from this page')

    const vusersInput = findControlByLabel<HTMLInputElement>(modal, 'VUsers', 'input')
    const iterationsInput = findControlByLabel<HTMLInputElement>(modal, 'Iterations', 'input')
    const logLevelSelect = findControlByLabel<HTMLSelectElement>(modal, 'Log level', 'select')
    const pacingSelect = findControlByLabel<HTMLSelectElement>(modal, 'Pacing', 'select')
    expect(vusersInput).toBeTruthy()
    expect(iterationsInput).toBeTruthy()
    expect(logLevelSelect).toBeTruthy()
    expect(pacingSelect).toBeTruthy()
    if (!vusersInput || !iterationsInput || !logLevelSelect || !pacingSelect) return
    expect(logLevelSelect.value).toBe('standard')
    expect(pacingSelect.options[pacingSelect.selectedIndex]?.textContent).toBe('Script timing')
    const initiallyDisabledSaveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(initiallyDisabledSaveButton?.disabled).toBe(true)
    expect(modal.textContent).not.toContain('Use default')

    await act(async () => {
      setInputValue(vusersInput, '25')
      setInputValue(iterationsInput, '4')
      setSelectValue(logLevelSelect, 'extended')
      setSelectValue(pacingSelect, 'fixed_delay')
    })
    expect(initiallyDisabledSaveButton?.disabled).toBe(false)
    expect(modal.querySelectorAll('.new-test-changed-pill').length).toBeGreaterThanOrEqual(4)

    const pacingAmountInput = Array.from(modal.querySelectorAll('label')).find(label => label.textContent?.includes('Milliseconds'))?.querySelector('input') as HTMLInputElement | null
    expect(pacingAmountInput).toBeTruthy()
    if (!pacingAmountInput) return

    await act(async () => {
      setInputValue(pacingAmountInput, '750')
    })

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Saved script settings for this run only. Scenario defaults were not changed.')
    expect(container.textContent).toContain('25 VUsers')
    const scriptGroupsTable = container.querySelector('.new-test-script-table')
    expect(scriptGroupsTable).toBeTruthy()
    const scriptGroupsTableText = scriptGroupsTable?.textContent || ''
    expect(scriptGroupsTableText).toContain('4')
    expect(scriptGroupsTableText).toContain('Fixed delay · 750ms')
    expect(scriptGroupsTableText).toContain('Extended')
    expect(scriptGroupsTableText).not.toMatch(/\bdefault\b/i)
    expect(container.textContent).toContain('Run override')

    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      groups: [expect.objectContaining({
        name: 'Checkout',
        script_id: 'script-1',
        vusers: 25,
        ramp_up: '1m',
        ramp_down: '30s',
        runtime_settings: {
          iterations: 4,
          log_level: 'extended',
          pacing: { type: 'fixed_delay', delay_ms: 750 },
        },
      })],
    }))
  })

  it('saves per-group host sharding as a run-only assignment override', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            { order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 30, ramp_up: '1m' },
          ],
          script_count: 1,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    expect(container.querySelector('.new-test-scenario-expanded-row')).toBeNull()
    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    expect(scenarioRow).toBeTruthy()
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    expect(scriptGroupRow).toBeTruthy()
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = container.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return

    const shardingRadio = Array.from(modal.querySelectorAll('label')).find(label => label.textContent?.includes('Specific host sharding'))?.querySelector('input') as HTMLInputElement | null
    expect(shardingRadio).toBeTruthy()
    await act(async () => {
      shardingRadio?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const hostCheckboxes = Array.from(modal.querySelectorAll('.new-test-generator-option input')) as HTMLInputElement[]
    expect(hostCheckboxes).toHaveLength(2)
    await act(async () => {
      hostCheckboxes.forEach(checkbox => checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    })

    expect(modal.textContent).toContain('Sharded: 2 hosts, capacity weighted')
    expect(modal.textContent).toContain('300 selected VUser capacity')

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const scriptGroupsTableText = container.querySelector('.new-test-script-table')?.textContent || ''
    expect(scriptGroupsTableText).toContain('Placement')
    expect(scriptGroupsTableText).toContain('Sharded: 2 hosts, capacity weighted')
    expect(container.textContent).toContain('Host Placement')
    expect(container.textContent).toContain('1 sharded group')

    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      groups: [expect.objectContaining({
        name: 'Checkout',
        script_id: 'script-1',
        vusers: 30,
        ramp_up: '1m',
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a:9090', 'gen-b:9090'],
          shard_strategy: 'capacity_weighted',
        },
      })],
    }))
  })

  it('surfaces governed schedule policies and approval actions', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-02T12:34:00Z'))
    apiMock.listScenarioSchedules.mockResolvedValue({
      schedules: [
        {
          id: 'schedule-1',
          scenario_id: 'scenario-1',
          name: 'Production Nightly',
          cron_expr: '0 2 * * 1-5',
          release_id: 'release-1',
          run_name_template: 'Production Nightly',
          enabled: true,
          revision: 2,
          timezone: 'America/New_York',
          owner_display_name: 'Project Admin',
          governance: {
            rbac_review: { decision: 'allowed', review_source: 'project_role' },
            quota_review: {
              decision: 'blocked',
              estimated_peak_vusers: 1500,
              findings: ['Estimated VUsers exceed project quota.'],
            },
          },
          blackout_windows: [{ name: 'maintenance', start_time: '00:00', end_time: '06:00', timezone: 'America/New_York' }],
          maintenance_windows: [{ name: 'shared-maintenance', start_time: '22:00', end_time: '23:00', timezone: 'America/New_York' }],
          availability_windows: [{ name: 'business-hours', start_time: '09:00', end_time: '17:00', timezone: 'America/New_York' }],
          approval_policy: 'production',
          missed_run_policy: 'catch_up_once',
          next_required_action: 'approval_required',
          pending_approval_id: 'approval-1',
          conflict_summary: 'Project quota exceeded by 20 VUsers.',
          last_occurrence: {
            id: 'occ-1',
            schedule_id: 'schedule-1',
            scenario_id: 'scenario-1',
            schedule_revision: 2,
            planned_at: '2026-05-18T06:00:00Z',
            status: 'blocked',
            reason_code: 'approval_required',
            approval_id: 'approval-1',
            created_at: '2026-05-18T06:00:00Z',
            updated_at: '2026-05-18T06:00:00Z',
          },
          created_at: '2026-05-17T00:00:00Z',
          updated_at: '2026-05-18T00:00:00Z',
        },
      ],
      count: 1,
    })
    apiMock.listScenarioScheduleOccurrences.mockResolvedValue({
      occurrences: [
        {
          id: 'occ-1',
          schedule_id: 'schedule-1',
          scenario_id: 'scenario-1',
          schedule_revision: 2,
          planned_at: '2026-05-18T06:00:00Z',
          materialized_at: '2026-05-18T06:00:03Z',
          status: 'blocked',
          reason_code: 'schedule_conflict',
          approval_id: 'approval-1',
          conflict_summary: 'Project quota exceeded by 20 VUsers.',
          created_at: '2026-05-18T06:00:00Z',
          updated_at: '2026-05-18T06:00:00Z',
        },
        {
          id: 'occ-2',
          schedule_id: 'schedule-1',
          scenario_id: 'scenario-1',
          schedule_revision: 2,
          planned_at: '2026-05-17T06:00:00Z',
          materialized_at: '2026-05-17T06:00:05Z',
          status: 'materialized',
          reason_code: 'started',
          test_id: 'test-17',
          created_at: '2026-05-17T06:00:00Z',
          updated_at: '2026-05-17T06:00:00Z',
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    const schedulesButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedules')
    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Production Nightly')
    expect(container.textContent).toContain('rev 2')
    expect(container.textContent).toContain('owner Project Admin')
    expect(container.textContent).toContain('America/New_York')
    expect(container.textContent).toContain('Current UTC: 12:34 UTC')
    const timezoneSelect = Array.from(container.querySelectorAll('select')).find(select => Array.from(select.options).some(option => option.value === 'America/New_York')) as HTMLSelectElement | undefined
    expect(timezoneSelect).toBeTruthy()
    expect(Array.from(timezoneSelect?.options || []).find(option => option.value === 'UTC')?.textContent).toBe('UTC - 12:34 UTC+00:00')
    expect(Array.from(timezoneSelect?.options || []).find(option => option.value === 'America/New_York')?.textContent).toBe('America/New York - 08:34 UTC-04:00')
    expect(container.textContent).toContain('1 blackout')
    expect(container.textContent).toContain('1 maintenance')
    expect(container.textContent).toContain('1 availability')
    expect(container.textContent).toContain('production')
    expect(container.textContent).toContain('catch up once')
    expect(container.textContent).toContain('approval required')
    expect(container.textContent).toContain('Project quota exceeded by 20 VUsers.')
    expect(container.textContent).toContain('RBAC allowed via project role')
    expect(container.textContent).toContain('Quota blocked · peak 1500 VUsers')
    expect(container.textContent).toContain('Estimated VUsers exceed project quota.')
    expect(container.textContent).toContain('Needs Action')
    expect(container.textContent).toContain('Conflicts')

    const auditButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Audit Trail')
    expect(auditButton).toBeTruthy()
    await act(async () => {
      auditButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.listScenarioScheduleOccurrences).toHaveBeenCalledWith('scenario-1', 'schedule-1')
    expect(container.textContent).toContain('Occurrence Audit Trail')
    expect(container.textContent).toContain('2 records')
    expect(container.textContent).toContain('schedule conflict')
    expect(container.textContent).toContain('test-17')
    expect(container.textContent).toContain('Blackout maintenance: 00:00-06:00 America/New_York')
    expect(container.textContent).toContain('Availability business-hours: 09:00-17:00 America/New_York')

    const approveButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Approve')
    expect(approveButton).toBeTruthy()
    await act(async () => {
      approveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.approveScenarioScheduleApproval).toHaveBeenCalledWith('scenario-1', 'schedule-1', 'approval-1', 'Approved from schedule dashboard.')
  })

  it('disables run and schedule mutations when effective RBAC denies test management', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: 'Scenario definitions and runs.',
          operations: [
            { id: 'view_tests', label: 'View tests', allowed: true, grant_source: 'project' },
            { id: 'manage_tests', label: 'Manage tests', allowed: false },
          ],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()

    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run') as HTMLButtonElement | undefined
    const createStartButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create & Start') as HTMLButtonElement | undefined
    expect(createQueuedButton?.disabled).toBe(true)
    expect(createStartButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Current session can view scenarios but cannot create runs or schedules for this project.')

    const schedulesButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedules')
    expect(schedulesButton).toBeTruthy()
    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const saveScheduleButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Save Schedule') as HTMLButtonElement | undefined
    expect(saveScheduleButton?.disabled).toBe(true)
    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
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
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const prototype = Object.getPrototypeOf(select) as HTMLSelectElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function scenarioRowTexts(root: HTMLElement) {
  return Array.from(root.querySelectorAll('.new-test-scenario-table-row')).map(row => row.textContent || '')
}

function findControlByLabel<T extends HTMLElement>(root: HTMLElement, labelText: string, selector: string): T | null {
  const label = Array.from(root.querySelectorAll('label')).find(element => element.textContent?.includes(labelText))
  return (label?.querySelector(selector) as T | null) || null
}
