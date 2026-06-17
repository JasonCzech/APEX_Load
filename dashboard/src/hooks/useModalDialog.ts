import { RefObject, useEffect, useRef } from 'react'

interface ModalDialogOptions {
  dialogRef: RefObject<HTMLElement>
  enabled?: boolean
  initialFocusRef?: RefObject<HTMLElement>
  onClose?: () => void
}

interface DialogStackEntry {
  id: number
  onClose?: () => void
}

const dialogStack: DialogStackEntry[] = []
let nextDialogId = 1

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'iframe',
  'object',
  'embed',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

export function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter(element => !element.hasAttribute('disabled') && isVisible(element))
}

function isVisible(element: HTMLElement) {
  const style = window.getComputedStyle(element)
  return style.display !== 'none' && style.visibility !== 'hidden'
}

function isTopDialog(id: number) {
  return dialogStack[dialogStack.length - 1]?.id === id
}

function removeDialog(id: number) {
  const index = dialogStack.findIndex(entry => entry.id === id)
  if (index >= 0) {
    dialogStack.splice(index, 1)
  }
}

function trapTabKey(event: KeyboardEvent, dialog: HTMLElement) {
  const focusable = focusableElements(dialog)
  if (focusable.length === 0) {
    event.preventDefault()
    dialog.focus()
    return
  }

  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const activeElement = document.activeElement

  if (event.shiftKey && activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

export function useModalDialog({
  dialogRef,
  enabled = true,
  initialFocusRef,
  onClose,
}: ModalDialogOptions) {
  const idRef = useRef<number>(0)

  useEffect(() => {
    if (!enabled) return undefined
    const dialog = dialogRef.current
    if (!dialog) return undefined

    if (!dialog.hasAttribute('tabindex')) {
      dialog.setAttribute('tabindex', '-1')
    }

    const id = nextDialogId++
    idRef.current = id
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialogStack.push({ id, onClose })

    window.setTimeout(() => {
      if (!isTopDialog(id)) return
      const firstFocusTarget = initialFocusRef?.current || focusableElements(dialog)[0] || dialog
      firstFocusTarget.focus()
    }, 0)

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isTopDialog(id)) return
      if (event.key === 'Escape' && onClose) {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key === 'Tab') {
        trapTabKey(event, dialog)
      }
    }

    document.addEventListener('keydown', handleKeyDown, true)
    return () => {
      document.removeEventListener('keydown', handleKeyDown, true)
      removeDialog(id)
      if (previouslyFocused?.isConnected) {
        previouslyFocused.focus()
      }
    }
  }, [dialogRef, enabled, initialFocusRef, onClose])
}
