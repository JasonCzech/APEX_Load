import { useCallback, useMemo } from 'react'

import {
  ClusterCapacityResponse,
  ClusterConnection,
  ClusterConnectionInput,
  ClusterConnectionListResponse,
  ClusterTestResult,
  PoolAllocationInput,
  PoolAllocationResponse,
} from '../../types/cluster'
import { request } from '../apiClient'

/**
 * Cluster registry + pool allocation transport. Every route here is system-admin
 * only server-side; the frontend gating in the cards is UX, not the boundary.
 */
export function useClustersApi() {
  const listClusters = useCallback(
    () => request<ClusterConnectionListResponse>('/system/clusters'),
    [],
  )

  const getCluster = useCallback(
    (id: string) => request<ClusterConnection>(`/system/clusters/${encodeURIComponent(id)}`),
    [],
  )

  const createCluster = useCallback(
    (body: ClusterConnectionInput) =>
      request<ClusterConnection>('/system/clusters', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateCluster = useCallback(
    (id: string, body: ClusterConnectionInput) =>
      request<ClusterConnection>(`/system/clusters/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    [],
  )

  const deleteCluster = useCallback(
    (id: string) => request<void>(`/system/clusters/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    [],
  )

  /**
   * Tests the STORED configuration, so it is only offered on a saved
   * connection. Testing an unsaved draft would require sending the plaintext
   * secret in the request body, which the write-only credential design forbids.
   */
  const testCluster = useCallback(
    (id: string) =>
      request<ClusterTestResult>(`/system/clusters/${encodeURIComponent(id)}/test`, { method: 'POST' }),
    [],
  )

  const getClusterCapacity = useCallback(
    (id: string) =>
      request<ClusterCapacityResponse>(`/system/clusters/${encodeURIComponent(id)}/capacity`),
    [],
  )

  const updatePoolAllocation = useCallback(
    (poolId: string, body: PoolAllocationInput) =>
      request<PoolAllocationResponse>(`/fleet/pools/${encodeURIComponent(poolId)}/allocation`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    [],
  )

  const getPoolAllocationPreflight = useCallback(
    (poolId: string, clusterId?: string) =>
      request<PoolAllocationResponse>(
        `/fleet/pools/${encodeURIComponent(poolId)}/allocation/preflight${
          clusterId ? `?cluster_id=${encodeURIComponent(clusterId)}` : ''
        }`,
      ),
    [],
  )

  return useMemo(
    () => ({
      listClusters,
      getCluster,
      createCluster,
      updateCluster,
      deleteCluster,
      testCluster,
      getClusterCapacity,
      updatePoolAllocation,
      getPoolAllocationPreflight,
    }),
    [
      listClusters,
      getCluster,
      createCluster,
      updateCluster,
      deleteCluster,
      testCluster,
      getClusterCapacity,
      updatePoolAllocation,
      getPoolAllocationPreflight,
    ],
  )
}
