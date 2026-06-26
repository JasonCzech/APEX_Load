// @vitest-environment jsdom

import { describe, expect, it, vi } from 'vitest'

import { buildNavigationShortcuts, focusCurrentPageSearch } from './appKeyboardShortcuts'
import type { NavPage } from './components/Sidebar'

describe('app keyboard shortcuts', () => {
  it('builds navigation shortcuts with access-aware visibility', () => {
    const onNavigate = vi.fn<(page: NavPage) => void>()
    const shortcuts = buildNavigationShortcuts({
      canUseAccessWorkspace: false,
      isDevUserPreview: true,
      onNavigate,
    })

    expect(shortcuts.find(shortcut => shortcut.id === 'navigation.access')?.hidden).toBe(true)
    expect(shortcuts.find(shortcut => shortcut.id === 'navigation.settings')?.hidden).toBe(true)

    shortcuts.find(shortcut => shortcut.id === 'navigation.scripts')?.onTrigger()
    expect(onNavigate).toHaveBeenCalledWith('scripts')
    shortcuts.find(shortcut => shortcut.id === 'navigation.fleet')?.onTrigger()
    expect(onNavigate).toHaveBeenCalledWith('fleet')
  })

  it('focuses and selects the first visible search or filter field', () => {
    document.body.innerHTML = `
      <input aria-label="Search scripts" value="orders" />
      <input aria-label="Filter status" value="running" />
    `
    const input = document.querySelector<HTMLInputElement>('input[aria-label="Search scripts"]')!

    expect(focusCurrentPageSearch(document)).toBe(true)
    expect(document.activeElement).toBe(input)
    expect(input.selectionStart).toBe(0)
    expect(input.selectionEnd).toBe(input.value.length)
  })

  it('returns false when no page search or filter is available', () => {
    document.body.innerHTML = '<button type="button">Run</button>'
    expect(focusCurrentPageSearch(document)).toBe(false)
  })
})
