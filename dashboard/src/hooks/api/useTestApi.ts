import { useCallback, useMemo } from 'react'

import {
  ArchivePage,
  DraftCapacityEstimate,
  GoalSizingReport,
  GoalSizingRequest,
  IntervalSummaryRecord,
  LaunchPreflightResponse,
  LoadModelSupportMatrixResponse,
  LoadModelTemplatesResponse,
  LoadModelValidationReport,
  ScenarioValidationContext,
  LiveErrorEvidence,
  LiveMetrics,
  ResourceSample,
  ReportAnnotationsInput,
  ReportAnnotationsResponse,
  RemoteRunProxyResponse,
  RunAnalysisPolicySelections,
  RunArchiveAnalysisResponse,
  RunArchiveIntegrityReport,
  RunArchiveManifest,
  RunArchiveReport,
  ScenarioDataPoolBinding,
  TestGroupParametersUpdateResult,
  TestGroupParametersView,
  TestGroupRuntimeSettingsUpdateResult,
  TestGroupRuntimeSettingsView,
  TestNote,
  TestRunManifest,
  TestSummary,
  TrafficProfileAnalyzeResponse,
  TrafficProfileDeriveRequest,
  TrafficProfileDeriveResponse,
  TrafficProfileSourcesResponse,
  RuntimeAdjustmentPreview,
  RuntimeAdjustmentRequest,
  TransactionEvent,
} from '../../types'
import type { ScriptParameterDefinition } from '../../types/scripts'
import type { JourneyRiskReport } from '../../components/runReport/journeyRiskModel'
import type { GoalConfig, GoalTargetStatus, ScenarioMode } from '../../types/goals'
import type { GeneratorAssignmentSettings } from '../../types/testConfiguration'
import type { RuntimeSettingsOverride } from '../../types/runtimeSettings'
import type { ScheduleClockChange } from '../../types/executionPlans'
import type { PowerBIPublishResponse, PowerBIPublishStatusResponse } from '../../types/settings'
import type { ExemplarPage, RcaAnalysisResponse, RcaCritique, RcaWindowLinks } from '../../types/rca'
import type {
  RunCalendarResponse,
  ScenarioSchedule,
  ScenarioScheduleApproval,
  ScenarioScheduleHealth,
  ScenarioScheduleOccurrence,
} from '../../types/scheduling'
import { apiFetch, request, requestWithResponse } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

type ArchiveTransactionsParams = {
  population?: 'action' | 'transaction' | 'unknown'
  limit?: number
  offset?: number
  action?: string
  soap_operation?: string
  group?: string
	region?: string
	network_profile?: string
	source_transaction?: string
	hierarchy?: string
	phase?: string
	has_detail?: boolean
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
  sample_limit?: number
  action?: string
  groups?: string[]
}

type MetricsParams = {
  groups?: string[]
  since?: string
  limit?: number
}

type GetTestParams = {
  includeHistory?: boolean
  since?: string
  limit?: number
}

type TestListResponse = {
  tests: TestSummary[]
  count: number
  total?: number
  limit?: number
  offset?: number
}

type TestNotesResponse = {
  test_id: string
  notes: TestNote[]
}

type ArchiveReportsBatchResponse = {
  reports: Record<string, RunArchiveReport>
  errors?: Record<string, string>
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
	transaction_response_time_sla_ms?: number
}

type TestCreateRequest = {
  requested_test_id?: string
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
  disabled_from?: string
  disabled_until?: string
  analysis_policy_selections?: RunAnalysisPolicySelections
  expected_revision?: number
}

type ScenarioScheduleListResponse = {
  schedules: ScenarioSchedule[]
  count: number
  /** Each schedule's clock changes, by schedule ID; schedules without any are left out. */
  clock_changes?: Record<string, ScheduleClockChange[]>
}

type ScenarioSchedulePreview = {
  cron_expr: string
  timezone: string
  occurrences: string[]
  count: number
  conflict_summary?: string
  clock_changes?: ScheduleClockChange[]
}

type ScenarioScheduleOccurrencesResponse = {
  occurrences: ScenarioScheduleOccurrence[]
  count: number
}

type ScenarioScheduleOccurrenceResolutionResponse = {
	occurrence: ScenarioScheduleOccurrence
	schedule: ScenarioSchedule
}

