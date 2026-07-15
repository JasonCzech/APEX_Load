import type { KeyboardEvent, MouseEvent } from 'react'

export function interactiveElementProps<T extends HTMLElement>(onActivate: (event: MouseEvent<T> | KeyboardEvent<T>) => void, label: string) {
  return {
    role: 'button' as const,
    tabIndex: 0,
    'aria-label': label,
    onClick(event: MouseEvent<T>) {
      if (event.defaultPrevented) return
      onActivate(event)
    },
    onKeyDown(event: KeyboardEvent<T>) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault()
        onActivate(event)
      }
    },
  }
}
