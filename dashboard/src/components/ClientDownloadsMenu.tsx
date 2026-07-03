import { useEffect, useRef, useState } from 'react'

import { apiFetch } from '../hooks/apiClient'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { downloadBlob, downloadFilenameFromDisposition } from '../utils/fileUtils'
import type { VersionResponse } from '../types'
import './ClientDownloadsMenu.css'

export interface DownloadPlatform {
  value: string
  label: string
}

// The five platforms the controller stages packages for (see
// sanitizeDownloadPlatform in pkg/api). macOS auto-detection only selects a
// package when the browser exposes an architecture hint; otherwise the picker
// is safer than sending Intel Macs to the Apple Silicon archive.
export const CLIENT_DOWNLOAD_PLATFORMS: DownloadPlatform[] = [
  { value: 'darwin-arm64', label: 'macOS (Apple Silicon)' },
  { value: 'darwin-amd64', label: 'macOS (Intel)' },
  { value: 'windows-amd64', label: 'Windows (x64)' },
  { value: 'linux-amd64', label: 'Linux (x64)' },
  { value: 'linux-arm64', label: 'Linux (ARM64)' },
]

interface DownloadArtifact {
  key: string
  name: string
  description: string
  path: string
  fallbackFilename: string
}

const ARTIFACTS: DownloadArtifact[] = [
  {
    key: 'recording-agent',
    name: 'Recording Agent',
    description: 'Local browser-recording bridge for recording sessions from your workstation.',
    path: '/downloads/recording-agent/latest',
    fallbackFilename: 'apexload-recording-agent.tar.gz',
  },
  {
    key: 'desktop',
    name: 'Desktop App',
    description: 'Native desktop client with an embedded local engine for offline authoring and runs.',
    path: '/downloads/desktop/latest',
    fallbackFilename: 'apexload-desktop.tar.gz',
  },
]

interface NavigatorUserAgentData {
  platform?: string
  getHighEntropyValues?: (hints: string[]) => Promise<{ platform?: string; architecture?: string }>
}

export function architecturePlatform(osHint: string, architectureHint: string): string | null {
  const os = osHint.toLowerCase()
  const architecture = architectureHint.toLowerCase()
  if (os.includes('mac')) {
    if (architecture.includes('arm') || architecture.includes('aarch64')) return 'darwin-arm64'
    if (architecture.includes('x86') || architecture.includes('amd64')) return 'darwin-amd64'
    return null
  }
  if (os.includes('win')) return 'windows-amd64'
  if (os.includes('linux')) {
    if (architecture.includes('arm') || architecture.includes('aarch64')) return 'linux-arm64'
    return 'linux-amd64'
  }
  return null
}

// detectPlatformFromNavigator guesses the visitor's platform. A null return
// intentionally falls back to the full picker.
export async function detectPlatformFromNavigator(nav?: Navigator): Promise<string | null> {
  const currentNavigator = nav || (typeof navigator === 'undefined' ? null : navigator)
  if (!currentNavigator) return null
  const uaData = (currentNavigator as Navigator & { userAgentData?: NavigatorUserAgentData }).userAgentData
  if (uaData?.getHighEntropyValues) {
    try {
      const values = await uaData.getHighEntropyValues(['architecture', 'platform'])
      const detected = architecturePlatform(values.platform || uaData.platform || '', values.architecture || '')
      if (detected) return detected
    } catch {
      // Fall back to lower-fidelity User-Agent checks below.
    }
  }
  const ua = `${currentNavigator.userAgent || ''} ${currentNavigator.platform || ''}`.toLowerCase()
  if (ua.includes('win')) return 'windows-amd64'
  if (ua.includes('mac')) return null
  if (ua.includes('linux') || ua.includes('android')) return 'linux-amd64'
  return null
}

export function platformLabel(value: string): string {
  return CLIENT_DOWNLOAD_PLATFORMS.find(p => p.value === value)?.label || value
}

interface StatusMessage {
  tone: 'success' | 'error'
  text: string
}

