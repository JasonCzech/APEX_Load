// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import PoolAllocationCard from './PoolAllocationCard'
import { FleetPool } from '../types'
import type { ClusterCapacityResponse } from '../types/cluster'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const clustersApiMock = {
  listClusters: vi.fn(),
  getCluster: vi.fn(),
  createCluster: vi.fn(),
  updateCluster: vi.fn(),
  deleteCluster: vi.fn(),
  testCluster: vi.fn(),
  getClusterCapacity: vi.fn(),
  updatePoolAllocation: vi.fn(),
  getPoolAllocationPreflight: vi.fn(),
}

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
  getProvisioner: vi.fn(),
}

vi.mock('../hooks/api/useClustersApi', () => ({ useClustersApi: () => clustersApiMock }))
vi.mock('../hooks/api/useFleetApi', () => ({ useFleetApi: () => fleetApiMock }))

let container: HTMLElement
let root: Root

function pool(overrides: Partial<FleetPool> = {}): FleetPool {
  return {
    id: 'pool-1',
    name: 'gen-pool',
    cloud_provider: 'kubernetes',
    status: 'active',
    cluster_id: 'cluster-1',
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T01:00:00Z',
    ...overrides,
  }
}

const capacity: ClusterCapacityResponse = {
  cluster_id: 'cluster-1',
  namespace: 'apex',
  node_capacity: {
    available: true,
    node_pools: [
      { name: 'loadgen', zone: 'eastus-2', node_count: 3, schedulable_nodes: 3, allocatable_cpu_millis: 3860, allocatable_memory_bytes: 12_000_000_000 },
    ],
  },
  quotas: [{ name: 'apex', hard: { 'requests.cpu': '8', 'requests.memory': '16Gi', pods: '20' }, used: { 'requests.cpu': '2', 'requests.memory': '4Gi', pods: '2' } }],
  generator_workload: { name: 'apexload-generator', replicas: 2, ready_replicas: 2, container: 'generator', current_requests: { cpu: '1', memory: '2Gi' } },
}

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool()], count: 1 })
  clustersApiMock.listClusters.mockResolvedValue({ clusters: [], total: 0 })
  clustersApiMock.getClusterCapacity.mockResolvedValue(capacity)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
  vi.useRealTimers()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function fieldByLabel(label: string): HTMLInputElement | undefined {
  return Array.from(container.querySelectorAll('label')).find(
    node => node.querySelector('span')?.textContent === label,
  )?.querySelector('input') as HTMLInputElement | undefined
}

function buttonByText(label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(
    button => button.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined
}

async function renderAndSelectPool(canManage = true) {
  await act(async () => {
    root.render(<PoolAllocationCard canManage={canManage} />)
  })
  await flush()
  const poolSelect = container.querySelector('select') as HTMLSelectElement
  await act(async () => {
    setSelectValue(poolSelect, 'pool-1')
  })
  await flush()
}

describe('PoolAllocationCard', () => {
  it('renders quota headroom with a projection of the pending change', async () => {
    await renderAndSelectPool()
    // Current + "after this change" rows for CPU, memory, and pods.
    expect(container.querySelectorAll('.pp-bar-fill').length).toBe(6)
    expect(container.textContent).toContain('Namespace quota')
  })

  it('disables save while the allocation is invalid and shows the model message', async () => {
    await renderAndSelectPool()

    const cpuRequest = fieldByLabel('CPU request')!
    const cpuLimit = fieldByLabel('CPU limit')!
    await act(async () => {
      setInputValue(cpuRequest, '1')
      setInputValue(cpuLimit, '500m')
    })
    await flush()

    // The card must render the model's verdict rather than reimplementing it.
    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toMatch(/cannot exceed/)
    expect(buttonByText('Apply allocation')?.disabled).toBe(true)

    await act(async () => {
      setInputValue(cpuLimit, '2')
    })
    await flush()
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(buttonByText('Apply allocation')?.disabled).toBe(false)
  })

  it('disables the toleration value when the operator is Exists', async () => {
    await renderAndSelectPool()
    await act(async () => {
      buttonByText('Add toleration')?.click()
    })
    await flush()

    const entry = container.querySelector('.pp-entry')!
    const operator = entry.querySelector('select') as HTMLSelectElement
    const valueInput = entry.querySelectorAll('input')[1] as HTMLInputElement
    expect(valueInput.disabled).toBe(false)

    await act(async () => {
      setSelectValue(operator, 'Exists')
    })
    await flush()
    // Kubernetes forbids a value alongside Exists, so the input must be blocked.
    expect((container.querySelector('.pp-entry')!.querySelectorAll('input')[1] as HTMLInputElement).disabled).toBe(true)
  })

  it('sends node pool and zone as dedicated fields with the pool CAS token', async () => {
    clustersApiMock.updatePoolAllocation.mockResolvedValue({ pool_id: 'pool-1', applied: true, container: 'generator' })
    await renderAndSelectPool()

    await act(async () => {
      setInputValue(fieldByLabel('CPU request')!, '500m')
      setInputValue(fieldByLabel('Node pool')!, 'loadgen')
      setInputValue(fieldByLabel('Zone')!, 'eastus-2')
    })
    await flush()

    await act(async () => {
      buttonByText('Apply allocation')?.click()
    })
    await flush()

    expect(clustersApiMock.updatePoolAllocation).toHaveBeenCalledTimes(1)
    const [poolId, body] = clustersApiMock.updatePoolAllocation.mock.calls[0]
    expect(poolId).toBe('pool-1')
    expect(body.cpu_request).toBe('500m')
    expect(body.node_pool).toBe('loadgen')
    expect(body.zone).toBe('eastus-2')
    expect(body.cluster_id).toBe('cluster-1')
    // The CAS token is what makes a concurrent edit 409 instead of clobbering.
    expect(body.updated_at).toBe('2026-08-06T01:00:00Z')
  })

  it('explains an unbound pool instead of offering an allocation form', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [pool({ cluster_id: '' })], count: 1 })
    await renderAndSelectPool()
    expect(container.textContent).toContain('not bound to a cluster connection')
    expect(fieldByLabel('CPU request')).toBeUndefined()
  })

  it('warns when node capacity is unavailable but still renders quota bars', async () => {
    clustersApiMock.getClusterCapacity.mockResolvedValue({
      ...capacity,
      node_capacity: { available: false, unavailable_reason: 'rbac_forbidden' },
    })
    await renderAndSelectPool()
    // Missing the optional node ClusterRole degrades, it does not break.
    expect(container.textContent).toContain('rbac_forbidden')
    expect(container.querySelectorAll('.pp-bar-fill').length).toBe(6)
  })

  it('hides management actions without permission', async () => {
    await renderAndSelectPool(false)
    expect(buttonByText('Apply allocation')).toBeUndefined()
    expect(buttonByText('Add toleration')).toBeUndefined()
    expect(buttonByText('Add selector')).toBeUndefined()
    // The reads behind this card are system-admin only too, so a real non-admin
    // gets a 403; the card states the requirement either way.
    expect(container.textContent).toContain('manage_system')
  })
})
