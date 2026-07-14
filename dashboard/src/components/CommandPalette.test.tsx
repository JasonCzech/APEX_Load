// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CommandPalette } from './CommandPalette'
import type { RegisteredKeyboardShortcut } from '../hooks/KeyboardShortcutProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const shortcuts: RegisteredKeyboardShortcut[] = [
  { id: 'nav.dashboard', sequence: 1, label: 'Open dashboard', keys: 'g d', group: 'Navigation', onTrigger: vi.fn() },
  { id: 'run.start', sequence: 2, label: 'Start run', keys: 'mod+enter', group: 'Test Run', onTrigger: vi.fn() },
]

describe('CommandPalette accessibility', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('exposes combobox, listbox, and active-option relationships', () => {
    act(() => root.render(<CommandPalette shortcuts={shortcuts} onClose={vi.fn()} />))

    const input = container.querySelector<HTMLInputElement>('[role="combobox"]')
    const options = Array.from(container.querySelectorAll<HTMLElement>('[role="option"]'))
    expect(input?.getAttribute('aria-controls')).toBe('command-palette-list')
    expect(input?.getAttribute('aria-expanded')).toBe('true')
    expect(options).toHaveLength(2)
    expect(input?.getAttribute('aria-activedescendant')).toBe(options[0].id)
    expect(options[0].getAttribute('aria-selected')).toBe('true')

    act(() => input?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))

    expect(input?.getAttribute('aria-activedescendant')).toBe(options[1].id)
    expect(options[1].getAttribute('aria-selected')).toBe('true')
  })

  it('removes aria-activedescendant when no command matches', () => {
    act(() => root.render(<CommandPalette shortcuts={shortcuts} onClose={vi.fn()} />))
    const input = container.querySelector<HTMLInputElement>('[role="combobox"]')
    if (!input) throw new Error('command search input not found')

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, 'missing command')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(input.hasAttribute('aria-activedescendant')).toBe(false)
    expect(container.querySelector('[role="status"]')?.textContent).toContain('No matching commands')
  })
})
