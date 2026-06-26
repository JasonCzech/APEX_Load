// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProjectPoolQuotasCard from './ProjectPoolQuotasCard'
import { Project } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const fleetApiMock = {
  listFleetPools: vi.fn().mockResolvedValue({ fleet_pools: [{ id: 'pool-east', name: 'East', status: 'active' }], count: 1 }),
}
const projectApiMock = { updateProject: vi.fn() }

vi.mock('../hooks/api/useFleetApi', () => ({ useFleetApi: () => fleetApiMock }))
vi.mock('../hooks/api/useProjectApi', () => ({ useProjectApi: () => projectApiMock }))

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

function project(overrides: Partial<Project> = {}): Project {
  return {
    id: 'p1',
    name: 'Project One',
    settings: { max_concurrent_tests: 5 },
    ...overrides,
  } as unknown as Project
}

describe('ProjectPoolQuotasCard', () => {
  it('adds a per-pool quota and saves it while preserving other settings', async () => {
    projectApiMock.updateProject.mockResolvedValue(project())
    await act(async () => {
      root.render(<ProjectPoolQuotasCard project={project()} canManage />)
    })
    await flush()

    // Add a quota row for the East pool.
    const select = container.querySelector<HTMLSelectElement>('select[aria-label="Add pool quota"]')
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(select, 'pool-east')
      select?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(container.textContent).toContain('East')

    // Set Max VUsers = 500.
    const numInput = container.querySelector<HTMLInputElement>('input[type="number"]')
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(numInput, '500')
      numInput?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await flush()

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Save pool quotas')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(projectApiMock.updateProject).toHaveBeenCalledTimes(1)
    const [id, body] = projectApiMock.updateProject.mock.calls[0] as [string, { settings: { max_concurrent_tests?: number; pool_quotas?: Record<string, { max_vusers?: number }> } }]
    expect(id).toBe('p1')
    expect(body.settings.max_concurrent_tests).toBe(5) // preserved
    expect(body.settings.pool_quotas?.['pool-east']).toEqual({ max_vusers: 500 })
  })

  it('seeds rows from existing pool quotas', async () => {
    await act(async () => {
      root.render(<ProjectPoolQuotasCard project={project({ settings: { pool_quotas: { 'pool-east': { max_concurrent_tests: 3 } } } } as Partial<Project>)} canManage />)
    })
    await flush()
    expect(container.textContent).toContain('East')
    const concurrentInput = container.querySelectorAll<HTMLInputElement>('input[type="number"]')[1]
    expect(concurrentInput?.value).toBe('3')
  })
})
