import { useCallback, useMemo } from 'react'

import {
  FleetGeneratorMetadata,
  FleetGeneratorMetadataListResponse,
  FleetPool,
  FleetPoolListResponse,
  FleetProvisioningRequest,
  FleetProvisioningRequestListResponse,
  ProvisionerInfo,
} from '../../types/fleet'
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
    (params?: { limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<FleetPoolListResponse>(`/fleet/pools${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getFleetPool = useCallback((id: string, options?: ApiCallOptions) => request<FleetPool>(`/fleet/pools/${id}`, withCallOptions(undefined, options)), [])

  const createFleetPool = useCallback(
    (body: Partial<FleetPool>, options?: ApiCallOptions) =>
      request<FleetPool>('/fleet/pools', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const updateFleetPool = useCallback(
    (id: string, body: Partial<FleetPool>, options?: ApiCallOptions) =>
      request<FleetPool>(`/fleet/pools/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)),
    [],
  )

  const deleteFleetPool = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/fleet/pools/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const listGeneratorMetadata = useCallback(
    (params?: { limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<FleetGeneratorMetadataListResponse>(`/fleet/generators${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const patchGeneratorMetadata = useCallback(
    (generatorId: string, patch: GeneratorMetadataPatch, options?: ApiCallOptions) =>
      request<FleetGeneratorMetadata>(`/fleet/generators/${generatorId}`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify(patch),
      }, options)),
    [],
  )

  const cordonGenerator = useCallback(
    (generatorId: string, options?: ApiCallOptions) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/cordon`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const uncordonGenerator = useCallback(
    (generatorId: string, options?: ApiCallOptions) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/uncordon`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const drainGenerator = useCallback(
    (generatorId: string, options?: ApiCallOptions) =>
      request<GeneratorLifecycleResponse>(`/fleet/generators/${generatorId}/drain`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const createProvisionRequest = useCallback(
    (body: { fleet_pool_id: string; desired_count: number; reason?: string; project_id?: string }, options?: ApiCallOptions) =>
      request<FleetProvisioningRequest>('/fleet/provision-requests', withCallOptions({
        method: 'POST',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const listProvisionRequests = useCallback(
    (params?: { fleet_pool_id?: string; limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<FleetProvisioningRequestListResponse>(`/fleet/provision-requests${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getProvisionRequest = useCallback(
    (id: string, options?: ApiCallOptions) => request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}`, withCallOptions(undefined, options)),
    [],
  )

  const cancelProvisionRequest = useCallback(
    (id: string, options?: ApiCallOptions) =>
      request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}/cancel`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const retryProvisionRequest = useCallback(
    (id: string, options?: ApiCallOptions) =>
      request<FleetProvisioningRequest>(`/fleet/provision-requests/${id}/retry`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const getProvisioner = useCallback((options?: ApiCallOptions) => request<ProvisionerInfo>('/fleet/provisioner', withCallOptions(undefined, options)), [])

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
