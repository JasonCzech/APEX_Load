import DialogLayer from './ux/DialogLayer'
import { useEffect, useRef, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { getDesktopBridge } from '../desktop/desktopBridge'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import type { VersionResponse } from '../types'
import { dialogBackdropProps } from './dialogBackdropProps'
import './AboutDialog.css'

interface AboutDialogProps {
  open: boolean
  onClose: () => void
}

// AboutDialog shows live build metadata (version/commit/build time) from
// /api/v1/system/version, replacing the previously hardcoded macOS About text
// and giving Windows/Linux an in-app About on every platform. In the desktop
// shell it also exposes diagnostics export + the logs folder for support.
export default function AboutDialog({ open, onClose }: AboutDialogProps) {
  const { getVersion } = useSystemApi()
  const [version, setVersion] = useState<VersionResponse | null>(null)
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const copyResetRef = useRef<number | undefined>(undefined)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const bridge = getDesktopBridge()
  useModalDialog({ dialogRef, enabled: open, onClose })

  useEffect(() => {
    if (!open) return
    let cancelled = false
    void getVersion().then(v => { if (!cancelled) setVersion(v) }).catch(() => undefined)
    return () => { cancelled = true }
  }, [open, getVersion])

  // A copy result's pending reset goes when the dialog does.
  useEffect(() => () => window.clearTimeout(copyResetRef.current), [])

  if (!open) return null

  const displayVersion = version?.display_version || version?.version || '—'

  // A failed export or copy says so on the button, rather than nothing at all.
  const copyDiagnostics = async () => {
    let copied = false
    try {
      const text = bridge?.ExportDiagnostics ? await bridge.ExportDiagnostics() : JSON.stringify(version ?? {}, null, 2)
      copied = await copyTextToClipboard(text)
    } catch {
      copied = false
    }
    setCopyState(copied ? 'copied' : 'failed')
    // Only the latest result's reset runs, so it stays up its full two seconds.
    window.clearTimeout(copyResetRef.current)
    copyResetRef.current = window.setTimeout(() => setCopyState('idle'), 2000)
  }

  return (
    <DialogLayer><div className="modal-overlay" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="modal-content about-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="about-dialog-title"
        style={{ maxWidth: 460 }}
      >
        <h2 id="about-dialog-title" style={{ marginTop: 0 }}>APEX Load</h2>
        <p style={{ color: 'var(--text-muted)', marginTop: '-8px' }}>Enterprise Performance Testing Platform</p>
        <dl className="about-grid">
          <dt>Version</dt><dd>{displayVersion}</dd>
          {version?.commit && (<><dt>Commit</dt><dd>{version.commit}</dd></>)}
          {version?.build_time && (<><dt>Built</dt><dd>{version.build_time}</dd></>)}
          {version?.go_version && (<><dt>Go</dt><dd>{version.go_version}</dd></>)}
          {version?.environment && (<><dt>Environment</dt><dd>{version.environment}</dd></>)}
        </dl>
        <div className="about-actions" style={{ display: 'flex', gap: 8, marginTop: 16, flexWrap: 'wrap' }}>
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => { void copyDiagnostics() }}>
            {copyState === 'copied' ? 'Copied' : copyState === 'failed' ? 'Copy failed' : 'Copy diagnostics'}
          </button>
          {bridge?.OpenLogsFolder && (
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => { void bridge.OpenLogsFolder?.() }}>
              Open logs folder
            </button>
          )}
          <button type="button" className="btn btn-sm btn-primary" style={{ marginLeft: 'auto' }} onClick={onClose}>Close</button>
        </div>
        <p style={{ color: 'var(--text-muted)', fontSize: 12, marginBottom: 0 }}>© 2026 Project Stormrunner</p>
      </div>
    </div></DialogLayer>
  )
}
