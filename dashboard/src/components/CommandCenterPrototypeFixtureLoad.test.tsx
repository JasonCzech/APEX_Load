// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CommandCenterPrototype from './CommandCenterPrototype'

// The demo fixtures arrive through a dynamic import, so the realistic failure is
// a chunk fetch that never resolves to a module: a hashed asset evicted after a
// redeploy, an offline reload, or a webview that blocks the request. Making the
// module throw on import reproduces exactly that rejection. This lives in its own
// file because the mock has to be registered before the component's `import()`
// runs, and the main suite needs the real fixtures.
vi.mock('./commandCenterPrototypeFixtures', () => {
  throw new Error('Failed to fetch dynamically imported module')
})

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CommandCenterPrototype demo fixture loading', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.restoreAllMocks()
    container.remove()
  })

  it('reports a failed demo fixture chunk instead of rendering a blank cockpit', async () => {
    await act(async () => {
      root.render(<CommandCenterPrototype prototypeMode />)
    })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    // Without a rejection handler the promise rejects unhandled, the fixtures
    // stay null, and the cockpit silently falls back to the empty run with no
    // explanation. The operator must be told the demo data is missing.
    expect(container.textContent).toContain('Command center demo fixtures failed to load')
    // ...and the demo run really is absent, so the message is describing the
    // state on screen rather than sitting next to a working cockpit.
    expect(container.textContent).not.toContain('Checkout Peak Soak - Command Center Prototype')
  })
})
