import { useCallback, useMemo } from 'react'

import {
  ArchivePage,
  DraftCapacityEstimate,
  GoalConfig,
  IntervalSummaryRecord,
  LiveMetrics,
  RunArchiveManifest,
  RunArchiveReport,
  RuntimeSettingsOverride,
  ScenarioSchedule,
  ScenarioDataPoolBinding,
  ScenarioMode,
  TestSummary,
  TransactionEvent,
} from '../../types'
import { BASE, request } from '../apiClient'

type ArchiveTransactionsParams = {
  limit?: number
  offset?: number
  action?: string
  soap_operation?: string
  group?: string
  success?: boolean
}

type ArchiveListParams = {
  limit?: number
  offset?: number
}

type TestListResponse = {
  tests: TestSummary[]
  count: number
}

type StartTestOptions = {
  override_capacity_shortfall?: boolean
}

type TestCreateGroupEntry = {
  name?: string
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  initialization?: 'at_runtime' | 'at_once'
}

type TestCreateRequest = {
  name?: string
  duration: string
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  folder_id?: string | null
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: {
    mode?: 'auto' | 'specific_hosts'
    selected_targets?: string[]
  }
  goal_config?: GoalConfig
  groups?: TestCreateGroupEntry[]
  data_pools?: ScenarioDataPoolBinding[]
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  runtime_settings?: RuntimeSettingsOverride
}

type TestActionResponse = {
  status: string
  test_id: string
  message?: string
}

type ScenarioScheduleRequest = {
  name: string
  cron_expr: string
  timezone?: string
  release_id?: string
  run_name_template?: string
  enabled?: boolean
}

type ScenarioScheduleListResponse = {
  schedules: ScenarioSchedule[]
  count: number
}

type DeleteTestResponse = {
  deleted: string
}

type MoveTestResponse = {
  id: string
  folder_id?: string | null
}

type TestMetricsResponse = {
  test_id: string
  status: string
  message?: string
  metrics?: LiveMetrics
  history?: unknown[]
}

type ScaleVUsersResponse = {
  test_id: string
  group: string
  target_vusers: number
  status: string
}

type AdjustDurationResponse = {
  test_id: string
  new_duration: string
  status: string
}

type ArchiveTransactionsResponse = ArchivePage<TransactionEvent>
type ArchiveResourcesResponse = ArchivePage<unknown>
type ArchiveSummariesResponse = ArchivePage<IntervalSummaryRecord>

