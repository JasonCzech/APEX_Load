// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const apiMock = { getScript: vi.fn() }
vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

import NewTestReadiness from './NewTestReadiness'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const scripts: Record<string, object> = {
  shop: { script: { name: 'Shop', version: '1', protocol: 'browser', config: { browser: { capture_document_metrics: false } }, actions: [] } },
  search: { script: { name: 'Search', version: '1', protocol: 'browser', config: { browser: { capture_document_metrics: true } }, actions: [] } },
  api: { script: { name: 'API', version: '1', protocol: 'http', config: {}, actions: [] } },
}

describe('NewTestReadiness browser evidence', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    apiMock.getScript.mockImplementation(async (id: string) => scripts[id])
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  const render = async (groups: { name: string; script_id: string }[], summary = false) => {
    await act(async () => {
      root.render(<NewTestReadiness projectId="p" summary={summary} scenario={{ id: 's', updated_at: '', data_pools: [],
        scripts: groups.map((group, order) => ({ ...group, order, vusers: 1 })) }} />)
    })
  }

  it('names the browser groups that will capture no page timings', async () => {
    await render([{ name: 'Shop', script_id: 'shop' }, { name: 'Search', script_id: 'search' }, { name: 'API', script_id: 'api' }], true)
    expect(container.querySelector('summary small')?.textContent).toContain('1 browser group without page timings')
    expect(container.textContent).toContain('Shop captures no page timings or Web Vitals')
    expect(container.textContent).toContain("Turn them on in the script's Runtime Settings › Browser.")
  })

  it('says where each group\'s browser settings come from', async () => {
    await act(async () => {
      root.render(<NewTestReadiness projectId="p" scenario={{ id: 's', updated_at: '', data_pools: [], runtime_settings: { browser_config: { headless: true } },
        scripts: [
          { order: 0, name: 'Shop', script_id: 'shop', vusers: 1 },
          { order: 1, name: 'Search', script_id: 'search', vusers: 1, runtime_settings: { browser_config: { headless: true } } },
        ] }} />)
    })
    expect(container.textContent).toContain("Turn them on in the scenario's runtime defaults, whose browser settings replace the scripts' (Shop); in the group's runtime settings, whose browser settings replace the script's (Search).")
  })

  it('says every browser group captures them, and says nothing for a run without browser groups', async () => {
    await render([{ name: 'Search', script_id: 'search' }])
    expect(container.textContent).toContain('Page timings and Web Vitals are captured for every browser group.')
    await render([{ name: 'API', script_id: 'api' }])
    expect(container.textContent).not.toContain('Browser evidence')
  })
})
