import { apiFetch, request } from '../apiClient'
import { downloadBlob, downloadFilenameFromDisposition } from '../../utils/fileUtils'

/** One downloadable HAR of a run archive (native, or synthetic and derived on request). */
export interface BrowserHARArchiveItem {
  name: string
  fidelity: 'native' | 'synthetic' | string
  kind: string
  available: boolean
  unavailable_reason?: string
  source_identity?: string
  source_generation?: string
  /** The browser evidence generation of the context a native HAR recorded. */
  evidence_generation?: string
  sequence?: number
  sha256?: string
  bytes?: number
  capture_mode?: string
  derived?: boolean
}

export interface BrowserHARArchiveList {
  version: number
  test_id: string
  project_id?: string
  artifacts: BrowserHARArchiveItem[]
  incomplete: boolean
  omitted?: Record<string, number>
  download_role: string
  can_download: boolean
}

export const SYNTHETIC_HAR_NAME = 'browser_har_synthetic.har'
const harDownloadTimeoutMs = 10 * 60 * 1000

export function listArchiveBrowserHARs(testID: string, signal?: AbortSignal): Promise<BrowserHARArchiveList> {
  return request<BrowserHARArchiveList>(`/tests/${encodeURIComponent(testID)}/archive/browser/har`, { signal, timeoutMs: 12_000, cache: 'no-store' })
}

async function saveDownload(path: string, fallbackName: string) {
  const response = await apiFetch(path, { timeoutMs: harDownloadTimeoutMs, cache: 'no-store' })
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText })) as { error?: string }
    throw new Error(body.error || response.statusText || `Download failed (${response.status})`)
  }
  const blob = await response.blob()
  await downloadBlob(blob, downloadFilenameFromDisposition(response.headers.get('Content-Disposition'), fallbackName))
}

export function downloadArchiveBrowserHAR(testID: string, name: string) {
  return saveDownload(`/tests/${encodeURIComponent(testID)}/archive/browser/har/${encodeURIComponent(name)}`, name)
}

/** The run's safe archive export with its native HARs kept (editors only). */
export function downloadArchiveWithHARs(testID: string) {
  return saveDownload(`/tests/${encodeURIComponent(testID)}/archive?include=har`, `${testID}-with-har.zip`)
}

export function downloadBrowserReplayHAR(sessionID: string, evidence = false) {
  const path = `/browser-replay/sessions/${encodeURIComponent(sessionID)}/${evidence ? 'evidence' : 'har'}`
  return saveDownload(path, evidence ? `replay-${sessionID}-evidence.zip` : `replay-${sessionID}.har`)
}

/** The HAR holding a retained resource row: the native HAR recorded by the
 * row's browser context when there is one, else the synthetic HAR. */
export function harForResourceRow(list: BrowserHARArchiveList | null | undefined, sourceGeneration?: string): BrowserHARArchiveItem | undefined {
  if (!list) return undefined
  const native = sourceGeneration ? list.artifacts.find(item => item.fidelity === 'native' && item.available && item.evidence_generation === sourceGeneration) : undefined
  return native ?? list.artifacts.find(item => item.name === SYNTHETIC_HAR_NAME && item.available)
}
