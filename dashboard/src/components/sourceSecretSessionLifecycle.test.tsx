// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Lifecycle = ReturnType<typeof useSourceSecretSessionLifecycle>

describe('useSourceSecretSessionLifecycle', () => {
  let container: HTMLDivElement
  let root: Root
  let mounted: boolean
  let lifecycle: Lifecycle
  const discard = vi.fn().mockResolvedValue(undefined)

  function Harness() {
    lifecycle = useSourceSecretSessionLifecycle(discard)
    return null
  }

  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mounted = true
    discard.mockClear()
    await act(async () => {
      root.render(<Harness />)
    })
  })

  afterEach(async () => {
    if (mounted) {
      await act(async () => {
        root.unmount()
        await Promise.resolve()
      })
    }
    container.remove()
  })

  it('discards a superseded accepted session and the final session on unmount', async () => {
    await act(async () => {
      lifecycle.replace('conversion', 'project-1', 'session-a')
      lifecycle.replace('conversion', 'project-1', 'session-b')
      await Promise.resolve()
    })

    expect(discard).toHaveBeenCalledTimes(1)
    expect(discard).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'session-a',
    })

    await act(async () => {
      root.unmount()
      mounted = false
      await Promise.resolve()
    })
    expect(discard).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'session-b',
    })
  })

  it('does not let a stale response discard a session adopted by the active lane', async () => {
    await act(async () => {
      lifecycle.replace('conversion', 'project-1', 'shared-session')
      lifecycle.discardUnaccepted('project-1', 'shared-session')
      await Promise.resolve()
    })

    expect(discard).not.toHaveBeenCalled()
  })

  it('cancels queued cleanup when an immediate replacement adopts the same session', async () => {
    await act(async () => {
      lifecycle.replace('conversion', 'project-1', 'reused-session')
      lifecycle.clear('conversion')
      lifecycle.replace('conversion', 'project-1', 'reused-session')
      await Promise.resolve()
    })

    expect(discard).not.toHaveBeenCalled()
  })
})
