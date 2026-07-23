// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { BrowserEmulationCatalog } from '../types/scripts'

const getBrowserEmulationCatalog = vi.fn()

vi.mock('./api/useSystemApi', () => ({
  useSystemApi: () => ({ getBrowserEmulationCatalog }),
}))

const { useBrowserEmulationCatalog } = await import('./useBrowserEmulationCatalog')
const { FALLBACK_BROWSER_EMULATION_CATALOG } = await import('../components/scriptEditorRuntimeSettingsModel')

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const SERVER_CATALOG: BrowserEmulationCatalog = {
  browsers: [{ type: 'arcadia', label: 'Arcadia Browser', versions: ['9'], platforms: ['windows'] }],
  platforms: [{ id: 'windows', label: 'Windows' }],
  languages: [{ tag: 'en-US', label: 'English (United States)' }],
}

describe('useBrowserEmulationCatalog', () => {
  let container: HTMLDivElement
  let root: Root
  let seen: BrowserEmulationCatalog | undefined

  function Probe({ enabled = true }: { enabled?: boolean }) {
    seen = useBrowserEmulationCatalog(enabled)
    return null
  }

  beforeEach(() => {
    getBrowserEmulationCatalog.mockReset()
    seen = undefined
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('serves the controller catalog once it resolves', async () => {
    getBrowserEmulationCatalog.mockResolvedValue(SERVER_CATALOG)
    await act(async () => {
      root.render(<Probe />)
    })
    expect(getBrowserEmulationCatalog).toHaveBeenCalledTimes(1)
    expect(seen?.browsers.map(b => b.type)).toEqual(['arcadia'])
  })

  it('keeps the bundled fallback when the request fails', async () => {
    getBrowserEmulationCatalog.mockRejectedValue(new Error('403'))
    await act(async () => {
      root.render(<Probe />)
    })
    expect(seen).toEqual(FALLBACK_BROWSER_EMULATION_CATALOG)
  })

  it('keeps the bundled fallback when the controller returns an empty catalog', async () => {
    getBrowserEmulationCatalog.mockResolvedValue({ browsers: [], platforms: [], languages: [] })
    await act(async () => {
      root.render(<Probe />)
    })
    expect(seen).toEqual(FALLBACK_BROWSER_EMULATION_CATALOG)
  })

  it('does not call the endpoint when the surface cannot show the section', async () => {
    await act(async () => {
      root.render(<Probe enabled={false} />)
    })
    expect(getBrowserEmulationCatalog).not.toHaveBeenCalled()
    expect(seen).toEqual(FALLBACK_BROWSER_EMULATION_CATALOG)
  })
})
