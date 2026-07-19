// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WailsDesktopBridge } from './desktopBridge'
import { useDesktopRoutePersistence } from './useDesktopRoutePersistence'
import { DESKTOP_OPEN_ACTIVATION_EVENT } from './useDesktopShellEvents'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

function Harness({ route: initialRoute, onRestore }: { route: string; onRestore?: (route: string) => void }) {
  const [route, setRoute] = useState(initialRoute)
  useDesktopRoutePersistence({
    enabled: true,
    route,
    restoreRoute: savedRoute => {
      onRestore?.(savedRoute)
      setRoute(savedRoute)
    },
  })
  return null
}

function PersistenceHarness({ route }: { route: string }) {
  useDesktopRoutePersistence({ enabled: true, route, restoreRoute: () => undefined })
  return null
}

describe('useDesktopRoutePersistence', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.history.replaceState({}, '', window.location.pathname)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    delete (window as BridgeWindow).go
    vi.restoreAllMocks()
  })

  it('does not overwrite the stored route before restoration resolves', async () => {
    let resolveSavedRoute!: (route: string) => void
    const getLastRoute = vi.fn(() => new Promise<string>(resolve => { resolveSavedRoute = resolve }))
    const setLastRoute = vi.fn().mockResolvedValue(undefined)
    const restoreRoute = vi.fn()
    ;(window as BridgeWindow).go = { main: { App: { GetLastRoute: getLastRoute, SetLastRoute: setLastRoute } } }

    await act(async () => root.render(<Harness route="#/dashboard" onRestore={restoreRoute} />))
    expect(setLastRoute).not.toHaveBeenCalled()

    await act(async () => resolveSavedRoute('#/results'))
    expect(restoreRoute).toHaveBeenCalledWith('#/results')
    expect(setLastRoute).toHaveBeenCalledWith('#/results')
  })

  it('preserves an explicit cold-launch route instead of restoring over it', async () => {
    window.history.replaceState({}, '', '#/test/run-1')
    const getLastRoute = vi.fn().mockResolvedValue('#/results')
    const setLastRoute = vi.fn().mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { GetLastRoute: getLastRoute, SetLastRoute: setLastRoute } } }

    await act(async () => root.render(<Harness route="#/test/run-1" />))
    expect(getLastRoute).not.toHaveBeenCalled()
    expect(setLastRoute).toHaveBeenCalledWith('#/test/run-1')
  })

  it.each(['url', 'menu-action', 'open-recent'])(
    'does not let session restore overwrite a queued %s native activation',
    async activationKind => {
      let resolveSavedRoute!: (route: string) => void
      const getLastRoute = vi.fn(() => new Promise<string>(resolve => { resolveSavedRoute = resolve }))
      const setLastRoute = vi.fn().mockResolvedValue(undefined)
      const restoreRoute = vi.fn()
      ;(window as BridgeWindow).go = { main: { App: { GetLastRoute: getLastRoute, SetLastRoute: setLastRoute } } }
      await act(async () => root.render(<Harness route="#/dashboard" onRestore={restoreRoute} />))

      act(() => window.dispatchEvent(new CustomEvent(DESKTOP_OPEN_ACTIVATION_EVENT, { detail: activationKind })))
      await act(async () => resolveSavedRoute('#/results'))

      expect(restoreRoute).not.toHaveBeenCalled()
    },
  )

  it('serializes rapid route writes before they cross the Wails bridge', async () => {
    window.history.replaceState({}, '', '#/first')
    let releaseFirst!: () => void
    const setLastRoute = vi.fn()
      .mockImplementationOnce(() => new Promise<void>(resolve => { releaseFirst = resolve }))
      .mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { SetLastRoute: setLastRoute } } }

    await act(async () => root.render(<PersistenceHarness route="#/first" />))
    await act(async () => root.render(<PersistenceHarness route="#/second" />))
    expect(setLastRoute.mock.calls).toEqual([['#/first']])

    await act(async () => {
      releaseFirst()
      await Promise.resolve()
    })
    expect(setLastRoute.mock.calls).toEqual([['#/first'], ['#/second']])
  })
})
