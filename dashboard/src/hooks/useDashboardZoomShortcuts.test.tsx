// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { DASHBOARD_ZOOM_STORAGE_KEY, useDashboardZoomShortcuts } from './useDashboardZoomShortcuts'

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
    document.documentElement.style.removeProperty('zoom')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    document.documentElement.style.removeProperty('zoom')
  })

  it('applies and persists the default zoom on mount', async () => {
    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1')
    expect(window.localStorage.getItem(DASHBOARD_ZOOM_STORAGE_KEY)).toBe('1')
  })

  it('loads a stored zoom value', async () => {
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, '1.2')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.2')
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1.2')
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
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1.1')
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
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1.1')

    await act(async () => {
      window.dispatchEvent(keyboardEvent('0', { ctrlKey: true }))
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1')
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
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1')
  })

  it('normalizes invalid and out-of-range stored zoom values', async () => {
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, '12')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1.5')
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1.5')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    window.localStorage.setItem(DASHBOARD_ZOOM_STORAGE_KEY, 'not-a-number')

    await act(async () => {
      root.render(<ZoomHarness />)
    })

    expect(container.querySelector('[data-testid="zoom"]')?.textContent).toBe('1')
    expect(document.documentElement.style.getPropertyValue('zoom')).toBe('1')
  })
})
