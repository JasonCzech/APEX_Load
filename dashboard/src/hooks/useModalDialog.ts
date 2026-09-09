import { RefObject, useEffect, useRef } from 'react'

interface ModalDialogOptions {
  dialogRef: RefObject<HTMLElement | null>
  enabled?: boolean
  initialFocusRef?: RefObject<HTMLElement | null>
  onClose?: () => void
  returnFocusSelector?: string
}

interface DialogStackEntry {
  id: number
  dialog: HTMLElement
}

const dialogStack: DialogStackEntry[] = []
let nextDialogId = 1
const inertBefore = new Map<HTMLElement, boolean>()
let bodyOverflow = ''

function updateModalIsolation() {
  for (const [element, wasInert] of inertBefore) {
    if (!wasInert) element.removeAttribute('inert')
  }
  inertBefore.clear()
  const top = dialogStack[dialogStack.length - 1]?.dialog
  if (!top) return
  // Isolate each sibling branch, including earlier body-level dialog portals.
  for (let branch: HTMLElement | null = top; branch?.parentElement; branch = branch.parentElement) {
    for (const sibling of Array.from(branch.parentElement.children)) {
      if (sibling === branch || !(sibling instanceof HTMLElement)) continue
      inertBefore.set(sibling, sibling.hasAttribute('inert'))
      sibling.setAttribute('inert', '')
    }
    if (branch.parentElement === document.body) break
  }
}

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
  if (element.closest('[hidden], [inert]')) return false
  for (let node: HTMLElement | null = element; node; node = node.parentElement) {
    const style = window.getComputedStyle(node)
    if (style.display === 'none' || style.visibility === 'hidden') return false
  }
  return true
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

  if (activeElement === dialog || !dialog.contains(activeElement)) {
    event.preventDefault()
    const recoveryTarget = event.shiftKey ? last : first
    recoveryTarget.focus()
  } else if (event.shiftKey && activeElement === first) {
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
  returnFocusSelector,
}: ModalDialogOptions) {
  const idRef = useRef<number>(0)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

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
    if (!dialogStack.length) {
      bodyOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
    }
    dialogStack.push({ id, dialog })
    updateModalIsolation()

    const focusTimer = window.setTimeout(() => {
      if (!isTopDialog(id)) return
      const firstFocusTarget = initialFocusRef?.current || focusableElements(dialog)[0] || dialog
      firstFocusTarget.focus()
    }, 0)

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isTopDialog(id)) return
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopImmediatePropagation()
        onCloseRef.current?.()
        return
      }
      if (event.key === 'Tab') {
        trapTabKey(event, dialog)
      }
    }

    const handleFocus = (event: FocusEvent) => {
      if (isTopDialog(id) && event.target instanceof Node && !dialog.contains(event.target)) {
        (focusableElements(dialog)[0] || dialog).focus()
      }
    }
    document.addEventListener('focusin', handleFocus)
    document.addEventListener('keydown', handleKeyDown, true)
    return () => {
      window.clearTimeout(focusTimer)
      document.removeEventListener('keydown', handleKeyDown, true)
      document.removeEventListener('focusin', handleFocus)
      removeDialog(id)
      updateModalIsolation()
      if (!dialogStack.length) document.body.style.overflow = bodyOverflow
      if (previouslyFocused?.isConnected && previouslyFocused !== document.body && isVisible(previouslyFocused)) {
        previouslyFocused.focus()
      } else if (returnFocusSelector) {
        document.querySelector<HTMLElement>(returnFocusSelector)?.focus()
      }
    }
  }, [dialogRef, enabled, initialFocusRef, returnFocusSelector])
}
