import { useCallback, useEffect, useRef } from 'react'

const ITEM_SELECTOR = 'button, a[href]'
const NEXT_KEYS = new Set(['ArrowDown', 'ArrowRight'])
const PREVIOUS_KEYS = new Set(['ArrowUp', 'ArrowLeft'])

function listItems(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(ITEM_SELECTOR))
    .filter(item => !(item as HTMLButtonElement).disabled && !item.closest('[hidden]'))
}

function setRovingStop(items: HTMLElement[], stop: HTMLElement | undefined) {
  for (const item of items) item.tabIndex = item === stop ? 0 : -1
}

function handleKeyDown(this: HTMLElement, event: KeyboardEvent) {
  if (event.altKey || event.ctrlKey || event.metaKey) return
  const items = listItems(this)
  const index = items.indexOf(event.target as HTMLElement)
  if (index < 0) return
  let next: HTMLElement | undefined
  if (NEXT_KEYS.has(event.key)) next = items[(index + 1) % items.length]
  else if (PREVIOUS_KEYS.has(event.key)) next = items[(index - 1 + items.length) % items.length]
  else if (event.key === 'Home') next = items[0]
  else if (event.key === 'End') next = items[items.length - 1]
  if (!next) return
  event.preventDefault()
  setRovingStop(items, next)
  next.focus()
}

/**
 * Section lists (Settings, Access, Help) are one Tab stop: Tab lands on the
 * current entry, and Arrow keys, Home and End move between entries. Arrow
 * Left/Right also work, for the single-row layout used at narrow widths.
 *
 * Returns a ref for the list container; the key listener follows whichever
 * element it is attached to, however late that element mounts.
 */
export function useSectionListKeys() {
  const rootRef = useRef<HTMLElement | null>(null)
  const ref = useCallback((element: HTMLElement | null) => {
    rootRef.current?.removeEventListener('keydown', handleKeyDown)
    rootRef.current = element
    element?.addEventListener('keydown', handleKeyDown)
  }, [])

  // Re-applied after every render so entries added by search or permissions
  // join the roving set; the current entry (or the first) keeps the Tab stop.
  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const items = listItems(root)
    const focused = items.find(item => item === document.activeElement)
    setRovingStop(items, focused || items.find(item => item.getAttribute('aria-current') === 'page') || items[0])
  })

  return ref
}
