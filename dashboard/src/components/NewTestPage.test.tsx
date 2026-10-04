// @vitest-environment jsdom

import { act, type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import NewTestPage from './NewTestPage'
import { CLEARABLE_RUNTIME_SECTIONS, type ClearableRuntimeSection } from './newTestRunModel'
import { pacingFromEdit } from './pacingEditModel'
import { readNewTestDraft, saveNewTestDraft } from './newTestDraft'
import { persistNewTestQueueAttempt } from './newTestQueueAttempt'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listApplications: vi.fn(),
  listScripts: vi.fn(),
  listReleases: vi.fn(),
  createRelease: vi.fn(),
  listScenarios: vi.fn(),
  previewScenarioSchedule: vi.fn(),
  getScript: vi.fn(),
  listScenarioSchedules: vi.fn(),
  listScenarioScheduleOccurrences: vi.fn(),
  createScenarioSchedule: vi.fn(),
  updateScenarioSchedule: vi.fn(),
  deleteScenarioSchedule: vi.fn(),
  approveScenarioScheduleApproval: vi.fn(),
  rejectScenarioScheduleApproval: vi.fn(),
  createTest: vi.fn(),
  getTest: vi.fn(),
  estimateTest: vi.fn(),
  preflightTest: vi.fn(),
  startTest: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  getSystemSettings: vi.fn(),
  getSystemInventory: vi.fn(),
  listTimeslots: vi.fn(),
  bindTimeslot: vi.fn(),
}
const projectMock = vi.hoisted(() => ({
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' } as {
    id: string
    name: string
    domain_id?: string
    created_at: string
    updated_at: string
  } | null,
}))

function scheduleWithDistinctWindowTimezones() {
  return {
    id: 'schedule-1',
    scenario_id: 'scenario-1',
    name: 'Regional Windows',
    cron_expr: '0 9 * * 1-5',
    timezone: 'America/New_York',
    enabled: true,
    revision: 4,
    approval_policy: 'auto',
    missed_run_policy: 'skip',
    blackout_windows: [{ name: 'UTC freeze', days_of_week: [1], start_time: '00:00', end_time: '01:00', timezone: 'UTC' }],
    maintenance_windows: [{ name: 'Berlin maintenance', days_of_week: [2], start_time: '02:00', end_time: '03:00', timezone: 'Europe/Berlin' }],
    availability_windows: [{ name: 'Tokyo business hours', days_of_week: [3], start_time: '09:00', end_time: '17:00', timezone: 'Asia/Tokyo' }],
    created_at: '2026-05-17T00:00:00Z',
    updated_at: '2026-05-18T00:00:00Z',
  }
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: projectMock.activeProject,
  }),
}))

vi.mock('recharts', () => ({
  Area: () => <span data-testid="mock-area" />,
  AreaChart: ({ children }: { children?: ReactNode }) => <div data-testid="mock-load-profile-chart">{children}</div>,
  CartesianGrid: () => <span data-testid="mock-cartesian-grid" />,
  Line: ({ dataKey, name }: { dataKey?: string; name?: string }) => <span data-testid="mock-line" data-key={dataKey || ''} data-name={name || ''} />,
  ReferenceArea: () => <span data-testid="mock-reference-area" />,
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="mock-responsive-container">{children}</div>,
  Tooltip: () => <span data-testid="mock-tooltip" />,
  XAxis: ({ label, type, domain }: { label?: string | { value?: string }; type?: string; domain?: Array<string | number> }) => (
    <span
      data-testid="mock-x-axis"
      data-label={typeof label === 'string' ? label : label?.value ?? ''}
      data-type={type || ''}
      data-domain={domain?.join(',') || ''}
    />
  ),
  YAxis: ({ label, orientation }: { label?: string | { value?: string }; orientation?: string }) => (
    <span data-testid="mock-y-axis" data-label={typeof label === 'string' ? label : label?.value ?? ''} data-orientation={orientation || 'left'} />
  ),
}))

