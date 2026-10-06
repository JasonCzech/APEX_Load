import type { JMeterBatchRepairRequest, JMeterBatchRepairResponse } from '../../types/scriptImports'
import { useCallback, useMemo } from 'react'

import {
  AuthoringComponent,
  AuthoringHealth,
  AuthoringCorrelationPreviewRequest,
  AuthoringCorrelationPreviewResponse,
  AuthoringSession,
  TestSummary,
} from '../../types'
import type { LibraryAssignment, ScriptAuthoringBaseline, StoredScenario, StoredScript } from '../../types/storedContent'
import type { ScenarioGroupPayload, ScenarioUpsertPayload } from '../../components/scenarioPayloadUtils'
import type { RuntimeSettingsOverride } from '../../types/runtimeSettings'
import type {
  ScenarioDataPoolBinding,
  SharedDataPool,
  SharedDataPoolRow,
} from '../../types/dataPools'
import type {
  BrowserReplayNetworkEntry,
  BrowserReplaySessionResponse,
  BrowserReplayStartRequest,
  BrowserReplayStartResponse,
  ScriptDebugBreakpoint,
  ScriptDebugSessionResponse,
  ScriptDebugStartRequest,
  ScriptDebugStartResponse,
  ScriptDebugStepMode,
} from '../../types/replay'
import { type AuthoringTemplate } from '../../types'
import { type CorrelationDiagnoseResponse, type CorrelationStep, type ScriptDefinition, type ScriptExportResponse, type ScriptReplayResponse, type ScriptValidationResponse } from '../../types/scripts'
import { type CloneScriptDraftResponse, type DiscardImportSecretSessionRequest, type MigrateImportSecretsRequest, type MigrateImportSecretsResponse, type RebindScriptSecretReferenceResponse, type ScriptSecretStatus } from '../../types/secrets'
import { type BrunoInspectResponse, type BrunoProjectionPreview, type ImportSourceRevision, type BrunoSupportMatrixResponse, type CommitBrunoImportRequest, type CommitBrunoImportResponse } from '../../types/scriptImports'
import { downloadFilenameFromDisposition } from '../../utils/fileUtils'
import { importFileLogicalPath } from '../../importLogicalPath'

export interface ScriptRecordingStatus {
  exists: boolean
  correlation_usable?: boolean
  correlation_unavailable_reason?: string
  request_count?: number
  correlation_request_count?: number
  correlation_truncated?: boolean
  source?: string
  sensitivity?: string
  captured_at?: string
}
import { type ApplyParameterizationRequest, type ApplyParameterizationResponse, type ApplySecretBindingsRequest, type ApplySecretBindingsResponse, type ApplySharedValuesRequest, type ApplySharedValuesResponse, type AuthoringCapabilitiesResponse, type Parameterization, type AsyncAPIImportResult, type AsyncAPISupportMatrixResponse, type BrokerConfigImportResult, type BrokerConfigSupportMatrixResponse, type BrowserRecordingImportResult, type CloudTestImportResult, type CloudTestSupportMatrixResponse, type CommitAsyncAPIImportBatchRequest, type CommitAsyncAPIImportBatchResponse, type CommitAsyncAPIImportRequest, type CommitAsyncAPIImportResponse, type CommitGenericImportBatchRequest, type CommitGenericImportBatchResponse, type CommitOpenAPIImportRequest, type CommitOpenAPIImportResponse, type DevWebImportResult, type DevWebSupportMatrixResponse, type EventHubsCaptureImportResult, type EventHubsCaptureSupportMatrixResponse, type HARImportResult, type ImportAssetUploadResponse, type JavaClasspathAssetUploadResponse, type JMeterImportResult, type JMeterSupportMatrixResponse, type K6ImportResult, type K6SupportMatrixResponse, type EnterpriseArchiveImportResult, type EnterpriseArchiveSupportMatrixResponse, type OpenAPIImportResult, type OpenAPISupportMatrixResponse, type OracleEBusinessReviewRequest, type OracleEBusinessReviewResponse, type OracleEBusinessSupportMatrixResponse, type PostmanImportResult, type PostmanSupportMatrixResponse, type RAMLImportResult, type RAMLSupportMatrixResponse, type SalesforceImportResult, type SampleImportResult, type SaveEnterpriseArchiveImportRequest, type SaveEnterpriseArchiveImportResponse, type SaveJMeterImportRequest, type SaveJMeterImportResponse, type SavePostmanImportRequest, type SavePostmanImportResponse, type WSDLImportResult } from '../../types/scriptImports'
import { type JsonRequestBody } from '../../types/generated/openapi'
import { ApiError, apiFetch, request, requestWithResponse } from '../apiClient'

import { admitScriptAuthoringBaseline, baselineFromSavedScript, ScriptSaveOutcomeUnknown, withoutScriptAuthoringReadContext } from '../../utils/scriptAuthoringRevision'

type StoredScriptDebugStartRequestBody = JsonRequestBody<'handleStartScriptDebugScript'>
type DraftScriptDebugStartRequestBody = JsonRequestBody<'handleStartScriptDebugDraftScript'>
type ScriptDebugStepRequestBody = JsonRequestBody<'handleStepScriptDebugSession'>
type ScriptDebugBreakpointsRequestBody = JsonRequestBody<'handleUpdateScriptDebugBreakpoints'>
type ScriptDebugOptionsRequestBody = JsonRequestBody<'handleUpdateScriptDebugOptions'>

// The public authoring types intentionally retain a few forward-compatible
// domain fields. This adapter keeps those call signatures stable while using
// the generated transport keys and scalar contracts at the serialization edge.
type CompatibleDebugStartRequest<Transport extends object, Domain extends object> = {
  [Key in keyof Transport]?: Key extends keyof Domain ? Domain[Key] : Transport[Key]
}

type StoredScriptDebugStartBody = CompatibleDebugStartRequest<StoredScriptDebugStartRequestBody, ScriptDebugStartRequest>
type DraftScriptDebugStartBody = CompatibleDebugStartRequest<
  DraftScriptDebugStartRequestBody,
  ScriptDebugStartRequest & { script: ScriptDefinition }
>

type ScriptListResponse = {
  scripts: StoredScript[]
  count: number
  limit?: number
  offset?: number
}

type ScriptListOptions = {
  archiveState?: "active" | "archived" | "all"
  limit?: number
  offset?: number
  search?: string
}

type DeleteScriptResponse = {
  deleted: string
}

type MoveScriptResponse = {
  id: string
  folder_id?: string | null
}

type ScenarioListResponse = {
  scenarios: StoredScenario[]
  count: number
}

type ScenarioDeleteResponse = {
  deleted: string
}

type ScenarioResultsResponse = {
  tests: TestSummary[]
  count: number
}

// Share the serialized scenario fields with the authoring/run builders while
// preserving optional API defaults and the existing request runtime contract.
type ScenarioUpsertRequest = Omit<Partial<ScenarioUpsertPayload>, 'name' | 'duration' | 'groups' | 'runtime_settings'> & {
  name: string
  duration: string
  runtime_settings?: RuntimeSettingsOverride
  groups: Array<Omit<ScenarioGroupPayload, 'initialization' | 'runtime_settings'> & {
    initialization?: ScenarioGroupPayload['initialization']
    runtime_settings?: RuntimeSettingsOverride
  }>
}

type DataPoolListResponse = {
  data_pools: SharedDataPool[]
  count: number
}

type DataPoolDetailsResponse = {
  data_pool: SharedDataPool
  rows: SharedDataPoolRow[]
  count?: number
  total?: number
  limit?: number
  offset?: number
  revision?: string
  has_more?: boolean
  next_cursor?: string
}

export type DataPoolImportOptions = {
  mode?: 'append' | 'replace'
  delimiter?: 'auto' | 'comma' | 'tab'
  header?: boolean
  columns?: string[]
  dryRun?: boolean
}

