// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import FleetGeneratorsCard from './FleetGeneratorsCard'
import { FleetGeneratorMetadata } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const fleetApiMock = {
  listFleetPools: vi.fn(),
  getFleetPool: vi.fn(),
  createFleetPool: vi.fn(),
  updateFleetPool: vi.fn(),
  deleteFleetPool: vi.fn(),
  listGeneratorMetadata: vi.fn(),
  patchGeneratorMetadata: vi.fn(),
  cordonGenerator: vi.fn(),
  uncordonGenerator: vi.fn(),
  drainGenerator: vi.fn(),
  createProvisionRequest: vi.fn(),
  getProvisionRequest: vi.fn(),
}

vi.mock('../hooks/api/useFleetApi', () => ({
  useFleetApi: () => fleetApiMock,
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

function meta(overrides: Partial<FleetGeneratorMetadata>): FleetGeneratorMetadata {
  return {
    generator_id: 'gen-1',
    region: 'us-east',
    fleet_pool_id: 'pool-1',
    provider_status: 'running',
    cordoned: false,
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('FleetGeneratorsCard', () => {
  it('shows the empty state and Add Metadata action when permitted', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage />)
    })
    await flush()
    expect(fleetApiMock.listGeneratorMetadata).toHaveBeenCalled()
    expect(container.textContent).toContain('No generator metadata yet')
    expect(container.textContent).toContain('Add Metadata')
  })

  it('exposes the metadata editor as a labelled modal and closes it with Escape', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage />)
    })
    await flush()
    const addButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add Metadata')
    await act(async () => addButton?.click())

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('fleet-generator-dialog-title')

    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('renders generators with schedulable / cordoned pills', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({
      generators: [meta({ cordoned: false, tags: { jvm: '17' } }), meta({ generator_id: 'gen-2', cordoned: true })],
      count: 2,
    })
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage />)
    })
    await flush()
    expect(container.textContent).toContain('gen-1')
    expect(container.textContent).toContain('region: us-east')
    expect(container.textContent).toContain('capabilities: jvm=17')
    expect(container.querySelector('.pp-pill-success')).toBeTruthy()
    expect(container.querySelector('.pp-pill-warning')).toBeTruthy()
  })

  it('toggles cordon state with the lifecycle endpoint', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({ cordoned: false })], count: 1 })
    fleetApiMock.cordonGenerator.mockResolvedValue({ generator_id: 'gen-1', target: 'gen-1:9090', drain_state: 'cordoned', cordoned: true })
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage />)
    })
    await flush()
    const cordonButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Cordon')
    await act(async () => {
      cordonButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    expect(fleetApiMock.cordonGenerator).toHaveBeenCalledWith('gen-1')
    expect(fleetApiMock.patchGeneratorMetadata).not.toHaveBeenCalled()
  })

  it('edits capability labels through generator metadata tags', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({ tags: { jvm: '11' } })], count: 1 })
    fleetApiMock.patchGeneratorMetadata.mockResolvedValue(meta({ tags: { jvm: '17', arch: 'amd64' } }))
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage />)
    })
    await flush()

    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Edit')
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const textarea = container.querySelector<HTMLTextAreaElement>('textarea')
    expect(textarea?.value).toBe('jvm=11')
    await act(async () => {
      if (!textarea) return
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      valueSetter?.call(textarea, 'jvm=17, arch=amd64')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Save')
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(fleetApiMock.patchGeneratorMetadata).toHaveBeenCalledWith('gen-1', expect.objectContaining({
      tags: { jvm: '17', arch: 'amd64' },
    }))
  })

  it('hides management actions without permission', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({})], count: 1 })
    await act(async () => {
      root.render(<FleetGeneratorsCard canManage={false} />)
    })
    await flush()
    expect(container.textContent).not.toContain('Add Metadata')
    expect(container.textContent).not.toContain('Cordon')
    expect(container.textContent).not.toContain('Edit')
  })
})
