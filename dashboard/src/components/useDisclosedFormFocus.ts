import { useEffect, useRef } from 'react'

// An inline form opened by a button that hides while the form is open: focus
// moves to the form's first field when it opens, and back to the button when
// it closes from inside (Cancel or a completed submit). Closing for another
// reason, such as a project switch, leaves focus where the user put it.
export function useDisclosedFormFocus(open: boolean) {
  const openerRef = useRef<HTMLButtonElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const wasOpenRef = useRef(open)
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      formRef.current?.querySelector<HTMLElement>('input:not([disabled]), select:not([disabled]), textarea:not([disabled])')?.focus()
    } else if (!open && wasOpenRef.current && (!document.activeElement || document.activeElement === document.body)) {
      openerRef.current?.focus()
    }
    wasOpenRef.current = open
  }, [open])
  return { openerRef, formRef }
}