export type DataPoolImportResult = {
  dry_run?: boolean
  mode: string
  columns: string[]
  row_count?: number
  sample_rows?: Record<string, string>[]
  imported?: number
  deleted?: number
  total?: number
  warnings?: string[]
  // Set on dry runs when the same upload cannot be committed in append mode
  // (for example, an engine-cap violation or a replace-only empty backup).
  append_warning?: string
}

type DataPoolDeleteResponse = {
  deleted: string
}

type DataPoolUpsertRequest = Pick<SharedDataPool, 'name'> & Partial<Pick<SharedDataPool, 'description' | 'schema' | 'project_id'>>
type DataPoolUpdateRequest =
  Pick<SharedDataPool, 'name' | 'updated_at'>
  & Partial<Pick<SharedDataPool, 'description' | 'schema' | 'archived_at'>>

type DataPoolRowUpsertRequest = Pick<SharedDataPoolRow, 'data'> & Partial<Pick<SharedDataPoolRow, 'status' | 'version'>>
type DataPoolRowUpdateRequest = Pick<SharedDataPoolRow, 'data' | 'version'> & Partial<Pick<SharedDataPoolRow, 'status'>>

type ScenarioDataPoolResponse = {
  data_pools: ScenarioDataPoolBinding[]
  count: number
}

type AuthoringTemplatesResponse = {
  templates: AuthoringTemplate[]
  count: number
}

type AuthoringComponentsResponse = {
  components: AuthoringComponent[]
  count: number
}

type AuthoringSessionsResponse = {
  sessions: AuthoringSession[]
  count: number
}

type CreateAuthoringSessionRequest = {
  project_id?: string
  /** Keeps the draft in the folder the author was working in; validated on create and again on approve. */
  folder_id?: string | null
  source: string
  name: string
  status?: string
  draft: ScriptDefinition
  review_state?: Record<string, unknown>
  idempotency_key?: string
}

type AuthoringSessionApprovalResponse = {
  session: AuthoringSession
  script: StoredScript
  authoring_health: AuthoringHealth
}

type EnterpriseArchiveImportOptions = {
  archive_member?: string
  name?: string
  base_url?: string
  include_assertions?: boolean
  preserve_transactions?: boolean
  migration_block_statuses?: string[]
  project_id?: string
  har_file?: File
  supplemental_files?: File[]
  folder_files?: File[]
  salesforce_native?: boolean
  expire_draft?: boolean
}

type PostmanImportOptions = {
  name?: string
  base_url?: string
  project_id?: string
  environment_file?: File
  include_assertions?: boolean
  sanitize_sensitive?: boolean
  use_parameter_data_source?: boolean
  default_think_time_ms?: number
  agentforce_transform?: boolean
}

export type BrowserToHTTPPreviewRequest = {
  name?: string
  project_id?: string
  replay_session_id?: string
  source_browser_script?: ScriptDefinition
  network_entries?: BrowserReplayNetworkEntry[]
  include_static_assets?: boolean
}

export type BrowserToHTTPPreviewResponse = {
  schema_version: string
  generated_at: string
  replay_session_id?: string
  source_browser_script?: ScriptDefinition
  generated_script?: ScriptDefinition
  warnings?: string[]
  review: {
    status: string
	promotion_status: string
    source_browser_actions: number
    captured_requests: number
    generated_http_actions: number
    skipped_static_assets: number
    correlation_candidates?: Array<{ name: string; source: string; evidence?: string; action?: string }>
    data_source_candidates?: Array<{ name: string; source: string; value?: string; evidence?: string; action?: string }>
    auth_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    cookie_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    blocking_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    findings?: string[]
	next_actions?: string[]
	evidence: {
	  replay_session_id?: string
	  replay_session_resolved: boolean
	  replay_session_status?: string
	  replay_session_successful: boolean
	  replay_network_entries?: number
	  network_durable: boolean
	  correlation_durable: boolean
	  durable: boolean
	  verification_status: string
	  network_entries: number
	  correlation_observations?: number
	  source_browser_script_id?: string
	  source_browser_script_name?: string
	  required_artifacts?: string[]
	}
  }
	governance: {
	  status: string
	  approval_required: boolean
	  approval_status: string
	  approved_by?: string
	  approved_at?: string
	  required_evidence?: string[]
	  blockers?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
	  next_actions?: string[]
	}
	diff: {
	  source_protocol?: string
	  generated_protocol?: string
	  source_browser_actions: number
	  generated_http_actions: number
	  captured_requests: number
	  endpoint_coverage_pct: number
	  hosts?: string[]
	  methods?: string[]
	  notes?: string[]
	}
  scenario_template: {
    name: string
    duration: string
    groups: Array<{ name: string; script_id: string; protocol: string; vusers: number; purpose: string; claim_policy?: string }>
    notes?: string[]
  }
}

// errorMessageFromResponse pulls a server-provided error string from a failed
// response body, falling back to the HTTP status text. It type-guards the
// `error` field so a non-string (object/array) payload cannot stringify into a
// "[object Object]" message.
async function errorMessageFromResponse(res: Response): Promise<string> {
  const body = await res.json().catch(() => undefined)
  if (body && typeof (body as { error?: unknown }).error === 'string') {
    return (body as { error: string }).error
  }
  return res.statusText
}

const enterpriseArchiveImportTimeoutMs = 210_000

function rethrowEnterpriseArchiveReviewError(error: unknown): never {
  if (error instanceof Error && error.message === `Request timed out after ${enterpriseArchiveImportTimeoutMs}ms`) {
    throw new Error('Import review timed out after 3 minutes 30 seconds. Your archive remains in the queue. Retry validation.')
  }
  throw error
}

