// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DASHBOARD_ZOOM_STORAGE_KEY, requestDashboardZoom, useDashboardZoomShortcuts } from './useDashboardZoomShortcuts'
import { DESKTOP_QUEUED_MENU_ZOOM_EVENT } from '../desktop/rendererEvents'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function ZoomHarness() {
  const zoom = useDashboardZoomShortcuts()

  return <span data-testid="zoom">{zoom}</span>
}

function keyboardEvent(key: string, options: Partial<KeyboardEventInit> = {}) {
  return new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
    ...options,
  })
}

describe('useDashboardZoomShortcuts', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.localStorage.clear()
    document.documentElement.style.removeProperty('--dashboard-zoom')
    document.documentElement.style.removeProperty('--dashboard-zoom-inverse')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    document.documentElement.style.removeProperty('--dashboard-zoom')
    document.documentElement.style.removeProperty('--dashboard-zoom-inverse')
    vi.restoreAllMocks()
  })

  it('applies and persists the default zoom on mount', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom-inverse')).toBe('1')
    expect(window.localStorage.getItem(DASHBOARD_ZOOM_STORAGE_KEY)).toBe('1')
  })

  it('zooms once per command-palette request', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })
    await act(async () => requestDashboardZoom('in'))
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.1')
    await act(async () => requestDashboardZoom('reset'))
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
  })

  it('loads a stored zoom value', async () => {
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, '1.2')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.2')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1.2')
    expect(Number(document.documentElement.style.getPropertyValue('--dashboard-zoom-inverse'))).toBeCloseTo(1 / 1.2)
  })

  it('uses and applies the default zoom when browser storage is blocked', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('Storage disabled', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Storage disabled', 'SecurityError')
    })

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1')
  })

  it('zooms in with Cmd plus or equals', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })

    const event = keyboardEvent('=', { metaKey: true })
    await act(async () => {
      window.dispatchEvent(event)
    })

    expect(event.defaultPrevented).toBe(true)
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1.1')
    expect(Number(document.documentElement.style.getPropertyValue('--dashboard-zoom-inverse'))).toBeCloseTo(1 / 1.1)
    expect(window.localStorage.getItem(DASHBOARD_ZOOM_STORAGE_KEY)).toBe('1.1')

    await act(async () => {
      window.dispatchEvent(keyboardEvent('+', { metaKey: true }))
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.2')
  })

  it('zooms out with Ctrl minus and resets with Ctrl zero', async () => {
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, '1.2')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    await act(async () => {
      window.dispatchEvent(keyboardEvent('-', { ctrlKey: true }))
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1.1')

    await act(async () => {
      window.dispatchEvent(keyboardEvent('0', { ctrlKey: true }))
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1')
    expect(window.localStorage.getItem(DASHBOARD_ZOOM_STORAGE_KEY)).toBe('1')
  })

  it('ignores plain plus and shortcuts with Alt', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })

    const plainEvent = keyboardEvent('=')
    const altEvent = keyboardEvent('-', { ctrlKey: true, altKey: true })

    await act(async () => {
      window.dispatchEvent(plainEvent)
      window.dispatchEvent(altEvent)
    })

    expect(plainEvent.defaultPrevented).toBe(false)
    expect(altEvent.defaultPrevented).toBe(false)
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1')
  })

  it('responds once to desktop menu zoom actions from the shell dispatcher', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })

    await act(async () => {
      window.dispatchEvent(
        new CustomEvent(DESKTOP_QUEUED_MENU_ZOOM_EVENT, { detail: 'in' }),
      )
    })
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.1')

    await act(async () => {
      window.dispatchEvent(
        new CustomEvent(DESKTOP_QUEUED_MENU_ZOOM_EVENT, { detail: 'reset' }),
      )
    })
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')

    await act(async () => {
      window.dispatchEvent(
        new CustomEvent(DESKTOP_QUEUED_MENU_ZOOM_EVENT, { detail: 'sideways' }),
      )
    })
    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
  })

  it('normalizes invalid and out-of-range stored zoom values', async () => {
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, '12')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.8')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1.8')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, 'not-a-number')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('--dashboard-zoom')).toBe('1')
  })
})
