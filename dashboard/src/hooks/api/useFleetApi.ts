import { useCallback, useMemo } from 'react'

import {
  FleetGeneratorMetadata,
  FleetGeneratorMetadataListResponse,
  FleetPool,
  FleetPoolListResponse,
  FleetProvisioningRequest,
  FleetProvisioningRequestListResponse,
  ProvisionerInfo,
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

export type GeneratorMetadataPatch = {
  target?: string
  fleet_pool_id?: string
  region?: string
  provider_id?: string
  provider_status?: string
  cordoned?: boolean
  tags?: Record<string, string>
}

export type GeneratorLifecycleResponse = {
  generator_id?: string
  target?: string
  drain_state: string
  cordoned: boolean
}

export function useFleetApi() {
  const listFleetPools = useCallback(
    (params?: { limit?: number; offset?: number }) =>
      request<FleetPoolListResponse>(`/fleet/pools${buildQuery({ ...params })}`),
    [],
  )

  const getFleetPool = useCallback((id: string) => request<FleetPool>(`/fleet/pools/${id}`), [])

  const createFleetPool = useCallback(
    (body: Partial<FleetPool>) =>
      request<FleetPool>('/fleet/pools', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateFleetPool = useCallback(
    (id: string, body: Partial<FleetPool>) =>
      request<FleetPool>(`/fleet/pools/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteFleetPool = useCallback(
    (id: string) => request<{ deleted: string }>(`/fleet/pools/${id}`, { method: 'DELETE' }),
    [],
  )

  const listGeneratorMetadata = useCallback(
    (params?: { limit?: number; offset?: number }) =>
      request<FleetGeneratorMetadataListResponse>(`/fleet/generators${buildQuery({ ...params })}`),
    [],
  )

  const patchGeneratorMetadata = useCallback(
    (generatorId: string, patch: GeneratorMetadataPatch) =>
      request<FleetGeneratorMetadata>(`/fleet/generators/${generatorId}`, {
        method: 'PATCH',
        body: JSON.stringify(patch),
      }),
    [],
  )

  const cordonGenerator = useCallback(
    (generatorId: string) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/cordon`, { method: 'POST' }),
    [],
  )

  const uncordonGenerator = useCallback(
    (generatorId: string) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/uncordon`, { method: 'POST' }),
    [],
  )

  const drainGenerator = useCallback(
    (generatorId: string) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/drain`, { method: 'POST' }),
    [],
  )

  const createProvisionRequest = useCallback(
    (body: { fleet_pool_id: string; desired_count: number; reason?: string; project_id?: string }) =>
      request<FleetProvisioningRequest>('/fleet/provision-requests', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const listProvisionRequests = useCallback(
    (params?: { fleet_pool_id?: string; limit?: number; offset?: number }) =>
      request<FleetProvisioningRequestListResponse>(`/fleet/provision-requests${buildQuery({ ...params })}`),
    [],
  )

  const getProvisionRequest = useCallback(
    (id: string) => request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}`),
    [],
  )

  const cancelProvisionRequest = useCallback(
    (id: string) =>
      request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}/cancel`, { method: 'POST' }),
    [],
  )

  const retryProvisionRequest = useCallback(
    (id: string) =>
      request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}/retry`, { method: 'POST' }),
    [],
  )

  const getProvisioner = useCallback(() => request<ProvisionerInfo>('/fleet/provisioner'), [])

  return useMemo(
    () => ({
      listFleetPools,
      getFleetPool,
      createFleetPool,
      updateFleetPool,
      deleteFleetPool,
      listGeneratorMetadata,
      patchGeneratorMetadata,
      cordonGenerator,
      uncordonGenerator,
      drainGenerator,
      createProvisionRequest,
      listProvisionRequests,
      getProvisionRequest,
      cancelProvisionRequest,
      retryProvisionRequest,
      getProvisioner,
    }),
    [listFleetPools, getFleetPool, createFleetPool, updateFleetPool, deleteFleetPool, listGeneratorMetadata, patchGeneratorMetadata, cordonGenerator, uncordonGenerator, drainGenerator, createProvisionRequest, listProvisionRequests, getProvisionRequest, cancelProvisionRequest, retryProvisionRequest, getProvisioner],
  )
}
