// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RowActions from './RowActions'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('RowActions', () => {
  let container: HTMLDivElement
  let root: Root
  const onDuplicate = vi.fn()
  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    await act(async () => root.render(<RowActions label="Checkout"><button type="button" onClick={event => { event.stopPropagation(); onDuplicate() }}>Duplicate</button></RowActions>))
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    onDuplicate.mockReset()
  })
  const toggle = () => container.querySelector<HTMLButtonElement>('.ux-row-actions-toggle')!
  const menu = () => container.querySelector<HTMLElement>('.ux-row-actions-menu')!

  it('overlays the row and closes on Escape with focus back on the toggle', async () => {
    expect(toggle().getAttribute('aria-label')).toBe('More actions for Checkout')
    expect(menu().hidden).toBe(true)
    await act(async () => toggle().click())
    expect(toggle().getAttribute('aria-expanded')).toBe('true')
    expect(menu().hidden).toBe(false)
    expect(menu().style.top || menu().style.bottom).not.toBe('')
    await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
    expect(menu().hidden).toBe(true)
    expect(document.activeElement).toBe(toggle())
  })

  it('closes on an outside click and after choosing an action', async () => {
    await act(async () => toggle().click())
    await act(async () => document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })))
    expect(menu().hidden).toBe(true)

    await act(async () => toggle().click())
    await act(async () => menu().querySelector('button')!.click())
    expect(onDuplicate).toHaveBeenCalledTimes(1)
    expect(menu().hidden).toBe(true)
    expect(document.activeElement).toBe(toggle())
  })
})
