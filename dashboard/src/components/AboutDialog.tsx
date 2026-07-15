import { useEffect, useRef, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { getDesktopBridge } from '../desktop/desktopBridge'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import type { VersionResponse } from '../types'
import { dialogBackdropProps } from './dialogBackdropProps'

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
  const [copied, setCopied] = useState(false)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const bridge = getDesktopBridge()
  useModalDialog({ dialogRef, enabled: open, onClose })

  useEffect(() => {
    if (!open) return
    let cancelled = false
    void getVersion().then(v => { if (!cancelled) setVersion(v) }).catch(() => undefined)
    return () => { cancelled = true }
  }, [open, getVersion])

  if (!open) return null

  const displayVersion = version?.display_version || version?.version || '—'

  const copyDiagnostics = async () => {
    let text: string
    if (bridge?.ExportDiagnostics) {
      text = await bridge.ExportDiagnostics()
    } else {
      text = JSON.stringify(version ?? {}, null, 2)
    }
    if (await copyTextToClipboard(text)) {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className="modal-overlay" {...dialogBackdropProps(onClose, 'Close About dialog')}>
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
            {copied ? 'Copied' : 'Copy diagnostics'}
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
    </div>
  )
}