function buildQuery(params?: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams()

  Object.entries(params || {}).forEach(([key, value]) => {
    if (value !== undefined) {
      query.set(key, String(value))
    }
  })

  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

async function fetchBlob(path: string): Promise<Blob> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useTestApi() {
  const listTests = useCallback(
    (params?: { project_id?: string; folder_id?: string | null; application_id?: string; release_id?: string; scenario_id?: string } | string, folderId?: string | null) => {
      const queryParams = typeof params === 'string'
        ? { project_id: params, folder_id: folderId === undefined ? undefined : folderId || '' }
        : {
            application_id: params?.application_id,
            release_id: params?.release_id,
            scenario_id: params?.scenario_id,
            project_id: params?.project_id,
            folder_id: params?.folder_id === undefined ? undefined : params.folder_id || '',
          }
      return request<TestListResponse>(`/tests${buildQuery(queryParams)}`)
    },
    [],
  )

  const getTest = useCallback((id: string) => request<TestSummary>(`/tests/${id}`), [])

  const createTest = useCallback(
    (data: TestCreateRequest) => request<TestSummary>('/tests', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const estimateTest = useCallback(
    (data: TestCreateRequest) => request<DraftCapacityEstimate>('/tests/estimate', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const startTest = useCallback(
    (id: string, options?: StartTestOptions) =>
      request<TestActionResponse>(`/tests/${id}/start`, {
        method: 'POST',
        body: options ? JSON.stringify(options) : undefined,
      }),
    [],
  )

  const pinComparisonBaseline = useCallback(
    (id: string) => request<TestSummary>(`/tests/${id}/comparison-baseline/pin`, { method: 'POST' }),
    [],
  )

  const unpinComparisonBaseline = useCallback(
    (id: string) => request<TestSummary>(`/tests/${id}/comparison-baseline/pin`, { method: 'DELETE' }),
    [],
  )

  const stopTest = useCallback((id: string) => request<TestActionResponse>(`/tests/${id}/stop`, { method: 'POST' }), [])

  const abortTest = useCallback((id: string) => request<TestActionResponse>(`/tests/${id}/abort`, { method: 'POST' }), [])

  const deleteTest = useCallback((id: string) => request<DeleteTestResponse>(`/tests/${id}`, { method: 'DELETE' }), [])
  
  const pauseTest = useCallback((id: string) => request<TestActionResponse>(`/tests/${id}/pause`, { method: 'POST' }), [])
  
  const resumeTest = useCallback((id: string) => request<TestActionResponse>(`/tests/${id}/resume`, { method: 'POST' }), [])

  const listScenarioSchedules = useCallback(
    (scenarioId: string) => request<ScenarioScheduleListResponse>(`/scenarios/${scenarioId}/schedules`),
    [],
  )

  const createScenarioSchedule = useCallback(
    (scenarioId: string, data: ScenarioScheduleRequest) => request<ScenarioSchedule>(`/scenarios/${scenarioId}/schedules`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateScenarioSchedule = useCallback(
    (scenarioId: string, scheduleId: string, data: ScenarioScheduleRequest) => request<ScenarioSchedule>(`/scenarios/${scenarioId}/schedules/${scheduleId}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteScenarioSchedule = useCallback(
    (scenarioId: string, scheduleId: string) => request<{ deleted: string }>(`/scenarios/${scenarioId}/schedules/${scheduleId}`, { method: 'DELETE' }),
    [],
  )

  const moveTest = useCallback(
    (id: string, folderId: string | null) =>
      request<MoveTestResponse>(`/tests/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const getMetrics = useCallback((id: string) => request<TestMetricsResponse>(`/tests/${id}/metrics`), [])

  const getTestArchiveManifest = useCallback((id: string) => request<RunArchiveManifest>(`/tests/${id}/archive/manifest`), [])

  const getTestArchiveTransactions = useCallback(
    (id: string, params?: ArchiveTransactionsParams) => {
      const suffix = buildQuery({
        limit: params?.limit,
        offset: params?.offset,
        action: params?.action,
        soap_operation: params?.soap_operation,
        group: params?.group,
        success: params?.success,
      })
      return request<ArchiveTransactionsResponse>(`/tests/${id}/archive/transactions${suffix}`)
    },
    [],
  )

  const getTestArchiveResources = useCallback(
    (id: string, params?: ArchiveListParams) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset })
      return request<ArchiveResourcesResponse>(`/tests/${id}/archive/resources${suffix}`)
    },
    [],
  )

  const getTestArchiveSummaries = useCallback(
    (id: string, params?: ArchiveListParams) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset })
      return request<ArchiveSummariesResponse>(`/tests/${id}/archive/summaries${suffix}`)
    },
    [],
  )

  const getTestArchiveReport = useCallback((id: string) => request<RunArchiveReport>(`/tests/${id}/archive/report`), [])

  const downloadTestArchive = useCallback((id: string) => fetchBlob(`/tests/${id}/archive`), [])

  const downloadTestArchiveArtifact = useCallback(
    (id: string, name: string) => fetchBlob(`/tests/${id}/archive/files/${encodeURIComponent(name)}`),
    [],
  )

  const scaleVUsers = useCallback(
    (testId: string, group: string, targetVUsers: number) =>
      request<ScaleVUsersResponse>(`/tests/${testId}/vusers`, {
        method: 'PATCH',
        body: JSON.stringify({ group, target_vusers: targetVUsers }),
      }),
    [],
  )

  const adjustDuration = useCallback(
    (testId: string, duration: string) =>
      request<AdjustDurationResponse>(`/tests/${testId}/duration`, {
        method: 'PATCH',
        body: JSON.stringify({ duration }),
      }),
    [],
  )

  return useMemo(
    () => ({
      listTests,
      getTest,
      createTest,
      estimateTest,
      startTest,
      pinComparisonBaseline,
      unpinComparisonBaseline,
      stopTest,
      abortTest,
      deleteTest,
      moveTest,
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      scaleVUsers,
      adjustDuration,
      pauseTest,
      resumeTest,
      listScenarioSchedules,
      createScenarioSchedule,
      updateScenarioSchedule,
      deleteScenarioSchedule,
    }),
    [
      listTests,
      getTest,
      createTest,
      estimateTest,
      startTest,
      stopTest,
      abortTest,
      deleteTest,
      pauseTest,
      resumeTest,
      listScenarioSchedules,
      createScenarioSchedule,
      updateScenarioSchedule,
      deleteScenarioSchedule,
      moveTest,
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      scaleVUsers,
      adjustDuration,
    ],
  )
}
