// @vitest-environment jsdom

import { act, useEffect } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { EditorView } from '@codemirror/view'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LinkedCodePane from './LinkedCodePane'
import { type LineRange } from './dslJsonPointers'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// CodeMirror does not mount under jsdom (it needs layout-measurement APIs and
// only attaches its EditorView asynchronously), so stand in a lightweight fake
// editor that calls onCreateEditor once with a view exposing just the surface
// LinkedCodePane touches: a 4-line doc and a recording dispatch.
const dispatchSpy = vi.fn()
vi.mock('@uiw/react-codemirror', () => ({
  default: ({ onCreateEditor }: { onCreateEditor?: (view: unknown) => void }) => {
    useEffect(() => {
      const fakeView = {
        state: {
          doc: {
            lines: 4,
            line: (n: number) => ({ from: n, number: n }),
          },
        },
        dispatch: dispatchSpy,
      }
      onCreateEditor?.(fakeView)
    }, [onCreateEditor])
    return null
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

  it('updates the linked highlight without scrolling when there is no active range', async () => {
    const scrollSpy = vi.spyOn(EditorView, 'scrollIntoView')
    await render(null)
    expect(scrollSpy).not.toHaveBeenCalled()
    // It still dispatches the (empty) linked-range effect to clear any highlight.
    expect(dispatchSpy).toHaveBeenCalled()
  })
})
