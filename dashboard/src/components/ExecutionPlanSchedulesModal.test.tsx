// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import type { ExecutionPlan } from '../types'
import ExecutionPlanSchedulesModal from './ExecutionPlanSchedulesModal'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  listExecutionPlanSchedules: vi.fn(),
  createExecutionPlanSchedule: vi.fn(),
  updateExecutionPlanSchedule: vi.fn(),
  deleteExecutionPlanSchedule: vi.fn(),
  listExecutionPlanScheduleOccurrences: vi.fn(),
}))

vi.mock('../hooks/api/useExecutionPlansApi', () => ({
  useExecutionPlansApi: () => apiMock,
}))

const plan = {
  id: 'plan-1',
  name: 'Nightly checkout',
} as ExecutionPlan

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
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('schedules unavailable')
  })

  it('keeps schedule CRUD available to editors but hides occurrence resolution', async () => {
    apiMock.listExecutionPlanSchedules.mockResolvedValue({
      schedules: [{
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
      }],
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
