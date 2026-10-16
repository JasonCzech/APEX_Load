import type { MouseEvent } from 'react'

/** Whether a click on a clickable table row landed inside one of the row's own disclosures. */
export function insideRowDisclosure(event: MouseEvent<HTMLElement>): boolean {
  const disclosure = event.target instanceof Element ? event.target.closest('details') : null
  return !!disclosure && event.currentTarget.contains(disclosure)
}
