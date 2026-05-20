// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  DEV_VIEW_STORAGE_KEY,
  apiFetch,
  buildDevPreviewHeaders,
  readStoredDevViewMode,
  request,
  resolveApiOrigin,
  writeStoredDevViewMode,
} from './apiClient'

describe('resolveApiOrigin', () => {
  it('uses an explicit API origin when configured', () => {
    expect(resolveApiOrigin(
      { VITE_API_ORIGIN: 'http://api.example.test:8080/' },
      { protocol: 'http:', hostname: 'localhost', port: '3000', origin: 'http://localhost:3000' },
    )).toBe('http://api.example.test:8080')
  })

  it('uses the same-origin Vite proxy for local dev dashboards', () => {
    expect(resolveApiOrigin(
      { DEV: true },
      { protocol: 'http:', hostname: 'localhost', port: '3031', origin: 'http://localhost:3031' },
    )).toBe('http://localhost:3031')
  })

  it('uses the same-origin proxy for localhost port 3000 even when DEV metadata is absent', () => {
    expect(resolveApiOrigin(
      {},
      { protocol: 'http:', hostname: '127.0.0.1', port: '3000', origin: 'http://127.0.0.1:3000' },
    )).toBe('http://127.0.0.1:3000')
  })

  it('falls back to the local controller for file URLs', () => {
    expect(resolveApiOrigin(
      { DEV: true },
      { protocol: 'file:', hostname: '', port: '', origin: 'null' },
    )).toBe('http://127.0.0.1:8080')
  })

  it('uses the serving origin for embedded controller dashboard assets', () => {
    expect(resolveApiOrigin(
      {},
      { protocol: 'http:', hostname: 'apexload.example.test', port: '', origin: 'https://apexload.example.test' },
    )).toBe('https://apexload.example.test')
  })
})

describe('dev preview API headers', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    window.localStorage.clear()
  })

  it('defaults to admin mode and includes the active project id when present', () => {
    window.localStorage.setItem('apexload.activeProjectId', 'project-1')

    expect(readStoredDevViewMode()).toBe('admin')
    expect(buildDevPreviewHeaders()).toEqual({
      'X-APEXLoad-Dev-View': 'admin',
      'X-APEXLoad-Dev-Project-ID': 'project-1',
    })
  })

  it('persists user mode for subsequent API calls', () => {
    writeStoredDevViewMode('user')

    expect(window.localStorage.getItem(DEV_VIEW_STORAGE_KEY)).toBe('user')
    expect(buildDevPreviewHeaders()['X-APEXLoad-Dev-View']).toBe('user')
  })

  it('attaches preview headers to FormData and blob-style fetches without forcing JSON content type', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    writeStoredDevViewMode('user')
    window.localStorage.setItem('apexload.activeProjectId', 'project-2')

    await apiFetch('/scripts/import/loadrunner', { method: 'POST', body: new FormData() })

    const init = fetchMock.mock.calls[0][1] as RequestInit
    const headers = init.headers as Headers
    expect(fetchMock.mock.calls[0][0]).toContain('/api/v1/scripts/import/loadrunner')
    expect(headers.get('X-APEXLoad-Dev-View')).toBe('user')
    expect(headers.get('X-APEXLoad-Dev-Project-ID')).toBe('project-2')
    expect(headers.has('Content-Type')).toBe(false)
  })

  it('attaches preview headers to JSON requests', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"ok":true}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    await request<{ ok: boolean }>('/auth/me')

    const init = fetchMock.mock.calls[0][1] as RequestInit
    const headers = init.headers as Headers
    expect(headers.get('X-APEXLoad-Dev-View')).toBe('admin')
    expect(headers.get('Content-Type')).toBe('application/json')
  })
})
