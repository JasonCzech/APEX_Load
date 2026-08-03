// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import FleetPoolsCard from './FleetPoolsCard'
import { FleetPool } from '../types'
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
  createProvisionRequest: vi.fn(),
  listProvisionRequests: vi.fn(),
  getProvisionRequest: vi.fn(),
  cancelProvisionRequest: vi.fn(),
  retryProvisionRequest: vi.fn(),
  getProvisioner: vi.fn().mockResolvedValue(null),
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
  fleetApiMock.listProvisionRequests.mockResolvedValue({ provisioning_requests: [], count: 0 })
  fleetApiMock.getProvisioner.mockResolvedValue(null)
  fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
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

function pool(overrides: Partial<FleetPool>): FleetPool {
  return {
    id: 'pool-1',
    name: 'east-http',
    cloud_provider: 'kubernetes',
    region: 'us-east',
    min_size: 1,
    max_size: 10,
    desired_size: 4,
    status: 'active',
    created_at: '',
    updated_at: '',
    members: [],
    ...overrides,
  }
}

function members(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    id: `member-${index}`,
    pool_id: 'pool-1',
    generator_id: `generator-${index}`,
    target: `generator-${index}:9090`,
    status: 'ready',
    joined_at: '',
  }))
}

describe('FleetPoolsCard', () => {
  it('shows the empty state and New Pool action when permitted', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()
    expect(fleetApiMock.listFleetPools).toHaveBeenCalled()
    expect(container.textContent).toContain('No managed generator pools')
    expect(container.textContent).toContain('New Pool')
  })

  it('opens a labelled pool dialog, focuses the name, and restores focus on Escape', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()

    const trigger = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'New Pool')
    trigger?.focus()
    await act(async () => {
      trigger?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushFocus()

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    const name = container.querySelector<HTMLInputElement>('input[placeholder="e.g. east-http"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('New Pool')
    expect(document.activeElement).toBe(name)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('announces asynchronous loading errors', async () => {
    fleetApiMock.listFleetPools.mockRejectedValue(new ApiError('fleet service unavailable', 503))
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('fleet service unavailable')
  })

  it('renders pools with status pill and member counts', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({
      fleet_pools: [pool({ status: 'active', members: [{ id: 'm1', pool_id: 'pool-1', generator_id: 'g1', target: 'g1:7700', status: 'ready', joined_at: '' }] })],
      count: 1,
    })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()
    expect(container.textContent).toContain('east-http')
    expect(container.querySelector('.pp-pill-success')).toBeTruthy()
    expect(container.textContent).toContain('kubernetes')
    expect(container.textContent).toContain('size 4 (1–10)')
    expect(container.textContent).toContain('1 runtime member')
    expect(container.textContent).toContain('Recent Provisioning')
  })

  it('renders the deduplicated runtime union of explicit and metadata members', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({
      fleet_pools: [pool({ members: members(1) })],
      count: 1,
    })
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({
      generators: [
        { generator_id: 'duplicate', target: 'generator-0:9090', fleet_pool_id: 'pool-1', created_at: '', updated_at: '' },
        { generator_id: 'metadata-only', target: 'generator-1:9090', fleet_pool_id: 'pool-1', created_at: '', updated_at: '' },
        { generator_id: 'other-pool', target: 'generator-2:9090', fleet_pool_id: 'pool-2', created_at: '', updated_at: '' },
      ],
      count: 3,
    })
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()

    expect(container.textContent).toContain('2 runtime members')
  })

  it('surfaces the active on-demand provisioner status', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({ members: members(4) })], count: 1 })
    fleetApiMock.getProvisioner.mockResolvedValue({ name: 'kubernetes', can_provision: true, description: 'in-cluster' })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()
    expect(fleetApiMock.getProvisioner).toHaveBeenCalled()
    expect(container.textContent).toContain('provisioner: kubernetes')
    expect(container.textContent).toContain('on-demand')
  })

  it('maps suspended status to the warning pill', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({ status: 'suspended' })], count: 1 })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()
    expect(container.querySelector('.pp-pill-warning')).toBeTruthy()
  })

  it('hides management actions without permission', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    await act(async () => {
      root.render(<FleetPoolsCard canManage={false} />)
    })
    await flush()
    expect(container.textContent).not.toContain('New Pool')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Provision')).toBe(false)
    expect(container.textContent).not.toContain('Delete')
  })

  it('surfaces a provisioning request result with its status', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({ members: members(4) })], count: 1 })
    fleetApiMock.listProvisionRequests.mockResolvedValueOnce({ provisioning_requests: [], count: 0 }).mockResolvedValue({
      provisioning_requests: [{
        id: 'req-1',
        fleet_pool_id: 'pool-1',
        status: 'not_configured',
        desired_count: 4,
        events: [{ id: 'e1', request_id: 'req-1', event_type: 'provider_unconfigured', message: 'no provider wired', timestamp: '' }],
        created_at: '',
        updated_at: '',
      }],
      count: 1,
    })
    fleetApiMock.createProvisionRequest.mockResolvedValue({
      id: 'req-1',
      fleet_pool_id: 'pool-1',
      status: 'not_configured',
      desired_count: 4,
      events: [{ id: 'e1', request_id: 'req-1', event_type: 'provider_unconfigured', message: 'no provider wired', timestamp: '' }],
      created_at: '',
      updated_at: '',
    })
    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()

    const provisionButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Provision')
    await act(async () => {
      provisionButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const requestButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Request provisioning')
    await act(async () => {
      requestButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(fleetApiMock.createProvisionRequest).toHaveBeenCalledWith({ fleet_pool_id: 'pool-1', desired_count: 4 })
    expect(container.textContent).toContain('not_configured')
    expect(container.textContent).toContain('provider_unconfigured')
  })

  it('blocks fresh-pool provisioning until runtime targets are associated', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({ desired_size: 2, members: [] })], count: 1 })
    fleetApiMock.getProvisioner.mockResolvedValue({ name: 'kubernetes', can_provision: true, description: 'in-cluster' })
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()
    const provision = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Provision')
    await act(async () => provision?.click())

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('0 associated runtime targets')
    const request = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Request provisioning')
    expect(request?.disabled).toBe(true)
    expect(fleetApiMock.createProvisionRequest).not.toHaveBeenCalled()
  })

  it('enforces provisioning min and max boundaries before submitting', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({
      fleet_pools: [pool({ min_size: 2, max_size: 4, desired_size: 2, members: members(4) })],
      count: 1,
    })
    fleetApiMock.createProvisionRequest.mockResolvedValue({
      id: 'req-boundary',
      fleet_pool_id: 'pool-1',
      status: 'not_configured',
      desired_count: 4,
      created_at: '',
      updated_at: '',
    })
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()

    const provision = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Provision')
    await act(async () => provision?.click())
    const countInput = container.querySelector<HTMLInputElement>('input[type="number"]')
    const requestButton = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Request provisioning')
    const setCount = async (value: string) => {
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        setter?.call(countInput, value)
        countInput?.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }

    expect(countInput?.min).toBe('2')
    expect(countInput?.max).toBe('4')
    expect(requestButton()?.disabled).toBe(false)

    await setCount('1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("minimum of 2")
    expect(requestButton()?.disabled).toBe(true)

    await setCount('5')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("maximum of 4")
    expect(requestButton()?.disabled).toBe(true)

    await setCount('4')
    expect(requestButton()?.disabled).toBe(false)
    await act(async () => requestButton()?.click())
    await flush()

    expect(fleetApiMock.createProvisionRequest).toHaveBeenCalledWith({ fleet_pool_id: 'pool-1', desired_count: 4 })
  })

  it('rejects impossible pool sizes before calling the API', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Pool')
    await act(async () => create?.click())
    const name = container.querySelector<HTMLInputElement>('input[placeholder="e.g. east-http"]')
    const numbers = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="number"]'))
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(name, 'invalid-pool')
      name?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(numbers[0], '2')
      numbers[0]?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(numbers[1], '1')
      numbers[1]?.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Desired size cannot be lower than min size')
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create')
    expect(submit?.disabled).toBe(true)
    expect(fleetApiMock.createFleetPool).not.toHaveBeenCalled()
  })

  it('registers typed pool settings as a global navigation draft', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    let latest: { guard: NavigationGuard; dirty: boolean } | null = null
    const register = (guard: NavigationGuard, dirty: boolean) => {
      latest = { guard, dirty }
      return () => undefined
    }
    await act(async () => root.render(
      <NavigationGuardRegisterContext.Provider value={register}>
        <FleetPoolsCard canManage />
      </NavigationGuardRegisterContext.Provider>,
    ))
    await flush()
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Pool')
    await act(async () => create?.click())
    const name = container.querySelector<HTMLInputElement>('input[placeholder="e.g. east-http"]')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(name, 'dirty-pool')
      name?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await flush()

    expect(latest && (latest as { dirty: boolean }).dirty).toBe(true)
    await act(async () => (latest as unknown as { guard: NavigationGuard }).guard.discard())
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('renders provisioning history and retries eligible requests', async () => {
    const failedRequest = {
      id: 'req-failed',
      fleet_pool_id: 'pool-1',
      status: 'failed',
      desired_count: 6,
      events: [{ id: 'e1', request_id: 'req-failed', event_type: 'failure', message: 'api server unreachable', timestamp: '' }],
      created_at: '',
      updated_at: '',
    }
    const successRequest = {
      ...failedRequest,
      status: 'success',
      events: [...failedRequest.events, { id: 'e2', request_id: 'req-failed', event_type: 'success', message: 'scaled apexload-generator to 6 replicas', timestamp: '' }],
    }
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    fleetApiMock.listProvisionRequests
      .mockResolvedValueOnce({ provisioning_requests: [failedRequest], count: 1 })
      .mockResolvedValue({ provisioning_requests: [successRequest], count: 1 })
    fleetApiMock.retryProvisionRequest.mockResolvedValue(successRequest)

    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()

    expect(fleetApiMock.listProvisionRequests).toHaveBeenCalledWith({ limit: 10 })
    expect(container.textContent).toContain('req-failed')
    expect(container.textContent).toContain('api server unreachable')

    const retryButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Retry')
    await act(async () => {
      retryButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(fleetApiMock.retryProvisionRequest).toHaveBeenCalledWith('req-failed')
    expect(container.textContent).toContain('scaled apexload-generator to 6 replicas')
  })

  it('cancels active provisioning requests from history', async () => {
    const activeRequest = {
      id: 'req-active',
      fleet_pool_id: 'pool-1',
      status: 'provisioning',
      desired_count: 3,
      events: [{ id: 'e1', request_id: 'req-active', event_type: 'provisioning', message: 'scaling pool', timestamp: '' }],
      created_at: '',
      updated_at: '',
    }
    const cancelledRequest = {
      ...activeRequest,
      status: 'cancelled',
      events: [...activeRequest.events, { id: 'e2', request_id: 'req-active', event_type: 'cancelled', message: 'operator cancelled', timestamp: '' }],
    }
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    fleetApiMock.listProvisionRequests
      .mockResolvedValueOnce({ provisioning_requests: [activeRequest], count: 1 })
      .mockResolvedValue({ provisioning_requests: [cancelledRequest], count: 1 })
    fleetApiMock.cancelProvisionRequest.mockResolvedValue(cancelledRequest)

    await act(async () => {
      root.render(<FleetPoolsCard canManage />)
    })
    await flush()

    const cancelButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Cancel')
    await act(async () => {
      cancelButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(fleetApiMock.cancelProvisionRequest).toHaveBeenCalledWith('req-active')
    expect(container.textContent).toContain('operator cancelled')
  })

  it('keeps an authoritative pool update when reconciliation fails', async () => {
    fleetApiMock.listFleetPools
      .mockResolvedValueOnce({ fleet_pools: [pool({ name: 'east-http' })], count: 1 })
      .mockRejectedValueOnce(new ApiError('pool refresh unavailable', 503))
    fleetApiMock.updateFleetPool.mockResolvedValue(pool({ name: 'east-http-updated' }))
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()

    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const name = container.querySelector<HTMLInputElement>('input[placeholder="e.g. east-http"]')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(name, 'east-http-updated')
      name?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.textContent).toContain('east-http-updated')
    expect(container.textContent).toContain('pool refresh unavailable')
    expect(container.textContent).toContain('Retry fleet data')
  })

  it('surfaces secondary fleet refresh failures without erasing pool data', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    fleetApiMock.listProvisionRequests.mockRejectedValue(new ApiError('history unavailable', 503))
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()

    expect(container.textContent).toContain('east-http')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('history unavailable')
    expect(container.textContent).toContain('Retry fleet data')
  })

  it('does not resurrect a deleted pool from a stale follow-up list', async () => {
    const stale = { fleet_pools: [pool({})], count: 1 }
    fleetApiMock.listFleetPools.mockResolvedValue(stale)
    fleetApiMock.deleteFleetPool.mockResolvedValue({ deleted: 'pool-1' })
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()

    const remove = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Delete')
    await act(async () => remove?.click())
    const reason = container.querySelector<HTMLInputElement>('input[placeholder="Operational reason"]')
    const confirmation = container.querySelector<HTMLInputElement>('input[placeholder="east-http"]')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(reason, 'retire pool')
      reason?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(confirmation, 'east-http')
      confirmation?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const confirm = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Delete pool')
    await act(async () => confirm?.click())
    await flush()

    expect(fleetApiMock.deleteFleetPool).toHaveBeenCalledWith('pool-1')
    expect(container.textContent).not.toContain('east-http')
    expect(container.textContent).toContain('0 managed pools')
  })

  it('locks an in-flight pool editor until its save completes', async () => {
    let resolveUpdate!: (value: FleetPool) => void
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    fleetApiMock.updateFleetPool.mockReturnValue(new Promise(resolve => { resolveUpdate = resolve }))
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    act(() => save?.click())

    const close = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Close')
    const cancel = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Pool')
    expect(close?.disabled).toBe(true)
    expect(cancel?.disabled).toBe(true)
    expect(create?.disabled).toBe(true)
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).not.toBeNull()

    await act(async () => {
      resolveUpdate(pool({ updated_at: '2026-08-03T12:00:00Z' }))
      await Promise.resolve()
    })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('does not mask a newer provisioning status with an older mutation response', async () => {
    const failed = {
      id: 'req-newer', fleet_pool_id: 'pool-1', status: 'failed', desired_count: 2,
      events: [], created_at: '', updated_at: '2026-08-03T10:00:00Z',
    }
    const accepted = { ...failed, status: 'accepted', updated_at: '2026-08-03T11:00:00Z' }
    const completed = { ...failed, status: 'success', updated_at: '2026-08-03T12:00:00Z' }
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
    fleetApiMock.listProvisionRequests
      .mockResolvedValueOnce({ provisioning_requests: [failed], count: 1 })
      .mockResolvedValueOnce({ provisioning_requests: [completed], count: 1 })
    fleetApiMock.retryProvisionRequest.mockResolvedValue(accepted)
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Retry')
    await act(async () => retry?.click())
    await flush()

    expect(container.textContent).toContain('success')
    expect(container.textContent).not.toContain('accepted')
  })

  it('does not offer a load retry for a later mutation failure', async () => {
    fleetApiMock.listFleetPools
      .mockResolvedValueOnce({ fleet_pools: [pool({})], count: 1 })
      .mockRejectedValueOnce(new ApiError('pool refresh unavailable', 503))
    fleetApiMock.updateFleetPool
      .mockResolvedValueOnce(pool({ name: 'committed-pool', updated_at: '2026-08-03T11:00:00Z' }))
      .mockRejectedValueOnce(new ApiError('pool mutation rejected', 409))
    await act(async () => root.render(<FleetPoolsCard canManage />))
    await flush()
    let edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    let save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()
    expect(container.textContent).toContain('Retry fleet data')

    edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')
    await act(async () => edit?.click())
    save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Save')
    await act(async () => save?.click())
    await flush()

    expect(container.textContent).toContain('pool mutation rejected')
    expect(container.textContent).not.toContain('Retry fleet data')
    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
  })
})

async function flushFocus() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}
