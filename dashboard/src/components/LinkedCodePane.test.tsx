// @vitest-environment jsdom

import { act, useEffect } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { EditorView } from '@codemirror/view'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LinkedCodePane from './LinkedCodePane'
import { routeLinkedCodePaneWheel } from './linkedCodePaneWheel'
import { type LineRange } from './dslJsonPointers'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// CodeMirror does not mount under jsdom (it needs layout-measurement APIs and
// only attaches its EditorView asynchronously), so stand in a lightweight fake
// editor that calls onCreateEditor once with a view exposing just the surface
// LinkedCodePane touches: a 4-line doc and a recording dispatch.
const { codeMirrorProps } = vi.hoisted(() => ({
  codeMirrorProps: { extensions: [] as unknown[] },
}))
const dispatchSpy = vi.fn()
const measuredScroll = { scrollTop: 0, clientHeight: 100 }
const measureSpy = vi.fn((spec?: { read: () => { top: number; height: number }; write: (block: { top: number; height: number }) => void }) => spec?.write(spec.read()))
vi.mock('@uiw/react-codemirror', () => ({
  default: function MockCodeMirror({
    extensions,
    height,
    maxHeight,
    onCreateEditor,
  }: {
    extensions?: unknown[]
    height?: string
    maxHeight?: string
    onCreateEditor?: (view: unknown) => void
  }) {
    codeMirrorProps.extensions = extensions || []
    useEffect(() => {
      const fakeView = {
        state: {
          doc: {
            lines: 4,
            line: (n: number) => ({ from: n, number: n }),
          },
        },
        dispatch: dispatchSpy,
        scrollDOM: measuredScroll,
        lineBlockAt: () => ({ top: 200, height: 20 }),
        requestMeasure: measureSpy,
      }
      onCreateEditor?.(fakeView)
    }, [onCreateEditor])
    return <div data-testid="mock-codemirror" data-height={height || ''} data-max-height={maxHeight || ''} />
  },
}))

