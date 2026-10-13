// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import type { ExecutionPlan } from '../types'
import ExecutionPlanApprovalsModal from './ExecutionPlanApprovalsModal'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  listExecutionPlanApprovals: vi.fn(),
  approveExecutionPlanApproval: vi.fn(),
  rejectExecutionPlanApproval: vi.fn(),
}))

vi.mock('../hooks/api/useExecutionPlansApi', () => ({
  useExecutionPlansApi: () => apiMock,
}))

const plan = {
  id: 'plan-1',
  name: 'Nightly checkout',
} as ExecutionPlan

describe('ExecutionPlanApprovalsModal accessibility', () => {
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

  it('labels the modal, focuses Close, and handles Escape', async () => {
    const onClose = vi.fn()
    apiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [], count: 0 })
    await act(async () => {
      root.render(<ExecutionPlanApprovalsModal plan={plan} canAdmin onClose={onClose} />)
    })
    await flush()

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    const close = button('Close')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toContain('Nightly checkout')
    expect(document.activeElement).toBe(close)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('announces API failures', async () => {
    apiMock.listExecutionPlanApprovals.mockRejectedValue(new ApiError('approvals unavailable', 503))
    await act(async () => {
      root.render(<ExecutionPlanApprovalsModal plan={plan} canAdmin onClose={() => {}} />)
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('approvals unavailable')
    expect(button('Retry approvals')).toBeTruthy()
  })

  it('preserves the committed approval when the follow-up GET fails', async () => {
    const pending = {
      id: 'approval-1',
      plan_id: plan.id,
      project_id: 'project-1',
      policy: 'vuser_quota',
      status: 'pending',
      requested_at: '2026-08-03T10:00:00Z',
      created_at: '2026-08-03T10:00:00Z',
      updated_at: '2026-08-03T10:00:00Z',
    }
    const approved = {
      ...pending,
      status: 'approved',
      resolved_by: 'admin',
      resolved_at: '2026-08-03T11:00:00Z',
      updated_at: '2026-08-03T11:00:00Z',
    }
    const onResolved = vi.fn()
    apiMock.listExecutionPlanApprovals
      .mockResolvedValueOnce({ approvals: [pending], count: 1 })
      .mockRejectedValueOnce(new ApiError('approval refresh unavailable', 503))
      .mockResolvedValueOnce({ approvals: [approved], count: 1 })
    apiMock.approveExecutionPlanApproval.mockResolvedValueOnce(approved)

    await act(async () => {
      root.render(<ExecutionPlanApprovalsModal plan={plan} canAdmin onClose={() => {}} onResolved={onResolved} />)
    })
    await flush()
    await act(async () => {
      button('Approve').click()
    })
    await flush()

    expect(apiMock.approveExecutionPlanApproval).toHaveBeenCalledOnce()
    expect(container.textContent).toContain('Approved')
    expect(container.textContent).toContain('approval refresh unavailable')
    expect(button('Retry approvals')).toBeTruthy()
    expect(onResolved).toHaveBeenCalledOnce()

    await act(async () => {
      button('Retry approvals').click()
    })
    await flush()
    expect(apiMock.approveExecutionPlanApproval).toHaveBeenCalledOnce()
    expect(container.textContent).toContain('Approved')
    expect(Array.from(container.querySelectorAll('button')).some(item => item.textContent === 'Retry approvals')).toBe(false)
  })

  it('says it is loading until the first answer, not that there are no requests', async () => {
    let answer: (value: unknown) => void = () => {}
    apiMock.listExecutionPlanApprovals.mockImplementation(() => new Promise(resolve => { answer = resolve }))
    await act(async () => {
      root.render(<ExecutionPlanApprovalsModal plan={plan} canAdmin onClose={() => {}} />)
    })
    expect(container.textContent).toContain('Loading approvals')
    expect(container.textContent).not.toContain('No approval requests')
    await act(async () => { answer({ approvals: [], count: 0 }) })
    await flush()
    expect(container.textContent).toContain('No approval requests')
  })

  it('reads in plain words, keeps the request reason and offers Run now for the approval ready to use', async () => {
    const base = { plan_id: plan.id, policy: 'quota_burst', created_at: '', updated_at: '' }
    apiMock.listExecutionPlanApprovals.mockResolvedValue({ approvals: [
      { ...base, id: 'a-pending', status: 'pending', required_role: 'project_admin', requested_by: 'dev-mode', requested_at: '2026-10-12T06:00:00Z', decision_reason: 'Aggregate peak 8 VUsers exceeds project limit 5' },
      { ...base, id: 'a-ready', status: 'approved', requested_by: 'service-account:sa-1', resolved_by: 'dev-mode', requested_at: '2026-10-11T06:00:00Z', resolved_at: '2026-10-11T06:05:00Z', decision_reason: 'Aggregate peak 8 VUsers exceeds project limit 5 Decision: release window' },
      { ...base, id: 'a-used', status: 'approved', requested_at: '2026-10-10T06:00:00Z', run_id: 'epr-1' },
    ], count: 3 })
    const onRunNow = vi.fn()
    await act(async () => {
      root.render(<ExecutionPlanApprovalsModal plan={plan} canAdmin onClose={() => {}} onRunNow={onRunNow} readyApprovalId="a-ready" notice="Nothing has started." />)
    })
    await flush()
    // Opened to say why, the dialog takes focus and is described by the notice.
    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(document.activeElement).toBe(dialog)
    expect(document.getElementById(dialog?.getAttribute('aria-describedby') || '')?.textContent).toBe('Nothing has started.')
    expect(container.querySelector('[role="status"]')?.textContent).toBe('Nothing has started.')
    const rows = [...container.querySelectorAll('.pp-list > .pp-row')] as HTMLElement[]
    expect(rows[0].querySelector('.pp-row-title')?.textContent).toBe('Over the project VUser limitWaiting for a decisionNeeds a project admin')
    expect(rows[0].textContent).toContain('by development mode')
    expect(rows[1].querySelector('.pp-row-title')?.textContent).toBe('Over the project VUser limitApproved')
    expect(rows[1].textContent).toContain('Aggregate peak 8 VUsers exceeds project limit 5 Decision: release window')
    expect(rows[1].textContent).toContain('by a service account')
    expect(rows[1].textContent).toContain('Approved Oct')
    expect(rows[2].querySelector('.pp-row-title')?.textContent).toBe('Over the project VUser limitApproved and used')
    expect(container.textContent).not.toMatch(/quota_burst|project_admin|dev-mode/)
    // Only the approval the next run uses offers to start it.
    expect(rows.map(row => [...row.querySelectorAll('button')].some(item => item.textContent === 'Run now'))).toEqual([false, true, false])
    await act(async () => { button('Run now').click() })
    expect(onRunNow).toHaveBeenCalledOnce()
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