export default function ClientDownloadsMenu() {
  const { getVersion } = useSystemApi()
  const [version, setVersion] = useState<VersionResponse | null>(null)
  const [open, setOpen] = useState(false)
  const [status, setStatus] = useState<StatusMessage | null>(null)
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let cancelled = false
    getVersion()
      .then(response => {
        if (!cancelled) setVersion(response)
      })
      .catch(() => {
        if (!cancelled) setVersion(null)
      })
    return () => {
      cancelled = true
    }
  }, [getVersion])

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const [detectedPlatform, setDetectedPlatform] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    detectPlatformFromNavigator().then(platform => {
      if (!cancelled) setDetectedPlatform(platform)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const enabled = Boolean(version?.deployment?.client_downloads_enabled)
  if (!version || !enabled) return null

  const download = async (artifact: DownloadArtifact, platform: string) => {
    setStatus(null)
    setBusyKey(`${artifact.key}:${platform}`)
    try {
      const res = await apiFetch(`${artifact.path}?platform=${encodeURIComponent(platform)}`)
      if (!res.ok) {
        let message = `${artifact.name} for ${platformLabel(platform)} is not available from this controller.`
        try {
          const data = await res.json()
          if (typeof data?.error === 'string') message = data.error
        } catch {
          // non-JSON error body; keep the default message
        }
        throw new Error(message)
      }
      const contentType = (res.headers.get('content-type') || '').toLowerCase()
      if (contentType.includes('text/html')) {
        throw new Error(`${artifact.name} download returned the dashboard instead of an archive. Update and restart the controller.`)
      }
      const blob = await res.blob()
      downloadBlob(blob, downloadFilenameFromDisposition(res.headers.get('content-disposition'), artifact.fallbackFilename))
      setStatus({ tone: 'success', text: `${artifact.name} download started (${platformLabel(platform)}).` })
    } catch (e: any) {
      setStatus({ tone: 'error', text: e?.message || `Failed to download ${artifact.name}.` })
    } finally {
      setBusyKey(null)
    }
  }

  return (
    <div className="client-downloads-menu" ref={containerRef}>
      <button
        type="button"
        className="btn btn-sm btn-secondary client-downloads-trigger"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(value => !value)}
      >
        <span className="client-downloads-trigger-icon" aria-hidden="true">⤓</span>
        <span className="client-downloads-trigger-label">Download client</span>
      </button>

      {open && (
        <div className="client-downloads-panel" role="dialog" aria-label="Download client software">
          <div className="client-downloads-panel-header">Client software</div>
          {ARTIFACTS.map(artifact => {
            const expanded = expandedKey === artifact.key
            return (
              <div className="client-downloads-item" key={artifact.key}>
                <div className="client-downloads-item-name">{artifact.name}</div>
                <div className="client-downloads-item-desc">{artifact.description}</div>
                <div className="client-downloads-item-actions">
                  {detectedPlatform ? (
                    <button
                      type="button"
                      className="btn btn-sm btn-primary"
                      disabled={busyKey === `${artifact.key}:${detectedPlatform}`}
                      onClick={() => download(artifact, detectedPlatform)}
                    >
                      {busyKey === `${artifact.key}:${detectedPlatform}` ? 'Starting…' : `Download for ${platformLabel(detectedPlatform)}`}
                    </button>
                  ) : (
                    <span className="client-downloads-pick-hint">Choose a platform:</span>
                  )}
                  {detectedPlatform && (
                    <button
                      type="button"
                      className="client-downloads-toggle"
                      aria-expanded={expanded}
                      onClick={() => setExpandedKey(expanded ? null : artifact.key)}
                    >
                      Other platforms {expanded ? '▴' : '▾'}
                    </button>
                  )}
                </div>
                {(expanded || !detectedPlatform) && (
                  <div className="client-downloads-platforms">
                    {CLIENT_DOWNLOAD_PLATFORMS.filter(p => p.value !== detectedPlatform).map(platform => (
                      <button
                        type="button"
                        key={platform.value}
                        className="client-downloads-platform-link"
                        disabled={busyKey === `${artifact.key}:${platform.value}`}
                        onClick={() => download(artifact, platform.value)}
                      >
                        {platform.label}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
          {status && (
            <div className={`client-downloads-status ${status.tone}`} role={status.tone === 'error' ? 'alert' : 'status'}>
              {status.text}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
