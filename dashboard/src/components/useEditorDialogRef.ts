import { useRef } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'

export function useEditorDialogRef(onClose: () => void) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose })
  return dialogRef
}
