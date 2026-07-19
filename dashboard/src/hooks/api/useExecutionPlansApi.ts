import { useCallback, useMemo } from 'react'

import {
  ExecutionPlan,
  ExecutionPlanApproval,
  ExecutionPlanApprovalListResponse,
  ExecutionPlanListResponse,
  ExecutionPlanRun,
  ExecutionPlanRunListResponse,
  ExecutionPlanRunResponse,
  ExecutionPlanSchedule,
  ExecutionPlanScheduleListResponse,
  ExecutionPlanScheduleOccurrenceListResponse,
  ExecutionPlanScheduleRequest,
	ScheduleOccurrenceResolutionRequest,
} from '../../types'
import { request } from '../apiClient'

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
    (params?: { project_id?: string; limit?: number; offset?: number }) =>
      request<ExecutionPlanListResponse>(`/execution-plans${buildQuery({ ...params })}`),
    [],
  )

  const getExecutionPlan = useCallback(
    (id: string) => request<ExecutionPlan>(`/execution-plans/${id}`),
    [],
  )

  const createExecutionPlan = useCallback(
    (body: Partial<ExecutionPlan>) =>
      request<ExecutionPlan>('/execution-plans', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateExecutionPlan = useCallback(
    (id: string, body: Partial<ExecutionPlan>) =>
      request<ExecutionPlan>(`/execution-plans/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteExecutionPlan = useCallback(
    (id: string) => request<{ deleted: string }>(`/execution-plans/${id}`, { method: 'DELETE' }),
    [],
  )

  const runExecutionPlan = useCallback(
    (id: string) => request<ExecutionPlanRunResponse>(`/execution-plans/${id}/runs`, { method: 'POST' }),
    [],
  )

  const listExecutionPlanRuns = useCallback(
    (id: string, params?: { limit?: number; offset?: number }) =>
      request<ExecutionPlanRunListResponse>(`/execution-plans/${id}/runs${buildQuery({ ...params })}`),
    [],
  )

  const getExecutionPlanRun = useCallback(
    (id: string, runId: string) =>
      request<ExecutionPlanRun>(`/execution-plans/${id}/runs/${runId}`),
    [],
  )

  // Recurring plan schedules (cron-materialized plan runs).
  const listExecutionPlanSchedules = useCallback(
    (planId: string) =>
      request<ExecutionPlanScheduleListResponse>(`/execution-plans/${planId}/schedules`),
    [],
  )

  const createExecutionPlanSchedule = useCallback(
    (planId: string, body: ExecutionPlanScheduleRequest) =>
      request<ExecutionPlanSchedule>(`/execution-plans/${planId}/schedules`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const updateExecutionPlanSchedule = useCallback(
    (planId: string, scheduleId: string, body: ExecutionPlanScheduleRequest) =>
      request<ExecutionPlanSchedule>(`/execution-plans/${planId}/schedules/${scheduleId}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    [],
  )

  const deleteExecutionPlanSchedule = useCallback(
    (planId: string, scheduleId: string) =>
      request<{ deleted: string }>(`/execution-plans/${planId}/schedules/${scheduleId}`, {
        method: 'DELETE',
      }),
    [],
  )

  const listExecutionPlanScheduleOccurrences = useCallback(
    (planId: string, scheduleId: string) =>
      request<ExecutionPlanScheduleOccurrenceListResponse>(
        `/execution-plans/${planId}/schedules/${scheduleId}/occurrences`,
      ),
    [],
  )

	const resolveExecutionPlanScheduleOccurrence = useCallback(
		(planId: string, scheduleId: string, occurrenceId: string, body: ScheduleOccurrenceResolutionRequest) =>
			request<{ occurrence: import('../../types').ExecutionPlanScheduleOccurrence; schedule: ExecutionPlanSchedule }>(
				`/execution-plans/${planId}/schedules/${scheduleId}/occurrences/${occurrenceId}/resolve`,
				{ method: 'POST', body: JSON.stringify(body) },
			),
		[],
	)

  // Governance approvals (quota-gated plan runs).
  const listExecutionPlanApprovals = useCallback(
    (planId: string) =>
      request<ExecutionPlanApprovalListResponse>(`/execution-plans/${planId}/approvals`),
    [],
  )

  const approveExecutionPlanApproval = useCallback(
    (planId: string, approvalId: string, reason?: string) =>
      request<ExecutionPlanApproval>(
        `/execution-plans/${planId}/approvals/${approvalId}/approve`,
        { method: 'POST', body: JSON.stringify({ reason: reason || '' }) },
      ),
    [],
  )

  const rejectExecutionPlanApproval = useCallback(
    (planId: string, approvalId: string, reason?: string) =>
      request<ExecutionPlanApproval>(
        `/execution-plans/${planId}/approvals/${approvalId}/reject`,
        { method: 'POST', body: JSON.stringify({ reason: reason || '' }) },
      ),
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
      listExecutionPlanSchedules,
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
      listExecutionPlanSchedules,
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
