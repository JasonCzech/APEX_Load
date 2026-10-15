import { useState } from 'react'

import { getDesktopBridge } from '../desktop/desktopBridge'
import { saveBlobViaBridge } from '../desktop/desktopFiles'
import { getWailsEventRuntime } from '../desktop/wailsRuntime'
import { copyTextToClipboard } from '../utils/clipboardUtils'

import './AuthGate.css'

export default function DesktopStartupFailure({ message }: { message: string }) {
  const bridge = getDesktopBridge()
  const runtime = getWailsEventRuntime()
  const [diagnostics, setDiagnostics] = useState<{ pending: boolean; message: string; failed: boolean }>({ pending: false, message: '', failed: false })
  // The engine cannot be restarted from inside this window, so the next best
  // help is a diagnostics file the user can attach to a support request. The
  // bridge returns the bundle's JSON; it is saved where the person chooses, or
  // copied when the shell cannot show a Save dialog.
  const exportDiagnostics = async () => {
    if (!bridge?.ExportDiagnostics || diagnostics.pending) return
    setDiagnostics({ pending: true, message: '', failed: false })
    try {
      const bundle = await bridge.ExportDiagnostics()
      const outcome = await saveBlobViaBridge(new Blob([bundle], { type: 'application/json' }), 'apexload-diagnostics.json')
      if (outcome === 'saved') setDiagnostics({ pending: false, message: 'Diagnostics saved.', failed: false })
      else if (outcome === 'cancelled') setDiagnostics({ pending: false, message: 'Diagnostics export cancelled.', failed: false })
      else if (await copyTextToClipboard(bundle)) setDiagnostics({ pending: false, message: 'Diagnostics copied to the clipboard.', failed: false })
      else setDiagnostics({ pending: false, message: 'Diagnostics could not be saved or copied.', failed: true })
    } catch (cause: unknown) {
      setDiagnostics({ pending: false, message: `Diagnostics could not be exported: ${cause instanceof Error ? cause.message : String(cause)}`, failed: true })
    }
  }
  return (
    <div className="auth-gate-shell">
      <section className="auth-gate-card auth-gate-card--error" role="alert">
        <p className="auth-gate-kicker">APEX Load Desktop</p>
        <h1>Local engine failed to start</h1>
        <p>{message || 'The embedded controller did not report a usable loopback address.'}</p>
        <p className="auth-gate-note">Quit and reopen APEX Load to retry the local engine. If it fails again, export diagnostics and include them when you report the problem.</p>
        <div className="auth-gate-actions">
          {runtime?.Quit && (
            <button className="auth-gate-action" type="button" onClick={() => runtime.Quit?.()}>Quit APEX Load</button>
          )}
          {bridge?.ExportDiagnostics && (
            <button className="auth-gate-action" type="button" onClick={() => { void exportDiagnostics() }} disabled={diagnostics.pending}>{diagnostics.pending ? 'Exporting diagnostics…' : 'Export diagnostics…'}</button>
          )}
          {bridge?.OpenLogsFolder && (
            <button className="auth-gate-action" type="button" onClick={() => { void bridge.OpenLogsFolder?.().catch(() => undefined) }}>Open logs</button>
          )}
          {bridge?.OpenAppDataFolder && (
            <button className="auth-gate-action" type="button" onClick={() => { void bridge.OpenAppDataFolder?.().catch(() => undefined) }}>Open app data</button>
          )}
        </div>
        {diagnostics.message && <p className={diagnostics.failed ? 'auth-gate-error' : 'auth-gate-note'} role="status">{diagnostics.message}</p>}
      </section>
    </div>
  )
}
