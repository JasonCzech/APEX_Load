import { request } from '../apiClient'
import type { BrowserResourcePageQuery } from '../../types/browserResourcePage'

/** This approved archive read operation carries the active project's ordinary
 * authorization and a bounded response/deadline. It never contacts the target. */
export function readArchiveBrowserResourcePage(testID: string, projectID: string, query: BrowserResourcePageQuery, signal: AbortSignal): Promise<unknown> {
  const parameters = new URLSearchParams({ project_id: projectID })
  for (const [key, value] of Object.entries(query)) if (value !== undefined && value !== '') parameters.set(key, String(value))
  return request<unknown>(`/tests/${encodeURIComponent(testID)}/archive/browser-resources?${parameters}`, {
    signal, timeoutMs: 12_000, maxResponseBytes: 2 << 20, cache: 'no-store',
  })
}
