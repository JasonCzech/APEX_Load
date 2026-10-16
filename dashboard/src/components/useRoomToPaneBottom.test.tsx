// @vitest-environment jsdom

import { act, useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useRoomToPaneBottom } from './useRoomToPaneBottom'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const rect = (top: number, bottom: number) => ({ top, bottom, height: bottom - top, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) }) as DOMRect

function Grid({ active }: { active: boolean }) {
  const ref = useRef<HTMLDivElement | null>(null)
  useRoomToPaneBottom(ref, active)
  return <div ref={ref} className="grid" />
}

describe('useRoomToPaneBottom', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  it('publishes the height from the element down to the pane bottom, leaving the space that follows it', async () => {
    let resized: (() => void) | undefined
    const disconnect = vi.fn()
    const observed: Element[] = []
    vi.stubGlobal('ResizeObserver', class { constructor(callback: () => void) { resized = callback } observe(element: Element) { observed.push(element) } disconnect = disconnect })
    // The re-measure waits for the next frame.
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => frames.push(callback))
    vi.stubGlobal('cancelAnimationFrame', () => {})
    const nextFrame = () => { for (const callback of frames.splice(0)) callback(0) }

    // A 600px pane scrolled 50px, whose content ends 40px (margin and padding)
    // below a grid that starts 300px into the content.
    const pane = document.body.appendChild(document.createElement('div'))
    pane.style.overflowY = 'auto'
    pane.style.paddingBottom = '20px'
    Object.defineProperty(pane, 'clientHeight', { configurable: true, value: 600 })
    pane.scrollTop = 50
    pane.getBoundingClientRect = () => rect(100, 700)
    const content = pane.appendChild(document.createElement('div'))
    content.style.marginBottom = '20px'
    const host = content.appendChild(document.createElement('div'))
    let gridBox = rect(350, 790)
    content.getBoundingClientRect = () => rect(50, gridBox.bottom)

    const root = createRoot(host)
    await act(async () => root.render(<Grid active />))
    const grid = host.querySelector<HTMLDivElement>('.grid')!
    grid.getBoundingClientRect = () => gridBox
    await act(async () => { resized?.(); nextFrame() })
    // 600 - (350 - 100 + 50) - 40
    expect(grid.style.getPropertyValue('--room-to-pane-bottom')).toBe('260px')

    // Content above the grid grows by 100px: the room shrinks to match.
    gridBox = rect(450, 890)
    await act(async () => { resized?.(); nextFrame() })
    expect(grid.style.getPropertyValue('--room-to-pane-bottom')).toBe('160px')

    // A section added to the pane later is watched too, and measured at once.
    gridBox = rect(350, 790)
    const added = document.createElement('section')
    await act(async () => { pane.insertBefore(added, content); await Promise.resolve(); nextFrame() })
    expect(observed).toContain(added)
    expect(grid.style.getPropertyValue('--room-to-pane-bottom')).toBe('260px')

    // Raw mode has no grid to size.
    await act(async () => root.render(<Grid active={false} />))
    expect(grid.style.getPropertyValue('--room-to-pane-bottom')).toBe('')
    expect(disconnect).toHaveBeenCalledOnce()
    await act(async () => root.unmount())
  })
})
