function browserStorage(): Storage | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

export function readStoredJSON<T>(key: string, fallback: T): T {
  const storage = browserStorage()
  if (!storage) return fallback
  try {
    const raw = storage.getItem(key)
    if (!raw) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function writeStoredJSON(key: string, value: unknown): void {
  const storage = browserStorage()
  if (!storage) return
  try {
    storage.setItem(key, JSON.stringify(value))
  } catch {
    // localStorage can be unavailable in locked-down browser contexts.
  }
}

export function readStoredString(key: string, fallback = ''): string {
  const storage = browserStorage()
  if (!storage) return fallback
  try {
    return storage.getItem(key) ?? fallback
  } catch {
    return fallback
  }
}

export function writeStoredString(key: string, value: string): void {
  const storage = browserStorage()
  if (!storage) return
  try {
    storage.setItem(key, value)
  } catch {
    // localStorage can be unavailable in locked-down browser contexts.
  }
}

export function removeStoredItem(key: string): void {
  const storage = browserStorage()
  if (!storage) return
  try {
    storage.removeItem(key)
  } catch {
    // localStorage can be unavailable in locked-down browser contexts.
  }
}

export function readStoredStringArray(key: string): string[] {
  const parsed = readStoredJSON<unknown>(key, [])
  return Array.isArray(parsed) ? parsed.filter(value => typeof value === 'string') : []
}

export function readStoredStringArrayRecord(key: string): Record<string, string[]> {
  const parsed = readStoredJSON<unknown>(key, {})
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
  return Object.fromEntries(
    Object.entries(parsed)
      .filter((entry): entry is [string, unknown[]] => typeof entry[0] === 'string' && Array.isArray(entry[1]))
      .map(([recordKey, value]) => [recordKey, value.filter(item => typeof item === 'string')]),
  )
}
