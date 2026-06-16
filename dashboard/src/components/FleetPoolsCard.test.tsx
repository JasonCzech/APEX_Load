// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import FleetPoolsCard from './FleetPoolsCard'
import { FleetPool } from '../types'

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
    expect(container.textContent).toContain('1 member')
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
    expect(container.textContent).not.toContain('Provision')
    expect(container.textContent).not.toContain('Delete')
  })

  it('surfaces a provisioning request result with its status', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({})], count: 1 })
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
})
