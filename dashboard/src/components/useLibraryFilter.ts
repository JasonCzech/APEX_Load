import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from 'react'

function readFilter<T extends string>(key: string | null, fallback: T, allowed?: readonly T[]): T {
  if (key === null) return fallback
  try {
    const value: unknown = JSON.parse(window.sessionStorage.getItem(key) || 'null')
    return typeof value === 'string' && (!allowed || allowed.includes(value as T)) ? value as T : fallback
  } catch { return fallback }
}

/**
 * Restore exploration after opening an asset or report without sharing filters
 * across projects. A null scope keeps the value in memory only.
 */
export function useLibraryFilter<T extends string = string>(scope: string | null, field: string, fallback: T, allowed?: readonly T[]): [T, Dispatch<SetStateAction<T>>] {
  const key = scope === null ? null : `apexload.library-filter:${scope}:${field}`
  const [saved, setSaved] = useState(() => ({ key, value: readFilter(key, fallback, allowed) }))
  const value = saved.key === key ? saved.value : readFilter(key, fallback, allowed)
  if (saved.key !== key) setSaved({ key, value })
  useEffect(() => {
    if (key === null) return
    try { window.sessionStorage.setItem(key, JSON.stringify(value)) } catch { /* Filtering works without storage. */ }
  }, [key, value])
  const setValue: Dispatch<SetStateAction<T>> = useCallback(next => {
    setSaved(current => {
      const previous = current.key === key ? current.value : readFilter(key, fallback, allowed)
      return { key, value: typeof next === 'function' ? next(previous) : next }
    })
  }, [key, fallback, allowed])
  return [value, setValue]
}
