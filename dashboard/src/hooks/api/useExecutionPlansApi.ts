import { useCallback, useMemo } from 'react'

import {
  ExecutionPlan,
  ExecutionPlanListResponse,
  ExecutionPlanRunResponse,
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

  return useMemo(
    () => ({
      listExecutionPlans,
      getExecutionPlan,
      createExecutionPlan,
      updateExecutionPlan,
      deleteExecutionPlan,
      runExecutionPlan,
    }),
    [listExecutionPlans, getExecutionPlan, createExecutionPlan, updateExecutionPlan, deleteExecutionPlan, runExecutionPlan],
  )
}
