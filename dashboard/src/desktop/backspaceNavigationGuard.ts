// Go back is on the platform's back keys (Cmd+[, Alt+Left), not Backspace.
// WebKit can still use Backspace outside an editable field as its own back
// key, which in this hash-routed app would leave the page the person is on
// (the debugger's results, an import review); browsers no longer do this.
// The guard listens on the window, after page handlers have seen the key, and
// cancels only that navigation default.

import { isEditableShortcutTarget } from '../hooks/KeyboardShortcutProvider'

export function installBackspaceNavigationGuard(target: Window): () => void {
  const handler = (event: KeyboardEvent) => {
    if (event.key !== 'Backspace' || event.defaultPrevented) return
    // WebKit does not navigate on Backspace with Cmd, Ctrl or Option held.
    if (event.metaKey || event.ctrlKey || event.altKey) return
    if (isEditableShortcutTarget(event.target, { textEditing: true })) return
    event.preventDefault()
  }
  target.addEventListener('keydown', handler)
  return () => target.removeEventListener('keydown', handler)
}
