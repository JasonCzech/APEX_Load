import { useEffect, useMemo, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import type { DeploymentIdentity, VersionResponse } from '../types'
import './DeploymentBanner.css'

function isDesktopSurface(): boolean {
  if (typeof window === 'undefined') return false
  if (window.location.protocol === 'wails:' || window.location.hostname === 'wails.localhost') return true
  return Boolean((window as unknown as { go?: { main?: { App?: unknown } } }).go?.main?.App)
}

function deploymentLabel(deployment?: DeploymentIdentity, environment?: string): string {
  const kind = deployment?.kind || ''
  if (kind === 'kubernetes') return 'Hosted K8s'
  if (kind === 'local-server') return 'Local server'
  if (kind === 'desktop') return 'Local engine'
  if (deployment?.label) return deployment.label
  return environment || 'Server'
}

export default function DeploymentBanner() {
  const { getVersion } = useSystemApi()
  const [version, setVersion] = useState<VersionResponse | null>(null)

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

  const desktopSurface = useMemo(() => isDesktopSurface(), [])
  const deployment = version?.deployment
  if (!version || !deployment) return null

  const label = desktopSurface
    ? `Desktop · ${deployment.kind === 'desktop' ? 'Local engine' : deploymentLabel(deployment, version.environment)}`
    : `Browser · ${deploymentLabel(deployment, version.environment)}`
  const warnWideBypass = deployment.bind_scope === 'wide' && !deployment.auth_enforced
  const showDesktopPromotion = !desktopSurface && deployment.desktop_available

  return (
    <div className="deployment-banner" aria-label="Deployment identity">
      <span className={`deployment-banner-chip ${deployment.kind}`}>
        <span className="deployment-banner-dot" aria-hidden="true" />
        {label}
      </span>
      {showDesktopPromotion && (
        <span className="deployment-banner-promotion" title="Optional desktop local workspace is available for this environment">
          Desktop available
        </span>
      )}
      {warnWideBypass && (
        <span className="deployment-banner-warning" title="Controller is reachable beyond loopback while auth is bypassed">
          Wide unauthenticated
        </span>
      )}
    </div>
  )
}
