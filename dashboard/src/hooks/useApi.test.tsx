// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'

import { ApiProvider, useApi } from './useApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ApiProvider', () => {
  const mounted: Array<{ root: ReturnType<typeof createRoot>; container: HTMLDivElement }> = []

  afterEach(async () => {
    while (mounted.length > 0) {
      const entry = mounted.pop()
      if (!entry) continue
      await act(async () => entry.root.unmount())
      entry.container.remove()
    }
  })

  it('shares the composed API surface through context', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    mounted.push({ root, container })

    function Harness() {
      const first = useApi()
      const second = useApi()
      return <span>{first === second && typeof first.getTest === 'function' ? 'ready' : 'missing'}</span>
    }

    await act(async () => {
      root.render(<ApiProvider><Harness /></ApiProvider>)
    })

    expect(container.textContent).toBe('ready')
  })
})
