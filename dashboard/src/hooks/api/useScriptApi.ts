import { useCallback, useMemo } from 'react'

import {
  AuthoringComponent,
  BrowserReplayStartRequest,
  BrowserReplayStartResponse,
  BrowserReplaySessionResponse,
  AuthoringHealth,
  AuthoringCorrelationPreviewRequest,
  AuthoringCorrelationPreviewResponse,
  AuthoringSession,
  BrowserReplayNetworkEntry,
  ScriptDebugBreakpoint,
  ScriptDebugSessionResponse,
  ScriptDebugStartRequest,
  ScriptDebugStartResponse,
  GeneratorAssignmentSettings,
  GoalConfig,
  RuntimeSettingsOverride,
  ScenarioDataPoolBinding,
  ScenarioMode,
  SharedDataPool,
  SharedDataPoolRow,
  StoredScenario,
  StoredScript,
  TestSummary,
} from '../../types'
import { type AuthoringTemplate } from '../../types'
import { type ScriptDefinition, type ScriptExportResponse, type ScriptReplayResponse, type ScriptValidationResponse } from '../../types/scripts'
import { type ApplyParameterizationRequest, type ApplyParameterizationResponse, type AsyncAPIImportResult, type BrowserRecordingImportResult, type DevWebImportResult, type HARImportResult, type JMeterImportResult, type JMeterSupportMatrixResponse, type EnterpriseArchiveImportResult, type EnterpriseArchiveSupportMatrixResponse, type OpenAPIImportResult, type PostmanImportResult, type RAMLImportResult, type SalesforceImportResult, type SampleImportResult, type SaveJMeterImportRequest, type SaveJMeterImportResponse, type WSDLImportResult } from '../../types/scriptImports'
import { apiFetch, request } from '../apiClient'

type ScriptListResponse = {
  scripts: StoredScript[]
  count: number
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

type ScenarioGroupPayload = {
  name?: string
  script_id: string
  role?: 'load' | 'observer'
  vusers: number
  ramp_up?: string
  ramp_down?: string
  stages?: Array<{
    name?: string
    type: string
    duration: string
    target_vusers?: number
    target_tps?: number
  }>
  initialization?: 'at_runtime' | 'at_once'
}

type ScenarioUpsertRequest = {
  name: string
  duration: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  runtime_settings?: RuntimeSettingsOverride
  groups: ScenarioGroupPayload[]
  data_pools?: ScenarioDataPoolBinding[]
}

type DataPoolListResponse = {
  data_pools: SharedDataPool[]
  count: number
}

type DataPoolDetailsResponse = {
  data_pool: SharedDataPool
  rows: SharedDataPoolRow[]
}

type DataPoolDeleteResponse = {
  deleted: string
}

type DataPoolUpsertRequest = Pick<SharedDataPool, 'name'> & Partial<Pick<SharedDataPool, 'description' | 'schema' | 'archived_at' | 'project_id'>>

type DataPoolRowUpsertRequest = Pick<SharedDataPoolRow, 'data'> & Partial<Pick<SharedDataPoolRow, 'status' | 'version'>>

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
  source: string
  name: string
  status?: string
  draft: ScriptDefinition
  review_state?: Record<string, unknown>
}

type AuthoringSessionApprovalResponse = {
  session: AuthoringSession
  script: StoredScript
  authoring_health: AuthoringHealth
}

type EnterpriseArchiveImportOptions = {
  name?: string
  base_url?: string
  include_assertions?: boolean
  preserve_transactions?: boolean
  migration_block_statuses?: string[]
  project_id?: string
  har_file?: File
  salesforce_native?: boolean
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
    source_browser_actions: number
    captured_requests: number
    generated_http_actions: number
    skipped_static_assets: number
    correlation_candidates?: Array<{ name: string; source: string; value?: string; evidence?: string; action?: string }>
    data_source_candidates?: Array<{ name: string; source: string; value?: string; evidence?: string; action?: string }>
    auth_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    cookie_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    blocking_findings?: Array<{ severity: string; summary: string; evidence?: string; action?: string }>
    findings?: string[]
  }
  scenario_template: {
    name: string
    duration: string
    groups: Array<{ name: string; script_id: string; protocol: string; vusers: number; purpose: string; claim_policy?: string }>
    notes?: string[]
  }
}

