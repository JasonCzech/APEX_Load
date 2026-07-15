import type { KeyboardEvent, MouseEvent } from 'react'

export function dialogBackdropProps(onDismiss: () => void, label = 'Close dialog') {
  return {
    role: 'button' as const,
    tabIndex: 0,
    'aria-label': label,
    onClick(event: MouseEvent<HTMLDivElement>) {
      if (event.target === event.currentTarget) onDismiss()
    },
    onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
      if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault()
        onDismiss()
      }
    },
  }
}
