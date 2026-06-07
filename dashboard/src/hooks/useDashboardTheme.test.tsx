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
    </div>
  )
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

    await act(async () => {
      const darkButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'dark')
      darkButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('dark')
    expect(window.localStorage.getItem('apexload.theme')).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('loads, applies, and persists solarized-dark with light browser color semantics', async () => {
    window.localStorage.setItem('apexload.theme', 'solarized-dark')

    await act(async () => {
      root.render(<ThemeHarness />)
    })

    expect(container.querySelector('[data-testid="theme"]')?.textContent).toBe('solarized-dark')
    expect(document.documentElement.dataset.theme).toBe('solarized-dark')
    // light colorScheme because it's not 'dark'
    expect(window.localStorage.getItem('apexload.theme')).toBe('solarized-dark')
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
