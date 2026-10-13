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

  /** Tests the STORED configuration and records the result as the connection's health. */
  const testCluster = useCallback(
    (id: string) =>
      request<ClusterTestResult>(`/system/clusters/${encodeURIComponent(id)}/test`, { method: 'POST' }),
    [],
  )

  /**
   * Tests a connection as its form describes it, before it is saved. The
   * credentials travel as they do on save (write-only: never returned), and
   * nothing is stored, health included. An edit names its stored connection,
   * whose credentials stand in for any left blank.
   */
  const testClusterDraft = useCallback(
    (body: ClusterConnectionInput, connectionId?: string) =>
      request<ClusterTestResult>('/system/clusters/test', {
        method: 'POST',
        body: JSON.stringify({ ...(connectionId ? { connection_id: connectionId } : {}), connection: body }),
      }),
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
      testClusterDraft,
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
      testClusterDraft,
      getClusterCapacity,
      updatePoolAllocation,
      getPoolAllocationPreflight,
    ],
  )
}
