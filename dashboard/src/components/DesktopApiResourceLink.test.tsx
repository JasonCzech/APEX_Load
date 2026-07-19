// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WailsDesktopBridge } from '../desktop/desktopBridge'
import { setDesktopApiCapability } from '../hooks/apiClient'
import DesktopApiResourceLink from './DesktopApiResourceLink'

const { downloadBlobMock, saveApiResourceViaBridgeMock, startDesktopBrowserDownloadMock } = vi.hoisted(() => ({
  downloadBlobMock: vi.fn(),
  saveApiResourceViaBridgeMock: vi.fn(),
  startDesktopBrowserDownloadMock: vi.fn(),
}))
vi.mock('../utils/fileUtils', async importOriginal => {
  const original = await importOriginal<typeof import('../utils/fileUtils')>()
  return { ...original, downloadBlob: downloadBlobMock }
})
vi.mock('../desktop/desktopFiles', () => ({
  saveApiResourceViaBridge: (...args: unknown[]) => saveApiResourceViaBridgeMock(...args),
  startDesktopBrowserDownload: (...args: unknown[]) => startDesktopBrowserDownloadMock(...args),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

describe('DesktopApiResourceLink', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    downloadBlobMock.mockReset()
    saveApiResourceViaBridgeMock.mockReset()
    saveApiResourceViaBridgeMock.mockResolvedValue('unavailable')
    startDesktopBrowserDownloadMock.mockReset()
    startDesktopBrowserDownloadMock.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    delete (window as BridgeWindow).go
    setDesktopApiCapability('')
    vi.restoreAllMocks()
  })

  it('retains a normal API anchor in the hosted browser', async () => {
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/report" filename="report.html">Report</DesktopApiResourceLink>,
    ))

    expect(container.querySelector('a')?.getAttribute('href')).toBe('/api/v1/tests/run-1/report')
    expect(container.querySelector('button')).toBeNull()
  })

  it('replaces direct Wails navigation with a capability-bearing fetch and blob fallback', async () => {
    ;(window as BridgeWindow).go = { main: { App: {} } }
    setDesktopApiCapability('launch-secret')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('report', {
      status: 200,
      headers: { 'Content-Disposition': 'attachment; filename="server-report.json"' },
    }))
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/archive/report" filename="fallback.json">Report JSON</DesktopApiResourceLink>,
    ))

    expect(container.querySelector('a')).toBeNull()
    await act(async () => { container.querySelector('button')?.click() })

    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect((init.headers as Headers).get('X-APEXLoad-Desktop-Token')).toBe('launch-secret')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), 'server-report.json')
  })

  it('streams Wails downloads through the native API save bridge', async () => {
    ;(window as BridgeWindow).go = { main: { App: {} } }
    setDesktopApiCapability('launch-secret')
    saveApiResourceViaBridgeMock.mockResolvedValue('saved')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/archive/report?format=pdf" filename="run-1.pdf">PDF report</DesktopApiResourceLink>,
    ))

    await act(async () => { container.querySelector('button')?.click() })

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      '/api/v1/tests/run-1/archive/report?format=pdf',
      'run-1.pdf',
    )
    expect(fetchMock).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
  })

  it('stops after a cancelled native API save instead of fetching a browser blob', async () => {
    ;(window as BridgeWindow).go = { main: { App: {} } }
    setDesktopApiCapability('launch-secret')
    saveApiResourceViaBridgeMock.mockResolvedValue('cancelled')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/archive/report" filename="run-1.pdf">PDF report</DesktopApiResourceLink>,
    ))

    await act(async () => { container.querySelector('button')?.click() })

    expect(fetchMock).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
  })

  it('streams through a short-lived navigation URL in the desktop bootstrap browser', async () => {
    setDesktopApiCapability('browser-session-secret')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/archive/report" filename="fallback.csv">CSV report</DesktopApiResourceLink>,
    ))

    expect(container.querySelector('a')).toBeNull()
    await act(async () => { container.querySelector('button')?.click() })

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      '/api/v1/tests/run-1/archive/report',
      'fallback.csv',
    )
    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      '/api/v1/tests/run-1/archive/report',
      'fallback.csv',
      'download',
    )
    expect(fetchMock).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
  })

  it('opens a short-lived navigation URL in a new tab from the desktop bootstrap browser', async () => {
    setDesktopApiCapability('browser-session-secret')
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/report" filename="fallback.html" mode="open">HTML report</DesktopApiResourceLink>,
    ))

    await act(async () => { container.querySelector('button')?.click() })

    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      '/api/v1/tests/run-1/report',
      'fallback.html',
      'open',
    )
    expect(fetchMock).not.toHaveBeenCalled()
    expect(downloadBlobMock).not.toHaveBeenCalled()
  })

  it('opens fetched desktop reports in a sandboxed in-app viewer', async () => {
    ;(window as BridgeWindow).go = { main: { App: {} } }
    setDesktopApiCapability('launch-secret')
    const createObjectURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:report-preview')
    const revokeObjectURL = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
    const windowOpen = vi.spyOn(window, 'open').mockReturnValue(null)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('<html><body>report</body></html>', {
      status: 200,
      headers: { 'Content-Disposition': 'inline; filename="run-report.html"' },
    }))
    await act(async () => root.render(
      <DesktopApiResourceLink href="/api/v1/tests/run-1/report" filename="fallback.html" mode="open">HTML report</DesktopApiResourceLink>,
    ))

    await act(async () => { container.querySelector('button')?.click() })

    const frame = document.body.querySelector('iframe[title="run-report.html"]') as HTMLIFrameElement | null
    expect(frame?.getAttribute('src')).toBe('blob:report-preview')
    expect(frame?.getAttribute('sandbox')).toBe('allow-scripts')
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(windowOpen).not.toHaveBeenCalled()

    const close = Array.from(document.body.querySelectorAll('button')).find(button => button.textContent === 'Close')
    await act(async () => { close?.click() })
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:report-preview')
  })
})
