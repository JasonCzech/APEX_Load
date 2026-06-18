import { useCallback, useMemo } from 'react'

import {
  EntitlementsResponse,
  EntitlementsUsageResponse,
  ProjectEntitlementsUsageResponse,
} from '../../types'
import { request } from '../apiClient'

export function useEntitlementsApi() {
  const getEntitlements = useCallback(
    () => request<EntitlementsResponse>('/system/entitlements'),
    [],
  )

  const getEntitlementsUsage = useCallback(
    () => request<EntitlementsUsageResponse>('/system/entitlements/usage'),
    [],
  )

  const getProjectEntitlementsUsage = useCallback(
    (projectId: string) =>
      request<ProjectEntitlementsUsageResponse>(`/projects/${projectId}/entitlements/usage`),
    [],
  )

  return useMemo(
    () => ({ getEntitlements, getEntitlementsUsage, getProjectEntitlementsUsage }),
    [getEntitlements, getEntitlementsUsage, getProjectEntitlementsUsage],
  )
}
