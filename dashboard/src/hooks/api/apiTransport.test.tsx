// @vitest-environment jsdom
//
// Transport-layer tests for the domain hooks.
//
// Every other suite that touches these hooks replaces them with vi.fn()s, so
// nothing exercised the layer that actually builds a request: the URL, the
// method, the body, the headers, how a failure response becomes an ApiError,
// and whether cancellation reaches fetch. These tests mock at the network
// boundary (global fetch) and run the real hook + apiClient code above it.

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

import { API_ORIGIN, ApiError } from '../apiClient'
import { useFleetApi } from './useFleetApi'
import { useProjectApi } from './useProjectApi'
import { SYSTEM_READINESS_REQUEST_TIMEOUT_MS, useSystemApi } from './useSystemApi'
import { useTestApi } from './useTestApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const BASE = `${API_ORIGIN}/api/v1`

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

type Api = {
  tests: ReturnType<typeof useTestApi>
  fleet: ReturnType<typeof useFleetApi>
  projects: ReturnType<typeof useProjectApi>
  system: ReturnType<typeof useSystemApi>
}

let api: Api
let container: HTMLDivElement
let root: Root
let fetchMock: Mock

function lastCall(): [string, RequestInit] {
  const call = fetchMock.mock.calls[fetchMock.mock.calls.length - 1]
  return [call[0] as string, (call[1] ?? {}) as RequestInit]
}

function headerEntries(headers?: HeadersInit): Record<string, string> {
  const out: Record<string, string> = {}
  new Headers(headers).forEach((value, key) => {
    out[key] = value
  })
  return out
}

function Capture() {
  api = { tests: useTestApi(), fleet: useFleetApi(), projects: useProjectApi(), system: useSystemApi() }
  return null
}

beforeEach(async () => {
  fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({})))
  vi.stubGlobal('fetch', fetchMock)
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => {
    root.render(<Capture />)
  })
})

afterEach(async () => {
  await act(async () => {
    root.unmount()
  })
  container.remove()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('request construction', () => {
  it('builds a GET url with the query the caller asked for', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ tests: [], count: 0 }))
    await api.tests.listTests({ project_id: 'p-1', folder_id: null, limit: 10, offset: 20 })

    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/tests?project_id=p-1&folder_id=&limit=10&offset=20`)
    expect(init.method).toBeUndefined()
    expect(init.body).toBeUndefined()
    expect(init.credentials).toBe('include')
    expect(headerEntries(init.headers)['content-type']).toBe('application/json')
  })

  it('drops undefined query parameters instead of serialising them', async () => {
    await api.tests.getTest('t-1')
    expect(lastCall()[0]).toBe(`${BASE}/tests/t-1`)

    await api.tests.getTest('t-1', { includeHistory: true, limit: 5 })
    expect(lastCall()[0]).toBe(`${BASE}/tests/t-1?include=history&limit=5`)
  })

  it('joins array query parameters with commas', async () => {
    await api.tests.getMetrics('t-1', { groups: ['checkout', 'browse'], since: '2026-08-01T00:00:00Z' })
    expect(lastCall()[0]).toBe(
      `${BASE}/tests/t-1/metrics?groups=checkout%2Cbrowse&since=2026-08-01T00%3A00%3A00Z`,
    )
  })

  it('percent-encodes path segments that can contain separators', async () => {
    await api.tests.getTestGroupParameters('t-1', 'group/one two')
    expect(lastCall()[0]).toBe(`${BASE}/tests/t-1/groups/group%2Fone%20two/parameters`)
  })

  it('sends the method and JSON body for a mutation', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 't-9' }))
    await api.tests.createTest({ name: 'smoke', groups: [] } as never)

    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/tests`)
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ name: 'smoke', groups: [] })
    expect(headerEntries(init.headers)['content-type']).toBe('application/json')
  })

  it('omits the body when a start has no override options', async () => {
    await api.tests.startTest('t-1')
    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/tests/t-1/start`)
    expect(init.method).toBe('POST')
    expect(init.body).toBeUndefined()

    await api.tests.startTest('t-1', { override_capacity_shortfall: true })
    expect(JSON.parse(String(lastCall()[1].body))).toEqual({ override_capacity_shortfall: true })
  })

  it('carries the dev-preview view header on controller requests', async () => {
    await api.fleet.listFleetPools({ limit: 25 })
    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/fleet/pools?limit=25`)
    expect(headerEntries(init.headers)['x-apexload-dev-view']).toBe('admin')
  })

  it('routes other domain hooks through the same client', async () => {
    await api.projects.listProjects({ domain_id: 'd-1' })
    expect(lastCall()[0]).toBe(`${BASE}/projects?domain_id=d-1`)
  })
})

