import { useEffect, useRef, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import { VersionResponse } from '../types'
import './BuildBadge.css'

// Maps a runtime environment to a badge tone. Anything Kubernetes-like (incl.
// aks/eks/gke) reads as "running in the cluster" (green); container/local read
// as info/neutral. Unknown values fall back to neutral.
function envTone(environment: string): 'cluster' | 'container' | 'local' {
  const env = environment.toLowerCase()
  if (env === 'kubernetes' || env === 'aks' || env === 'eks' || env === 'gke' || env === 'k8s') {
    return 'cluster'
  }
  if (env === 'docker' || env === 'container') {
    return 'container'
  }
  return 'local'
}

function formatBuildTime(buildTime?: string): string | null {
  if (!buildTime) return null
  const parsed = new Date(buildTime)
  return Number.isNaN(parsed.getTime()) ? buildTime : parsed.toLocaleString()
}

function formatVersionLabel(version?: string): string {
  const raw = (version || '').trim()
  if (!raw) return 'dev'
  if (/^[0-9a-fA-F]{12,64}$/.test(raw)) return `dev+${raw.slice(0, 7)}`
  const normalized = raw.length > 1 && /^[vV][0-9]/.test(raw) ? raw.slice(1) : raw
  return /^[0-9]/.test(normalized) ? `v${normalized}` : normalized
}

// BuildBadge is a self-contained, subtle indicator of the running build and
// environment. It fetches the version once on mount and renders nothing if the
// endpoint is unreachable, so it can never break its host chrome.
//
// placement controls which direction the details popover opens. Use "up" when
// the badge sits near the bottom of the viewport (e.g. the sidebar footer).
export default function BuildBadge({ placement = 'down' }: { placement?: 'up' | 'down' }) {
  const { getVersion } = useSystemApi()
  const [info, setInfo] = useState<VersionResponse | null>(null)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let active = true
    getVersion()
      .then((data) => {
        if (active) setInfo(data)
      })
      .catch(() => {
        // Silent: the indicator is non-critical chrome.
      })
    return () => {
      active = false
    }
  }, [getVersion])

  useEffect(() => {
    if (!open) return
    function onPointerDown(event: MouseEvent) {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  if (!info) return null

  const tone = envTone(info.environment)
  const buildTime = formatBuildTime(info.build_time)
  const versionLabel = formatVersionLabel(info.display_version || info.version)
  const summary = `${info.environment} · ${versionLabel}`

  return (
    <div className="build-badge" ref={ref}>
      <button
        type="button"
        className={`build-badge-pill ${tone}`}
        title={`APEX Load ${versionLabel} · ${info.environment}${buildTime ? ` · built ${buildTime}` : ''}`}
        aria-label={`Build info: ${summary}`}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="build-badge-dot" />
        <span className="build-badge-env">{info.environment}</span>
        <span className="build-badge-version">{versionLabel}</span>
      </button>
      {open && (
        <div className={`build-badge-popover ${placement}`} role="dialog" aria-label="Build details">
          <BuildDetail label="Version" value={versionLabel} />
          <BuildDetail label="Environment" value={info.environment} />
          {info.commit && <BuildDetail label="Commit" value={info.commit} mono />}
          {info.revision && info.revision !== info.commit && <BuildDetail label="Revision" value={info.revision} mono />}
          {buildTime && <BuildDetail label="Built" value={buildTime} />}
          {info.go_version && <BuildDetail label="Runtime" value={info.go_version} />}
        </div>
      )}
    </div>
  )
}

function BuildDetail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="build-badge-row">
      <span className="build-badge-row-label">{label}</span>
      <span className={`build-badge-row-value${mono ? ' mono' : ''}`}>{value}</span>
    </div>
  )
}
