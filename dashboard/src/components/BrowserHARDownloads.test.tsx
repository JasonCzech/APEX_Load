// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const apiFetch = vi.fn()
const downloadBlob = vi.fn(async () => 'saved')
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), apiFetch: (...args: unknown[]) => apiFetch(...args), request: vi.fn() }))
vi.mock('../utils/fileUtils', () => ({ downloadBlob: (...args: unknown[]) => downloadBlob(...(args as [])), downloadFilenameFromDisposition: (_: string | null, fallback: string) => fallback }))

import BrowserHARDownloads from './BrowserHARDownloads'
import { ApiError } from '../hooks/apiClient'
import BrowserResourceInspector from './BrowserResourceInspector'
import { downloadBrowserReplayHAR, harForResourceRow, SYNTHETIC_HAR_NAME, type BrowserHARArchiveList } from '../hooks/api/useBrowserHARApi'
import { RESOURCE_GENERATION, resourcePageFixture } from './browserResourcePage.testFixtures'

const NATIVE = 'browser_har_' + 'a'.repeat(32) + '.har'
const list = (canDownload: boolean, evidenceGeneration = RESOURCE_GENERATION): BrowserHARArchiveList => ({
  version: 1, test_id: 'run-one', project_id: 'project-one', download_role: 'editor', can_download: canDownload, incomplete: true, omitted: { artifact_limit: 2 },
  artifacts: [
    { name: NATIVE, fidelity: 'native', kind: 'har_native', available: true, evidence_generation: evidenceGeneration, bytes: 1234, sha256: 'b'.repeat(64) },
    { name: SYNTHETIC_HAR_NAME, fidelity: 'synthetic', kind: 'har_synthetic', available: true, derived: true, capture_mode: 'retained_network_entries' },
  ],
})
const button = (node: HTMLElement, text: string) => Array.from(node.querySelectorAll('button')).find(element => element.textContent === text)
const okResponse = () => ({ ok: true, status: 200, headers: new Headers(), blob: async () => new Blob(['{}']), json: async () => ({}) })

afterEach(() => { apiFetch.mockReset(); downloadBlob.mockClear() })