describe('response handling', () => {
  it('returns the decoded JSON payload', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ tests: [{ id: 't-1' }], count: 1 }))
    await expect(api.tests.listTests()).resolves.toEqual({ tests: [{ id: 't-1' }], count: 1 })
  })

  it('resolves undefined for a 204', async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await expect(api.tests.deleteTest('t-1')).resolves.toBeUndefined()
  })

  it('normalises an error response into ApiError with status, code and payload', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: 'capacity shortfall', code: 'capacity_shortfall', shortfall: 4 }, 409),
    )
    const error = await api.tests.startTest('t-1').catch((err: unknown) => err)
    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.message).toBe('capacity shortfall')
    expect(apiError.status).toBe(409)
    expect(apiError.code).toBe('capacity_shortfall')
    expect(apiError.payload).toEqual({
      error: 'capacity shortfall',
      code: 'capacity_shortfall',
      shortfall: 4,
    })
  })

  it('falls back to the status text when the error body has no message', async () => {
    fetchMock.mockResolvedValueOnce(new Response('', { status: 503, statusText: 'Service Unavailable' }))
    const error = (await api.tests.getTest('t-1').catch((err: unknown) => err)) as ApiError
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(503)
    expect(error.message).toBe('Service Unavailable')
  })

  it('accepts the validated readiness document carried by an HTTP 503 response', async () => {
    const readiness = {
      status: 'not_ready',
      generated_at: '2026-08-11T18:30:00Z',
      checks: [
        {
          name: 'metadata_store',
          status: 'not_ready',
          message: 'metadata store probe failed',
          details: { store_type: 'postgres', reason: 'probe_failed' },
        },
      ],
    }
    fetchMock.mockResolvedValueOnce(jsonResponse(readiness, 503))

    await expect(api.system.getSystemReadiness()).resolves.toEqual(readiness)
    expect(lastCall()[0]).toBe(`${BASE}/system/readiness`)
  })

  it('rejects an unrelated or malformed HTTP 503 readiness response', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: 'upstream unavailable' }, 503))

    const error = await api.system.getSystemReadiness().catch((err: unknown) => err)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(503)
  })

  it('reports dashboard HTML served in place of JSON as a transport error', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response('<!doctype html><html><body>dashboard</body></html>', {
        status: 200,
        headers: { 'content-type': 'text/html' },
      }),
    )
    const error = (await api.tests.getTest('t-1').catch((err: unknown) => err)) as ApiError
    expect(error).toBeInstanceOf(ApiError)
    expect(error.message).toContain('dashboard HTML instead of JSON')
  })
})

