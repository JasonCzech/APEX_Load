// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ClientDownloadsMenu from './ClientDownloadsMenu'

const getVersionMock = vi.fn()
const apiFetchMock = vi.fn()
const downloadBlobMock = vi.fn()

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getVersion: getVersionMock }),
}))

vi.mock('../hooks/apiClient', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: (...args: unknown[]) => downloadBlobMock(...args),
  downloadFilenameFromDisposition: (_disposition: string | null, fallback: string) => fallback,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function deploymentVersion(clientDownloadsEnabled: boolean) {
  return {
    version: 'dev',
    environment: 'docker',
    deployment: {
      kind: 'docker',
      label: 'Docker',
      bind_scope: 'loopback',
      auth_enforced: false,
      desktop_available: false,
      client_downloads_enabled: clientDownloadsEnabled,
    },
  }
}

function okPackageResponse() {
  return {
    ok: true,
    headers: {
      get: (key: string) =>
        key.toLowerCase() === 'content-type'
          ? 'application/gzip'
          : 'attachment; filename="apexload-recording-agent-0.1.0-darwin-arm64.tar.gz"',
    },
    blob: async () => new Blob(['package']),
  }
}

const flush = () => act(async () => { await Promise.resolve() })

describe('ClientDownloadsMenu', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    getVersionMock.mockReset()
    apiFetchMock.mockReset()
    downloadBlobMock.mockReset()
    // Force a deterministic macOS platform detection.
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)',
      configurable: true,
    })
    Object.defineProperty(window.navigator, 'platform', {
      value: 'MacIntel',
      configurable: true,
    })
    Object.defineProperty(window.navigator, 'userAgentData', {
      value: {
        platform: 'macOS',
        getHighEntropyValues: vi.fn().mockResolvedValue({ platform: 'macOS', architecture: 'x86' }),
      },
      configurable: true,
    })
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const render = async () => {
    await act(async () => {
      root.render(<ClientDownloadsMenu />)
    })
    await flush()
  }

  const openPanel = async () => {
    const trigger = container.querySelector('.client-downloads-trigger') as HTMLButtonElement
    await act(async () => {
      trigger.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  it('renders nothing when client downloads are disabled', async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(false))
    await render()
    expect(container.querySelector('.client-downloads-trigger')).toBeNull()
    expect(container.textContent).not.toContain('Download client')
  })

  it('shows the trigger and lists both artifacts when enabled', async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true))
    await render()

    expect(container.querySelector('.client-downloads-trigger')).not.toBeNull()
    expect(container.querySelector('.client-downloads-panel')).toBeNull()

    await openPanel()

    expect(container.querySelector('.client-downloads-panel')).not.toBeNull()
    expect(container.textContent).toContain('Recording Agent')
    expect(container.textContent).toContain('Desktop App')
    expect(container.textContent).toContain('Download for macOS (Intel)')
  })

  it('downloads the recording agent for the detected platform', async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true))
    apiFetchMock.mockResolvedValue(okPackageResponse())
    await render()
    await openPanel()

    const primary = container
      .querySelectorAll('.client-downloads-item')[0]
      .querySelector('.btn-primary') as HTMLButtonElement
    await act(async () => {
      primary.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(apiFetchMock).toHaveBeenCalledWith('/downloads/recording-agent/latest?platform=darwin-amd64')
    expect(downloadBlobMock).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Recording Agent download started')
  })

  it('surfaces a server error when a package is unavailable', async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true))
    apiFetchMock.mockResolvedValue({
      ok: false,
      headers: { get: () => null },
      json: async () => ({ error: 'APEX Load Desktop package for darwin-amd64 is not staged.' }),
    })
    await render()
    await openPanel()

    const desktopPrimary = container
      .querySelectorAll('.client-downloads-item')[1]
      .querySelector('.btn-primary') as HTMLButtonElement
    await act(async () => {
      desktopPrimary.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(downloadBlobMock).not.toHaveBeenCalled()
    expect(container.querySelector('.client-downloads-status.error')?.textContent).toContain('is not staged')
  })

  it('falls back to the platform picker when macOS architecture is unknown', async () => {
    Object.defineProperty(window.navigator, 'userAgentData', {
      value: undefined,
      configurable: true,
    })
    getVersionMock.mockResolvedValue(deploymentVersion(true))
    await render()
    await openPanel()

    expect(container.textContent).not.toContain('Download for macOS (Apple Silicon)')
    expect(container.textContent).not.toContain('Download for macOS (Intel)')
    expect(container.textContent).toContain('Choose a platform:')
    expect(container.textContent).toContain('macOS (Apple Silicon)')
    expect(container.textContent).toContain('macOS (Intel)')
  })

  it('closes the panel on an outside click', async () => {
    getVersionMock.mockResolvedValue(deploymentVersion(true))
    await render()
    await openPanel()
    expect(container.querySelector('.client-downloads-panel')).not.toBeNull()

    await act(async () => {
      document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    })
    expect(container.querySelector('.client-downloads-panel')).toBeNull()
  })
})
