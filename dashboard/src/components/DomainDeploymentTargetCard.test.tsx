// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DomainDeploymentTargetCard from './DomainDeploymentTargetCard'
import { conflictingPoolsFromError } from './deploymentTargetModel'
import { ApiError } from '../hooks/apiClient'
import type { ClusterConnection } from '../types/cluster'
import type { DomainDeploymentTarget } from '../types/deploymentTarget'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const deploymentTargetsApiMock = {
  listDeploymentTargets: vi.fn(),
  getDeploymentTarget: vi.fn(),
  updateDeploymentTarget: vi.fn(),
  clearDeploymentTarget: vi.fn(),
}
const clustersApiMock = { listClusters: vi.fn() }
const fleetApiMock = { listFleetPools: vi.fn() }

vi.mock('../hooks/api/useDeploymentTargetsApi', () => ({
  useDeploymentTargetsApi: () => deploymentTargetsApiMock,
}))
vi.mock('../hooks/api/useClustersApi', () => ({ useClustersApi: () => clustersApiMock }))
vi.mock('../hooks/api/useFleetApi', () => ({ useFleetApi: () => fleetApiMock }))

let container: HTMLElement
let root: Root

function cluster(id: string, kind: ClusterConnection['kind']): ClusterConnection {
  return {
    id,
    name: `${id}-name`,
    kind,
    namespace: 'apex',
    tls_mode: 'system_roots',
    auth_mode: kind === 'eks' ? 'aws_iam' : 'azure_service_principal',
    status: 'active',
    has_client_secret: false,
    credential_source: 'unset',
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T00:00:00Z',
  }
}

function target(overrides: Partial<DomainDeploymentTarget> = {}): DomainDeploymentTarget {
  return {
    domain_id: 'domain-1',
    domain_name: 'Payments',
    configured: false,
    scoped_pool_count: 0,
    ...overrides,
  }
}

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function render(canManage = true) {
  await act(async () => {
    root.render(<DomainDeploymentTargetCard domainId="domain-1" domainName="Payments" canManage={canManage} />)
  })
  await flush()
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find((button) =>
    (button.textContent ?? '').includes(text),
  )
}

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  vi.clearAllMocks()
  deploymentTargetsApiMock.getDeploymentTarget.mockResolvedValue(target())
  clustersApiMock.listClusters.mockResolvedValue({
    clusters: [cluster('c-aks', 'aks'), cluster('c-eks', 'eks')],
    total: 2,
  })
  fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

