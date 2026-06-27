// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useDashboardTheme } from './useDashboardTheme'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function ThemeHarness() {
  const { theme, setTheme } = useDashboardTheme()

  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button type="button" onClick={() => setTheme('light')}>light</button>
      <button type="button" onClick={() => setTheme('solarized-dark')}>solarized-dark</button>
      <button type="button" onClick={() => setTheme('dark')}>dark</button>
      <button type="button" onClick={() => setTheme('system')}>system</button>
    </div>
  )
}

type FakeMediaQueryList = {
  matches: boolean
  media: string
  addEventListener: (type: string, cb: () => void) => void
  removeEventListener: (type: string, cb: () => void) => void
  emit: (next: boolean) => void
}

function installMatchMedia(initialDark: boolean): FakeMediaQueryList {
  const listeners = new Set<() => void>()
  const mql: FakeMediaQueryList = {
    matches: initialDark,
    media: '(prefers-color-scheme: dark)',
    addEventListener: (_type, cb) => { listeners.add(cb) },
    removeEventListener: (_type, cb) => { listeners.delete(cb) },
    emit: next => { mql.matches = next; listeners.forEach(cb => cb()) },
  }
  ;(window as unknown as { matchMedia: (q: string) => FakeMediaQueryList }).matchMedia = () => mql
  return mql
}

describe('useDashboardTheme', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
    document.documentElement.style.colorScheme = ''
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    delete (window as unknown as { matchMedia?: unknown }).matchMedia
  })

  it('defaults to dark mode and applies it to the document root', async () => {
    await act(async () => {
      root.render(<ThemeHarness />)
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('dark')
  })

  it('loads and persists light mode changes', async () => {
    window.localStorage.setItem('apexload.theme', 'light')

    await act(async () => {
      root.render(<ThemeHarness />)
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.style.colorScheme).toBe('light')

    await act(async () => {
      const darkButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'dark')
      darkButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('loads, applies, and persists solarized-dark with dark browser control semantics', async () => {
    window.localStorage.setItem('apexload.theme', 'solarized-dark')

    await act(async () => {
      root.render(<ThemeHarness />)
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('solarized-dark')
    expect(document.documentElement.dataset.theme).toBe('solarized-dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('solarized-dark')
  })

  it('resolves the system preference from prefers-color-scheme and persists "system"', async () => {
    const media = installMatchMedia(true)
    window.localStorage.setItem('apexload.theme', 'system')

    await act(async () => {
      root.render(<ThemeHarness />)
    })

    // Preference is "system"; applied theme resolves to dark.
    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('system')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('system')

    // OS flips to light → applied theme updates live, preference stays "system".
    await act(async () => {
      media.emit(false)
    })
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.style.colorScheme).toBe('light')
    expect(window.localStorage.getItem('apexload.theme')).toBe('system')
  })

  it('switching to system resolves to light when the OS prefers light', async () => {
    installMatchMedia(false)
    await act(async () => {
      root.render(<ThemeHarness />)
    })
    await act(async () => {
      const systemButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'system')
      systemButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('system')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('rejects unknown stored theme values back to dark', async () => {
    window.localStorage.setItem('apexload.theme', 'midnight')

    await act(async () => {
      root.render(<ThemeHarness />)
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('dark')
  })
})
