// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import FleetGeneratorsCard from './FleetGeneratorsCard'
import { FleetGeneratorMetadata } from '../types'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardRegisterContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const fleetApiMock = {
  listFleetPools: vi.fn(),
  getFleetPool: vi.fn(),
  createFleetPool: vi.fn(),
  updateFleetPool: vi.fn(),
  deleteFleetPool: vi.fn(),
  listGeneratorMetadata: vi.fn(),
  patchGeneratorMetadata: vi.fn(),
  deleteGeneratorMetadata: vi.fn(),
  cordonGenerator: vi.fn(),
  uncordonGenerator: vi.fn(),
  drainGenerator: vi.fn(),
  createProvisionRequest: vi.fn(),
  getProvisionRequest: vi.fn(),
}

vi.mock('../hooks/api/useFleetApi', () => ({
  useFleetApi: () => fleetApiMock,
}))

// The live inventory read; unreadable by default (state unknown).
const requestMock = vi.hoisted(() => vi.fn(() => Promise.reject(new Error('inventory unavailable'))))
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: requestMock }))
const confirmActionMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('./ux/confirmAction', () => ({ confirmAction: confirmActionMock }))

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
    target: 'gen-1:9090',
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
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [{ id: 'pool-1', name: 'Pool one' }], count: 1 })
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

  it('says a generator nobody has enrolled is awaiting enrollment, and removes its metadata', async () => {
    requestMock.mockImplementation(() => Promise.resolve({ execution_mode: 'distributed', generators: [{ generator_id: 'gen-2', target: 'gen-2:9090' }] }) as never)
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({ generator_id: 'typo', target: 'typo:19999', fleet_pool_id: '' }), meta({ generator_id: 'gen-2', target: 'gen-2:9090' })], count: 2 })
    fleetApiMock.deleteGeneratorMetadata.mockResolvedValue({ deleted: 'typo' })
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const row = (id: string) => Array.from(container.querySelectorAll('.pp-row')).find(candidate => candidate.querySelector('.pp-row-title')?.textContent?.startsWith(id))!
    expect(row('typo').textContent).toContain('awaiting enrollment')
    expect(row('gen-2').textContent).toContain('schedulable')
    const remove = Array.from(row('typo').querySelectorAll('button')).find(button => button.textContent?.trim() === 'Remove')!
    await act(async () => remove.click())
    await flush()
    expect(fleetApiMock.deleteGeneratorMetadata).toHaveBeenCalledWith('typo')
    expect(container.textContent).not.toContain('typo:19999')
    requestMock.mockImplementation(() => Promise.reject(new Error('inventory unavailable')))
  })

  it('does not judge enrollment on a controller without a generator inventory', async () => {
    requestMock.mockImplementation(() => Promise.resolve({ execution_mode: 'local', generators: [] }) as never)
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({ generator_id: 'gen-2', target: 'gen-2:9090' })], count: 1 })
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    expect(container.textContent).not.toContain('awaiting enrollment')
    expect(container.textContent).toContain('schedulable')
    requestMock.mockImplementation(() => Promise.reject(new Error('inventory unavailable')))
  })

  it('rejects a runtime target that is a URL before saving', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const add = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add Metadata')
    await act(async () => add?.click())
    const target = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(input => input.placeholder === 'host:port or pod DNS name')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(target, 'http://gen-1:9090')
      target.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('#generator-target-error')?.textContent).toContain('without http://')
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')?.disabled).toBe(true)
  })

  it('defaults a new runtime target to the generator id and sends it for pool placement', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    fleetApiMock.patchGeneratorMetadata.mockResolvedValue(meta({ generator_id: 'gen-new:9090', target: 'gen-new:9090' }))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()

    const add = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add Metadata')
    await act(async () => add?.click())
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
    const generatorID = inputs.find(input => input.placeholder === 'stable generator identity')
    const poolID = container.querySelector<HTMLSelectElement>('select')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(generatorID, 'gen-new:9090')
      generatorID?.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set?.call(poolID, 'pool-1')
      poolID?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(fleetApiMock.patchGeneratorMetadata).toHaveBeenCalledWith('gen-new:9090', expect.objectContaining({
      target: 'gen-new:9090',
      fleet_pool_id: 'pool-1',
    }))
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
      expect(textarea).toBeTruthy()
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

    expect(fleetApiMock.patchGeneratorMetadata).toHaveBeenCalledWith('gen-1', {
      tags: { jvm: '17', arch: 'amd64' },
    })
  })

  it('patches only fields changed in a stale edit snapshot', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({
      generators: [meta({ target: 'gen-1:9090', region: 'us-east', cordoned: false, tags: { arch: 'amd64' } })],
      count: 1,
    })
    fleetApiMock.patchGeneratorMetadata.mockResolvedValue(meta({ target: 'gen-1:9090', region: 'us-west', cordoned: true, tags: { arch: 'arm64' } }))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const region = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(input => input.placeholder === 'optional')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(region, 'us-west')
      region?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(fleetApiMock.patchGeneratorMetadata).toHaveBeenCalledWith('gen-1', { region: 'us-west' })
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

  it('keeps the editor open when the metadata mutation fails', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({})], count: 1 })
    fleetApiMock.patchGeneratorMetadata.mockRejectedValue(new ApiError('metadata update rejected', 409))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()

    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const textarea = container.querySelector<HTMLTextAreaElement>('textarea')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, 'jvm=18')
      textarea?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('metadata update rejected')
  })

  it('reconciles metadata after an ambiguous lifecycle failure', async () => {
    fleetApiMock.listGeneratorMetadata
      .mockResolvedValueOnce({ generators: [meta({ cordoned: false })], count: 1 })
      .mockResolvedValueOnce({ generators: [meta({ cordoned: true, updated_at: '2026-08-03T12:00:00Z' })], count: 1 })
    fleetApiMock.cordonGenerator.mockRejectedValue(new ApiError('cordon acknowledgement lost', 503))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const cordon = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cordon')
    await act(async () => cordon?.click())
    await flush()

    expect(container.textContent).toContain('cordon acknowledgement lost')
    expect(container.textContent).toContain('verify the lifecycle state')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Uncordon')).toBe(true)
  })

  it('registers typed generator metadata as a global navigation draft', async () => {
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    let latest: { guard: NavigationGuard; dirty: boolean } | null = null
    const register = (guard: NavigationGuard, dirty: boolean) => {
      latest = { guard, dirty }
      return () => undefined
    }
    await act(async () => root.render(
      <NavigationGuardRegisterContext.Provider value={register}>
        <FleetGeneratorsCard canManage />
      </NavigationGuardRegisterContext.Provider>,
    ))
    await flush()
    const add = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add Metadata')
    await act(async () => add?.click())
    const generatorID = container.querySelector<HTMLInputElement>('input[placeholder="stable generator identity"]')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(generatorID, 'gen-dirty')
      generatorID?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await flush()

    expect(latest && (latest as { dirty: boolean }).dirty).toBe(true)
    await act(async () => (latest as unknown as { guard: NavigationGuard }).guard.discard())
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('retains the committed metadata when its follow-up list request fails', async () => {
    fleetApiMock.listGeneratorMetadata
      .mockResolvedValueOnce({ generators: [meta({ tags: { jvm: '11' } })], count: 1 })
      .mockRejectedValueOnce(new ApiError('metadata refresh unavailable', 503))
    fleetApiMock.patchGeneratorMetadata.mockResolvedValue(meta({ tags: { jvm: '17' } }))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()

    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const textarea = container.querySelector<HTMLTextAreaElement>('textarea')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, 'jvm=17')
      textarea?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.textContent).toContain('capabilities: jvm=17')
    expect(container.textContent).toContain('metadata refresh unavailable')
    expect(container.textContent).toContain('Retry generator metadata')
    expect(fleetApiMock.patchGeneratorMetadata).toHaveBeenCalledTimes(1)
  })

  it('keeps an in-flight editor locked until its save finishes', async () => {
    let resolvePatch!: (value: FleetGeneratorMetadata) => void
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [meta({})], count: 1 })
    fleetApiMock.patchGeneratorMetadata.mockReturnValue(new Promise(resolve => { resolvePatch = resolve }))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const textarea = container.querySelector<HTMLTextAreaElement>('textarea')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, 'arch=arm64')
      textarea?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    act(() => save?.click())

    const close = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Close')
    const cancel = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')
    const add = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add Metadata')
    expect(close?.disabled).toBe(true)
    expect(cancel?.disabled).toBe(true)
    expect(add?.disabled).toBe(true)
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).not.toBeNull()

    await act(async () => {
      resolvePatch(meta({ updated_at: '2026-08-03T12:00:00Z' }))
      await Promise.resolve()
    })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('accepts a newer server snapshot over an older committed response', async () => {
    fleetApiMock.listGeneratorMetadata
      .mockResolvedValueOnce({ generators: [meta({ tags: { jvm: '11' }, updated_at: '2026-08-03T10:00:00Z' })], count: 1 })
      .mockResolvedValueOnce({ generators: [meta({ tags: { jvm: '21' }, updated_at: '2026-08-03T12:00:00Z' })], count: 1 })
    fleetApiMock.patchGeneratorMetadata.mockResolvedValue(meta({ tags: { jvm: '17' }, updated_at: '2026-08-03T11:00:00Z' }))
    await act(async () => root.render(<FleetGeneratorsCard canManage />))
    await flush()
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const textarea = container.querySelector<HTMLTextAreaElement>('textarea')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, 'jvm=17')
      textarea?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(container.textContent).toContain('capabilities: jvm=21')
    expect(container.textContent).not.toContain('capabilities: jvm=17')
  })
})
