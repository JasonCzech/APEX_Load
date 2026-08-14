// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ComputeInfrastructureCard from './ComputeInfrastructureCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const computeApi = {
  listCloudConnections: vi.fn(),
  listExecutionTargets: vi.fn(),
  createCloudConnection: vi.fn(),
  updateCloudConnection: vi.fn(),
  createExecutionTarget: vi.fn(),
  updateExecutionTarget: vi.fn(),
  testExecutionTarget: vi.fn(),
}

vi.mock('../hooks/api/useComputeApi', () => ({ useComputeApi: () => computeApi }))

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  computeApi.listCloudConnections.mockResolvedValue({
    cloud_connections: [{ id: 'azure-1', name: 'Azure', provider: 'azure', auth_mode: 'azure_identity', credential_source: 'workload_identity', has_client_secret: false, has_aws_external_id: false, status: 'active', created_at: '', updated_at: '' }],
    total: 1,
  })
  computeApi.listExecutionTargets.mockResolvedValue({ execution_targets: [], total: 0 })
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

describe('ComputeInfrastructureCard', () => {
  it('explains unavailable Azure direct connectivity and exposes AWS public-IP placement', async () => {
    await act(async () => root.render(<ComputeInfrastructureCard canManage />))
    await flush()
    const open = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Execution Target')
    await act(async () => open?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    const connectivity = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(select => select.parentElement?.textContent?.includes('Connectivity'))
    const disabledDirect = Array.from(connectivity?.options || []).find(option => option.value === 'direct')
    expect(disabledDirect?.disabled).toBe(true)
    expect(disabledDirect?.textContent).toContain('no stable address')

    const runtime = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(select => select.parentElement?.textContent?.includes('Runtime'))
    await act(async () => {
      if (runtime) {
        runtime.value = 'aws_ecs_fargate'
        runtime.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(container.textContent).toContain('Public IP assignment')
    const publicIP = Array.from(container.querySelectorAll<HTMLSelectElement>('select')).find(select => select.parentElement?.textContent?.includes('Public IP assignment'))
    expect(publicIP?.value).toBe('disabled')
  })
})
