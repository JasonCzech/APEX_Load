import { THEME_OPTIONS, type DashboardThemePreference } from '../hooks/useDashboardTheme'

interface ThemeToggleProps {
  theme: DashboardThemePreference
  onThemeChange: (theme: DashboardThemePreference) => void
}

export default function ThemeToggle({ theme, onThemeChange }: ThemeToggleProps) {
  return (
    <div className="theme-toggle">
      <select
        className="control-input theme-select"
        value={theme}
        onChange={(e) => onThemeChange(e.target.value as DashboardThemePreference)}
        aria-label="Dashboard color theme"
      >
        {THEME_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </div>
  )
}
