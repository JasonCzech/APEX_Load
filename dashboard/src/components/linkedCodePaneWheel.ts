const SCROLLABLE_OVERFLOW_VALUES = new Set(['auto', 'scroll', 'overlay'])

function wheelDeltaYPixels(event: WheelEvent, referenceElement: HTMLElement) {
  if (event.deltaMode === WheelEvent.DOM_DELTA_LINE) return event.deltaY * 16
  if (event.deltaMode === WheelEvent.DOM_DELTA_PAGE) return event.deltaY * referenceElement.clientHeight
  return event.deltaY
}

function canScrollVertically(element: HTMLElement) {
  const overflowY = window.getComputedStyle(element).overflowY
  return SCROLLABLE_OVERFLOW_VALUES.has(overflowY) && element.scrollHeight - element.clientHeight > 1
}

function canScrollByDelta(element: HTMLElement, deltaY: number) {
  const maxScrollTop = Math.max(0, element.scrollHeight - element.clientHeight)
  if (deltaY < 0) return element.scrollTop > 0
  if (deltaY > 0) return element.scrollTop < maxScrollTop
  return false
}

export function findLinkedCodePaneScrollAncestor(element: HTMLElement): HTMLElement | null {
  let candidate = element.parentElement
  while (candidate) {
    if (canScrollVertically(candidate)) return candidate
    candidate = candidate.parentElement
  }
  const scrollingElement = document.scrollingElement
  return scrollingElement instanceof HTMLElement && canScrollVertically(scrollingElement) ? scrollingElement : null
}

export function routeLinkedCodePaneWheel(event: WheelEvent, sourceScrollElement: HTMLElement) {
  if (event.defaultPrevented || event.deltaY === 0) return false
  const sourceDeltaY = wheelDeltaYPixels(event, sourceScrollElement)
  if (canScrollByDelta(sourceScrollElement, sourceDeltaY)) return false
  const ancestor = findLinkedCodePaneScrollAncestor(sourceScrollElement)
  if (!ancestor) return false
  const deltaY = wheelDeltaYPixels(event, ancestor)
  const maxScrollTop = Math.max(0, ancestor.scrollHeight - ancestor.clientHeight)
  const nextScrollTop = Math.max(0, Math.min(maxScrollTop, ancestor.scrollTop + deltaY))
  if (nextScrollTop === ancestor.scrollTop) return false
  ancestor.scrollTop = nextScrollTop
  event.preventDefault()
  return true
}
