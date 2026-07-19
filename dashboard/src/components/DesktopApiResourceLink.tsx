import { useEffect, useState, type ReactNode } from 'react'

import { isDesktopSurface } from '../desktop/desktopBridge'
import { saveApiResourceViaBridge, startDesktopBrowserDownload } from '../desktop/desktopFiles'
import { API_ORIGIN, apiFetch, hasDesktopApiCapability, resolveApiResourceUrl } from '../hooks/apiClient'
import { downloadBlob, downloadFilenameFromDisposition } from '../utils/fileUtils'

type DesktopApiResourceLinkProps = {
  href: string
  children: ReactNode
  className?: string
  title?: string
  target?: string
  rel?: string
  mode?: 'download' | 'open'
  filename: string
  onError?: (message: string) => void
}

function safeDownloadFilename(value: string): string {
  const basename = value.replace(/\\/g, '/').split('/').pop() || 'download'
  const sanitized = Array.from(basename.replace(/[<>:"|?*]/g, '_'))
    .map(character => character.charCodeAt(0) < 32 ? '_' : character)
    .join('')
  return sanitized.trim() || 'download'
}

function nativeAPIResourcePath(href: string): string | null {
  try {
    const resource = new URL(resolveApiResourceUrl(href))
    if (resource.origin !== new URL(API_ORIGIN).origin || !resource.pathname.startsWith('/api/v1/')) {
      return null
    }
    return `${resource.pathname}${resource.search}`
  } catch {
    return null
  }
}

// Normal browser dashboards retain ordinary links. Wails must fetch API
// resources through apiClient so the per-launch desktop capability travels in
// a request header; direct webview navigation cannot attach that header.
export default function DesktopApiResourceLink({
  href,
  children,
  className,
  title,
  target,
  rel,
  mode = 'download',
  filename,
  onError,
}: DesktopApiResourceLinkProps) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [openResource, setOpenResource] = useState<{ objectURL: string; filename: string; blob: Blob } | null>(null)

  useEffect(() => () => {
    if (openResource) URL.revokeObjectURL(openResource.objectURL)
  }, [openResource])

  // The desktop browser bootstrap has no native bridge, but it still carries a
  // capability in sessionStorage. It must mint a short-lived navigation URL;
  // an ordinary direct navigation cannot attach the capability header.
  if (!isDesktopSurface() && !hasDesktopApiCapability()) {
    return <a className={className} href={href} title={title} target={target} rel={rel}>{children}</a>
  }

  const open = async () => {
    if (pending) return
    setPending(true)
    setError('')
    try {
      const resourcePath = nativeAPIResourcePath(href)
      if (mode === 'download') {
        if (resourcePath) {
          const nativeOutcome = await saveApiResourceViaBridge(resourcePath, safeDownloadFilename(filename))
          if (nativeOutcome !== 'unavailable') return
        }
      }
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        if (!resourcePath) throw new Error('The desktop API resource URL is invalid.')
        await startDesktopBrowserDownload(resourcePath, safeDownloadFilename(filename), mode)
        return
      }
      const response = await apiFetch(resolveApiResourceUrl(href), { headers: { Accept: '*/*' } })
      if (!response.ok) throw new Error(`Download failed (${response.status} ${response.statusText || 'request failed'})`)
      const blob = await response.blob()
      if (mode === 'open') {
        const objectURL = URL.createObjectURL(blob)
        const resolvedName = safeDownloadFilename(downloadFilenameFromDisposition(response.headers.get('content-disposition'), filename))
        setOpenResource({ objectURL, filename: resolvedName, blob })
      } else {
        const resolvedName = safeDownloadFilename(downloadFilenameFromDisposition(response.headers.get('content-disposition'), filename))
        await downloadBlob(blob, resolvedName)
      }
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : 'The desktop API resource could not be downloaded.'
      setError(message)
      onError?.(message)
    } finally {
      setPending(false)
    }
  }

  const downloadPreview = async () => {
    if (!openResource) return
    setError('')
    try {
      await downloadBlob(openResource.blob, openResource.filename)
    } catch (cause: unknown) {
      const message = cause instanceof Error ? cause.message : 'The desktop API resource could not be downloaded.'
      setError(message)
      onError?.(message)
    }
  }

  return (
    <span>
      <button className={className} type="button" title={title} disabled={pending} onClick={() => { void open() }}>
        {pending ? 'Loading…' : children}
      </button>
      {error && !onError && <span className="scenario-script-status error" role="alert">{error}</span>}
      {openResource && (
        <div className="modal-overlay" role="presentation">
          <section className="modal-content desktop-api-resource-viewer" role="dialog" aria-modal="true" aria-label={`Preview ${openResource.filename}`}>
            <div className="modal-header">
              <h3>{openResource.filename}</h3>
              <div className="desktop-api-resource-viewer-actions">
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void downloadPreview() }}>Download</button>
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setOpenResource(null)}>Close</button>
              </div>
            </div>
            <iframe title={openResource.filename} src={openResource.objectURL} sandbox="allow-scripts" />
          </section>
        </div>
      )}
    </span>
  )
}
