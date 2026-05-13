import { useEffect, useState } from 'react'

export type DashboardTheme = 'dark' | 'light'

const THEME_STORAGE_KEY = 'stormrunner.theme'

function readStoredTheme(): DashboardTheme {
  if (typeof window === 'undefined') {
    return 'dark'
  }

  const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
  return stored === 'light' ? 'light' : 'dark'
}

export function useDashboardTheme() {
  const [theme, setTheme] = useState<DashboardTheme>(() => readStoredTheme())

  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.dataset.theme = theme
      document.documentElement.style.colorScheme = theme
    }

    if (typeof window !== 'undefined') {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme)
    }
  }, [theme])

  return { theme, setTheme }
}