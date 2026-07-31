// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

vi.mock('../apiClient', () => ({
  apiFetch: vi.fn(),
  request: vi.fn(),
}))

import { DEFAULT_SETTINGS } from '../../components/settingsPageModel'
import { request } from '../apiClient'
import { useSystemApi } from './useSystemApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let api: ReturnType<typeof useSystemApi>
function Capture() {
  api = useSystemApi()
  return null
}

describe('useSystemApi settings concurrency', () => {
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
})