describe('NewTestPage', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  it('serializes newly selected random pacing with explicit safe bounds', () => {
    expect(pacingFromEdit('random_delay', '', undefined)).toEqual({
      type: 'random_delay',
      min_delay_ms: 1000,
      max_delay_ms: 1000,
    })
    expect(pacingFromEdit('random_interval', '', {
      type: 'random_interval',
      min_interval_ms: 7500,
      max_interval_ms: 2500,
    })).toEqual({
      type: 'random_interval',
      min_interval_ms: 2500,
      max_interval_ms: 7500,
    })
    expect(pacingFromEdit('fixed_delay', '0.75')).toEqual({
      type: 'fixed_delay',
      delay_ms: 750,
    })
    expect(pacingFromEdit('fixed_interval', '5')).toEqual({
      type: 'fixed_interval',
      interval_ms: 5000,
    })
    expect(pacingFromEdit('random_delay', '2.5')).toEqual({
      type: 'random_delay',
      min_delay_ms: 2500,
      max_delay_ms: 2500,
    })
  })

  beforeEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
    projectMock.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)

    apiMock.listApplications.mockResolvedValue({ applications: [{ id: 'app-1', name: 'ACC', project_id: 'project-1' }] })
    apiMock.listScripts.mockResolvedValue({ scripts: [] })
    apiMock.listReleases.mockResolvedValue({ releases: [{ id: 'release-1', name: 'PI29', application_id: 'app-1' }] })
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
    window.sessionStorage.clear()
    apiMock.previewScenarioSchedule.mockResolvedValue({ occurrences: ['2026-09-14T13:00:00Z'] })
    apiMock.getScript.mockRejectedValue(new Error('No script access'))
    apiMock.createTest.mockResolvedValue({ id: 'test-1' })
    apiMock.getTest.mockRejectedValue(new Error('test not found'))
    apiMock.estimateTest.mockResolvedValue({})
    apiMock.preflightTest.mockResolvedValue({
      decision: 'start_now',
      start_now_allowed: true,
      schedule_allowed: false,
      queue_allowed: false,
      candidate_window: {
        start_at: '2026-05-12T20:00:00Z',
        end_at: '2026-05-12T20:10:00Z',
        duration_seconds: 600,
      },
      blocking_conflicts: [],
      warnings: [],
    })
    apiMock.startTest.mockResolvedValue({})
    apiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    apiMock.bindTimeslot.mockResolvedValue({})
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
            { id: 'manage_projects', label: 'Manage projects', allowed: true, grant_source: 'project' },
          ],
        },
      ],
    })
    apiMock.getSystemSettings.mockResolvedValue({
      schedule_defaults: {
        approval_policy: 'auto',
        missed_run_policy: 'skip',
        blackout_start_time: '',
        blackout_end_time: '',
        availability_start_time: '',
        availability_end_time: '',
      },
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

  it('keeps archived scenario execution and schedule changes read-only', async () => {
    const initial = await apiMock.listScenarios()
    initial.scenarios[0].archived_at = '2026-10-02T00:00:00Z'
    apiMock.listScenarios.mockResolvedValue(initial)
    await act(async () => root.render(<NewTestPage activeApplication={{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }} activeRelease={{ id: 'release-1', name: 'Release', application_id: 'app-1', created_at: '', updated_at: '' }} releases={[]} initialScenarioId="scenario-1" onSelectTest={vi.fn()} onCreateRelease={vi.fn()} />))
    await flushPromises()
    expect(container.textContent).toContain('This scenario is archived')
    const start = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Start Now')!
    expect(start.disabled).toBe(true)
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Schedules')!.click())
    expect(container.querySelector<HTMLInputElement>('[aria-label="Raw cron expression"]')?.disabled).toBe(true)
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
  })

  it('suggests assigned applications but requires release selection and clears it on application override', async () => {
    const initial = await apiMock.listScenarios()
    initial.scenarios[0].library_assignment = { application: { id: 'app-2', name: 'Assigned App' }, releases: [{ id: 'release-2', name: 'Assigned Release', application_id: 'app-2' }], revision: 1 }
    apiMock.listScenarios.mockResolvedValue(initial)
    apiMock.listApplications.mockResolvedValue({ applications: [{ id: 'app-1', name: 'Workspace App', project_id: 'project-1' }, { id: 'app-2', name: 'Assigned App', project_id: 'project-1' }] })
    apiMock.listReleases.mockImplementation((id: string) => Promise.resolve({ releases: [{ id: id === 'app-2' ? 'release-2' : 'release-1', name: 'Assigned Release', application_id: id }] }))
    await act(async () => root.render(<NewTestPage activeApplication={{ id: 'app-1', name: 'Workspace App', created_at: '', updated_at: '' }} activeRelease={{ id: 'release-1', name: 'Workspace Release', application_id: 'app-1', created_at: '', updated_at: '' }} releases={[]} initialScenarioId={initial.scenarios[0].id} onSelectTest={vi.fn()} onCreateRelease={vi.fn()} />))
    await flushPromises()
    const application = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Launch application'))!
    const release = container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')!
    expect(application.value).toBe('app-2')
    expect(release.value).toBe('')
    expect(release.textContent).toContain('assigned')
    await act(async () => setSelectValue(release, 'release-2'))
    expect(release.value).toBe('release-2')
    await act(async () => setSelectValue(application, 'app-1'))
    expect(release.value).toBe('')
  })

  it('keeps an explicit release through transient workspace application hydration', async () => {
    const props = { activeApplication: { id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }, activeRelease: null, releases: [{ id: 'release-1', name: 'Release', application_id: 'app-1', created_at: '', updated_at: '' }], onSelectTest: vi.fn(), onCreateRelease: vi.fn() }
    await act(async () => root.render(<NewTestPage {...props} />))
    await flushPromises()
    await selectLaunchRelease()
    await act(async () => root.render(<NewTestPage {...props} activeApplication={{ ...props.activeApplication, id: 'transient-app' }} />))
    await act(async () => root.render(<NewTestPage {...props} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')?.value).toBe('release-1')
  })

  it('does not replace a release choice when assignment suggestions arrive late', async () => {
    const initial = await apiMock.listScenarios()
    initial.scenarios[0].library_assignment = { application: { id: 'app-2', name: 'Assigned App' }, releases: [], revision: 1 }
    apiMock.listScenarios.mockResolvedValue(initial)
    let resolveApplications!: (value: unknown) => void
    apiMock.listApplications.mockImplementation(() => new Promise(resolve => { resolveApplications = resolve }))
    await act(async () => root.render(<NewTestPage activeApplication={{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }} activeRelease={null} releases={[{ id: 'release-1', name: 'Release', application_id: 'app-1', created_at: '', updated_at: '' }]} initialScenarioId={initial.scenarios[0].id} onSelectTest={vi.fn()} onCreateRelease={vi.fn()} />))
    await selectLaunchRelease()
    await act(async () => resolveApplications({ applications: [{ id: 'app-1', name: 'App', project_id: 'project-1' }, { id: 'app-2', name: 'Assigned App', project_id: 'project-1' }] }))
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')?.value).toBe('release-1')
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')?.value).toBe('app-1')
  })

  it('keeps an explicit application override when scenario assignments arrive before a release is chosen', async () => {
    const initial = await apiMock.listScenarios()
    initial.scenarios[0].library_assignment = { application: { id: 'app-1', name: 'Assigned App' }, releases: [], revision: 1 }
    let resolveScenarios!: (value: unknown) => void
    apiMock.listScenarios.mockImplementation(() => new Promise(resolve => { resolveScenarios = resolve }))
    apiMock.listApplications.mockResolvedValue({ applications: [{ id: 'app-1', name: 'Assigned App', project_id: 'project-1' }, { id: 'app-2', name: 'Override App', project_id: 'project-1' }] })
    await act(async () => root.render(<NewTestPage activeApplication={{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }} activeRelease={null} releases={[]} initialScenarioId={initial.scenarios[0].id} onSelectTest={vi.fn()} onCreateRelease={vi.fn()} />))
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')!, 'app-2'))
    await act(async () => resolveScenarios(initial))
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')?.value).toBe('app-2')
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')?.value).toBe('')
  })

  it('keeps a schedule release choice through transient workspace hydration', async () => {
    const props = { activeApplication: { id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }, activeRelease: null, releases: [{ id: 'release-1', name: 'Release', application_id: 'app-1', created_at: '', updated_at: '' }], onSelectTest: vi.fn(), onCreateRelease: vi.fn() }
    await act(async () => root.render(<NewTestPage {...props} />))
    await flushPromises()
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Schedules')!.click())
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-release-label"]')!, 'release-1'))
    await act(async () => root.render(<NewTestPage {...props} activeApplication={{ ...props.activeApplication, id: 'transient-app' }} />))
    await act(async () => root.render(<NewTestPage {...props} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-release-label"]')?.value).toBe('release-1')
  })

  it('fences duplicate release creation and ignores its result after application changes', async () => {
    const apps = [{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }, { id: 'app-2', name: 'Other', project_id: 'project-1', created_at: '', updated_at: '' }]
    apiMock.listApplications.mockResolvedValue({ applications: apps })
    let resolveRelease!: (value: { id: string; name: string; application_id: string; created_at: string; updated_at: string }) => void
    const create = vi.fn(() => new Promise<Parameters<typeof resolveRelease>[0]>(resolve => { resolveRelease = resolve }))
    await act(async () => root.render(<NewTestPage activeApplication={apps[0]} activeRelease={null} releases={[]} onSelectTest={vi.fn()} onCreateRelease={create} />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === '+ New Release')!.click())
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('[aria-label="New release name"]')!, 'Created release'))
    const add = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add')!
    await act(async () => { add.click(); add.click() })
    expect(create).toHaveBeenCalledTimes(1)
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')!, 'app-2'))
    await act(async () => resolveRelease({ id: 'created', name: 'Created release', application_id: 'app-1', created_at: '', updated_at: '' }))
    const release = container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')!
    expect(release.value).toBe('')
    expect(release.textContent).not.toContain('Created release')
  })

  it('reports release creation failures in an override application', async () => {
    const apps = [{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }, { id: 'app-2', name: 'Other', project_id: 'project-1', created_at: '', updated_at: '' }]
    apiMock.listApplications.mockResolvedValue({ applications: apps })
    apiMock.createRelease.mockRejectedValue(new Error('Release creation unavailable'))
    await act(async () => root.render(<NewTestPage activeApplication={apps[0]} activeRelease={null} releases={[]} onSelectTest={vi.fn()} onCreateRelease={vi.fn()} />))
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')!, 'app-2'))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === '+ New Release')!.click())
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('[aria-label="New release name"]')!, 'Created release'))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add')!.click())
    expect(apiMock.createRelease).toHaveBeenCalledWith('app-2', { name: 'Created release' })
    expect(container.textContent).toContain('Release creation unavailable')
  })

  it('preserves a newly created release when an older catalog read resolves late', async () => {
    const created = { id: 'created', name: 'Created release', application_id: 'app-1', created_at: '', updated_at: '' }
    let resolveOldCatalog!: (value: { releases: typeof created[] }) => void
    apiMock.listReleases.mockImplementationOnce(() => new Promise(resolve => { resolveOldCatalog = resolve }))
      .mockResolvedValue({ releases: [created] })
    await act(async () => root.render(<NewTestPage activeApplication={{ id: 'app-1', name: 'App', project_id: 'project-1', created_at: '', updated_at: '' }} activeRelease={null} releases={[]} onSelectTest={vi.fn()} onCreateRelease={vi.fn().mockResolvedValue(created)} />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === '+ New Release')!.click())
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('[aria-label="New release name"]')!, created.name))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add')!.click())
    await act(async () => resolveOldCatalog({ releases: [] }))
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')?.value).toBe(created.id)
    expect(apiMock.listReleases).toHaveBeenCalledTimes(2)
  })

  it('reapplies assignment suggestions when returning through a project without scenarios', async () => {
    const initial = await apiMock.listScenarios()
    initial.scenarios[0].library_assignment = { application: { id: 'app-2', name: 'Assigned App' }, releases: [], revision: 1 }
    apiMock.listScenarios.mockImplementation(() => Promise.resolve(projectMock.activeProject?.id === 'project-1' ? initial : { scenarios: [] }))
    apiMock.listApplications.mockResolvedValue({ applications: [{ id: 'app-1', name: 'Workspace', project_id: 'project-1' }, { id: 'app-2', name: 'Assigned App', project_id: 'project-1' }] })
    const props = { activeApplication: { id: 'app-1', name: 'Workspace', project_id: 'project-1', created_at: '', updated_at: '' }, activeRelease: null, releases: [], initialScenarioId: initial.scenarios[0].id, onSelectTest: vi.fn(), onCreateRelease: vi.fn() }
    await act(async () => root.render(<NewTestPage {...props} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')?.value).toBe('app-2')
    projectMock.activeProject = { id: 'project-2', name: 'Empty', created_at: '', updated_at: '' }
    await act(async () => root.render(<NewTestPage {...props} activeApplication={null} />))
    projectMock.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    await act(async () => root.render(<NewTestPage {...props} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Launch application"]')?.value).toBe('app-2')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    window.localStorage.clear()
    window.sessionStorage.clear()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('edits and round-trips random delay bounds in the global run-default editor', async () => {
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

    const runtimeDefaultsToggle = container.querySelector('.new-test-runtime-defaults summary')
    await act(async () => runtimeDefaultsToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const pacingCard = Array.from(container.querySelectorAll('button.new-test-default-card'))
      .find(button => button.textContent?.includes('Pacing'))
    expect(pacingCard).toBeTruthy()
    await act(async () => pacingCard?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    const modal = document.querySelector('.run-default-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const pacing = findControlByLabel<HTMLSelectElement>(modal, 'Pacing', 'select')
    expect(pacing).toBeTruthy()
    if (!pacing) return
    await act(async () => setSelectValue(pacing, 'random_delay'))

    const minimum = findControlByLabel<HTMLInputElement>(modal, 'Minimum delay (seconds)', 'input')
    const maximum = findControlByLabel<HTMLInputElement>(modal, 'Maximum delay (seconds)', 'input')
    expect(minimum?.value).toBe('1')
    expect(maximum?.value).toBe('1')
    if (!minimum || !maximum) return
    await act(async () => {
      setInputValue(minimum, '0.25')
      setInputValue(maximum, '0.75')
    })
    const save = Array.from(modal.querySelectorAll('button'))
      .find(button => button.textContent === 'Save Run Override')
    await act(async () => save?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(pacingCard?.textContent).toContain('Random delay · 0.25–0.75 seconds')
    await act(async () => pacingCard?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const reopened = document.querySelector('.run-default-modal') as HTMLElement | null
    expect(reopened).toBeTruthy()
    if (!reopened) return
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Minimum delay (seconds)', 'input')?.value).toBe('0.25')
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Maximum delay (seconds)', 'input')?.value).toBe('0.75')
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

  it('shows a first-run scenario state and does not complete Run Setup without a name', async () => {
    apiMock.listScenarios.mockResolvedValue({ scenarios: [] })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
          onOpenScenarioBuilder={vi.fn()}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('No scenarios are available yet. Open Scenario Builder to create the first reusable workload.')
    expect(container.textContent).not.toContain('No scenarios match this search.')
    const runSetupStep = Array.from(container.querySelectorAll<HTMLElement>('.new-test-step'))
      .find(step => step.querySelector('h3')?.textContent === 'Run Setup')
    const stepIndicator = runSetupStep?.querySelector('.new-test-step-index')
    expect(stepIndicator?.textContent).toBe('2')
    expect(stepIndicator?.classList.contains('complete')).toBe(false)
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

  it('treats the calendar date prefill as navigation context rather than an unsaved edit', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            initialScenarioId="scenario-1"
            initialPrefillStartLocal="2026-10-01T09:00"
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Schedules')
    expect(schedulesButton).toBeDefined()
    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).not.toContain('Unsaved Changes')
    expect(container.textContent).toContain('Scenario Schedule')
  })

  it('preserves a run name entered before the scenario catalog finishes loading', async () => {
    const catalog = await apiMock.listScenarios()
    let resolveCatalog!: (value: typeof catalog) => void
    apiMock.listScenarios.mockReturnValue(new Promise(resolve => { resolveCatalog = resolve }))
    await act(async () => {
      root.render(<NewTestPage
        activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
        activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
        releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
        initialScenarioId="scenario-1" onSelectTest={vi.fn()}
        onCreateRelease={vi.fn().mockResolvedValue(null)}
      />)
    })
    const name = container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')!
    expect(name).toBeTruthy()
    await act(async () => { setInputValue(name, 'Operator launch name') })
    expect(name.value).toBe('Operator launch name')
    await act(async () => { resolveCatalog(catalog) })
    await flushPromises()
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).toBe('Operator launch name')
  })

  it('reseeds the run name when explicitly reselecting a cleared scenario', async () => {
    await act(async () => root.render(<NewTestPage
      activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
      activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
      releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
      initialScenarioId="scenario-1" onSelectTest={vi.fn()}
      onCreateRelease={vi.fn().mockResolvedValue(null)}
    />))
    await flushPromises()
    const tab = (name: string) => Array.from(container.querySelectorAll('button')).find(button => button.textContent === name)!
    await act(async () => tab('Schedules').click())
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-scenario-label"]')!, ''))
    await act(async () => tab('Run Setup').click())
    const row = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row')!
    await act(async () => row.click())
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).toBe('Multi-group (3 groups)')
  })

  it('retains a dirty run setup when switching tabs without creating a test', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            initialScenarioId="scenario-1"
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const runNameInput = container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')
    expect(runNameInput).toBeDefined()
    if (!runNameInput) return

    await act(async () => {
      setInputValue(runNameInput, 'Dirty run setup')
      runNameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Schedules')
    expect(schedulesButton).toBeDefined()
    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).not.toContain('Unsaved Changes')
    expect(container.textContent).toContain('Scenario Schedule')
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Run Setup')?.click())
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).toBe('Dirty run setup')
    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(apiMock.startTest).not.toHaveBeenCalled()
  })

  it('guards scenario row changes that would discard dirty launch overrides', async () => {
    const currentScenarios = await apiMock.listScenarios()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        ...currentScenarios.scenarios,
        {
          id: 'scenario-2',
          name: 'Alternate Checkout',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [{ order: 1, name: 'Alternate', script_id: 'script-2', script_name: 'Alternate Script', protocol: 'http', vusers: 5 }],
          script_count: 1,
          created_at: '2026-05-13T20:00:00Z',
          updated_at: '2026-05-13T20:00:00Z',
          associated_releases: [],
        },
      ],
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            initialScenarioId="scenario-1"
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const runNameInput = container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')
    const alternateRow = Array.from(container.querySelectorAll<HTMLTableRowElement>('.new-test-scenario-table-row'))
      .find(row => row.textContent?.includes('Alternate Checkout'))
    expect(runNameInput).toBeTruthy()
    expect(alternateRow).toBeTruthy()
    await act(async () => {
      if (runNameInput) setInputValue(runNameInput, 'Dirty launch name')
      alternateRow?.click()
    })

    expect(container.textContent).toContain('Unsaved Changes')
    expect(container.querySelector('.new-test-scenario-table-row.selected')?.textContent).toContain('Multi-group')

    const discardButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Discard changes'))
    await act(async () => discardButton?.click())

    expect(container.querySelector('.new-test-scenario-table-row.selected')?.textContent).toContain('Alternate Checkout')
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).not.toBe('Dirty launch name')
  })

  it('preserves an open run-default editor when the page tab changes', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            initialScenarioId="scenario-1"
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const durationCard = Array.from(container.querySelectorAll<HTMLButtonElement>('button.new-test-default-card'))
      .find(button => button.textContent?.includes('Duration'))
    await act(async () => durationCard?.click())
    const durationInput = findControlByLabel<HTMLInputElement>(document.querySelector('.run-default-modal') as HTMLElement, 'Run duration', 'input')
    expect(durationInput).toBeTruthy()
    await act(async () => {
      if (durationInput) setInputValue(durationInput, '45m')
    })

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())

    expect(container.textContent).not.toContain('Unsaved Changes')
    expect(document.querySelector('.run-default-modal')).toBeTruthy()
  })

  it('rejects invalid duration text in run and group overrides', async () => {
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          initialScenarioId="scenario-1"
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })
    await flushPromises()

    const durationCard = Array.from(container.querySelectorAll<HTMLButtonElement>('button.new-test-default-card'))
      .find(button => button.textContent?.includes('Duration'))
    await act(async () => durationCard?.click())
    const durationModal = document.querySelector('.run-default-modal') as HTMLElement
    const durationInput = findControlByLabel<HTMLInputElement>(durationModal, 'Run duration', 'input')
    await act(async () => {
      if (durationInput) setInputValue(durationInput, '30')
    })
    expect(durationInput?.getAttribute('aria-invalid')).toBe('true')
    await act(async () => {
      Array.from(durationModal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save Run Override')?.click()
    })
    expect(document.querySelector('.run-default-modal')).toBe(durationModal)
    expect(container.textContent).toContain('Run duration must be a positive Go duration')

    await act(async () => {
      Array.from(durationModal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')?.click()
    })
    const selectedScenario = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row.selected')
    await act(async () => selectedScenario?.click())
    await act(async () => container.querySelector<HTMLTableRowElement>('.new-test-script-row')?.click())
    const groupModal = document.querySelector('.run-group-modal') as HTMLElement
    const rampUp = findControlByLabel<HTMLInputElement>(groupModal, 'Ramp up', 'input')
    await act(async () => {
      if (rampUp) setInputValue(rampUp, '30')
    })
    expect(rampUp?.getAttribute('aria-invalid')).toBe('true')
    await act(async () => {
      Array.from(groupModal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save For This Run')?.click()
    })
    expect(document.querySelector('.run-group-modal')).toBe(groupModal)
    expect(container.textContent).toContain('Ramp up must be a non-negative Go duration')

    await act(async () => {
      if (rampUp) setInputValue(rampUp, '0s')
    })
    expect(rampUp?.getAttribute('aria-invalid')).toBe('false')
    await act(async () => {
      Array.from(groupModal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save For This Run')?.click()
    })
    expect(document.querySelector('.run-group-modal')).toBeNull()
  })

  it('lets an operator intentionally clear the seeded run name', async () => {
    const props = {
      activeApplication: { id: 'app-1', name: 'ACC', created_at: '', updated_at: '' },
      activeRelease: { id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' },
      releases: [{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }],
      initialScenarioId: 'scenario-1',
      onSelectTest: vi.fn(),
      onCreateRelease: vi.fn().mockResolvedValue(null),
    }
    await act(async () => { root.render(<NewTestPage {...props} />) })
    await flushPromises()
    const runNameInput = container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')
    expect(runNameInput?.value).toBe('Multi-group (3 groups)')

    await act(async () => {
      if (runNameInput) setInputValue(runNameInput, '')
    })
    await flushPromises()
    await act(async () => { root.render(<NewTestPage {...props} />) })
    await flushPromises()

    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).toBe('')
  })

  it('saves a local draft with pending group edits without creating or starting a test', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            initialScenarioId="scenario-1"
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const selectedScenario = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row.selected')
    await act(async () => selectedScenario?.click())
    const groupRow = container.querySelector<HTMLTableRowElement>('.new-test-script-row')
    await act(async () => groupRow?.click())
    const groupModal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(groupModal).toBeTruthy()
    const vusersInput = groupModal ? findControlByLabel<HTMLInputElement>(groupModal, 'VUsers', 'input') : null
    await act(async () => {
      if (vusersInput) setInputValue(vusersInput, '25')
    })

    const saveDraft = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save draft')
    await act(async () => saveDraft?.click())
    const saved = readNewTestDraft('project-1', 'app-1')
    expect(saved).not.toBeNull()
    expect(Object.values(saved!.groupRunOverrides)[0].vusers).toBe(25)
    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(apiMock.startTest).not.toHaveBeenCalled()
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
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

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1', 'all')

    const duplicateKeyWarning = consoleErrorSpy.mock.calls.some(([message]: unknown[]) =>
      typeof message === 'string' && message.includes('Encountered two children with the same key'),
    )

    expect(duplicateKeyWarning).toBe(false)

    const releaseTags = Array.from(container.querySelectorAll('.new-test-tag')).map((element) => element.textContent)
    expect(releaseTags).toEqual([])
    expect(container.textContent).not.toContain('Previously Tested')
  })

  it('creates a scenario schedule from the schedules view', async () => {
    apiMock.getSystemSettings.mockResolvedValueOnce({
      schedule_defaults: {
        approval_policy: 'production',
        missed_run_policy: 'catch_up_once',
        blackout_start_time: '00:00',
        blackout_end_time: '06:00',
        availability_start_time: '09:00',
        availability_end_time: '17:00',
      },
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

    const runSetupButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Run Setup')
    const schedulesButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedules')
    expect(runSetupButton).toBeTruthy()
    expect(schedulesButton).toBeTruthy()
    expect(runSetupButton?.getAttribute('aria-pressed')).toBe('true')
    expect(schedulesButton?.getAttribute('aria-pressed')).toBe('false')

    await act(async () => {
      schedulesButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(runSetupButton?.getAttribute('aria-pressed')).toBe('false')
    expect(schedulesButton?.getAttribute('aria-pressed')).toBe('true')

    const scheduleRelease = container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-release-label"]')!
    await act(async () => setSelectValue(scheduleRelease, 'release-1'))
    const saveButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Save Schedule')
    expect(saveButton).toBeTruthy()
    expect(saveButton?.closest('.new-test-step-header')).toBeTruthy()
    expect(container.textContent).not.toContain('Calendar Windows')
    expect(container.textContent).not.toContain('Missed Run Policy')

    await flushDebouncedPreflight()
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.listScenarioSchedules).toHaveBeenCalledWith('scenario-1')
    expect(apiMock.createScenarioSchedule).toHaveBeenCalledWith('scenario-1', expect.objectContaining({
      name: 'Multi-group (3 groups) Schedule',
      cron_expr: '0 9 * * 1-5',
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      enabled: true,
      approval_policy: 'production',
      missed_run_policy: 'catch_up_once',
      blackout_windows: [expect.objectContaining({ start_time: '00:00', end_time: '06:00', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })],
      availability_windows: [expect.objectContaining({ start_time: '09:00', end_time: '17:00', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })],
    }))
  })

  it('preserves per-window timezones when an existing schedule is saved without window edits', async () => {
    apiMock.listScenarioSchedules.mockResolvedValue({ schedules: [scheduleWithDistinctWindowTimezones()], count: 1 })

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

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => editButton?.click())
    const updateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Update Schedule')
    await flushDebouncedPreflight()
    await act(async () => updateButton?.click())
    await flushPromises()

    expect(apiMock.updateScenarioSchedule).toHaveBeenCalledWith('scenario-1', 'schedule-1', expect.objectContaining({
      timezone: 'America/New_York',
      expected_revision: 4,
      blackout_windows: [expect.objectContaining({ name: 'UTC freeze', timezone: 'UTC' })],
      maintenance_windows: [expect.objectContaining({ name: 'Berlin maintenance', timezone: 'Europe/Berlin' })],
      availability_windows: [expect.objectContaining({ name: 'Tokyo business hours', timezone: 'Asia/Tokyo' })],
    }))
  })

  it('shows and edits a per-window timezone independently of the cron timezone', async () => {
    apiMock.listScenarioSchedules.mockResolvedValue({ schedules: [scheduleWithDistinctWindowTimezones()], count: 1 })

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

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => editButton?.click())
    const maintenanceStart = container.querySelector<HTMLInputElement>('[aria-label="Maintenance windows window 1 start time"]')
    const maintenanceTimezone = container.querySelector<HTMLSelectElement>('[aria-label="Maintenance windows window 1 timezone"]')
    expect(maintenanceStart).toBeTruthy()
    expect(maintenanceTimezone?.value).toBe('Europe/Berlin')
    await act(async () => {
      if (maintenanceStart) setInputValue(maintenanceStart, '02:30')
      if (maintenanceTimezone) setSelectValue(maintenanceTimezone, 'Asia/Tokyo')
    })
    const updateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Update Schedule')
    await flushDebouncedPreflight()
    await act(async () => updateButton?.click())
    await flushPromises()

    expect(apiMock.updateScenarioSchedule).toHaveBeenCalledWith('scenario-1', 'schedule-1', expect.objectContaining({
      maintenance_windows: [expect.objectContaining({
        name: 'Berlin maintenance',
        start_time: '02:30',
        end_time: '03:00',
        timezone: 'Asia/Tokyo',
      })],
    }))
  })

  it('ignores a stale schedule response after the selected scenario changes', async () => {
    let resolveScenarioOne: ((value: { schedules: ReturnType<typeof scheduleWithDistinctWindowTimezones>[]; count: number }) => void) | undefined
    const scenarioOneSchedules = new Promise<{ schedules: ReturnType<typeof scheduleWithDistinctWindowTimezones>[]; count: number }>(resolve => {
      resolveScenarioOne = resolve
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Scenario One',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [],
          script_count: 0,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
        {
          id: 'scenario-2',
          name: 'Scenario Two',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [],
          script_count: 0,
          created_at: '2026-05-12T20:00:00Z',
          updated_at: '2026-05-12T20:00:00Z',
          associated_releases: [],
        },
      ],
    })
    apiMock.listScenarioSchedules.mockImplementation((scenarioID: string) => {
      if (scenarioID === 'scenario-1') return scenarioOneSchedules
      return Promise.resolve({
        schedules: [{
          ...scheduleWithDistinctWindowTimezones(),
          id: 'schedule-2',
          scenario_id: 'scenario-2',
          name: 'Scenario Two Schedule',
        }],
        count: 1,
      })
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
    expect(apiMock.listScenarioSchedules).toHaveBeenCalledWith('scenario-1')

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())
    const scenarioSelect = container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-scenario-label"]')
    expect(scenarioSelect).toBeTruthy()
    await act(async () => {
      if (scenarioSelect) setSelectValue(scenarioSelect, 'scenario-2')
    })
    await flushPromises()
    expect(container.textContent).toContain('Scenario Two Schedule')

    await act(async () => {
      resolveScenarioOne?.({
        schedules: [{ ...scheduleWithDistinctWindowTimezones(), name: 'Stale Scenario One Schedule' }],
        count: 1,
      })
    })
    await flushPromises()

    expect(container.textContent).toContain('Scenario Two Schedule')
    expect(container.querySelector('[aria-label="Selected scenario schedules"]')?.textContent).not.toContain('Stale Scenario One Schedule')
  })

  it('preserves a dirty recurring draft across both page tabs', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())
    const scheduleNameInput = container.querySelector<HTMLInputElement>('[aria-labelledby="schedule-name-label"]')
    expect(scheduleNameInput).toBeTruthy()
    await act(async () => {
      if (scheduleNameInput) setInputValue(scheduleNameInput, 'Dirty recurring schedule')
    })

    const runSetupButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Run Setup')
    await act(async () => runSetupButton?.click())
    expect(container.textContent).not.toContain('Unsaved Changes')
    await act(async () => schedulesButton?.click())
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="schedule-name-label"]')?.value).toBe('Dirty recurring schedule')
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
    expect(apiMock.createTest).not.toHaveBeenCalled()
  })

  it('guards editing another schedule when the current schedule draft is dirty', async () => {
    apiMock.listScenarioSchedules.mockResolvedValue({
      schedules: [
        scheduleWithDistinctWindowTimezones(),
        { ...scheduleWithDistinctWindowTimezones(), id: 'schedule-2', name: 'Second Schedule', revision: 2 },
      ],
      count: 2,
    })
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NewTestPage
            activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
            activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
            releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
            onSelectTest={vi.fn()}
            onCreateRelease={vi.fn().mockResolvedValue(null)}
          />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())
    await flushPromises()
    const editButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(button => button.textContent === 'Edit')
    expect(editButtons).toHaveLength(2)
    await act(async () => editButtons[0]?.click())
    const scheduleNameInput = container.querySelector<HTMLInputElement>('[aria-labelledby="schedule-name-label"]')
    expect(scheduleNameInput?.value).toBe('Regional Windows')
    await act(async () => {
      if (scheduleNameInput) setInputValue(scheduleNameInput, 'Dirty first schedule')
      editButtons[1]?.click()
    })

    expect(container.textContent).toContain('Unsaved Changes')
    expect(scheduleNameInput?.value).toBe('Dirty first schedule')

    const discardButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Discard changes'))
    await act(async () => discardButton?.click())
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="schedule-name-label"]')?.value).toBe('Second Schedule')
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

    const vusersHeader = Array.from(container.querySelectorAll<HTMLButtonElement>('.new-test-scenario-table th.sortable > button')).find(button => button.textContent?.includes('VUsers'))
    expect(vusersHeader).toBeTruthy()
    expect(vusersHeader?.closest('th')?.getAttribute('aria-sort')).toBe('none')
    expect(vusersHeader?.tabIndex).toBe(0)
    await act(async () => {
      vusersHeader?.click()
    })
    expect(scenarioRowTexts(container)[0]).toContain('Java Batch Review')
    expect(vusersHeader?.closest('th')?.getAttribute('aria-sort')).toBe('descending')

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
    expect(container.querySelector('.new-test-scenario-expanded-row')?.textContent).toContain('Log level')

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
    const launchCard = sidebar?.querySelector('.new-test-launch-card')
    expect(readinessCard).toBeTruthy()
    expect(profileCard).toBeTruthy()
    expect(launchCard).toBeTruthy()
    if (!readinessCard || !profileCard || !launchCard) return
    expect(sidebar?.firstElementChild).toBe(profileCard)
    expect(readinessCard.textContent).toContain('Run Summary')
    expect(readinessCard.querySelector('.readiness-list')).toBeNull()
    expect(readinessCard.querySelector('.new-test-summary-grid')).toBeNull()
    expect(readinessCard.textContent).not.toContain('Hold for Manual Start')
    expect(readinessCard.textContent).not.toContain('Schedule Run')
    const summaryList = readinessCard.querySelector('.new-test-summary-list')
    expect(summaryList).toBeTruthy()
    expect(summaryList?.textContent).toContain('Run Name')
    expect(summaryList?.textContent).toContain('Checkout Smoke')
    expect(summaryList?.textContent).toContain('Release')
    expect(summaryList?.textContent).toContain('None selected')
    expect(summaryList?.textContent).toContain('Total VUsers')
    expect(summaryList?.textContent).toContain('Script Groups')
    expect(summaryList?.textContent).toContain('Capacity')
    expect(readinessCard.textContent).not.toContain('Application')
    expect(profileCard.textContent).toContain('Modeled Load Profile')
    expect(profileCard.textContent).toContain('Planned VUsers for this run.')
    expect(profileCard.textContent).toContain('10peak')
    expect(profileCard.textContent).toContain('20m total')
    expect(profileCard.querySelector('[data-testid="mock-responsive-container"]')).toBeTruthy()
    expect(profileCard.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(profileCard.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-type')).toBe('number')
    expect(profileCard.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-domain')).toBe('0,dataMax')
    expect(profileCard.querySelector('[data-testid="mock-y-axis"]')?.getAttribute('data-label')).toBe('VUsers')
    expect(launchCard.textContent).toContain('Run timing')
    expect(launchCard.textContent).toContain('Hold for Manual Start')
    expect(launchCard.textContent).toContain('Schedule Run')
    expect(profileCard.compareDocumentPosition(launchCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(launchCard.compareDocumentPosition(readinessCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(container.textContent).toContain('Apply runtime defaults to all script groups')
    const scriptGroupsTable = container.querySelector('.new-test-script-table')
    expect(scriptGroupsTable).toBeTruthy()
    const scriptGroupsTableText = scriptGroupsTable?.textContent || ''
    expect(scriptGroupsTableText).toContain('Iterations')
    expect(scriptGroupsTableText).toContain('Pacing')
    expect(scriptGroupsTableText).toContain('Think time')
    expect(scriptGroupsTableText).toContain('Log level')
    expect(scriptGroupsTableText).toContain('Auto transactions')
    expect(scriptGroupsTableText).toContain('Duration driven')
    expect(scriptGroupsTableText).toContain('Script timing')
    expect(scriptGroupsTableText).toContain('Script think time')
    expect(scriptGroupsTableText).toContain('Standard')
    expect(scriptGroupsTableText).toContain('Per iteration')
    expect(scriptGroupsTableText).toContain('Stop / Snapshot')
    expect(scriptGroupsTableText).toContain('Auto')

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

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Checkout Smoke',
      duration: '10m',
      scenario_id: 'scenario-1',
      reserve_launch_window: true,
      scheduled_start_at: '2026-05-12T20:00:00.000Z',
      runtime_settings: {
        log_level: 'extended',
        auto_transactions: 'per_iteration',
        error_handling: {
          snapshot_on_error: true,
        },
      },
    }))
    expect(apiMock.startTest).not.toHaveBeenCalled()
    expect(selectTest).toHaveBeenCalledWith('test-1')
  })

  it('renders a production traffic TPS curve in the modeled load profile', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-profile', name: 'Production Replay', duration: '3m', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false,
        traffic_profile: {
          version: 1,
          source: { kind: 'file', file: { filename: 'traffic.csv', timestamp_column: 'time', value_columns: ['transactions'] } },
          value_semantics: 'transactions_per_bucket', runtime_target: 'tps', interpolation: 'step', source_duration: '3m', target_duration: '3m', refreshed_at: '2026-08-31T00:00:00Z',
          mappings: [{ series_key: 'requests', series_name: 'Requests', group_name: 'Checkout', peak_target: 12, points: [{ offset_ms: 0, target: 3 }, { offset_ms: 60_000, target: 12 }, { offset_ms: 120_000, target: 5 }] }],
        },
        scripts: [{ order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 9 }],
        script_count: 1, created_at: '2026-08-31T00:00:00Z', updated_at: '2026-08-31T00:00:00Z', associated_releases: [],
      }],
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

    const profileCard = container.querySelector('.new-test-profile-card')
    expect(profileCard?.textContent).toContain('Production-derived adaptive TPS targets')
    expect(profileCard?.textContent).toContain('3minimum TPS')
    expect(profileCard?.textContent).toContain('6.67average TPS')
    expect(profileCard?.textContent).toContain('12peak TPS')
    expect(profileCard?.querySelector('[data-key="totalTps"]')?.getAttribute('data-name')).toBe('Production target TPS')
    expect(Array.from(profileCard?.querySelectorAll('[data-testid="mock-y-axis"]') || []).map(axis => axis.getAttribute('data-label'))).toEqual(['TPS'])
    expect(profileCard?.querySelector('[data-testid="mock-y-axis"]')?.getAttribute('data-orientation')).toBe('left')
    expect(profileCard?.textContent).not.toContain('peak VUsers')
  })

  it('renders the configured TPS target for a goal-oriented scenario without a traffic profile', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-goal', name: 'Adaptive Goal', duration: '3m', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false,
        goal_config: { target_average_tps: 75, min_vusers: 1, max_vusers: 100 },
        scripts: [{ order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 9 }],
        script_count: 1, created_at: '2026-08-31T00:00:00Z', updated_at: '2026-08-31T00:00:00Z', associated_releases: [],
      }],
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

    const profileCard = container.querySelector('.new-test-profile-card')
    expect(profileCard?.textContent).toContain('75minimum TPS')
    expect(profileCard?.textContent).toContain('75average TPS')
    expect(profileCard?.textContent).toContain('75peak TPS')
    expect(profileCard?.querySelector('[data-key="totalTps"]')?.getAttribute('data-name')).toBe('Target TPS')
    expect(Array.from(profileCard?.querySelectorAll('[data-testid="mock-y-axis"]') || []).map(axis => axis.getAttribute('data-label'))).toEqual(['TPS'])
    expect(profileCard?.textContent).not.toContain('peak VUsers')
  })

  it('sends run-level trace context for approved origins', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1', name: 'Checkout Smoke', duration: '10m', scenario_mode: 'vuser_driven',
        scripts: [{ order: 1, name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10, ramp_up: '1m', ramp_down: '30s' }],
        script_count: 1, created_at: '2026-05-12T20:00:00Z', updated_at: '2026-05-12T20:00:00Z', associated_releases: [],
      }],
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
    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const traceToggle = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Propagate W3C trace context'))?.querySelector('input') as HTMLInputElement | null
    expect(traceToggle).toBeTruthy()
    if (!traceToggle) return
    await act(async () => {
      traceToggle.click()
    })
    const summaryBeforeOrigins = container.querySelector('.new-test-summary-list')?.textContent || ''
    expect(summaryBeforeOrigins).toContain('Origins need review')
    const originsInput = container.querySelector('.run-trace-context textarea') as HTMLTextAreaElement | null
    expect(originsInput).toBeTruthy()
    if (!originsInput) return
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(originsInput, 'https://shop.example.test\n')
      originsInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('.new-test-summary-list')?.textContent).toContain('traceparent to 1 approved origin')

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      trace_context: { enabled: true, transaction_marker: false, origins: ['https://shop.example.test'] },
    }))
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
    expect(container.querySelector('.new-test-script-table')?.textContent).toContain('Edit settings')

    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
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

    const pacingAmountInput = Array.from(modal.querySelectorAll('label')).find(label => label.textContent?.includes('Seconds'))?.querySelector('input') as HTMLInputElement | null
    expect(pacingAmountInput).toBeTruthy()
    if (!pacingAmountInput) return

    await act(async () => {
      setInputValue(pacingAmountInput, '0.75')
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
    expect(scriptGroupsTableText).toContain('Fixed delay · 0.75 seconds')
    expect(scriptGroupsTableText).toContain('Extended')
    expect(scriptGroupsTableText).toContain('Script default')
    expect(container.textContent).toContain('Run override')

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      reserve_launch_window: true,
      scheduled_start_at: '2026-05-12T20:00:00.000Z',
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
    expect(apiMock.startTest).not.toHaveBeenCalled()
  })

  it('keeps inherited error handling selectable as Script default and overrides only the edited half', async () => {
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect).toBeTruthy()
    expect(snapshotSelect).toBeTruthy()
    if (!continueSelect || !snapshotSelect) return

    // The scenario leaves runtime_settings.error_handling unset, so both selects must offer and
    // display the inherit option rather than silently reading as an explicit Disabled. The two
    // halves name different levels: continue-on-error inherits the scenario policy that enables
    // it, while snapshot-on-error still falls back to the script's own config.
    for (const [select, expectedLabel] of [
      [continueSelect, 'Scenario default (enabled)'],
      [snapshotSelect, 'Script default'],
    ] as const) {
      const inheritOption = Array.from(select.options).find(option => option.value === '')
      expect(inheritOption?.textContent).toBe(expectedLabel)
      expect(select.value).toBe('')
      expect(select.options[select.selectedIndex]?.textContent).toBe(expectedLabel)
    }

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(true)

    await act(async () => {
      setSelectValue(snapshotSelect, 'true')
    })
    expect(saveButton?.disabled).toBe(false)
    expect(continueSelect.value).toBe('')

    await act(async () => {
      setSelectValue(snapshotSelect, '')
    })
    expect(saveButton?.disabled).toBe(true)

    await act(async () => {
      setSelectValue(snapshotSelect, 'true')
    })
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      groups: [expect.objectContaining({
        script_id: 'script-1',
        runtime_settings: { error_handling: { snapshot_on_error: true } },
      })],
    }))
  })

  it('carries the untouched error handling half into the group override payload', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: { error_handling: { continue_on_error: true } },
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: { snapshot_on_error: false } },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect).toBeTruthy()
    expect(snapshotSelect).toBeTruthy()
    if (!continueSelect || !snapshotSelect) return
    // The group carries its own error_handling section, and the API applies the winning
    // section wholesale (mergeRuntimeSettings + applyRuntimeOverrides), so the scenario's
    // continue_on_error:true is NOT inherited here — the group section omits it, which
    // executes as false. The dialog has to open on what the run would actually do.
    expect(continueSelect.value).toBe('false')
    expect(snapshotSelect.value).toBe('false')

    await act(async () => {
      setSelectValue(snapshotSelect, 'true')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // The API replaces error_handling wholesale, and the group already carries its own
    // section, so the scenario's continue_on_error:true must NOT be folded back in on save.
    // The saved section has to match what the dialog displayed (continue Disabled): the
    // group section stays authoritative and the omitted half keeps executing as false.
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      groups: [expect.objectContaining({
        script_id: 'script-1',
        runtime_settings: { error_handling: { snapshot_on_error: true } },
      })],
    }))
  })

  it('seeds the untouched error handling half from the run defaults when the group has no section', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: { error_handling: { continue_on_error: true } },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect).toBeTruthy()
    expect(snapshotSelect).toBeTruthy()
    if (!continueSelect || !snapshotSelect) return
    // No group section of its own, so the scenario's section is the one that executes.
    expect(continueSelect.value).toBe('true')
    expect(snapshotSelect.value).toBe('false')

    await act(async () => {
      setSelectValue(snapshotSelect, 'true')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // Writing a group section pins BOTH halves, so the displayed continue_on_error:true has to
    // be carried into it — dropping it would silently flip the group to stop-on-first-error.
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      groups: [expect.objectContaining({
        script_id: 'script-1',
        runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: true } },
      })],
    }))
  })

  it('clears a pinned group error handling section back to the script default', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: true } },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const openGroupDialog = async () => {
      const row = Array.from(container.querySelectorAll('.new-test-script-row')).find(element => element.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
      await act(async () => {
        row?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      return document.querySelector('.run-group-modal') as HTMLElement | null
    }

    const modal = await openGroupDialog()
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect).toBeTruthy()
    expect(snapshotSelect).toBeTruthy()
    if (!continueSelect || !snapshotSelect) return
    expect(continueSelect.value).toBe('true')
    expect(snapshotSelect.value).toBe('true')
    // Nothing above the group pins a section, so clearing the group's really does hand the run
    // back to the script's own config.error_handling — except for continue-on-error, which the
    // scenario policy enables whenever no layer explicitly disables it.
    expect(Array.from(continueSelect.options).find(option => option.value === '')?.textContent).toBe('Scenario default (enabled)')
    expect(Array.from(snapshotSelect.options).find(option => option.value === '')?.textContent).toBe('Script default')

    await act(async () => {
      setSelectValue(continueSelect, '')
      setSelectValue(snapshotSelect, '')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Saved script settings for this run only.')

    // Re-opening has to show the clear the operator just saved, not the stale stored section.
    const reopened = await openGroupDialog()
    expect(reopened).toBeTruthy()
    if (!reopened) return
    expect(findControlByLabel<HTMLSelectElement>(reopened, 'Continue on error', 'select')?.value).toBe('')
    expect(findControlByLabel<HTMLSelectElement>(reopened, 'Snapshot on error', 'select')?.value).toBe('')
    const closeButton = Array.from(reopened.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // The group override is overlaid back onto the group's stored runtime_settings to build the
    // payload, so an absent key would silently re-apply the section the operator just cleared.
    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    expect(payload.groups).toHaveLength(1)
    expect(payload.groups?.[0]).toEqual(expect.objectContaining({ script_id: 'script-1' }))
    expect(payload.groups?.[0].runtime_settings).toBeUndefined()
  })

  it('reads an all-false group error handling section as the stop-on-error it executes', async () => {
    // dsl.ErrorHandlingConfig is all-omitempty behind a pointer, so a group that pinned
    // continue_on_error:false / snapshot_on_error:false comes back from the API as a NON-NIL
    // `{}` — the most common section shape there is. The backend still applies it whole
    // (mergeRuntimeSettings -> `script.Config.ErrorHandling = *overrides.ErrorHandling`), so
    // reading `{}` as "no section" claims the script's own values survive when they do not.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: {} },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    expect(scriptGroupRow?.textContent).toContain('Stop / No snapshot')
    expect(scriptGroupRow?.textContent).not.toContain('Script default')

    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    expect(findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')?.value).toBe('false')
    expect(findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')?.value).toBe('false')
  })

  it('clears a legacy flat browser section for this run instead of re-applying the stored one', async () => {
    // The browser block offers the same inherit affordance as pacing/think time/error handling
    // ("Standard" on each select, a blankable device profile), and clearing it deletes the
    // section from the run override. The override is overlaid back onto the group's STORED
    // runtime_settings, so without a null tombstone the delete is invisible and the run keeps
    // the emulation the operator just cleared.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'browser',
              vusers: 10,
              runtime_settings: { browser: { device_profile: 'iPhone 14 Pro', headless: true }, log_level: 'debug' },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const openGroupDialog = async () => {
      const row = Array.from(container.querySelectorAll('.new-test-script-row')).find(element => element.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
      await act(async () => {
        row?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      return document.querySelector('.run-group-modal') as HTMLElement | null
    }

    const modal = await openGroupDialog()
    expect(modal).toBeTruthy()
    if (!modal) return
    const deviceProfile = findControlByLabel<HTMLInputElement>(modal, 'Device profile', 'input')
    const headless = findControlByLabel<HTMLSelectElement>(modal, 'Headless', 'select')
    expect(deviceProfile?.value).toBe('iPhone 14 Pro')
    expect(headless?.value).toBe('true')
    if (!deviceProfile || !headless) return

    await act(async () => {
      setInputValue(deviceProfile, '')
    })
    await act(async () => {
      setSelectValue(headless, '')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Saved script settings for this run only.')

    // Re-opening must show the clear, not the stored section the read path used to fall back to.
    const reopened = await openGroupDialog()
    expect(reopened).toBeTruthy()
    if (!reopened) return
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Device profile', 'input')?.value).toBe('')
    expect(findControlByLabel<HTMLSelectElement>(reopened, 'Headless', 'select')?.value).toBe('')
    const closeButton = Array.from(reopened.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    expect(payload.groups).toHaveLength(1)
    // The unedited sibling section survives; only the cleared browser section is removed.
    expect(payload.groups?.[0].runtime_settings).toEqual({ log_level: 'debug' })
  })

  it('clears a whole-section browser_config for this run instead of pinning an empty one', async () => {
    // Same operator action and same promise as the flat-shape test above, in the OTHER wire
    // shape the scenario editor writes (RuntimeSettingsGroupEditor's patchBrowserConfig always
    // emits browser_config). Leaving the section behind as `{}` is not a clear: the API applies
    // it as `script.Config.Browser = *cloneViaJSON(BrowserConfig)`, so the script's own engine,
    // viewport and timeouts are replaced with the zero value while the dialog says "Standard".
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'browser',
              vusers: 10,
              runtime_settings: { browser_config: { device_profile: 'iPhone 14 Pro', headless: true }, log_level: 'debug' },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const openGroupDialog = async () => {
      const row = Array.from(container.querySelectorAll('.new-test-script-row')).find(element => element.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
      await act(async () => {
        row?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      return document.querySelector('.run-group-modal') as HTMLElement | null
    }

    const modal = await openGroupDialog()
    expect(modal).toBeTruthy()
    if (!modal) return
    const deviceProfile = findControlByLabel<HTMLInputElement>(modal, 'Device profile', 'input')
    const headless = findControlByLabel<HTMLSelectElement>(modal, 'Headless', 'select')
    expect(deviceProfile?.value).toBe('iPhone 14 Pro')
    expect(headless?.value).toBe('true')
    if (!deviceProfile || !headless) return

    await act(async () => {
      setInputValue(deviceProfile, '')
    })
    await act(async () => {
      setSelectValue(headless, '')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Saved script settings for this run only.')

    const reopened = await openGroupDialog()
    expect(reopened).toBeTruthy()
    if (!reopened) return
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Device profile', 'input')?.value).toBe('')
    expect(findControlByLabel<HTMLSelectElement>(reopened, 'Headless', 'select')?.value).toBe('')
    const closeButton = Array.from(reopened.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    expect(payload.groups).toHaveLength(1)
    // Identical to the flat shape: the section is released, not replaced with an empty pin.
    expect(payload.groups?.[0].runtime_settings).toEqual({ log_level: 'debug' })
  })

  it('refuses to offer an error handling inherit option a group retry policy would keep pinned', async () => {
    // The dialog edits only the two booleans, so a stored section carrying retry fields can
    // never be emptied — the section stays pinned and (being applied whole) keeps executing both
    // booleans as false. Offering "Script default" there promises a clear that silently does
    // nothing, which is the exact defect this control exists to prevent.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: { max_retries: 3 } },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect?.value).toBe('false')
    expect(snapshotSelect?.value).toBe('false')
    for (const select of [continueSelect, snapshotSelect]) {
      const inheritOption = Array.from(select?.options || []).find(option => option.value === '')
      expect(inheritOption?.disabled).toBe(true)
      expect(inheritOption?.textContent).toBe('Pinned by group retry policy')
    }
  })

  it('releases a group error handling section back to the scenario default without re-seeding it', async () => {
    // The group pins nothing of its own; the scenario does. Picking the inherit option on BOTH
    // halves means "this group transmits no section", so the save must not seed one from the run
    // level — a seeded section is applied whole and would drive the scenario's continue_on_error
    // back to false, the opposite of the "Scenario default" the option promises.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: { error_handling: { continue_on_error: true, max_retries: 3 } },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect?.value).toBe('true')
    expect(snapshotSelect?.value).toBe('false')
    if (!continueSelect || !snapshotSelect) return
    // The group itself pins nothing, so the option is available and names the level that wins.
    const inheritOption = Array.from(continueSelect.options).find(option => option.value === '')
    expect(inheritOption?.disabled).toBe(false)
    expect(inheritOption?.textContent).toBe('Scenario default (enabled)')

    await act(async () => {
      setSelectValue(continueSelect, '')
    })
    await act(async () => {
      setSelectValue(snapshotSelect, '')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    // No group section at all: the scenario's continue_on_error:true and its retries execute.
    expect(payload.groups?.[0]?.runtime_settings).toBeUndefined()
  })

  it('releases the whole error handling section when the inherit option is picked on one half', async () => {
    // The inherit option is a SECTION-level statement: error_handling is applied whole, so a
    // group cannot stop pinning ONE half. Picking it on Continue on error alone used to leave
    // the other half behind as a brand new group section ({snapshot_on_error:true}), which
    // executes continue_on_error as false — the exact opposite of the "Scenario default" the
    // option names, on a group that pinned nothing before the edit.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: true } },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    expect(continueSelect?.value).toBe('true')
    expect(snapshotSelect?.value).toBe('true')
    if (!continueSelect || !snapshotSelect) return
    const inheritOption = Array.from(continueSelect.options).find(option => option.value === '')
    expect(inheritOption?.disabled).toBe(false)
    expect(inheritOption?.textContent).toBe('Scenario default (enabled)')

    await act(async () => {
      setSelectValue(continueSelect, '')
    })
    // Releasing is the only thing the option can mean, so the dialog shows it on both halves.
    expect(continueSelect.value).toBe('')
    expect(snapshotSelect.value).toBe('')

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    // No group section at all: the scenario's continue_on_error:true is what the run executes.
    expect(payload.groups?.[0]?.runtime_settings).toBeUndefined()
  })

  it('carries the scenario value into a half left on inherit when the other half re-pins the section', async () => {
    // Once the operator pins a half again the section is back on the wire, and it is applied
    // whole — so the half still showing "Scenario default" has to be written with the scenario's
    // value. Omitting it would execute false while the select names the scenario default.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: true } },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const continueSelect = findControlByLabel<HTMLSelectElement>(modal, 'Continue on error', 'select')
    const snapshotSelect = findControlByLabel<HTMLSelectElement>(modal, 'Snapshot on error', 'select')
    if (!continueSelect || !snapshotSelect) return

    await act(async () => {
      setSelectValue(continueSelect, '')
    })
    await act(async () => {
      setSelectValue(snapshotSelect, 'false')
    })
    expect(continueSelect.value).toBe('')
    expect(snapshotSelect.value).toBe('false')

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      groups: [expect.objectContaining({
        script_id: 'script-1',
        runtime_settings: { error_handling: { continue_on_error: true, snapshot_on_error: false } },
      })],
    }))
  })

  it('clears think time back to the script default instead of pinning the leftover mean', async () => {
    // "Script think time" clears the section, but the mean stayed in the draft, and
    // thinkTimeFromEdit only bails out on an EMPTY mean — so the inherit selection used to be
    // reconstituted as a FIXED pin, silently downgrading the stored distribution.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { think_time: { type: 'gaussian', mean_ms: 900, std_ms: 100 }, log_level: 'debug' },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const openGroupDialog = async () => {
      const row = Array.from(container.querySelectorAll('.new-test-script-row')).find(element => element.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
      await act(async () => {
        row?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
      return document.querySelector('.run-group-modal') as HTMLElement | null
    }

    const modal = await openGroupDialog()
    expect(modal).toBeTruthy()
    if (!modal) return
    const modeSelect = findControlByLabel<HTMLSelectElement>(modal, 'Think time mode', 'select')
    expect(modeSelect?.value).toBe('gaussian')
    expect(findControlByLabel<HTMLInputElement>(modal, 'Think time mean (ms)', 'input')?.value).toBe('900')
    if (!modeSelect) return

    await act(async () => {
      setSelectValue(modeSelect, '')
    })
    // The inherit option carries no mean, so the field goes away with the value it held.
    expect(findControlByLabel<HTMLInputElement>(modal, 'Think time mean (ms)', 'input')).toBeNull()

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const reopened = await openGroupDialog()
    expect(findControlByLabel<HTMLSelectElement>(reopened as HTMLElement, 'Think time mode', 'select')?.value).toBe('')
    const closeButton = Array.from((reopened as HTMLElement).querySelectorAll('button')).find(button => button.textContent === 'Cancel')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    expect(payload.groups?.[0]?.runtime_settings).toEqual({ log_level: 'debug' })
  })

  it('tombstones every runtime section the group dialog offers an inherit option for', async () => {
    // Construction guard for CLEARABLE_RUNTIME_SECTIONS, driven from BOTH ends so neither half
    // can go vacuous:
    //   * the stored group section is built FROM the constant, so a listed section cannot quietly
    //     drop out of the fixture and leave the terminal assertion with nothing to catch;
    //   * the inherit affordances are discovered from the rendered dialog and their count is
    //     pinned EXACTLY, so a newly clearable control fails here instead of silently re-applying
    //     the stored section at run time (which is how the browser section was missed twice).
    const storedSections: Record<ClearableRuntimeSection, Record<string, unknown>> = {
      pacing: { type: 'fixed_delay', delay_ms: 1000 },
      think_time: { type: 'gaussian', mean_ms: 900, std_ms: 100 },
      error_handling: { continue_on_error: true, snapshot_on_error: true },
      // The two browser wire shapes are one logical section: the dialog's four controls write
      // whichever one the level carries, and both have to end up tombstoned.
      browser: { device_profile: 'iPhone 14 Pro', headless: true, touch_mode: true, orientation: 'portrait' },
      browser_config: { device_profile: 'pixel_8', headless: false, touch_mode: false, orientation: 'landscape' },
    }
    const groupRuntimeSettings = Object.fromEntries(
      CLEARABLE_RUNTIME_SECTIONS.map(section => {
        expect(storedSections[section]).toBeTruthy()
        return [section, storedSections[section]]
      }),
    )
    expect(Object.keys(groupRuntimeSettings)).toHaveLength(CLEARABLE_RUNTIME_SECTIONS.length)
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'browser',
              vusers: 10,
              runtime_settings: groupRuntimeSettings,
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return

    const inheritControls = () => {
      const runtimeSections = Array.from(modal.querySelectorAll('section')).filter(section => {
        const heading = section.querySelector('h4')?.textContent?.trim() || ''
        return heading === 'Runtime Settings' || heading === 'Browser Runtime'
      })
      const controls: Array<HTMLSelectElement | HTMLInputElement> = []
      for (const section of runtimeSections) {
        section.querySelectorAll('select').forEach(select => {
          if (Array.from(select.options).some(option => option.value === '' && !option.disabled)) controls.push(select)
        })
        section.querySelectorAll('input').forEach(input => {
          if (/^(Script|Standard)/.test(input.placeholder || '')) controls.push(input)
        })
      }
      return controls
    }

    // Pacing, think time mode + mean, continue/snapshot on error, device profile, orientation,
    // headless, touch mode. Pinned EXACTLY: a lower count means the discovery selector stopped
    // matching and the guard would pass vacuously, and a higher one means a clearable control was
    // added to the dialog — which has to fail here until its section is carried through
    // CLEARABLE_RUNTIME_SECTIONS, the stored fixture above and the tombstone pass.
    expect(inheritControls().length).toBe(9)

    for (let pass = 0; pass < 20; pass += 1) {
      const pending = inheritControls().find(control => control.value !== '')
      if (!pending) break
      await act(async () => {
        if (pending instanceof HTMLSelectElement) setSelectValue(pending, '')
        else setInputValue(pending, '')
      })
    }
    expect(inheritControls().every(control => control.value === '')).toBe(true)

    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Saved script settings for this run only.')

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalled()
    const payload = apiMock.createTest.mock.calls[0][0] as { groups?: Array<Record<string, unknown>> }
    expect(payload.groups).toHaveLength(1)
    // Every stored section the dialog just cleared has to be gone from the submitted group.
    expect(payload.groups?.[0]?.runtime_settings).toBeUndefined()
  })

  it('clears a stored group pacing section back to script timing', async () => {
    // Same overlay limitation as the error handling clear: the pacing select's inherit option
    // ("Script timing") has to remove the group's stored section, not leave it standing.
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Checkout Smoke',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 }, log_level: 'debug' },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const groupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      groupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const pacingSelect = findControlByLabel<HTMLSelectElement>(modal, 'Pacing', 'select')
    expect(pacingSelect?.value).toBe('fixed_delay')
    if (!pacingSelect) return

    await act(async () => {
      setSelectValue(pacingSelect, '')
    })
    const saveButton = Array.from(modal.querySelectorAll('button')).find(button => button.textContent === 'Save For This Run') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      groups: [expect.objectContaining({
        script_id: 'script-1',
        runtime_settings: { log_level: 'debug' },
      })],
    }))
  })

  it('edits and round-trips random interval bounds in the per-script run editor', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Random pacing',
        duration: '10m',
        scenario_mode: 'vuser_driven',
        scripts: [{
          order: 1,
          name: 'Checkout',
          script_id: 'script-1',
          script_name: 'Checkout Script',
          protocol: 'http',
          vusers: 10,
        }],
        script_count: 1,
        created_at: '2026-05-12T20:00:00Z',
        updated_at: '2026-05-12T20:00:00Z',
        associated_releases: [],
      }],
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row')
    await act(async () => scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const groupRow = Array.from(container.querySelectorAll('.new-test-script-row'))
      .find(row => row.textContent?.includes('Checkout'))
    await act(async () => groupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return
    const pacing = findControlByLabel<HTMLSelectElement>(modal, 'Pacing', 'select')
    expect(pacing).toBeTruthy()
    if (!pacing) return
    await act(async () => setSelectValue(pacing, 'random_interval'))

    const minimum = findControlByLabel<HTMLInputElement>(modal, 'Minimum interval (seconds)', 'input')
    const maximum = findControlByLabel<HTMLInputElement>(modal, 'Maximum interval (seconds)', 'input')
    expect(minimum?.value).toBe('1')
    expect(maximum?.value).toBe('1')
    if (!minimum || !maximum) return
    await act(async () => {
      setInputValue(minimum, '2.5')
      setInputValue(maximum, '7.5')
    })
    const save = Array.from(modal.querySelectorAll('button'))
      .find(button => button.textContent === 'Save For This Run')
    await act(async () => save?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.querySelector('.new-test-script-table')?.textContent)
      .toContain('Random interval · 2.5–7.5 seconds')

    await act(async () => groupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const reopened = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(reopened).toBeTruthy()
    if (!reopened) return
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Minimum interval (seconds)', 'input')?.value).toBe('2.5')
    expect(findControlByLabel<HTMLInputElement>(reopened, 'Maximum interval (seconds)', 'input')?.value).toBe('7.5')
  })

  it('keeps staged group VUsers immutable at the peak stage target', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Staged Checkout',
        duration: '10m',
        scenario_mode: 'vuser_driven',
        scripts: [{
          order: 1,
          name: 'Checkout',
          script_id: 'script-1',
          script_name: 'Checkout Script',
          protocol: 'http',
          vusers: 20,
          stages: [
            { type: 'ramp_up', duration: '1m', target_vusers: 10 },
            { type: 'hold', duration: '8m', target_vusers: 30 },
            { type: 'ramp_down', duration: '1m', target_vusers: 0 },
          ],
        }],
        script_count: 1,
        created_at: '2026-05-12T20:00:00Z',
        updated_at: '2026-05-12T20:00:00Z',
        associated_releases: [],
      }],
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

    const scenarioRow = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row')
    await act(async () => scenarioRow?.click())
    const groupRow = container.querySelector<HTMLTableRowElement>('.new-test-script-row')
    await act(async () => groupRow?.click())

    const modal = document.querySelector<HTMLElement>('.run-group-modal')
    expect(modal).toBeTruthy()
    if (!modal) return
    const stagedVUsers = findControlByLabel<HTMLInputElement>(modal, 'Peak VUsers (stage-derived)', 'input')
    expect(stagedVUsers?.value).toBe('30')
    expect(stagedVUsers?.disabled).toBe(true)
    expect(modal.textContent).toContain('fixed at its peak stage target of 30 VUsers')
    expect(modal.textContent).toContain('Edit the scenario stages to change it')
    expect(container.textContent).toContain('Scenario: group 1 VUsers must match its peak stage target of 30')
    const holdButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Hold for Manual Start')
    expect(holdButton?.disabled).toBe(true)
  })

  it('portals the run-group dialog and keeps its header outside the scrollable body', async () => {
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

    const scenarioRow = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row')
    await act(async () => scenarioRow?.click())
    const groupRow = container.querySelector<HTMLTableRowElement>('.new-test-script-row')
    await act(async () => groupRow?.click())

    const modal = document.querySelector<HTMLElement>('.run-group-modal')
    expect(modal).toBeTruthy()
    if (!modal) return
    expect(container.contains(modal)).toBe(false)
    expect(modal.closest('.page')).toBeNull()
    expect(modal.parentElement?.classList.contains('modal-overlay')).toBe(true)
    expect(modal.parentElement?.parentElement).toBe(document.body)
    expect(modal.firstElementChild?.classList.contains('modal-header')).toBe(true)
    expect(modal.children.item(1)?.classList.contains('run-group-modal-body')).toBe(true)

    const closeButton = Array.from(modal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Close')
    await act(async () => closeButton?.click())
    expect(document.querySelector('.run-group-modal')).toBeNull()
  })

  it('blocks observer VUsers above ten with inline validation instead of a global diagnostic', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Browser Canary',
        duration: '10m',
        scenario_mode: 'vuser_driven',
        scripts: [{
          order: 1,
          name: 'Checkout Canary',
          script_id: 'script-1',
          script_name: 'Checkout Canary Script',
          protocol: 'browser',
          role: 'observer',
          vusers: 10,
        }],
        script_count: 1,
        created_at: '2026-05-12T20:00:00Z',
        updated_at: '2026-05-12T20:00:00Z',
        associated_releases: [],
      }],
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

    const scenarioRow = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row')
    await act(async () => scenarioRow?.click())
    const groupRow = container.querySelector<HTMLTableRowElement>('.new-test-script-row')
    await act(async () => groupRow?.click())

    const modal = document.querySelector<HTMLElement>('.run-group-modal')
    expect(modal).toBeTruthy()
    if (!modal) return
    const vusersInput = findControlByLabel<HTMLInputElement>(modal, 'VUsers', 'input')
    const saveButton = Array.from(modal.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save For This Run')
    expect(vusersInput?.max).toBe('10')
    expect(saveButton).toBeTruthy()
    if (!vusersInput || !saveButton) return

    await act(async () => setInputValue(vusersInput, '11'))

    const inlineError = modal.querySelector<HTMLElement>('#new-test-group-vusers-error')
    expect(vusersInput.getAttribute('aria-invalid')).toBe('true')
    expect(vusersInput.getAttribute('aria-describedby')).toBe('new-test-group-vusers-error')
    expect(inlineError?.getAttribute('role')).toBe('alert')
    expect(inlineError?.textContent).toBe('Checkout Canary: observer canaries must use between 1 and 10 VUsers.')
    expect(saveButton.disabled).toBe(true)
    expect(container.querySelector('.new-test-error-diagnostic')).toBeNull()

    await act(async () => saveButton.click())
    expect(document.querySelector('.run-group-modal')).toBe(modal)
    expect(container.querySelector('.new-test-error-diagnostic')).toBeNull()

    await act(async () => setInputValue(vusersInput, '9'))
    expect(modal.querySelector('#new-test-group-vusers-error')).toBeNull()
    expect(vusersInput.getAttribute('aria-invalid')).toBe('false')
    expect(saveButton.disabled).toBe(false)
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

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal).toBeTruthy()
    if (!modal) return

    const shardingRadio = Array.from(modal.querySelectorAll('label')).find(label => label.textContent?.includes('Pin to host'))?.querySelector('input') as HTMLInputElement | null
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
    expect(scriptGroupsTableText).toContain('Runtime / placement')
    expect(scriptGroupsTableText).toContain('Sharded: 2 hosts, capacity weighted')
    expect(container.textContent).toContain('Host Placement')
    expect(container.textContent).toContain('1 sharded group')

    await flushDebouncedPreflight()
    const createQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      createQueuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      reserve_launch_window: true,
      scheduled_start_at: '2026-05-12T20:00:00.000Z',
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
    expect(apiMock.startTest).not.toHaveBeenCalled()
  })

  it('uses saved per-group placement as the New Test baseline and clears it with explicit auto for launch only', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-1',
          name: 'Pinned Checkout',
          duration: '10m',
          scenario_mode: 'vuser_driven',
          scripts: [
            {
              order: 1,
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 30,
              ramp_up: '1m',
              assignment: { mode: 'specific_hosts', selected_targets: ['gen-a:9090'] },
            },
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

    const scenarioRow = container.querySelector('.new-test-scenario-table-row') as HTMLTableRowElement | null
    expect(scenarioRow).toBeTruthy()
    await act(async () => {
      scenarioRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.new-test-script-table')?.textContent).toContain('Pinned: gen-a:9090')

    const scriptGroupRow = Array.from(container.querySelectorAll('.new-test-script-row')).find(row => row.textContent?.includes('Checkout')) as HTMLTableRowElement | undefined
    await act(async () => {
      scriptGroupRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const modal = document.querySelector('.run-group-modal') as HTMLElement | null
    expect(modal?.textContent).toContain('Capacity estimates may include configured or local execution capacity')
    expect(modal?.textContent).toContain('gen-a:9090')
    const autoRadio = Array.from(modal?.querySelectorAll('label') || []).find(label => label.textContent?.includes('Automatic placement'))?.querySelector('input') as HTMLInputElement | null
    expect(autoRadio).toBeTruthy()
    await act(async () => {
      autoRadio?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const saveButton = Array.from(modal?.querySelectorAll('button') || []).find(button => button.textContent === 'Save For This Run')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.new-test-script-table')?.textContent).toContain('Auto')
    expect(container.textContent).toContain('Run override')

    await flushDebouncedPreflight()
    const reserveButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      reserveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        assignment: { mode: 'auto' },
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
    await flushPromises()

    expect(container.querySelector('.new-test-schedule-health-card')).toBeNull()
    expect(container.textContent).not.toContain('Schedule Health')
    expect(container.textContent).not.toContain('Enabled Schedules')
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

  it('blocks launch and explains a failed preflight instead of failing open', async () => {
    apiMock.preflightTest.mockRejectedValue(new Error('Capacity service unavailable'))
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()
    await flushDebouncedPreflight()

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Capacity service unavailable')
    const start = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Start Now')
    expect(start?.disabled).toBe(true)
    expect(apiMock.createTest).not.toHaveBeenCalled()
  })

  it('shows a disabled start action and queued-run action when immediate preflight returns queue', async () => {
    const selectTest = vi.fn()
    const openCommandCenter = vi.fn()
    apiMock.preflightTest.mockResolvedValue({
      decision: 'queue',
      start_now_allowed: false,
      schedule_allowed: false,
      queue_allowed: true,
      queue_reason: 'Controller concurrency is currently full.',
      candidate_window: {
        start_at: '2026-05-12T20:00:00Z',
        end_at: '2026-05-12T20:10:00Z',
        duration_seconds: 600,
      },
      blocking_conflicts: [],
      warnings: [{ code: 'current_capacity_unavailable', severity: 'warning', source: 'controller', message: 'Controller concurrency is currently full.' }],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={selectTest}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()
    await flushDebouncedPreflight()

    const startNowButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start') as HTMLButtonElement | undefined
    expect(startNowButton).toBeTruthy()
    expect(startNowButton?.disabled).toBe(true)
    const queuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    expect(queuedButton).toBeTruthy()

    await act(async () => {
      queuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await vi.waitFor(() => {
      expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
        scenario_id: 'scenario-1',
        release_id: 'release-1',
      }))
      expect(apiMock.startTest).toHaveBeenCalledWith('test-1')
      expect(openCommandCenter).toHaveBeenCalledWith('test-1')
    })
    expect(selectTest).not.toHaveBeenCalled()
  })

  it('recovers an uncertain queued-run create response by its client-requested ID', async () => {
    const openCommandCenter = vi.fn()
    let requestedTestId = ''
    apiMock.preflightTest.mockResolvedValue({
      decision: 'queue',
      start_now_allowed: false,
      schedule_allowed: false,
      queue_allowed: true,
      queue_reason: 'Controller concurrency is currently full.',
      candidate_window: { start_at: '2026-05-12T20:00:00Z', end_at: '2026-05-12T20:10:00Z', duration_seconds: 600 },
      blocking_conflicts: [],
      warnings: [],
    })
    apiMock.createTest.mockImplementation(async (payload: { requested_test_id?: string }) => {
      requestedTestId = payload.requested_test_id || ''
      throw new Error('connection closed after create')
    })
    apiMock.getTest.mockImplementation(async (id: string) => ({
      id,
      name: 'Recovered Queue Run',
      status: 'QUEUED',
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'release-1',
      scenario_id: 'scenario-1',
      total_vusers: 10,
      duration: '30m0s',
      created_at: '2026-05-12T20:00:00Z',
      groups: [],
    }))

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })
    await flushPromises()
    await flushDebouncedPreflight()

    let queuedButton: HTMLButtonElement | undefined
    await vi.waitFor(() => {
      queuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
      expect(queuedButton).toBeTruthy()
      expect(queuedButton?.disabled).toBe(false)
    })
    await act(async () => queuedButton!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await vi.waitFor(() => expect(openCommandCenter).toHaveBeenCalled())

    expect(requestedTestId).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({ requested_test_id: requestedTestId }))
    expect(apiMock.getTest).toHaveBeenCalledWith(requestedTestId)
    expect(apiMock.startTest).toHaveBeenCalledWith(requestedTestId)
    expect(openCommandCenter).toHaveBeenCalledWith(requestedTestId)
    expect(window.localStorage.length).toBe(0)
  })

  it('recovers the same queued run after unmount and remount instead of creating a duplicate', async () => {
    const openCommandCenter = vi.fn()
    let createdRun: Record<string, unknown> | null = null
    apiMock.preflightTest.mockResolvedValue({
      decision: 'queue',
      start_now_allowed: false,
      schedule_allowed: false,
      queue_allowed: true,
      queue_reason: 'Controller concurrency is currently full.',
      candidate_window: { start_at: '2026-05-12T20:00:00Z', end_at: '2026-05-12T20:10:00Z', duration_seconds: 600 },
      blocking_conflicts: [],
      warnings: [],
    })
    apiMock.createTest.mockImplementation(async (payload: { requested_test_id?: string }) => {
      createdRun = {
        id: payload.requested_test_id,
        name: 'Preserved Queue Run',
        status: 'QUEUED',
        project_id: 'project-1',
        application_id: 'app-1',
        release_id: 'release-1',
        scenario_id: 'scenario-1',
        total_vusers: 10,
        duration: '30m0s',
        created_at: '2026-05-12T20:00:00Z',
        groups: [],
      }
      return createdRun
    })
    apiMock.getTest.mockImplementation(async () => createdRun)
    apiMock.startTest.mockRejectedValueOnce(new Error('start acknowledgement lost')).mockResolvedValueOnce({})

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })
    await flushPromises()
    await flushDebouncedPreflight()

    const queuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    await vi.waitFor(() => expect(queuedButton?.disabled).toBe(false))
    await act(async () => queuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    const requestedTestId = apiMock.createTest.mock.calls[0][0].requested_test_id as string
    expect(container.textContent).toContain('queued run was preserved for recovery')
    expect(apiMock.createTest).toHaveBeenCalledTimes(1)
    expect(apiMock.startTest).toHaveBeenLastCalledWith(requestedTestId)
    expect(window.localStorage.length).toBe(1)

    await act(async () => root.unmount())
    root = createRoot(container)
    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })
    await flushPromises()
    await flushDebouncedPreflight()

    expect(container.textContent).toContain('Preserved queued run')
    const remountedRunName = container.querySelector<HTMLInputElement>('input[aria-labelledby="new-test-run-name-label"]')
    expect(remountedRunName).toBeTruthy()
    if (remountedRunName) {
      await act(async () => setInputValue(remountedRunName, 'Different replacement draft'))
    }
    await flushDebouncedPreflight()
    const remountedQueuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run') as HTMLButtonElement | undefined
    expect(remountedQueuedButton?.disabled).toBe(true)
    const resumeButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Resume queued run')
    await act(async () => resumeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    expect(apiMock.getTest).toHaveBeenCalledWith(requestedTestId)
    expect(apiMock.createTest).toHaveBeenCalledTimes(1)
    expect(apiMock.startTest).toHaveBeenCalledTimes(2)
    expect(apiMock.startTest).toHaveBeenLastCalledWith(requestedTestId)
    expect(window.localStorage.length).toBe(0)
  })

  it('requires explicit confirmation before replacing a preserved queued-run attempt', async () => {
    const preserved = persistNewTestQueueAttempt(
      'old-payload',
      'queued-existing',
      { projectId: 'project-1', applicationId: 'app-1' },
    )
    apiMock.getTest.mockResolvedValue({
      id: preserved.requestedTestId,
      name: 'Existing Queue Run',
      status: 'QUEUED',
      project_id: 'project-1',
      application_id: 'app-1',
      release_id: 'release-1',
      scenario_id: 'scenario-1',
      total_vusers: 10,
      duration: '30m0s',
      created_at: '2026-05-12T20:00:00Z',
      groups: [],
    })
    apiMock.preflightTest.mockResolvedValue({
      decision: 'queue',
      start_now_allowed: false,
      schedule_allowed: false,
      queue_allowed: true,
      queue_reason: 'Controller concurrency is currently full.',
      candidate_window: { start_at: '2026-05-12T20:00:00Z', end_at: '2026-05-12T20:10:00Z', duration_seconds: 600 },
      blocking_conflicts: [],
      warnings: [],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })
    await flushPromises()
    await flushDebouncedPreflight()

    expect(container.textContent).toContain('Existing Queue Run')
    const queuedButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create Queued Run') as HTMLButtonElement | undefined
    expect(queuedButton?.disabled).toBe(true)

    const createAnother = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create another run')
    await act(async () => createAnother?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.textContent).toContain('existing queued run was not deleted')
    expect(queuedButton?.disabled).toBe(false)
    expect(window.localStorage.length).toBe(0)

    await act(async () => queuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await vi.waitFor(() => expect(apiMock.createTest).toHaveBeenCalledTimes(1))
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      requested_test_id: expect.not.stringMatching(/^queued-existing$/),
    }))
  })

  it('binds a selected reserved timeslot to the run it launches', async () => {
    const openCommandCenter = vi.fn()
    apiMock.listTimeslots.mockResolvedValue({
      timeslots: [
        { id: 'slot-1', project_id: 'project-1', start_at: '2026-07-01T10:00:00Z', end_at: '2026-07-01T12:00:00Z', approval_state: 'approved', created_at: '', updated_at: '' },
        { id: 'slot-2', project_id: 'project-1', start_at: '2026-07-02T10:00:00Z', end_at: '2026-07-02T12:00:00Z', approval_state: 'pending', created_at: '', updated_at: '' },
        { id: 'slot-3', project_id: 'project-1', start_at: '2026-07-03T10:00:00Z', end_at: '2026-07-03T12:00:00Z', approval_state: 'approved', bound_test_id: 'other', created_at: '', updated_at: '' },
      ],
      count: 3,
    })
    apiMock.preflightTest.mockResolvedValue({
      decision: 'queue',
      start_now_allowed: false,
      schedule_allowed: false,
      queue_allowed: true,
      candidate_window: { start_at: '2026-05-12T20:00:00Z', end_at: '2026-05-12T20:10:00Z', duration_seconds: 600 },
      blocking_conflicts: [],
      warnings: [],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()
    await flushDebouncedPreflight()

    // Only the approved + unbound slot is offered (pending and already-bound are filtered out).
    const slotSelect = Array.from(container.querySelectorAll('select')).find(select =>
      Array.from(select.options).some(option => option.value === 'slot-1'),
    ) as HTMLSelectElement | undefined
    expect(slotSelect).toBeTruthy()
    expect(Array.from(slotSelect!.options).map(option => option.value)).toEqual(['', 'slot-1'])

    await act(async () => {
      slotSelect!.value = 'slot-1'
      slotSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushDebouncedPreflight()

    const queuedButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Create Queued Run')
    await act(async () => {
      queuedButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // The timeslot is now bound strictly as part of creation (timeslot_id in the
    // create payload), not via a best-effort post-create bind call.
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({ timeslot_id: 'slot-1' }))
    expect(apiMock.bindTimeslot).not.toHaveBeenCalled()
    expect(apiMock.startTest).toHaveBeenCalledWith('test-1')
    expect(openCommandCenter).toHaveBeenCalledWith('test-1')
  })

  it('reserves the next 15-minute timeslot without starting the run', async () => {
    const openCommandCenter = vi.fn()
    apiMock.preflightTest.mockResolvedValue({
      decision: 'start_now',
      start_now_allowed: true,
      schedule_allowed: false,
      queue_allowed: false,
      candidate_window: {
        start_at: '2026-05-12T20:07:30Z',
        end_at: '2026-05-12T20:17:30Z',
        duration_seconds: 600,
      },
      blocking_conflicts: [],
      warnings: [],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()
    await flushDebouncedPreflight()

    expect(container.textContent).toContain('Hold for Manual Start')
    expect(container.textContent).toContain('Proposed launch window')

    const reserveButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start')
    await act(async () => {
      reserveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      reserve_launch_window: true,
      scheduled_start_at: '2026-05-12T20:15:00.000Z',
    }))
    expect(apiMock.startTest).not.toHaveBeenCalled()
    expect(openCommandCenter).toHaveBeenCalledWith('test-1')
  })

  it('starts an immediately allowed run and opens its command center', async () => {
    const openCommandCenter = vi.fn()
    apiMock.preflightTest.mockResolvedValue({
      decision: 'start_now',
      start_now_allowed: true,
      schedule_allowed: false,
      queue_allowed: false,
      candidate_window: {
        start_at: '2026-05-12T20:07:30Z',
        end_at: '2026-05-12T20:17:30Z',
        duration_seconds: 600,
      },
      blocking_conflicts: [],
      warnings: [],
    })

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
          onSelectTest={vi.fn()}
          onOpenCommandCenter={openCommandCenter}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
    })

    await flushPromises()
    await flushDebouncedPreflight()

    const startButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Start Now')
    expect(startButton).toBeTruthy()
    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      release_id: 'release-1',
    }))
    expect(apiMock.createTest).not.toHaveBeenCalledWith(expect.objectContaining({ reserve_launch_window: true }))
    expect(apiMock.startTest).toHaveBeenCalledWith('test-1')
    expect(openCommandCenter).toHaveBeenCalledWith('test-1')
  })

  it('sends scheduled_start_at when one-time scheduled preflight is allowed', async () => {
    const selectTest = vi.fn()
    const scheduledLocal = '2026-06-02T13:30'
    const scheduledISO = new Date(scheduledLocal).toISOString()
    apiMock.preflightTest.mockImplementation((payload: { intent?: string }) => Promise.resolve(payload.intent === 'scheduled'
      ? {
          decision: 'schedule',
          start_now_allowed: false,
          schedule_allowed: true,
          queue_allowed: false,
          candidate_window: {
            start_at: scheduledISO,
            end_at: '2026-06-02T14:00:00Z',
            duration_seconds: 1800,
          },
          blocking_conflicts: [],
          warnings: [],
        }
      : {
          decision: 'start_now',
          start_now_allowed: true,
          schedule_allowed: false,
          queue_allowed: false,
          candidate_window: {
            start_at: '2026-05-12T20:00:00Z',
            end_at: '2026-05-12T20:10:00Z',
            duration_seconds: 600,
          },
          blocking_conflicts: [],
          warnings: [],
        }))

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
    await flushDebouncedPreflight()
    const scheduledInput = container.querySelector('input[type="datetime-local"]') as HTMLInputElement | null
    expect(scheduledInput).toBeTruthy()

    await act(async () => {
      if (scheduledInput) setInputValue(scheduledInput, scheduledLocal)
    })
    await flushDebouncedPreflight()

    expect(container.textContent).toContain('Scheduled window clear')
    const scheduleButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedule Run')
    expect((scheduleButton as HTMLButtonElement | undefined)?.disabled).toBe(false)

    await act(async () => {
      scheduleButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.preflightTest).toHaveBeenCalledWith(expect.objectContaining({
      intent: 'scheduled',
      scheduled_start_at: scheduledISO,
    }))
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      scenario_id: 'scenario-1',
      scheduled_start_at: scheduledISO,
    }))
    expect(apiMock.startTest).not.toHaveBeenCalled()
    expect(selectTest).toHaveBeenCalledWith('test-1')
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

    await flushDebouncedPreflight()
    const startNowButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Hold for Manual Start') as HTMLButtonElement | undefined
    const scheduleButton = Array.from(container.querySelectorAll('button')).find(element => element.textContent === 'Schedule Run') as HTMLButtonElement | undefined
    expect(startNowButton?.disabled).toBe(true)
    expect(scheduleButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Current session can view scenarios but cannot create runs or schedules for this project.')
    expect(container.querySelector('[role="alert"]')?.classList.contains('page-error-banner')).toBe(true)

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

  it('does not show an RBAC denial while project access is still loading', async () => {
    apiMock.getAuthEffectiveAccess.mockImplementation(() => new Promise(() => {}))

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
      await Promise.resolve()
    })

    expect(apiMock.getAuthEffectiveAccess).toHaveBeenCalled()
    expect(container.textContent).not.toContain('Current session can view scenarios but cannot create runs or schedules for this project.')
  })

  it('shows a retryable access error instead of misreporting an RBAC denial', async () => {
    apiMock.getAuthEffectiveAccess
      .mockRejectedValueOnce(new Error('access service unavailable'))
      .mockResolvedValueOnce({
        enabled: true,
        authenticated: true,
        project_id: 'project-1',
        resources: [{
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: 'Scenario definitions and runs.',
          operations: [{ id: 'manage_tests', label: 'Manage tests', allowed: true, grant_source: 'project' }],
        }],
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

    expect(container.textContent).toContain('access service unavailable')
    expect(container.textContent).not.toContain('Current session can view scenarios but cannot create runs or schedules for this project.')
    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry permissions')
    expect(retry).toBeTruthy()

    await act(async () => retry?.click())
    await flushPromises()
    expect(apiMock.getAuthEffectiveAccess).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('access service unavailable')
  })

  it('waits for a project scope and ignores scenarios returned from the previous project', async () => {
    let resolveProjectOne: ((value: { scenarios: Array<Record<string, unknown>> }) => void) | undefined
    apiMock.listScenarios
      .mockImplementationOnce(() => new Promise(resolve => { resolveProjectOne = resolve }))
      .mockResolvedValueOnce({
        scenarios: [{
          id: 'scenario-project-2',
          name: 'Project Two Scenario',
          duration: '5m',
          scenario_mode: 'vuser_driven',
          scripts: [],
          script_count: 0,
          created_at: '',
          updated_at: '',
          associated_releases: [],
        }],
      })

    const renderPage = () => root.render(
      <NewTestPage
        activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
        activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
        releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
        onSelectTest={vi.fn()}
        onCreateRelease={vi.fn().mockResolvedValue(null)}
      />,
    )

    await act(async () => {
      renderPage()
      await Promise.resolve()
    })
    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1', 'all')

    projectMock.activeProject = { id: 'project-2', name: 'Project Two', created_at: '', updated_at: '' }
    await act(async () => {
      renderPage()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-2', 'all')
    expect(container.textContent).toContain('Project Two Scenario')

    await act(async () => {
      resolveProjectOne?.({ scenarios: [{
        id: 'scenario-project-1',
        name: 'Stale Project One Scenario',
        duration: '5m',
        scenario_mode: 'vuser_driven',
        scripts: [],
        script_count: 0,
        created_at: '',
        updated_at: '',
        associated_releases: [],
      }] })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Project Two Scenario')
    expect(container.textContent).not.toContain('Stale Project One Scenario')
  })

  it('does not issue project-scoped requests before a project is resolved', async () => {
    projectMock.activeProject = null

    await act(async () => {
      root.render(
        <NewTestPage
          activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
          activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
          releases={[]}
          onSelectTest={vi.fn()}
          onCreateRelease={vi.fn().mockResolvedValue(null)}
        />,
      )
      await Promise.resolve()
    })

    expect(apiMock.listScenarios).not.toHaveBeenCalled()
    expect(apiMock.listTimeslots).not.toHaveBeenCalled()
    expect(apiMock.getAuthEffectiveAccess).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Current session can view scenarios but cannot create runs or schedules for this project.')
  })

  it('traps focus in the run-default dialog, closes on Escape, and restores its opener', async () => {
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

    const runtimeDefaultsToggle = container.querySelector<HTMLElement>('.new-test-runtime-defaults summary')
    await act(async () => runtimeDefaultsToggle?.click())
    const opener = Array.from(container.querySelectorAll<HTMLButtonElement>('button.new-test-default-card'))
      .find(button => button.textContent?.includes('Log Level'))
    expect(opener).toBeDefined()
    opener?.focus()
    await act(async () => {
      opener?.click()
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = container.querySelector<HTMLElement>('.run-default-modal')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('new-test-run-default-dialog-title')
    expect(dialog?.contains(document.activeElement)).toBe(true)
    const focusable = Array.from(dialog?.querySelectorAll<HTMLButtonElement>('button:not([disabled])') || [])
    focusable[focusable.length - 1]?.focus()
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    })
    expect(document.activeElement).toBe(focusable[0])

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.querySelector('.run-default-modal')).toBeNull()
    expect(document.activeElement).toBe(opener)
  })

  it('programmatically labels run setup and repeated schedule-window controls', async () => {
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

    expect(container.querySelector('[aria-labelledby="new-test-run-name-label"]')).toBeInstanceOf(HTMLInputElement)
    expect(container.querySelector('[aria-labelledby="new-test-release-label"]')).toBeInstanceOf(HTMLSelectElement)
    const scenarioRow = container.querySelector<HTMLTableRowElement>('.new-test-scenario-table-row')
    expect(scenarioRow?.tabIndex).toBe(0)

    const schedulesButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Schedules')
    await act(async () => schedulesButton?.click())

    expect(container.querySelector('[aria-labelledby="schedule-scenario-label"]')).toBeInstanceOf(HTMLSelectElement)
    expect(container.querySelector('[aria-labelledby="schedule-name-label"]')).toBeInstanceOf(HTMLInputElement)
    expect(container.querySelector('[aria-labelledby="schedule-timezone-label"]')).toBeInstanceOf(HTMLSelectElement)
    expect(container.querySelector('[aria-labelledby="schedule-release-label"]')).toBeInstanceOf(HTMLSelectElement)

    const addWindowButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === '+ Add window')
    await act(async () => addWindowButton?.click())
    expect(container.querySelector('[aria-label="Blackout windows window 1 name"]')).toBeInstanceOf(HTMLInputElement)
    expect(container.querySelector('[aria-label="Blackout windows window 1 start time"]')).toBeInstanceOf(HTMLInputElement)
    expect(container.querySelector('[aria-label="Blackout windows window 1 end time"]')).toBeInstanceOf(HTMLInputElement)
    expect(container.querySelector('[aria-label="Blackout windows window 1 days of week"]')).not.toBeNull()
  })
  async function renderAuditPage() {
    await act(async () => root.render(<NavigationGuardProvider><NewTestPage
      activeApplication={{ id: 'app-1', name: 'ACC', created_at: '', updated_at: '' }}
      activeRelease={{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }}
      releases={[{ id: 'release-1', name: 'PI29', application_id: 'app-1', created_at: '', updated_at: '' }]}
      onSelectTest={vi.fn()} onCreateRelease={vi.fn().mockResolvedValue(null)}
    /></NavigationGuardProvider>))
    await flushPromises()
  }
  const auditButton = (name: string) => Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === name)

  it('keeps the selected scenario identity visible when filtering excludes its row', async () => {
    await renderAuditPage()
    const search = container.querySelector<HTMLInputElement>('input[type="search"]')!
    await act(async () => setInputValue(search, 'No matching scenario'))
    expect(container.querySelectorAll('.new-test-scenario-table-row')).toHaveLength(0)
    expect(container.querySelector('[aria-label="Selected scenario"]')?.textContent).toContain('scenario-1')
    expect(container.querySelector('.new-test-launch-card')?.textContent).toContain('scenario-1')
    await act(async () => auditButton('Show selected scenario')?.click())
    expect(container.querySelectorAll('.new-test-scenario-table-row')).toHaveLength(1)
  })

  it('restores a saved run draft after unmount without executing or modifying a schedule', async () => {
    await renderAuditPage()
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')!, 'Saved draft run'))
    await act(async () => auditButton('Save draft')?.click())
    expect(readNewTestDraft('project-1', 'app-1')?.runName).toBe('Saved draft run')
    const catalog = await apiMock.listScenarios('project-1')
    let finishCatalog!: (value: unknown) => void
    apiMock.listScenarios.mockReturnValue(new Promise(resolve => { finishCatalog = resolve }))
    await act(async () => root.render(null))
    await renderAuditPage()
    expect(auditButton('Restore saved draft')?.disabled).toBe(true)
    expect(container.textContent).toContain('The scenario list must load')
    await act(async () => { finishCatalog(catalog) })
    expect(auditButton('Restore saved draft')?.disabled).toBe(false)
    await act(async () => auditButton('Restore saved draft')?.click())
    await flushPromises()
    expect(container.querySelector<HTMLInputElement>('[aria-labelledby="new-test-run-name-label"]')?.value).toBe('Saved draft run')
    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(apiMock.startTest).not.toHaveBeenCalled()
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
  })

  it('keeps a missing saved reservation bound and applies current policies when restoring a new schedule draft', async () => {
    await renderAuditPage()
    await act(async () => auditButton('Save draft')?.click())
    const saved = readNewTestDraft('project-1', 'app-1')!
    saveNewTestDraft({ ...saved, selectedTimeslotId: 'missing-reservation' })
    await act(async () => root.render(null))
    apiMock.getSystemSettings.mockResolvedValue({ schedule_defaults: { approval_policy: 'manual_review', missed_run_policy: 'skip' } })
    await renderAuditPage()
    await act(async () => auditButton('Restore saved draft')?.click())
    await flushPromises()
    expect(container.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-timeslot-label"]')?.value).toBe('missing-reservation')
    expect(auditButton('Start Now')?.disabled).toBe(true)
    expect(container.textContent).toContain('saved capacity reservation is unavailable')
    await act(async () => auditButton('Schedules')?.click())
    expect(container.querySelector('[aria-label="Effective schedule policies"]')?.textContent).toContain('manual review')
    expect(apiMock.createTest).not.toHaveBeenCalled()
  })

  it('explains empty required schedule fields instead of only disabling save', async () => {
    await renderAuditPage()
    await act(async () => auditButton('Schedules')?.click())
    await act(async () => {
      setInputValue(container.querySelector<HTMLInputElement>('[aria-labelledby="schedule-name-label"]')!, '')
      setInputValue(container.querySelector<HTMLInputElement>('[aria-label="Raw cron expression"]')!, '')
    })
    expect(auditButton('Save Schedule')?.disabled).toBe(true)
    expect(container.querySelector('#new-test-schedule-name-error')?.textContent).toBe('Enter a schedule name.')
    expect(container.querySelector('#new-test-cron-error')?.textContent).toContain('Enter a schedule frequency')
    expect(container.querySelector('[aria-labelledby="schedule-name-label"]')?.getAttribute('aria-invalid')).toBe('true')
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
  })

  it('blocks invalid cron and hides stale occurrences while the new preview is pending', async () => {
    await renderAuditPage()
    await act(async () => auditButton('Schedules')?.click())
    await flushDebouncedPreflight()
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-release-label"]')!, 'release-1'))
    expect(auditButton('Save Schedule')?.disabled).toBe(false)
    apiMock.previewScenarioSchedule.mockRejectedValueOnce(new Error('Invalid cron expression'))
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('[aria-label="Raw cron expression"]')!, 'invalid'))
    expect(auditButton('Save Schedule')?.disabled).toBe(true)
    expect(container.querySelector('.cron-next-runs')).toBeNull()
    expect(container.textContent).toContain('Refreshing next runs')
    await flushDebouncedPreflight()
    expect(container.querySelector('[aria-label="Raw cron expression"]')?.getAttribute('aria-invalid')).toBe('true')
    expect(auditButton('Save Schedule')?.disabled).toBe(true)
    await act(async () => auditButton('Save Schedule')?.click())
    expect(apiMock.createScenarioSchedule).not.toHaveBeenCalled()
  })

  it('does not claim that fallback schedule policies are verified after a settings failure', async () => {
    apiMock.getSystemSettings.mockRejectedValue(new Error('Settings unavailable'))
    await renderAuditPage()
    await act(async () => auditButton('Schedules')?.click())
    await flushDebouncedPreflight()
    expect(auditButton('Save Schedule')?.disabled).toBe(true)
    expect(container.textContent).toContain('Scheduling defaults could not be loaded')
    apiMock.getSystemSettings.mockResolvedValue({ schedule_defaults: { approval_policy: 'manual_review', missed_run_policy: 'skip' } })
    await act(async () => auditButton('Retry scheduling defaults')?.click())
    await flushPromises()
    expect(container.querySelector('[aria-label="Effective schedule policies"]')?.textContent).toContain('manual review')
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-labelledby="schedule-release-label"]')!, 'release-1'))
    expect(auditButton('Save Schedule')?.disabled).toBe(false)
  })

  it('reviews nested assertions, safe target origins, missing SLAs and unknown monitoring coverage', async () => {
    apiMock.getScript.mockResolvedValue({ script: { config: { base_url: 'https://private:secret@example.com/hidden?key=hidden' }, actions: [{ type: 'if', steps: [{ type: 'http', url: '/orders', assertions: [{ type: 'status', expected: 200 }] }] }] } })
    await renderAuditPage()
    await flushPromises()
    const review = container.querySelector('[aria-label="Run readiness review"]')!
    expect(review.textContent).toContain('https://example.com')
    expect(review.textContent).not.toContain('private:secret')
    expect(review.textContent).not.toContain('?key=hidden')
    expect(review.textContent).toContain('1 script assertions configured')
    expect(review.textContent).toContain('No group response-time SLA configured')
    expect(review.textContent).toContain('Target monitoring coverage is not verified')
  })

})

async function selectLaunchRelease() {
  const select = document.querySelector<HTMLSelectElement>('[aria-labelledby="new-test-release-label"]')
  if (select && !select.value) await act(async () => setSelectValue(select, 'release-1'))
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function flushDebouncedPreflight() {
  await selectLaunchRelease()
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 450))
  })
  await flushPromises()
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