function buildScriptQuery(projectId?: string, folderId?: string | null) {
  const query = new URLSearchParams()
  if (projectId) query.set('project_id', projectId)
  if (folderId !== undefined) query.set('folder_id', folderId || '')
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
    (projectId?: string, folderId?: string | null) =>
      request<ScriptListResponse>(`/scripts${buildScriptQuery(projectId, folderId)}`),
    [],
  )

  const createScript = useCallback(
    (script: ScriptDefinition, projectId?: string, folderId?: string | null) =>
      request<StoredScript>('/scripts', { method: 'POST', body: JSON.stringify({ script, project_id: projectId, folder_id: folderId }) }),
    [],
  )

  const getScript = useCallback((id: string) => request<StoredScript>(`/scripts/${id}`), [])

  const updateScript = useCallback(
    (id: string, script: ScriptDefinition) =>
      request<StoredScript>(`/scripts/${id}`, { method: 'PUT', body: JSON.stringify({ script }) }),
    [],
  )

  const deleteScript = useCallback((id: string) => request<DeleteScriptResponse>(`/scripts/${id}`, { method: 'DELETE' }), [])

  const moveScript = useCallback(
    (id: string, folderId: string | null) =>
      request<MoveScriptResponse>(`/scripts/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const validateScript = useCallback((id: string) => request<ScriptValidationResponse>(`/scripts/${id}/validate`, { method: 'POST' }), [])

  const validateDraftScript = useCallback(
    (script: ScriptDefinition, projectId?: string, folderId?: string | null, variables?: Record<string, string>) =>
      request<ScriptValidationResponse>('/scripts/validate', { method: 'POST', body: JSON.stringify({ script, project_id: projectId, folder_id: folderId, variables }) }),
    [],
  )

  const listAuthoringTemplates = useCallback(
    (params?: { protocol?: string; use_case?: string }) => {
      const query = new URLSearchParams()
      if (params?.protocol) query.set('protocol', params.protocol)
      if (params?.use_case) query.set('use_case', params.use_case)
      const suffix = query.toString()
      return request<AuthoringTemplatesResponse>(`/scripts/authoring/templates${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

  const listAuthoringComponents = useCallback(
    (params?: { kind?: string; protocol?: string }) => {
      const query = new URLSearchParams()
      if (params?.kind) query.set('kind', params.kind)
      if (params?.protocol) query.set('protocol', params.protocol)
      const suffix = query.toString()
      return request<AuthoringComponentsResponse>(`/scripts/authoring/components${suffix ? `?${suffix}` : ''}`)
    },
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

  const replayScript = useCallback(
		(id: string, payload?: { group_name?: string; replay_session_id?: string; variables?: Record<string, string>; ignore_think_time?: boolean; script?: ScriptDefinition }) =>
			request<ScriptReplayResponse>(`/scripts/${id}/replay`, { method: 'POST', body: JSON.stringify(payload || {}) }),
		[],
	)

  const replayDraftScript = useCallback(
    (payload: { script: ScriptDefinition; group_name?: string; replay_session_id?: string; variables?: Record<string, string>; ignore_think_time?: boolean }) =>
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
    (id: string, payload?: ScriptDebugStartRequest) =>
      request<ScriptDebugStartResponse>(`/scripts/${id}/debug/sessions`, { method: 'POST', body: JSON.stringify(payload || {}) }),
    [],
  )

  const startDraftScriptDebugSession = useCallback(
    (payload: ScriptDebugStartRequest & { script: ScriptDefinition }) =>
      request<ScriptDebugStartResponse>('/scripts/debug/sessions', { method: 'POST', body: JSON.stringify(payload) }),
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
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}`),
    [],
  )

  const stopScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}/stop`, { method: 'POST' }),
    [],
  )

  const pauseScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}/pause`, { method: 'POST' }),
    [],
  )

  const resumeScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}/resume`, { method: 'POST' }),
    [],
  )

  const stepScriptDebugSession = useCallback(
    (sessionId: string) => request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}/step`, { method: 'POST' }),
    [],
  )

  const updateScriptDebugBreakpoints = useCallback(
    (sessionId: string, breakpoints: ScriptDebugBreakpoint[]) =>
      request<ScriptDebugSessionResponse>(`/script-debug/sessions/${sessionId}/breakpoints`, { method: 'PATCH', body: JSON.stringify({ breakpoints }) }),
    [],
  )

  const exportScriptDebugSession = useCallback(
    async (sessionId: string, format: 'json' | 'log' = 'json') => {
      const res = await apiFetch(`/script-debug/sessions/${sessionId}/export?format=${encodeURIComponent(format)}`)
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

  const importEnterpriseArchive = useCallback(
    async (file: File, options?: EnterpriseArchiveImportOptions): Promise<EnterpriseArchiveImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.har_file) formData.append('har_file', options.har_file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))
      if (options?.migration_block_statuses?.length) formData.append('migration_block_statuses', options.migration_block_statuses.join(','))
      if (options?.salesforce_native) formData.append('salesforce_native', 'true')

      const res = await apiFetch('/scripts/import/enterprise-script-archive', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<EnterpriseArchiveImportResult>
    },
    [],
  )

  const validateEnterpriseArchiveImport = useCallback(
    async (file: File, options?: EnterpriseArchiveImportOptions): Promise<EnterpriseArchiveImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.har_file) formData.append('har_file', options.har_file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))
      if (options?.migration_block_statuses?.length) formData.append('migration_block_statuses', options.migration_block_statuses.join(','))
      if (options?.salesforce_native) formData.append('salesforce_native', 'true')

      const res = await apiFetch('/scripts/import/enterprise-script-archive/validate', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<EnterpriseArchiveImportResult>
    },
    [],
  )

  const getEnterpriseArchiveSupportMatrix = useCallback(
		() => request<EnterpriseArchiveSupportMatrixResponse>('/scripts/import/enterprise-script-archive/support-matrix'),
		[],
	)

  const importJMeter = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; scenario_draft?: boolean; project_id?: string }): Promise<JMeterImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.scenario_draft) formData.append('scenario_draft', 'true')

      const res = await apiFetch('/scripts/import/jmeter', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<JMeterImportResult>
    },
    [],
  )

  const validateJMeterImport = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; scenario_draft?: boolean; project_id?: string }): Promise<JMeterImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.scenario_draft) formData.append('scenario_draft', 'true')

      const res = await apiFetch('/scripts/import/jmeter/validate', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<JMeterImportResult>
    },
    [],
  )

  const getJMeterSupportMatrix = useCallback(
    () => request<JMeterSupportMatrixResponse>('/scripts/import/jmeter/support-matrix'),
    [],
  )

  const saveJMeterImport = useCallback(
    (payload: SaveJMeterImportRequest) =>
      request<SaveJMeterImportResponse>('/scripts/import/jmeter/save', { method: 'POST', body: JSON.stringify(payload) }),
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

  const importDevWeb = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<DevWebImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/devweb', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<DevWebImportResult>
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
    }): Promise<HARImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_static_assets) formData.append('include_static_assets', 'true')
      if (options?.secondary_file) formData.append('secondary_file', options.secondary_file)
      if (options?.artifact_mode) formData.append('artifact_mode', options.artifact_mode)
      if (typeof options?.retention_days === 'number') formData.append('retention_days', String(options.retention_days))
      if (typeof options?.redact_sensitive === 'boolean') formData.append('redact_sensitive', options.redact_sensitive ? 'true' : 'false')

      const res = await apiFetch('/scripts/import/har', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<HARImportResult>
    },
    [],
  )

  const importSalesforce = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<SalesforceImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/salesforce', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<SalesforceImportResult>
    },
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
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<BrowserRecordingImportResult>
    },
    [],
  )

  const importOpenAPI = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<OpenAPIImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/openapi', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<OpenAPIImportResult>
    },
    [],
  )

  const importRAML = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<RAMLImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/raml', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<RAMLImportResult>
    },
    [],
  )

  const importPostman = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; project_id?: string }): Promise<PostmanImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)

      const res = await apiFetch('/scripts/import/postman', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<PostmanImportResult>
    },
    [],
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
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<WSDLImportResult>
    },
    [],
  )

  const importAsyncAPI = useCallback(
    async (file: File, options?: { name?: string; project_id?: string }): Promise<AsyncAPIImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('script_name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)

      const res = await apiFetch('/scripts/import/asyncapi', { method: 'POST', body: formData })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<AsyncAPIImportResult>
    },
    [],
  )

  const applyImportParameterization = useCallback(
    (payload: ApplyParameterizationRequest) =>
      request<ApplyParameterizationResponse>('/scripts/import/parameterize/apply', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const listScenarios = useCallback((projectId?: string) => request<ScenarioListResponse>(`/scenarios${buildScenarioQuery(projectId)}`), [])

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

  const getDataPool = useCallback((id: string) => request<DataPoolDetailsResponse>(`/data-pools/${id}`), [])

  const updateDataPool = useCallback(
    (id: string, data: DataPoolUpsertRequest) => request<SharedDataPool>(`/data-pools/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteDataPool = useCallback((id: string) => request<DataPoolDeleteResponse>(`/data-pools/${id}`, { method: 'DELETE' }), [])

  const createDataPoolRow = useCallback(
    (poolId: string, data: DataPoolRowUpsertRequest) => request<SharedDataPoolRow>(`/data-pools/${poolId}/rows`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateDataPoolRow = useCallback(
    (poolId: string, rowId: string, data: DataPoolRowUpsertRequest) => request<SharedDataPoolRow>(`/data-pools/${poolId}/rows/${rowId}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteDataPoolRow = useCallback((poolId: string, rowId: string) => request<DataPoolDeleteResponse>(`/data-pools/${poolId}/rows/${rowId}`, { method: 'DELETE' }), [])

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
      updateScript,
      deleteScript,
      moveScript,
      validateScript,
      validateDraftScript,
      listAuthoringTemplates,
      listAuthoringComponents,
      listAuthoringSessions,
      createAuthoringSession,
      approveAuthoringSession,
      deleteAuthoringSession,
      previewAuthoringCorrelation,
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
      exportScriptDebugSession,
      exportScript,
      importEnterpriseArchive,
      validateEnterpriseArchiveImport,
      getEnterpriseArchiveSupportMatrix,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      saveJMeterImport,
      importSample,
      importDevWeb,
      importHAR,
      importSalesforce,
      importBrowserRecording,
      importOpenAPI,
      importRAML,
      importPostman,
      importWSDL,
      importAsyncAPI,
      applyImportParameterization,
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
      listScenarioDataPools,
      updateScenarioDataPools,
    }),
    [
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      moveScript,
      validateScript,
      validateDraftScript,
      listAuthoringTemplates,
      listAuthoringComponents,
      listAuthoringSessions,
      createAuthoringSession,
      approveAuthoringSession,
      deleteAuthoringSession,
      previewAuthoringCorrelation,
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
      exportScriptDebugSession,
      exportScript,
      importEnterpriseArchive,
      validateEnterpriseArchiveImport,
      getEnterpriseArchiveSupportMatrix,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      saveJMeterImport,
      importSample,
      importDevWeb,
      importHAR,
      importSalesforce,
      importBrowserRecording,
      importOpenAPI,
      importRAML,
      importPostman,
      importWSDL,
      importAsyncAPI,
      applyImportParameterization,
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
      listScenarioDataPools,
      updateScenarioDataPools,
    ],
  )
}
