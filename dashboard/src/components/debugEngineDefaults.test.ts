import { afterEach, describe, expect, it, vi } from 'vitest'

const { requestMock } = vi.hoisted(() => ({ requestMock: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request: requestMock }))

import { loadEngineConfigDefaults, resetEngineConfigDefaultsCache } from './debugEngineDefaults'

describe('engine config defaults', () => {
  afterEach(() => {
    requestMock.mockReset()
    resetEngineConfigDefaultsCache()
  })

  it('loads GET /dsl/config-defaults once and caches it', async () => {
    requestMock.mockResolvedValue({ config: { base_url: '', http: { request_timeout_ms: 60000 } } })
    await expect(loadEngineConfigDefaults()).resolves.toEqual({ base_url: '', http: { request_timeout_ms: 60000 } })
    await loadEngineConfigDefaults()
    expect(requestMock).toHaveBeenCalledTimes(1)
    expect(requestMock).toHaveBeenCalledWith('/dsl/config-defaults')
  })

  it('retries on the next load after a failure', async () => {
    requestMock.mockRejectedValueOnce(new Error('offline'))
    await expect(loadEngineConfigDefaults()).rejects.toThrow('offline')
    requestMock.mockResolvedValueOnce({ config: { base_url: '' } })
    await expect(loadEngineConfigDefaults()).resolves.toEqual({ base_url: '' })
    expect(requestMock).toHaveBeenCalledTimes(2)
  })

  it('rejects a response without a config', async () => {
    requestMock.mockResolvedValueOnce({})
    await expect(loadEngineConfigDefaults()).rejects.toThrow()
  })
})