describe('browser HAR downloads', () => {
  it('picks the native HAR of a row’s context, else the synthetic HAR', () => {
    expect(harForResourceRow(list(true), RESOURCE_GENERATION)?.name).toBe(NATIVE)
    expect(harForResourceRow(list(true), 'f'.repeat(32))?.name).toBe(SYNTHETIC_HAR_NAME)
    expect(harForResourceRow(list(true), undefined)?.name).toBe(SYNTHETIC_HAR_NAME)
    const unavailable = list(true); unavailable.artifacts[1].available = false
    expect(harForResourceRow(unavailable, 'f'.repeat(32))).toBeUndefined()
    expect(harForResourceRow(null, RESOURCE_GENERATION)).toBeUndefined()
  })

  it('lists HARs for viewers but lets only editors download', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    try {
      await act(async () => root.render(<BrowserHARDownloads testID="run-one" list={list(false)} />))
      expect(node.textContent).toContain('Downloading a HAR needs editor access')
      expect(node.textContent).toContain('2 artifact limit')
      expect(button(node, 'Download native HAR')?.disabled).toBe(true)
      expect(button(node, 'Download archive with native HARs')).toBeUndefined()
      apiFetch.mockResolvedValue(okResponse())
      await act(async () => root.render(<BrowserHARDownloads testID="run-one" list={list(true)} />))
      await act(async () => button(node, 'Download synthetic HAR')!.click())
      expect(apiFetch.mock.calls[0][0]).toBe(`/tests/run-one/archive/browser/har/${SYNTHETIC_HAR_NAME}`)
      expect(downloadBlob).toHaveBeenCalledOnce()
      await act(async () => button(node, 'Download archive with native HARs')!.click())
      expect(apiFetch.mock.calls[1][0]).toBe('/tests/run-one/archive?include=har')
      apiFetch.mockResolvedValue({ ok: false, status: 403, statusText: 'Forbidden', headers: new Headers(), json: async () => ({ error: 'browser HAR downloads require editor access to the project' }) })
      await act(async () => button(node, 'Download native HAR')!.click())
      const alert = node.querySelector('[role="alert"]')
      expect(alert?.querySelector('.ux-notice-title')?.textContent).toBe('The HAR could not be downloaded')
      expect(alert?.querySelector('details')?.textContent).toContain('require editor access')
      await act(async () => button(node, 'Download archive with native HARs')!.click())
      expect(node.querySelector('[role="alert"] .ux-notice-title')?.textContent).toBe('The run archive could not be downloaded')
    } finally { await act(async () => root.unmount()) }
  })

  it('reports a failed download from a resource row as a notice', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    apiFetch.mockResolvedValue({ ok: false, status: 503, statusText: 'Service Unavailable', headers: new Headers(), json: async () => ({ error: 'archive store unavailable' }) })
    try {
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-one" loadPage={async () => resourcePageFixture()} loadHARList={async () => list(true)} />))
      await act(async () => button(node, 'Download the native HAR holding this request')!.click())
      const alert = node.querySelector('[aria-label="Retained archive resource inspector"] > [role="alert"]')
      expect(alert?.querySelector('.ux-notice-title')?.textContent).toBe('The HAR could not be downloaded')
      expect(alert?.querySelector('details')?.textContent).toContain('archive store unavailable')
    } finally { await act(async () => root.unmount()) }
  })

  it('offers the HAR holding a retained resource from its row', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    apiFetch.mockResolvedValue(okResponse())
    try {
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-one" loadPage={async () => resourcePageFixture()} loadHARList={async () => list(true)} />))
      await act(async () => button(node, 'Download the native HAR holding this request')!.click())
      expect(apiFetch.mock.calls[0][0]).toBe(`/tests/run-one/archive/browser/har/${NATIVE}`)
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-two" loadPage={async () => resourcePageFixture()} loadHARList={async () => list(true, 'f'.repeat(32))} />))
      expect(button(node, 'Download the synthetic HAR holding this request')).toBeDefined()
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-three" loadPage={async () => resourcePageFixture()} loadHARList={async () => list(false)} />))
      expect(Array.from(node.querySelectorAll('button')).some(element => element.textContent?.includes('HAR holding this request'))).toBe(false)
    } finally { await act(async () => root.unmount()) }
  })

  it('says plainly when the saved archive is not available yet, checks again, and alerts on other list failures', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    let missingCalls = 0
    const missingThenListed = async () => { missingCalls++; if (missingCalls === 1) throw new ApiError('archive not found', 404); return list(true) }
    try {
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-one" loadPage={async () => resourcePageFixture()} loadHARList={missingThenListed} />))
      const hars = node.querySelector('[aria-label="Browser HARs"]')!
      expect(hars.querySelector('[role="alert"]')).toBeNull()
      expect(hars.querySelector('[role="note"]')?.textContent).toBe("This run's saved archive is not available yet (it may still be publishing), so its browser HARs cannot be listed yet. Check again")
      expect(hars.textContent).not.toContain('archive not found')
      // Once the archive is published, checking again lists its HARs.
      await act(async () => button(node, 'Check again')!.click())
      expect(missingCalls).toBe(2)
      expect(button(node, 'Download native HAR')).toBeDefined()
      // Any other list failure, including another 404, is a load failure:
      // plain title, the server's text behind Details, and a retry.
      let calls = 0
      const flaky = async () => { calls++; if (calls === 1) throw new ApiError('test not found', 404); return list(true) }
      await act(async () => root.render(<BrowserResourceInspector testID="run-one" projectID="project-one" sourceIdentity="source-two" loadPage={async () => resourcePageFixture()} loadHARList={flaky} />))
      const alert = node.querySelector('[aria-label="Browser HARs"] [role="alert"]')
      expect(alert?.textContent).toContain('Browser HARs could not be listed')
      expect(alert?.querySelector('details')?.textContent).toContain('test not found')
      await act(async () => button(node, 'Retry browser HARs')!.click())
      expect(calls).toBe(2)
      expect(node.querySelector('[aria-label="Browser HARs"] [role="alert"]')).toBeNull()
      expect(button(node, `Download native HAR`)).toBeDefined()
    } finally { await act(async () => root.unmount()) }
  })

  it('reports a failed download with the server reference', async () => {
    apiFetch.mockResolvedValue({ ok: false, status: 503, statusText: 'Service Unavailable', headers: new Headers({ 'X-Request-ID': 'req-123' }), json: async () => ({ error: 'internal server error' }) })
    await expect(downloadBrowserReplayHAR('replay-1')).rejects.toThrow('internal server error (ref req-123)')
    expect(downloadBlob).not.toHaveBeenCalled()
  })
})
