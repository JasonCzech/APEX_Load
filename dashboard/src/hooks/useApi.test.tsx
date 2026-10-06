// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./apiClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./apiClient')>()
  return {
    ...actual,
    apiFetch: vi.fn(),
    apiFetchFromOrigin: vi.fn(),
    request: vi.fn().mockResolvedValue({}),
    requestFromOrigin: vi.fn().mockResolvedValue({}),
  }
})

import { ApiProvider } from './ApiProvider'
import { ApiError, useApi, useOptionalApi } from './useApi'
import { ApiError as ClientApiError } from './apiClient'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// One representative export per domain hook spread into the context, so a hook
// dropped from (or shadowed out of) the provider fails here.
const REPRESENTATIVE_SERVICES = [
  'listTests',
  'listApplications',
  'listProjects',
  'listDomains',
  'listScripts',
  'getAuthEffectiveAccess',
  'startRecording',
  'getBrowserRecorderBridgeStatus',
  'listRemotes',
  'listTimeslots',
  'listFleetPools',
  'listExecutionPlans',
  'listCollectors',
  'listObjectRepositories',
  'listCorrelationRulesets',
  'getEntitlements',
  'listDashboardDefinitions',
  'listELKConnections',
  'listDynatraceConnections',
] as const

describe('ApiProvider', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('exposes every domain hook on one flat context value', async () => {
    let services: Record<string, unknown> | undefined
    function Consumer() {
      services = useApi() as unknown as Record<string, unknown>
      return null
    }
    await act(async () => {
      root.render(
        <ApiProvider>
          <Consumer />
        </ApiProvider>,
      )
    })

    expect(services).toBeDefined()
    REPRESENTATIVE_SERVICES.forEach(name => {
      expect(typeof services?.[name]).toBe('function')
    })
  })

  it('keeps the context value stable across re-renders', async () => {
    const seen: unknown[] = []
    function Consumer() {
      seen.push(useApi())
      return null
    }
    const tree = () => (
      <ApiProvider>
        <Consumer />
      </ApiProvider>
    )
    await act(async () => {
      root.render(tree())
    })
    await act(async () => {
      root.render(tree())
    })
    expect(seen.length).toBeGreaterThan(1)
    expect(seen[seen.length - 1]).toBe(seen[0])
  })

  it('returns null for optional consumers outside the provider and preserves ApiError identity', async () => {
    let value: unknown = 'not rendered'
    function Consumer() {
      value = useOptionalApi()
      return null
    }
    await act(async () => root.render(<Consumer />))
    expect(value).toBeNull()
    expect(ApiError).toBe(ClientApiError)
  })

  it('fails loudly when used outside the provider', async () => {
    function Consumer() {
      useApi()
      return null
    }
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    // React re-throws through the DOM, and jsdom prints the whole stack unless
    // the error event is handled. The throw is the assertion here, not noise.
    const swallow = (event: ErrorEvent) => event.preventDefault()
    window.addEventListener('error', swallow)
    try {
      await expect(
        act(async () => {
          root.render(<Consumer />)
        }),
      ).rejects.toThrow('useApi must be used within ApiProvider')
    } finally {
      window.removeEventListener('error', swallow)
      errors.mockRestore()
    }
  })
})
