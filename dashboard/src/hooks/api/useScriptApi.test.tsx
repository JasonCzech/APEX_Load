import { authoringFixture, savedAuthoringFixture } from '../../test/scriptAuthoringFixtures'
import { scenarioAuthoringFixture, withScenarioAuthoring } from '../../test/scenarioAuthoringFixtures'
// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

vi.mock('../apiClient', () => ({
  apiFetch: vi.fn(),
  request: vi.fn(),
  requestWithResponse: vi.fn(),
}))

import { apiFetch, request, requestWithResponse } from '../apiClient'
import { useScriptApi } from './useScriptApi'
import type { ApplyParameterizationRequest } from '../../types/scriptImports'
import type { StoredScript } from '../../types/storedContent'
import { ScriptSaveOutcomeUnknown } from '../../utils/scriptAuthoringRevision'

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
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('sends a caller-supplied Idempotency-Key on script and scenario creates', async () => {
    const options = { headers: { 'Idempotency-Key': 'create-intent-key-0001' } }
    const lastKey = () => new Headers(((request as Mock).mock.lastCall?.[1] as RequestInit).headers).get('Idempotency-Key')
    await api.createScript({ id: '', name: 'Retry safe', protocol: 'http', version: '1', config: { base_url: 'https://example.test' }, actions: [] } as never, 'project', null, undefined, options)
    expect((request as Mock).mock.lastCall?.[0]).toBe('/scripts')
    expect(lastKey()).toBe('create-intent-key-0001')
    await api.createScenario({ name: 'Retry safe', duration: '1m', groups: [] } as never, options)
    expect((request as Mock).mock.lastCall?.[0]).toBe('/scenarios')
    expect(lastKey()).toBe('create-intent-key-0001')
  })

  it('serializes the complete shared scenario shape for create and save, including explicit clears and advanced group fields', async () => {
    const payload = {
      name: 'Iteration snapshot', project_id: 'project', workload_shape_id: '', duration: '', termination_mode: 'iterations' as const, start_delay: '2s',
      runtime_settings: { results: { message_detail: { mode: 'off' as const, include_resources: false } }, websocket: { callback_interval_ms: 0, compression: false, reconnect: false } },
      groups: [{ name: 'group', script_id: 'script', vusers: 1, iterations: 3, start_offset: '4s', initialization: 'at_once' as const, transaction_response_time_sla_ms: 1250, runtime_settings: { results: {}, websocket: {} } }],
    }
    await api.createScenario(payload)
    expect(request).toHaveBeenLastCalledWith('/scenarios', expect.objectContaining({ method: 'POST', body: JSON.stringify(payload) }))
    const baseline = scenarioAuthoringFixture({}, 'scenario', 'project')
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: withScenarioAuthoring({ id: 'scenario', project_id: 'project' }, 'b'), headers: new Headers({ 'X-APEX-Scenario-Save-Mode': 'conditioned' }) })
    await api.updateScenario('scenario', payload, baseline)
    expect(requestWithResponse).toHaveBeenLastCalledWith('/scenarios/scenario', expect.objectContaining({ method: 'PUT', headers: { 'If-Match': baseline.etag }, body: JSON.stringify(payload) }))
    expect(payload.workload_shape_id).toBe('')
    expect(payload.groups[0].runtime_settings).toEqual({ results: {}, websocket: {} })
  })

  it('saves a scenario only against an admitted baseline and accepts only a conditioned receipt carrying the next baseline', async () => {
    const baseline = scenarioAuthoringFixture({}, 'scenario-1', 'project-1')
    const payload = { name: 'Edited', project_id: 'project-1', duration: '2m', groups: [] }
    const saved = withScenarioAuthoring({ id: 'scenario-1', name: 'Edited', project_id: 'project-1' }, 'b')
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: saved, headers: new Headers({ 'X-APEX-Scenario-Save-Mode': 'conditioned' }) })
    expect(await api.updateScenario('scenario-1', payload, baseline)).toEqual(saved)
    const [url, options] = (requestWithResponse as Mock).mock.lastCall!
    expect(url).toBe('/scenarios/scenario-1')
    expect(options.headers).toEqual({ 'If-Match': '"scenario-authoring-v1:scn-v1-' + 'a'.repeat(64) + '"' })
    expect(options.body).not.toContain('scn-v1-')

    for (const [headers, body] of [
      [new Headers(), saved],
      [new Headers({ 'X-APEX-Scenario-Save-Mode': 'legacy_unconditioned' }), saved],
      [new Headers({ 'X-APEX-Scenario-Save-Mode': 'conditioned' }), { ...saved, authoring_projection: undefined }],
      [new Headers({ 'X-APEX-Scenario-Save-Mode': 'conditioned' }), { ...saved, id: 'other' }],
    ] as const) {
      ;(requestWithResponse as Mock).mockResolvedValueOnce({ body, headers })
      await expect(api.updateScenario('scenario-1', payload, baseline)).rejects.toMatchObject({ name: 'ScenarioSaveOutcomeUnknown' })
    }

    ;(requestWithResponse as Mock).mockClear()
    await expect(api.updateScenario('scenario-1', payload, { ...baseline, etag: '"unpaired"' })).rejects.toThrow('supported scenario revision')
    await expect(api.updateScenario('scenario-2', payload, baseline)).rejects.toThrow('supported scenario revision')
    await expect(api.updateScenario('scenario-1', { ...payload, project_id: 'project-2' }, baseline)).rejects.toThrow('supported scenario revision')
    expect(requestWithResponse).not.toHaveBeenCalled()
  })

  it('deletes a listed scenario only while it is unchanged and with its current tag', async () => {
    const current = withScenarioAuthoring({ id: 'scenario-1', project_id: 'project-1', updated_at: '2026-10-07T10:00:00Z' }, 'c')
    ;(request as Mock).mockResolvedValueOnce(current).mockResolvedValueOnce({ deleted: 'scenario-1' })
    await api.deleteScenario('scenario-1', '2026-10-07T10:00:00Z')
    expect(request).toHaveBeenNthCalledWith(1, '/scenarios/scenario-1')
    expect(request).toHaveBeenLastCalledWith('/scenarios/scenario-1', { method: 'DELETE', headers: { 'If-Match': '"scenario-authoring-v1:scn-v1-' + 'c'.repeat(64) + '"' } })

    ;(request as Mock).mockReset()
    ;(request as Mock).mockResolvedValueOnce(current)
    await expect(api.deleteScenario('scenario-1', '2026-10-07T09:00:00Z')).rejects.toMatchObject({ name: 'ScenarioChangedSinceObserved' })
    expect(request).toHaveBeenCalledTimes(1)

    // A controller without durable revisions serves no tag; the delete goes
    // unconditioned and the project's policy decides.
    ;(request as Mock).mockReset()
    ;(request as Mock).mockResolvedValueOnce({ id: 'scenario-1', updated_at: 'x' }).mockResolvedValueOnce({ deleted: 'scenario-1' })
    await api.deleteScenario('scenario-1', 'x')
    expect(request).toHaveBeenLastCalledWith('/scenarios/scenario-1', { method: 'DELETE' })
  })

  it('carries the scenario tag on library assignment and the observed baseline on data-pool writes', async () => {
    const current = withScenarioAuthoring({ id: 'scenario-1', project_id: 'project-1' }, 'd')
    ;(request as Mock).mockResolvedValueOnce(current).mockResolvedValueOnce({ revision: 2 })
    await api.replaceLibraryAssignment('scenarios', 'scenario-1', null, [], 1)
    expect(request).toHaveBeenLastCalledWith('/scenarios/scenario-1/library-assignment', expect.objectContaining({ method: 'PUT', headers: { 'If-Match': '"scenario-authoring-v1:scn-v1-' + 'd'.repeat(64) + '"' } }))
    ;(request as Mock).mockClear()
    await api.replaceLibraryAssignment('scripts', 'script-1', null, [], 1)
    expect(request).toHaveBeenCalledTimes(1)
    expect((request as Mock).mock.lastCall?.[1]).not.toHaveProperty('headers')

    const baseline = scenarioAuthoringFixture({}, 'scenario-1')
    await api.updateScenarioDataPools('scenario-1', [], baseline)
    expect(request).toHaveBeenLastCalledWith('/scenarios/scenario-1/data-pools', expect.objectContaining({ method: 'PUT', headers: { 'If-Match': baseline.etag } }))
    expect(() => api.updateScenarioDataPools('scenario-2', [], baseline)).toThrow('supported scenario revision')
  })

  it('loads a strict paired authoring projection through the bounded envelope', async () => {
    const pair = authoringFixture()
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: pair.projection, headers: new Headers({ ETag: pair.etag }) })
    expect(await api.getScriptAuthoring('script-1', 'project-1')).toEqual(pair)
    expect(requestWithResponse).toHaveBeenCalledWith('/scripts/script-1?view=authoring', expect.objectContaining({ timeoutMs: 30_000, maxResponseBytes: 12 << 20 }))
  })

  it('sends the exact observed tag outside DSL and accepts only a final conditioned receipt', async () => {
    const pair = authoringFixture()
    const saved = savedAuthoringFixture({ name: 'Edited' })
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: saved, headers: new Headers({ 'X-APEX-Script-Save-Mode': 'conditioned' }) })
    expect(await api.updateScript('script-1', saved.script!, pair)).toEqual(saved)
    const [url, options] = (requestWithResponse as Mock).mock.calls[(requestWithResponse as Mock).mock.calls.length - 1]
    expect(url).toBe('/scripts/script-1')
    expect(options.headers).toEqual({ 'If-Match': pair.etag })
    expect(JSON.parse(options.body)).toEqual({ script: saved.script })
    expect(options.body).not.toContain('scr-v1-')
  })

  it.each([undefined, 'legacy_unconditioned'])('refuses a successful write without a protected save-mode receipt (%s)', async mode => {
    const headers = new Headers()
    if (mode) headers.set('X-APEX-Script-Save-Mode', mode)
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: savedAuthoringFixture(), headers })
    await expect(api.updateScript('script-1', authoringFixture().projection.script, authoringFixture())).rejects.toThrow('save outcome could not be verified')
    expect(request).not.toHaveBeenCalled()
  })

  it('sends a saved-script repair with the observed tag and accepts an apply only with a conditioned receipt', async () => {
    const pair = authoringFixture()
    const repair = { finding: 'legacy_tls_replacement', action_path: 'init[0]', operation: { type: 'select_tls', tls_min: '1.2', tls_max: '1.3' } }
    const outcome = { script_id: 'script-1', content_revision: pair.projection.content_revision, findings: [], readiness: { replay_ready: true, promotion_ready: false }, secret_binding_requirements: [], fidelity_report: {} }
    const evaluated = { ...outcome, script: pair.projection.script, before: pair.projection.script }
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: evaluated, headers: new Headers() })
    expect(await api.previewScriptRepair('script-1', repair, pair, 'project-1')).toEqual(evaluated)
    const [previewURL, previewOptions] = (requestWithResponse as Mock).mock.calls[0]
    expect(previewURL).toBe('/scripts/script-1/repairs/preview')
    expect(previewOptions).toEqual(expect.objectContaining({ method: 'POST', headers: { 'If-Match': pair.etag }, timeoutMs: 30_000, maxResponseBytes: 12 << 20 }))
    expect(JSON.parse(previewOptions.body)).toEqual(repair)
    expect(previewOptions.body).not.toContain('scr-v1-')

    // An apply carries the persisted script, not the preview's before/after pair.
    const applied = { ...outcome, stored: savedAuthoringFixture({}, 'script-1', 'project-1', 'c') }
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: applied, headers: new Headers({ 'X-APEX-Script-Save-Mode': 'conditioned' }) })
    expect(await api.applyScriptRepair('script-1', repair, pair, 'project-1')).toEqual(applied)
    const [applyURL, applyOptions] = (requestWithResponse as Mock).mock.calls[1]
    expect(applyURL).toBe('/scripts/script-1/repairs/apply')
    expect(applyOptions.headers).toEqual({ 'If-Match': pair.etag })
    // An unpaired baseline sends nothing.
    ;(requestWithResponse as Mock).mockClear()
    await expect(api.previewScriptRepair('script-1', repair, { ...pair, etag: '"unpaired"' }, 'project-1')).rejects.toThrow('supported paired authoring revision')
    expect(requestWithResponse).not.toHaveBeenCalled()
  })

  it.each([
    ['no save-mode receipt', new Headers(), (stored: StoredScript) => stored],
    ['an unconditioned save', new Headers({ 'X-APEX-Script-Save-Mode': 'legacy_unconditioned' }), (stored: StoredScript) => stored],
    ['another script', new Headers({ 'X-APEX-Script-Save-Mode': 'conditioned' }), (stored: StoredScript) => ({ ...stored, id: 'script-2' })],
    ['an unpaired next baseline', new Headers({ 'X-APEX-Script-Save-Mode': 'conditioned' }), (stored: StoredScript) => ({ ...stored, content_revision: `scr-v1-${'d'.repeat(64)}` })],
  ])('refuses a repair apply with %s as an unknown save outcome', async (_label, headers, receipt) => {
    const pair = authoringFixture()
    const stored = receipt(savedAuthoringFixture({}, 'script-1', 'project-1', 'c'))
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: { script_id: 'script-1', content_revision: pair.projection.content_revision, stored }, headers })
    const outcome = api.applyScriptRepair('script-1', { finding: 'legacy_tls_replacement', action_path: 'init[0]', operation: { type: 'select_tls' } }, pair, 'project-1')
    await expect(outcome).rejects.toBeInstanceOf(ScriptSaveOutcomeUnknown)
    await expect(outcome).rejects.toThrow('save outcome could not be verified')
  })

  it('refuses an old controller which ignores the authoring query', async () => {
    ;(requestWithResponse as Mock).mockResolvedValueOnce({ body: savedAuthoringFixture(), headers: new Headers({ ETag: authoringFixture().etag }) })
    await expect(api.getScriptAuthoring('script-1', 'project-1')).rejects.toThrow('supported paired authoring revision')
  })

  it('sends only the observed authoring tag for a protected pure secret rebind', async () => {
    const pair = authoringFixture()
    ;(request as Mock).mockResolvedValueOnce({ script: pair.projection.script, changed_locations: [], changed_count: 0 })
    await api.rebindScriptSecretReference(pair.projection.script, 'OLD', 'NEW', pair)
    expect(request).toHaveBeenLastCalledWith('/scripts/secret-references/rebind', expect.objectContaining({
      headers: { 'If-Match': pair.etag }, timeoutMs: 30_000, maxResponseBytes: 12 << 20,
      body: JSON.stringify({ script: pair.projection.script, old_name: 'OLD', new_name: 'NEW' }),
    }))
    expect((request as Mock).mock.calls[(request as Mock).mock.calls.length - 1]?.[1].body).not.toContain('scr-v1-')
    ;(request as Mock).mockClear()
    expect(() => api.rebindScriptSecretReference(pair.projection.script, 'OLD', 'NEW', { ...pair, etag: '"unpaired"' })).toThrow('supported paired authoring revision')
    expect(request).not.toHaveBeenCalled()
  })

  it('keeps pure rebind authored changes while removing read-only server clocks only for paired clients', async () => {
    const pair = authoringFixture()
    const transformed = { ...pair.projection.script, created_at: '0001-01-01T00:00:00Z', updated_at: '0001-01-01T00:00:00Z',
      archived_at: 'readonly-marker', actions: [{ name: 'Authored rebind', type: 'request', target: '/',
        headers: { Authorization: '{{secret.NEW}}' }, metadata: { created_at: 'authored nested marker' } }] }
    const helperResult = { script: transformed, changed_locations: ['/actions/0/headers/Authorization'], changed_count: 1 }
    ;(request as Mock).mockResolvedValue(helperResult)
    const normalized = await api.rebindScriptSecretReference(pair.projection.script, 'OLD', 'NEW', pair)
    expect(normalized.script).toEqual({ ...pair.projection.script, actions: transformed.actions })
    expect(normalized.changed_locations).toEqual(helperResult.changed_locations)
    expect(normalized.changed_count).toBe(1)
    expect(transformed.created_at).toBe('0001-01-01T00:00:00Z')
    expect(pair.projection.script.actions).toEqual([])
    expect((request as Mock).mock.calls[(request as Mock).mock.calls.length - 1]?.[1].headers).toEqual({ 'If-Match': pair.etag })
    const legacy = await api.rebindScriptSecretReference(pair.projection.script, 'OLD', 'NEW')
    expect(legacy).toBe(helperResult)
    expect((request as Mock).mock.calls[(request as Mock).mock.calls.length - 1]?.[1].headers).toBeUndefined()
  })

  it.each([{ id: 'other-script' }, { project_id: 'other-project' }])('refuses a paired helper result for a different resource (%s)', override => {
    const pair = authoringFixture()
    ;(request as Mock).mockResolvedValueOnce({ script: { ...pair.projection.script, ...override }, changed_locations: [], changed_count: 0 })
    return expect(api.rebindScriptSecretReference(pair.projection.script, 'OLD', 'NEW', pair)).rejects.toThrow('different script or project')
  })

  it('preserves exact Aura action routes and encoding metadata in the apply request', async () => {
    const payload: ApplyParameterizationRequest = {
      script: { name: 'Aura draft', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] },
      suggestions: [{ id: 'nested', kind: 'data_param', var_name: 'recordid', replacement: '{{data.records.recordid}}', confidence: 0.7,
        rule_id: 'aura', rationale: 'Reviewed field', auto_apply: false, high_risk: false,
        locations: [{ section: 'actions', action_index: 99, field: 'body', replace_text: '',
          action_path: 'actions[0].control.steps[1].params.requests[2]',
          aura_route: [{ kind: 'key', key: 'actions' }, { kind: 'index', index: 0 }, { kind: 'json_string' }, { kind: 'key', key: 'recordId' }],
          decoded_field_path: 'actions[0].params.details::<json>.recordId',
          encoding_layers: ['url_form', 'json', 'json_string'], payload_shape: 'structural-digest' }] }],
      selected_ids: ['nested'], confirm_high_risk: false,
    }
    await api.applyImportParameterization(payload)
    const [url, options] = (request as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/parameterize/apply')
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual(payload)
  })

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s sends project scope before archive and supporting files', async method => {
      const archive = new File(['fixture'], 'script.zip')
      const har = new File(['{}'], 'recording.har')
      const supplemental = new File(['data'], 'accounts.dat')
      await api[method](archive, { project_id: 'project-1', har_file: har, supplemental_files: [supplemental] })
      const [, options] = (request as Mock).mock.calls[0]
      const body = options.body as FormData
      expect(Array.from(body.keys())).toEqual(['project_id', 'file', 'har_file', 'supplemental_file'])
      expect(body.get('project_id')).toBe('project-1')
      expect(body.get('file')).toBe(archive)
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s selects a nested member while retaining original upload bytes', async method => {
      const archive = new File(['fixture'], 'collection.zip')
      await api[method](archive, { project_id: 'project-1', archive_member: 'Project.USZ' })
      const body = (request as Mock).mock.calls[0][1].body as FormData
      expect(body.get('file')).toBe(archive)
      expect(body.get('archive_member')).toBe('Project.USZ')
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s sends draft expiration only when explicitly enabled', async method => {
      const archive = new File(['fixture'], 'script.zip')
      await api[method](archive)
      let body = (request as Mock).mock.calls[0][1].body as FormData
      expect(body.has('expire_draft')).toBe(false)

      ;(request as Mock).mockClear()
      await api[method](archive, { expire_draft: true })
      body = (request as Mock).mock.calls[0][1].body as FormData
      expect(body.get('expire_draft')).toBe('true')
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s preserves VuGen folder paths without a ZIP upload', async method => {
      const source = new File(['source'], 'Action.c')
      const project = new File(['project'], 'Demo.usr')
      Object.defineProperty(source, 'webkitRelativePath', { value: 'Demo/Action.c' })
      Object.defineProperty(project, 'webkitRelativePath', { value: 'Demo/Demo.usr' })
      await api[method](new File([], 'Demo'), { project_id: 'project-1', folder_files: [source, project] })
      const body = (request as Mock).mock.calls[0][1].body as FormData
      expect(Array.from(body.keys())).toEqual(['project_id', 'file:Demo%2FAction.c', 'file:Demo%2FDemo.usr'])
      expect(body.has('file')).toBe(false)
      expect((body.get('file:Demo%2FAction.c') as File).name).toBe(source.name)
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s aborts stalled response bodies at its deadline and keeps the upload retryable', async method => {
      const client = await vi.importActual<typeof import('../apiClient')>('../apiClient')
      ;(request as Mock).mockImplementationOnce(client.request)
      vi.useFakeTimers()
      let signal: AbortSignal | undefined
      const text = vi.fn(() => new Promise<string>(() => {}))
      vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, options) => {
        signal = options?.signal as AbortSignal
        return { ok: true, status: 200, headers: new Headers({ 'Content-Type': 'application/json' }), text } as unknown as Response
      })
      const pending = api[method](new File(['fixture'], 'collection.zip'))
      const rejection = expect(pending).rejects.toThrow('Your archive remains in the queue. Retry validation.')
      await vi.advanceTimersByTimeAsync(209_999)
      expect(text).toHaveBeenCalledTimes(1)
      expect(signal?.aborted).toBe(false)
      await vi.advanceTimersByTimeAsync(1)
      await rejection
      expect(signal?.aborted).toBe(true)
      expect(vi.getTimerCount()).toBe(0)
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s rejects a stalled header request even if the transport ignores abort', async method => {
      const client = await vi.importActual<typeof import('../apiClient')>('../apiClient')
      ;(request as Mock).mockImplementationOnce(client.request)
      vi.useFakeTimers()
      let signal: AbortSignal | undefined
      vi.spyOn(globalThis, 'fetch').mockImplementation((_input, options) => {
        signal = options?.signal as AbortSignal
        return new Promise<Response>(() => {})
      })
      const pending = api[method](new File(['fixture'], 'script.zip'))
      const rejection = expect(pending).rejects.toThrow('Import review timed out after 3 minutes 30 seconds.')
      await vi.advanceTimersByTimeAsync(210_000)
      await rejection
      expect(signal?.aborted).toBe(true)
      expect(vi.getTimerCount()).toBe(0)
    },
  )

  it.each(['importEnterpriseArchive', 'validateEnterpriseArchiveImport'] as const)(
    '%s clears its deadline after JSON success or server error', async method => {
      const client = await vi.importActual<typeof import('../apiClient')>('../apiClient')
      vi.useFakeTimers()
      const fetchMock = vi.spyOn(globalThis, 'fetch')
      for (const ok of [true, false]) {
        ;(request as Mock).mockImplementationOnce(client.request)
        fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(ok ? { scripts: [] } : { error: 'Invalid script archive' }), {
          status: ok ? 200 : 422,
          headers: { 'Content-Type': 'application/json' },
        }))
        const pending = api[method](new File(['fixture'], 'script.zip'))
        if (ok) await expect(pending).resolves.toEqual({ scripts: [] })
        else await expect(pending).rejects.toThrow('Invalid script archive')
        const signal = fetchMock.mock.calls[fetchMock.mock.calls.length - 1][1]?.signal as AbortSignal
        expect(vi.getTimerCount()).toBe(0)
        await vi.advanceTimersByTimeAsync(210_000)
        expect(signal.aborted).toBe(false)
      }
    },
  )

  it.each(['importJMeter', 'validateJMeterImport'] as const)('%s authorizes the project before streaming file bytes', async method => {
    const file = new File(['<jmeterTestPlan/>'], 'plan.jmx')
    await api[method](file, { project_id: 'project-1', name: 'Orders' })
    const [, options] = (apiFetch as Mock).mock.calls[0]
    expect(Array.from((options.body as FormData).keys()).slice(0, 2)).toEqual(['project_id', 'file'])
    expect((options.body as FormData).get('file')).toBe(file)
  })

  it.each(['importHAR', 'importSalesforce'] as const)('%s authorizes the project before upload bytes and retains transform options', async method => {
    const file = new File(['{}'], 'capture.har', { type: 'application/json' })
    await api[method](file, { project_id: 'project with spaces', name: 'Capture', base_url: 'https://example.test', agentforce_transform: true })
    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe(`/scripts/import/${method === 'importHAR' ? 'har' : 'salesforce'}?project_id=project+with+spaces&agentforce_transform=true&response_format=canonical-v1`)
    const body = options.body as FormData
    expect(Array.from(body.keys())).toEqual(['project_id', 'file', 'name', 'base_url'])
    expect(body.get('project_id')).toBe('project with spaces')
    expect(body.get('file')).toBe(file)
  })

  it('retains HAR native selection alongside project scope and the secondary capture', async () => {
    const file = new File(['{}'], 'first.har')
    const second = new File(['{}'], 'second.har')
    await api.importHAR(file, { project_id: 'project-1', salesforce_native: false, secondary_file: second, include_static_assets: true })
    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe('/scripts/import/har?project_id=project-1&salesforce_native=false&response_format=canonical-v1')
    expect(Array.from((options.body as FormData).keys())).toEqual(['project_id', 'file', 'include_static_assets', 'secondary_file'])
    expect((options.body as FormData).get('secondary_file')).toBe(second)
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

  it.each(['importPostman', 'validatePostmanImport'] as const)('%s includes project scope before collection bytes and in the URL', async method => {
    const file = new File(['{}'], 'collection.json', { type: 'application/json' })
    await api[method](file, { project_id: 'project-1', agentforce_transform: true })
    const [url, options] = (apiFetch as Mock).mock.calls[0]
    expect(url).toBe(`/scripts/import/postman${method === 'validatePostmanImport' ? '/validate' : ''}?project_id=project-1&agentforce_transform=true`)
    expect(Array.from((options.body as FormData).keys()).slice(0, 2)).toEqual(['project_id', 'file'])
  })

  // The controller authorizes multipart imports from the project_id query or a
  // multipart field that precedes the first file part (it never buffers file
  // bytes before authorization). A file-first body is rejected with 400, which
  // silently broke nine importers once; pin the ordering for every wrapper.
  it.each([
    'importDevWeb',
    'importK6',
    'importCloudTest',
    'importEventHubsCapture',
    'importBrokerConfig',
    'importBrowserRecording',
    'validateBrowserRecordingImport',
    'replayBrowserRecordingImport',
    'importRAML',
    'importWSDL',
    'importAsyncAPI',
    'importOpenAPI',
  ] as const)('%s sends project_id before the file part', async method => {
    const file = new File(['spec'], 'source.txt', { type: 'text/plain' })
    await (api[method] as (file: File, options: { project_id: string }) => Promise<unknown>)(file, { project_id: 'project-1' })
    const [, options] = (apiFetch as Mock).mock.calls[0]
    const keys = Array.from((options.body as FormData).keys())
    expect(keys).toContain('project_id')
    expect(keys.indexOf('project_id')).toBeLessThan(keys.indexOf('file'))
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

  it('downloads the exact runnable script from the server endpoint', async () => {
    const content = '{"config":{"http":{"auth":{"password":"preserved-password"}}}}'
    ;(apiFetch as Mock).mockResolvedValueOnce({
      ok: true,
      text: async () => content,
    })

    await expect(api.downloadScript('script/with spaces')).resolves.toBe(content)
    expect(apiFetch).toHaveBeenCalledWith('/scripts/script%2Fwith%20spaces/download')
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
