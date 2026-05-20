import { DashboardTheme } from '../hooks/useDashboardTheme'

interface ThemeToggleProps {
  theme: DashboardTheme
  onThemeChange: (theme: DashboardTheme) => void
}

export default function ThemeToggle({ theme, onThemeChange }: ThemeToggleProps) {
  return (
    <div className="theme-toggle" role="group" aria-label="Dashboard color theme">
      <button
        type="button"
        className={theme === 'light' ? 'active' : ''}
        aria-pressed={theme === 'light'}
        onClick={() => onThemeChange('light')}
      >
        <span className="theme-toggle-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" focusable="false">
            <circle cx="12" cy="12" r="4" />
            <path d="M12 2v2.5M12 19.5V22M4.93 4.93 6.7 6.7M17.3 17.3l1.77 1.77M2 12h2.5M19.5 12H22M4.93 19.07 6.7 17.3M17.3 6.7l1.77-1.77" />
          </svg>
        </span>
        <span>Light</span>
      </button>
      <button
        type="button"
        className={theme === 'dark' ? 'active' : ''}
        aria-pressed={theme === 'dark'}
        onClick={() => onThemeChange('dark')}
      >
        <span className="theme-toggle-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" focusable="false">
            <path d="M20.4 14.7A8 8 0 0 1 9.3 3.6 8.6 8.6 0 1 0 20.4 14.7Z" />
          </svg>
        </span>
        <span>Dark</span>
      </button>
    </div>
  )
}
