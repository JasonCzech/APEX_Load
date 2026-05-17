// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import NewTestPage from './NewTestPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  listScenarioSchedules: vi.fn(),
  createScenarioSchedule: vi.fn(),
  updateScenarioSchedule: vi.fn(),
  deleteScenarioSchedule: vi.fn(),
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
    apiMock.createScenarioSchedule.mockResolvedValue({ id: 'schedule-1' })
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
      enabled: true,
    }))
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
