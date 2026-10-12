// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useScrollEdges } from './useScrollEdges'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function Edges({ element }: { element: HTMLElement | null }) {
  const edges = useScrollEdges(element)
  return <output>{`${edges.start ? 'start' : '-'} ${edges.end ? 'end' : '-'}`}</output>
}

// A row whose sizes jsdom cannot lay out, so the test sets them.
function row(scrollWidth: number, clientWidth: number) {
  const element = document.createElement('div')
  Object.defineProperty(element, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(element, 'clientWidth', { configurable: true, value: clientWidth })
  Object.defineProperty(element, 'scrollLeft', { configurable: true, writable: true, value: 0 })
  return element
}

describe('useScrollEdges', () => {
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
  })

  it('says which ends of a scrolling row have more past them', async () => {
    const element = row(300, 100)
    await act(async () => root.render(<Edges element={element} />))
    expect(container.textContent).toBe('- end')

    element.scrollLeft = 100
    await act(async () => { element.dispatchEvent(new Event('scroll')) })
    expect(container.textContent).toBe('start end')

    element.scrollLeft = 200
    await act(async () => { element.dispatchEvent(new Event('scroll')) })
    expect(container.textContent).toBe('start -')
  })

  it('marks neither end of a row that fits, or of no row', async () => {
    await act(async () => root.render(<Edges element={row(100, 100)} />))
    expect(container.textContent).toBe('- -')
    await act(async () => root.render(<Edges element={null} />))
    expect(container.textContent).toBe('- -')
  })
})
