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
  createScenario: vi.fn(),
  updateScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listScenarioResults: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
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