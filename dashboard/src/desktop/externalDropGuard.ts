// A drop from outside the app that is not a file (a link or text dragged from a
// browser or another app) would load in place of the dashboard: a webview
// navigates to a link dropped on a page that does not take it. On Windows the
// desktop webview accepts external drops, because that is how Wails learns a
// dropped file's path (desktop/dragdrop.go); the shell installs this guard on
// every platform so none depends on its webview turning such drops away.
// File drops are left to Wails' runtime, which already cancels their default;
// so are drops into an editable field (text into a search box) and the app's
// own drag and drop, which starts in the page.

import { isEditableShortcutTarget } from '../hooks/KeyboardShortcutProvider'

// installExternalDropGuard returns the function that removes it.
export function installExternalDropGuard(target: Window = window): () => void {
  // A drag that starts in the page is the app's own. It ends at dragend, at a
  // drop, or by the next mouse move (no mouse events fire during a drag), so a
  // dragend lost with a removed source element cannot leave the guard off.
  let internalDrag = false
  const onDragStart = () => { internalDrag = true }
  const onDragEnd = () => { internalDrag = false }
  const guards = (event: DragEvent) => {
    if (internalDrag || event.defaultPrevented) return false
    const types = Array.from(event.dataTransfer?.types ?? [])
    return !types.includes('Files') && !isEditableShortcutTarget(event.target, { textEditing: true })
  }
  // Taking the drag (and refusing it) is what stops the webview from loading it.
  const refuse = (event: DragEvent) => {
    if (!guards(event)) return
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'none'
  }
  const onDrop = (event: DragEvent) => {
    if (guards(event)) event.preventDefault()
    internalDrag = false
  }
  target.addEventListener('dragstart', onDragStart, true)
  target.addEventListener('dragend', onDragEnd, true)
  target.addEventListener('mousemove', onDragEnd, true)
  target.addEventListener('dragenter', refuse)
  target.addEventListener('dragover', refuse)
  target.addEventListener('drop', onDrop)
  return () => {
    target.removeEventListener('dragstart', onDragStart, true)
    target.removeEventListener('dragend', onDragEnd, true)
    target.removeEventListener('mousemove', onDragEnd, true)
    target.removeEventListener('dragenter', refuse)
    target.removeEventListener('dragover', refuse)
    target.removeEventListener('drop', onDrop)
  }
}
