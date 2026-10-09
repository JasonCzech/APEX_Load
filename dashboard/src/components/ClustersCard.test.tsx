// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ClustersCard from './ClustersCard'
import { ApiError } from '../hooks/apiClient'
import type { ClusterConnection, ClusterTestResult } from '../types/cluster'

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

vi.mock('../hooks/api/useClustersApi', () => ({
  useClustersApi: () => clustersApiMock,
}))

let container: HTMLElement
let root: Root

function connection(overrides: Partial<ClusterConnection> = {}): ClusterConnection {
  return {
    id: 'cluster-1',
    name: 'eastus-perf',
    kind: 'aks',
    api_server_url: 'https://apex-aks.hcp.eastus.azmk8s.io',
    namespace: 'apex',
    tls_mode: 'custom_ca',
    auth_mode: 'azure_service_principal',
    azure_tenant_id: 'tenant-1',
    azure_client_id: 'client-1',
    status: 'active',
    has_client_secret: true,
    credential_source: 'inline_encrypted',
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T01:00:00Z',
    ...overrides,
  }
}

const okTest: ClusterTestResult = {
  status: 'ok',
  summary: 'all checks passed',
  probes: [{ probe: 'api_server', ok: true }],
  checked_at: '2026-08-06T02:00:00Z',
  server_version: 'v1.29.4',
}

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  clustersApiMock.listClusters.mockResolvedValue({ clusters: [connection()], total: 1 })
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
  })
}

async function render(canManage: boolean) {
  await act(async () => {
    root.render(<ClustersCard canManage={canManage} />)
  })
  await flush()
}

// React tracks its own value on the DOM node, so a controlled input only sees a
// change when the native setter is used before dispatching.
function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function buttonByText(label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(
    button => button.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined
}

describe('ClustersCard', () => {
  it('renders the registered clusters', async () => {
    await render(true)
    expect(container.textContent).toContain('eastus-perf')
    expect(container.textContent).toContain('Azure Kubernetes Service')
    expect(container.textContent).toContain('namespace apex')
    expect(container.textContent).toContain('Stored (encrypted)')
  })

  it('hides management actions without permission', async () => {
    await render(false)
    expect(buttonByText('Connect Cluster')).toBeUndefined()
    expect(buttonByText('Edit')).toBeUndefined()
    expect(buttonByText('Delete')).toBeUndefined()
    expect(buttonByText('Test connection')).toBeUndefined()
    // The routes are system-admin only on read too, so a real non-admin gets a
    // 403 rather than this list; the card explains the requirement either way.
    expect(container.textContent).toContain('manage_system')
  })

  it('never renders a stored credential back into the form', async () => {
    await render(true)
    await act(async () => {
      buttonByText('Edit')?.click()
    })
    await flush()

    const secretInput = container.querySelector('input[type="password"]') as HTMLInputElement | null
    expect(secretInput).not.toBeNull()
    // The read transport type carries no secret at all, so the field must be
    // empty and the copy must say the stored one is kept.
    expect(secretInput?.value).toBe('')
    expect(container.textContent).toContain('Leave blank to keep the stored credential')
  })

  it('flags each invalid field it was given and lists the rest before Connect', async () => {
    await render(true)
    await act(async () => { buttonByText('Connect Cluster')?.click() })
    await flush()
    const field = (label: string) => Array.from(container.querySelectorAll('label.pp-field')).find(item => item.querySelector('span')?.textContent === label)!.querySelector('input') as HTMLInputElement
    // Nothing is flagged before the engineer types; the summary names what is missing.
    expect(container.querySelector('[aria-invalid="true"]')).toBeNull()
    expect(container.textContent).toContain('To connect, complete or correct: Name, Namespace, API server URL')
    await act(async () => {
      setInputValue(field('Namespace'), 'Bad Namespace!')
      setInputValue(field('Generator selector'), 'not==valid==x')
    })
    for (const label of ['Namespace', 'Generator selector']) {
      const input = field(label)
      expect(input.getAttribute('aria-invalid')).toBe('true')
      expect(document.getElementById(input.getAttribute('aria-describedby')!)?.textContent).toBeTruthy()
    }
    expect(field('Name').getAttribute('aria-invalid')).toBeNull()
    expect(buttonByText('Connect')?.disabled).toBe(true)
  })

  it('shows a test result and clears it when any field is edited', async () => {
    clustersApiMock.testCluster.mockResolvedValue(okTest)
    await render(true)

    await act(async () => {
      buttonByText('Test connection')?.click()
    })
    await flush()

    expect(clustersApiMock.testCluster).toHaveBeenCalledWith('cluster-1')
    const result = container.querySelector('[data-testid="cluster-test-result"]')
    expect(result?.textContent).toContain('v1.29.4')

    // Editing any field must invalidate the result: a stale "connection OK"
    // next to changed settings is actively misleading.
    await act(async () => {
      buttonByText('Edit')?.click()
    })
    await flush()
    const nameInput = container.querySelector('input') as HTMLInputElement
    await act(async () => {
      setInputValue(nameInput, 'renamed')
    })
    await flush()
    expect(container.querySelector('[data-testid="cluster-test-result"]')).toBeNull()
  })

  it('reports a load failure with a retry affordance', async () => {
    clustersApiMock.listClusters.mockRejectedValueOnce(new ApiError('cluster store unavailable', 503))
    await render(true)

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('cluster store unavailable')

    clustersApiMock.listClusters.mockResolvedValue({ clusters: [connection()], total: 1 })
    await act(async () => {
      buttonByText('Retry cluster data')?.click()
    })
    await flush()
    expect(clustersApiMock.listClusters).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('eastus-perf')
  })

  it('surfaces the 409 when a cluster is still referenced by pools', async () => {
    clustersApiMock.deleteCluster.mockRejectedValue(
      new ApiError('cluster connection is still used by 1 fleet pool(s): gen-pool; repoint them first', 409),
    )
    await render(true)

    await act(async () => {
      buttonByText('Delete')?.click()
    })
    await flush()

    // The destructive modal gates confirmation on a reason plus the typed
    // target name.
    const [reasonInput, confirmInput] = Array.from(container.querySelectorAll('input')) as HTMLInputElement[]
    await act(async () => {
      setInputValue(reasonInput, 'decommissioning the cluster')
      setInputValue(confirmInput, 'eastus-perf')
    })
    await flush()
    await act(async () => {
      buttonByText('Delete cluster')?.click()
    })
    await flush()

    expect(container.textContent).toContain('gen-pool')
  })
})
