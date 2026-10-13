// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import type { ExecutionPlan } from '../types'
import type { SchedulePreviewResponse } from '../types/executionPlans'
import ExecutionPlanSchedulesModal from './ExecutionPlanSchedulesModal'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  listExecutionPlanSchedules: vi.fn(),
  createExecutionPlanSchedule: vi.fn(),
  updateExecutionPlanSchedule: vi.fn(),
  deleteExecutionPlanSchedule: vi.fn(),
  listExecutionPlanScheduleOccurrences: vi.fn(),
  resolveExecutionPlanScheduleOccurrence: vi.fn(),
  previewExecutionPlanSchedule: vi.fn(async (): Promise<SchedulePreviewResponse> => ({ cron_expr: '0 2 * * *', timezone: 'UTC', occurrences: [], count: 0 })),
}))

vi.mock('../hooks/api/useExecutionPlansApi', () => ({
  useExecutionPlansApi: () => apiMock,
}))

const plan = {
  id: 'plan-1',
  name: 'Nightly checkout',
} as ExecutionPlan

const schedule = {
  id: 'schedule-1',
  plan_id: plan.id,
  project_id: 'project-1',
  revision: 1,
  name: 'Nightly',
  cron_expr: '0 2 * * *',
  timezone: 'UTC',
  enabled: true,
  created_at: '',
  updated_at: '',
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>(resolvePromise => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

describe('ExecutionPlanSchedulesModal accessibility', () => {
  let container: HTMLDivElement
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

  it('labels the modal, manages focus, and handles Escape', async () => {
    const onClose = vi.fn()
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage onClose={onClose} />)
    })
    await flush()

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toContain('Nightly checkout')
    expect(document.activeElement).toBe(button('Close'))

    await act(async () => {
      button('New schedule').click()
    })
    await flush()
    expect(document.activeElement).toBe(container.querySelector('input[placeholder="e.g. nightly"]'))

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('has one way out at a time and explains the pause window', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [{ ...schedule, enabled: false }], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage onClose={() => {}} />)
    })
    await flush()
    expect(container.querySelector('.pp-row-title .pp-pill')?.textContent).toBe('Turned off')
    // No header Close beside the footer's.
    expect(Array.from(container.querySelectorAll('button')).filter(item => item.textContent === 'Close')).toHaveLength(1)
    expect(container.querySelector('.pp-modal-header button')).toBeNull()
    await act(async () => button('New schedule').click())
    await flush()
    const pauseFrom = Array.from(container.querySelectorAll<HTMLLabelElement>('label')).find(label => label.textContent?.startsWith('Pause from'))?.querySelector('input')
    expect(document.getElementById(pauseFrom?.getAttribute('aria-describedby') || '')?.textContent).toContain('No runs start between these times')
    expect(Array.from(container.querySelectorAll('label')).some(label => label.textContent === 'Enabled')).toBe(true)
    // Cancel closes the form; focus moves to the Close that replaces it.
    await act(async () => button('Cancel').click())
    await flush()
    expect(document.activeElement).toBe(button('Close'))
  })

  it('announces API failures', async () => {
    apiMock.listExecutionPlanSchedules.mockRejectedValue(new ApiError('schedules unavailable', 503))
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage onClose={() => {}} />)
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('schedules unavailable')
    expect(button('Retry schedules')).toBeTruthy()
  })

  it('keeps schedule CRUD available to editors but hides occurrence resolution', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({
      schedules: [schedule],
      count: 1,
    })
    apiMock.listExecutionPlanScheduleOccurrences.mockResolvedValue({
      occurrences: [{
        id: 'occurrence-1',
        schedule_id: 'schedule-1',
        plan_id: plan.id,
        project_id: 'project-1',
        schedule_revision: 1,
        revision: 1,
        planned_at: '',
        status: 'blocked',
        created_at: '',
        updated_at: '',
      }],
      count: 1,
    })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage canAdmin={false} onClose={() => {}} />)
    })
    await flush()
    expect(button('Edit')).toBeTruthy()
    await act(async () => {
      button('Runs').click()
    })
    await flush()
    expect(container.textContent).toContain('Blocked')
    expect(Array.from(container.querySelectorAll('button')).some(item => item.textContent === 'Retry')).toBe(false)
  })

  it('keeps the authoritative POST result and retries reconciliation with GET only', async () => {
    const blockedOccurrence = {
      id: 'occurrence-1',
      schedule_id: schedule.id,
      plan_id: plan.id,
      project_id: 'project-1',
      schedule_revision: 1,
      revision: 1,
      planned_at: '',
      status: 'blocked',
      reason_code: 'missed_run',
      created_at: '',
      updated_at: '',
    }
    const committedOccurrence = { ...blockedOccurrence, revision: 2, status: 'skipped', updated_at: '2026-08-03T12:00:00Z' }
    const committedSchedule = { ...schedule, revision: 2, updated_at: '2026-08-03T12:00:00Z' }
    apiMock.listExecutionPlanSchedules
      .mockResolvedValueOnce({ schedules: [schedule], count: 1 })
      .mockRejectedValueOnce(new ApiError('schedule refresh unavailable', 503))
    apiMock.listExecutionPlanScheduleOccurrences
      .mockResolvedValueOnce({ occurrences: [blockedOccurrence], count: 1 })
      .mockRejectedValueOnce(new ApiError('occurrence refresh unavailable', 503))
    apiMock.resolveExecutionPlanScheduleOccurrence.mockResolvedValueOnce({
      occurrence: committedOccurrence,
      schedule: committedSchedule,
    })

    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage canAdmin onClose={() => {}} />)
    })
    await flush()
    await act(async () => {
      button('Runs').click()
    })
    await flush()
    await act(async () => {
      button('Skip').click()
    })
    await flush()

    expect(apiMock.resolveExecutionPlanScheduleOccurrence).toHaveBeenCalledOnce()
    expect(container.textContent).toContain('Skipped')
    expect(container.textContent).toContain('was resolved, but the latest schedule state could not be refreshed')
    expect(button('Retry occurrence refresh')).toBeTruthy()

    apiMock.listExecutionPlanSchedules.mockResolvedValueOnce({ schedules: [committedSchedule], count: 1 })
    apiMock.listExecutionPlanScheduleOccurrences.mockResolvedValueOnce({ occurrences: [committedOccurrence], count: 1 })
    await act(async () => {
      button('Retry occurrence refresh').click()
    })
    await flush()

    expect(apiMock.resolveExecutionPlanScheduleOccurrence).toHaveBeenCalledOnce()
    expect(apiMock.listExecutionPlanScheduleOccurrences).toHaveBeenCalledTimes(3)
    expect(Array.from(container.querySelectorAll('button')).some(item => item.textContent === 'Retry occurrence refresh')).toBe(false)
  })

  it('ignores a late occurrence response after another schedule is opened', async () => {
    const secondSchedule = { ...schedule, id: 'schedule-2', name: 'Hourly' }
    const firstResponse = deferred<{ occurrences: Array<Record<string, unknown>>; count: number }>()
    const secondResponse = deferred<{ occurrences: Array<Record<string, unknown>>; count: number }>()
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [schedule, secondSchedule], count: 2 })
    apiMock.listExecutionPlanScheduleOccurrences.mockImplementation((_planId: string, scheduleId: string) => (
      scheduleId === schedule.id ? firstResponse.promise : secondResponse.promise
    ))

    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage canAdmin onClose={() => {}} />)
    })
    await flush()
    await act(async () => {
      button('Runs').click()
    })
    await flush()
    await act(async () => {
      button('Runs').click()
    })
    await flush()

    await act(async () => {
      secondResponse.resolve({
        occurrences: [{
          id: 'occurrence-2', schedule_id: secondSchedule.id, plan_id: plan.id, revision: 1,
          planned_at: '', status: 'failed', conflict_summary: 'second schedule', created_at: '', updated_at: '',
        }],
        count: 1,
      })
      await secondResponse.promise
    })
    await flush()
    expect(container.textContent).toContain('second schedule')

    await act(async () => {
      firstResponse.resolve({
        occurrences: [{
          id: 'occurrence-1', schedule_id: schedule.id, plan_id: plan.id, revision: 1,
          planned_at: '', status: 'failed', conflict_summary: 'stale first schedule', created_at: '', updated_at: '',
        }],
        count: 1,
      })
      await firstResponse.promise
    })
    await flush()
    expect(container.textContent).toContain('second schedule')
    expect(container.textContent).not.toContain('stale first schedule')
  })

	it('guards a dirty schedule draft before closing the schedules workspace', async () => {
		const onClose = vi.fn()
		apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
		await act(async () => {
			root.render(
				<NavigationGuardProvider>
					<ExecutionPlanSchedulesModal plan={plan} canManage onClose={onClose} />
				</NavigationGuardProvider>,
			)
		})
		await flush()
		await act(async () => button('New schedule').click())
		const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="e.g. nightly"]')
		await act(async () => {
			Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(nameInput, 'weekday draft')
			nameInput?.dispatchEvent(new Event('input', { bubbles: true }))
		})
		// The open form's footer offers Cancel (the form) and Save; Escape closes the dialog.
		expect(Array.from(container.querySelectorAll('.pp-modal-actions button')).map(item => item.textContent)).toEqual(['Cancel', 'Create'])
		await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))

		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(onClose).not.toHaveBeenCalled()
		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(item => item.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(onClose).toHaveBeenCalledOnce()
	})

  it('files a schedule under the plan release or its own, with the next runs and policies shown', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
    apiMock.previewExecutionPlanSchedule.mockResolvedValue({ cron_expr: '0 2 * * *', timezone: 'UTC', occurrences: ['2026-10-13T02:00:00Z', '2026-10-14T02:00:00Z'], count: 2 })
    apiMock.createExecutionPlanSchedule.mockResolvedValue({ ...schedule, release_id: 'rel-2' })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal
        plan={{ ...plan, overlap_policy: 'queue', application_id: 'app-1', release_id: 'rel-1' }}
        canManage
        onClose={() => {}}
        planReleaseLabel="Storefront › R2026.11"
        releaseOptions={[{ id: 'rel-1', name: 'R2026.11' }, { id: 'rel-2', name: 'R2026.12' }]}
      />)
    })
    await flush()
    await act(async () => button('New schedule').click())
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 450)) })
    await flush()
    expect(apiMock.previewExecutionPlanSchedule).toHaveBeenCalledWith(plan.id, expect.objectContaining({ cron_expr: '0 2 * * *' }), 5)
    expect(container.textContent).toContain('Next runs (')
    expect(container.querySelectorAll('.cron-next-runs li')).toHaveLength(2)
    const release = container.querySelector<HTMLSelectElement>('select[aria-label="Release this schedule\'s runs are filed under"]')
    expect(release?.options[0].textContent).toBe("Use the plan's release (Storefront › R2026.11)")
    expect(release?.hasAttribute('aria-describedby')).toBe(false)
    expect(container.textContent).toContain('Wait for the running one to finish (set on the plan)')
    expect(container.querySelector('.new-test-schedule-policy')?.textContent).toContain('Missed runsSkip missed runs')
    expect(container.textContent).toContain("The run waits for a project admin's approval")
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(container.querySelector('input[placeholder="e.g. nightly"]'), 'nightly')
      container.querySelector('input[placeholder="e.g. nightly"]')?.dispatchEvent(new Event('input', { bubbles: true }))
      release!.value = 'rel-2'
      release!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => button('Create').click())
    await flush()
    expect(apiMock.createExecutionPlanSchedule).toHaveBeenCalledWith(plan.id, expect.objectContaining({ name: 'nightly', release_id: 'rel-2' }))
  })

  // A saved schedule and a candidate one both name the daylight-saving
  // changes their times fall into; next runs read in the schedule's zone.
  it('names skipped and repeated times on saved and previewed schedules', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({
      schedules: [{ ...schedule, cron_expr: '30 1 * * *', timezone: 'America/Chicago', next_run_at: '2026-10-13T06:30:00Z' }],
      count: 1,
      clock_changes: { [schedule.id]: [{ date: '2026-11-01', kind: 'repeated', local_times: ['01:30'], matches: 1, runs_at: '2026-11-01T06:30:00Z', at: '2026-11-01T07:00:00Z' }] },
    })
    apiMock.previewExecutionPlanSchedule.mockResolvedValue({
      cron_expr: '30 2 14 3 *', timezone: 'America/Chicago', occurrences: ['2028-03-14T07:30:00Z'], count: 1,
      clock_changes: [{ date: '2027-03-14', kind: 'skipped', local_times: ['02:30'], matches: 1, at: '2027-03-14T08:00:00Z' }],
    })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage onClose={() => {}} />)
    })
    await flush()
    expect(container.textContent).toContain('next: Oct 13, 2026, 1:30 AM CDT')
    expect(container.textContent).toContain('Sun, Nov 1, 2026: 1:30 AM happens twice (clocks go back); it runs once, at the first (Nov 1, 2026, 1:30 AM CDT).')
    await act(async () => button('New schedule').click())
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 450)) })
    await flush()
    expect(container.textContent).toContain("Sun, Mar 14, 2027: 2:30 AM doesn't exist (clocks go forward), so that run is skipped.")
  })

  it('says how to file scheduled runs under a release when the plan has none', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [], count: 0 })
    apiMock.previewExecutionPlanSchedule.mockResolvedValue({ cron_expr: '0 2 * * *', timezone: 'UTC', occurrences: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage onClose={() => {}} />)
    })
    await flush()
    await act(async () => button('New schedule').click())
    await flush()
    const release = container.querySelector<HTMLSelectElement>('select[aria-label="Release this schedule\'s runs are filed under"]')
    expect(Array.from(release?.options || []).map(option => option.textContent)).toEqual(['Imported Legacy (the plan has no release)'])
    const help = document.getElementById(release?.getAttribute('aria-describedby') || '')
    expect(help?.textContent).toBe('To file scheduled runs under a release, edit the plan and choose its application and release first.')
  })

  it('words a held scheduled run plainly and offers its approval', async () => {
    const onReviewApproval = vi.fn()
    apiMock.listExecutionPlanSchedules.mockResolvedValue({ schedules: [{
      ...schedule, next_required_action: 'approval_required', next_run_at: '2026-10-12T06:30:00Z', last_error: 'plan run requires approval (exceeds project quota)',
    }], count: 1 })
    apiMock.listExecutionPlanScheduleOccurrences.mockResolvedValue({ occurrences: [{
      id: 'occ-1', schedule_id: schedule.id, plan_id: plan.id, planned_at: '2026-10-12T06:30:00Z', status: 'blocked', reason_code: 'approval_required', run_id: 'exec-plan-run-d7d1', created_at: '', updated_at: '',
    }], count: 1 })
    await act(async () => {
      root.render(<ExecutionPlanSchedulesModal plan={plan} canManage canAdmin onClose={() => {}} onReviewApproval={onReviewApproval} />)
    })
    await flush()
    expect(container.textContent).toContain('Waiting for approval')
    expect(container.textContent).toMatch(/Waiting for approval: the .* run is held; later runs wait for the decision\./)
    expect(container.textContent).not.toContain('next:')
    expect(container.textContent).not.toContain('last error')
    await act(async () => button('Runs').click())
    await flush()
    expect(container.textContent).not.toContain('approval_required')
    expect(container.textContent).not.toContain('exec-plan-run-d7d1')
    await act(async () => button('Review approval').click())
    expect(onReviewApproval).toHaveBeenCalled()
  })

  function button(label: string) {
    const match = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent === label)
    if (!match) throw new Error(`button not found: ${label}`)
    return match
  }
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}
