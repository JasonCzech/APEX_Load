// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useAnchoredPopover } from './useAnchoredPopover'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function Probe({ left }: { left: number }) {
  const { position, toggle, triggerRef } = useAnchoredPopover({ align: 'start' })
  return <>
    <button ref={element => {
      triggerRef.current = element
      if (element) element.getBoundingClientRect = () => ({ left, right: left + 20, top: 100, bottom: 120, width: 20, height: 20, x: left, y: 100, toJSON: () => ({}) })
    }} onClick={toggle}>open</button>
    <output>{JSON.stringify(position)}</output>
  </>
}

describe('useAnchoredPopover', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); host.remove() })

  it('keeps a start-aligned panel on screen for triggers in the right half', () => {
    act(() => root.render(<Probe left={100} />))
    act(() => host.querySelector('button')!.click())
    expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ left: 100 })
    act(() => host.querySelector('button')!.click())
    act(() => root.render(<Probe left={window.innerWidth - 60} />))
    act(() => host.querySelector('button')!.click())
    expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ right: 40 })
  })

  it('places the panel against a transformed ancestor, which is its containing block', () => {
    const dialog = document.createElement('div')
    dialog.style.transform = 'translateY(0)'
    dialog.getBoundingClientRect = () => ({ left: 50, top: 40, right: 850, bottom: 640, width: 800, height: 600, x: 50, y: 40, toJSON: () => ({}) })
    Object.defineProperty(dialog, 'clientWidth', { value: 800 })
    Object.defineProperty(dialog, 'clientHeight', { value: 600 })
    host.append(dialog)
    const inner = createRoot(dialog)
    try {
      act(() => inner.render(<Probe left={100} />))
      act(() => dialog.querySelector('button')!.click())
      // Trigger at viewport (100, 120): 50px right of and 84px below the dialog's corner.
      expect(JSON.parse(dialog.querySelector('output')!.textContent!)).toEqual({ left: 50, top: 84 })
    } finally {
      act(() => inner.unmount())
    }
  })

  it('treats a backdrop-filtered ancestor as the containing block too', () => {
    const backdrop = document.createElement('div')
    backdrop.style.setProperty('backdrop-filter', 'blur(8px)')
    backdrop.getBoundingClientRect = () => ({ left: 20, top: 10, right: 820, bottom: 610, width: 800, height: 600, x: 20, y: 10, toJSON: () => ({}) })
    Object.defineProperty(backdrop, 'clientWidth', { value: 800 })
    Object.defineProperty(backdrop, 'clientHeight', { value: 600 })
    host.append(backdrop)
    const inner = createRoot(backdrop)
    try {
      act(() => inner.render(<Probe left={100} />))
      act(() => backdrop.querySelector('button')!.click())
      expect(JSON.parse(backdrop.querySelector('output')!.textContent!)).toEqual({ left: 80, top: 114 })
    } finally {
      act(() => inner.unmount())
    }
  })

  it('lets Escape close only the panel, not an enclosing dialog listening for it', () => {
    let dialogEscapes = 0
    const onDialogKey = (event: KeyboardEvent) => { if (event.key === 'Escape') dialogEscapes += 1 }
    document.addEventListener('keydown', onDialogKey)
    try {
      act(() => root.render(<Probe left={100} />))
      act(() => host.querySelector('button')!.click())
      act(() => { host.querySelector('button')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
      expect(host.querySelector('output')!.textContent).toBe('null')
      expect(document.activeElement).toBe(host.querySelector('button'))
      expect(dialogEscapes).toBe(0)
      // Closed, the panel no longer intercepts Escape.
      act(() => { host.querySelector('button')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
      expect(dialogEscapes).toBe(1)
    } finally {
      document.removeEventListener('keydown', onDialogKey)
    }
  })
})
