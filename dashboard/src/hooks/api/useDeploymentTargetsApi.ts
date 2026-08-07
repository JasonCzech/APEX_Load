import { useCallback, useMemo } from 'react'

import {
  DomainDeploymentTarget,
  DomainDeploymentTargetInput,
  DomainDeploymentTargetListResponse,
} from '../../types/deploymentTarget'
import { request } from '../apiClient'

/**
 * Per-domain deployment-target transport.
 *
 * Every route here is system-admin only server-side, reads included; the
 * frontend gating in the card is UX, not the boundary.
 *
 * The routes live under /system/ rather than /domains/{id}/... deliberately —
 * see handlers_domain_deployment_target.go for the RBAC reasoning.
 */
export function useDeploymentTargetsApi() {
  const listDeploymentTargets = useCallback(
    () => request<DomainDeploymentTargetListResponse>('/system/deployment-targets'),
    [],
  )

  const getDeploymentTarget = useCallback(
    (domainId: string) =>
      request<DomainDeploymentTarget>(`/system/deployment-targets/${encodeURIComponent(domainId)}`),
    [],
  )

  const updateDeploymentTarget = useCallback(
    (domainId: string, body: DomainDeploymentTargetInput) =>
      request<DomainDeploymentTarget>(`/system/deployment-targets/${encodeURIComponent(domainId)}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    [],
  )

  /** Clears the constraint. Always permitted: removing a constraint can only
   *  widen what a pool may do, so it cannot strand an existing pool. */
  const clearDeploymentTarget = useCallback(
    (domainId: string) =>
      request<DomainDeploymentTarget>(`/system/deployment-targets/${encodeURIComponent(domainId)}`, {
        method: 'DELETE',
      }),
    [],
  )

  return useMemo(
    () => ({
      listDeploymentTargets,
      getDeploymentTarget,
      updateDeploymentTarget,
      clearDeploymentTarget,
    }),
    [listDeploymentTargets, getDeploymentTarget, updateDeploymentTarget, clearDeploymentTarget],
  )
}
