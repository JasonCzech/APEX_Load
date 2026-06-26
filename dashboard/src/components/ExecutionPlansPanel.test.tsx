// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ExecutionPlansPanel from './ExecutionPlansPanel'
import { ApiError } from '../hooks/apiClient'
import { ExecutionPlan } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const plansApiMock = {
  listExecutionPlans: vi.fn(),
  getExecutionPlan: vi.fn(),
  createExecutionPlan: vi.fn(),
  updateExecutionPlan: vi.fn(),
  deleteExecutionPlan: vi.fn(),
  runExecutionPlan: vi.fn(),
  listExecutionPlanRuns: vi.fn(),
  getExecutionPlanRun: vi.fn(),
  listExecutionPlanSchedules: vi.fn(),
  createExecutionPlanSchedule: vi.fn(),
  updateExecutionPlanSchedule: vi.fn(),
  deleteExecutionPlanSchedule: vi.fn(),
  listExecutionPlanScheduleOccurrences: vi.fn(),
  listExecutionPlanApprovals: vi.fn(),
  approveExecutionPlanApproval: vi.fn(),
  rejectExecutionPlanApproval: vi.fn(),
}

vi.mock('../hooks/api/useExecutionPlansApi', () => ({
  useExecutionPlansApi: () => plansApiMock,
}))

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

