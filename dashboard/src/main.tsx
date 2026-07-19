import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { initializeDesktopApiOrigin } from './hooks/apiClient'
import { ApiProvider } from './hooks/useApi'
import { getDesktopBridge } from './desktop/desktopBridge'
import DesktopStartupFailure from './components/DesktopStartupFailure'
import './index.css'
import './components/Modal.css'

async function bootstrap() {
  const root = ReactDOM.createRoot(document.getElementById('root')!)
  if (getDesktopBridge()) {
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
  root.render(
    <React.StrictMode>
      <ApiProvider>
        <App />
      </ApiProvider>
    </React.StrictMode>,
  )
}

void bootstrap()
