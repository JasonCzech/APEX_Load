// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ExecutionPlansPanel from './ExecutionPlansPanel'
import { ApiError } from '../hooks/apiClient'
import { ExecutionPlan } from '../types'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

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

const confirmActionMock = vi.hoisted(() => vi.fn<(message: string) => Promise<boolean>>(async () => true))
vi.mock('./ux/confirmAction', () => ({ confirmAction: confirmActionMock }))

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
    revision: 7,
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
    expect(plansApiMock.listExecutionPlans).toHaveBeenCalledWith({ project_id: 'proj-a', limit: 100, offset: 0 })
    expect(container.textContent).toContain('No execution plans yet')
    expect(container.textContent).toContain('New Execution Plan')
  })

  it('exposes the plan editor as a labelled modal and closes it with Escape', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const newButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Execution Plan')
    await act(async () => newButton?.click())

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('execution-plan-editor-title')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')

    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).toBeNull()
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

  it('sends the current revision when saving an existing plan', async () => {
    const existing = plan({ revision: 7 })
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [existing], count: 1 })
    plansApiMock.updateExecutionPlan.mockResolvedValue(plan({ revision: 8, name: 'renamed' }))
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')?.click()
    })
    await act(async () => setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'renamed'))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('.pp-modal-actions button')).find(button => button.textContent === 'Save')?.click()
    })
    await flush()
    expect(plansApiMock.updateExecutionPlan).toHaveBeenCalledWith('p1', expect.objectContaining({ revision: 7, name: 'renamed' }))
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

  it('asks before starting load and describes a parallel run accurately', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ run_mode: 'parallel' })], count: 1 })
    plansApiMock.runExecutionPlan.mockResolvedValue({ execution_plan_id: 'p1', run_mode: 'parallel', run_id: 'epr-2', status: 'running', results: [{ order: 0, scenario_id: 's1', test_id: 't1', status: 'created' }], materialized_at: '' })
    plansApiMock.getExecutionPlanRun.mockResolvedValue({ id: 'epr-2', plan_id: 'p1', run_mode: 'parallel', status: 'completed', entries: [{ order: 0, scenario_id: 's1', test_id: 't1', status: 'created' }], created_at: '', updated_at: '' })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-a" canManage />) })
    await flush()
    const runBtn = () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Run') as HTMLElement
    confirmActionMock.mockResolvedValueOnce(false)
    await act(async () => { runBtn().click() })
    await flush()
    expect(confirmActionMock.mock.calls[0][0]).toContain('all at the same time')
    expect(plansApiMock.runExecutionPlan).not.toHaveBeenCalled()
    await act(async () => { runBtn().click() })
    await flush()
    expect(plansApiMock.runExecutionPlan).toHaveBeenCalledWith('p1')
    expect(container.textContent).toContain('parallel — every enabled entry starts at once')
    expect(container.textContent).toContain('All runs started')
    expect(container.textContent).not.toContain('each entry starts only after the previous run finishes')
  })

  it('names the entries and the real outcome when a plan run only partly starts', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ run_mode: 'parallel', entries: [{ order: 0, scenario_id: 's1', scenario_name: 'Browse catalog', enabled: true }, { order: 1, scenario_id: 's2', scenario_name: 'Checkout', enabled: true }] })], count: 1 })
    plansApiMock.runExecutionPlan.mockResolvedValue({ execution_plan_id: 'p1', run_mode: 'parallel', status: 'completed', results: [{ order: 0, scenario_id: 's1', test_id: 't1', status: 'created' }, { order: 1, scenario_id: 's2', status: 'failed', detail: 'scenario archived' }], created_count: 1, failed_count: 1, materialized_at: '' })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => { ([...container.querySelectorAll('button')].find(b => b.textContent === 'Run') as HTMLElement).click() })
    await flush()
    const dialog = document.querySelector('[aria-labelledby="execution-plan-result-title"]')
    expect(dialog?.querySelector('h3')?.textContent).toBe('Plan run partly started: “nightly”')
    expect(dialog?.textContent).toContain('#1 Browse catalog')
    expect(dialog?.textContent).toContain('#2 Checkout')
    expect(dialog?.textContent).toContain('1 failed to start')
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
    const addPoolBtn = [...container.querySelectorAll('button')].find(b => b.textContent === '+ Add data pool')
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
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
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
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
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

  it('lets editors inspect pending approvals without exposing project-admin decisions', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({
      approvals: [
        { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', required_role: 'ProjectAdmin', requested_at: '', created_at: '', updated_at: '' },
      ],
      count: 1,
    })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin={false} />)
    })
    await flush()
    const approvalsButton = [...container.querySelectorAll('button')].find(button => button.textContent === 'Approvals')
    await act(async () => {
      ;(approvalsButton as HTMLElement).click()
    })
    await flush()
    expect(container.textContent).toContain('Approvals — “nightly”')
    expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Approve')).toBe(false)
    expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Reject')).toBe(false)
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

  it('ignores a late execution-plan response from the previous project', async () => {
    let resolveProjectA: ((value: { execution_plans: ExecutionPlan[]; count: number }) => void) | undefined
    plansApiMock.listExecutionPlans.mockImplementation(({ project_id }: { project_id: string }) => {
      if (project_id === 'proj-a') return new Promise(resolve => { resolveProjectA = resolve })
      return Promise.resolve({ execution_plans: [plan({ id: 'p-b', project_id: 'proj-b', name: 'project-b-plan' })], count: 1 })
    })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-a" canManage />) })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.textContent).toContain('project-b-plan')

    await act(async () => {
      resolveProjectA?.({ execution_plans: [plan({ name: 'stale-project-a-plan' })], count: 1 })
    })
    await flush()
    expect(container.textContent).toContain('project-b-plan')
    expect(container.textContent).not.toContain('stale-project-a-plan')
  })

  it('closes and clears an execution-plan draft when the project changes', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Execution Plan')?.click()
    })
    await act(async () => setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'project-a-draft'))

    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Execution Plan')?.click()
    })
    expect(container.querySelector<HTMLInputElement>('input[placeholder="e.g. nightly regression"]')?.value).toBe('')
  })

  it('keeps the last good execution-plan list when a same-project refresh fails', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValueOnce({ execution_plans: [plan({ name: 'retained-plan', run_mode: 'parallel' })], count: 1 })
    plansApiMock.runExecutionPlan.mockResolvedValue({
      execution_plan_id: 'p1',
      run_mode: 'parallel',
      status: 'completed',
      results: [],
      created_count: 0,
      failed_count: 0,
      materialized_at: '',
    })
    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-a" canManage />) })
    await flush()
    plansApiMock.listExecutionPlans.mockRejectedValueOnce(new ApiError('plan refresh unavailable', 503))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Run')?.click()
    })
    await flush()
    expect(container.textContent).toContain('retained-plan')
    expect(container.textContent).toContain('plan refresh unavailable')
  })

	it('guards a dirty execution-plan draft before closing the editor', async () => {
		plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
		await act(async () => {
			root.render(
				<NavigationGuardProvider>
					<ExecutionPlansPanel projectId="proj-a" canManage />
				</NavigationGuardProvider>,
			)
		})
		await flush()
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Execution Plan')?.click()
		})
		await act(async () => setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'unsaved plan'))
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('.pp-modal-actions button')).find(button => button.textContent === 'Cancel')?.click()
		})

		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(container.querySelector<HTMLInputElement>('input[placeholder="e.g. nightly regression"]')?.value).toBe('unsaved plan')
		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(button => button.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(container.querySelector('input[placeholder="e.g. nightly regression"]')).toBeNull()
	})
})
