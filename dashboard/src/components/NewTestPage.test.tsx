// @vitest-environment jsdom

import { act } from 'react'
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
  startTest: vi.fn(),
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
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
  })

  it('renders duplicate release names without duplicate key warnings', async () => {
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
    expect(releaseTags).toEqual(['PI29', 'PI29'])
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

  it('filters and selects launch scenarios from workload cards', async () => {
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
    const searchInput = Array.from(container.querySelectorAll('input[type="search"]')).find(input => input.getAttribute('placeholder')?.includes('Find by scenario')) as HTMLInputElement | undefined
    expect(searchInput).toBeDefined()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'java_http')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Java Batch Review')
    expect(container.querySelectorAll('.new-test-scenario-card')).toHaveLength(1)

    const javaCard = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Java Batch Review'))
    expect(javaCard).toBeDefined()
    if (!javaCard) return

    await act(async () => {
      javaCard.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Goal Oriented')
    expect(container.textContent).toContain('40')
    expect(container.textContent).toContain('Batch Script')

    await act(async () => {
      setInputValue(searchInput, 'does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match this search.')
  })

  it('surfaces governed schedule policies and approval actions', async () => {
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
