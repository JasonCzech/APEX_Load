// Remount-surviving notices for the Settings page.
//
// Every page renders inside a LazyLoadBoundary keyed on the full route hash,
// so any workspace-scope change remounts the page and destroys component
// state — and a single scope switch remounts more than once, because route
// canonicalization then appends the application and release ids to the hash.
// Outcome messages produced just before a switch — "created X in project Y",
// "the create succeeded, do not retry" — are written here and re-read by each
// mount in that chain. A crumb is peeked, not consumed, so it survives the
// whole chain; the freshness bound is what retires it, keeping a stale crumb
// from resurfacing minutes later.
//
// sessionStorage scopes the crumb to the tab, which is also the scope of the
// interaction that produced it.

const FRESHNESS_MS = 15_000

interface StoredBreadcrumb<T> {
  at: number
  value: T
}

function sessionStore(): Storage | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    return window.sessionStorage
  } catch {
    return undefined
  }
}

export function writeSettingsBreadcrumb<T>(key: string, value: T): void {
  const storage = sessionStore()
  if (!storage) return
  try {
    const crumb: StoredBreadcrumb<T> = { at: Date.now(), value }
    storage.setItem(key, JSON.stringify(crumb))
  } catch {
    // Storage can be unavailable in locked-down browser contexts.
  }
}

// Call when a user action retires the notice a crumb carries (retry
// succeeded, selection explicitly cleared it) so a later remount inside the
// freshness window cannot resurrect an already-handled message.
export function clearSettingsBreadcrumb(key: string): void {
  const storage = sessionStore()
  if (!storage) return
  try {
    storage.removeItem(key)
  } catch {
    // Storage can be unavailable in locked-down browser contexts.
  }
}

export function readSettingsBreadcrumb<T>(key: string): T | null {
  const storage = sessionStore()
  if (!storage) return null
  try {
    const raw = storage.getItem(key)
    if (!raw) return null
    const crumb = JSON.parse(raw) as StoredBreadcrumb<T>
    if (typeof crumb?.at !== 'number' || Date.now() - crumb.at > FRESHNESS_MS) {
      storage.removeItem(key)
      return null
    }
    return crumb.value ?? null
  } catch {
    storage.removeItem(key)
    return null
  }
}
