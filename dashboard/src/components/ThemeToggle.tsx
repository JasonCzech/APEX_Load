import { DashboardThemePreference } from '../hooks/useDashboardTheme'

interface ThemeToggleProps {
  theme: DashboardThemePreference
  onThemeChange: (theme: DashboardThemePreference) => void
}

export default function ThemeToggle({ theme, onThemeChange }: ThemeToggleProps) {
  return (
    <div className="theme-toggle" style={{ display: 'flex', alignItems: 'center' }}>
      <select
        className="control-input"
        style={{ padding: '4px 8px', borderRadius: 'var(--radius)', backgroundColor: 'var(--bg-surface)', color: 'var(--text-primary)', border: '1px solid var(--border)', fontSize: '14px' }}
        value={theme}
        onChange={(e) => onThemeChange(e.target.value as DashboardThemePreference)}
        aria-label="Dashboard color theme"
      >
        <option value="system">System (Auto)</option>
        <option value="dark">Dark Theme</option>
        <option value="light">Light Theme</option>
        <option value="solarized-dark">Solarized Dark</option>
        <option value="solarized-light">Solarized Light</option>
        <option value="monokai-dimmed">Monokai Dimmed</option>
      </select>
    </div>
  )
}
