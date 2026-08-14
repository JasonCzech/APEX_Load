import { useCallback, useMemo } from 'react'

import {
  FleetGeneratorMetadata,
  FleetGeneratorMetadataListResponse,
  FleetPool,
  FleetPoolListResponse,
  FleetProvisioningRequest,
  FleetProvisioningInstanceListResponse,
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

export type GeneratorEvacuationResponse = GeneratorLifecycleResponse & {
  evacuated_tests?: string[]
  replacement_targets?: string[]
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

  const evacuateGenerator = useCallback(
    (generatorId: string, options?: ApiCallOptions) =>
      request<GeneratorEvacuationResponse>(`/fleet/generators/${generatorId}/evacuate`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const listLoadZones = useCallback(
    (options?: ApiCallOptions) => request<{ load_zones: Array<{ region?: string; zone?: string; generators?: number }> }>('/fleet/load-zones', withCallOptions(undefined, options)),
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

  const listProvisionRequestInstances = useCallback(
    (id: string, options?: ApiCallOptions) => request<FleetProvisioningInstanceListResponse>(`/fleet/provision-requests/${id}/instances`, withCallOptions(undefined, options)),
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
      evacuateGenerator,
      listLoadZones,
      createProvisionRequest,
      listProvisionRequests,
      getProvisionRequest,
      listProvisionRequestInstances,
      cancelProvisionRequest,
      retryProvisionRequest,
      getProvisioner,
    }),
    [listFleetPools, getFleetPool, createFleetPool, updateFleetPool, deleteFleetPool, listGeneratorMetadata, patchGeneratorMetadata, cordonGenerator, uncordonGenerator, drainGenerator, evacuateGenerator, listLoadZones, createProvisionRequest, listProvisionRequests, getProvisionRequest, listProvisionRequestInstances, cancelProvisionRequest, retryProvisionRequest, getProvisioner],
  )
}
