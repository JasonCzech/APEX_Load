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
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('approvals unavailable')
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
