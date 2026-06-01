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

  it('marks the active theme and requests theme changes', async () => {
    const onThemeChange = vi.fn()

    await act(async () => {
      root.render(<ThemeToggle theme="sepia" onThemeChange={onThemeChange} />)
    })

    const light = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Light'))
    const sepia = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Sepia'))
    const dark = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Dark'))
    expect(light).toBeDefined()
    expect(sepia).toBeDefined()
    expect(dark).toBeDefined()
    expect(light?.getAttribute('aria-pressed')).toBe('false')
    expect(sepia?.getAttribute('aria-pressed')).toBe('true')
    expect(dark?.getAttribute('aria-pressed')).toBe('false')

    await act(async () => {
      sepia?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onThemeChange).toHaveBeenCalledWith('sepia')
  })
})
