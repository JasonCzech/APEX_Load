import type { MouseEvent } from 'react'

/**
 * Click-outside dismissal for a modal overlay. The backdrop is presentation
 * only: it is not focusable and does not wrap the dialog in a button, so screen
 * readers announce the dialog itself. Keyboard users close with Escape or the
 * dialog's own Close button.
 */
export function dialogBackdropProps(onDismiss: () => void) {
  return {
    role: 'presentation' as const,
    onClick(event: MouseEvent<HTMLDivElement>) {
      if (event.target === event.currentTarget) onDismiss()
    },
  }
}
