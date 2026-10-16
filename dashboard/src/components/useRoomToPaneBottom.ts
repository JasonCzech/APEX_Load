import { useLayoutEffect, type RefObject } from 'react'

function scrollPane(element: HTMLElement): HTMLElement | null {
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(parent).overflowY)) return parent
  }
  return null
}

/**
 * Publish, as `--room-to-pane-bottom`, the height from an element's top down
 * to the bottom of the pane it scrolls in, measured in the pane's content so
 * scrolling the pane does not change it. A scrolling table caps its height
 * with it: the table keeps its natural height while its rows fit and stops at
 * the bottom of the page when they do not. The stylesheet keeps its own cap
 * as the minimum, for panes too short to leave a useful room.
 */
export function useRoomToPaneBottom(ref: RefObject<HTMLElement | null>, active: boolean) {
  useLayoutEffect(() => {
    const element = ref.current
    const pane = element && scrollPane(element)
    if (!active || !element || !pane || typeof ResizeObserver === 'undefined') return
    const measure = () => {
      // Positions in the pane's content, so scrolling the pane changes nothing.
      const origin = pane.getBoundingClientRect().top + pane.clientTop - pane.scrollTop
      const box = element.getBoundingClientRect()
      // Whatever follows the element (margins, padding, later content) keeps
      // its place below the element's new bottom edge.
      let contentBottom = box.bottom
      for (const child of Array.from(pane.children)) {
        contentBottom = Math.max(contentBottom, child.getBoundingClientRect().bottom + (parseFloat(getComputedStyle(child).marginBottom) || 0))
      }
      const trailing = contentBottom + (parseFloat(getComputedStyle(pane).paddingBottom) || 0) - box.bottom
      const room = Math.max(0, Math.floor(pane.clientHeight - (box.top - origin) - trailing))
      element.style.setProperty('--room-to-pane-bottom', `${room}px`)
    }
    measure()
    // The pane resizes with the window; its content resizes when anything
    // above the element grows or shrinks, or gains a child. Measure on the
    // next frame: resizing the element inside the observer's callback would
    // be a resize loop.
    let frame = 0
    const remeasure = () => { if (!frame) frame = requestAnimationFrame(() => { frame = 0; measure() }) }
    const resizes = new ResizeObserver(remeasure)
    resizes.observe(pane)
    for (const child of Array.from(pane.children)) resizes.observe(child)
    const additions = new MutationObserver(records => {
      for (const record of records) record.addedNodes.forEach(node => { if (node instanceof Element) resizes.observe(node) })
      remeasure()
    })
    additions.observe(pane, { childList: true })
    return () => {
      resizes.disconnect()
      additions.disconnect()
      cancelAnimationFrame(frame)
      element.style.removeProperty('--room-to-pane-bottom')
    }
  }, [active, ref])
}
