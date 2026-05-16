// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestHub from './TestHub'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  listScripts: vi.fn(),
  listDataPools: vi.fn(),
  estimateTest: vi.fn(),
  createScenario: vi.fn(),
  updateScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listScenarioResults: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

describe('TestHub', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.listScenarios.mockResolvedValue({ scenarios: [], count: 0 })
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [], count: 0 })
    apiMock.estimateTest.mockResolvedValue({
      duration: '1m',
      groups: [],
      assignment: { mode: 'auto' },
      footprint: {
        total_vusers: 10,
        duration: '1m',
        complexity_score: 1.25,
        estimated_cpu_cores: 0.01,
        estimated_memory_mb: 8,
        estimated_network_mbps: 1,
      },
      assignment_recommendation: {
        mode: 'auto',
        recommended_targets: ['gen-a'],
        summary: 'Auto-assignment recommends 1 host for the current scenario footprint.',
      },
    })
    apiMock.createScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.updateScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.deleteScenario.mockResolvedValue({ deleted: 'scenario-1' })
    apiMock.listScenarioResults.mockResolvedValue({ tests: [], count: 0 })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('creates a scenario from the typed form payload', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1')
    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')

    const newScenarioButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('New Scenario'))
    expect(newScenarioButton).toBeDefined()
    if (!newScenarioButton) return

    await act(async () => {
      newScenarioButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const inputs = Array.from(container.querySelectorAll('input'))
    const nameInput = inputs.find(input => input.placeholder === 'Scenario name')
    const durationInput = inputs.find(input => input.placeholder === 'Duration, e.g. 5m')
    const vusersInput = inputs.find(input => input.getAttribute('type') === 'number')

    expect(nameInput).toBeDefined()
    expect(durationInput).toBeDefined()
    expect(vusersInput).toBeDefined()
    if (!nameInput || !durationInput || !vusersInput) return

    await act(async () => {
      setInputValue(nameInput, 'Checkout Scenario')
      setInputValue(durationInput, '5m')
      setInputValue(vusersInput, '25')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Scenario'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith({
      name: 'Checkout Scenario',
      duration: '5m',
      scenario_mode: 'vuser_driven',
      script_shakeout: false,
      resource_baseline: false,
      assignment: { mode: 'auto' },
      groups: [
        {
          script_id: 'script-1',
          vusers: 25,
          initialization: 'at_runtime',
        },
      ],
      data_pools: [],
    })
  })

  it('shows saved assignment defaults in the scenario library', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Pinned Checkout',
        duration: '5m',
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a', 'gen-b'],
        },
        scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Pinned Checkout')
    expect(container.textContent).toContain('Specific Hosts')
    expect(container.textContent).toContain('gen-a, gen-b')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}