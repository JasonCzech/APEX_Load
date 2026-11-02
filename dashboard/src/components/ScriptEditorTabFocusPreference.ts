import { useCallback, useEffect, useState } from 'react'

// The script editors' CodeMirror panes (Script JSON, request body) indent on
// Tab by default; Esc, then Tab leaves them. Keyboard-only and switch-access
// users can instead make Tab move focus, which every such editor shares.
export const EDITOR_TAB_MOVES_FOCUS_STORAGE_KEY = 'apexload.script-editor.tab-moves-focus'
const EDITOR_TAB_MOVES_FOCUS_EVENT = 'apexload:script-editor-tab-moves-focus'

function readStoredPreference(): boolean {
  if (typeof window === 'undefined') return false
  try {
    return window.localStorage.getItem(EDITOR_TAB_MOVES_FOCUS_STORAGE_KEY) === 'on'
  } catch {
    return false
  }
}

/** Whether Tab moves focus out of the script editors' code panes (instead of indenting). */
export function useEditorTabMovesFocus(): [boolean, (enabled: boolean) => void] {
  const [enabled, setEnabled] = useState(readStoredPreference)

  useEffect(() => {
    if (typeof window === 'undefined') return undefined
    const syncPreference = (event: Event) => {
      if (event instanceof CustomEvent && typeof event.detail === 'boolean') {
        setEnabled(event.detail)
        return
      }
      setEnabled(readStoredPreference())
    }
    window.addEventListener('storage', syncPreference)
    window.addEventListener(EDITOR_TAB_MOVES_FOCUS_EVENT, syncPreference)
    return () => {
      window.removeEventListener('storage', syncPreference)
      window.removeEventListener(EDITOR_TAB_MOVES_FOCUS_EVENT, syncPreference)
    }
  }, [])

  const updatePreference = useCallback((next: boolean) => {
    if (typeof window === 'undefined') {
      setEnabled(next)
      return
    }
    try {
      window.localStorage.setItem(EDITOR_TAB_MOVES_FOCUS_STORAGE_KEY, next ? 'on' : 'off')
    } catch {
      // The in-memory preference still works when browser storage is unavailable.
    }
    setEnabled(next)
    window.dispatchEvent(new CustomEvent(EDITOR_TAB_MOVES_FOCUS_EVENT, { detail: next }))
  }, [])

  return [enabled, updatePreference]
}

/** The keyboard hint beside a code pane, for the current Tab behavior. */
export function editorTabFocusHint(tabMovesFocus: boolean, editorName: string): string {
  return tabMovesFocus
    ? `Tab moves focus out of the ${editorName}. Turn off Tab moves focus to indent with Tab.`
    : `Press Esc, then Tab to move focus out of the ${editorName}.`
}