describe('LinkedCodePane', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    dispatchSpy.mockClear()
    measureSpy.mockClear()
    measuredScroll.scrollTop = 0
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  const render = async (activeRange: LineRange | null) => {
    await act(async () => {
      root.render(<LinkedCodePane value="a\nb\nc\nd" language="json" activeRange={activeRange} />)
    })
  }

  it('does not scroll on the initial highlight, nor on an equal-but-new range, but does on a real change', async () => {
    const scrollSpy = vi.spyOn(EditorView, 'scrollIntoView')

    // Initial render: the first mapping is auto-selected when the review opens.
    // Highlighting it must not drag the page down to this pane.
    await render({ startLine: 2, endLine: 2 })
    expect(scrollSpy).not.toHaveBeenCalled()
    expect(dispatchSpy).toHaveBeenCalled()

    // A 2s status poll upstream rebuilds the range object with identical values.
    // The pane must not yank the viewport back to the highlighted line.
    await render({ startLine: 2, endLine: 2 })
    expect(scrollSpy).not.toHaveBeenCalled()

    // A genuine, user-driven selection change scrolls the new line into view.
    await render({ startLine: 3, endLine: 3 })
    expect(scrollSpy).toHaveBeenCalledTimes(1)
  })

  it('centers initial and repeated reveals inside its own scroller without page scrolling', async () => {
    const outerScroll = vi.spyOn(EditorView, 'scrollIntoView')
    await act(async () => root.render(<LinkedCodePane value="a\nb\nc\nd" activeRange={{ startLine: 2, endLine: 2 }} revealOnMount revealRequestId={1} />))
    expect(measuredScroll.scrollTop).toBe(160)
    const initialMeasures = measureSpy.mock.calls.length
    await act(async () => root.render(<LinkedCodePane value="a\nb\nc\nd" activeRange={{ startLine: 2, endLine: 2 }} revealOnMount revealRequestId={2} />))
    expect(measureSpy.mock.calls.length).toBeGreaterThan(initialMeasures)
    expect(outerScroll).not.toHaveBeenCalled()
  })

  it('updates the linked highlight without scrolling when there is no active range', async () => {
    const scrollSpy = vi.spyOn(EditorView, 'scrollIntoView')
    await render(null)
    expect(scrollSpy).not.toHaveBeenCalled()
    // It still dispatches the (empty) linked-range effect to clear any highlight.
    expect(dispatchSpy).toHaveBeenCalled()
  })

  it('passes the bounded height to CodeMirror so source mapping panes own their scrollbars', async () => {
    await act(async () => {
      root.render(<LinkedCodePane value="a\nb" language="json" activeRange={null} maxHeight="100%" />)
    })

    const editor = container.querySelector('[data-testid="mock-codemirror"]') as HTMLDivElement | null
    expect(editor?.dataset.height).toBe('100%')
    expect(editor?.dataset.maxHeight).toBe('100%')
  })

  it('allows source mapping panes to disable line wrapping', async () => {
    await act(async () => {
      root.render(<LinkedCodePane value="a very long source line" language="json" wrapLines />)
    })
    expect(codeMirrorProps.extensions).toContain(EditorView.lineWrapping)

    await act(async () => {
      root.render(<LinkedCodePane value="a very long source line" language="json" wrapLines={false} />)
    })
    expect(codeMirrorProps.extensions).not.toContain(EditorView.lineWrapping)
  })

  it('lets the linked code pane scroll while it has room in the wheel direction', () => {
    const outerFrame = document.createElement('div')
    const panel = document.createElement('div')
    const codeScroller = document.createElement('div')
    outerFrame.style.overflowY = 'auto'
    codeScroller.style.overflowY = 'auto'
    Object.defineProperty(outerFrame, 'clientHeight', { configurable: true, value: 240 })
    Object.defineProperty(outerFrame, 'scrollHeight', { configurable: true, value: 960 })
    Object.defineProperty(codeScroller, 'clientHeight', { configurable: true, value: 120 })
    Object.defineProperty(codeScroller, 'scrollHeight', { configurable: true, value: 720 })
    codeScroller.scrollTop = 180

    outerFrame.appendChild(panel)
    panel.appendChild(codeScroller)
    container.appendChild(outerFrame)

    const wheel = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 180 })
    const handled = routeLinkedCodePaneWheel(wheel, codeScroller)

    expect(handled).toBe(false)
    expect(outerFrame.scrollTop).toBe(0)
    expect(codeScroller.scrollTop).toBe(180)
    expect(wheel.defaultPrevented).toBe(false)
  })

  it('routes wheel movement to the larger scroll frame when the code pane is at its boundary', () => {
    const outerFrame = document.createElement('div')
    const codeScroller = document.createElement('div')
    outerFrame.style.overflowY = 'auto'
    codeScroller.style.overflowY = 'auto'
    Object.defineProperty(outerFrame, 'clientHeight', { configurable: true, value: 240 })
    Object.defineProperty(outerFrame, 'scrollHeight', { configurable: true, value: 960 })
    Object.defineProperty(codeScroller, 'clientHeight', { configurable: true, value: 120 })
    Object.defineProperty(codeScroller, 'scrollHeight', { configurable: true, value: 720 })
    codeScroller.scrollTop = 600

    outerFrame.appendChild(codeScroller)
    container.appendChild(outerFrame)

    const wheel = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 180 })
    const handled = routeLinkedCodePaneWheel(wheel, codeScroller)

    expect(handled).toBe(true)
    expect(outerFrame.scrollTop).toBe(180)
    expect(codeScroller.scrollTop).toBe(600)
    expect(wheel.defaultPrevented).toBe(true)
  })

  it('routes upward wheel movement to the larger scroll frame only after the code pane reaches the top', () => {
    const outerFrame = document.createElement('div')
    const codeScroller = document.createElement('div')
    outerFrame.style.overflowY = 'auto'
    codeScroller.style.overflowY = 'auto'
    Object.defineProperty(outerFrame, 'clientHeight', { configurable: true, value: 240 })
    Object.defineProperty(outerFrame, 'scrollHeight', { configurable: true, value: 960 })
    Object.defineProperty(codeScroller, 'clientHeight', { configurable: true, value: 120 })
    Object.defineProperty(codeScroller, 'scrollHeight', { configurable: true, value: 720 })
    outerFrame.scrollTop = 240
    codeScroller.scrollTop = 0

    outerFrame.appendChild(codeScroller)
    container.appendChild(outerFrame)

    const wheel = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -120 })
    const handled = routeLinkedCodePaneWheel(wheel, codeScroller)

    expect(handled).toBe(true)
    expect(outerFrame.scrollTop).toBe(120)
    expect(codeScroller.scrollTop).toBe(0)
    expect(wheel.defaultPrevented).toBe(true)
  })
})


it('remeasures an editor after pane resize and disconnects the observer on unmount', async () => {
  let resized: (() => void) | undefined
  const disconnect = vi.fn()
  vi.stubGlobal('ResizeObserver', class { constructor(callback: () => void) { resized = callback } observe() {} disconnect = disconnect })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<LinkedCodePane value="sample" ariaLabel="Destination" />))
    measureSpy.mockClear()
    await act(async () => resized?.())
    expect(measureSpy).toHaveBeenCalledOnce()
    expect(measureSpy).toHaveBeenCalledWith()
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals() }
  expect(disconnect).toHaveBeenCalledOnce()
})
