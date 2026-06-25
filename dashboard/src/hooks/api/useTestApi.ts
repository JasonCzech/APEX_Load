import { useCallback, useMemo } from 'react'

import {
  ArchivePage,
  DraftCapacityEstimate,
  GeneratorAssignmentSettings,
  GoalConfig,
  GoalSizingReport,
  GoalSizingRequest,
  GoalTargetStatus,
  IntervalSummaryRecord,
  LaunchPreflightResponse,
  LoadModelSupportMatrixResponse,
  LoadModelTemplatesResponse,
  LoadModelValidationReport,
  LiveErrorEvidence,
  LiveMetrics,
  ResourceSample,
  ReportAnnotationsInput,
  ReportAnnotationsResponse,
  RemoteRunProxyResponse,
  RunArchiveAnalysisResponse,
  RunArchiveManifest,
  RunArchiveReport,
  RunCalendarResponse,
  RuntimeSettingsOverride,
  ScenarioSchedule,
  ScenarioScheduleOccurrence,
  ScenarioScheduleHealth,
  ScenarioDataPoolBinding,
  ScenarioMode,
  TestNote,
  TestRunManifest,
  TestSummary,
  TransactionEvent,
} from '../../types'
import { apiFetch, request } from '../apiClient'

type ArchiveTransactionsParams = {
  limit?: number
  offset?: number
  action?: string
  soap_operation?: string
  group?: string
  success?: boolean
  time_from?: string
  time_to?: string
  status?: string
  min_duration_ms?: number
  max_duration_ms?: number
  min_bytes?: number
  max_bytes?: number
  error_contains?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

type ArchiveListParams = {
  limit?: number
  offset?: number
  action?: string
  groups?: string[]
}

type MetricsParams = {
  groups?: string[]
}

type TestListResponse = {
  tests: TestSummary[]
  count: number
}

type TestNotesResponse = {
  test_id: string
  notes: TestNote[]
}

type DeleteTestNoteResponse = {
  deleted: string
}

type StartTestOptions = {
  override_capacity_shortfall?: boolean
  override_script_shakeout_failure?: boolean
}

type TestCreateGroupEntry = {
  name?: string
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  stages?: Array<{
    name?: string
    type: string
    duration: string
    target_vusers?: number
    target_tps?: number
  }>
  iterations?: number
  runtime_settings?: RuntimeSettingsOverride
  initialization?: 'at_runtime' | 'at_once'
  assignment?: GeneratorAssignmentSettings
}

type TestCreateRequest = {
  name?: string
  duration: string
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  folder_id?: string | null
  reserve_launch_window?: boolean
  scheduled_start_at?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  groups?: TestCreateGroupEntry[]
  data_pools?: ScenarioDataPoolBinding[]
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  runtime_settings?: RuntimeSettingsOverride
}

type LaunchPreflightRequest = TestCreateRequest & {
  intent?: 'immediate' | 'scheduled' | 'queue'
}

type RemoteRunCreateRequest = TestCreateRequest & {
  test?: TestCreateRequest
  remote_project_id?: string
  start_immediately?: boolean
  start_options?: StartTestOptions
}

type TestActionResponse = {
  status: string
  test_id: string
  message?: string
  test?: TestSummary
}

type ScenarioScheduleRequest = {
  name: string
  cron_expr: string
  timezone?: string
  release_id?: string
  timeslot_id?: string
  run_name_template?: string
  enabled?: boolean
  approval_policy?: string
  missed_run_policy?: string
  blackout_windows?: Array<{ name?: string, days_of_week?: number[], start_time: string, end_time: string, timezone?: string }>
  maintenance_windows?: Array<{ name?: string, days_of_week?: number[], start_time: string, end_time: string, timezone?: string }>
  availability_windows?: Array<{ name?: string, days_of_week?: number[], start_time: string, end_time: string, timezone?: string }>
}

type ScenarioScheduleListResponse = {
  schedules: ScenarioSchedule[]
  count: number
}

type ScenarioScheduleOccurrencesResponse = {
  occurrences: ScenarioScheduleOccurrence[]
  count: number
}

type ScenarioScheduleApprovalResponse = {
  approval: unknown
  schedule: ScenarioSchedule
}

type RunCalendarParams = {
  from: string
  to: string
  project_id?: string
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

type LiveErrorEvidenceResponse = {
  test_id: string
  signature: string
  evidence: LiveErrorEvidence[]
}

type LiveErrorEvidenceParams = {
  signature: string
  action?: string
  limit?: number
}

type ScaleVUsersResponse = {
  test_id: string
  group: string
  action?: 'run' | 'pause' | 'stop'
  target_vusers: number
  ramp_duration?: string
  status: string
}

type ScaleVUsersOptions = {
  action?: 'run' | 'pause' | 'stop'
  ramp_duration?: string
}

type GoalTargetUpdateResponse = {
  test_id: string
  group: string
  target_tps: number
  target_average_tps: number
  goal_targets: Record<string, GoalTargetStatus>
  status: string
}

type AdjustDurationResponse = {
  test_id: string
  new_duration: string
  status: string
}

type ArchiveTransactionsResponse = ArchivePage<TransactionEvent>
type ArchiveResourcesResponse = ArchivePage<ResourceSample>
type ArchiveSummariesResponse = ArchivePage<IntervalSummaryRecord>
type ArchiveAnalysisParams = ArchiveTransactionsParams & {
  baseline_id?: string
}

function buildQuery(params?: Record<string, string | number | boolean | string[] | undefined>) {
  const query = new URLSearchParams()

  Object.entries(params || {}).forEach(([key, value]) => {
    if (value === undefined) return
    if (Array.isArray(value)) {
      if (value.length === 0) return
      query.set(key, value.join(','))
      return
    }
    query.set(key, String(value))
  })

  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

async function fetchBlob(path: string): Promise<Blob> {
  const res = await apiFetch(path)
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

  const preflightTest = useCallback(
    (data: LaunchPreflightRequest) => request<LaunchPreflightResponse>('/tests/preflight', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const listLoadModelTemplates = useCallback(
    (params?: { shape_id?: string }) =>
      request<LoadModelTemplatesResponse>(`/scenarios/load-models/templates${buildQuery({ shape_id: params?.shape_id })}`),
    [],
  )

  const getLoadModelSupportMatrix = useCallback(
    () => request<LoadModelSupportMatrixResponse>('/scenarios/load-models/support-matrix'),
    [],
  )

  const validateScenarioLoadModel = useCallback(
    (data: unknown) => request<LoadModelValidationReport>('/scenarios/validate-load-model', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const sizeScenarioFromGoal = useCallback(
    (data: GoalSizingRequest) => request<GoalSizingReport>('/scenarios/size-from-goal', { method: 'POST', body: JSON.stringify(data) }),
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

  const createRemoteRun = useCallback(
    (remoteId: string, data: RemoteRunCreateRequest) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs`, {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    [],
  )

  const getRemoteRun = useCallback(
    (remoteId: string, remoteTestId: string) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs/${encodeURIComponent(remoteTestId)}`),
    [],
  )

  const controlRemoteRun = useCallback(
    (remoteId: string, remoteTestId: string, action: 'start' | 'stop' | 'abort' | 'pause' | 'resume', options?: StartTestOptions) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs/${encodeURIComponent(remoteTestId)}/${action}`, {
        method: 'POST',
        body: action === 'start' && options ? JSON.stringify(options) : undefined,
      }),
    [],
  )

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

  const listScenarioScheduleOccurrences = useCallback(
    (scenarioId: string, scheduleId: string) => request<ScenarioScheduleOccurrencesResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/occurrences`),
    [],
  )

  const getScheduleHealth = useCallback(() => request<ScenarioScheduleHealth>('/schedules/health'), [])

  const getRunCalendar = useCallback(
    (params: RunCalendarParams) => request<RunCalendarResponse>(`/run-calendar${buildQuery(params)}`),
    [],
  )

  const approveScenarioScheduleApproval = useCallback(
    (scenarioId: string, scheduleId: string, approvalId: string, reason?: string) =>
      request<ScenarioScheduleApprovalResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/approvals/${approvalId}/approve`, {
        method: 'POST',
        body: JSON.stringify({ reason: reason || '' }),
      }),
    [],
  )

  const rejectScenarioScheduleApproval = useCallback(
    (scenarioId: string, scheduleId: string, approvalId: string, reason?: string) =>
      request<ScenarioScheduleApprovalResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/approvals/${approvalId}/reject`, {
        method: 'POST',
        body: JSON.stringify({ reason: reason || '' }),
      }),
    [],
  )

  const moveTest = useCallback(
    (id: string, folderId: string | null) =>
      request<MoveTestResponse>(`/tests/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const getMetrics = useCallback((id: string, params?: MetricsParams) => {
    const suffix = buildQuery({ groups: params?.groups })
    return request<TestMetricsResponse>(`/tests/${id}/metrics${suffix}`)
  }, [])

  const getTestErrorEvidence = useCallback(
    (id: string, params: LiveErrorEvidenceParams) =>
      request<LiveErrorEvidenceResponse>(`/tests/${id}/errors/evidence${buildQuery({
        signature: params.signature,
        action: params.action,
        limit: params.limit,
      })}`),
    [],
  )

  const getTestManifest = useCallback((id: string) => request<TestRunManifest>(`/tests/${id}/manifest`), [])

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
        time_from: params?.time_from,
        time_to: params?.time_to,
        status: params?.status,
        min_duration_ms: params?.min_duration_ms,
        max_duration_ms: params?.max_duration_ms,
        min_bytes: params?.min_bytes,
        max_bytes: params?.max_bytes,
        error_contains: params?.error_contains,
        sort_by: params?.sort_by,
        sort_dir: params?.sort_dir,
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
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset, action: params?.action, groups: params?.groups })
      return request<ArchiveSummariesResponse>(`/tests/${id}/archive/summaries${suffix}`)
    },
    [],
  )

  const getTestArchiveReport = useCallback((id: string) => request<RunArchiveReport>(`/tests/${id}/archive/report`), [])

  const getTestReportAnnotations = useCallback(
    (id: string, profile: string) =>
      request<ReportAnnotationsResponse>(`/tests/${id}/report/annotations${buildQuery({ profile })}`),
    [],
  )

  const saveTestReportAnnotations = useCallback(
    (id: string, profile: string, data: ReportAnnotationsInput) =>
      request<ReportAnnotationsResponse>(`/tests/${id}/report/annotations${buildQuery({ profile })}`, {
        method: 'PUT',
        body: JSON.stringify(data),
      }),
    [],
  )

  const listTestNotes = useCallback((id: string) => request<TestNotesResponse>(`/tests/${id}/notes`), [])

  const createTestNote = useCallback(
    (id: string, body: string) =>
      request<TestNote>(`/tests/${id}/notes`, {
        method: 'POST',
        body: JSON.stringify({ body }),
      }),
    [],
  )

  const updateTestNote = useCallback(
    (id: string, noteId: string, body: string) =>
      request<TestNote>(`/tests/${id}/notes/${noteId}`, {
        method: 'PATCH',
        body: JSON.stringify({ body }),
      }),
    [],
  )

  const deleteTestNote = useCallback(
    (id: string, noteId: string) =>
      request<DeleteTestNoteResponse>(`/tests/${id}/notes/${noteId}`, {
        method: 'DELETE',
      }),
    [],
  )

  const downloadTestReport = useCallback(
    (id: string, profile: string) => fetchBlob(`/tests/${id}/report${buildQuery({ profile })}`),
    [],
  )

  const getTestArchiveAnalysis = useCallback(
    (id: string, params?: ArchiveAnalysisParams) => {
      const suffix = buildQuery({
        baseline_id: params?.baseline_id,
        limit: params?.limit,
        offset: params?.offset,
        action: params?.action,
        soap_operation: params?.soap_operation,
        group: params?.group,
        success: params?.success,
        time_from: params?.time_from,
        time_to: params?.time_to,
        status: params?.status,
        min_duration_ms: params?.min_duration_ms,
        max_duration_ms: params?.max_duration_ms,
        min_bytes: params?.min_bytes,
        max_bytes: params?.max_bytes,
        error_contains: params?.error_contains,
        sort_by: params?.sort_by,
        sort_dir: params?.sort_dir,
      })
      return request<RunArchiveAnalysisResponse>(`/tests/${id}/archive/analysis${suffix}`)
    },
    [],
  )

  const downloadTestArchive = useCallback((id: string) => fetchBlob(`/tests/${id}/archive`), [])

  const downloadTestArchiveComparison = useCallback(
    (id: string, baselineId: string, breakdown: 'overall' | 'platform' | 'engine' | 'device', action?: string) =>
      fetchBlob(`/tests/${id}/archive/comparison${buildQuery({ format: 'csv', baseline_id: baselineId, breakdown, action })}`),
    [],
  )

  const downloadTestArchiveMultiComparison = useCallback(
    (baselineId: string, comparisonIds: string[], breakdown: 'overall' | 'platform' | 'engine' | 'device', action?: string) =>
      fetchBlob(`/tests/${baselineId}/archive/comparison${buildQuery({ format: 'csv', comparison_ids: comparisonIds.join(','), breakdown, action })}`),
    [],
  )

  const downloadTestArchiveBrowserDiagnostics = useCallback(
    (id: string) => fetchBlob(`/tests/${id}/archive/browser-diagnostics`),
    [],
  )

  const downloadTestArchiveArtifact = useCallback(
    (id: string, name: string) => fetchBlob(`/tests/${id}/archive/files/${encodeURIComponent(name)}`),
    [],
  )

  const scaleVUsers = useCallback(
    (testId: string, group: string, targetVUsers: number, options?: ScaleVUsersOptions) =>
      request<ScaleVUsersResponse>(`/tests/${testId}/vusers`, {
        method: 'PATCH',
        body: JSON.stringify({ group, target_vusers: targetVUsers, action: options?.action, ramp_duration: options?.ramp_duration }),
      }),
    [],
  )

  const updateGoalTarget = useCallback(
    (testId: string, group: string, targetTps: number) =>
      request<GoalTargetUpdateResponse>(`/tests/${testId}/goal-targets`, {
        method: 'PATCH',
        body: JSON.stringify({ group, target_tps: targetTps }),
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
      preflightTest,
      getLoadModelSupportMatrix,
      listLoadModelTemplates,
      validateScenarioLoadModel,
      sizeScenarioFromGoal,
      startTest,
      pinComparisonBaseline,
      unpinComparisonBaseline,
      stopTest,
      abortTest,
      deleteTest,
      moveTest,
      getMetrics,
      getTestErrorEvidence,
      getTestManifest,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      getTestReportAnnotations,
      saveTestReportAnnotations,
      listTestNotes,
      createTestNote,
      updateTestNote,
      deleteTestNote,
      downloadTestReport,
      getTestArchiveAnalysis,
      downloadTestArchive,
      downloadTestArchiveComparison,
      downloadTestArchiveMultiComparison,
      downloadTestArchiveBrowserDiagnostics,
      downloadTestArchiveArtifact,
      scaleVUsers,
      updateGoalTarget,
      adjustDuration,
      pauseTest,
      resumeTest,
      createRemoteRun,
      getRemoteRun,
      controlRemoteRun,
      listScenarioSchedules,
      createScenarioSchedule,
      updateScenarioSchedule,
      deleteScenarioSchedule,
      listScenarioScheduleOccurrences,
      getScheduleHealth,
      getRunCalendar,
      approveScenarioScheduleApproval,
      rejectScenarioScheduleApproval,
    }),
    [
      listTests,
      getTest,
      createTest,
      estimateTest,
      preflightTest,
      getLoadModelSupportMatrix,
      listLoadModelTemplates,
      validateScenarioLoadModel,
      sizeScenarioFromGoal,
      startTest,
      pinComparisonBaseline,
      unpinComparisonBaseline,
      stopTest,
      abortTest,
      deleteTest,
      pauseTest,
      resumeTest,
      createRemoteRun,
      getRemoteRun,
      controlRemoteRun,
      listScenarioSchedules,
      createScenarioSchedule,
      updateScenarioSchedule,
      deleteScenarioSchedule,
      listScenarioScheduleOccurrences,
      getScheduleHealth,
      getRunCalendar,
      approveScenarioScheduleApproval,
      rejectScenarioScheduleApproval,
      moveTest,
      getMetrics,
      getTestErrorEvidence,
      getTestManifest,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      getTestReportAnnotations,
      saveTestReportAnnotations,
      listTestNotes,
      createTestNote,
      updateTestNote,
      deleteTestNote,
      downloadTestReport,
      getTestArchiveAnalysis,
      downloadTestArchive,
      downloadTestArchiveComparison,
      downloadTestArchiveMultiComparison,
      downloadTestArchiveBrowserDiagnostics,
      downloadTestArchiveArtifact,
      scaleVUsers,
      updateGoalTarget,
      adjustDuration,
    ],
  )
}
