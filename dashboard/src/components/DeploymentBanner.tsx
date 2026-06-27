import { useEffect, useMemo, useRef, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import { isDesktopSurface } from '../desktop/desktopBridge'
import type { DeploymentIdentity, VersionResponse } from '../types'
import './DeploymentBanner.css'

const WIDE_BYPASS_HELP_ID = 'deployment-wide-bypass-help'

function deploymentLabel(deployment?: DeploymentIdentity, environment?: string): string {
  const kind = deployment?.kind || ''
  if (kind === 'kubernetes') return 'Hosted K8s'
  if (kind === 'desktop') return 'Local engine'
  if (deployment?.label) return deployment.label
  return environment || 'Server'
}

function wideBypassDetail(deployment: DeploymentIdentity, environment?: string): string {
  const kind = deployment.kind.toLowerCase()
  const env = (environment || '').toLowerCase()
  if (kind === 'docker' || kind === 'container' || env === 'docker' || env === 'container') {
    return 'The controller is listening on all container interfaces while auth is in dev bypass mode. Docker Compose publishes the host port on 127.0.0.1 by default, so this is expected for local Docker.'
  }
  return 'The controller is reachable beyond loopback while auth is in dev bypass mode. This is useful for local development, but should not be exposed to a shared network.'
}

export default function DeploymentBanner() {
  const { getVersion } = useSystemApi()
  const [version, setVersion] = useState<VersionResponse | null>(null)
  const [wideBypassHelpOpen, setWideBypassHelpOpen] = useState(false)
  const wideBypassRef = useRef<HTMLDivElement | null>(null)

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
    if (!wideBypassHelpOpen) return
    const closeOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && wideBypassRef.current?.contains(target)) {
        return
      }
      setWideBypassHelpOpen(false)
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setWideBypassHelpOpen(false)
      }
    }
    const closeOnResize = () => setWideBypassHelpOpen(false)
    window.addEventListener('pointerdown', closeOnOutsidePointerDown)
    window.addEventListener('resize', closeOnResize)
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      window.removeEventListener('pointerdown', closeOnOutsidePointerDown)
      window.removeEventListener('resize', closeOnResize)
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [wideBypassHelpOpen])

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
        <div className="deployment-banner-warning-wrap" ref={wideBypassRef}>
          <button
            type="button"
            className="deployment-banner-warning"
            title="Controller is reachable beyond loopback while auth is bypassed. Click for details."
            aria-label="Explain wide unauthenticated warning"
            aria-expanded={wideBypassHelpOpen}
            aria-controls={WIDE_BYPASS_HELP_ID}
            onClick={() => setWideBypassHelpOpen(open => !open)}
          >
            Wide unauthenticated
            <span className="deployment-banner-help-mark" aria-hidden="true">?</span>
          </button>
          {wideBypassHelpOpen && (
            <div id={WIDE_BYPASS_HELP_ID} className="deployment-banner-help" role="status" aria-live="polite">
              <div className="deployment-banner-help-title">Why this appears</div>
              <p>{wideBypassDetail(deployment, version.environment)}</p>
              <p>Configure OIDC or trusted-header auth before exposing the controller beyond your machine.</p>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