describe('caller-supplied transport options', () => {
  it('passes the caller abort signal through to fetch', async () => {
    const controller = new AbortController()
    let seen: AbortSignal | undefined
    fetchMock.mockImplementationOnce((_url: string, init: RequestInit) => {
      seen = init.signal ?? undefined
      return new Promise((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
      })
    })

    const pending = api.tests.getTest('t-1', undefined, { signal: controller.signal })
    const settled = pending.catch((err: unknown) => err)
    expect(seen).toBeDefined()
    expect(seen?.aborted).toBe(false)

    controller.abort()
    const error = (await settled) as DOMException
    expect(seen?.aborted).toBe(true)
    expect(error.name).toBe('AbortError')
  })

  it('applies a caller deadline as a hard timeout', async () => {
    fetchMock.mockImplementationOnce(() => new Promise(() => {}))
    await expect(api.tests.getTestArchiveReport('t-1', { timeoutMs: 5 })).rejects.toThrow(
      'Request timed out after 5ms',
    )
  })

  it('bounds readiness requests below the monitoring poll interval', async () => {
    vi.useFakeTimers()
    try {
      fetchMock.mockImplementationOnce((_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
      }))

      const settled = api.system.getSystemReadiness().catch((error: unknown) => error)
      expect(SYSTEM_READINESS_REQUEST_TIMEOUT_MS).toBeLessThan(5_000)
      await vi.advanceTimersByTimeAsync(SYSTEM_READINESS_REQUEST_TIMEOUT_MS)

      await expect(settled).resolves.toMatchObject({
        message: `Request timed out after ${SYSTEM_READINESS_REQUEST_TIMEOUT_MS}ms`,
      })
      expect(lastCall()[1].signal?.aborted).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it('times out readiness while a response body remains open after headers arrive', async () => {
    vi.useFakeTimers()
    let bodyController: ReadableStreamDefaultController<Uint8Array> | undefined
    try {
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          bodyController = controller
          controller.enqueue(new TextEncoder().encode('{"status":"not_ready"'))
        },
      })
      fetchMock.mockResolvedValueOnce(new Response(body, {
        status: 503,
        statusText: 'Service Unavailable',
        headers: { 'content-type': 'application/json' },
      }))

      const settled = api.system.getSystemReadiness().catch((error: unknown) => error)
      await vi.advanceTimersByTimeAsync(SYSTEM_READINESS_REQUEST_TIMEOUT_MS)

      await expect(settled).resolves.toMatchObject({
        message: `Request timed out after ${SYSTEM_READINESS_REQUEST_TIMEOUT_MS}ms`,
      })
      expect(lastCall()[1].signal?.aborted).toBe(true)
    } finally {
      bodyController?.close()
      vi.useRealTimers()
    }
  })

  it('keeps the hook body and method when the caller adds headers', async () => {
    await api.tests.createTestNote('t-1', 'looks slow', { headers: { 'X-Trace': 'note-1' } })
    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/tests/t-1/notes`)
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ body: 'looks slow' })
    const headers = headerEntries(init.headers)
    expect(headers['x-trace']).toBe('note-1')
    expect(headers['content-type']).toBe('application/json')
  })

  it('threads options through blob downloads too', async () => {
    fetchMock.mockResolvedValueOnce(new Response('id,ms\n', { status: 200 }))
    const controller = new AbortController()
    const blob = await api.tests.downloadTestReportFormat('t-1', 'exec', 'csv', {
      signal: controller.signal,
    })
    expect(blob).toMatchObject({ size: 6, type: 'text/plain;charset=utf-8' })
    await expect(blob.text()).resolves.toBe('id,ms\n')
    const [url, init] = lastCall()
    expect(url).toBe(`${BASE}/tests/t-1/archive/report?format=csv&profile=exec`)
    expect(init.signal).toBe(controller.signal)
  })

  it('downloads DOCX from the profile-shaped archive report endpoint', async () => {
    fetchMock.mockResolvedValueOnce(new Response('PK', { status: 200 }))
    const blob = await api.tests.downloadTestReportFormat('t-1', 'audit', 'docx')
    expect(blob).toMatchObject({ size: 2, type: 'text/plain;charset=utf-8' })
    await expect(blob.text()).resolves.toBe('PK')
    const [url] = lastCall()
    expect(url).toBe(`${BASE}/tests/t-1/archive/report?format=docx&profile=audit`)
  })

  it('surfaces a failed download as an error carrying the server message', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: 'archive missing' }, 404))
    await expect(api.tests.downloadTestArchive('t-1')).rejects.toThrow('archive missing')
  })

  it('ignores a stray non-object trailing argument', async () => {
    // `ids.map(api.getTestArchiveReport)` passes the array index in the options
    // slot. TypeScript rejects that spelling outright, so the cast reproduces
    // what an untyped call site (an event handler, a .map in plain JS) does.
    const getReport = api.tests.getTestArchiveReport as unknown as (
      id: string,
      index: number,
    ) => Promise<unknown>
    await Promise.all(['t-1', 't-2'].map(getReport))
    expect(fetchMock.mock.calls.map(call => call[0])).toEqual([
      `${BASE}/tests/t-1/archive/report`,
      `${BASE}/tests/t-2/archive/report`,
    ])
    expect(lastCall()[1].method).toBeUndefined()
  })
})
