// @vitest-environment jsdom

import { act, useLayoutEffect, useRef, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { EditorSelection, EditorState, type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LinkedCodePane from './LinkedCodePane'
import { routeLinkedCodePaneWheel } from './linkedCodePaneWheel'
import { type LineRange } from './dslJsonPointers'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// CodeMirror does not mount under jsdom (it needs layout-measurement APIs), so
// stand in a lightweight fake editor exposing just the surface LinkedCodePane
// touches: a doc of the value's lines and a recording dispatch. Like react-codemirror, it
// creates its view a render after mounting (the container ref sets state and a
// layout effect builds the view), so the pane's own effects first run without
// a view.
const { codeMirrorProps } = vi.hoisted(() => ({
  codeMirrorProps: { extensions: [] as unknown[] },
}))
const dispatchSpy = vi.fn()
const measuredScroll = { scrollTop: 0, clientHeight: 100 }
const measureSpy = vi.fn()
const createdViews: unknown[] = []
vi.mock('@uiw/react-codemirror', () => ({
  default: function MockCodeMirror({
    extensions,
    height,
    maxHeight,
    onCreateEditor,
    value,
  }: {
    extensions?: unknown[]
    height?: string
    maxHeight?: string
    onCreateEditor?: (view: unknown) => void
    value?: string
  }) {
    codeMirrorProps.extensions = extensions || []
    const [container, setContainer] = useState<HTMLDivElement | null>(null)
    const created = useRef(false)
    useLayoutEffect(() => {
      if (!container || created.current) return
      created.current = true
      const fakeView = {
        state: {
          doc: {
            lines: (value || '').split('\n').length,
            line: (n: number) => ({ from: n, number: n }),
          },
        },
        dispatch: dispatchSpy,
        scrollDOM: measuredScroll,
        lineBlockAt: () => ({ top: 200, height: 20 }),
        requestMeasure: measureSpy,
      }
      createdViews.push(fakeView)
      onCreateEditor?.(fakeView)
    }, [container, onCreateEditor, value])
    return <div ref={setContainer} data-testid="mock-codemirror" data-height={height || ''} data-max-height={maxHeight || ''} />
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
    createdViews.length = 0
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
      root.render(<LinkedCodePane value={'a\nb\nc\nd'} language="json" activeRange={activeRange} />)
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

  it('highlights and reveals the range once the editor exists, then only on a new range or reveal request', async () => {
    const scrollSpy = vi.spyOn(EditorView, 'scrollIntoView')
    const pane = (revealRequestId: number) => <LinkedCodePane value={'a\nb\nc\nd'} activeRange={{ startLine: 2, endLine: 2 }} revealOnMount revealRequestId={revealRequestId} />

    // The view arrives a render after mounting; the highlight and reveal wait for it.
    await act(async () => root.render(pane(1)))
    expect(createdViews).toHaveLength(1)
    expect(dispatchSpy).toHaveBeenCalled()
    expect(scrollSpy).toHaveBeenCalledTimes(1)
    expect(scrollSpy).toHaveBeenLastCalledWith(2, { y: 'center' })

    // An unrelated re-render leaves the pane where the reader put it.
    await act(async () => root.render(pane(1)))
    expect(scrollSpy).toHaveBeenCalledTimes(1)

    // Selecting the same mapping again reveals it again.
    await act(async () => root.render(pane(2)))
    expect(scrollSpy).toHaveBeenCalledTimes(2)
  })

  it('applies the range to the new editor when a mapped context remounts it', async () => {
    const scrollSpy = vi.spyOn(EditorView, 'scrollIntoView')
    const value = Array.from({ length: 40 }, (_, index) => `line ${index + 1}`).join('\n')
    await act(async () => root.render(<LinkedCodePane value={value} activeRange={{ startLine: 10, endLine: 10 }} contextLines={4} revealOnMount revealRequestId={1} />))
    expect(createdViews).toHaveLength(1)
    // Context starts four lines above the range, so the range is line 5 of the excerpt.
    expect(scrollSpy).toHaveBeenLastCalledWith(5, { y: 'center' })

    // A new selection moves the excerpt, which mounts a new editor.
    dispatchSpy.mockClear()
    await act(async () => root.render(<LinkedCodePane value={value} activeRange={{ startLine: 30, endLine: 31 }} contextLines={4} revealOnMount revealRequestId={2} />))
    expect(createdViews).toHaveLength(2)
    expect(dispatchSpy).toHaveBeenCalled()
    expect(scrollSpy).toHaveBeenCalledTimes(2)
  })

  it('scrolls only its own scroller to the target that CodeMirror has measured', () => {
    // Render once so the extensions the pane gives CodeMirror are captured.
    act(() => root.render(<LinkedCodePane value="a" />))
    const [handler] = EditorState.create({ extensions: codeMirrorProps.extensions as Extension[] }).facet(EditorView.scrollHandler)
    const scroller = { scrollTop: 0, scrollLeft: 0, clientHeight: 100, getBoundingClientRect: () => ({ top: 50, left: 0, right: 300 }) }
    // Gutters end 40px in; the target sits in view horizontally unless moved.
    let caret: { left: number; right: number } | null = { left: 60, right: 61 }
    const dom = { querySelector: () => ({ getBoundingClientRect: () => ({ right: 40 }) }) }
    // The document starts 4px into the scroller, and moves up as it scrolls.
    const view = { scrollDOM: scroller, dom, coordsAtPos: () => caret, get documentTop() { return 54 - scroller.scrollTop }, lineBlockAt: () => ({ top: 400, bottom: 420, height: 20 }) } as unknown as EditorView
    const range = EditorSelection.cursor(7)

    // Centered: the block's top (400 + the 4px content offset) less half the remaining height.
    expect(handler(view, range, { x: 'nearest', y: 'center', xMargin: 5, yMargin: 5 })).toBe(true)
    expect(scroller.scrollTop).toBe(364)

    // Already visible: "nearest" leaves the pane where it is.
    expect(handler(view, range, { x: 'nearest', y: 'nearest', xMargin: 5, yMargin: 5 })).toBe(true)
    expect(scroller.scrollTop).toBe(364)

    // A block taller than the pane aligns its top instead of centering.
    const tall = { scrollDOM: scroller, dom, coordsAtPos: () => caret, get documentTop() { return 54 - scroller.scrollTop }, lineBlockAt: () => ({ top: 400, bottom: 700, height: 300 }) } as unknown as EditorView
    handler(tall, range, { x: 'nearest', y: 'center', xMargin: 5, yMargin: 5 })
    expect(scroller.scrollTop).toBe(399)
    expect(scroller.scrollLeft).toBe(0)

    // An unwrapped line scrolled out sideways: back past the gutters, or on to its end.
    scroller.scrollLeft = 200
    caret = { left: -150, right: -149 }
    handler(view, range, { x: 'nearest', y: 'nearest', xMargin: 5, yMargin: 5 })
    expect(scroller.scrollLeft).toBe(5)
    caret = { left: 400, right: 410 }
    handler(view, range, { x: 'nearest', y: 'nearest', xMargin: 5, yMargin: 5 })
    expect(scroller.scrollLeft).toBe(120)
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
