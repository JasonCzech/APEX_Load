import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { apiFetch, initializeDesktopApiOrigin } from './hooks/apiClient'
import { ApiProvider } from './hooks/ApiProvider'
import { getDesktopBridge } from './desktop/desktopBridge'
import { installDesktopDropHandling } from './desktop/useDesktopFileDrop'
import DesktopStartupFailure from './components/DesktopStartupFailure'
import { applyInitialTheme } from './hooks/useDashboardTheme'
import { CLIENT_ERRORS_PATH, installClientErrorReporting } from './utils/clientErrorReporting'
import { frontendBuildMetadata } from './buildMetadata'
import './index.css'
import './components/Modal.css'
import './topbarNavigation.css'
import './themeComponents.css'
import './sectionNavigation.css'
import './typography.css'

async function bootstrap() {
  applyInitialTheme()
  const root = ReactDOM.createRoot(document.getElementById('root')!)
  if (getDesktopBridge()) {
    installDesktopDropHandling()
    root.render(
      <div className="auth-gate-shell">
        <section className="auth-gate-card auth-gate-card--loading" role="status">
          <p className="auth-gate-kicker">APEX Load Desktop</p>
          <h1>Starting local engine</h1>
          <p>Waiting for the secured loopback controller to become ready.</p>
        </section>
      </div>,
    )
  }
  try {
    await initializeDesktopApiOrigin()
  } catch (error: unknown) {
    const bridge = getDesktopBridge()
    const startupError = await bridge?.GetStartupError?.().catch(() => '')
    const message = startupError || (error instanceof Error ? error.message : String(error || ''))
    root.render(<DesktopStartupFailure message={message} />)
    return
  }
  // Installed once the API origin is known, so reports reach the controller
  // this dashboard is talking to.
  installClientErrorReporting(
    report => apiFetch(CLIENT_ERRORS_PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(report),
      keepalive: true,
      timeoutMs: 10_000,
      silent: true,
    }).then(response => response.status, () => undefined),
    { appVersion: frontendBuildMetadata.ux.version },
  )
  root.render(
    <React.StrictMode>
      <ApiProvider>
        <App />
      </ApiProvider>
    </React.StrictMode>,
  )
}

void bootstrap()
