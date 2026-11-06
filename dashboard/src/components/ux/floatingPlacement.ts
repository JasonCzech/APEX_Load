// Placing a floating panel (a context menu, a small popover dialog) that
// lives on document.body, outside the zoomed app layout.

const MARGIN = 8

/** The dashboard's zoom (App.css zooms .app-layout); a panel on document.body applies it itself. */
export function dashboardZoom(): number {
  if (typeof document === 'undefined') return 1
  return parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--dashboard-zoom')) || 1
}

/**
 * Where a panel of `size` opens at `point` (viewport pixels): down and to the
 * right of it, flipped left or up where that would leave the viewport, and
 * never closer than 8px to an edge.
 */
export function placeAtPoint(point: { x: number; y: number }, size: { width: number; height: number }): { left: number; top: number } {
  const width = size.width || 200
  const height = size.height || 200
  const viewportWidth = window.innerWidth || 1024
  const viewportHeight = window.innerHeight || 768
  let left = point.x
  let top = point.y
  if (left + width > viewportWidth - MARGIN) left = Math.min(point.x - width, viewportWidth - width - MARGIN)
  if (top + height > viewportHeight - MARGIN) top = Math.min(point.y - height, viewportHeight - height - MARGIN)
  return { left: Math.max(MARGIN, left), top: Math.max(MARGIN, top) }
}