type ScenarioScheduleApprovalResponse = {
  approval: unknown
  schedule: ScenarioSchedule
}

type ScenarioScheduleApprovalsListResponse = {
  approvals: ScenarioScheduleApproval[]
  count: number
}

type RunCalendarParams = {
  from: string
  to: string
  project_id?: string
  capacity?: boolean
  tz?: string
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

async function fetchBlob(path: string, options?: ApiCallOptions): Promise<Blob> {
  const res = await apiFetch(path, withCallOptions(undefined, options))
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useTestApi() {
  const listTests = useCallback(
    (params?: { project_id?: string; folder_id?: string | null; application_id?: string; release_id?: string; scenario_id?: string; summary?: boolean; active_only?: boolean; limit?: number; offset?: number } | string, folderId?: string | null, options?: ApiCallOptions) => {
      const queryParams = typeof params === 'string'
        ? { project_id: params, folder_id: folderId === undefined ? undefined : folderId || '' }
        : {
            application_id: params?.application_id,
            release_id: params?.release_id,
            scenario_id: params?.scenario_id,
            project_id: params?.project_id,
            folder_id: params?.folder_id === undefined ? undefined : params.folder_id || '',
            summary: params?.summary,
            active_only: params?.active_only,
            limit: params?.limit,
            offset: params?.offset,
          }
      return request<TestListResponse>(`/tests${buildQuery(queryParams)}`, withCallOptions(undefined, options))
    },
    [],
  )

  const getTest = useCallback((id: string, params?: GetTestParams, options?: ApiCallOptions) => {
    const suffix = buildQuery({
      include: params?.includeHistory ? 'history' : undefined,
      since: params?.since,
      limit: params?.limit,
    })
    return request<TestSummary>(`/tests/${id}${suffix}`, withCallOptions(undefined, options))
  }, [])

  const createTest = useCallback(
    (data: TestCreateRequest, options?: ApiCallOptions) => request<TestSummary>('/tests', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  // A run create under a draft-owned intent key (receipt contract 1): a retry
  // after a lost response returns the run the first attempt created.
  const createTestKeyed = useCallback(async (data: TestCreateRequest, key: string): Promise<{ kind: 'created' | 'replayed'; resource: TestSummary }> => {
    const response = await requestWithResponse<TestSummary>('/tests', { method: 'POST', body: JSON.stringify(data), headers: { 'Idempotency-Key': key } })
    return { kind: response.headers.get('Idempotent-Replayed') === 'true' ? 'replayed' : 'created', resource: response.body }
  }, [])

  const estimateTest = useCallback(
    (data: TestCreateRequest, options?: ApiCallOptions) => request<DraftCapacityEstimate>('/tests/estimate', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const preflightTest = useCallback(
    (data: LaunchPreflightRequest, options?: ApiCallOptions) => request<LaunchPreflightResponse>('/tests/preflight', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const listLoadModelTemplates = useCallback(
    (params?: { shape_id?: string; project_id?: string }, options?: ApiCallOptions) =>
      request<LoadModelTemplatesResponse>(`/scenarios/load-models/templates${buildQuery({ shape_id: params?.shape_id, project_id: params?.project_id })}`, withCallOptions(undefined, options)),
    [],
  )

  const getLoadModelSupportMatrix = useCallback(
    (projectId?: string, options?: ApiCallOptions) => request<LoadModelSupportMatrixResponse>(`/scenarios/load-models/support-matrix${buildQuery({ project_id: projectId })}`, withCallOptions(undefined, options)),
    [],
  )

  const validateScenarioLoadModel = useCallback(
    (data: unknown, options?: ApiCallOptions) => request<LoadModelValidationReport>('/scenarios/validate-load-model', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const getScenarioValidationContext = useCallback(
    async (data: unknown, options?: ApiCallOptions): Promise<ScenarioValidationContext | null> => {
      const report = await request<LoadModelValidationReport>(
        '/scenarios/validate-load-model?context_only=true',
        withCallOptions({ method: 'POST', body: JSON.stringify(data), timeoutMs: 30_000 }, options),
      )
      return report?.validation_context ?? null
    },
    [],
  )

  const sizeScenarioFromGoal = useCallback(
    (data: GoalSizingRequest, options?: ApiCallOptions) => request<GoalSizingReport>('/scenarios/size-from-goal', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const getTrafficProfileSources = useCallback(
    (options?: ApiCallOptions) => request<TrafficProfileSourcesResponse>('/scenarios/traffic-profile/sources', withCallOptions(undefined, options)),
    [],
  )

  const analyzeTrafficProfile = useCallback(
    (data: FormData | { project_id?: string; source: unknown }, options?: ApiCallOptions) => request<TrafficProfileAnalyzeResponse>('/scenarios/traffic-profile/analyze', withCallOptions({ method: 'POST', body: data instanceof FormData ? data : JSON.stringify(data) }, options)),
    [],
  )

  const deriveTrafficProfile = useCallback(
    (data: TrafficProfileDeriveRequest, options?: ApiCallOptions) => request<TrafficProfileDeriveResponse>('/scenarios/traffic-profile/derive', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const analyzeCSVDriven = useCallback(
    (data: FormData, options?: ApiCallOptions) => request<TrafficProfileAnalyzeResponse>('/scenarios/csv-driven/analyze', withCallOptions({ method: 'POST', body: data }, options)),
    [],
  )

  const deriveCSVDriven = useCallback(
    (data: TrafficProfileDeriveRequest, options?: ApiCallOptions) => request<TrafficProfileDeriveResponse>('/scenarios/csv-driven/derive', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const startTest = useCallback(
    (id: string, options?: StartTestOptions, callOptions?: ApiCallOptions) =>
      request<TestActionResponse>(`/tests/${id}/start`, withCallOptions({
        method: 'POST',
        body: options ? JSON.stringify(options) : undefined,
      }, callOptions)),
    [],
  )

  const pinComparisonBaseline = useCallback(
    (id: string, options?: ApiCallOptions) => request<TestSummary>(`/tests/${id}/comparison-baseline/pin`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const unpinComparisonBaseline = useCallback(
    (id: string, options?: ApiCallOptions) => request<TestSummary>(`/tests/${id}/comparison-baseline/pin`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const stopTest = useCallback((id: string, options?: ApiCallOptions) => request<TestActionResponse>(`/tests/${id}/stop`, withCallOptions({ method: 'POST' }, options)), [])

  const abortTest = useCallback((id: string, options?: ApiCallOptions) => request<TestActionResponse>(`/tests/${id}/abort`, withCallOptions({ method: 'POST' }, options)), [])

  const deleteTest = useCallback((id: string, options?: ApiCallOptions) => request<DeleteTestResponse>(`/tests/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])
  
  const pauseTest = useCallback((id: string, options?: ApiCallOptions) => request<TestActionResponse>(`/tests/${id}/pause`, withCallOptions({ method: 'POST' }, options)), [])
  
  const resumeTest = useCallback((id: string, options?: ApiCallOptions) => request<TestActionResponse>(`/tests/${id}/resume`, withCallOptions({ method: 'POST' }, options)), [])

  const createRemoteRun = useCallback(
    (remoteId: string, data: RemoteRunCreateRequest, options?: ApiCallOptions) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs`, withCallOptions({
        method: 'POST',
        body: JSON.stringify(data),
      }, options)),
    [],
  )

  const getRemoteRun = useCallback(
    (remoteId: string, remoteTestId: string, options?: ApiCallOptions) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs/${encodeURIComponent(remoteTestId)}`, withCallOptions(undefined, options)),
    [],
  )

  const controlRemoteRun = useCallback(
    (remoteId: string, remoteTestId: string, action: 'start' | 'stop' | 'abort' | 'pause' | 'resume', options?: StartTestOptions, callOptions?: ApiCallOptions) =>
      request<RemoteRunProxyResponse>(`/remotes/${encodeURIComponent(remoteId)}/runs/${encodeURIComponent(remoteTestId)}/${action}`, withCallOptions({
        method: 'POST',
        body: action === 'start' && options ? JSON.stringify(options) : undefined,
      }, callOptions)),
    [],
  )

  const listScenarioSchedules = useCallback(
    (scenarioId: string, options?: ApiCallOptions) => request<ScenarioScheduleListResponse>(`/scenarios/${scenarioId}/schedules`, withCallOptions(undefined, options)),
    [],
  )

  const createScenarioSchedule = useCallback(
    (scenarioId: string, data: ScenarioScheduleRequest, options?: ApiCallOptions) => request<ScenarioSchedule>(`/scenarios/${scenarioId}/schedules`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateScenarioSchedule = useCallback(
    (scenarioId: string, scheduleId: string, data: ScenarioScheduleRequest, options?: ApiCallOptions) => request<ScenarioSchedule>(`/scenarios/${scenarioId}/schedules/${scheduleId}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteScenarioSchedule = useCallback(
    (scenarioId: string, scheduleId: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/scenarios/${scenarioId}/schedules/${scheduleId}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const previewScenarioSchedule = useCallback(
    (scenarioId: string, data: ScenarioScheduleRequest, count = 5, options?: ApiCallOptions) => request<ScenarioSchedulePreview>(`/scenarios/${scenarioId}/schedules/preview?count=${count}`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const listScenarioScheduleOccurrences = useCallback(
    (scenarioId: string, scheduleId: string, options?: ApiCallOptions) => request<ScenarioScheduleOccurrencesResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/occurrences`, withCallOptions(undefined, options)),
    [],
  )

  const listScenarioScheduleApprovals = useCallback(
    (scenarioId: string, scheduleId: string, options?: ApiCallOptions) => request<ScenarioScheduleApprovalsListResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/approvals`, withCallOptions(undefined, options)),
    [],
  )

  const deleteScenarioScheduleOccurrence = useCallback(
    (scenarioId: string, scheduleId: string, occurrenceId: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/scenarios/${scenarioId}/schedules/${scheduleId}/occurrences/${occurrenceId}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

	const resolveScenarioScheduleOccurrence = useCallback(
		(scenarioId: string, scheduleId: string, occurrenceId: string, action: 'retry' | 'catch_up' | 'skip', expectedRevision?: number, reason?: string, options?: ApiCallOptions) =>
			request<ScenarioScheduleOccurrenceResolutionResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/occurrences/${occurrenceId}/resolve`, withCallOptions({
				method: 'POST',
				body: JSON.stringify({ action, expected_revision: expectedRevision, reason: reason || '' }),
			}, options)),
		[],
	)

  const getScheduleHealth = useCallback((options?: ApiCallOptions) => request<ScenarioScheduleHealth>('/schedules/health', withCallOptions(undefined, options)), [])

  const getRunCalendar = useCallback(
    (params: RunCalendarParams, options?: ApiCallOptions) => request<RunCalendarResponse>(`/run-calendar${buildQuery(params)}`, withCallOptions(undefined, options)),
    [],
  )

  const approveScenarioScheduleApproval = useCallback(
    (scenarioId: string, scheduleId: string, approvalId: string, reason?: string, options?: ApiCallOptions) =>
      request<ScenarioScheduleApprovalResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/approvals/${approvalId}/approve`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ reason: reason || '' }),
      }, options)),
    [],
  )

  const rejectScenarioScheduleApproval = useCallback(
    (scenarioId: string, scheduleId: string, approvalId: string, reason?: string, options?: ApiCallOptions) =>
      request<ScenarioScheduleApprovalResponse>(`/scenarios/${scenarioId}/schedules/${scheduleId}/approvals/${approvalId}/reject`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ reason: reason || '' }),
      }, options)),
    [],
  )

  const moveTest = useCallback(
    (id: string, folderId: string | null, options?: ApiCallOptions) =>
      request<MoveTestResponse>(`/tests/${id}/move`, withCallOptions({ method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }, options)),
    [],
  )

  const getMetrics = useCallback((id: string, params?: MetricsParams, options?: ApiCallOptions) => {
    const suffix = buildQuery({ groups: params?.groups, since: params?.since, limit: params?.limit })
    return request<TestMetricsResponse>(`/tests/${id}/metrics${suffix}`, withCallOptions(undefined, options))
  }, [])

  const getTestErrorEvidence = useCallback(
    (id: string, params: LiveErrorEvidenceParams, options?: ApiCallOptions) =>
      request<LiveErrorEvidenceResponse>(`/tests/${id}/errors/evidence${buildQuery({
        signature: params.signature,
        action: params.action,
        limit: params.limit,
      })}`, withCallOptions(undefined, options)),
    [],
  )

  const getTestManifest = useCallback((id: string, options?: ApiCallOptions) => request<TestRunManifest>(`/tests/${id}/manifest`, withCallOptions(undefined, options)), [])

  const getTestGroupRuntimeSettings = useCallback(
    (id: string, group: string, options?: ApiCallOptions) =>
      request<TestGroupRuntimeSettingsView>(`/tests/${id}/groups/${encodeURIComponent(group)}/runtime-settings`, withCallOptions(undefined, options)),
    [],
  )

  const updateTestGroupRuntimeSettings = useCallback(
    (id: string, group: string, data: { runtime_settings: RuntimeSettingsOverride; fields: string[]; expected_revision: string }, options?: ApiCallOptions) =>
      request<TestGroupRuntimeSettingsUpdateResult>(`/tests/${id}/groups/${encodeURIComponent(group)}/runtime-settings`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify(data),
      }, options)),
    [],
  )

  const getTestGroupParameters = useCallback(
    (id: string, group: string, options?: ApiCallOptions) =>
      request<TestGroupParametersView>(`/tests/${id}/groups/${encodeURIComponent(group)}/parameters`, withCallOptions(undefined, options)),
    [],
  )

  const updateTestGroupParameters = useCallback(
    (id: string, group: string, data: { parameters: ScriptParameterDefinition[]; expected_revision: string }, options?: ApiCallOptions) =>
      request<TestGroupParametersUpdateResult>(`/tests/${id}/groups/${encodeURIComponent(group)}/parameters`, withCallOptions({
        method: 'PUT',
        body: JSON.stringify(data),
      }, options)),
    [],
  )

  const getTestArchiveManifest = useCallback((id: string, options?: ApiCallOptions) => request<RunArchiveManifest>(`/tests/${id}/archive/manifest`, withCallOptions(undefined, options)), [])

  const verifyTestArchive = useCallback(
    (id: string, options?: ApiCallOptions) => request<RunArchiveIntegrityReport>(`/tests/${id}/archive/verify`, withCallOptions(undefined, options)),
    [],
  )

  const getTestArchiveTransactions = useCallback(
    (id: string, params?: ArchiveTransactionsParams, options?: ApiCallOptions) => {
      const suffix = buildQuery({
        limit: params?.limit,
        offset: params?.offset,
        action: params?.action,
        population: params?.population,
        region: params?.region,
        network_profile: params?.network_profile,
        source_transaction: params?.source_transaction,
        hierarchy: params?.hierarchy,
        phase: params?.phase,
        has_detail: params?.has_detail,
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
      return request<ArchiveTransactionsResponse>(`/tests/${id}/archive/transactions${suffix}`, withCallOptions(undefined, options))
    },
    [],
  )

  const getTestArchiveResources = useCallback(
    (id: string, params?: ArchiveListParams, options?: ApiCallOptions) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset, sample_limit: params?.sample_limit })
      return request<ArchiveResourcesResponse>(`/tests/${id}/archive/resources${suffix}`, withCallOptions(undefined, options))
    },
    [],
  )

  const getTestArchiveSummaries = useCallback(
    (id: string, params?: ArchiveListParams, options?: ApiCallOptions) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset, sample_limit: params?.sample_limit, action: params?.action, groups: params?.groups })
      return request<ArchiveSummariesResponse>(`/tests/${id}/archive/summaries${suffix}`, withCallOptions(undefined, options))
    },
    [],
  )

  const getTestArchiveJourneys = useCallback(
    (id: string, params?: { policy?: string; revision?: number }, options?: ApiCallOptions) =>
      request<JourneyRiskReport>(`/tests/${encodeURIComponent(id)}/archive/journeys${buildQuery({ policy: params?.policy || undefined, revision: params?.revision || undefined })}`, withCallOptions(undefined, options)),
    [],
  )

  const getTestArchiveReport = useCallback((id: string, options?: ApiCallOptions & { includeReleaseTrend?: boolean; comparisonId?: string }) => request<RunArchiveReport>(`/tests/${id}/archive/report${buildQuery({ include_release_trend: options?.includeReleaseTrend, comparison_id: options?.comparisonId })}`, withCallOptions(undefined, options)), [])

  const getTestArchiveReports = useCallback(
    (ids: string[], projectId: string, options?: ApiCallOptions) =>
      request<ArchiveReportsBatchResponse>(`/tests/archive/reports${buildQuery({ project_id: projectId })}`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ ids }),
      }, options)),
    [],
  )

  // Root-cause v2: the latest completed assessment, one analysis, and a
  // request for a new one.
  const getRootCauseAssessment = useCallback(
    (id: string, options?: ApiCallOptions) =>
      request<RcaAnalysisResponse>(`/tests/${encodeURIComponent(id)}/archive/root-cause/v2`, withCallOptions(undefined, options)),
    [],
  )

  const getRootCauseAnalysis = useCallback(
    (id: string, analysisId: string, options?: ApiCallOptions) =>
      request<RcaAnalysisResponse>(`/tests/${encodeURIComponent(id)}/archive/root-cause/analyses/${encodeURIComponent(analysisId)}`, withCallOptions(undefined, options)),
    [],
  )

  const requestRootCauseAnalysis = useCallback(
    (id: string, body: { enrich?: boolean; baseline_test_id?: string } = {}, options?: ApiCallOptions) =>
      request<RcaAnalysisResponse>(`/tests/${encodeURIComponent(id)}/archive/root-cause/analyses`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const getRootCauseCritiques = useCallback(
    (id: string, analysisId: string, options?: ApiCallOptions) =>
      request<{ critiques: RcaCritique[] }>(`/tests/${encodeURIComponent(id)}/archive/root-cause/analyses/${encodeURIComponent(analysisId)}/critiques`, withCallOptions(undefined, options)),
    [],
  )

  // getRootCauseWindowLinks opens one time window of a run in each of its
  // trace providers that has a window link template.
  const getRootCauseWindowLinks = useCallback(
    (id: string, from: string, to: string, options?: ApiCallOptions) =>
      request<RcaWindowLinks>(`/tests/${encodeURIComponent(id)}/archive/root-cause/links${buildQuery({ from, to })}`, withCallOptions(undefined, options)),
    [],
  )

  const getTestArchiveExemplars = useCallback(
    (id: string, query: { name?: string; group?: string; population?: string; from?: string; to?: string; limit?: number; offset?: number } = {}, options?: ApiCallOptions) =>
      request<ExemplarPage>(`/tests/${encodeURIComponent(id)}/archive/exemplars${buildQuery(query)}`, withCallOptions(undefined, options)),
    [],
  )

  const getTestReportAnnotations = useCallback(
    (id: string, profile: string, options?: ApiCallOptions) =>
      request<ReportAnnotationsResponse>(`/tests/${id}/report/annotations${buildQuery({ profile })}`, withCallOptions(undefined, options)),
    [],
  )

  const saveTestReportAnnotations = useCallback(
    (id: string, profile: string, data: ReportAnnotationsInput, options?: ApiCallOptions) =>
      request<ReportAnnotationsResponse>(`/tests/${id}/report/annotations${buildQuery({ profile })}`, withCallOptions({
        method: 'PUT',
        body: JSON.stringify(data),
      }, options)),
    [],
  )

  const listTestNotes = useCallback((id: string, options?: ApiCallOptions) => request<TestNotesResponse>(`/tests/${id}/notes`, withCallOptions(undefined, options)), [])

  const createTestNote = useCallback(
    (id: string, body: string, options?: ApiCallOptions) =>
      request<TestNote>(`/tests/${id}/notes`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ body }),
      }, options)),
    [],
  )

  const updateTestNote = useCallback(
    (id: string, noteId: string, body: string, options?: ApiCallOptions) =>
      request<TestNote>(`/tests/${id}/notes/${noteId}`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify({ body }),
      }, options)),
    [],
  )

  const deleteTestNote = useCallback(
    (id: string, noteId: string, options?: ApiCallOptions) =>
      request<DeleteTestNoteResponse>(`/tests/${id}/notes/${noteId}`, withCallOptions({
        method: 'DELETE',
      }, options)),
    [],
  )

  const downloadTestReport = useCallback(
    (id: string, profile: string, options?: ApiCallOptions & { comparisonId?: string }) => fetchBlob(`/tests/${id}/report${buildQuery({ profile, comparison_id: options?.comparisonId })}`, options),
    [],
  )

  const publishTestToPowerBI = useCallback(
    (id: string, options?: { force?: boolean }, callOptions?: ApiCallOptions) =>
      request<PowerBIPublishResponse>(`/tests/${id}/publish/powerbi`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ force: Boolean(options?.force) }),
      }, callOptions)),
    [],
  )

  const getPowerBIPublishStatus = useCallback(
    (id: string, options?: ApiCallOptions) => request<PowerBIPublishStatusResponse>(`/tests/${id}/publish/powerbi`, withCallOptions(undefined, options)),
    [],
  )

  const downloadTestReportFormat = useCallback(
    (id: string, profile: string, format: 'pdf' | 'csv' | 'docx' | 'analysis_zip', options?: ApiCallOptions & { comparisonId?: string }) =>
      fetchBlob(`/tests/${id}/archive/report${buildQuery({ format, profile, comparison_id: options?.comparisonId })}`, options),
    [],
  )

  const getTestArchiveAnalysis = useCallback(
    (id: string, params?: ArchiveAnalysisParams, options?: ApiCallOptions) => {
      const suffix = buildQuery({
        baseline_id: params?.baseline_id,
        limit: params?.limit,
        offset: params?.offset,
        action: params?.action,
        population: params?.population,
        region: params?.region,
        network_profile: params?.network_profile,
        source_transaction: params?.source_transaction,
        hierarchy: params?.hierarchy,
        phase: params?.phase,
        has_detail: params?.has_detail,
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
      return request<RunArchiveAnalysisResponse>(`/tests/${id}/archive/analysis${suffix}`, withCallOptions(undefined, options))
    },
    [],
  )

  const downloadTestArchive = useCallback((id: string, options?: ApiCallOptions) => fetchBlob(`/tests/${id}/archive`, options), [])

  const downloadTestArchiveComparison = useCallback(
    (id: string, baselineId: string, breakdown: 'overall' | 'platform' | 'engine' | 'device', action?: string, options?: ApiCallOptions) =>
      fetchBlob(`/tests/${id}/archive/comparison${buildQuery({ format: 'csv', baseline_id: baselineId, breakdown, action })}`, options),
    [],
  )

  const downloadTestArchiveMultiComparison = useCallback(
    (baselineId: string, comparisonIds: string[], breakdown: 'overall' | 'platform' | 'engine' | 'device', action?: string, options?: ApiCallOptions) =>
      fetchBlob(`/tests/${baselineId}/archive/comparison${buildQuery({ format: 'csv', comparison_ids: comparisonIds.join(','), breakdown, action })}`, options),
    [],
  )

  const downloadTestArchiveBrowserDiagnostics = useCallback(
    (id: string, options?: ApiCallOptions) => fetchBlob(`/tests/${id}/archive/browser-diagnostics`, options),
    [],
  )

  const downloadTestArchiveArtifact = useCallback(
    (id: string, name: string, options?: ApiCallOptions) => fetchBlob(`/tests/${id}/archive/files/${encodeURIComponent(name)}`, options),
    [],
  )

  const scaleVUsers = useCallback(
    (testId: string, group: string, targetVUsers: number, options?: ScaleVUsersOptions, callOptions?: ApiCallOptions) =>
      request<ScaleVUsersResponse>(`/tests/${testId}/vusers`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify({ group, target_vusers: targetVUsers, action: options?.action, ramp_duration: options?.ramp_duration }),
      }, callOptions)),
    [],
  )

  const updateGoalTarget = useCallback(
    (testId: string, group: string, targetTps: number, options?: ApiCallOptions) =>
      request<GoalTargetUpdateResponse>(`/tests/${testId}/goal-targets`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify({ group, target_tps: targetTps }),
      }, options)),
    [],
  )

  const adjustDuration = useCallback(
    (testId: string, duration: string, options?: ApiCallOptions) =>
      request<AdjustDurationResponse>(`/tests/${testId}/duration`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify({ duration }),
      }, options)),
    [],
  )

  const previewRuntimeAdjustment = useCallback(
    (testId: string, payload: RuntimeAdjustmentRequest, options?: ApiCallOptions) =>
      request<RuntimeAdjustmentPreview>(`/tests/${testId}/runtime-adjustments/preview`, withCallOptions({
        method: 'POST', body: JSON.stringify(payload),
      }, options)),
    [],
  )

  const applyRuntimeAdjustment = useCallback(
    (testId: string, payload: RuntimeAdjustmentRequest, options?: ApiCallOptions) =>
      request<RuntimeAdjustmentPreview>(`/tests/${testId}/runtime-adjustments`, withCallOptions({
        method: 'PATCH', body: JSON.stringify(payload),
      }, options)),
    [],
  )

  return useMemo(
    () => ({
      listTests,
      getTest,
      createTest,
      createTestKeyed,
      estimateTest,
      preflightTest,
      getLoadModelSupportMatrix,
      listLoadModelTemplates,
      validateScenarioLoadModel,
      getScenarioValidationContext,
      sizeScenarioFromGoal,
      getTrafficProfileSources,
      analyzeTrafficProfile,
      deriveTrafficProfile,
      analyzeCSVDriven,
      deriveCSVDriven,
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
      getTestGroupRuntimeSettings,
      updateTestGroupRuntimeSettings,
      getTestGroupParameters,
      updateTestGroupParameters,
      getTestArchiveManifest,
      verifyTestArchive,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveJourneys,
      getTestArchiveReport,
      getTestArchiveReports,
      getTestReportAnnotations,
      saveTestReportAnnotations,
      getRootCauseAssessment,
      getRootCauseAnalysis,
      requestRootCauseAnalysis,
      getTestArchiveExemplars,
      getRootCauseCritiques,
      getRootCauseWindowLinks,
      listTestNotes,
      createTestNote,
      updateTestNote,
      deleteTestNote,
      downloadTestReport,
      downloadTestReportFormat,
      publishTestToPowerBI,
      getPowerBIPublishStatus,
      getTestArchiveAnalysis,
      downloadTestArchive,
      downloadTestArchiveComparison,
      downloadTestArchiveMultiComparison,
      downloadTestArchiveBrowserDiagnostics,
      downloadTestArchiveArtifact,
      scaleVUsers,
      updateGoalTarget,
      adjustDuration,
      previewRuntimeAdjustment,
      applyRuntimeAdjustment,
      pauseTest,
      resumeTest,
      createRemoteRun,
      getRemoteRun,
      controlRemoteRun,
      listScenarioSchedules,
      createScenarioSchedule,
      updateScenarioSchedule,
      deleteScenarioSchedule,
      previewScenarioSchedule,
      listScenarioScheduleOccurrences,
      listScenarioScheduleApprovals,
      deleteScenarioScheduleOccurrence,
		  resolveScenarioScheduleOccurrence,
      getScheduleHealth,
      getRunCalendar,
      approveScenarioScheduleApproval,
      rejectScenarioScheduleApproval,
    }),
    [
      listTests,
      getTest,
      createTest,
      createTestKeyed,
      estimateTest,
      preflightTest,
      getLoadModelSupportMatrix,
      listLoadModelTemplates,
      validateScenarioLoadModel,
      getScenarioValidationContext,
      sizeScenarioFromGoal,
      getTrafficProfileSources,
      analyzeTrafficProfile,
      deriveTrafficProfile,
      analyzeCSVDriven,
      deriveCSVDriven,
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
      previewScenarioSchedule,
      listScenarioScheduleOccurrences,
      listScenarioScheduleApprovals,
      deleteScenarioScheduleOccurrence,
		  resolveScenarioScheduleOccurrence,
      getScheduleHealth,
      getRunCalendar,
      approveScenarioScheduleApproval,
      rejectScenarioScheduleApproval,
      moveTest,
      getMetrics,
      getTestErrorEvidence,
      getTestManifest,
      getTestGroupRuntimeSettings,
      updateTestGroupRuntimeSettings,
      getTestGroupParameters,
      updateTestGroupParameters,
      getTestArchiveManifest,
      verifyTestArchive,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveJourneys,
      getTestArchiveReport,
      getTestArchiveReports,
      getTestReportAnnotations,
      saveTestReportAnnotations,
      getRootCauseAssessment,
      getRootCauseAnalysis,
      requestRootCauseAnalysis,
      getTestArchiveExemplars,
      getRootCauseCritiques,
      getRootCauseWindowLinks,
      listTestNotes,
      createTestNote,
      updateTestNote,
      deleteTestNote,
      downloadTestReport,
      downloadTestReportFormat,
      publishTestToPowerBI,
      getPowerBIPublishStatus,
      getTestArchiveAnalysis,
      downloadTestArchive,
      downloadTestArchiveComparison,
      downloadTestArchiveMultiComparison,
      downloadTestArchiveBrowserDiagnostics,
      downloadTestArchiveArtifact,
      scaleVUsers,
      updateGoalTarget,
      adjustDuration,
      previewRuntimeAdjustment,
      applyRuntimeAdjustment,
    ],
  )
}
