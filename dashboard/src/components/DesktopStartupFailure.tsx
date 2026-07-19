import { getDesktopBridge } from '../desktop/desktopBridge'
import { getWailsEventRuntime } from '../desktop/wailsRuntime'

import './AuthGate.css'

export default function DesktopStartupFailure({ message }: { message: string }) {
  const bridge = getDesktopBridge()
  const runtime = getWailsEventRuntime()
  return (
    <div className="auth-gate-shell">
      <section className="auth-gate-card auth-gate-card--error" role="alert">
        <p className="auth-gate-kicker">APEX Load Desktop</p>
        <h1>Local engine failed to start</h1>
        <p>{message || 'The embedded controller did not report a usable loopback address.'}</p>
        <p className="auth-gate-note">Quit and reopen APEX Load to retry the local engine.</p>
        <div className="auth-gate-actions">
          {runtime?.Quit && (
            <button className="auth-gate-action" type="button" onClick={() => runtime.Quit?.()}>Quit APEX Load</button>
          )}
          {bridge?.OpenLogsFolder && (
            <button className="auth-gate-action" type="button" onClick={() => { void bridge.OpenLogsFolder?.().catch(() => undefined) }}>Open logs</button>
          )}
          {bridge?.OpenAppDataFolder && (
            <button className="auth-gate-action" type="button" onClick={() => { void bridge.OpenAppDataFolder?.().catch(() => undefined) }}>Open app data</button>
          )}
        </div>
      </section>
    </div>
  )
}
