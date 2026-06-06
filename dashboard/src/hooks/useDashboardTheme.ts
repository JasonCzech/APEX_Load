import { useEffect, useState } from 'react'

export type DashboardTheme = 'dark' | 'light'

const THEME_STORAGE_KEY = 'apexload.theme'
const DASHBOARD_THEMES = new Set<DashboardTheme>(['dark', 'light'])

function isDashboardTheme(value: string | null): value is DashboardTheme {
  return value !== null && DASHBOARD_THEMES.has(value as DashboardTheme)
}

function readStoredTheme(): DashboardTheme {
  if (typeof window === 'undefined') {
    return 'dark'
  }

  const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
  return isDashboardTheme(stored) ? stored : 'dark'
}

export function useDashboardTheme() {
  const [theme, setTheme] = useState<DashboardTheme>(() => readStoredTheme())

  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.dataset.theme = theme
      document.documentElement.style.colorScheme = theme === 'dark' ? 'dark' : 'light'
    }

    if (typeof window !== 'undefined') {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme)
    }
  }, [theme])

  return { theme, setTheme }
}
