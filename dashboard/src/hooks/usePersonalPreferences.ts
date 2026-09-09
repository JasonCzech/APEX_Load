import { useCallback, useEffect, useRef, useState } from 'react'
import { readStoredJSON, writeStoredJSON } from '../utils/browserStorage'

export type PersonalPreferences = { tableRows: 10 | 25; motion: 'system' | 'reduce' }
export const PERSONAL_PREFERENCES_KEY = 'apexload.personal-preferences'
const EVENT = 'apexload:personal-preferences'
export const DEFAULT_PERSONAL_PREFERENCES: PersonalPreferences = { tableRows: 10, motion: 'system' }
export function readPersonalPreferences(): PersonalPreferences {
  const value = readStoredJSON<Partial<PersonalPreferences> | null>(PERSONAL_PREFERENCES_KEY, null)
  return { tableRows: value?.tableRows === 25 ? 25 : 10, motion: value?.motion === 'reduce' ? 'reduce' : 'system' }
}
export function usePersonalPreferences() {
  const [preferences, setPreferences] = useState(readPersonalPreferences)
  const current = useRef(preferences)
  useEffect(() => {
    const sync = (event: Event) => {
      if (event instanceof StorageEvent && event.key !== null && event.key !== PERSONAL_PREFERENCES_KEY) return
      const next = event instanceof CustomEvent ? event.detail as PersonalPreferences : readPersonalPreferences()
      current.current = next
      setPreferences(next)
    }
    window.addEventListener(EVENT, sync)
    window.addEventListener('storage', sync)
    return () => { window.removeEventListener(EVENT, sync); window.removeEventListener('storage', sync) }
  }, [])
  const update = useCallback((patch: Partial<PersonalPreferences>) => {
    const next = { ...current.current, ...patch }
    current.current = next
    writeStoredJSON(PERSONAL_PREFERENCES_KEY, next)
    setPreferences(next)
    window.dispatchEvent(new CustomEvent(EVENT, { detail: next }))
  }, [])
  return [preferences, update] as const
}

export function usePersonalAppearance() {
  const [preferences] = usePersonalPreferences()
  useEffect(() => {
    document.documentElement.dataset.motion = preferences.motion
    return () => { delete document.documentElement.dataset.motion }
  }, [preferences.motion])
}
