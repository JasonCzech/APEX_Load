// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useScriptRuntimeSettingsController } from './useScriptRuntimeSettingsController'

type Controller = ReturnType<typeof useScriptRuntimeSettingsController>

describe('useScriptRuntimeSettingsController', () => {
  let root: Root | null = null
  let container: HTMLDivElement | null = null

  beforeEach(() => {
    ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  })

  afterEach(() => {
    if (root) act(() => root?.unmount())
    container?.remove()
    root = null
    container = null
  })

  function renderController(initialProtocol = 'http') {
    const onOpenRuntimeTab = vi.fn()
    let latest: Controller | null = null
    let setProtocol: ((value: string) => void) | null = null
    let openSections: Record<string, boolean> = {}

    function Harness() {
      const [protocol, updateProtocol] = useState(initialProtocol)
      setProtocol = updateProtocol
      latest = useScriptRuntimeSettingsController({
        onOpenRuntimeTab,
        protocol,
        setOpenSections: updater => {
          openSections = updater(openSections)
        },
      })
      return null
    }

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    act(() => root?.render(<Harness />))

    return {
      controller: () => {
        if (!latest) throw new Error('Controller did not render')
        return latest
      },
      onOpenRuntimeTab,
      openSections: () => openSections,
      setProtocol: (value: string) => act(() => setProtocol?.(value)),
    }
  }

  it('selects exactly one section and coordinates imported-runtime navigation', () => {
    const harness = renderController()
    expect(harness.controller().activeSection).toBe('script_config')

    act(() => harness.controller().selectSection('http'))
    expect(harness.controller().activeSection).toBe('http')
    expect(harness.openSections()).toMatchObject({ runtime: true, http: true, think: false, log: false })

    act(() => harness.controller().openImportedRuntimeSettings('Logging'))
    expect(harness.onOpenRuntimeTab).toHaveBeenCalledTimes(1)
    expect(harness.controller().activeSection).toBe('log')
    expect(harness.openSections()).toMatchObject({ runtime: true, http: false, log: true })
  })

  it('falls back when a protocol change makes the active section unavailable', () => {
    const harness = renderController('http')
    act(() => harness.controller().selectSection('web_runtime'))
    expect(harness.controller().activeSection).toBe('web_runtime')

    harness.setProtocol('grpc')
    expect(harness.controller().activeSection).toBe('think')
    expect(harness.controller().sectionIsAvailable('web_runtime')).toBe(false)
    expect(harness.controller().sectionIsAvailable('grpc')).toBe(true)
    expect(harness.openSections()).toMatchObject({ runtime: true, think: true, web_runtime: false })
  })

  it('retains the HTTP subtab while coordinating section state', () => {
    const harness = renderController('browser')
    act(() => {
      harness.controller().selectSection('http')
      harness.controller().selectHTTPTransportTab('browser')
    })
    expect(harness.controller().activeSection).toBe('http')
    expect(harness.controller().activeHTTPTransportTab).toBe('browser')
  })
})