describe('DomainDeploymentTargetCard', () => {
  it('renders an unconfigured domain as unconstrained rather than as an error', async () => {
    await render()
    expect(container.textContent).toContain('Unconstrained')
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('offers only the clusters the chosen provider permits', async () => {
    await render()
    const selects = container.querySelectorAll('select')
    const providerSelect = selects[0] as HTMLSelectElement
    const clusterSelect = selects[1] as HTMLSelectElement

    await act(async () => {
      providerSelect.value = 'aws_eks'
      providerSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()

    const options = Array.from(clusterSelect.querySelectorAll('option')).map((option) => option.value)
    expect(options).toContain('c-eks')
    expect(options).not.toContain('c-aks')
  })

  it('saves the selected provider', async () => {
    deploymentTargetsApiMock.updateDeploymentTarget.mockResolvedValue(
      target({ configured: true, provider: 'aws_eks', updated_at: '2026-08-06T02:00:00Z' }),
    )
    await render()

    const providerSelect = container.querySelectorAll('select')[0] as HTMLSelectElement
    await act(async () => {
      providerSelect.value = 'aws_eks'
      providerSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()

    await act(async () => {
      buttonByText('Save deployment target')?.click()
    })
    await flush()

    expect(deploymentTargetsApiMock.updateDeploymentTarget).toHaveBeenCalledWith(
      'domain-1',
      expect.objectContaining({ provider: 'aws_eks' }),
    )
    // A first write must NOT carry a concurrency token.
    const [, payload] = deploymentTargetsApiMock.updateDeploymentTarget.mock.calls[0]
    expect('updated_at' in payload).toBe(false)
  })

  it('blocks Save and lists the conflicts when a pool is bound to another cloud', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({
      fleet_pools: [
        {
          id: 'pool-1',
          name: 'east-http',
          cloud_provider: 'azure',
          status: 'active',
          domain_id: 'domain-1',
          cluster_id: 'c-aks',
          created_at: '2026-08-06T00:00:00Z',
          updated_at: '2026-08-06T00:00:00Z',
        },
      ],
      count: 1,
    })
    await render()

    const providerSelect = container.querySelectorAll('select')[0] as HTMLSelectElement
    await act(async () => {
      providerSelect.value = 'aws_eks'
      providerSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()

    expect(container.querySelector('[data-testid="deployment-target-conflicts"]')?.textContent).toContain('east-http')
    expect(buttonByText('Save deployment target')?.disabled).toBe(true)
    expect(deploymentTargetsApiMock.updateDeploymentTarget).not.toHaveBeenCalled()
  })

  it('surfaces the conflicting pools the server reports on a 409', async () => {
    // A REAL ApiError carrying the REAL server body. writeJSONError sends
    // {error, detail} and apiClient puts that whole body on .payload — an
    // earlier version of this test invented a top-level `detail`, so it passed
    // against a shape production never produces.
    deploymentTargetsApiMock.updateDeploymentTarget.mockRejectedValue(
      new ApiError('fleet pools conflict', 409, {
        error: 'fleet pools conflict',
        detail: {
          provider: 'aws_eks',
          conflicting_pools: [{ pool_id: 'pool-9', pool_name: 'legacy-pool', reason: 'bound to an Azure cluster' }],
        },
      }),
    )
    await render()

    const providerSelect = container.querySelectorAll('select')[0] as HTMLSelectElement
    await act(async () => {
      providerSelect.value = 'aws_eks'
      providerSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    await act(async () => {
      buttonByText('Save deployment target')?.click()
    })
    await flush()

    expect(container.querySelector('[data-testid="deployment-target-conflicts"]')?.textContent).toContain('legacy-pool')
  })

  it('disables every control for a non-superuser', async () => {
    await render(false)
    for (const element of container.querySelectorAll('select')) {
      expect((element as HTMLSelectElement).disabled).toBe(true)
    }
    expect(buttonByText('Save deployment target')?.disabled).toBe(true)
    expect(buttonByText('Clear constraint')?.disabled).toBe(true)
  })

  it('clears the constraint on an already-configured domain', async () => {
    deploymentTargetsApiMock.getDeploymentTarget.mockResolvedValue(
      target({ configured: true, provider: 'azure_aks', updated_at: '2026-08-06T02:00:00Z' }),
    )
    deploymentTargetsApiMock.clearDeploymentTarget.mockResolvedValue(target())
    await render()

    await act(async () => {
      buttonByText('Clear constraint')?.click()
    })
    await flush()

    expect(deploymentTargetsApiMock.clearDeploymentTarget).toHaveBeenCalledWith('domain-1')
    expect(container.textContent).toContain('no longer constrained')
  })

  it('sends the concurrency token when editing a configured domain', async () => {
    deploymentTargetsApiMock.getDeploymentTarget.mockResolvedValue(
      target({ configured: true, provider: 'azure_aks', updated_at: '2026-08-06T02:00:00Z' }),
    )
    deploymentTargetsApiMock.updateDeploymentTarget.mockResolvedValue(
      target({ configured: true, provider: 'aws_eks', updated_at: '2026-08-06T03:00:00Z' }),
    )
    await render()

    const providerSelect = container.querySelectorAll('select')[0] as HTMLSelectElement
    await act(async () => {
      providerSelect.value = 'aws_eks'
      providerSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    await act(async () => {
      buttonByText('Save deployment target')?.click()
    })
    await flush()

    const [, payload] = deploymentTargetsApiMock.updateDeploymentTarget.mock.calls[0]
    expect(payload.updated_at).toBe('2026-08-06T02:00:00Z')
  })
})

describe('conflictingPoolsFromError', () => {
  it('reads the conflicts out of the real ApiError payload shape', () => {
    const error = new ApiError('conflict', 409, {
      error: 'conflict',
      detail: {
        provider: 'aws_eks',
        conflicting_pools: [{ pool_id: 'p1', pool_name: 'east-http', reason: 'bound to an Azure cluster' }],
      },
    })
    expect(conflictingPoolsFromError(error).map((c) => c.pool_id)).toEqual(['p1'])
  })

  it('returns nothing for errors that carry no conflict detail', () => {
    expect(conflictingPoolsFromError(new ApiError('boom', 500, { error: 'boom' }))).toEqual([])
    expect(conflictingPoolsFromError(new Error('network'))).toEqual([])
    expect(conflictingPoolsFromError(undefined)).toEqual([])
    // The shape an earlier version of this code wrongly expected.
    expect(conflictingPoolsFromError(Object.assign(new Error('x'), { detail: { conflicting_pools: [{}] } }))).toEqual([])
  })
})
