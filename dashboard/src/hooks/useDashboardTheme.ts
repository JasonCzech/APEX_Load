import { useEffect, useState } from 'react'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

// Concrete themes that map to a data-theme attribute.
export type DashboardTheme = 'dark' | 'light' | 'solarized-dark' | 'solarized-light' | 'monokai-dimmed'
// The user-selectable preference, which additionally includes 'system' (follow
// the OS light/dark setting). 'system' is stored as the preference; the applied
// data-theme is always one of the concrete DashboardTheme values.
export type DashboardThemePreference = DashboardTheme | 'system'

/** Theme choices in display order, labelled the same in the top bar and in personal settings. */
export const THEME_OPTIONS: ReadonlyArray<{ value: DashboardThemePreference; label: string }> = [
  { value: 'system', label: 'System (Auto)' },
  { value: 'dark', label: 'Dark Theme' },
  { value: 'light', label: 'Light Theme' },
  { value: 'solarized-dark', label: 'Solarized Dark' },
  { value: 'solarized-light', label: 'Solarized Light' },
  { value: 'monokai-dimmed', label: 'Monokai Dimmed' },
]

const THEME_STORAGE_KEY = 'apexload.theme'
const DASHBOARD_THEMES = new Set<DashboardTheme>(['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed'])
const THEME_PREFERENCES = new Set<DashboardThemePreference>([...DASHBOARD_THEMES, 'system'])
const DARK_BROWSER_CONTROL_THEMES = new Set<DashboardTheme>(['dark', 'solarized-dark', 'monokai-dimmed'])
const SYSTEM_DARK_QUERY = '(prefers-color-scheme: dark)'
// New browsers (no stored preference) start in the Light theme.
const DEFAULT_THEME: DashboardThemePreference = 'light'

function isThemePreference(value: string | null): value is DashboardThemePreference {
  return value !== null && THEME_PREFERENCES.has(value as DashboardThemePreference)
}

function readStoredTheme(): DashboardThemePreference {
  if (typeof window === 'undefined') {
    return DEFAULT_THEME
  }
  const stored = readStoredString(THEME_STORAGE_KEY)
  return isThemePreference(stored) ? stored : DEFAULT_THEME
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

// Apply the stored (or default) theme before React's first render so the page
// never paints in the wrong theme while the app loads.
export function applyInitialTheme() {
  applyTheme(resolveTheme(readStoredTheme()))
}

export function useDashboardTheme() {
  const [theme, setTheme] = useState<DashboardThemePreference>(() => readStoredTheme())

  useEffect(() => {
    applyTheme(resolveTheme(theme))

    if (typeof window !== 'undefined') {
      // Persist the PREFERENCE ('system' included), never the resolved value.
      writeStoredString(THEME_STORAGE_KEY, theme)
    }

    // When following the OS, re-resolve live as the system setting changes.
    if (theme !== 'system' || typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined
    }
    const media = window.matchMedia(SYSTEM_DARK_QUERY)
    const handler = () => applyTheme(resolveTheme('system'))
    // Prefer the modern API; fall back to the deprecated addListener for older
    // Safari/WebKit (and the macOS WKWebView) which lack addEventListener on MQL.
    if (typeof media.addEventListener === 'function') {
      media.addEventListener('change', handler)
      return () => media.removeEventListener('change', handler)
    }
    const legacy = media as MediaQueryList & {
      addListener?: (cb: (e: MediaQueryListEvent) => void) => void
      removeListener?: (cb: (e: MediaQueryListEvent) => void) => void
    }
    legacy.addListener?.(handler)
    return () => legacy.removeListener?.(handler)
  }, [theme])

  return { theme, setTheme }
}
