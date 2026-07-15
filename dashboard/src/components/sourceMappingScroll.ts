export function scrollMappingRowIntoTableView(container: HTMLElement, row: HTMLElement) {
  const rowTop = row.offsetTop
  const rowBottom = rowTop + row.offsetHeight
  const visibleTop = container.scrollTop
  const visibleBottom = visibleTop + container.clientHeight
  if (rowTop < visibleTop) container.scrollTop = Math.max(0, rowTop)
  else if (rowBottom > visibleBottom) container.scrollTop = Math.max(0, rowBottom - container.clientHeight)
}
