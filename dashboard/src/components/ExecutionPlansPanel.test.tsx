// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ExecutionPlansPanel from './ExecutionPlansPanel'
import { ApiError } from '../hooks/apiClient'
import { ExecutionPlan } from '../types'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { ApplicationContext, type ApplicationContextValue } from '../hooks/applicationContext'
import type { ExecutionPlanRunView } from '../types/executionPlans'

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
  stopExecutionPlanRun: vi.fn(),
  listExecutionPlanSchedules: vi.fn(),
  previewExecutionPlanSchedule: vi.fn(async () => ({ cron_expr: '', timezone: 'UTC', occurrences: [], count: 0 })),
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

const applicationApiMock = vi.hoisted(() => ({
  listApplications: vi.fn(async () => ({ applications: [{ id: 'app-1', project_id: 'proj-a', name: 'Storefront', created_at: '', updated_at: '' }], count: 1 })),
  listReleases: vi.fn(async (applicationId: string) => ({
    releases: applicationId === 'app-1' ? [{ id: 'rel-1', application_id: 'app-1', name: 'R2026.11', created_at: '', updated_at: '' }] : [],
    count: 1,
  })),
}))
vi.mock('../hooks/api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
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
    expect(plansApiMock.listExecutionPlans).toHaveBeenCalledWith({ project_id: 'proj-a', limit: 100, offset: 0, include_activity: 'true' })
    expect(container.textContent).toContain('No execution plans yet')
    expect(container.textContent).toContain('New execution plan')
  })

  it('exposes the plan editor as a labelled modal and closes it with Escape', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const newButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New execution plan')
    await act(async () => newButton?.click())

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('execution-plan-editor-title')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')

    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('renders plans with run-mode pill and enabled-entry count', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ entries: [
      { order: 0, scenario_id: 's1', scenario_name: 'Browse catalog', enabled: true, failure_policy: 'skip' },
      { order: 1, scenario_id: 's2', enabled: false },
    ] })], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage scenarioOptions={[{ id: 's2', name: 'Checkout peak hour' }]} />)
    })
    await flush()
    expect(container.textContent).toContain('nightly')
    expect(container.querySelector('.pp-pill-info')?.textContent).toBe('One after another')
    expect(container.textContent).toContain('2 entries')
    expect(container.textContent).toContain('1 enabled')
    // The card lists its entries in order, with what each does if it fails.
    const entries = [...container.querySelectorAll('.pp-plan-entries li')].map(item => item.textContent)
    expect(entries).toEqual(['Browse catalog · continues if it fails', 'Checkout peak hour · turned off'])
    // The plan's status ("draft") changes nothing, so the card does not show it.
    expect(container.textContent).not.toMatch(/draft/i)
    // Run is the card's one action; the rest wait in More, Delete among them.
    const actions = container.querySelector('.pp-row-actions') as HTMLElement
    expect([...actions.children].map(child => child.tagName === 'BUTTON' ? child.textContent : child.querySelector('.ux-row-actions-toggle')?.textContent)).toEqual(['Run', 'More'])
    expect([...actions.querySelectorAll('.ux-row-actions-menu button')].map(button => button.textContent)).toEqual(['Edit', 'Schedules', 'Approvals', 'Delete'])
  })

  it('names each entry for its scenario and tells duplicate scenario names apart', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ entries: [{ order: 0, scenario_id: 's1', enabled: true }] })], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage scenarioOptions={[
        { id: 's1', name: 'Checkout peak hour', ref_key: 'SCN-1' },
        { id: 's2', name: 'checkout peak hour ', ref_key: 'SCN-2' },
        { id: 's3', name: 'Browse' },
      ]} />)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Edit') as HTMLElement).click()
    })
    await flush()
    const dialog = document.querySelector('[aria-labelledby="execution-plan-editor-title"]') as HTMLElement
    expect(dialog.classList.contains('pp-modal-wide')).toBe(true)
    expect(dialog.querySelector('h3')?.textContent).toBe('Edit execution plan')
    // One way out: no header Close beside the footer's Cancel.
    expect([...dialog.querySelectorAll('button')].some(button => button.textContent === 'Close')).toBe(false)
    expect([...dialog.querySelectorAll<HTMLOptionElement>('select[aria-label="Entry 1 scenario"] option')].map(option => option.textContent)).toEqual([
      'Choose a scenario',
      'Checkout peak hour (SCN-1) · duplicate name',
      'checkout peak hour  (SCN-2) · duplicate name',
      'Browse',
    ])
    expect([...dialog.querySelectorAll<HTMLOptionElement>('select[aria-label="Entry 1: if this run fails"] option')].map(option => option.textContent)).toEqual(['Stop the plan', 'Continue with the next'])
    expect(dialog.querySelector('input[type="checkbox"]')?.getAttribute('aria-label')).toBe('Enabled: entry 1, Checkout peak hour (SCN-1)')
    expect([...dialog.querySelectorAll<HTMLOptionElement>('.pp-modal-form > .pp-field-row select option')].slice(0, 2).map(option => option.textContent)).toEqual(['In parallel', 'One after another'])
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

  it('keeps a live override as saved and shows the scenario live tables', async () => {
    const live = { pool_id: '', alias: 'work', access_mode: 'live' as const, selection_strategy: 'lease' as const, scope: 'run' as const, live_options: { profile: 'native_queue' as const, seed_policy: 'empty' as const, columns: ['job_id'] } }
    const existing = plan({ run_mode: 'sequential', entries: [{ order: 0, scenario_id: 's1', enabled: true, data_pools: [live] }] })
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [existing], count: 1 })
    plansApiMock.updateExecutionPlan.mockResolvedValue(existing)
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage scenarioOptions={[{ id: 's1', name: 'Checkout', data_pools: [
        { pool_id: '', alias: 'jobs', access_mode: 'live', selection_strategy: 'manual', scope: 'run' },
      ] }]} />)
    })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')?.click()
    })
    const editor = document.querySelector('[aria-labelledby="execution-plan-editor-title"]')
    expect(editor?.textContent).toContain('work — live table, this run only')
    expect(editor?.textContent).toContain("The scenario's live tables: jobs (this run only).")
    expect(editor?.textContent).toContain('rows do not pass from one entry to the next')
    await act(async () => setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'renamed'))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('.pp-modal-actions button')).find(button => button.textContent === 'Save')?.click()
    })
    await flush()
    const saved = plansApiMock.updateExecutionPlan.mock.calls[0][1]
    expect(saved.entries[0].data_pools).toEqual([live])
  })

  it('shows a refused save inside the open editor', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ revision: 7 })], count: 1 })
    plansApiMock.updateExecutionPlan.mockRejectedValue(new ApiError('schedule "nightly" files its runs under a release of another application', 409))
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
    const editor = document.querySelector('[aria-labelledby="execution-plan-editor-title"]')
    expect(editor?.querySelector('[role="alert"]')?.textContent).toContain('files its runs under a release of another application')
    expect(document.querySelectorAll('.pp-error')).toHaveLength(1)
  })

  it('hides management actions without permission', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({})], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage={false} />)
    })
    await flush()
    expect(container.textContent).not.toContain('New execution plan')
    expect([...container.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Run')).toBe(false)
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
    const newBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'New execution plan')
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
    // The list is read again, so the card shows the request now waiting.
    expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(2)
    // The dialog itself says why it opened; nothing under its overlay does.
    const approvals = document.querySelector('[role="dialog"]') as HTMLElement
    expect(approvals.querySelector('[role="status"]')?.textContent).toContain('Nothing has started: this run is over the project\'s VUser limit')
    expect(container.querySelector('.pp-error')).toBeNull()
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
    expect(container.textContent).toContain('New execution plan')
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
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New execution plan')?.click()
    })
    await act(async () => setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'project-a-draft'))

    await act(async () => { root.render(<ExecutionPlansPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New execution plan')?.click()
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
			Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New execution plan')?.click()
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
  it('files a new plan under the workspace release and saves its overlap policy', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [], count: 0 })
    plansApiMock.createExecutionPlan.mockResolvedValue(plan({}))
    const workspace = {
      activeApplication: { id: 'app-1', project_id: 'proj-a', name: 'Storefront', created_at: '', updated_at: '' },
      activeRelease: { id: 'rel-1', application_id: 'app-1', name: 'R2026.11', created_at: '', updated_at: '' },
    } as unknown as ApplicationContextValue
    await act(async () => {
      root.render(<ApplicationContext.Provider value={workspace}><ExecutionPlansPanel projectId="proj-a" canManage /></ApplicationContext.Provider>)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'New execution plan') as HTMLElement).click()
    })
    await flush()
    await flush()
    const application = container.querySelector<HTMLSelectElement>('select[aria-label="Application the plan\'s runs are filed under"]')
    const release = container.querySelector<HTMLSelectElement>('select[aria-label="Release the plan\'s runs are filed under"]')
    expect(application?.value).toBe('app-1')
    expect(release?.value).toBe('rel-1')
    expect(release?.textContent).toContain('R2026.11')
    expect(container.textContent).toContain('is filed under this release and shows in its workspace')
    const overlap = container.querySelector<HTMLSelectElement>('select[aria-label="If the plan is started while a run of it is still running"]')
    expect(overlap?.value).toBe('skip')
    await act(async () => {
      setValue(container.querySelector('input[placeholder="e.g. nightly regression"]'), 'workspace plan')
      setValue(container.querySelector('input[placeholder="scenario id"]'), 's1')
      overlap!.value = 'queue'
      overlap!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(container.textContent).toContain('A scheduled run waits and starts when the previous run finishes')
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Create') as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.createExecutionPlan).toHaveBeenCalledWith(expect.objectContaining({ application_id: 'app-1', release_id: 'rel-1', overlap_policy: 'queue' }))
  })

  function activeRun(overrides: Partial<ExecutionPlanRunView> = {}): ExecutionPlanRunView {
    const started = new Date(Date.now() - 5 * 60_000).toISOString()
    return {
      id: 'run-1', plan_id: 'p1', run_mode: 'parallel', status: 'completed', created_at: started, updated_at: started,
      active: true, outcome: 'running', started_at: started, estimated_end_at: new Date(Date.now() + 10 * 60_000).toISOString(),
      entries: [{ order: 0, scenario_id: 's1', scenario_name: 'Checkout', status: 'created', test_id: 't1', test_status: 'RUNNING' }],
      ...overrides,
    }
  }

  it('shows a running plan with its progress, a way back to it and Stop plan', async () => {
    const onOpenTest = vi.fn()
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{ ...plan({ run_mode: 'parallel', overlap_policy: 'skip' }), activity: { active_run: activeRun(), active_runs: 1 } }], count: 1 })
    plansApiMock.getExecutionPlanRun.mockResolvedValue(activeRun())
    plansApiMock.stopExecutionPlanRun.mockResolvedValue({ run: activeRun({ active: false, outcome: 'stopped', ended_at: new Date().toISOString() }), stop_requested: true })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage onOpenTest={onOpenTest} />)
    })
    await flush()
    expect(container.textContent).toContain('Running')
    expect(container.textContent).toContain('1 of 1 run active')
    expect(container.textContent).toMatch(/5 min elapsed · about 10 min left/)
    const run = [...container.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Run') as HTMLButtonElement
    expect(run.disabled).toBe(true)
    expect(run.title).toContain('does not start another alongside it')
    expect(document.getElementById(run.getAttribute('aria-describedby') || '')?.textContent).toBe('Run is unavailable until this run ends.')

    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'View progress') as HTMLElement).click()
    })
    await flush()
    const dialog = document.querySelector('[aria-labelledby="execution-plan-progress-title"]') as HTMLElement
    expect(dialog.textContent).toContain('Running')
    expect(dialog.textContent).toContain('elapsed')
    expect(dialog.textContent).not.toContain('updating…')
    await act(async () => {
      ;([...dialog.querySelectorAll('button')].find(b => b.textContent === 'Open run') as HTMLElement).click()
    })
    expect(onOpenTest).toHaveBeenCalledWith('t1')

    await act(async () => {
      ;([...dialog.querySelectorAll('button')].find(b => b.textContent === 'Stop plan') as HTMLElement).click()
    })
    await flush()
    expect(confirmActionMock).toHaveBeenCalledWith(expect.stringContaining('Stop “nightly”?'), expect.objectContaining({ confirmLabel: 'Stop plan' }))
    expect(plansApiMock.stopExecutionPlanRun).toHaveBeenCalledWith('p1', 'run-1')
    expect(dialog.textContent).toContain('Stopped')
  })

  it('shows a run held for approval on the card with a way to review it', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{
      ...plan({}),
      activity: {
        pending_approval: { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', requested_at: '', created_at: '', updated_at: '' },
        waiting: [{ schedule_id: 'sch-1', schedule_name: 'Nightly', planned_at: '2026-10-12T06:30:00Z', reason_code: 'approval_required', approval_id: 'appr-1' }],
      },
    }], count: 1 })
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
    })
    await flush()
    expect(container.textContent).toContain('Waiting for approval')
    expect(container.textContent).toContain('held until a project admin approves it')
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Review approval') as HTMLElement).click()
    })
    await flush()
    expect(plansApiMock.listExecutionPlanApprovals).toHaveBeenCalledWith('p1')
  })

  it('explains a run the overlap policy kept from starting', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ overlap_policy: 'queue' })], count: 1 })
    plansApiMock.runExecutionPlan.mockRejectedValue(new ApiError('plan running', 409, {
      status: 'plan_running',
      overlap: { overlap_policy: 'queue', active_run_id: 'run-0', started_at: '', message: '"nightly" is already running (started Oct 12 06:27 UTC).' },
    }))
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Run') as HTMLElement).click()
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('is already running (started Oct 12 06:27 UTC)')
    // The editor opened next starts without that refusal.
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Edit') as HTMLElement).click()
    })
    expect(document.querySelector('[aria-labelledby="execution-plan-editor-title"] [role="alert"]')).toBeNull()
  })

  it('names how the last run ended and reopens it', async () => {
    const ended = new Date().toISOString()
    const last = activeRun({ active: false, outcome: 'failed', ended_at: ended, estimated_end_at: undefined })
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{ ...plan({}), activity: { last_run: last } }], count: 1 })
    plansApiMock.getExecutionPlanRun.mockResolvedValue(last)
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.textContent).toContain('Last run: Failed')
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'View last run') as HTMLElement).click()
    })
    await flush()
    const dialog = document.querySelector('[aria-labelledby="execution-plan-progress-title"]') as HTMLElement
    expect(dialog.textContent).toContain('Failed')
    expect(dialog.textContent).toContain('Finished')
    expect([...dialog.querySelectorAll('button')].some(b => b.textContent === 'Stop plan')).toBe(false)
  })
  it('shows a scheduled run held for approval under the run in progress, without a live region', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{
      ...plan({}),
      activity: {
        active_run: activeRun(),
        active_runs: 1,
        waiting: [{ schedule_id: 'sch-1', schedule_name: 'Nightly', planned_at: '2026-10-12T06:30:00Z', reason_code: 'approval_required', approval_id: 'appr-1' }],
      },
    }], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
    })
    await flush()
    expect(container.textContent).toContain('Running')
    expect(container.textContent).toContain('Waiting for approval')
    expect([...container.querySelectorAll('button')].some(b => b.textContent === 'Review approval')).toBe(true)
    // The elapsed time changes on every refresh; it must not be re-announced.
    expect(container.querySelector('.pp-plan-state[role="status"]')).toBeNull()
  })

  it('does not leave the list loading when a refresh overtakes a reload', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const running = { ...plan({ overlap_policy: 'allow' }), activity: { active_run: activeRun(), active_runs: 1 } }
      let releaseReload: (value: unknown) => void = () => {}
      plansApiMock.listExecutionPlans.mockReset()
      plansApiMock.listExecutionPlans
        .mockResolvedValueOnce({ execution_plans: [running], count: 1 })
        .mockImplementationOnce(() => new Promise(resolve => { releaseReload = resolve }))
        .mockResolvedValue({ execution_plans: [running], count: 1 })
      plansApiMock.updateExecutionPlan.mockResolvedValue(running)
      await act(async () => {
        root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
      })
      await flush()
      // Saving reloads the list; the reload is slow.
      await act(async () => {
        ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Edit') as HTMLElement).click()
      })
      await flush()
      await act(async () => {
        ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Save') as HTMLElement).click()
      })
      await flush()
      expect(plansApiMock.updateExecutionPlan).toHaveBeenCalled()
      expect(container.textContent).toContain('Loading execution plans')
      // The activity refresh fires and finishes first, then the reload.
      await act(async () => { vi.advanceTimersByTime(5100) })
      await flush()
      await act(async () => { releaseReload({ execution_plans: [running], count: 1 }) })
      await flush()
      expect(container.textContent).not.toContain('Loading execution plans')
      expect(container.textContent).toContain('nightly')
    } finally {
      vi.useRealTimers()
    }
  })

  it('refreshes slowly while a run only waits', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      plansApiMock.listExecutionPlans.mockReset()
      plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{
        ...plan({}),
        activity: { pending_approval: { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', requested_at: '', created_at: '', updated_at: '' } },
      }], count: 1 })
      await act(async () => {
        root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
      })
      await flush()
      expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(1)
      await act(async () => { vi.advanceTimersByTime(6000) })
      await flush()
      expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(1)
      await act(async () => { vi.advanceTimersByTime(25000) })
      await flush()
      expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('refreshes often while a run is in progress', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      plansApiMock.listExecutionPlans.mockReset()
      plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [{ ...plan({}), activity: { active_run: activeRun(), active_runs: 1 } }], count: 1 })
      await act(async () => {
        root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
      })
      await flush()
      expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(1)
      await act(async () => { vi.advanceTimersByTime(5100) })
      await flush()
      expect(plansApiMock.listExecutionPlans).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it("keeps the plan's application while the editor re-reads the lists", async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ application_id: 'app-1', release_id: 'rel-1' })], count: 1 })
    let resolveApplications: (value: unknown) => void = () => {}
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    applicationApiMock.listApplications.mockImplementationOnce(() => new Promise(resolve => { resolveApplications = resolve }) as never)
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Edit') as HTMLElement).click()
    })
    await flush()
    const application = container.querySelector<HTMLSelectElement>('select[aria-label="Application the plan\'s runs are filed under"]')
    expect(application?.value).toBe('app-1')
    expect(application?.selectedOptions[0]?.textContent).toBe('Storefront')
    expect(container.textContent).toContain('Runs are filed under Storefront › R2026.11')
    await act(async () => { resolveApplications({ applications: [{ id: 'app-1', name: 'Storefront', created_at: '', updated_at: '' }], count: 1 }) })
    await flush()
  })

  it('re-reads releases each time the editor opens', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [plan({ application_id: 'app-1', release_id: 'rel-1' })], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    const before = applicationApiMock.listReleases.mock.calls.length
    for (let open = 0; open < 2; open++) {
      await act(async () => {
        ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Edit') as HTMLElement).click()
      })
      await flush()
      await act(async () => {
        ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Cancel') as HTMLElement).click()
      })
      await flush()
    }
    expect(applicationApiMock.listReleases.mock.calls.length - before).toBeGreaterThanOrEqual(2)
  })
  it('says before asking that a plan over the VUser limit needs approval, and shows a request already waiting', async () => {
    const pending = { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'pending', requested_at: '', created_at: '', updated_at: '' }
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [
      { ...plan({ id: 'p1', name: 'over' }), activity: { quota: { peak_vusers: 8, max_vusers: 5 } } },
      { ...plan({ id: 'p2', name: 'waiting' }), activity: { quota: { peak_vusers: 8, max_vusers: 5 }, pending_approval: { ...pending, plan_id: 'p2' } } },
    ], count: 2 })
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [{ ...pending, plan_id: 'p2' }], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
    })
    await flush()
    const runButtons = [...container.querySelectorAll('button')].filter(b => b.textContent === 'Run') as HTMLElement[]
    confirmActionMock.mockResolvedValueOnce(false)
    await act(async () => { runButtons[0].click() })
    await flush()
    const [asked, askedOptions] = confirmActionMock.mock.calls[0] as unknown as [string, { confirmLabel?: string }]
    expect(asked).toContain('Its peak of 8 VUsers is over the project limit of 5, so this asks a project admin to approve it first. Nothing starts until it is approved. You can approve it next.')
    expect(askedOptions).toEqual(expect.objectContaining({ confirmLabel: 'Request approval' }))
    expect(plansApiMock.runExecutionPlan).not.toHaveBeenCalled()

    // A request already waiting is shown, not asked again.
    await act(async () => { runButtons[1].click() })
    await flush()
    expect(confirmActionMock).toHaveBeenCalledTimes(1)
    expect(plansApiMock.runExecutionPlan).not.toHaveBeenCalled()
    const dialog = document.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog.textContent).toContain('Approvals — “waiting”')
    expect(dialog.querySelector('[role="status"]')?.textContent).toBe("Its peak of 8 VUsers is over the project limit of 5, so its run needs a project admin's approval. Nothing has started.")
  })

  it('offers Run now on the approval the next run uses, and starts the plan with it', async () => {
    const approved = { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'approved', requested_at: '2026-10-12T06:00:00Z', created_at: '', updated_at: '' }
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [
      { ...plan({ run_mode: 'parallel' }), activity: { quota: { peak_vusers: 8, max_vusers: 5, ready_approval_id: 'appr-1' } } },
    ], count: 1 })
    // appr-0 is used; appr-2 was granted for the plan before it changed.
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [
      { ...approved, id: 'appr-2', requested_at: '2026-10-12T07:00:00Z' },
      approved,
      { ...approved, id: 'appr-0', requested_at: '2026-10-11T06:00:00Z', run_id: 'epr-0' },
    ], count: 3 })
    plansApiMock.runExecutionPlan.mockResolvedValue({ execution_plan_id: 'p1', run_mode: 'parallel', status: 'completed', results: [{ order: 0, scenario_id: 's1', test_id: 't1', status: 'created' }], created_count: 1, failed_count: 0, materialized_at: '' })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Approvals') as HTMLElement).click()
    })
    await flush()
    const rows = [...document.querySelectorAll('[role="dialog"] .pp-list > .pp-row')]
    expect(rows.map(row => [...row.querySelectorAll('button')].some(button => button.textContent === 'Run now'))).toEqual([false, true, false])
    await act(async () => { ([...rows[1].querySelectorAll('button')].find(b => b.textContent === 'Run now') as HTMLElement).click() })
    await flush()
    const [asked, askedOptions] = confirmActionMock.mock.calls[confirmActionMock.mock.calls.length - 1] as unknown as [string, { confirmLabel?: string }]
    expect(asked).toContain('Its peak of 8 VUsers is over the project limit of 5; the approval granted for it lets this run start.')
    expect(askedOptions).toEqual(expect.objectContaining({ confirmLabel: 'Start runs' }))
    expect(plansApiMock.runExecutionPlan).toHaveBeenCalledWith('p1')
    expect(document.querySelector('[aria-labelledby="execution-plan-result-title"]')).toBeTruthy()
  })

  it('names the load of the runs in progress when a run alongside them goes over the limit', async () => {
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [
      { ...plan({ run_mode: 'parallel', overlap_policy: 'allow' }), activity: { active_run: activeRun(), active_runs: 1, quota: { peak_vusers: 4, active_vusers: 3, max_vusers: 5 } } },
    ], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage />)
    })
    await flush()
    confirmActionMock.mockResolvedValueOnce(false)
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Run') as HTMLElement).click()
    })
    await flush()
    const [asked, askedOptions] = confirmActionMock.mock.calls[0] as unknown as [string, { confirmLabel?: string }]
    expect(asked).toContain('Its peak of 4 VUsers, with the 3 VUsers of its runs already in progress, is over the project limit of 5, so this asks a project admin to approve it first.')
    expect(askedOptions).toEqual(expect.objectContaining({ confirmLabel: 'Request approval' }))
  })

  it('holds Run now, as Run, while a run of the plan is in progress', async () => {
    const approved = { id: 'appr-1', plan_id: 'p1', policy: 'quota_burst', status: 'approved', requested_at: '', created_at: '', updated_at: '' }
    plansApiMock.listExecutionPlans.mockResolvedValue({ execution_plans: [
      { ...plan({ run_mode: 'parallel', overlap_policy: 'skip' }), activity: { active_run: activeRun(), active_runs: 1, quota: { peak_vusers: 8, max_vusers: 5, ready_approval_id: 'appr-1' } } },
    ], count: 1 })
    plansApiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [approved], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlansPanel projectId="proj-a" canManage canAdmin />)
    })
    await flush()
    await act(async () => {
      ;([...container.querySelectorAll('button')].find(b => b.textContent === 'Approvals') as HTMLElement).click()
    })
    await flush()
    const runNow = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(b => b.textContent === 'Run now')
    expect(runNow?.disabled).toBe(true)
    expect(document.getElementById(runNow?.getAttribute('aria-describedby') || '')?.textContent).toBe('A run of this plan is in progress; Run now is available when it ends.')
  })
})
