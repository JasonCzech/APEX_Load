// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ThemeToggle from './ThemeToggle'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ThemeToggle', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('renders a select element and requests theme changes', async () => {
    const onThemeChange = vi.fn()

    await act(async () => {
      root.render(<ThemeToggle theme="solarized-dark" onThemeChange={onThemeChange} />)
    })

    const select = container.querySelector('select')
    expect(select).toBeDefined()
    if (!select) return

    expect(select.value).toBe('solarized-dark')
    expect(Array.from(select.options).map(o => o.value)).toEqual(['system', 'dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed'])

    await act(async () => {
      select.value = 'light'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onThemeChange).toHaveBeenCalledWith('light')
  })
})
