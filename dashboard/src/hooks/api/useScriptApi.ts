import { useCallback, useMemo } from 'react'

import {
  BrowserReplayStartRequest,
  BrowserReplayStartResponse,
  BrowserReplaySessionResponse,
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
import { type ScriptDefinition, type ScriptExportResponse, type ScriptReplayResponse, type ScriptValidationResponse } from '../../types/scripts'
import { type ImportedScript, type JMeterImportResult, type JMeterSupportMatrixResponse, type LoadRunnerImportResult, type SampleImportResult, type SaveJMeterImportRequest, type SaveJMeterImportResponse } from '../../types/scriptImports'
import { BASE, request } from '../apiClient'

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
  vusers: number
  ramp_up?: string
  ramp_down?: string
  initialization?: 'at_runtime' | 'at_once'
}

type ScenarioUpsertRequest = {
  name: string
  duration: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: {
    mode?: 'auto' | 'specific_hosts'
    selected_targets?: string[]
  }
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
    (script: ScriptDefinition | ImportedScript, projectId?: string, folderId?: string | null) =>
      request<StoredScript>('/scripts', { method: 'POST', body: JSON.stringify({ script, project_id: projectId, folder_id: folderId }) }),
    [],
  )

  const getScript = useCallback((id: string) => request<StoredScript>(`/scripts/${id}`), [])

  const updateScript = useCallback(
    (id: string, script: ScriptDefinition | ImportedScript) =>
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

  const exportScript = useCallback(
    (id: string, outputDir?: string, filename?: string) =>
      request<ScriptExportResponse>(`/scripts/${id}/export`, {
        method: 'POST',
        body: JSON.stringify({ output_dir: outputDir, filename }),
      }),
    [],
  )

  const importLoadRunner = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; preserve_transactions?: boolean; project_id?: string }): Promise<LoadRunnerImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))

      const res = await fetch(`${BASE}/scripts/import/loadrunner`, {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<LoadRunnerImportResult>
    },
    [],
  )

  const validateLoadRunnerImport = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean; preserve_transactions?: boolean; project_id?: string }): Promise<LoadRunnerImportResult> => {
      const formData = new FormData()
      formData.append('file', file)
      if (options?.name) formData.append('name', options.name)
      if (options?.project_id) formData.append('project_id', options.project_id)
      if (options?.base_url) formData.append('base_url', options.base_url)
      if (options?.include_assertions) formData.append('include_assertions', 'true')
      if (options?.preserve_transactions !== undefined) formData.append('preserve_transactions', String(options.preserve_transactions))

      const res = await fetch(`${BASE}/scripts/import/loadrunner/validate`, {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(body.error || res.statusText)
      }
      return res.json() as Promise<LoadRunnerImportResult>
    },
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

      const res = await fetch(`${BASE}/scripts/import/jmeter`, {
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

      const res = await fetch(`${BASE}/scripts/import/jmeter/validate`, {
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
      }
    }) => request<SampleImportResult>('/scripts/import/sample', { method: 'POST', body: JSON.stringify(data) }),
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
  		replayScript,
  		replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      exportScript,
      importLoadRunner,
      validateLoadRunnerImport,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      saveJMeterImport,
      importSample,
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
  		replayScript,
  		replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      exportScript,
      importLoadRunner,
      validateLoadRunnerImport,
      importJMeter,
      validateJMeterImport,
      getJMeterSupportMatrix,
      saveJMeterImport,
      importSample,
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
