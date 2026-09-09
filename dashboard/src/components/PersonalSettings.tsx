import { type DashboardAuthStatus } from '../types'
import { type DashboardThemePreference } from '../hooks/useDashboardTheme'
import { usePersonalPreferences } from '../hooks/usePersonalPreferences'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'
import { isDesktopSurface } from '../desktop/desktopBridge'
import DesktopNotificationsToggle from './DesktopNotificationsToggle'
import FormField from './ux/FormField'
import './PersonalSettings.css'

export type PersonalSection = 'appearance' | 'workspace' | 'notifications' | 'account'
export type PersonalSettingsProps = { theme: DashboardThemePreference; onThemeChange: (theme: DashboardThemePreference) => void; authStatus?: DashboardAuthStatus; section?: PersonalSection }

/** Shared by personal-only settings and the administrative Preferences destination. */
export default function PersonalSettings({ theme, onThemeChange, authStatus, section }: PersonalSettingsProps) {
  const [preferences, updatePreferences] = usePersonalPreferences()
  const [wordWrap, setWordWrap] = useCodeLineWrapPreference()
  const show = (id: PersonalSection) => !section || section === id
  return <div className="personal-settings-content">
    {show('appearance') && <section className="settings-section" aria-label="Appearance preferences"><h3>Appearance</h3>
      <FormField label="Dashboard Theme" htmlFor="personal-theme" hint="Choose a theme or follow your device's light/dark setting."><select className="settings-input" value={theme} onChange={event => onThemeChange(event.target.value as DashboardThemePreference)}>{[['system','System (Auto)'],['dark','Dark'],['light','Light'],['solarized-dark','Solarized Dark'],['solarized-light','Solarized Light'],['monokai-dimmed','Monokai Dimmed']].map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select></FormField>
      <FormField label="Motion" htmlFor="personal-motion" hint="Reduce interface animations and smooth scrolling. Following your device also respects its accessibility preference."><select className="settings-input" value={preferences.motion} onChange={event => updatePreferences({motion:event.target.value as 'system' | 'reduce'})}><option value="system">Follow device preference</option><option value="reduce">Reduce motion</option></select></FormField>
    </section>}
    {show('workspace') && <section className="settings-section" aria-label="Tables and editor preferences"><h3>Tables &amp; editor</h3>
      <FormField label="Default rows per table" htmlFor="personal-table-rows" hint="Default for standard dashboard tables. A table's Rows control can override this; server-paged and specialized tables keep their own controls."><select className="settings-input" value={preferences.tableRows} onChange={event => updatePreferences({tableRows:Number(event.target.value) as 10 | 25})}><option value="10">10 rows</option><option value="25">25 rows</option></select></FormField>
      <FormField label="Editor word wrap" htmlFor="personal-word-wrap" hint="Wrap long lines in supported script and code editors. This does not modify the saved script."><select className="settings-input" value={wordWrap ? 'on' : 'off'} onChange={event => setWordWrap(event.target.value === 'on')}><option value="on">Wrap long lines</option><option value="off">Scroll horizontally</option></select></FormField>
    </section>}
    {show('notifications') && <section className="settings-section" aria-label="Notification preferences"><h3>Notifications</h3>{isDesktopSurface() ? <DesktopNotificationsToggle /> : <p className="setting-desc">Test-outcome desktop notifications are available in the APEX Load desktop app. In this browser, follow live status in Command Center and completed runs in Results.</p>}<p className="setting-desc">Team email and webhook alerts are managed by your administrator.</p></section>}
    {show('account') && <section className="settings-section" aria-label="Your account"><h3>Account</h3><dl className="personal-account-details"><div><dt>Name</dt><dd>{authStatus?.user?.display_name || authStatus?.user?.subject || (authStatus?.mode === 'dev' ? 'Development preview' : 'Unavailable')}</dd></div><div><dt>Email</dt><dd>{authStatus?.user?.email || 'Not provided'}</dd></div><div><dt>Sign-in provider</dt><dd>{authStatus?.provider_name || authStatus?.mode || 'Unavailable'}</dd></div></dl><p className="setting-desc">Your profile and access are managed by your organization. Use Sign out in the sidebar to end your session.</p></section>}
  </div>
}
