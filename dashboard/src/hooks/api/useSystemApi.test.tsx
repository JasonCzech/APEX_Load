// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

vi.mock('../apiClient', async () => {
  const actual = await vi.importActual<typeof import('../apiClient')>('../apiClient')
  return {
    ...actual,
    apiFetch: vi.fn(),
    request: vi.fn(),
  }
})

import { DEFAULT_SETTINGS } from '../../components/settingsPageModel'
import type { SystemReadinessResponse } from '../../types/system'
import { ApiError, request } from '../apiClient'
import { SYSTEM_READINESS_REQUEST_TIMEOUT_MS, useSystemApi } from './useSystemApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let api: ReturnType<typeof useSystemApi>
function Capture() {
  api = useSystemApi()
  return null
}

describe('useSystemApi', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    ;(request as Mock).mockResolvedValue(DEFAULT_SETTINGS)
    await act(async () => {
      root.render(<Capture />)
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('sends the loaded settings revision as If-Match without persisting the response projection', async () => {
    const revision = `ss-${'a'.repeat(64)}`
    await api.updateSystemSettings({ ...DEFAULT_SETTINGS, revision })

    expect(request).toHaveBeenCalledTimes(1)
    const [path, options] = (request as Mock).mock.calls[0]
    expect(path).toBe('/system/settings')
    expect(options.method).toBe('PUT')
    expect(options.headers).toEqual({ 'If-Match': JSON.stringify(revision) })
    expect(JSON.parse(options.body)).toEqual(DEFAULT_SETTINGS)
  })

  it('accepts a validated not_ready document carried by ApiError 503', async () => {
    const readiness: SystemReadinessResponse = {
      status: 'not_ready',
      generated_at: '2026-08-11T18:30:00Z',
      checks: [
        {
          name: 'metadata_store',
          status: 'not_ready',
          message: 'metadata store probe failed',
          details: { store_type: 'postgres', reason: 'probe_failed' },
        },
      ],
    }
    ;(request as Mock).mockRejectedValueOnce(new ApiError('Service Unavailable', 503, readiness))

    await expect(api.getSystemReadiness()).resolves.toEqual(readiness)
    expect(request).toHaveBeenCalledWith('/system/readiness', {
      timeoutMs: SYSTEM_READINESS_REQUEST_TIMEOUT_MS,
    })
  })

  it('rejects a malformed readiness body carried by ApiError 503', async () => {
    const error = new ApiError('Service Unavailable', 503, {
      status: 'not_ready',
      generated_at: '2026-08-11T18:30:00Z',
      checks: [{ name: 'metadata_store', status: 'not_ready' }],
    })
    ;(request as Mock).mockRejectedValueOnce(error)

    await expect(api.getSystemReadiness()).rejects.toBe(error)
  })

  it('bounds readiness requests below the monitoring poll interval', async () => {
    const readiness: SystemReadinessResponse = {
      status: 'ready',
      generated_at: '2026-08-11T18:30:00Z',
      checks: [],
    }
    ;(request as Mock).mockResolvedValueOnce(readiness)

    await expect(api.getSystemReadiness()).resolves.toEqual(readiness)
    expect(SYSTEM_READINESS_REQUEST_TIMEOUT_MS).toBeLessThan(5_000)
    expect(request).toHaveBeenCalledWith('/system/readiness', {
      timeoutMs: SYSTEM_READINESS_REQUEST_TIMEOUT_MS,
    })
  })
})
