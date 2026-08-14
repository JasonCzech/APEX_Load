import { useCallback, useMemo } from 'react'

import type {
  CloudConnection,
  CloudConnectionInput,
  CloudConnectionTestResult,
  CloudResourceInventory,
  ExecutionTarget,
  ExecutionTargetCapacity,
  ExecutionTargetInput,
  ExecutionTargetTestResult,
} from '../../types/compute'
import { request } from '../apiClient'

export function useComputeApi() {
  const listCloudConnections = useCallback(() =>
    request<{ cloud_connections: CloudConnection[]; total: number }>('/system/cloud-connections'), [])
  const createCloudConnection = useCallback((body: CloudConnectionInput) =>
    request<CloudConnection>('/system/cloud-connections', { method: 'POST', body: JSON.stringify(body) }), [])
  const updateCloudConnection = useCallback((id: string, body: CloudConnectionInput) =>
    request<CloudConnection>(`/system/cloud-connections/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }), [])
  const deleteCloudConnection = useCallback((id: string) =>
    request<void>(`/system/cloud-connections/${encodeURIComponent(id)}`, { method: 'DELETE' }), [])
  const testCloudConnection = useCallback((id: string) =>
    request<CloudConnectionTestResult>(`/system/cloud-connections/${encodeURIComponent(id)}/test`, { method: 'POST' }), [])
  const listCloudConnectionResources = useCallback((id: string) =>
    request<CloudResourceInventory>(`/system/cloud-connections/${encodeURIComponent(id)}/resources`), [])

  const listExecutionTargets = useCallback(() =>
    request<{ execution_targets: ExecutionTarget[]; total: number }>('/system/execution-targets'), [])
  const createExecutionTarget = useCallback((body: ExecutionTargetInput) =>
    request<ExecutionTarget>('/system/execution-targets', { method: 'POST', body: JSON.stringify(body) }), [])
  const updateExecutionTarget = useCallback((id: string, body: ExecutionTargetInput) =>
    request<ExecutionTarget>(`/system/execution-targets/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }), [])
  const deleteExecutionTarget = useCallback((id: string) =>
    request<void>(`/system/execution-targets/${encodeURIComponent(id)}`, { method: 'DELETE' }), [])
  const testExecutionTarget = useCallback((id: string) =>
    request<ExecutionTargetTestResult>(`/system/execution-targets/${encodeURIComponent(id)}/test`, { method: 'POST' }), [])
  const getExecutionTargetCapacity = useCallback((id: string) =>
    request<ExecutionTargetCapacity>(`/system/execution-targets/${encodeURIComponent(id)}/capacity`), [])

  return useMemo(() => ({
    listCloudConnections,
    createCloudConnection,
    updateCloudConnection,
    deleteCloudConnection,
    testCloudConnection,
    listCloudConnectionResources,
    listExecutionTargets,
    createExecutionTarget,
    updateExecutionTarget,
    deleteExecutionTarget,
    testExecutionTarget,
    getExecutionTargetCapacity,
  }), [
    listCloudConnections, createCloudConnection, updateCloudConnection, deleteCloudConnection,
    testCloudConnection, listCloudConnectionResources,
    listExecutionTargets, createExecutionTarget, updateExecutionTarget, deleteExecutionTarget, testExecutionTarget,
    getExecutionTargetCapacity,
  ])
}
