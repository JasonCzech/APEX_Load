import { useEffect, useState } from 'react'

// Concrete themes that map to a data-theme attribute.
export type DashboardTheme = 'dark' | 'light' | 'solarized-dark' | 'solarized-light' | 'monokai-dimmed'
// The user-selectable preference, which additionally includes 'system' (follow
// the OS light/dark setting). 'system' is stored as the preference; the applied
// data-theme is always one of the concrete DashboardTheme values.
export type DashboardThemePreference = DashboardTheme | 'system'

const THEME_STORAGE_KEY = 'apexload.theme'
const DASHBOARD_THEMES = new Set<DashboardTheme>(['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed'])
const THEME_PREFERENCES = new Set<DashboardThemePreference>([...DASHBOARD_THEMES, 'system'])
const DARK_BROWSER_CONTROL_THEMES = new Set<DashboardTheme>(['dark', 'solarized-dark', 'monokai-dimmed'])
const SYSTEM_DARK_QUERY = '(prefers-color-scheme: dark)'

function isThemePreference(value: string | null): value is DashboardThemePreference {
  return value !== null && THEME_PREFERENCES.has(value as DashboardThemePreference)
}

function readStoredTheme(): DashboardThemePreference {
  if (typeof window === 'undefined') {
    return 'dark'
  }
  const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
  return isThemePreference(stored) ? stored : 'dark'
}

function prefersDark(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia(SYSTEM_DARK_QUERY).matches
}

// resolveTheme turns a preference into the concrete theme to apply. Pure given
// the matchMedia state; exported for testing.
export function resolveTheme(pref: DashboardThemePreference): DashboardTheme {
  if (pref === 'system') {
    return prefersDark() ? 'dark' : 'light'
  }
  return pref
}

function applyTheme(resolved: DashboardTheme) {
  if (typeof document === 'undefined') {
    return
  }
  document.documentElement.dataset.theme = resolved
  document.documentElement.style.colorScheme = DARK_BROWSER_CONTROL_THEMES.has(resolved) ? 'dark' : 'light'
}

export function useDashboardTheme() {
  const [theme, setTheme] = useState<DashboardThemePreference>(() => readStoredTheme())

  useEffect(() => {
    applyTheme(resolveTheme(theme))

    if (typeof window !== 'undefined') {
      // Persist the PREFERENCE ('system' included), never the resolved value.
      window.localStorage.setItem(THEME_STORAGE_KEY, theme)
    }

    // When following the OS, re-resolve live as the system setting changes.
    if (theme !== 'system' || typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined
    }
    const media = window.matchMedia(SYSTEM_DARK_QUERY)
    const handler = () => applyTheme(resolveTheme('system'))
    media.addEventListener?.('change', handler)
    return () => media.removeEventListener?.('change', handler)
  }, [theme])

  return { theme, setTheme }
}