// Drive a React-controlled input (onChange listens for the native input event).
function setValue(el: Element | null, value: string) {
  if (!el) throw new Error('input not found')
  const input = el as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function plan(overrides: Partial<ExecutionPlan>): ExecutionPlan {
  return {
    id: 'p1',
    project_id: 'proj-a',
    name: 'nightly',
    run_mode: 'sequential',
    entries: [
      { order: 0, scenario_id: 's1', enabled: true },
      { order: 1, scenario_id: 's2', enabled: false },
    ],
    status: 'draft',
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('ExecutionPlansPanel', () => {
  it('shows the empty state and New action for an active project', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(plansApiMock.listExecutionPlans).toHaveBeenCalledWith({ project_id: 'proj-a' })
    expect(container.textContent).toContain('No execution plans yet')
    expect(container.textContent).toContain('New Execution Plan')
  })

  it('renders plans with run-mode pill and enabled-entry count', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.textContent).toContain('nightly')
    expect(container.textContent).toContain('sequential')
    expect(container.querySelector('.pp-pill-info')).toBeTruthy()
    expect(container.textContent).toContain('2 entries')
    expect(container.textContent).toContain('1 enabled')
    // Manage actions present.
    expect(container.textContent).toContain('Run')
  })

  it('hides management actions without permission', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage={false} />)
    })
    await flush()
    expect(container.textContent).not.toContain('New Execution Plan')
    expect(container.textContent).not.toContain('Run')
  })

  it('prompts to select a project when none is active', async () => {
    await act(async () => {
      root.render(<ExecutionPlansPanel canManage />)
    })
    await flush()
    expect(plansApiMock.listExecutionPlans).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Select a project')
  })

  it('runs a sequential plan and shows gated per-entry progress', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.runExecutionPlan.mockResolvedValue({
      execution_plan_id: 'p1',
      run_mode: 'sequential',
      run_id: 'epr-1',
      status: 'running',
      results: [
        { order: 0, scenario_id: 's1', test_id: 't1', status: 'running' },
        { order: 1, scenario_id: 's2', status: 'pending' },
      ],
      materialized_at: '',
    })
    plansApiMock.getExecutionPlanRun.mockResolvedValue({
      id: 'epr-1',
      plan_id: 'p1',
      run_mode: 'sequential',
      status: 'completed',
      entries: [
        { order: 0, scenario_id: 's1', test_id: 't1', status: 'complete' },
        { order: 1, scenario_id: 's2', test_id: 't2', status: 'complete' },
      ],
      created_at: '',
      updated_at: '',
    })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const runBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Run')
    await act(async () => {
      ;(runBtn as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.runExecutionPlan).toHaveBeenCalledWith('p1')
    expect(plansApiMock.getExecutionPlanRun).toHaveBeenCalledWith('p1', 'epr-1')
    expect(container.textContent).toContain('each entry starts only after the previous run finishes')
    expect(container.textContent).toContain('completed')
  })

  it('authors per-entry timeslot and data pools into the create payload', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    plansApiMock.createExecutionPlan.mockResolvedValue(plan({}))
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const newBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'New Execution Plan')
    await act(async () => {
      ;(newBtn as HTMLElement).click()
    })
    await flush()
    await act(async () => {
      setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'plan x')
      setValue(container.querySelector('input[placeholder="scenario id"]'), 's1')
      setValue(container.querySelector('input[placeholder="optional timeslot id"]'), 'slot-1')
    })
    await flush()
    const addPoolBtn = [...container.querySelectorAll('button')].find(b => b.textContent === '+ data pool')
    await act(async () => {
      ;(addPoolBtn as HTMLElement).click()
    })
    await flush()
    await act(async () => {
      setValue(container.querySelector('input[placeholder="pool id"]'), 'pool-1')
      setValue(container.querySelector('input[placeholder="alias"]'), 'users')
    })
    await flush()
    const createBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Create')
    await act(async () => {
      ;(createBtn as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.createExecutionPlan).toHaveBeenCalledTimes(1)
    const payload = plansApiMock.createExecutionPlan.mock.calls[0][0] as {
      entries: { timeslot_id?: string; data_pools?: { pool_id: string; alias: string }[] }[]
    }
    expect(payload.entries[0].timeslot_id).toBe('slot-1')
    expect(payload.entries[0].data_pools?.[0]).toMatchObject({ pool_id: 'pool-1', alias: 'users' })
  })

  it('opens the schedules modal for a plan', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const schedBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Schedules')
    await act(async () => {
      ;(schedBtn as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.listExecutionPlanSchedules).toHaveBeenCalledWith('p1')
    expect(container.textContent).toContain('Schedules — “nightly”')
    expect(container.textContent).toContain('No schedules yet')
  })

  it('creates a plan schedule with the authored cron + policy payload', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
    plansApiMock.createExecutionPlanSchedule.mockResolvedValue({ id: 'sch-1' })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Schedules') as HTMLElement).click()
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'New schedule') as HTMLElement).click()
    })
    await flush()
    await act(async () => {
      setValue(container.querySelector('input[placeholder="e.g. nightly"]'), 'nightly run')
      setValue(container.querySelector('input[aria-label="Raw cron expression"]'), '0 3 * * 1-5')
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Create') as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.createExecutionPlanSchedule).toHaveBeenCalledTimes(1)
    const [planId, payload] = plansApiMock.createExecutionPlanSchedule.mock.calls[0] as [string, { name: string; cron_expr: string; enabled?: boolean; missed_run_policy?: string }]
    expect(planId).toBe('p1')
    expect(payload.name).toBe('nightly run')
    expect(payload.cron_expr).toBe('0 3 * * 1-5')
    expect(payload.enabled).toBe(true)
    expect(payload.missed_run_policy).toBe('skip')
  })

  it('surfaces a quota-blocked run by opening the approvals modal', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.runExecutionPlan.mockRejectedValue(
      new ApiError('blocked', 409, {
        execution_plan_id: 'p1',
        status: 'blocked',
        reason: 'approval_required',
        approval: { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', requested_at: '' },
      }),
    )
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({
      approvals: [
        { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', required_role: 'ProjectAdmin', requested_at: '', created_at: '', updated_at: '' },
      ],
      count: 1,
    })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const runBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Run')
    await act(async () => {
      ;(runBtn as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.runExecutionPlan).toHaveBeenCalledWith('p1')
    expect(plansApiMock.listExecutionPlanApprovals).toHaveBeenCalledWith('p1')
    expect(container.textContent).toContain('needs project-admin approval')
    expect(container.textContent).toContain('Approvals — “nightly”')
    // The pending request is actionable.
    expect([...container.querySelectorAll('button')].some(b => b.textContent === 'Approve')).toBe(true)
  })

  it('approves a pending plan approval with the entered reason', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({
      approvals: [
        { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', required_role: 'ProjectAdmin', requested_at: '', created_at: '', updated_at: '' },
      ],
      count: 1,
    })
    plansApiMock.approveExecutionPlanApproval.mockResolvedValue({ id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'approved', requested_at: '', created_at: '', updated_at: '' })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const apprBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Approvals')
    await act(async () => {
      ;(apprBtn as HTMLElement).click()
    })
    await flush()
    await act(async () => {
      setValue(container.querySelector('.pp-approval-reason'), 'capacity confirmed')
    })
    await flush()
    const approveBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Approve')
    await act(async () => {
      ;(approveBtn as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.approveExecutionPlanApproval).toHaveBeenCalledWith('p1', 'appr-1', 'capacity confirmed')
  })

  it('opens a pre-seeded draft from a handed-in scenario and consumes the seed', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    const onSeedConsumed = vi.fn()
    await act(async () => {
      root.render(
        <ExecutionPlansPanel
          projectId="proj-a"
          canManage
          scenarioOptions={[{ id: 's1', name: 'Checkout' }]}
          seedScenarioId="s1"
          onSeedConsumed={onSeedConsumed}
        />,
      )
    })
    await flush()
    // Draft modal opens, pre-named from the scenario, and the seed is consumed.
    expect(container.textContent).toContain('New Execution Plan')
    const nameInput = container.querySelector<HTMLInputElement>('.pp-modal-form input')
    expect(nameInput?.value).toContain('Checkout')
    expect(onSeedConsumed).toHaveBeenCalledTimes(1)
  })
})
