import { useCallback, useMemo } from 'react'

import type {
  ExecutionPlan,
  ExecutionPlanApproval,
  ExecutionPlanApprovalListResponse,
  ExecutionPlanListResponse,
  ExecutionPlanRunListResponse,
  ExecutionPlanRunResponse,
  ExecutionPlanRunView,
  ExecutionPlanSchedule,
  ExecutionPlanScheduleListResponse,
  ExecutionPlanScheduleOccurrenceListResponse,
  ExecutionPlanScheduleRequest,
  ScheduleOccurrenceResolutionRequest,
} from '../../types/executionPlans'
import type { SchedulePreviewResponse } from '../../types/executionPlans'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

function buildQuery(params: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

export function useExecutionPlansApi() {
  const listExecutionPlans = useCallback(
    (params?: { project_id?: string; limit?: number; offset?: number; include_activity?: 'true' }, options?: ApiCallOptions) =>
      request<ExecutionPlanListResponse>(`/execution-plans${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getExecutionPlan = useCallback(
    (id: string, options?: ApiCallOptions) => request<ExecutionPlan>(`/execution-plans/${id}`, withCallOptions(undefined, options)),
    [],
  )

  const createExecutionPlan = useCallback(
    (body: Partial<ExecutionPlan>, options?: ApiCallOptions) =>
      request<ExecutionPlan>('/execution-plans', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const updateExecutionPlan = useCallback(
    (id: string, body: Partial<ExecutionPlan>, options?: ApiCallOptions) =>
      request<ExecutionPlan>(`/execution-plans/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)),
    [],
  )

  const deleteExecutionPlan = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/execution-plans/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const runExecutionPlan = useCallback(
    (id: string, options?: ApiCallOptions) => request<ExecutionPlanRunResponse>(`/execution-plans/${id}/runs`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const listExecutionPlanRuns = useCallback(
    (id: string, params?: { limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<ExecutionPlanRunListResponse>(`/execution-plans/${id}/runs${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getExecutionPlanRun = useCallback(
    (id: string, runId: string, options?: ApiCallOptions) =>
      request<ExecutionPlanRunView>(`/execution-plans/${id}/runs/${runId}`, withCallOptions(undefined, options)),
    [],
  )

  const stopExecutionPlanRun = useCallback(
    (id: string, runId: string, options?: ApiCallOptions) =>
      request<{ run?: ExecutionPlanRunView; stop_requested: boolean; error?: string }>(
        `/execution-plans/${id}/runs/${runId}/stop`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  // Recurring plan schedules (cron-materialized plan runs).
  const listExecutionPlanSchedules = useCallback(
    (planId: string, options?: ApiCallOptions) =>
      request<ExecutionPlanScheduleListResponse>(`/execution-plans/${planId}/schedules`, withCallOptions(undefined, options)),
    [],
  )

  const previewExecutionPlanSchedule = useCallback(
    (planId: string, body: ExecutionPlanScheduleRequest, count = 5, options?: ApiCallOptions) =>
      request<SchedulePreviewResponse>(`/execution-plans/${planId}/schedules/preview?count=${count}`, withCallOptions({
        method: 'POST',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const createExecutionPlanSchedule = useCallback(
    (planId: string, body: ExecutionPlanScheduleRequest, options?: ApiCallOptions) =>
      request<ExecutionPlanSchedule>(`/execution-plans/${planId}/schedules`, withCallOptions({
        method: 'POST',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const updateExecutionPlanSchedule = useCallback(
    (planId: string, scheduleId: string, body: ExecutionPlanScheduleRequest, options?: ApiCallOptions) =>
      request<ExecutionPlanSchedule>(`/execution-plans/${planId}/schedules/${scheduleId}`, withCallOptions({
        method: 'PUT',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const deleteExecutionPlanSchedule = useCallback(
    (planId: string, scheduleId: string, options?: ApiCallOptions) =>
      request<{ deleted: string }>(`/execution-plans/${planId}/schedules/${scheduleId}`, withCallOptions({
        method: 'DELETE',
      }, options)),
    [],
  )

  const listExecutionPlanScheduleOccurrences = useCallback(
    (planId: string, scheduleId: string, options?: ApiCallOptions) =>
      request<ExecutionPlanScheduleOccurrenceListResponse>(
        `/execution-plans/${planId}/schedules/${scheduleId}/occurrences`, withCallOptions(undefined, options)),
    [],
  )

	const resolveExecutionPlanScheduleOccurrence = useCallback(
		(planId: string, scheduleId: string, occurrenceId: string, body: ScheduleOccurrenceResolutionRequest, options?: ApiCallOptions) =>
			request<{ occurrence: import('../../types').ExecutionPlanScheduleOccurrence; schedule: ExecutionPlanSchedule }>(
				`/execution-plans/${planId}/schedules/${scheduleId}/occurrences/${occurrenceId}/resolve`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
		[],
	)

  // Governance approvals (quota-gated plan runs).
  const listExecutionPlanApprovals = useCallback(
    (planId: string, options?: ApiCallOptions) =>
      request<ExecutionPlanApprovalListResponse>(`/execution-plans/${planId}/approvals`, withCallOptions(undefined, options)),
    [],
  )

  const approveExecutionPlanApproval = useCallback(
    (planId: string, approvalId: string, reason?: string, options?: ApiCallOptions) =>
      request<ExecutionPlanApproval>(
        `/execution-plans/${planId}/approvals/${approvalId}/approve`, withCallOptions({ method: 'POST', body: JSON.stringify({ reason: reason || '' }) }, options)),
    [],
  )

  const rejectExecutionPlanApproval = useCallback(
    (planId: string, approvalId: string, reason?: string, options?: ApiCallOptions) =>
      request<ExecutionPlanApproval>(
        `/execution-plans/${planId}/approvals/${approvalId}/reject`, withCallOptions({ method: 'POST', body: JSON.stringify({ reason: reason || '' }) }, options)),
    [],
  )

  return useMemo(
    () => ({
      listExecutionPlans,
      getExecutionPlan,
      createExecutionPlan,
      updateExecutionPlan,
      deleteExecutionPlan,
      runExecutionPlan,
      listExecutionPlanRuns,
      getExecutionPlanRun,
      stopExecutionPlanRun,
      listExecutionPlanSchedules,
      previewExecutionPlanSchedule,
      createExecutionPlanSchedule,
      updateExecutionPlanSchedule,
      deleteExecutionPlanSchedule,
      listExecutionPlanScheduleOccurrences,
		  resolveExecutionPlanScheduleOccurrence,
      listExecutionPlanApprovals,
      approveExecutionPlanApproval,
      rejectExecutionPlanApproval,
    }),
    [
      listExecutionPlans,
      getExecutionPlan,
      createExecutionPlan,
      updateExecutionPlan,
      deleteExecutionPlan,
      runExecutionPlan,
      listExecutionPlanRuns,
      getExecutionPlanRun,
      stopExecutionPlanRun,
      listExecutionPlanSchedules,
      previewExecutionPlanSchedule,
      createExecutionPlanSchedule,
      updateExecutionPlanSchedule,
      deleteExecutionPlanSchedule,
      listExecutionPlanScheduleOccurrences,
		  resolveExecutionPlanScheduleOccurrence,
      listExecutionPlanApprovals,
      approveExecutionPlanApproval,
      rejectExecutionPlanApproval,
    ],
  )
}
