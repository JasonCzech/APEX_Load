// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

vi.mock('../apiClient', () => ({
  apiFetch: vi.fn(),
  request: vi.fn(),
}))

import { apiFetch, request } from '../apiClient'
import { useScriptApi } from './useScriptApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let api: ReturnType<typeof useScriptApi>
function Capture() {
  api = useScriptApi()
  return null
}

describe('useScriptApi import wrappers', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    ;(apiFetch as Mock).mockResolvedValue({ ok: true, json: async () => ({ ok: true }) })
    ;(request as Mock).mockResolvedValue({ ok: true })
    await act(async () => {
      root.render(<Capture />)
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('sends OpenAPI project scope before file bytes for streaming authorization', async () => {
    const file = new File(['openapi: 3.0.3'], 'api.yaml', { type: 'text/yaml' })
    await api.importOpenAPI(file, { project_id: 'project-1', name: 'Orders', agentforce_transform: true })
    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/openapi?agentforce_transform=true')
    const body = options.body as FormData
    expect(Array.from(body.keys())).toEqual(['project_id', 'file', 'script_name'])
    expect(body.get('project_id')).toBe('project-1')
    expect(body.get('file')).toBe(file)
  })

  it('posts k6 imports as multipart with script_name/base_url/project_id', async () => {
    const file = new File(['import http from "k6/http";'], 'script.js', { type: 'application/javascript' })
    await api.importK6(file, { project_id: 'p1', base_url: 'https://api.example.test', name: 'My k6' })

    expect(apiFetch).toHaveBeenCalledTimes(1)
    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/k6')
    expect(options.method).toBe('POST')
    const body = options.body as FormData
    expect(body.get('script_name')).toBe('My k6')
    expect(body.get('base_url')).toBe('https://api.example.test')
    expect(body.get('project_id')).toBe('p1')
    expect(body.get('file')).toBeInstanceOf(File)
  })

  it('posts CloudTest exports as multipart and fetches the fixture-gated support matrix', async () => {
    const file = new File(['<Repository/>'], 'repository.xml', { type: 'application/xml' })
    await api.importCloudTest(file, { project_id: 'p1' })

    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/cloudtest')
    expect(options.method).toBe('POST')
    expect((options.body as FormData).get('project_id')).toBe('p1')
    expect((options.body as FormData).get('file')).toBe(file)

    await api.getCloudTestSupportMatrix()
    expect(request).toHaveBeenCalledWith('/scripts/import/cloudtest/support-matrix')
  })

  it('posts Event Hubs Capture imports with event_hub/max_events query params', async () => {
    const file = new File(['avro'], 'capture.avro')
    await api.importEventHubsCapture(file, { project_id: 'p1', event_hub: 'orders', max_events: 5 })

    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/eventhubs-capture?event_hub=orders&max_events=5')
    expect((options.body as FormData).get('project_id')).toBe('p1')
  })

  it('posts Broker Config imports with format/max_destinations query params', async () => {
    const file = new File(['DEFINE QLOCAL(ORDERS)'], 'broker.mqsc')
    await api.importBrokerConfig(file, { project_id: 'p1', format: 'mqsc', max_destinations: 3 })

    const [url] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/broker-config?format=mqsc&max_destinations=3')
  })

  it('sends native Bruno output mode inside the additive inspect options JSON', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    await api.inspectBrunoImport([file], {
      project_id: 'p1',
      output_mode: 'native_requests',
    })

    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/bruno/inspect?project_id=p1')
    const body = options.body as FormData
    expect(JSON.parse(String(body.get('options')))).toEqual({ output_mode: 'native_requests' })
    expect(body.get('file')).toBeInstanceOf(File)
  })

  it('normalizes Windows directory upload paths in Bruno multipart fields', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    Object.defineProperty(file, 'webkitRelativePath', {
      configurable: true,
      value: 'CTA_Collection\\opencollection.yml',
    })
    await api.inspectBrunoImport([file], { project_id: 'p1', output_mode: 'native_requests' })

    const [, options] = (apiFetch as Mock).mock.calls[0]
    const body = options.body as FormData
    expect(body.get('file:CTA_Collection%2Fopencollection.yml')).toBeInstanceOf(File)
  })

  it('merges canonical Bruno run settings and output mode into one inspect options object', async () => {
    const file = new File(['meta { name: Demo }'], 'request.bru', { type: 'text/plain' })
    await api.inspectBrunoImport([file], {
      project_id: 'p1',
      output_mode: 'canonical',
      run_options: {
        selection: 'collection',
        recursive: true,
      },
    })

    const [, options] = (apiFetch as Mock).mock.calls[0]
    expect(JSON.parse(String((options.body as FormData).get('options')))).toEqual({
      output_mode: 'canonical',
      selection: 'collection',
      recursive: true,
    })
  })

  it('retains the Bruno commit HTTP status as private receipt metadata', async () => {
    ;(apiFetch as Mock).mockResolvedValueOnce({
      ok: true,
      status: 201,
      json: async () => ({
        batch_id: 'batch-1',
        created: true,
        idempotent_replay: false,
        count: 1,
        scripts: [{ id: 'script-1' }],
      }),
    })

    const result = await api.commitBrunoImport({
      project_id: 'p1',
      import_session_id: 'session-1',
      mode: 'create_native_batch',
      folder_id: 'folder-1',
    })

    expect(apiFetch).toHaveBeenCalledWith('/scripts/import/bruno/commit', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    }))
    expect(result).toEqual(expect.objectContaining({
      batch_id: 'batch-1',
      _http_status: 201,
    }))
  })

  it('posts Oracle E-Business reviews as JSON to the review endpoint', async () => {
    const payload = { project_id: 'p1', package: 'oracle_web', source_text: 'GET /OA_HTML' }
    await api.reviewOracleEBusiness(payload)

    expect(request).toHaveBeenCalledWith('/scripts/import/oracle-ebusiness/review', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify(payload),
    }))
  })

  it('fetches authoring capabilities from the capabilities endpoint', async () => {
    await api.listAuthoringCapabilities()
    expect(request).toHaveBeenCalledWith('/scripts/authoring/capabilities')
  })

  it('preserves recording provenance when a reviewed draft is saved', async () => {
    const script = { name: 'Recorded', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [] }

    await api.createScript(script, 'project-1', undefined, 'recording-1')

    expect(request).toHaveBeenCalledWith('/scripts', {
      method: 'POST',
      body: JSON.stringify({
        script,
        project_id: 'project-1',
        source_recording_session_id: 'recording-1',
      }),
    })
  })

  it('binds stored correlation diagnosis to the raw replay session when available', async () => {
    const replay = [{ id: 0, request_url: '/login' }]

    await api.diagnoseScriptReplay('script-1', replay, 'replay-1')

    expect(request).toHaveBeenCalledWith('/scripts/script-1/diagnose-replay', {
      method: 'POST',
      body: JSON.stringify({ replay, replay_session_id: 'replay-1' }),
    })
  })

  it('sends uploaded recordings only through the disclosure-safe generic endpoint', async () => {
    const replay = [{ id: 0, request_url: '/login' }]
    const recorded = [{ id: 0, request_url: '/recorded-login' }]

    await api.diagnoseCorrelations(recorded, replay)

    expect(request).toHaveBeenCalledWith('/correlations/diagnose', {
      method: 'POST',
      body: JSON.stringify({ recorded, replay }),
    })
  })

  it('applies logical secret bindings through the dedicated endpoint', async () => {
    const payload = {
      script: { name: 'Imported', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [] },
      script_index: 2,
      bindings: [{ requirement_id: 'secret-binding-1', secret_name: 'LOGIN_TOKEN' }],
    }

    await api.applySecretBindings(payload)

    expect(request).toHaveBeenCalledWith('/scripts/secret-bindings/apply', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  })

  it('discards protected source-secret sessions without requesting a response body', async () => {
    const payload = { project_id: 'project-1', source_secret_session_id: 'source-session-1' }

    await api.discardImportSecretSession(payload)

    expect(request).toHaveBeenCalledWith('/scripts/import/secrets/discard', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  })

  it('uses canonical replay session routes for script debugger controls', async () => {
    await api.getScriptDebugSession('debug-1')
    await api.pauseScriptDebugSession('debug-1')
    await api.resumeScriptDebugSession('debug-1')
    await api.stepScriptDebugSession('debug-1', 'over')
    await api.updateScriptDebugBreakpoints('debug-1', [{ phase: 'action', action_index: 2 }])
    await api.updateScriptDebugOptions('debug-1', { break_on_failure: true })
    await api.stopScriptDebugSession('debug-1')

    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1')
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/pause', expect.objectContaining({ method: 'POST' }))
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/resume', expect.objectContaining({ method: 'POST' }))
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/step', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ mode: 'over' }),
    }))
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/breakpoints', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ breakpoints: [{ phase: 'action', action_index: 2 }] }),
    }))
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/options', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ break_on_failure: true }),
    }))
    expect(request).toHaveBeenCalledWith('/replay/sessions/debug-1/stop', expect.objectContaining({ method: 'POST' }))
  })

  it('preserves stored and draft script-debug start payloads', async () => {
    const storedPayload = {
      group_name: 'checkout-debug',
      variables: { tenant: 'north' },
      start_paused: true,
    }
    const script = {
      name: 'Draft Debug',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test' },
      actions: [],
    }

    await api.startScriptDebugSession('script-1', storedPayload)
    await api.startDraftScriptDebugSession({ ...storedPayload, script })

    expect(request).toHaveBeenCalledWith('/scripts/script-1/debug/sessions', {
      method: 'POST',
      body: JSON.stringify(storedPayload),
    })
    expect(request).toHaveBeenCalledWith('/scripts/debug/sessions', {
      method: 'POST',
      body: JSON.stringify({ ...storedPayload, script }),
    })
  })

  it('exports script debugger sessions from canonical replay routes', async () => {
    const text = vi.fn().mockResolvedValue('debug log')
    ;(apiFetch as Mock).mockResolvedValueOnce({ ok: true, text })

    await expect(api.exportScriptDebugSession('debug-1', 'log')).resolves.toBe('debug log')

    expect(apiFetch).toHaveBeenCalledWith('/replay/sessions/debug-1/export?format=log')
  })

  it('keeps data-pool exports as blobs instead of materializing a second text copy', async () => {
    const blob = new Blob(['customer_id,region\nC-1,east\n'], { type: 'text/csv' })
    const readBlob = vi.fn().mockResolvedValue(blob)
    ;(apiFetch as Mock).mockResolvedValueOnce({
      ok: true,
      headers: new Headers({
        'Content-Disposition': 'attachment; filename="Customers.csv"',
      }),
      blob: readBlob,
    })

    await expect(api.exportDataPoolRows('pool-1')).resolves.toEqual({
      blob,
      filename: 'Customers.csv',
    })

    expect(apiFetch).toHaveBeenCalledWith('/data-pools/pool-1/rows/export')
    expect(readBlob).toHaveBeenCalledTimes(1)
  })
})
