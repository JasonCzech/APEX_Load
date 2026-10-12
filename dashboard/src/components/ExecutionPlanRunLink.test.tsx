// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import ExecutionPlanRunLink from './ExecutionPlanRunLink'
import { executionPlanRunLabel } from './executionPlanActivityModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ExecutionPlanRunLink', () => {
  it('names the plan and the entry on a run a plan started', async () => {
    expect(executionPlanRunLabel({ plan_id: 'p1', plan_name: 'Nightly regression', plan_run_id: 'r1', entry_order: 3, entry_number: 2, entry_count: 3 })).toBe('Nightly regression, entry 2 of 3')
    expect(executionPlanRunLabel({ plan_id: 'p1', plan_run_id: 'r1', entry_order: 0 })).toBe('Execution plan')

    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => {
      root.render(<ExecutionPlanRunLink projectId="proj-a" link={{ plan_id: 'p1', plan_name: 'Nightly regression', plan_run_id: 'r1', entry_order: 1, entry_number: 2, entry_count: 3 }} />)
    })
    expect(container.textContent).toBe('Plan: Nightly regression, entry 2 of 3')
    const href = container.querySelector('a')?.getAttribute('href') || ''
    expect(href).toContain('#/tests?')
    expect(href).toContain('view=plans')
    expect(href).toContain('projectId=proj-a')
    await act(async () => {
      root.render(<ExecutionPlanRunLink link={undefined} />)
    })
    expect(container.textContent).toBe('')
    act(() => root.unmount())
  })
})
