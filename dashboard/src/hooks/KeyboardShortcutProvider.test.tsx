// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  KeyboardShortcutProvider,
  formatShortcutDisplay,
  matchingShortcutsForEvent,
  parseShortcutSequence,
  useKeyboardShortcuts,
  type KeyboardShortcut,
  type RegisteredKeyboardShortcut,
} from './KeyboardShortcutProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function keyboardEvent(key: string, options: Partial<KeyboardEventInit> = {}) {
  return new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
    ...options,
  })
}

function registeredShortcut(overrides: Partial<RegisteredKeyboardShortcut>): RegisteredKeyboardShortcut {
  return {
    id: overrides.id || 'shortcut',
    label: overrides.label || 'Shortcut',
    keys: overrides.keys || 'k',
    group: overrides.group || 'Global',
    priority: overrides.priority,
    sequence: overrides.sequence || 1,
    disabled: overrides.disabled,
    hidden: overrides.hidden,
    allowInEditable: overrides.allowInEditable,
    preventDefault: overrides.preventDefault,
    onTrigger: overrides.onTrigger || vi.fn(),
  }
}

function Publisher({ shortcuts }: { shortcuts: KeyboardShortcut[] }) {
  useKeyboardShortcuts(shortcuts)
  return null
}

describe('KeyboardShortcutProvider helpers', () => {
  it('formats platform-aware shortcut labels', () => {
    expect(formatShortcutDisplay('Cmd/Ctrl+S', true)).toBe('Cmd+S')
    expect(formatShortcutDisplay('Cmd/Ctrl+S', false)).toBe('Ctrl+S')
    expect(formatShortcutDisplay('g d', false)).toBe('G D')
  })

  it('parses two-step shortcut sequences', () => {
    const parsed = parseShortcutSequence('g d')
    expect(parsed.steps).toHaveLength(2)
    expect(parsed.steps.map(step => step.key)).toEqual(['g', 'd'])
  })

  it('filters disabled and hidden shortcuts and orders matches by priority', () => {
    const event = keyboardEvent('k')
    const high = registeredShortcut({ id: 'high', priority: 20, sequence: 1 })
    const low = registeredShortcut({ id: 'low', priority: 1, sequence: 10 })
    const disabled = registeredShortcut({ id: 'disabled', disabled: true, priority: 99 })
    const hidden = registeredShortcut({ id: 'hidden', hidden: true, priority: 99 })

    const matches = matchingShortcutsForEvent([low, disabled, high, hidden], event)

    expect(matches.exact.map(shortcut => shortcut.id)).toEqual(['high', 'low'])
    expect(matches.prefixes).toEqual([])
  })

  it('identifies sequence prefixes separately from exact matches', () => {
    const event = keyboardEvent('g')
    const shortcut = registeredShortcut({ id: 'dashboard', keys: 'g d' })

    const matches = matchingShortcutsForEvent([shortcut], event)

    expect(matches.exact).toEqual([])
    expect(matches.prefixes.map(match => match.id)).toEqual(['dashboard'])
  })
})

describe('KeyboardShortcutProvider', () => {
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
    vi.clearAllMocks()
  })

  it('triggers the highest-priority matching shortcut', async () => {
    const low = vi.fn()
    const high = vi.fn()

    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <Publisher shortcuts={[
            { id: 'low', label: 'Low', keys: 'k', priority: 1, onTrigger: low },
            { id: 'high', label: 'High', keys: 'k', priority: 10, onTrigger: high },
          ]} />
        </KeyboardShortcutProvider>,
      )
    })

    await act(async () => {
      window.dispatchEvent(keyboardEvent('k'))
    })

    expect(high).toHaveBeenCalledTimes(1)
    expect(low).not.toHaveBeenCalled()
  })

  it('triggers two-key sequences and ignores editable targets by default', async () => {
    const onTrigger = vi.fn()

    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <input aria-label="Editor" />
          <Publisher shortcuts={[{ id: 'dashboard', label: 'Dashboard', keys: 'g d', onTrigger }]} />
        </KeyboardShortcutProvider>,
      )
    })

    await act(async () => {
      window.dispatchEvent(keyboardEvent('g'))
      window.dispatchEvent(keyboardEvent('d'))
    })
    expect(onTrigger).toHaveBeenCalledTimes(1)

    const input = container.querySelector('input')!
    await act(async () => {
      input.dispatchEvent(keyboardEvent('g'))
      input.dispatchEvent(keyboardEvent('d'))
    })
    expect(onTrigger).toHaveBeenCalledTimes(1)
  })

  it('allows explicitly editable-safe shortcuts', async () => {
    const onTrigger = vi.fn()

    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <input aria-label="Script name" />
          <Publisher shortcuts={[{
            id: 'save',
            label: 'Save',
            keys: 'Cmd/Ctrl+S',
            allowInEditable: true,
            onTrigger,
          }]} />
        </KeyboardShortcutProvider>,
      )
    })

    const input = container.querySelector('input')!
    await act(async () => {
      input.dispatchEvent(keyboardEvent('s', { metaKey: true }))
    })

    expect(onTrigger).toHaveBeenCalledTimes(1)
  })

  it('opens and closes shortcut help from the built-in shortcut', async () => {
    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <span>Ready</span>
        </KeyboardShortcutProvider>,
      )
    })

    await act(async () => {
      window.dispatchEvent(keyboardEvent('?', { shiftKey: true }))
    })

    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('Keyboard Shortcuts')

    await act(async () => {
      document.dispatchEvent(keyboardEvent('Escape'))
    })

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('opens the command palette with Cmd+K and runs a filtered command', async () => {
    const onTrigger = vi.fn()

    await act(async () => {
      root.render(
        <KeyboardShortcutProvider>
          <input aria-label="Editor" />
          <Publisher shortcuts={[
            { id: 'run', label: 'Run shakeout', group: 'Test Run', keys: 'F5', onTrigger },
            { id: 'stop', label: 'Stop run', group: 'Test Run', keys: 'F8', onTrigger: vi.fn() },
          ]} />
        </KeyboardShortcutProvider>,
      )
    })

    // Opens even while focused in an editable field (allowInEditable).
    const editor = container.querySelector('input')!
    await act(async () => {
      editor.dispatchEvent(keyboardEvent('k', { metaKey: true }))
    })

    const palette = document.body.querySelector('[role="dialog"][aria-label="Command palette"]')
    expect(palette).not.toBeNull()
    expect(palette?.textContent).toContain('Run shakeout')
    expect(palette?.textContent).toContain('Stop run')

    const search = palette!.querySelector('input') as HTMLInputElement
    const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      nativeSetter.call(search, 'shakeout')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(palette?.textContent).toContain('Run shakeout')
    expect(palette?.textContent).not.toContain('Stop run')

    await act(async () => {
      search.dispatchEvent(keyboardEvent('Enter'))
    })
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    expect(onTrigger).toHaveBeenCalledTimes(1)
    expect(document.body.querySelector('[role="dialog"][aria-label="Command palette"]')).toBeNull()
  })
})
