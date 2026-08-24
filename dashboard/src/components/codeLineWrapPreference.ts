import { useCallback, useEffect, useState } from 'react'

export const CODE_LINE_WRAP_STORAGE_KEY = 'apexload.script-editor.word-wrap'
const CODE_LINE_WRAP_EVENT = 'apexload:script-editor-word-wrap'

function readStoredPreference(): boolean {
  if (typeof window === 'undefined') return true
  try {
    return window.localStorage.getItem(CODE_LINE_WRAP_STORAGE_KEY) !== 'off'
  } catch {
    return true
  }
}

export function useCodeLineWrapPreference(): [boolean, (enabled: boolean) => void] {
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
    window.addEventListener(CODE_LINE_WRAP_EVENT, syncPreference)
    return () => {
      window.removeEventListener('storage', syncPreference)
      window.removeEventListener(CODE_LINE_WRAP_EVENT, syncPreference)
    }
  }, [])

  const updatePreference = useCallback((next: boolean) => {
    if (typeof window === 'undefined') {
      setEnabled(next)
      return
    }
    try {
      window.localStorage.setItem(CODE_LINE_WRAP_STORAGE_KEY, next ? 'on' : 'off')
    } catch {
      // The in-memory preference still works when browser storage is unavailable.
    }
    setEnabled(next)
    window.dispatchEvent(new CustomEvent(CODE_LINE_WRAP_EVENT, { detail: next }))
  }, [])

  return [enabled, updatePreference]
}
