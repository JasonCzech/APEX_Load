import { useEffect, useState } from 'react'

export interface ScrollEdges {
  /** Content is scrolled past on the left. */
  start: boolean
  /** More content waits past the right edge. */
  end: boolean
}

const NO_EDGES: ScrollEdges = { start: false, end: false }

/**
 * Which ends of a sideways-scrolling row have content past them, so the row
 * can show that more is there. A row that fits has neither.
 */
export function useScrollEdges(element: HTMLElement | null): ScrollEdges {
  const [edges, setEdges] = useState<ScrollEdges>(NO_EDGES)
  useEffect(() => {
    if (!element) {
      setEdges(NO_EDGES)
      return
    }
    const update = () => {
      const start = element.scrollLeft > 1
      const end = element.scrollLeft + element.clientWidth < element.scrollWidth - 1
      setEdges(previous => previous.start === start && previous.end === end ? previous : { start, end })
    }
    update()
    element.addEventListener('scroll', update, { passive: true })
    // The row's own width and its content's both decide whether it overflows.
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update)
    observer?.observe(element)
    if (element.firstElementChild) observer?.observe(element.firstElementChild)
    return () => {
      element.removeEventListener('scroll', update)
      observer?.disconnect()
    }
  }, [element])
  return edges
}