function buildScriptQuery(projectId?: string, folderId?: string | null, options?: ScriptListOptions) {
  const query = new URLSearchParams()
  if (projectId) query.set('project_id', projectId)
  if (folderId !== undefined) query.set('folder_id', folderId || '')
  if (options?.limit !== undefined) query.set('limit', String(options.limit))
  if (options?.offset !== undefined) query.set('offset', String(options.offset))
  if (options?.search?.trim()) query.set('q', options.search.trim())
  if (options?.archiveState) query.set("archive_state", options.archiveState)
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

function buildScenarioQuery(projectId?: string) {
  if (!projectId) return ''
  const query = new URLSearchParams()
  query.set('project_id', projectId)
  return `?${query.toString()}`
}

export function useScriptApi() {
  const listScripts = useCallback(
    (projectId?: string, folderId?: string | null, options?: ScriptListOptions) =>
      request<ScriptListResponse>(`/scripts${buildScriptQuery(projectId, folderId, options)}`),
    [],
  )

  const createScript = useCallback(
    (script: ScriptDefinition, projectId?: string, folderId?: string | null, sourceRecordingSessionId?: string) =>
      request<StoredScript>('/scripts', {
        method: 'POST',
        body: JSON.stringify({
          script,
          project_id: projectId,
          folder_id: folderId,
          source_recording_session_id: sourceRecordingSessionId,
        }),
      }),
    [],
  )

  const getScript = useCallback((id: string) => request<StoredScript>(`/scripts/${id}`), [])

  const getScriptAuthoring = useCallback(async (id: string, projectId?: string, signal?: AbortSignal): Promise<ScriptAuthoringBaseline> => {
    const response = await requestWithResponse<unknown>(`/scripts/${encodeURIComponent(id)}?view=authoring`, {
      timeoutMs: 30_000, maxResponseBytes: 12 << 20, signal,
    })
    return admitScriptAuthoringBaseline(response.body, response.headers.get('ETag'), id, projectId)
  }, [])

  const updateScript = useCallback(async (id: string, script: ScriptDefinition, baseline: ScriptAuthoringBaseline): Promise<StoredScript> => {
    const admitted = admitScriptAuthoringBaseline(baseline?.projection, baseline?.etag ?? null, id, script.project_id || undefined)
    const response = await requestWithResponse<StoredScript>(`/scripts/${encodeURIComponent(id)}`, {
      method: 'PUT', headers: { 'If-Match': admitted.etag }, body: JSON.stringify({ script }),
      timeoutMs: 30_000, maxResponseBytes: 12 << 20,
    })
    if (response.headers.get('X-APEX-Script-Save-Mode') !== 'conditioned' || response.body?.id !== id) throw new ScriptSaveOutcomeUnknown()
    baselineFromSavedScript(response.body, admitted.projection.project_id)
    return response.body
  }, [])

  const deleteScript = useCallback((id: string) => request<DeleteScriptResponse>(`/scripts/${id}`, { method: 'DELETE' }), [])

  const replaceLibraryAssignment = useCallback(
    (kind: 'scripts' | 'scenarios', id: string, applicationId: string | null, releaseIds: string[], expectedRevision: number) =>
      kind === 'scripts'
        ? request<LibraryAssignment>(`/scripts/${encodeURIComponent(id)}/library-assignment`, { method: 'PUT', body: JSON.stringify({ application_id: applicationId, release_ids: releaseIds, expected_revision: expectedRevision }) })
        : request<LibraryAssignment>(`/scenarios/${encodeURIComponent(id)}/library-assignment`, { method: 'PUT', body: JSON.stringify({ application_id: applicationId, release_ids: releaseIds, expected_revision: expectedRevision }) }), [],
  )

  const moveScript = useCallback(
    (id: string, folderId: string | null) =>
      request<MoveScriptResponse>(`/scripts/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const validateScript = useCallback((id: string) => request<ScriptValidationResponse>(`/scripts/${id}/validate`, { method: 'POST' }), [])

  const getScriptSecretStatus = useCallback(
    (id: string, options?: { details?: boolean }) => request<ScriptSecretStatus>(`/scripts/${encodeURIComponent(id)}/secret-status${options?.details ? '?details=bindings' : ''}`),
    [],
  )

  const rebindScriptSecretReference = useCallback(
    (script: ScriptDefinition, oldName: string, newName: string, baseline?: ScriptAuthoringBaseline) => {
      const observed = baseline ? admitScriptAuthoringBaseline(baseline.projection, baseline.etag, script.id || '', script.project_id) : null
      return request<RebindScriptSecretReferenceResponse>('/scripts/secret-references/rebind', {
        method: 'POST',
        ...(observed ? { headers: { 'If-Match': observed.etag } } : {}),
        timeoutMs: 30_000,
        maxResponseBytes: 12 << 20,
        body: JSON.stringify({ script, old_name: oldName, new_name: newName }),
      }).then(response => {
        if (!observed) return response
        const authored = withoutScriptAuthoringReadContext(response.script)
        if (authored.id !== observed.projection.id || authored.project_id !== observed.projection.project_id) {
          throw new Error('The helper returned a different script or project. Your original draft is retained.')
        }
        return { ...response, script: authored }
      })
    },
    [],
  )

  const cloneScriptDraft = useCallback(
    (id: string, options?: { import_detected_credential_values?: boolean }) =>
      request<CloneScriptDraftResponse>(`/scripts/${encodeURIComponent(id)}/clone-draft`, {
        method: 'POST',
        body: JSON.stringify(options || {}),
      }),
    [],
  )

  const migrateImportSecrets = useCallback(
    (payload: MigrateImportSecretsRequest) =>
      request<MigrateImportSecretsResponse>('/scripts/import/secrets/migrate', {
        method: 'POST',
        body: JSON.stringify(payload),
      }),
    [],
  )

  const discardImportSecretSession = useCallback(
    (payload: DiscardImportSecretSessionRequest) =>
      request<void>('/scripts/import/secrets/discard', {
        method: 'POST',
        body: JSON.stringify(payload),
      }),
    [],
  )

  const validateDraftScript = useCallback(
    (script: ScriptDefinition, projectId?: string, folderId?: string | null, variables?: Record<string, string>) =>
      request<ScriptValidationResponse>('/scripts/validate', { method: 'POST', body: JSON.stringify({ script, project_id: projectId, folder_id: folderId, variables }) }),
    [],
  )

  const listAuthoringCapabilities = useCallback(
    (projectId?: string) => request<AuthoringCapabilitiesResponse>(`/scripts/authoring/capabilities${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`),
    [],
  )

  const listAuthoringTemplates = useCallback(
    (params?: { protocol?: string; use_case?: string; projectId?: string }) => {
      const query = new URLSearchParams()
      if (params?.protocol) query.set('protocol', params.protocol)
      if (params?.use_case) query.set('use_case', params.use_case)
      if (params?.projectId) query.set('project_id', params.projectId)
      const suffix = query.toString()
      return request<AuthoringTemplatesResponse>(`/scripts/authoring/templates${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

  const listAuthoringComponents = useCallback(
    (params?: { kind?: string; protocol?: string; projectId?: string }) => {
      const query = new URLSearchParams()
      if (params?.kind) query.set('kind', params.kind)
      if (params?.protocol) query.set('protocol', params.protocol)
      if (params?.projectId) query.set('project_id', params.projectId)
      const suffix = query.toString()
      return request<AuthoringComponentsResponse>(`/scripts/authoring/components${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

  // Save the current selection of actions as a project-scoped reusable block, or remove one.
  const createAuthoringComponent = useCallback(
    (payload: AuthoringComponent & { project_id: string }) =>
      request<AuthoringComponent>('/scripts/authoring/components', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const deleteAuthoringComponent = useCallback(
    (id: string) => request<{ deleted: string }>(`/scripts/authoring/components/${id}`, { method: 'DELETE' }),
    [],
  )

  const listAuthoringSessions = useCallback(
    (projectId?: string) => request<AuthoringSessionsResponse>(`/scripts/authoring/sessions${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`),
    [],
  )

  const createAuthoringSession = useCallback(
    (payload: CreateAuthoringSessionRequest) => request<AuthoringSession>('/scripts/authoring/sessions', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const approveAuthoringSession = useCallback(
    (id: string) => request<AuthoringSessionApprovalResponse>(`/scripts/authoring/sessions/${id}/approve`, { method: 'POST' }),
    [],
  )

  const deleteAuthoringSession = useCallback(
    (id: string) => request<{ deleted: string }>(`/scripts/authoring/sessions/${id}`, { method: 'DELETE' }),
    [],
  )

  const previewAuthoringCorrelation = useCallback(
    (id: string, payload: AuthoringCorrelationPreviewRequest) =>
      request<AuthoringCorrelationPreviewResponse>(`/scripts/authoring/sessions/${id}/correlations/preview`, { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const getScriptRecordingStatus = useCallback(
    (id: string) => request<ScriptRecordingStatus>(`/scripts/${id}/recording/status`),
    [],
  )

  const diagnoseScriptReplay = useCallback(
    (id: string, replay: CorrelationStep[], replaySessionId?: string) =>
      request<CorrelationDiagnoseResponse>(`/scripts/${id}/diagnose-replay`, {
        method: 'POST',
        body: JSON.stringify({ replay, replay_session_id: replaySessionId || undefined }),
      }),
    [],
  )

  const diagnoseCorrelations = useCallback(
    (recorded: CorrelationStep[], replay: CorrelationStep[]) =>
      request<CorrelationDiagnoseResponse>('/correlations/diagnose', { method: 'POST', body: JSON.stringify({ recorded, replay }) }),
    [],
  )

  const replayScript = useCallback(
    (id: string, payload?: { group_name?: string; project_id?: string; replay_session_id?: string; variables?: Record<string, string>; ignore_think_time?: boolean; script?: ScriptDefinition }) =>
      request<ScriptReplayResponse>(`/scripts/${id}/replay`, { method: 'POST', body: JSON.stringify(payload || {}) }),
    [],
  )

  const replayDraftScript = useCallback(
    (payload: { script: ScriptDefinition; group_name?: string; project_id?: string; replay_session_id?: string; variables?: Record<string, string>; ignore_think_time?: boolean }) =>
      request<ScriptReplayResponse>('/scripts/replay', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const startBrowserReplaySession = useCallback(
    (id: string, payload?: BrowserReplayStartRequest) =>
      request<BrowserReplayStartResponse>(`/scripts/${id}/browser-replay/sessions`, { method: 'POST', body: JSON.stringify(payload || {}) }),
    [],
  )

  const startDraftBrowserReplaySession = useCallback(
    (payload: BrowserReplayStartRequest & { script: ScriptDefinition }) =>
      request<BrowserReplayStartResponse>('/scripts/browser-replay/sessions', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const startScriptDebugSession = useCallback(
    (id: string, payload?: ScriptDebugStartRequest) => {
      const body: StoredScriptDebugStartBody = payload || {}
      return request<ScriptDebugStartResponse>(`/scripts/${id}/debug/sessions`, { method: 'POST', body: JSON.stringify(body) })
    },
    [],
  )

  const startDraftScriptDebugSession = useCallback(
    (payload: ScriptDebugStartRequest & { script: ScriptDefinition }) => {
      const body: DraftScriptDebugStartBody = payload
      return request<ScriptDebugStartResponse>('/scripts/debug/sessions', { method: 'POST', body: JSON.stringify(body) })
    },
    [],
  )

  const previewBrowserToHTTP = useCallback(
    (payload: BrowserToHTTPPreviewRequest) =>
      request<BrowserToHTTPPreviewResponse>('/scripts/browser-to-http/preview', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const getBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}`),
    [],
  )

  const stopBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/stop`, { method: 'POST' }),
    [],
  )

  const pauseBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/pause`, { method: 'POST' }),
    [],
  )

  const resumeBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/resume`, { method: 'POST' }),
    [],
  )

  const getScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}`),
    [],
  )

  const stopScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/stop`, { method: 'POST' }),
    [],
  )

  const pauseScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/pause`, { method: 'POST' }),
    [],
  )

  const resumeScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/resume`, { method: 'POST' }),
    [],
  )

  const stepScriptDebugSession = useCallback(
    (sessionId: string, mode: ScriptDebugStepMode = 'into') => {
      const body: ScriptDebugStepRequestBody = { mode }
      return request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/step`, { method: 'POST', body: JSON.stringify(body) })
    },
    [],
  )

  const updateScriptDebugBreakpoints = useCallback(
    (sessionId: string, breakpoints: ScriptDebugBreakpoint[]) => {
      const transportBreakpoints: ScriptDebugBreakpointsRequestBody['breakpoints'] = breakpoints.map(breakpoint => ({
        ...breakpoint,
        phase: breakpoint.phase as ScriptDebugBreakpointsRequestBody['breakpoints'][number]['phase'],
      }))
      const body: ScriptDebugBreakpointsRequestBody = { breakpoints: transportBreakpoints }
      return request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/breakpoints`, { method: 'PATCH', body: JSON.stringify(body) })
    },
    [],
  )

  const updateScriptDebugOptions = useCallback(
    (sessionId: string, options: { break_on_failure?: boolean }) => {
      // Existing callers may send an empty object; retain that behavior while
      // deriving the accepted field and value type from the generated body.
      const body: Partial<ScriptDebugOptionsRequestBody> = options
      return request<ScriptDebugSessionResponse>(`/replay/sessions/${sessionId}/options`, { method: 'PATCH', body: JSON.stringify(body) })
    },
    [],
  )

  const exportScriptDebugSession = useCallback(
    async (sessionId: string, format: 'json' | 'log' = 'json') => {
      const res = await apiFetch(`/replay/sessions/${sessionId}/export?format=${encodeURIComponent(format)}`)
      if (!res.ok) {
        throw new Error(res.statusText || 'Failed to export script debugger session')
      }
      return format === 'log' ? res.text() : res.blob()
    },
    [],
  )

  const exportScript = useCallback(
    (id: string, outputDir?: string, filename?: string) =>
      request<ScriptExportResponse>(`/scripts/${id}/export`, {
        method: 'POST',
        body: JSON.stringify({ output_dir: outputDir, filename }),
      }),
    [],
  )

  const downloadScript = useCallback(async (id: string) => {
    const res = await apiFetch(`/scripts/${encodeURIComponent(id)}/download`)
    if (!res.ok) {
      throw new Error(res.statusText || 'Failed to download script')
    }
    return res.text()
  }, [])

  const importEnterpriseArchive = useCallback(
    async (file: File, options?: EnterpriseArchiveImportOptions): Promise<EnterpriseArchiveImportResult> => {
      const formData = new FormData()
      // The server authorizes scope before accepting archive bytes.
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.folder_files?.length) {
        options.folder_files.forEach(folderFile => {
          formData.append(`file:${encodeURIComponent(importFileLogicalPath(folderFile))}`, folderFile, folderFile.name)
        })
      } else formData.append('file', file)
      if (options?.har_file) formData.append('har_file', options.har_file)
      if (options?.supplemental_files?.length) {
        options.supplemental_files.forEach((supplementalFile) => {
          formData.append('supplemental_file', supplementalFile)
        })
      }
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))
      if (options?.migration_block_statuses?.length) formData.append('migration_block_statuses', options.migration_block_statuses.join(','))
      if (options?.archive_member) formData.append('archive_member', options.archive_member)
      if (options?.salesforce_native) formData.append('salesforce_native', 'true')
      if (options?.expire_draft) formData.append('expire_draft', 'true')

      // request keeps the deadline active through JSON body consumption.
      return request<EnterpriseArchiveImportResult>('/scripts/import/enterprise-script-archive', {
        method: 'POST',
        body: formData,
        timeoutMs: enterpriseArchiveImportTimeoutMs,
      }).catch(rethrowEnterpriseArchiveReviewError)
    },
    [],
  )

  const validateEnterpriseArchiveImport = useCallback(
    async (file: File, options?: EnterpriseArchiveImportOptions): Promise<EnterpriseArchiveImportResult> => {
      const formData = new FormData()
      // The server authorizes scope before accepting archive bytes.
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.folder_files?.length) {
        options.folder_files.forEach(folderFile => {
          formData.append(`file:${encodeURIComponent(importFileLogicalPath(folderFile))}`, folderFile, folderFile.name)
        })
      } else formData.append('file', file)
      if (options?.har_file) formData.append('har_file', options.har_file)
      if (options?.supplemental_files?.length) {
        options.supplemental_files.forEach((supplementalFile) => {
          formData.append('supplemental_file', supplementalFile)
        })
      }
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))
      if (options?.migration_block_statuses?.length) formData.append('migration_block_statuses', options.migration_block_statuses.join(','))
      if (options?.archive_member) formData.append('archive_member', options.archive_member)
      if (options?.salesforce_native) formData.append('salesforce_native', 'true')
      if (options?.expire_draft) formData.append('expire_draft', 'true')

      return request<EnterpriseArchiveImportResult>('/scripts/import/enterprise-script-archive/validate', {
        method: 'POST',
        body: formData,
        timeoutMs: enterpriseArchiveImportTimeoutMs,
      }).catch(rethrowEnterpriseArchiveReviewError)
    },
    [],
  )

  const getEnterpriseArchiveSupportMatrix = useCallback(
    () => request<EnterpriseArchiveSupportMatrixResponse>('/scripts/import/enterprise-script-archive/support-matrix'),
    [],
  )

  const importJMeter = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; scenario_draft?: boolean; project_id?: string; time_mode?: string; timezone?: string; variable_bindings?: Record<string, string> }): Promise<JMeterImportResult> => {
      const formData = new FormData()
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.scenario_draft) formData.append('scenario_draft', 'true')
      if (options?.time_mode) formData.append('time_mode', options.time_mode)
      if (options?.timezone) formData.append('timezone', options.timezone)
      if (options?.variable_bindings) formData.append('variable_bindings', JSON.stringify(options.variable_bindings))

      const res = await apiFetch('/scripts/import/jmeter', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<JMeterImportResult>
    },
    [],
  )

  const validateJMeterImport = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; scenario_draft?: boolean; project_id?: string; time_mode?: string; timezone?: string; variable_bindings?: Record<string, string> }): Promise<JMeterImportResult> => {
      const formData = new FormData()
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.scenario_draft) formData.append('scenario_draft', 'true')
      if (options?.time_mode) formData.append('time_mode', options.time_mode)
      if (options?.timezone) formData.append('timezone', options.timezone)
      if (options?.variable_bindings) formData.append('variable_bindings', JSON.stringify(options.variable_bindings))

      const res = await apiFetch('/scripts/import/jmeter/validate', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<JMeterImportResult>
    },
    [],
  )

  const getJMeterSupportMatrix = useCallback(
    () => request<JMeterSupportMatrixResponse>('/scripts/import/jmeter/support-matrix'),
    [],
  )

  const getDevWebSupportMatrix = useCallback(
    () => request<DevWebSupportMatrixResponse>('/scripts/import/devweb/support-matrix'),
    [],
  )

  const getK6SupportMatrix = useCallback(
    () => request<K6SupportMatrixResponse>('/scripts/import/k6/support-matrix'),
    [],
  )

  const getCloudTestSupportMatrix = useCallback(
    () => request<CloudTestSupportMatrixResponse>('/scripts/import/cloudtest/support-matrix'),
    [],
  )

  const getRAMLSupportMatrix = useCallback(
    () => request<RAMLSupportMatrixResponse>('/scripts/import/raml/support-matrix'),
    [],
  )

  const getOpenAPISupportMatrix = useCallback(
    () => request<OpenAPISupportMatrixResponse>('/scripts/import/openapi/support-matrix'),
    [],
  )

  const getAsyncAPISupportMatrix = useCallback(
    () => request<AsyncAPISupportMatrixResponse>('/scripts/import/asyncapi/support-matrix'),
    [],
  )

  const getPostmanSupportMatrix = useCallback(
    () => request<PostmanSupportMatrixResponse>('/scripts/import/postman/support-matrix'),
    [],
  )

  const getEventHubsCaptureSupportMatrix = useCallback(
    () => request<EventHubsCaptureSupportMatrixResponse>('/scripts/import/eventhubs-capture/support-matrix'),
    [],
  )

  const getBrokerConfigSupportMatrix = useCallback(
    () => request<BrokerConfigSupportMatrixResponse>('/scripts/import/broker-config/support-matrix'),
    [],
  )

  const getOracleEBusinessSupportMatrix = useCallback(
    () => request<OracleEBusinessSupportMatrixResponse>('/scripts/import/oracle-ebusiness/support-matrix'),
    [],
  )

  const repairJMeterBatch = useCallback(
    (payload: JMeterBatchRepairRequest, apply = false) => apply
      ? request<JMeterBatchRepairResponse>('/scripts/import/repairs/apply', { method: 'POST', body: JSON.stringify(payload) })
      : request<JMeterBatchRepairResponse>('/scripts/import/repairs/preview', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const saveJMeterImport = useCallback(
    (payload: SaveJMeterImportRequest) =>
      request<SaveJMeterImportResponse>('/scripts/import/jmeter/save', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const saveEnterpriseArchiveImport = useCallback(
    (payload: SaveEnterpriseArchiveImportRequest) =>
      request<SaveEnterpriseArchiveImportResponse>('/scripts/import/enterprise-script-archive/save', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const savePostmanImport = useCallback(
    (payload: SavePostmanImportRequest) =>
      request<SavePostmanImportResponse>('/scripts/import/postman/save', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const commitOpenAPIImport = useCallback(
    (payload: CommitOpenAPIImportRequest) =>
      request<CommitOpenAPIImportResponse>('/scripts/import/openapi/commit', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const commitAsyncAPIImport = useCallback(
    (payload: CommitAsyncAPIImportRequest) =>
      request<CommitAsyncAPIImportResponse>('/scripts/import/asyncapi/commit', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const commitAsyncAPIImportBatch = useCallback(
    (payload: CommitAsyncAPIImportBatchRequest) =>
      request<CommitAsyncAPIImportBatchResponse>('/scripts/import/asyncapi/commit-batch', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const commitGenericImportBatch = useCallback(
    (payload: CommitGenericImportBatchRequest) =>
      request<CommitGenericImportBatchResponse>('/scripts/import/commit-batch', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const importSample = useCallback(
    (data: {
      name?: string
      project_id?: string
      format: string
      content: string
      response_content?: string
      options?: {
        base_url?: string
        include_assertions?: boolean
        infer_extractors?: boolean
        sanitize_sensitive?: boolean
        include_static_assets?: boolean
      }
    }) => request<SampleImportResult>('/scripts/import/sample', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const uploadImportAsset = useCallback(
    async (importId: string, file: File, options?: { project_id?: string; name?: string; asset_name?: string; content_type?: string }): Promise<ImportAssetUploadResponse> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.name) formData.append('name', options.name)
      if (options?.asset_name) formData.append('asset_name', options.asset_name)
      if (options?.content_type) formData.append('content_type', options.content_type)
      const query = options?.project_id ? `?project_id=${encodeURIComponent(options.project_id)}` : ''
      const res = await apiFetch(`/scripts/import/${encodeURIComponent(importId)}/assets/upload${query}`, {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<ImportAssetUploadResponse>
    },
    [],
  )

  const uploadJavaClasspathJar = useCallback(
    async (importId: string, file: File, options: { project_id: string; entry?: string }): Promise<JavaClasspathAssetUploadResponse> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options.entry) formData.append('entry', options.entry)
      formData.append('project_id', options.project_id)
      // project_id is passed in the query so authorization resolves without
      // consuming the multipart body (the server streams the JAR to disk).
      const query = `?project_id=${encodeURIComponent(options.project_id)}`
      const res = await apiFetch(`/scripts/import/${encodeURIComponent(importId)}/java-classpath/upload${query}`, {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<JavaClasspathAssetUploadResponse>
    },
    [],
  )

  const importDevWeb = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<DevWebImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/devweb', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<DevWebImportResult>
    },
    [],
  )

  const importK6 = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<K6ImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.name) formData.append('script_name', options.name)

      const res = await apiFetch('/scripts/import/k6', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<K6ImportResult>
    },
    [],
  )

  const importCloudTest = useCallback(
    async (file: File, options?: { project_id?: string }): Promise<CloudTestImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.project_id) formData.append('project_id', options.project_id)

      const res = await apiFetch('/scripts/import/cloudtest', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<CloudTestImportResult>
    },
    [],
  )

  const importEventHubsCapture = useCallback(
    async (file: File, options?: { project_id?: string; event_hub?: string; max_events?: number }): Promise<EventHubsCaptureImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.project_id) formData.append('project_id', options.project_id)
      const query = new URLSearchParams()
      if (options?.event_hub) query.set('event_hub', options.event_hub)
      if (typeof options?.max_events === 'number') query.set('max_events', String(options.max_events))
      const suffix = query.toString()

      const res = await apiFetch(`/scripts/import/eventhubs-capture${suffix ? `?${suffix}` : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<EventHubsCaptureImportResult>
    },
    [],
  )

  const importBrokerConfig = useCallback(
    async (file: File, options?: { project_id?: string; format?: string; max_destinations?: number }): Promise<BrokerConfigImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.project_id) formData.append('project_id', options.project_id)
      const query = new URLSearchParams()
      if (options?.format) query.set('format', options.format)
      if (typeof options?.max_destinations === 'number') query.set('max_destinations', String(options.max_destinations))
      const suffix = query.toString()

      const res = await apiFetch(`/scripts/import/broker-config${suffix ? `?${suffix}` : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<BrokerConfigImportResult>
    },
    [],
  )

  const importHAR = useCallback(
    async (file: File, options?: {
      name?: string
      base_url?: string
      project_id?: string
      include_static_assets?: boolean
      secondary_file?: File
      artifact_mode?: string
      retention_days?: number
      redact_sensitive?: boolean
      salesforce_native?: boolean
      agentforce_transform?: boolean
      salvage_truncated?: boolean
    }): Promise<HARImportResult> => {
      const formData = new FormData()
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_static_assets) formData.append('include_static_assets', 'true')
      if (options?.secondary_file) formData.append('secondary_file', options.secondary_file)
      if (options?.artifact_mode) formData.append('artifact_mode', options.artifact_mode)
      if (typeof options?.retention_days === 'number') formData.append('retention_days', String(options.retention_days))
      if (typeof options?.redact_sensitive === 'boolean') formData.append('redact_sensitive', options.redact_sensitive ? 'true' : 'false')
      if (options?.salvage_truncated) formData.append('salvage_truncated', 'true')

      const query = new URLSearchParams()
      if (options?.project_id) query.set('project_id', options.project_id)
      if (options?.salesforce_native === false) query.set('salesforce_native', 'false')
      if (options?.agentforce_transform) query.set('agentforce_transform', 'true')
      const suffix = query.toString()
      const res = await apiFetch(`/scripts/import/har${suffix ? `?${suffix}` : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<HARImportResult>
    },
    [],
  )

  const importSalesforce = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string; agentforce_transform?: boolean }): Promise<SalesforceImportResult> => {
      const formData = new FormData()
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const query = new URLSearchParams()
      if (options?.project_id) query.set('project_id', options.project_id)
      if (options?.agentforce_transform) query.set('agentforce_transform', 'true')
      const suffix = query.toString()
      const res = await apiFetch(`/scripts/import/salesforce${suffix ? `?${suffix}` : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<SalesforceImportResult>
    },
    [],
  )

  const reviewOracleEBusiness = useCallback(
    (payload: OracleEBusinessReviewRequest) =>
      request<OracleEBusinessReviewResponse>('/scripts/import/oracle-ebusiness/review', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const importBrowserRecording = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<BrowserRecordingImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/recorder', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<BrowserRecordingImportResult>
    },
    [],
  )

  const validateBrowserRecordingImport = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<BrowserRecordingImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/recorder/validate', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<BrowserRecordingImportResult>
    },
    [],
  )

  const replayBrowserRecordingImport = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<BrowserRecordingImportResult & BrowserReplayStartResponse> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/recorder/replay', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<BrowserRecordingImportResult & BrowserReplayStartResponse>
    },
    [],
  )

  const importOpenAPI = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string; agentforce_transform?: boolean }): Promise<OpenAPIImportResult> => {
      const formData = new FormData()
      // Authorize the project before the server consumes uploaded bytes.
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch(`/scripts/import/openapi${options?.agentforce_transform ? '?agentforce_transform=true' : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<OpenAPIImportResult>
    },
    [],
  )

  const importRAML = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string; agentforce_transform?: boolean }): Promise<RAMLImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch(`/scripts/import/raml${options?.agentforce_transform ? '?agentforce_transform=true' : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<RAMLImportResult>
    },
    [],
  )

  const importPostmanUpload = useCallback(
    async (endpoint: '/scripts/import/postman' | '/scripts/import/postman/validate', file: File, options?: PostmanImportOptions): Promise<PostmanImportResult> => {
      const formData = new FormData()
      // Authorization reads only fields before the first file.
      if (options?.project_id) formData.append('project_id', options.project_id)
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.environment_file) formData.append('environment', options.environment_file)
      if (options?.include_assertions !== undefined) formData.append('include_assertions', String(options.include_assertions))
      if (options?.sanitize_sensitive !== undefined) formData.append('sanitize_sensitive', String(options.sanitize_sensitive))
      if (options?.use_parameter_data_source !== undefined) formData.append('use_parameter_data_source', String(options.use_parameter_data_source))
      if (options?.default_think_time_ms !== undefined) formData.append('default_think_time_ms', String(options.default_think_time_ms))

      const query = new URLSearchParams()
      if (options?.project_id) query.set('project_id', options.project_id)
      if (options?.agentforce_transform) query.set('agentforce_transform', 'true')
      const res = await apiFetch(`${endpoint}${query.size ? `?${query}` : ''}`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<PostmanImportResult>
    },
    [],
  )

  const importPostman = useCallback(
    (file: File, options?: PostmanImportOptions): Promise<PostmanImportResult> =>
      importPostmanUpload('/scripts/import/postman', file, options),
    [importPostmanUpload],
  )

  const validatePostmanImport = useCallback(
    (file: File, options?: PostmanImportOptions): Promise<PostmanImportResult> =>
      importPostmanUpload('/scripts/import/postman/validate', file, options),
    [importPostmanUpload],
  )

  const importWSDL = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; soap_version?: string; project_id?: string }): Promise<WSDLImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.soap_version) formData.append('soap_version', options.soap_version)

      const res = await apiFetch('/scripts/import/wsdl', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<WSDLImportResult>
    },
    [],
  )

  const importAsyncAPI = useCallback(
    async (file: File, options?: {
      name?: string
      project_id?: string
      brokers?: string
      channels?: string
      mock?: boolean
      include_assertions?: boolean
      default_think_time_ms?: number
      schema_registry_url?: string
      schema_id?: number
      subject?: string
      serialization?: string
    }): Promise<AsyncAPIImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.brokers) formData.append('brokers', options.brokers)
      if (options?.channels) formData.append('channels', options.channels)
      if (typeof options?.mock === 'boolean') formData.append('mock', String(options.mock))
      if (typeof options?.include_assertions === 'boolean') formData.append('include_assertions', String(options.include_assertions))
      if (typeof options?.default_think_time_ms === 'number') formData.append('default_think_time_ms', String(options.default_think_time_ms))
      if (options?.schema_registry_url) formData.append('schema_registry_url', options.schema_registry_url)
      if (typeof options?.schema_id === 'number' && Number.isFinite(options.schema_id) && options.schema_id > 0) formData.append('schema_id', String(options.schema_id))
      if (options?.subject) formData.append('subject', options.subject)
      if (options?.serialization) formData.append('serialization', options.serialization)

      const res = await apiFetch('/scripts/import/asyncapi', { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<AsyncAPIImportResult>
    },
    [],
  )

  const getBrunoSupportMatrix = useCallback(
    () => request<BrunoSupportMatrixResponse>('/scripts/import/bruno/support-matrix'),
    [],
  )

  const inspectBrunoImport = useCallback(
    async (
      files: File[],
      options: {
        project_id: string
        output_mode?: 'canonical' | 'native_requests'
        name?: string
        sandbox?: 'safe' | 'developer'
        sync_script_id?: string
        run_options?: {
          default_environment?: string
          default_global_environment?: string
          selection?: string
          paths?: string[]
          include_tags?: string[]
          exclude_tags?: string[]
          recursive?: boolean
          execution?: string
          bail?: boolean
          tests_only?: boolean
          delay_ms?: number
          stream_timeout_ms?: number
          persist_session?: boolean
        }
      },
    ): Promise<BrunoInspectResponse> => {
      if (files.length === 0) throw new Error('At least one Bruno source file is required.')
      const formData = new FormData()
      formData.append('project_id', options.project_id)
      if (options.name) formData.append('name', options.name)
      if (options.sandbox) formData.append('sandbox', options.sandbox)
      if (options.sync_script_id) formData.append('sync_script_id', options.sync_script_id)
      const inspectOptions = {
        ...(options.run_options || {}),
        ...(options.output_mode ? { output_mode: options.output_mode } : {}),
      }
      if (Object.keys(inspectOptions).length > 0) formData.append('options', JSON.stringify(inspectOptions))
      const directoryUpload = files.length > 1 || files.some(file => Boolean(file.webkitRelativePath))
      files.forEach(file => {
        const relative = importFileLogicalPath(file)
        const field = directoryUpload ? `file:${encodeURIComponent(relative)}` : 'file'
        formData.append(field, file, file.name)
      })
	      const response = await apiFetch(`/scripts/import/bruno/inspect?project_id=${encodeURIComponent(options.project_id)}`, { method: 'POST', body: formData })
      if (!response.ok) {
        const payload = await response.json().catch(() => ({ error: response.statusText }))
        const message = typeof payload?.error === 'string' ? payload.error : response.statusText
        throw new ApiError(message, response.status, payload)
      }
      return response.json() as Promise<BrunoInspectResponse>
    },
    [],
  )

  const getBrunoImportSessionSource = useCallback(
    async (importSessionId: string, projectId: string, sourcePath: string): Promise<string> => {
      const query = new URLSearchParams({ project_id: projectId, path: sourcePath })
      const response = await apiFetch(`/scripts/import/bruno/sessions/${encodeURIComponent(importSessionId)}/source?${query.toString()}`)
      if (!response.ok) {
        const payload = await response.json().catch(() => ({ error: response.statusText }))
        const message = typeof payload?.error === 'string' ? payload.error : response.statusText
        throw new ApiError(message, response.status, payload)
      }
      return response.text()
    },
    [],
  )

  const commitBrunoImport = useCallback(
    async (payload: CommitBrunoImportRequest): Promise<CommitBrunoImportResponse> => {
      const response = await apiFetch('/scripts/import/bruno/commit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        const errorPayload = await response.json().catch(() => ({ error: response.statusText }))
        const message = typeof errorPayload?.error === 'string' ? errorPayload.error : response.statusText
        throw new ApiError(message, response.status, errorPayload)
      }
      const result = await response.json() as CommitBrunoImportResponse
      if (!result || typeof result !== 'object') {
        throw new Error('Bruno commit response must be a JSON object.')
      }
      return {
        ...result,
        _http_status: response.status,
      } as CommitBrunoImportResponse
    },
    [],
  )

  const listBrunoRevisions = useCallback(
    (scriptId: string) =>
      request<{ revisions: ImportSourceRevision[]; count: number }>(`/scripts/${scriptId}/imports/revisions`),
    [],
  )

  const rollbackBrunoRevision = useCallback(
    (scriptId: string, revisionId: string, expectedActiveRevisionId?: string) =>
      request<{ revision: ImportSourceRevision }>(`/scripts/${scriptId}/imports/rollback`, {
        method: 'POST',
        body: JSON.stringify({
          revision_id: revisionId,
          expected_active_revision_id: expectedActiveRevisionId,
        }),
      }),
    [],
  )

  const previewBrunoProjection = useCallback(
    (scriptId: string) =>
      request<BrunoProjectionPreview>(`/scripts/${scriptId}/bruno-projection/preview`, { method: 'POST' }),
    [],
  )

  const commitBrunoProjection = useCallback(
    (scriptId: string) =>
      request<Record<string, unknown>>(`/scripts/${scriptId}/bruno-projection/commit`, { method: 'POST' }),
    [],
  )

  const applyImportParameterization = useCallback(
    (payload: ApplyParameterizationRequest) =>
      request<ApplyParameterizationResponse>('/scripts/import/parameterize/apply', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  // Collection-wide values entered once during import review. Unlike
  // parameterization, this applies across every script of a multi-script import
  // in one round trip, because one value can touch all of them.
  const applyImportSharedValues = useCallback(
    (payload: ApplySharedValuesRequest) =>
      request<ApplySharedValuesResponse>('/scripts/import/shared-values/apply', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const applySecretBindings = useCallback(
    (payload: ApplySecretBindingsRequest) =>
      request<ApplySecretBindingsResponse>('/scripts/secret-bindings/apply', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  // Run the deterministic parameterization engine over a saved script (editor "Deep scan").
  // The note explains correlations are out of scope without captured observations.
  const suggestParameterization = useCallback(
    (scriptId: string) =>
      request<Parameterization & { note?: string }>(`/scripts/${scriptId}/parameterize/suggest`, { method: 'POST' }),
    [],
  )

  const listScenarios = useCallback((projectId?: string, archiveState?: "active" | "archived" | "all") => request<ScenarioListResponse>(`/scenarios${buildScenarioQuery(projectId)}${archiveState ? `${projectId ? "&" : "?"}archive_state=${archiveState}` : ""}`), [])

  const createScenario = useCallback(
    (data: ScenarioUpsertRequest) => request<StoredScenario>('/scenarios', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const getScenario = useCallback((id: string) => request<StoredScenario>(`/scenarios/${id}`), [])

  const updateScenario = useCallback(
    (id: string, data: ScenarioUpsertRequest) => request<StoredScenario>(`/scenarios/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteScenario = useCallback((id: string) => request<ScenarioDeleteResponse>(`/scenarios/${id}`, { method: 'DELETE' }), [])

  const listScenarioResults = useCallback(
    (id: string, params?: { application_id?: string; release_id?: string }) => {
      const query = new URLSearchParams()
      if (params?.application_id) query.set('application_id', params.application_id)
      if (params?.release_id) query.set('release_id', params.release_id)
      const suffix = query.toString()
      return request<ScenarioResultsResponse>(`/scenarios/${id}/results${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

  const listDataPools = useCallback(
    (projectId?: string, includeArchived = false) => {
      const query = new URLSearchParams()
      if (projectId) query.set('project_id', projectId)
      if (includeArchived) query.set('include_archived', 'true')
      const suffix = query.toString()
      return request<DataPoolListResponse>(`/data-pools${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

  const createDataPool = useCallback(
    (data: DataPoolUpsertRequest) => request<SharedDataPool>('/data-pools', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const getDataPool = useCallback((id: string, page?: { limit?: number; offset?: number; cursor?: string }) => {
    const query = new URLSearchParams()
    if (page?.limit !== undefined) query.set('limit', String(page.limit))
    if (page?.offset !== undefined) query.set('offset', String(page.offset))
    // An explicitly empty cursor selects the first keyset page. Omitting the
    // field preserves the legacy bounded details request for other consumers.
    if (page?.cursor !== undefined) query.set('cursor', page.cursor)
    const suffix = query.toString()
    return request<DataPoolDetailsResponse>(`/data-pools/${id}${suffix ? `?${suffix}` : ''}`)
  }, [])

  const updateDataPool = useCallback(
    (id: string, data: DataPoolUpdateRequest) => request<SharedDataPool>(`/data-pools/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteDataPool = useCallback((id: string) => request<DataPoolDeleteResponse>(`/data-pools/${id}`, { method: 'DELETE' }), [])

  const createDataPoolRow = useCallback(
    (poolId: string, data: DataPoolRowUpsertRequest) => request<SharedDataPoolRow>(`/data-pools/${poolId}/rows`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateDataPoolRow = useCallback(
    (poolId: string, rowId: string, data: DataPoolRowUpdateRequest) => request<SharedDataPoolRow>(`/data-pools/${poolId}/rows/${rowId}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteDataPoolRow = useCallback((poolId: string, rowId: string) => request<DataPoolDeleteResponse>(`/data-pools/${poolId}/rows/${rowId}`, { method: 'DELETE' }), [])

  const importDataPoolRows = useCallback(
    async (poolId: string, file: File, options?: DataPoolImportOptions): Promise<DataPoolImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.mode) formData.append('mode', options.mode)
      if (options?.delimiter) formData.append('delimiter', options.delimiter)
      if (options?.header === false) formData.append('header', 'false')
      if (options?.columns?.length) formData.append('columns', options.columns.join(','))
      if (options?.dryRun) formData.append('dry_run', 'true')
      const res = await apiFetch(`/data-pools/${poolId}/rows/import`, { method: 'POST', body: formData })
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      return res.json() as Promise<DataPoolImportResult>
    },
    [],
  )

  const exportDataPoolRows = useCallback(
    async (poolId: string, status?: 'active' | 'all'): Promise<{ blob: Blob; filename: string | null }> => {
      const query = status && status !== 'all' ? `?status=${status}` : ''
      const res = await apiFetch(`/data-pools/${poolId}/rows/export${query}`)
      if (!res.ok) {
        throw new Error(await errorMessageFromResponse(res))
      }
      const filename = downloadFilenameFromDisposition(res.headers.get('Content-Disposition'), '')
      return { blob: await res.blob(), filename: filename || null }
    },
    [],
  )

  const listScenarioDataPools = useCallback((scenarioId: string) => request<ScenarioDataPoolResponse>(`/scenarios/${scenarioId}/data-pools`), [])

  const updateScenarioDataPools = useCallback(
    (scenarioId: string, dataPools: ScenarioDataPoolBinding[]) => request<ScenarioDataPoolResponse>(`/scenarios/${scenarioId}/data-pools`, { method: 'PUT', body: JSON.stringify({ data_pools: dataPools }) }),
    [],
  )

  return useMemo(
    () => ({
      listScripts,
      createScript,
      getScript,
      getScriptAuthoring,
      updateScript,
      deleteScript,
      moveScript,
      replaceLibraryAssignment,
      validateScript,
      getScriptSecretStatus,
      rebindScriptSecretReference,
      cloneScriptDraft,
      migrateImportSecrets,
      discardImportSecretSession,
      validateDraftScript,
      listAuthoringCapabilities,
      listAuthoringTemplates,
      listAuthoringComponents,
      createAuthoringComponent,
      deleteAuthoringComponent,
      listAuthoringSessions,
      createAuthoringSession,
      approveAuthoringSession,
      deleteAuthoringSession,
      previewAuthoringCorrelation,
      getScriptRecordingStatus,
      diagnoseScriptReplay,
      diagnoseCorrelations,
      replayScript,
      replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      startScriptDebugSession,
      startDraftScriptDebugSession,
      previewBrowserToHTTP,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      getScriptDebugSession,
      stopScriptDebugSession,
      pauseScriptDebugSession,
      resumeScriptDebugSession,
      stepScriptDebugSession,
      updateScriptDebugBreakpoints,
      updateScriptDebugOptions,
      exportScriptDebugSession,
      exportScript,
      downloadScript,
      importEnterpriseArchive,
      validateEnterpriseArchiveImport,
      getEnterpriseArchiveSupportMatrix,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      getDevWebSupportMatrix,
		getK6SupportMatrix,
		getCloudTestSupportMatrix,
      getRAMLSupportMatrix,
      getOpenAPISupportMatrix,
      getAsyncAPISupportMatrix,
      getPostmanSupportMatrix,
      getEventHubsCaptureSupportMatrix,
      getBrokerConfigSupportMatrix,
      getOracleEBusinessSupportMatrix,
      saveJMeterImport,
      repairJMeterBatch,
      saveEnterpriseArchiveImport,
      savePostmanImport,
      commitOpenAPIImport,
      commitAsyncAPIImport,
      commitAsyncAPIImportBatch,
      commitGenericImportBatch,
      importSample,
      uploadImportAsset,
      uploadJavaClasspathJar,
      importDevWeb,
		importK6,
		importCloudTest,
      importEventHubsCapture,
      importBrokerConfig,
      importHAR,
      importSalesforce,
      reviewOracleEBusiness,
      importBrowserRecording,
      validateBrowserRecordingImport,
      replayBrowserRecordingImport,
      importOpenAPI,
      importRAML,
      importPostman,
      validatePostmanImport,
      importWSDL,
      importAsyncAPI,
      getBrunoSupportMatrix,
      inspectBrunoImport,
      getBrunoImportSessionSource,
      commitBrunoImport,
      listBrunoRevisions,
      rollbackBrunoRevision,
      previewBrunoProjection,
      commitBrunoProjection,
      applyImportParameterization,
      applyImportSharedValues,
      applySecretBindings,
      suggestParameterization,
      listScenarios,
      createScenario,
      getScenario,
      updateScenario,
      deleteScenario,
      listScenarioResults,
      listDataPools,
      createDataPool,
      getDataPool,
      updateDataPool,
      deleteDataPool,
      createDataPoolRow,
      updateDataPoolRow,
      deleteDataPoolRow,
      importDataPoolRows,
      exportDataPoolRows,
      listScenarioDataPools,
      updateScenarioDataPools,
    }),
    [
      listScripts,
      createScript,
      getScript,
      getScriptAuthoring,
      updateScript,
      deleteScript,
      moveScript,
      replaceLibraryAssignment,
      validateScript,
      getScriptSecretStatus,
      rebindScriptSecretReference,
      cloneScriptDraft,
      migrateImportSecrets,
      discardImportSecretSession,
      validateDraftScript,
      listAuthoringCapabilities,
      listAuthoringTemplates,
      listAuthoringComponents,
      createAuthoringComponent,
      deleteAuthoringComponent,
      listAuthoringSessions,
      createAuthoringSession,
      approveAuthoringSession,
      deleteAuthoringSession,
      previewAuthoringCorrelation,
      getScriptRecordingStatus,
      diagnoseScriptReplay,
      diagnoseCorrelations,
      replayScript,
      replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      startScriptDebugSession,
      startDraftScriptDebugSession,
      previewBrowserToHTTP,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      getScriptDebugSession,
      stopScriptDebugSession,
      pauseScriptDebugSession,
      resumeScriptDebugSession,
      stepScriptDebugSession,
      updateScriptDebugBreakpoints,
      updateScriptDebugOptions,
      exportScriptDebugSession,
      exportScript,
      downloadScript,
      importEnterpriseArchive,
      validateEnterpriseArchiveImport,
      getEnterpriseArchiveSupportMatrix,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      getDevWebSupportMatrix,
      getK6SupportMatrix,
      getCloudTestSupportMatrix,
      getRAMLSupportMatrix,
      getOpenAPISupportMatrix,
      getAsyncAPISupportMatrix,
      getPostmanSupportMatrix,
      getEventHubsCaptureSupportMatrix,
      getBrokerConfigSupportMatrix,
      getOracleEBusinessSupportMatrix,
      saveJMeterImport,
      repairJMeterBatch,
      saveEnterpriseArchiveImport,
      savePostmanImport,
      commitOpenAPIImport,
      commitAsyncAPIImport,
      commitAsyncAPIImportBatch,
      commitGenericImportBatch,
      importSample,
      uploadImportAsset,
      uploadJavaClasspathJar,
      importDevWeb,
      importK6,
      importCloudTest,
      importEventHubsCapture,
      importBrokerConfig,
      importHAR,
      importSalesforce,
      reviewOracleEBusiness,
      importBrowserRecording,
      validateBrowserRecordingImport,
      replayBrowserRecordingImport,
      importOpenAPI,
      importRAML,
      importPostman,
      validatePostmanImport,
      importWSDL,
      importAsyncAPI,
      getBrunoSupportMatrix,
      inspectBrunoImport,
      getBrunoImportSessionSource,
      commitBrunoImport,
      listBrunoRevisions,
      rollbackBrunoRevision,
      previewBrunoProjection,
      commitBrunoProjection,
      applyImportParameterization,
      applyImportSharedValues,
      applySecretBindings,
      suggestParameterization,
      listScenarios,
      createScenario,
      getScenario,
      updateScenario,
      deleteScenario,
      listScenarioResults,
      listDataPools,
      createDataPool,
      getDataPool,
      updateDataPool,
      deleteDataPool,
      createDataPoolRow,
      updateDataPoolRow,
      deleteDataPoolRow,
      importDataPoolRows,
      exportDataPoolRows,
      listScenarioDataPools,
      updateScenarioDataPools,
    ],
  )
}
