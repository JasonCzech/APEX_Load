// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ContextMenu, { type ContextMenuItem } from './ContextMenu'
import { placeAtPoint } from './floatingPlacement'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const ITEMS: ContextMenuItem[] = [
  { id: 'wrap', label: 'Wrap in transaction…' },
  { id: 'rename', label: 'Rename…', disabled: true, reason: 'Select a transaction.' },
  { id: 'duplicate', label: 'Duplicate', separatorBefore: true },
  { id: 'delete', label: 'Delete', danger: true, separatorBefore: true },
]

const menuItems = () => Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
const press = (key: string, init: KeyboardEventInit = {}) => act(() => {
  (document.activeElement || document.body).dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }))
})

describe('ContextMenu', () => {
  let host: HTMLDivElement
  let root: Root
  let onSelect: ReturnType<typeof vi.fn<(id: string) => void>>
  let onClose: ReturnType<typeof vi.fn<(returnFocus: boolean) => void>>
  beforeEach(() => {
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    onSelect = vi.fn<(id: string) => void>()
    onClose = vi.fn<(returnFocus: boolean) => void>()
    act(() => root.render(<ContextMenu label="Step actions" point={{ x: 40, y: 50 }} items={ITEMS} onSelect={onSelect} onClose={onClose} />))
  })
  afterEach(() => { act(() => root.unmount()); host.remove() })

  it('is a labelled menu on document.body with the first command focused', () => {
    const menu = document.querySelector('[role="menu"]')
    expect(menu?.getAttribute('aria-label')).toBe('Step actions')
    expect(host.contains(menu)).toBe(false)
    expect(document.activeElement?.textContent).toBe('Wrap in transaction…')
    expect(menuItems().map(item => item.parentElement?.className || '')).toEqual(['', '', 'ux-context-menu-group', 'ux-context-menu-group'])
    expect(menuItems()[3].className).toContain('danger')
  })

  it('moves focus with the arrows, Home, End and a first letter, wrapping around', () => {
    press('ArrowUp')
    expect(document.activeElement?.textContent).toBe('Delete')
    press('ArrowDown')
    expect(document.activeElement?.textContent).toBe('Wrap in transaction…')
    press('End')
    expect(document.activeElement?.textContent).toBe('Delete')
    press('Home')
    press('d')
    expect(document.activeElement?.textContent).toBe('Duplicate')
    press('d')
    expect(document.activeElement?.textContent).toBe('Delete')
  })

  it('keeps a disabled command focusable, says why, and does nothing on it', () => {
    const rename = menuItems()[1]
    expect(rename.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(rename.getAttribute('aria-describedby')!)?.textContent).toBe('Select a transaction.')
    act(() => rename.focus())
    expect(document.querySelector('.ux-context-menu-tip')?.textContent).toBe('Select a transaction.')
    act(() => rename.click())
    expect(onSelect).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('runs a command, closing without taking focus back (the command decides)', () => {
    act(() => menuItems()[2].click())
    expect(onClose).toHaveBeenCalledWith(false)
    expect(onSelect).toHaveBeenCalledWith('duplicate')
  })

  it('closes on Escape (returning focus) and stops it there', () => {
    const outer = vi.fn()
    document.addEventListener('keydown', outer)
    press('Escape')
    document.removeEventListener('keydown', outer)
    expect(onClose).toHaveBeenCalledWith(true)
    expect(outer).not.toHaveBeenCalled()
  })

  it('keeps its keys, an editor shortcut included, from reaching the page', () => {
    const outer = vi.fn()
    document.addEventListener('keydown', outer)
    press('z', { metaKey: true })
    press('ArrowDown')
    document.removeEventListener('keydown', outer)
    expect(outer).not.toHaveBeenCalled()
    expect(document.activeElement?.textContent).toBe('Rename…')
  })

  it('closes on an outside press and on a scroll outside it', () => {
    act(() => { document.body.dispatchEvent(new Event('pointerdown', { bubbles: true })) })
    expect(onClose).toHaveBeenLastCalledWith(false)
    onClose.mockClear()
    act(() => { document.querySelector('[role="menu"]')!.dispatchEvent(new Event('pointerdown', { bubbles: true })) })
    expect(onClose).not.toHaveBeenCalled()
    act(() => { window.dispatchEvent(new Event('scroll')) })
    expect(onClose).toHaveBeenCalledWith(false)
  })
})

describe('placeAtPoint', () => {
  it('opens down-right of the point and flips to stay inside the viewport', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 800 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 600 })
    expect(placeAtPoint({ x: 100, y: 100 }, { width: 200, height: 150 })).toEqual({ left: 100, top: 100 })
    expect(placeAtPoint({ x: 700, y: 550 }, { width: 200, height: 150 })).toEqual({ left: 500, top: 400 })
    expect(placeAtPoint({ x: 10, y: 590 }, { width: 200, height: 700 })).toEqual({ left: 10, top: 8 })
  })
})
