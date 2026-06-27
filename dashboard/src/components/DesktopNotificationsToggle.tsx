import { useState } from 'react'

import { isDesktopNotificationsEnabled, setDesktopNotificationsEnabled } from '../hooks/useDesktopNotifications'

// DesktopNotificationsToggle is a self-contained Settings row for the desktop
// shell's native test-outcome notifications. Rendered only in desktop mode.
export default function DesktopNotificationsToggle() {
  const [enabled, setEnabled] = useState(() => isDesktopNotificationsEnabled())

  return (
    <div className="setting-row">
      <div className="setting-info">
        <span className="setting-label">Desktop Notifications</span>
        <span className="setting-desc">
          Show a native notification when a test completes, fails, or a script shakeout fails — only while this window is in the background.
        </span>
      </div>
      <label className="settings-checkbox-label">
        <input
          type="checkbox"
          checked={enabled}
          onChange={event => {
            setEnabled(event.target.checked)
            setDesktopNotificationsEnabled(event.target.checked)
          }}
        />
        <span>{enabled ? 'Enabled' : 'Disabled'}</span>
      </label>
    </div>
  )
}
