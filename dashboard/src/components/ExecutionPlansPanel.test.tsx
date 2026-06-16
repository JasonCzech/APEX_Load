// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ExecutionPlansPanel from './ExecutionPlansPanel'
import { ExecutionPlan } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const plansApiMock = {
  listExecutionPlans: vi.fn(),
  getExecutionPlan: vi.fn(),
  createExecutionPlan: vi.fn(),
  updateExecutionPlan: vi.fn(),
  deleteExecutionPlan: vi.fn(),
  runExecutionPlan: vi.fn(),
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
