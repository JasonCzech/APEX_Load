// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import type { ExecutionPlan } from '../types'
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
    expect(container.textContent).toContain('blocked')
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
    expect(container.textContent).toContain('skipped')
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
          planned_at: '', status: 'failed', reason_code: 'second schedule', created_at: '', updated_at: '',
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
          planned_at: '', status: 'failed', reason_code: 'stale first schedule', created_at: '', updated_at: '',
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
		await act(async () => button('Done').click())

		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(onClose).not.toHaveBeenCalled()
		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(item => item.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(onClose).toHaveBeenCalledOnce()
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
